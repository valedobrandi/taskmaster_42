package internal

import (
	"context"
	"net"
	"sync/atomic"
)

type ControlService struct {
	mgr         *Manager
	configPath  string
	memGuardCfg *atomic.Pointer[MemoryGuardConfig]
	exitRoot    context.CancelFunc
}

func NewControlService(
	mgr *Manager,
	configPath string,
	memGuardCfg *atomic.Pointer[MemoryGuardConfig],
	exitRoot context.CancelFunc,
) *ControlService {
	return &ControlService{
		mgr:         mgr,
		configPath:  configPath,
		memGuardCfg: memGuardCfg,
		exitRoot:    exitRoot,
	}
}

func (s *ControlService) Status() []StatusReport {
	return s.mgr.Status()
}

func (s *ControlService) Start(name string) error {
	return s.mgr.Start(name)
}

func (s *ControlService) Stop(name string) error {
	return s.mgr.Stop(name)
}

func (s *ControlService) Restart(name string) error {
	return s.mgr.Restart(name)
}

func (s *ControlService) Attach(name string, conn net.Conn) (<-chan struct{}, error) {
	return s.mgr.Attach(name, conn)
}

func (s *ControlService) MemoryGuardCfg() *atomic.Pointer[MemoryGuardConfig] {
	return s.memGuardCfg
}

func (s *ControlService) Reload() error {
	cfg, memGuard, err := LoadConfig(s.configPath)
	if err != nil {
		return err
	}
	if err := s.mgr.Reload(cfg); err != nil {
		return err
	}
	if s.memGuardCfg != nil {
		s.memGuardCfg.Store(&memGuard)
	}
	return nil
}

func (s *ControlService) Shutdown() {
	if s.exitRoot != nil {
		s.exitRoot()
	}
}

func (s *ControlService) MemoryGuardStatus() map[string]any {
	if s.memGuardCfg == nil {
		return map[string]any{
			"enabled":   false,
			"threshold": 0.0,
			"interval":  0,
		}
	}

	cfg := s.memGuardCfg.Load()
	if cfg == nil {
		return map[string]any{
			"enabled":   false,
			"threshold": 0.0,
			"interval":  0,
		}
	}

	return map[string]any{
		"enabled":   cfg.Enabled,
		"threshold": cfg.Threshold,
		"interval":  cfg.Interval,
	}
}
