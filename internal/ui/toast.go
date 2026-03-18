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

// HasActive returns whether any toasts are showing.
func (tm ToastManager) HasActive() bool {
	return len(tm.toasts) > 0
}

// View renders the toast stack as a bordered block.
func (tm ToastManager) View(width int) string {
	if len(tm.toasts) == 0 {
		return ""
	}

	t := ActiveTheme
	var lines []string

	for _, toast := range tm.toasts {
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

	content := strings.Join(lines, "\n")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Secondary).
		Padding(0, 1)

	return box.Render(content)
}
