package internal

import (
	"context"
	"fmt"
	"sort"
	"sync/atomic"
	"time"
)

// RunMemoryGuard starts a background loop that monitors system memory usage.
// All config fields (enabled, threshold, interval) hot-update on reload.
func RunMemoryGuard(ctx context.Context, cfg *atomic.Pointer[MemoryGuardConfig], mgr *Manager, logger *Logger) {
	initial := cfg.Load()
	if !initial.Enabled {
		return
	}

	activeInterval := time.Duration(initial.Interval) * time.Second
	ticker := time.NewTicker(activeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current := cfg.Load()
			if !current.Enabled {
				continue
			}
			if newInterval := time.Duration(current.Interval) * time.Second; newInterval != activeInterval {
				activeInterval = newInterval
				ticker.Reset(activeInterval)
			}
			usage, err := MemoryUsagePercent()
			if err != nil {
				if logger != nil {
					logger.LogMessage(LevelWarn, fmt.Sprintf("memory guard: failed to read memory usage: %v", err))
				}
				continue
			}
			if usage >= current.Threshold {
				killLowestPriority(mgr, logger)
			}
		}
	}
}

// priorityRank maps a priority string to a numeric rank and whether it is known.
func priorityRank(p string) (int, bool) {
	switch p {
	case "low":
		return 0, true
	case "medium":
		return 1, true
	case "high":
		return 2, true
	default:
		return 1, false
	}
}

// killLowestPriority selects the RUNNING process with the lowest memory_priority
func killLowestPriority(mgr *Manager, logger *Logger) {
	instances := mgr.GetRunningInstances()
	if len(instances) == 0 {
		if logger != nil {
			logger.LogMessage(LevelWarn, "memory guard: no RUNNING processes to kill")
		}
		return
	}

	type candidate struct {
		name     string
		priority int
	}

	candidates := make([]candidate, 0, len(instances))
	for _, inst := range instances {
		rank, known := priorityRank(inst.Priority)
		if !known && logger != nil {
			logger.LogMessage(LevelWarn, fmt.Sprintf(
				"memory guard: process %q has invalid memory_priority %q (treated as medium)",
				inst.Name, inst.Priority,
			))
		}
		candidates = append(candidates, candidate{name: inst.Name, priority: rank})
	}

	// Sort by priority ascending, then alphabetically for determinism
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].priority != candidates[j].priority {
			return candidates[i].priority < candidates[j].priority
		}
		return candidates[i].name < candidates[j].name
	})

	victim := candidates[0].name

	if err := mgr.Stop(victim); err != nil {
		if logger != nil {
			logger.LogMessage(LevelError, fmt.Sprintf("memory guard: failed to kill %q: %v", victim, err))
		}
		return
	}

	if logger != nil {
		logger.LogMessage(LevelWarn, fmt.Sprintf(
			"memory guard: killed %q (lowest priority, system memory exceeded threshold)",
			victim,
		))
	}
}
