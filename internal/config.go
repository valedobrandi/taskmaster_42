package internal

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ProcessName    string            `yaml:"process_name"`
	Program        string            `yaml:"program"`
	Cmd            []string          `yaml:"cmd"`
	Numprocs       int               `yaml:"numprocs"`
	Umask          int               `yaml:"umask"`
	Workingdir     string            `yaml:"workingdir"`
	Autostart      bool              `yaml:"autostart"`
	Autorestart    string            `yaml:"autorestart"`
	Exitcodes      []int             `yaml:"exitcodes"`
	Startretries   int               `yaml:"startretries"`
	Starttime      int               `yaml:"starttime"`
	Stopsignal     string            `yaml:"stopsignal"`
	Stoptime       int               `yaml:"stoptime"`
	Stdout         string            `yaml:"stdout"`
	Stderr         string            `yaml:"stderr"`
	Env            map[string]string `yaml:"env"`
	Uid            *uint32           `yaml:"uid"`
	Gid            *uint32           `yaml:"gid"`
	MemoryPriority string            `yaml:"memory_priority"`
}

type MemoryGuardConfig struct {
	Enabled   bool    `yaml:"enabled"`
	Threshold float64 `yaml:"threshold"` // 0–100, system memory usage %
	Interval  int     `yaml:"interval"`  // seconds between checks
}

func validateOutputPath(kind, path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}

	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s parent dir %q does not exist (path: %q)", kind, dir, path)
		}
		return fmt.Errorf("stat %s parent dir %q: %w", kind, dir, err)
	}

	if !info.IsDir() {
		return fmt.Errorf("%s parent %q is not a directory (path: %q)", kind, dir, path)
	}

	const readWriteMode = 4 | 2
	if err := syscall.Access(dir, readWriteMode); err != nil {
		return fmt.Errorf("%s parent dir %q is not readable/writable: %w", kind, dir, err)
	}

	return nil
}

func (c *Config) ValidateRuntime() error {
	euid := uint32(os.Geteuid())
	egid := uint32(os.Getegid())

	if c.Uid != nil && *c.Uid != euid {
		return fmt.Errorf("uid %d does not match current effective uid %d", *c.Uid, euid)
	}

	if c.Gid != nil && *c.Gid != egid {
		return fmt.Errorf("gid %d does not match current effective gid %d", *c.Gid, egid)
	}

	return nil
}

func (c *Config) Validate() error {
	// Identity slice: mandatory fields
	if c.Program == "" {
		return fmt.Errorf("program must not be empty")
	}

	if len(c.Cmd) == 0 || strings.TrimSpace(c.Cmd[0]) == "" {
		return fmt.Errorf("cmd must list at least one non-empty executable")
	}

	if err := c.ValidateRuntime(); err != nil {
		return fmt.Errorf("runtime validation: %w", err)
	}

	if c.Numprocs < 1 {
		return fmt.Errorf("numprocs must be >= 1, got %d", c.Numprocs)
	}

	validSignals := map[string]bool{
		"TERM": true, "HUP": true, "INT": true, "QUIT": true,
		"KILL": true, "USR1": true, "USR2": true,
	}

	if c.Stopsignal != "" && !validSignals[c.Stopsignal] {
		return fmt.Errorf("stopsignal must be one of: TERM, HUP, INT, QUIT, KILL, USR1, USR2, got %q", c.Stopsignal)
	}

	if c.Stoptime < 0 {
		return fmt.Errorf("stoptime must be >= 0, got %d", c.Stoptime)
	}

	if c.Starttime < 0 {
		return fmt.Errorf("starttime must be >= 0, got %d", c.Starttime)
	}

	if c.Umask < 0 || c.Umask > 0o777 {
		return fmt.Errorf("umask must be between 0 and 0o777 (0-511 decimal), got %d", c.Umask)
	}

	if c.Workingdir != "" {
		info, err := os.Stat(c.Workingdir)
		if err != nil {
			return fmt.Errorf("workingdir %q does not exist: %w", c.Workingdir, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("workingdir %q is a file, not a directory", c.Workingdir)
		}
	}

	if err := validateOutputPath("stdout", c.Stdout); err != nil {
		return err
	}

	if err := validateOutputPath("stderr", c.Stderr); err != nil {
		return err
	}

	_, err := exec.LookPath(c.Cmd[0])
	if err != nil {
		return fmt.Errorf("executable %q not found in PATH: %w", c.Cmd[0], err)
	}

	// Normalize and validate memory_priority
	if c.MemoryPriority == "" {
		c.MemoryPriority = "medium"
	}
	c.MemoryPriority = strings.ToLower(c.MemoryPriority)
	switch c.MemoryPriority {
	case "low", "medium", "high":
		// valid
	default:
		return fmt.Errorf("memory_priority must be one of: low, medium, high, got %q", c.MemoryPriority)
	}

	return nil
}

func ExpandPrograms(programs map[string]Config) map[string]*Config {
	names := make([]string, 0, len(programs))
	for name := range programs {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make(map[string]*Config)

	for _, name := range names {
		spec := programs[name]
		for i := 0; i < spec.Numprocs; i++ {
			inst := spec
			inst.ProcessName = fmt.Sprintf("%s:%02d", name, i)
			ptr := new(Config)
			*ptr = inst
			out[inst.ProcessName] = ptr
		}
	}
	return out
}

func LoadConfig(path string) (map[string]*Config, MemoryGuardConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, MemoryGuardConfig{}, fmt.Errorf("read config file %s: %w", path, err)
	}

	var top struct {
		MemoryGuard MemoryGuardConfig `yaml:"memory_guard"`
		ConfigMap   map[string]Config `yaml:"programs"`
	}

	if err := yaml.Unmarshal(data, &top); err != nil {
		return nil, MemoryGuardConfig{}, fmt.Errorf(`parse yaml: %w`, err)
	}

	if top.ConfigMap == nil {
		return nil, MemoryGuardConfig{}, fmt.Errorf("missing programs")
	}

	for name, config := range top.ConfigMap {
		config.Program = name
		top.ConfigMap[name] = config
	}

	for name, config := range top.ConfigMap {
		if err := config.Validate(); err != nil {
			return nil, MemoryGuardConfig{}, fmt.Errorf("validate spec %q: %w", name, err)
		}
	}

	expanded := ExpandPrograms(top.ConfigMap)

	// Validate memory guard config only if enabled
	memGuard := top.MemoryGuard
	if memGuard.Enabled {
		if memGuard.Threshold < 1 || memGuard.Threshold > 99 {
			return nil, MemoryGuardConfig{}, fmt.Errorf("memory_guard.threshold must be between 1 and 99, got %.1f", memGuard.Threshold)
		}
		if memGuard.Interval < 1 {
			return nil, MemoryGuardConfig{}, fmt.Errorf("memory_guard.interval must be >= 1, got %d", memGuard.Interval)
		}
	}

	return expanded, memGuard, nil
}
