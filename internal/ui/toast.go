package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ToastLevel controls the color of a toast notification.
type ToastLevel int

const (
	ToastInfo ToastLevel = iota
	ToastSuccess
	ToastError
)

// ToastDismissMsg is sent when a toast's duration expires.
type ToastDismissMsg struct {
	ID int
}

// Toast is a single timed notification.
type Toast struct {
	Text      string
	Level     ToastLevel
	CreatedAt time.Time
	Duration  time.Duration
	ID        int
}

// ToastManager manages a queue of auto-dismissing notifications.
type ToastManager struct {
	toasts     []Toast
	nextID     int
	maxVisible int
}

// NewToastManager creates a toast manager with a default max of 3 visible toasts.
func NewToastManager() ToastManager {
	return ToastManager{maxVisible: 3}
}

// Add creates a new toast and returns its ID and a dismiss command.
func (tm *ToastManager) Add(text string, level ToastLevel, duration time.Duration) (int, tea.Cmd) {
	if duration == 0 {
		duration = 4 * time.Second
	}

	id := tm.nextID
	tm.nextID++

	tm.toasts = append(tm.toasts, Toast{
		Text:      text,
		Level:     level,
		CreatedAt: time.Now(),
		Duration:  duration,
		ID:        id,
	})

	// Trim to max visible
	if len(tm.toasts) > tm.maxVisible {
		tm.toasts = tm.toasts[len(tm.toasts)-tm.maxVisible:]
	}

	dismissID := id
	dismissDur := duration
	cmd := func() tea.Msg {
		time.Sleep(dismissDur)
		return ToastDismissMsg{ID: dismissID}
	}

	return id, cmd
}

// Dismiss removes a toast by ID.
func (tm *ToastManager) Dismiss(id int) {
	for i, t := range tm.toasts {
		if t.ID == id {
			tm.toasts = append(tm.toasts[:i], tm.toasts[i+1:]...)
			return
		}
	}
}

// Cleanup removes toasts that have exceeded their duration.
// This is a fallback for cases where the dismiss command may not fire
// (e.g., when toasts originate from view closures routed through the
// message loop). Call from Update periodically.
func (tm *ToastManager) Cleanup() {
	now := time.Now()
	filtered := tm.toasts[:0]
	for _, t := range tm.toasts {
		if now.Sub(t.CreatedAt) < t.Duration {
			filtered = append(filtered, t)
		}
	}
	tm.toasts = filtered
}

// HasActive returns whether any non-expired toasts exist.
func (tm ToastManager) HasActive() bool {
	now := time.Now()
	for _, t := range tm.toasts {
		if now.Sub(t.CreatedAt) < t.Duration {
			return true
		}
	}
	return false
}

// View renders the toast stack as a bordered block.
// Skips expired toasts that haven't been cleaned up yet.
func (tm ToastManager) View(width int) string {
	t := ActiveTheme
	now := time.Now()
	var lines []string

	for _, toast := range tm.toasts {
		if now.Sub(toast.CreatedAt) >= toast.Duration {
			continue
		}

		var icon string
		var style lipgloss.Style

		switch toast.Level {
		case ToastSuccess:
			icon = "✓"
			style = lipgloss.NewStyle().Foreground(t.Success)
		case ToastError:
			icon = "✗"
			style = lipgloss.NewStyle().Foreground(t.Error)
		default:
			icon = "●"
			style = lipgloss.NewStyle().Foreground(t.Primary)
		}

		line := style.Render(fmt.Sprintf(" %s %s ", icon, toast.Text))
		lines = append(lines, line)
	}

	if len(lines) == 0 {
		return ""
	}

	content := strings.Join(lines, "\n")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Secondary).
		Padding(0, 1)

	return box.Render(content)
}
