package helpers

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"taskmaster/test_taskmaster/config"
)

// ProcStatus holds the parsed state of a supervised process.
type ProcStatus struct {
	Name     string
	State    string
	PID      int
	ExitCode int
}

// TestContext carries all runtime paths and the live daemon handle.
type TestContext struct {
	Root       string
	ConfigPath string
	BackupPath string
	DaemonPath string
	CtlPath    string
	LogPath    string
	SocketPath string
	Daemon     *exec.Cmd
}

// StartDaemon launches the daemon binary and attaches log files in the OS temp dir.
func StartDaemon(ctx *TestContext) error {
	cmd := exec.Command(ctx.DaemonPath)
	cmd.Dir = ctx.Root

	stdout, err := os.Create(filepath.Join(os.TempDir(), config.DaemonStdout))
	if err != nil {
		return err
	}
	stderr, err := os.Create(filepath.Join(os.TempDir(), config.DaemonStderr))
	if err != nil {
		stdout.Close()
		return err
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		stdout.Close()
		stderr.Close()
		return err
	}
	ctx.Daemon = cmd
	return nil
}

// StopDaemon sends SIGTERM to the daemon and waits for it to exit.
func StopDaemon(ctx *TestContext) {
	if ctx.Daemon != nil && ctx.Daemon.Process != nil {
		_ = ctx.Daemon.Process.Signal(syscall.SIGTERM)
		_, _ = ctx.Daemon.Process.Wait()
		ctx.Daemon = nil
	}
	// Fallback: kill any stray daemon processes.
	exec.Command("pkill", "-f", config.DaemonBin).Run()
	waitForDaemonExit(ctx.SocketPath, 5*time.Second)
}

// WaitForCtlReady polls until taskmasterctl responds successfully.
func WaitForCtlReady(ctx *TestContext, timeout time.Duration) error {
	_, err := WaitForStatus(ctx, timeout, func(_ map[string]ProcStatus) bool { return true })
	return err
}

// WaitForStatus polls status until predicate returns true or timeout elapses.
func WaitForStatus(ctx *TestContext, timeout time.Duration, predicate func(map[string]ProcStatus) bool) (map[string]ProcStatus, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		out, err := RunCtl(ctx, "status")
		if err == nil {
			st := ParseStatus(out)
			if predicate(st) {
				return st, nil
			}
		} else {
			lastErr = err
		}
		time.Sleep(config.StatusPollInterval)
	}
	if lastErr == nil {
		lastErr = errors.New("timeout waiting for expected status")
	}
	return nil, lastErr
}

// RunCtl sends a single command line to taskmasterctl and returns combined output.
func RunCtl(ctx *TestContext, input string) (string, error) {
	cmd := exec.Command(ctx.CtlPath)
	cmd.Dir = ctx.Root
	cmd.Stdin = strings.NewReader(input + "\n")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

func waitForDaemonExit(socketPath string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !daemonProcessExists() {
			_ = os.Remove(socketPath)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = os.Remove(socketPath)
}

func daemonProcessExists() bool {
	return exec.Command("pgrep", "-f", config.DaemonBin).Run() == nil
}

// ParseStatus converts raw "status" output into a map keyed by process name.
func ParseStatus(out string) map[string]ProcStatus {
	result := map[string]ProcStatus{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "ok") ||
			strings.HasPrefix(line, "error") || strings.HasPrefix(line, "taskmaster") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		state := strings.ToLower(fields[1])
		result[name] = ProcStatus{
			Name:     name,
			State:    state,
			PID:      extractPID(line),
			ExitCode: extractExitCode(line),
		}
	}
	return result
}

func extractPID(line string) int {
	return extractInt(line, "pid ")
}

func extractExitCode(line string) int {
	return extractInt(line, "status ")
}

// extractInt finds a keyword in line and parses the integer that follows it.
func extractInt(line, keyword string) int {
	idx := strings.Index(line, keyword)
	if idx < 0 {
		return 0
	}
	tail := line[idx+len(keyword):]
	end := 0
	for end < len(tail) && tail[end] >= '0' && tail[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	v, _ := strconv.Atoi(tail[:end])
	return v
}
