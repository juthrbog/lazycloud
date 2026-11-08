package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	// maxToastLines is the most lines that one toast shows. On a narrow
	// terminal, a long toast would otherwise cover the screen.
	maxToastLines = 4
	// toastFrame is the width of a toast box around its text: the border
	// and padding of the box (4), the icon and its spaces (3), and the
	// space after the text (1).
	toastFrame = 8
	// toastMargin is the gap between the toast box and the right edge
	// of the screen (see the toast overlay in app.go).
	toastMargin = 2
	// minToastText is the narrowest text column. A very narrow terminal
	// cuts the box at its edge rather than wrap one letter to a line.
	minToastText = 10
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

// defaultDuration returns a level-appropriate duration for toasts.
func defaultDuration(level ToastLevel) time.Duration {
	switch level {
	case ToastError:
		return 6 * time.Second
	case ToastSuccess:
		return 3 * time.Second
	default:
		return 4 * time.Second
	}
}

// Add creates a new toast and returns its ID and a dismiss command.
func (tm *ToastManager) Add(text string, level ToastLevel, duration time.Duration) (int, tea.Cmd) {
	if duration == 0 {
		duration = defaultDuration(level)
	}

	id := tm.nextID
	tm.nextID++

	tm.toasts = append(tm.toasts, Toast{
		Text:      Sanitize(text),
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

// View renders the toast stack as a bordered block that fits in width,
// with the margin that the app keeps to the right edge. A long toast wraps,
// and its wrapped lines start under its text. Skips expired toasts that
// haven't been cleaned up yet.
func (tm ToastManager) View(width int) string {
	textWidth := max(width-toastMargin-toastFrame, minToastText)
	s := S
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
			style = s.Success
		case ToastError:
			icon = "✗"
			style = s.Error
		default:
			icon = "●"
			style = s.Info
		}

		lines = append(lines, style.Render(toastLines(icon, toast.Text, textWidth)))
	}

	if len(lines) == 0 {
		return ""
	}

	content := strings.Join(lines, "\n")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ActiveTheme.Secondary).
		Padding(0, 1)

	return box.Render(content)
}

// toastLines wraps the text of one toast to width and puts the icon before
// the first line. The other lines start under the text. S3 keys and paths
// have no spaces, so the text can also break after "/", "_", and ".". Text
// longer than maxToastLines lines ends with "…".
func toastLines(icon, text string, width int) string {
	wrapped := strings.Split(ansi.Wrap(text, width, "/_."), "\n")
	if len(wrapped) > maxToastLines {
		wrapped = wrapped[:maxToastLines]
		last := strings.TrimRight(wrapped[maxToastLines-1], " ")
		wrapped[maxToastLines-1] = ansi.Truncate(last, width-1, "") + "…"
	}
	var b strings.Builder
	for i, line := range wrapped {
		if i == 0 {
			fmt.Fprintf(&b, " %s %s ", icon, line)
			continue
		}
		fmt.Fprintf(&b, "\n   %s ", line)
	}
	return b.String()
}
