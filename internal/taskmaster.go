package internal

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"sync/atomic"
	"time"
)

// DefaultBackoffDelay is the sleep duration used before retrying after a backoff or restart.
const DefaultBackoffDelay = time.Second

type Status string

const (
	STARTING Status = "starting"
	RUNNING  Status = "running"
	STOPPED  Status = "stopped"
	FATAL    Status = "fatal"
	BACKOFF  Status = "backoff"
)

// AutorestartPolicy defines the possible values for the Autorestart configuration field.
type AutorestartPolicy string

const (
	AutorestartAlways     AutorestartPolicy = "always"
	AutorestartNever      AutorestartPolicy = "never"
	AutorestartUnexpected AutorestartPolicy = "unexpected"
)

type ProcessUpdate struct {
	Name      string
	Epoch     uint64
	Status    Status
	Pid       int
	ExitCode  int
	EventTime time.Time
}

type UpdateTracker struct {
	name    string
	epoch   uint64
	updates chan<- ProcessUpdate
}

type processInfo struct {
	runtime *Runtime
	spec    *atomic.Pointer[Config]
	tracker *UpdateTracker
}

func (p *processInfo) currentSpec() *Config {
	return p.spec.Load()
}

func (p *processInfo) stopSignal() os.Signal {
	return getStopSignal(p.currentSpec().Stopsignal)
}

func (p *processInfo) stopTimeout() time.Duration {
	return time.Duration(p.currentSpec().Stoptime) * time.Second
}

func (t *UpdateTracker) Emit(status Status, pid int, exitCode int) {
	update := ProcessUpdate{
		Name:      t.name,
		Epoch:     t.epoch,
		Status:    status,
		Pid:       pid,
		ExitCode:  exitCode,
		EventTime: time.Now(),
	}
	select {
	case t.updates <- update:
	default:
		// Channel full or consumer unavailable; discard to avoid blocking the supervisor
	}
}

func getExitCode(err error) int {
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		return -1
	}
	return 0
}

func stopProcess(cmd *exec.Cmd, stopSignal os.Signal, stopTimeout time.Duration, exitCh <-chan error) int {
	_ = cmd.Process.Signal(stopSignal)

	var err error
	select {
	case err = <-exitCh:
	case <-time.After(stopTimeout):
		_ = cmd.Process.Kill()
		err = <-exitCh
	}
	return getExitCode(err)
}

func restartPolicy(exitCode int, spec *Config) bool {
	policy := AutorestartPolicy(spec.Autorestart)
	switch policy {
	case AutorestartAlways:
		return true
	case AutorestartNever:
		return false
	case AutorestartUnexpected:
		return !slices.Contains(spec.Exitcodes, exitCode)
	}
	return false
}

func tryStartProcess(spec *Config, tracker *UpdateTracker) (*processHandle, chan error, error) {
	handle, err := startProcess(spec)
	if err != nil {
		// Process start retry failed
		tracker.Emit(FATAL, 0, -1)
		return nil, nil, err
	}

	exitCh := make(chan error, 1)

	go func(c *exec.Cmd) {
		exitCh <- c.Wait()
	}(handle.cmd)

	return handle, exitCh, nil
}

func waitForStartup(processInfo *processInfo, name string, logger *Logger) (*processHandle, chan error, int, bool) {
	var handle *processHandle
	var err error
	var exitCh chan error
	var pid int
	started := false

	spec := processInfo.currentSpec()
	if spec == nil {
		return nil, nil, 0, false
	}

	for attempt := 0; attempt <= spec.Startretries; attempt++ {
		// Emit starting status
		processInfo.tracker.Emit(STARTING, 0, 0)

		spec = processInfo.currentSpec()

		// Attempt process start
		handle, exitCh, err = tryStartProcess(spec, processInfo.tracker)

		if err != nil {
			continue
		}

		processInfo.runtime.streams = handle.streams
		pid = handle.cmd.Process.Pid
		if logger != nil {
			logger.LogMessage(LevelInfo, fmt.Sprintf("spawned: '%s' with pid %d", name, pid))
		}

		if spec.Starttime <= 0 {
			processInfo.tracker.Emit(RUNNING, pid, 0)
			started = true
			break
		}

		// Validate process startup within window
		startupWindow := time.Duration(spec.Starttime) * time.Second
		timer := time.NewTimer(startupWindow)

		select {
		case <-processInfo.runtime.ctx.Done():
			// Context cancelled during startup validation
			exitCode := stopProcess(handle.cmd, processInfo.stopSignal(), processInfo.stopTimeout(), exitCh)
			processInfo.tracker.Emit(STOPPED, pid, exitCode)
			timer.Stop()
			return nil, nil, 0, false

		case err = <-exitCh:
			// Process exited during startup validation
			timer.Stop()
			exitCode := getExitCode(err)
			processInfo.tracker.Emit(FATAL, pid, exitCode)
			if restartPolicy(exitCode, processInfo.currentSpec()) {
				processInfo.tracker.Emit(BACKOFF, pid, exitCode)
				time.Sleep(DefaultBackoffDelay)
				continue
			}
			return nil, nil, 0, false

		case <-timer.C:
			// Startup validation passed
			processInfo.tracker.Emit(RUNNING, pid, 0)
			started = true
		}

		if started {
			break
		}
	}

	if !started {
		// All startup retries exhausted
		if logger != nil {
			logger.LogMessage(LevelCritical, fmt.Sprintf("process '%s' failed to start after %d attempts", name, spec.Startretries))
		}
		return nil, nil, 0, false
	}

	return handle, exitCh, pid, true
}

func monitorRuntime(processInfo *processInfo, cmd *exec.Cmd, exitCh chan error, pid int) LifecycleEvent {
	select {
	case <-processInfo.runtime.ctx.Done():
		// Context cancelled during runtime
		exitCode := stopProcess(cmd, processInfo.stopSignal(), processInfo.stopTimeout(), exitCh)
		processInfo.tracker.Emit(STOPPED, pid, exitCode)
		return LifecycleEvent{
			Action:   ActionStop,
			Pid:      pid,
			ExitCode: exitCode,
		}

	case err := <-exitCh:
		// Process exited during runtime
		exitCode := getExitCode(err)

		processInfo.tracker.Emit(STOPPED, pid, exitCode)

		if restartPolicy(exitCode, processInfo.currentSpec()) {

			processInfo.tracker.Emit(BACKOFF, pid, exitCode)

			return LifecycleEvent{
				Action:   ActionRestart,
				Pid:      pid,
				ExitCode: exitCode,
			}
		}
		return LifecycleEvent{
			Action:   ActionStop,
			Pid:      pid,
			ExitCode: exitCode,
		}
	}
}

func supervise(runtime *Runtime, name string, spec *atomic.Pointer[Config], tracker *UpdateTracker, logger *Logger) {

	processInfo := &processInfo{
		runtime: runtime,
		spec:    spec,
		tracker: tracker,
	}

	for {
		handle, exitCh, pid, ok := waitForStartup(processInfo, name, logger)
		if !ok {
			return
		}

		result := monitorRuntime(processInfo, handle.cmd, exitCh, pid)

		switch result.Action {
		case ActionStop:
			return
		case ActionRestart:
			if logger != nil {
				logger.LogMessage(LevelInfo, fmt.Sprintf("restarting: '%s'", name))
			}
			time.Sleep(DefaultBackoffDelay)
		}
	}
}
