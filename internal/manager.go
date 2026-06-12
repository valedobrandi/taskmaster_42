package internal

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

func getStopSignal(signalStr string) os.Signal {
	switch signalStr {
	case "HUP":
		return syscall.SIGHUP
	case "INT":
		return syscall.SIGINT
	case "QUIT":
		return syscall.SIGQUIT
	case "KILL":
		return syscall.SIGKILL
	case "USR1":
		return syscall.SIGUSR1
	case "USR2":
		return syscall.SIGUSR2
	case "TERM":
		fallthrough
	default:
		return syscall.SIGTERM // Default to standard graceful termination
	}
}

type command interface{}

type start struct {
	name  string
	reply chan error
}

type stop struct {
	name  string
	reply chan error
}

type status struct {
	reply chan []StatusReport
}

type running struct {
	reply chan []RunningInstance
}

type attach struct {
	name  string
	conn  net.Conn
	reply chan attachResult
}

type attachResult struct {
	done <-chan struct{}
	err  error
}

type load struct {
	configs map[string]*Config
	reply   chan error
}

type reload struct {
	configs map[string]*Config
	reply   chan error
}

type finished struct {
	name    string
	runtime *Runtime
}

type shutdown struct {
	reply chan struct{}
}

type Manager struct {
	cmd     chan command
	updates chan ProcessUpdate
	ctx     context.Context
	wait    sync.WaitGroup
	logger  *Logger
}

type Instance struct {
	spec    atomic.Pointer[Config]
	runtime *Runtime
	epoch   uint64
	state   ProcessInstance
}

type ProcessInstance struct {
	Status    Status
	Pid       int
	ExitCode  int
	LastStart time.Time
}

type StatusReport struct {
	Name     string
	Status   Status
	Pid      int
	ExitCode int
	Uptime   string
}

func CreateManager(ctx context.Context) *Manager {
	m := &Manager{
		cmd:     make(chan command, 100),
		updates: make(chan ProcessUpdate, 100),
		ctx:     ctx,
	}
	go m.eventLoop()
	return m
}

func (m *Manager) SetLogger(logger *Logger) {
	m.logger = logger
}

// RunningInstance is a read-only snapshot of a RUNNING process.
type RunningInstance struct {
	Name     string
	Priority string // "low", "medium", or "high"
}

func isConfigChanged(prevConfig, newConfig *Config) bool {
	if prevConfig == nil || newConfig == nil {
		return true
	}

	// Only check fields that require process restart
	if len(prevConfig.Cmd) != len(newConfig.Cmd) {
		return true
	}

	for i := range prevConfig.Cmd {
		if prevConfig.Cmd[i] != newConfig.Cmd[i] {
			return true
		}
	}

	if prevConfig.Workingdir != newConfig.Workingdir {
		return true
	}

	if prevConfig.Umask != newConfig.Umask {
		return true
	}

	if (prevConfig.Uid == nil) != (newConfig.Uid == nil) || (prevConfig.Uid != nil && *prevConfig.Uid != *newConfig.Uid) {
		return true
	}

	if (prevConfig.Gid == nil) != (newConfig.Gid == nil) || (prevConfig.Gid != nil && *prevConfig.Gid != *newConfig.Gid) {
		return true
	}

	if len(prevConfig.Env) != len(newConfig.Env) {
		return true
	}

	for k, v := range prevConfig.Env {
		if newConfig.Env[k] != v {
			return true
		}
	}

	if prevConfig.Stdout != newConfig.Stdout || prevConfig.Stderr != newConfig.Stderr {
		return true
	}

	return false
}

