package internal

import (
	"context"
	"testing"
	"time"
)

func TestManager_should_restart_running_process_when_config_changes_on_reload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := CreateManager(ctx)
	defer mgr.Shutdown()

	specs := map[string]*Config{
		"worker:00": {
			ProcessName:    "worker:00",
			Program:        "worker",
			Cmd:            []string{"/bin/sh", "-c", "sleep 30"},
			Numprocs:       1,
			Autostart:      true,
			Autorestart:    "never",
			Exitcodes:      []int{0},
			Startretries:   0,
			Starttime:      0,
			Stopsignal:     "TERM",
			Stoptime:       1,
			Env:            map[string]string{},
			MemoryPriority: "low",
		},
	}

	if err := mgr.Load(specs); err != nil {
		t.Fatalf("load: %v", err)
	}

	initial := waitForStatusReport(t, mgr, 3*time.Second, func(report StatusReport) bool {
		return report.Name == "worker:00" && report.Status == RUNNING && report.Pid > 0
	})

	reloaded := map[string]*Config{
		"worker:00": {
			ProcessName:    "worker:00",
			Program:        "worker",
			Cmd:            []string{"/bin/sh", "-c", "sleep 30"},
			Numprocs:       1,
			Autostart:      true,
			Autorestart:    "never",
			Exitcodes:      []int{0},
			Startretries:   0,
			Starttime:      0,
			Stopsignal:     "TERM",
			Stoptime:       1,
			Env:            map[string]string{"RELOADED": "1"},
			MemoryPriority: "low",
		},
	}

	if err := mgr.Reload(reloaded); err != nil {
		t.Fatalf("reload: %v", err)
	}

	updated := waitForStatusReport(t, mgr, 5*time.Second, func(report StatusReport) bool {
		return report.Name == "worker:00" && report.Status == RUNNING && report.Pid > 0 && report.Pid != initial.Pid
	})

	if updated.Pid == initial.Pid {
		t.Fatalf("expected pid to change after reload, got %d", updated.Pid)
	}
}

func TestManager_should_preserve_unchanged_process_when_other_process_changes_on_reload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := CreateManager(ctx)
	defer mgr.Shutdown()

	specs := map[string]*Config{
		"changed:00": {
			ProcessName:    "changed:00",
			Program:        "changed",
			Cmd:            []string{"/bin/sh", "-c", "sleep 30"},
			Numprocs:       1,
			Autostart:      true,
			Autorestart:    "never",
			Exitcodes:      []int{0},
			Startretries:   0,
			Starttime:      0,
			Stopsignal:     "TERM",
			Stoptime:       1,
			Env:            map[string]string{},
			MemoryPriority: "low",
		},
		"steady:00": {
			ProcessName:    "steady:00",
			Program:        "steady",
			Cmd:            []string{"/bin/sh", "-c", "sleep 30"},
			Numprocs:       1,
			Autostart:      true,
			Autorestart:    "never",
			Exitcodes:      []int{0},
			Startretries:   0,
			Starttime:      0,
			Stopsignal:     "TERM",
			Stoptime:       1,
			Env:            map[string]string{},
			MemoryPriority: "medium",
		},
	}

	if err := mgr.Load(specs); err != nil {
		t.Fatalf("load: %v", err)
	}

	changedBefore := waitForStatusReport(t, mgr, 3*time.Second, func(report StatusReport) bool {
		return report.Name == "changed:00" && report.Status == RUNNING && report.Pid > 0
	})
	steadyBefore := waitForStatusReport(t, mgr, 3*time.Second, func(report StatusReport) bool {
		return report.Name == "steady:00" && report.Status == RUNNING && report.Pid > 0
	})

	reloaded := map[string]*Config{
		"changed:00": {
			ProcessName:    "changed:00",
			Program:        "changed",
			Cmd:            []string{"/bin/sh", "-c", "sleep 30"},
			Numprocs:       1,
			Autostart:      true,
			Autorestart:    "never",
			Exitcodes:      []int{0},
			Startretries:   0,
			Starttime:      0,
			Stopsignal:     "TERM",
			Stoptime:       1,
			Env:            map[string]string{"RELOADED": "1"},
			MemoryPriority: "low",
		},
		"steady:00": {
			ProcessName:    "steady:00",
			Program:        "steady",
			Cmd:            []string{"/bin/sh", "-c", "sleep 30"},
			Numprocs:       1,
			Autostart:      true,
			Autorestart:    "never",
			Exitcodes:      []int{0},
			Startretries:   0,
			Starttime:      0,
			Stopsignal:     "TERM",
			Stoptime:       1,
			Env:            map[string]string{},
			MemoryPriority: "medium",
		},
	}

	instances := map[string]*Instance{}
	for name, spec := range specs {
		inst := &Instance{}
		inst.spec.Store(spec)
		instances[name] = inst
	}
	plan := Restarting(instances, reloaded)
	if len(plan.Restart) != 1 || plan.Restart[0] != "changed:00" {
		t.Fatalf("expected only changed:00 to restart, got %+v", plan)
	}
	if len(plan.Update) != 1 || plan.Update[0] != "steady:00" {
		t.Fatalf("expected only steady:00 to update in place, got %+v", plan)
	}

	if err := mgr.Reload(reloaded); err != nil {
		t.Fatalf("reload: %v", err)
	}

	changedAfter := waitForStatusReport(t, mgr, 5*time.Second, func(report StatusReport) bool {
		return report.Name == "changed:00" && report.Status == RUNNING && report.Pid > 0 && report.Pid != changedBefore.Pid
	})
	steadyAfter := waitForStatusReport(t, mgr, 1*time.Second, func(report StatusReport) bool {
		return report.Name == "steady:00" && report.Status == RUNNING && report.Pid > 0
	})

	if changedAfter.Pid == changedBefore.Pid {
		t.Fatalf("expected changed process pid to change after reload, got %d", changedAfter.Pid)
	}
	if steadyAfter.Pid != steadyBefore.Pid {
		t.Fatalf("expected unchanged process pid to stay %d, got %d", steadyBefore.Pid, steadyAfter.Pid)
	}
}

func waitForStatusReport(t *testing.T, mgr *Manager, timeout time.Duration, predicate func(StatusReport) bool) StatusReport {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, report := range mgr.Status() {
			if predicate(report) {
				return report
			}
		}
		time.Sleep(25 * time.Millisecond)
	}

	t.Fatal("timed out waiting for matching status report")
	return StatusReport{}
}
