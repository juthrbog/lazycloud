package eventlog

import (
	"fmt"
	"io"
	"sync"
	"time"

	charmlog "charm.land/log/v2"
)

// Level represents the severity of a log entry.
type Level int

const (
	LevelInfo Level = iota
	LevelWarn
	LevelError
	LevelDebug
)

func (l Level) String() string {
	switch l {
	case LevelInfo:
		return "INF"
	case LevelWarn:
		return "WRN"
	case LevelError:
		return "ERR"
	case LevelDebug:
		return "DBG"
	default:
		return "???"
	}
}

// Category groups related events.
type Category string

const (
	CatAWS    Category = "aws"
	CatNav    Category = "nav"
	CatConfig Category = "cfg"
	CatUI     Category = "ui"
	CatApp    Category = "app"
)

// Entry is a single event log entry.
type Entry struct {
	Time     time.Time
	Level    Level
	Category Category
	Message  string
}

// Format returns a display string for the entry.
func (e Entry) Format() string {
	ts := e.Time.Format("15:04:05")
	return fmt.Sprintf("%s  %s  [%s]  %s", ts, e.Level, e.Category, e.Message)
}

const maxEntries = 500

var (
	mu          sync.Mutex
	entries     []Entry
	charmLogger *charmlog.Logger
)

// SetOutput configures file-based logging to the given writer.
// Pass nil to disable file logging. The writer receives JSON-formatted log lines.
func SetOutput(w io.Writer) {
	mu.Lock()
	defer mu.Unlock()
	if w == nil {
		charmLogger = nil
		return
	}
	charmLogger = charmlog.NewWithOptions(w, charmlog.Options{
		Formatter:       charmlog.JSONFormatter,
		ReportTimestamp: true,
		Level:           charmlog.DebugLevel,
	})
}

func charmLevel(l Level) charmlog.Level {
	switch l {
	case LevelDebug:
		return charmlog.DebugLevel
	case LevelWarn:
		return charmlog.WarnLevel
	case LevelError:
		return charmlog.ErrorLevel
	default:
		return charmlog.InfoLevel
	}
}

// Log adds an entry to the global event log.
func Log(level Level, cat Category, msg string) {
	mu.Lock()
	defer mu.Unlock()

	entries = append(entries, Entry{
		Time:     time.Now(),
		Level:    level,
		Category: cat,
		Message:  msg,
	})

	// Ring buffer: drop oldest when over capacity
	if len(entries) > maxEntries {
		entries = entries[len(entries)-maxEntries:]
	}

	if charmLogger != nil {
		charmLogger.With("category", string(cat)).Log(charmLevel(level), msg)
	}
}

// Logf adds a formatted entry to the global event log.
func Logf(level Level, cat Category, format string, args ...any) {
	Log(level, cat, fmt.Sprintf(format, args...))
}

// Info is a convenience for Log(LevelInfo, ...).
func Info(cat Category, msg string) { Log(LevelInfo, cat, msg) }

// Infof is a convenience for Logf(LevelInfo, ...).
func Infof(cat Category, format string, args ...any) { Logf(LevelInfo, cat, format, args...) }

// Warn is a convenience for Log(LevelWarn, ...).
func Warn(cat Category, msg string) { Log(LevelWarn, cat, msg) }

// Warnf is a convenience for Logf(LevelWarn, ...).
func Warnf(cat Category, format string, args ...any) { Logf(LevelWarn, cat, format, args...) }

// Error is a convenience for Log(LevelError, ...).
func Error(cat Category, msg string) { Log(LevelError, cat, msg) }

// Errorf is a convenience for Logf(LevelError, ...).
func Errorf(cat Category, format string, args ...any) { Logf(LevelError, cat, format, args...) }

// Debug is a convenience for Log(LevelDebug, ...).
func Debug(cat Category, msg string) { Log(LevelDebug, cat, msg) }

// Debugf is a convenience for Logf(LevelDebug, ...).
func Debugf(cat Category, format string, args ...any) { Logf(LevelDebug, cat, format, args...) }

// Entries returns a copy of all current entries.
func Entries() []Entry {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Entry, len(entries))
	copy(out, entries)
	return out
}

// Len returns the number of entries.
func Len() int {
	mu.Lock()
	defer mu.Unlock()
	return len(entries)
}
