package internal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestControlService_should_reload_config_when_reload_requested(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	configPath := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(configPath, []byte(`
memory_guard:
  enabled: true
  threshold: 80
  interval: 5
programs:
  worker:
    cmd: ["/bin/sh", "-c", "sleep 1"]
    numprocs: 1
    umask: 18
    autostart: false
    autorestart: "never"
    exitcodes: [0]
    startretries: 1
    starttime: 0
    stopsignal: "TERM"
    stoptime: 1
    stdout: ""
    stderr: ""
    env: {}
    memory_priority: "low"
`), 0644); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	initialConfigs, initialMemGuard, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load initial config: %v", err)
	}

	mgr := CreateManager(ctx)
	t.Cleanup(mgr.Shutdown)

	if err := mgr.Load(initialConfigs); err != nil {
		t.Fatalf("manager load initial config: %v", err)
	}

	var memGuardCfg atomic.Pointer[MemoryGuardConfig]
	memGuardCfg.Store(&initialMemGuard)

	service := NewControlService(mgr, configPath, &memGuardCfg, cancel)

	if err := os.WriteFile(configPath, []byte(`
memory_guard:
  enabled: true
  threshold: 65
  interval: 2
programs:
  worker:
    cmd: ["/bin/sh", "-c", "sleep 1"]
    numprocs: 2
    umask: 18
    autostart: false
    autorestart: "never"
    exitcodes: [0]
    startretries: 1
    starttime: 0
    stopsignal: "TERM"
    stoptime: 1
    stdout: ""
    stderr: ""
    env: {}
    memory_priority: "high"
`), 0644); err != nil {
		t.Fatalf("write updated config: %v", err)
	}

	if err := service.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	statuses := service.Status()
	if len(statuses) != 2 {
		t.Fatalf("expected 2 process instances after reload, got %d", len(statuses))
	}

	currentMemGuard := memGuardCfg.Load()
	if currentMemGuard == nil {
		t.Fatal("expected memory guard config to be present")
	}
	if currentMemGuard.Threshold != 65 {
		t.Fatalf("expected threshold 65, got %.1f", currentMemGuard.Threshold)
	}
	if currentMemGuard.Interval != 2 {
		t.Fatalf("expected interval 2, got %d", currentMemGuard.Interval)
	}
}

func TestControlService_should_return_memory_guard_status_when_requested(t *testing.T) {
	var memGuardCfg atomic.Pointer[MemoryGuardConfig]
	memGuardCfg.Store(&MemoryGuardConfig{
		Enabled:   true,
		Threshold: 72,
		Interval:  9,
	})

	service := NewControlService(CreateManager(context.Background()), "", &memGuardCfg, nil)
	defer service.mgr.Shutdown()

	status := service.MemoryGuardStatus()

	enabled, ok := status["enabled"].(bool)
	if !ok || !enabled {
		t.Fatalf("expected enabled=true, got %#v", status["enabled"])
	}
	threshold, ok := status["threshold"].(float64)
	if !ok || threshold != 72 {
		t.Fatalf("expected threshold=72, got %#v", status["threshold"])
	}
	interval, ok := status["interval"].(int)
	if !ok || interval != 9 {
		t.Fatalf("expected interval=9, got %#v", status["interval"])
	}
}

func TestServer_dispatch_should_return_shutdown_post_action_when_shutdown_requested(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	var memGuardCfg atomic.Pointer[MemoryGuardConfig]
	memGuardCfg.Store(&MemoryGuardConfig{})

	mgr := CreateManager(ctx)
	t.Cleanup(mgr.Shutdown)

	shutdownCalled := false
	service := NewControlService(mgr, "", &memGuardCfg, func() {
		shutdownCalled = true
		cancel()
	})
	server := &Server{control: service}

	resp, post := server.dispatch(RPCRequest{ID: 7, Method: "shutdown"})
	if resp.Error != "" {
		t.Fatalf("expected no error, got %q", resp.Error)
	}
	if resp.Result != "ok" {
		t.Fatalf("expected ok result, got %#v", resp.Result)
	}
	if post == nil {
		t.Fatal("expected shutdown post action")
	}

	post()

	if !shutdownCalled {
		t.Fatal("expected shutdown callback to be invoked")
	}
}

func TestServer_dispatch_should_return_memory_guard_status_when_requested(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	var memGuardCfg atomic.Pointer[MemoryGuardConfig]
	memGuardCfg.Store(&MemoryGuardConfig{
		Enabled:   true,
		Threshold: 55,
		Interval:  4,
	})

	mgr := CreateManager(ctx)
	t.Cleanup(mgr.Shutdown)

	service := NewControlService(mgr, "", &memGuardCfg, cancel)
	server := &Server{control: service}

	resp, _ := server.dispatch(RPCRequest{ID: 3, Method: "memory_guard_status"})
	if resp.Error != "" {
		t.Fatalf("expected no error, got %q", resp.Error)
	}

	payload, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}

	var status struct {
		Enabled   bool    `json:"enabled"`
		Threshold float64 `json:"threshold"`
		Interval  int     `json:"interval"`
	}
	if err := json.Unmarshal(payload, &status); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if !status.Enabled || status.Threshold != 55 || status.Interval != 4 {
		t.Fatalf("unexpected memory guard status: %+v", status)
	}
}
