package internal

import (
	"context"
	"fmt"
	"time"
)

type Lifecycle int

const (
	ActionStop Lifecycle = iota
	ActionRestart
)

type LifecycleEvent struct {
	Action   Lifecycle
	Pid      int
	ExitCode int
}

func Exit(logger *Logger, mgr *Manager, shutdownFunc context.CancelFunc, message string) {
	logger.LogMessage(LevelInfo, message)
	shutdownFunc()
	mgr.Shutdown()
	time.Sleep(500 * time.Microsecond)
	logger.Close()
}

func HotWire(
	control *ControlService,
	logger *Logger,
) error {
	if err := control.Reload(); err != nil {
		return fmt.Errorf("failed to reload config: %w", err)
	}

	logger.LogMessage(LevelInfo, "received reload signal (SIGHUP), reloading config")

	return nil
}
