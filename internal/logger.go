package internal

import (
	"fmt"
	"log/syslog"
	"os"
	"time"
)

// Logger listens for process updates on a dedicated channel and logs them to a file.
// This decouples logging from the manager's status update flow, avoiding channel contention.
type Logger struct {
	file    *os.File
	entries chan logEntry
	syslog  *syslog.Writer
}

type LogLevel string

const (
	LevelCritical LogLevel = "CRIT"
	LevelError    LogLevel = "ERRO"
	LevelWarn     LogLevel = "WARN"
	LevelInfo     LogLevel = "INFO"
	LevelDebug    LogLevel = "DEBG"
)

type logEntry struct {
	level   LogLevel
	message string
}

// NewLogger creates a new Logger instance with a dedicated updates channel.
func NewLogger(logFile string) (*Logger, error) {
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open log file %s: %w", logFile, err)
	}
	writer, _ := syslog.New(syslog.LOG_DAEMON, "taskmaster")

	return &Logger{
		file:    f,
		entries: make(chan logEntry, 100),
		syslog:  writer,
	}, nil
}

// Start begins listening for process updates in a goroutine.
// The logger will continue running until the updates channel is closed.
func (l *Logger) Start() {
	go func() {
		for entry := range l.entries {
			timestamp := time.Now().Format("2006-01-02 15:04:05,000")
			logLine := fmt.Sprintf("%s %s %s\n", timestamp, entry.level, entry.message)
			if _, err := l.file.WriteString(logLine); err != nil {
				fmt.Fprintf(os.Stderr, "[logger] write error: %v\n", err)
			}

			if l.syslog != nil {
				switch entry.level {
				case LevelCritical:
					_ = l.syslog.Crit(entry.message)
				case LevelError:
					_ = l.syslog.Err(entry.message)
				case LevelWarn:
					_ = l.syslog.Warning(entry.message)
				case LevelDebug:
					_ = l.syslog.Debug(entry.message)
				default:
					_ = l.syslog.Info(entry.message)
				}
			}

		}
	}()
}

// Log sends a process update to the logger without blocking.
// If the logger's channel is full, the update is discarded (non-blocking send).
func (l *Logger) Log(update ProcessUpdate) {
	level, message := formatProcessUpdate(update)
	l.enqueue(level, message)
}

func (l *Logger) LogMessage(level LogLevel, message string) {
	l.enqueue(level, message)
}

func (l *Logger) enqueue(level LogLevel, message string) {
	select {
	case l.entries <- logEntry{level: level, message: message}:
	default:
		// Channel full, discard to avoid blocking the manager
	}
}

// Close closes the logger's file and updates channel.
func (l *Logger) Close() error {
	close(l.entries)
	if l.syslog != nil {
		_ = l.syslog.Close()
	}
	return l.file.Close()
}

func formatProcessUpdate(update ProcessUpdate) (LogLevel, string) {
	name := update.Name
	pid := update.Pid
	code := update.ExitCode

	switch update.Status {
	case STARTING:
		return LevelInfo, fmt.Sprintf("process '%s' entering STARTING state", name)
	case RUNNING:
		if pid > 0 {
			return LevelInfo, fmt.Sprintf("process '%s' entered RUNNING state (pid %d)", name, pid)
		}
		return LevelInfo, fmt.Sprintf("process '%s' entered RUNNING state", name)
	case BACKOFF:
		return LevelWarn, fmt.Sprintf("process '%s' entered BACKOFF state", name)
	case FATAL:
		return LevelCritical, fmt.Sprintf("process '%s' entered FATAL state", name)
	case STOPPED:
		if code != 0 {
			return LevelWarn, fmt.Sprintf("exited: '%s' (exit status %d; not expected)", name, code)
		}
		return LevelInfo, fmt.Sprintf("exited: '%s' (exit status 0)", name)
	default:
		return LevelInfo, fmt.Sprintf("process '%s' state %s", name, update.Status)
	}
}