func (m *Manager) eventLoop() {
	instances := make(map[string]*Instance)

	for {
		select {

		case update := <-m.updates:
			if inst, exists := instances[update.Name]; exists {
				if update.Epoch != inst.epoch {
					break
				}
				inst.state.Status = update.Status
				inst.state.Pid = update.Pid
				inst.state.ExitCode = update.ExitCode
				if update.Status == RUNNING && !update.EventTime.IsZero() {
					inst.state.LastStart = update.EventTime
				}
			}
			if m.logger != nil {
				m.logger.Log(update)
			}
		case cmd := <-m.cmd:
			switch c := cmd.(type) {
			case start:
				inst, exist := instances[c.name]
				if !exist {
					c.reply <- fmt.Errorf("process %s not found in manager registry", c.name)
					break
				}
				if inst.runtime != nil {
					c.reply <- fmt.Errorf("process %s is already running", c.name)
					break
				}
				m.initSupervise(c.name, inst)
				c.reply <- nil
			case stop:
				inst, exist := instances[c.name]
				if !exist {
					c.reply <- fmt.Errorf("process %s not found in manager registry", c.name)
					break
				}
				rt := inst.runtime
				inst.runtime = nil
				c.reply <- nil
				stopWaitRuntime(rt)
			case status:
				reports := make([]StatusReport, 0, len(instances))
				for name, inst := range instances {
					reports = append(reports, StatusReport{
						Name:     name,
						Status:   inst.state.Status,
						Pid:      inst.state.Pid,
						ExitCode: inst.state.ExitCode,
						Uptime:   formatUptime(inst.state.LastStart),
					})
				}
				c.reply <- reports
			case running:
				var running []RunningInstance
				for name, inst := range instances {
					spec := inst.spec.Load()
					if spec == nil {
						continue
					}
					if inst.state.Status == RUNNING {
						running = append(running, RunningInstance{
							Name:     name,
							Priority: spec.MemoryPriority,
						})
					}
				}
				c.reply <- running
			case attach:
				inst, exist := instances[c.name]
				if !exist {
					c.reply <- attachResult{err: fmt.Errorf("process %s not found in manager registry", c.name)}
					break
				}
				if inst.runtime == nil || inst.state.Status != RUNNING {
					c.reply <- attachResult{err: fmt.Errorf("process %s is not running", c.name)}
					break
				}
				if err := inst.runtime.streams.stdout.Attach(c.conn); err != nil {
					c.reply <- attachResult{err: err}
					break
				}
				if err := inst.runtime.streams.stderr.Attach(c.conn); err != nil {
					inst.runtime.streams.stdout.Detach(c.conn)
					c.reply <- attachResult{err: err}
					break
				}
				done := make(chan struct{})
				rt := inst.runtime
				go func() {
					defer close(done)
					_, _ = io.Copy(io.Discard, c.conn)
					rt.streams.stdout.Detach(c.conn)
					rt.streams.stderr.Detach(c.conn)
				}()
				c.reply <- attachResult{done: done}
			case load:
				var autostart []string
				for name, spec := range c.configs {
					inst := &Instance{state: ProcessInstance{Status: STOPPED}}
					inst.spec.Store(spec)
					instances[name] = inst
					if spec.Autostart {
						autostart = append(autostart, name)
					}
				}
				c.reply <- nil
				for _, name := range autostart {
					m.initSupervise(name, instances[name])
				}
			case reload:
				sequence := Restarting(instances, c.configs)

				var toStop []*Runtime
				var toStart []string

				for _, name := range sequence.Update {
					instances[name].spec.Store(c.configs[name])
				}

				for _, name := range sequence.Stop {
					inst := instances[name]
					toStop = append(toStop, inst.runtime)
					inst.runtime = nil
					delete(instances, name)
				}

				for _, name := range sequence.Restart {
					prevInst := instances[name]
					toStop = append(toStop, prevInst.runtime)
					currInst := &Instance{
						state: ProcessInstance{Status: STOPPED},
					}
					currInst.spec.Store(c.configs[name])
					instances[name] = currInst
					if prevInst.runtime != nil {
						toStart = append(toStart, name)
					}
				}

				for _, name := range sequence.Start {
					currInst := &Instance{
						state: ProcessInstance{Status: STOPPED},
					}
					currInst.spec.Store(c.configs[name])
					instances[name] = currInst

					if c.configs[name].Autostart {
						toStart = append(toStart, name)
					}
				}

				c.reply <- nil

				for _, rt := range toStop {
					stopWaitRuntime(rt)
				}

				for _, name := range toStart {
					m.initSupervise(name, instances[name])
				}

			case finished:
				inst, exist := instances[c.name]
				if !exist {
					break
				}
				if inst.runtime == c.runtime {
					inst.runtime = nil
				}

			case shutdown:
				c.reply <- struct{}{}
				return
			}
		}

	}
}

func stopWaitRuntime(rt *Runtime) {
	if rt != nil {
		rt.stop()
		<-rt.done
	}
}

func (m *Manager) initSupervise(name string, inst *Instance) {
	rt := newRuntime(m.ctx)
	inst.runtime = rt
	inst.epoch++

	m.wait.Add(1)
	tracker := &UpdateTracker{name: name, epoch: inst.epoch, updates: m.updates}
	logger := m.logger
	go func() {
		supervise(rt, name, &inst.spec, tracker, logger)
		select {
		case m.cmd <- finished{name: name, runtime: rt}:
		default:
		}
		close(rt.done)
		m.wait.Done()
	}()
}

func (m *Manager) GetRunningInstances() []RunningInstance {
	reply := make(chan []RunningInstance, 1)
	m.cmd <- running{reply: reply}
	return <-reply
}

func (m *Manager) Start(name string) error {
	reply := make(chan error, 1)
	m.cmd <- start{name: name, reply: reply}
	return <-reply
}

func (m *Manager) Stop(name string) error {
	reply := make(chan error, 1)
	m.cmd <- stop{name: name, reply: reply}
	return <-reply
}

func (m *Manager) Restart(name string) error {
	if err := m.Stop(name); err != nil {
		return err
	}

	return m.Start(name)
}

func (m *Manager) Status() []StatusReport {
	reply := make(chan []StatusReport, 1)
	m.cmd <- status{reply: reply}
	return <-reply
}

func (m *Manager) Load(specs map[string]*Config) error {
	reply := make(chan error, 1)
	m.cmd <- load{configs: specs, reply: reply}
	return <-reply
}

func (m *Manager) Reload(newConfigs map[string]*Config) error {
	reply := make(chan error, 1)
	m.cmd <- reload{configs: newConfigs, reply: reply}
	return <-reply
}

func (m *Manager) Attach(name string, conn net.Conn) (<-chan struct{}, error) {
	reply := make(chan attachResult, 1)
	m.cmd <- attach{name: name, conn: conn, reply: reply}
	result := <-reply
	return result.done, result.err
}

func (m *Manager) Shutdown() {
	reply := make(chan struct{}, 1)
	m.cmd <- shutdown{reply: reply}
	<-reply
	m.wait.Wait()
	close(m.updates)
}

func formatUptime(startTime time.Time) string {
	if startTime.IsZero() {
		return "0s"
	}
	duration := time.Since(startTime)
	hours := int(duration.Hours())
	minutes := int(duration.Minutes()) % 60
	seconds := int(duration.Seconds()) % 60

	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}
