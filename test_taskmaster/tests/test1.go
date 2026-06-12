package tests

import (
	"strings"
	"time"

	"taskmaster/test_taskmaster/config"
	"taskmaster/test_taskmaster/helpers"
)

func RunPoint1(ctx *helpers.TestContext, r *helpers.Report) {
	r.Section("TEST 1 — Control Shell")
	r.Info("Guide: ctl must start, stop, restart programs and reflect state in status.")
	r.Info("Uses dummy:00 for the full start → stop → restart cycle.")

	r.Passf("Daemon started (PID %d)", ctx.Daemon.Process.Pid)

	_, err := helpers.WaitForStatus(ctx, config.DefaultWaitTimeout, func(m map[string]helpers.ProcStatus) bool {
		p, ok := m["dummy:00"]
		return ok && p.State == "running"
	})
	if err != nil {
		r.Failf("1.0 dummy:00 not running before stop test: %v", err)
		return
	}

	helpers.RunCtl(ctx, "stop dummy:00")
	_, err = helpers.WaitForStatus(ctx, config.DefaultWaitTimeout, func(m map[string]helpers.ProcStatus) bool {
		p, ok := m["dummy:00"]
		return ok && p.State == "stopped"
	})
	if err != nil {
		r.Failf("1.0 stop dummy:00 did not settle before start: %v", err)
		return
	}

	out, err := helpers.RunCtl(ctx, "start dummy:00")
	if err == nil && (strings.Contains(out, "started") || strings.Contains(out, "running") || strings.Contains(out, "RUNNING")) {
		_, _ = helpers.WaitForStatus(ctx, config.DefaultWaitTimeout, func(m map[string]helpers.ProcStatus) bool {
			p, ok := m["dummy:00"]
			return ok && p.State == "running"
		})
		r.Passf("1.1 start dummy:00 -> running")
	} else {
		r.Failf("1.1 start dummy:00 failed: %v | out: %q", err, out)
	}
	stOut, _ := helpers.RunCtl(ctx, "status")
	if strings.Contains(stOut, "dummy:00") && strings.Contains(stOut, "RUNNING") {
		r.Passf("1.2 status shows dummy:00 as running")
	} else {
		r.Failf("1.2 status does NOT show dummy:00 as running")
	}

	out, err = helpers.RunCtl(ctx, "stop dummy:00")
	if err == nil && strings.Contains(out, "stopped") {
		r.Passf("1.3 stop dummy:00 -> stopped")
	} else {
		r.Failf("1.3 stop failed: %v | out: %q", err, out)
	}

	time.Sleep(config.StopSettleWait)
	stOut, _ = helpers.RunCtl(ctx, "status")
	if strings.Contains(stOut, "dummy:00") && strings.Contains(stOut, "STOPPED") {
		r.Passf("1.4 status shows dummy:00 as stopped")
	} else {
		r.Failf("1.4 status does NOT show dummy:00 as stopped")
	}

	out, err = helpers.RunCtlArgs(ctx, "start", "dummy:00")
	if err == nil && strings.Contains(out, "started") {
		_, _ = helpers.WaitForStatus(ctx, config.DefaultWaitTimeout, func(m map[string]helpers.ProcStatus) bool {
			p, ok := m["dummy:00"]
			return ok && p.State == "running"
		})
		r.Passf("1.4a argv start dummy:00 -> running")
	} else {
		r.Failf("1.4a argv start failed: %v | out: %q", err, out)
	}

	out, err = helpers.RunCtlArgs(ctx, "status")
	if err == nil && strings.Contains(out, "dummy:00") && strings.Contains(out, "RUNNING") {
		r.Passf("1.4b argv status shows dummy:00 as running")
	} else {
		r.Failf("1.4b argv status failed: %v | out: %q", err, out)
	}

	_, _ = helpers.RunCtl(ctx, "restart dummy:00")
	_, err = helpers.WaitForStatus(ctx, config.RestartWaitTimeout, func(m map[string]helpers.ProcStatus) bool {
		p, ok := m["dummy:00"]
		return ok && p.State == "running"
	})
	if err == nil {
		r.Passf("1.5 restart dummy:00 -> running")
	} else {
		r.Failf("1.5 restart failed (timeout waiting for RUNNING)")
	}

	stOut, _ = helpers.RunCtl(ctx, "status")
	if strings.Contains(stOut, "dummy:00") && strings.Contains(stOut, "RUNNING") {
		r.Passf("1.6 status shows dummy:00 as running after restart")
	} else {
		r.Failf("1.6 status does NOT show dummy:00 as running after restart")
	}

	out, _ = helpers.RunCtl(ctx, "start no_such_process")
	outL := strings.ToLower(out)
	if strings.Contains(outL, "error") || strings.Contains(outL, "not found") || strings.Contains(outL, "unknown") {
		r.Passf("1.7 unknown process -> error (expected)")
	} else {
		r.Failf("1.7 unknown process should error, got: %q", out)
	}

	stMap, _ := helpers.WaitForStatus(ctx, config.StopWaitTimeout, func(_ map[string]helpers.ProcStatus) bool { return true })
	if len(stMap) >= config.MinExpectedPrograms {
		r.Passf("1.8 status lists %d programs (>=3)", len(stMap))
	} else {
		r.Failf("1.8 status lists only %d programs (expected >=3)", len(stMap))
	}
}
