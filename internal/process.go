package internal

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
)

type processHandle struct {
	cmd     *exec.Cmd
	streams *streamRouter
}

func environment(extra map[string]string) []string {
	env := slices.Clone(os.Environ())
	if len(extra) == 0 {
		return env
	}
	idx := make(map[string]int, len(env))
	for i, kv := range env {
		if j := strings.IndexByte(kv, '='); j >= 0 {
			idx[kv[:j]] = i
		}
	}
	for k, v := range extra {
		p := k + "=" + v
		if i, ok := idx[k]; ok {
			env[i] = p
		} else {
			idx[k] = len(env)
			env = append(env, p)
		}
	}
	return env
}

func buildCommand(config *Config) *exec.Cmd {

	if config.Umask == 0 {
		return exec.Command(config.Cmd[0], config.Cmd[1:]...)
	}

	umask := fmt.Sprintf("%o", config.Umask)

	args := []string{"-c", "umask " + umask + " && exec \"$@\"", "--"}
	args = append(args, config.Cmd...)
	return exec.Command("/bin/sh", args...)
}

func privileged(config *Config, cmd *exec.Cmd) {

	uid := uint32(os.Geteuid())
	gid := uint32(os.Getegid())

	if config.Uid == nil {
		config.Uid = &uid
	}

	if config.Gid == nil {
		config.Gid = &gid
	}

	if gid == uint32(os.Getegid()) && uid == uint32(os.Geteuid()) {
		return
	}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}

	cmd.SysProcAttr.Credential = &syscall.Credential{
		Uid: *config.Uid,
		Gid: *config.Gid,
	}
}

func resolveOutputPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return "/dev/null"
	}
	return path
}

func startProcess(config *Config) (*processHandle, error) {

	cmd := buildCommand(config)

	privileged(config, cmd)

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	} else {
		cmd.SysProcAttr.Setpgid = true
	}

	cmd.Dir = config.Workingdir
	cmd.Env = environment(config.Env)

	router := newStreamRoute()
	var closers []io.Closer
	fail := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			_ = closers[i].Close()
		}
	}

	stdoutPath := resolveOutputPath(config.Stdout)
	stdoutFile, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fail()
		return nil, fmt.Errorf("open stdout %s: %w", stdoutPath, err)
	}
	closers = append(closers, stdoutFile)

	stderrPath := resolveOutputPath(config.Stderr)
	stderrFile := stdoutFile
	if stderrPath == stdoutPath {
		// Reuse the same file descriptor when both streams share a target.
	} else {
		stderrFile, err = os.OpenFile(stderrPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			fail()
			return nil, fmt.Errorf("open stderr %s: %w", stderrPath, err)
		}
		closers = append(closers, stderrFile)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		fail()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		fail()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	err = cmd.Start()
	if err != nil {
		fail()
		return nil, err
	}

	if stderrFile == stdoutFile {
		var closeShared sync.Once
		go func() {
			defer closeShared.Do(func() { _ = stdoutFile.Close() })
			_, _ = io.Copy(router.stdoutWrite(stdoutFile), stdoutPipe)
		}()
		go func() {
			defer closeShared.Do(func() { _ = stderrFile.Close() })
			_, _ = io.Copy(router.stderrWrite(stderrFile), stderrPipe)
		}()
	} else {
		go func() {
			_, _ = io.Copy(router.stdoutWrite(stdoutFile), stdoutPipe)
			_ = stdoutFile.Close()
		}()
		go func() {
			_, _ = io.Copy(router.stderrWrite(stderrFile), stderrPipe)
			_ = stderrFile.Close()
		}()
	}

	return &processHandle{cmd: cmd, streams: router}, nil
}
