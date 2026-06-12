package main

import (
	"fmt"
	"os"
	"path/filepath"

	"taskmaster/test_taskmaster/config"
	"taskmaster/test_taskmaster/helpers"
	"taskmaster/test_taskmaster/tests"
)

func main() {
	os.Exit(run())
}

func run() int {
	root, err := os.Getwd()
	helpers.Must(err)
	helpers.Must(prepareGoBuildEnv())

	p := helpers.DefaultPrinter
	p.Banner("══════════════════════════════════════════════")
	p.Banner("  Taskmaster Evaluation — End-to-End Suite")
	p.Banner("══════════════════════════════════════════════")

	ctx := &helpers.TestContext{
		Root:       root,
		ConfigPath: filepath.Join(root, config.ConfigFile),
		BackupPath: config.BackupPath(),
		DaemonPath: filepath.Join(root, config.DaemonBin),
		CtlPath:    filepath.Join(root, config.CtlBin),
		LogPath:    filepath.Join(root, config.LogFile),
		SocketPath: filepath.Join(os.TempDir(), fmt.Sprintf("taskmaster-%d.sock", os.Getpid())),
	}

	p.Section("Building Binaries")
	if err := helpers.RunCmd(ctx.Root, "go", "build", "-o", ctx.DaemonPath, config.DaemonBuildTarget); err != nil {
		fmt.Printf("Failed to build daemon: %v\n", err)
		return 1
	}
	if err := helpers.RunCmd(ctx.Root, "go", "build", "-o", ctx.CtlPath, config.CtlBuildTarget); err != nil {
		fmt.Printf("Failed to build ctl: %v\n", err)
		return 1
	}
	p.Pass("Build succeeded")

	helpers.Must(helpers.CopyFile(ctx.ConfigPath, ctx.BackupPath))
	defer cleanup(ctx)

	global := helpers.NewReport()

	runTestBlock(ctx, global, "TEST 0", tests.RunPoint0)
	runTestBlock(ctx, global, "TEST 1", tests.RunPoint1)
	runTestBlock(ctx, global, "TEST 2", tests.RunPoint2)
	runTestBlock(ctx, global, "TEST 3", tests.RunPoint3)
	runTestBlock(ctx, global, "TEST 4", tests.RunPoint4)
	runTestBlock(ctx, global, "TEST 5.1", tests.RunPoint51)
	runTestBlock(ctx, global, "TEST 5.2", tests.RunPoint52)
	runTestBlock(ctx, global, "TEST 5.3", tests.RunPoint53)
	runTestBlock(ctx, global, "TEST 5.4", tests.RunPoint54)
	runTestBlock(ctx, global, "TEST 5.5", tests.RunPoint55)
	runTestBlock(ctx, global, "TEST 5.6", tests.RunPoint56)
	runTestBlock(ctx, global, "TEST 5.7", tests.RunPoint57)
	runTestBlock(ctx, global, "TEST 5.8", tests.RunPoint58)
	runTestBlock(ctx, global, "TEST 5.9", tests.RunPoint59)
	runTestBlock(ctx, global, "TEST 5.10", tests.RunPoint510)
	runTestBlock(ctx, global, "TEST 5.11", tests.RunPoint511)
	runTestBlock(ctx, global, "TEST 5.12", tests.RunPoint512)
	runTestBlock(ctx, global, "BONUS 1", tests.RunBonus1)
	runTestBlock(ctx, global, "TEST 6", tests.RunPoint6)
	runTestBlock(ctx, global, "TEST 7", tests.RunPoint7)
	runTestBlock(ctx, global, "STABILITY", tests.RunStability)

	p.Banner("══════════════════════════════════════════════")
	p.Banner("  Total Suite Results")
	p.Banner("══════════════════════════════════════════════")
	global.PrintResults("Suite")

	if global.Fail > 0 {
		return 1
	}
	return 0
}

func runTestBlock(ctx *helpers.TestContext, global *helpers.Report, title string, fn func(*helpers.TestContext, *helpers.Report)) {
	_ = helpers.CopyFile(ctx.BackupPath, ctx.ConfigPath)
	helpers.StopDaemon(ctx)
	_ = os.Remove(ctx.SocketPath)
	_ = os.Remove(ctx.LogPath)

	pt := helpers.NewReport()
	if err := helpers.StartDaemon(ctx); err != nil {
		pt.Failf("Failed to start daemon for %s: %v", title, err)
		global.Merge(pt)
		return
	}
	if err := helpers.WaitForCtlReady(ctx, config.DaemonReadyTimeout); err != nil {
		pt.Failf("Daemon did not become ready for %s: %v", title, err)
		global.Merge(pt)
		return
	}

	fn(ctx, pt)
	pt.PrintResults(title)
	global.Merge(pt)
}

func cleanup(ctx *helpers.TestContext) {
	helpers.StopDaemon(ctx)
	_ = helpers.CopyFile(ctx.BackupPath, ctx.ConfigPath)
	_ = os.Remove(ctx.BackupPath)
	_ = os.Remove(ctx.SocketPath)
}

func prepareGoBuildEnv() error {
	cacheVars := map[string]string{
		"GOCACHE":    filepath.Join(os.TempDir(), "taskmaster-go-build-cache"),
		"GOMODCACHE": filepath.Join(os.TempDir(), "taskmaster-go-mod-cache"),
	}

	for key, value := range cacheVars {
		if os.Getenv(key) != "" {
			continue
		}
		if err := os.MkdirAll(value, 0o755); err != nil {
			return err
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}

	return nil
}
