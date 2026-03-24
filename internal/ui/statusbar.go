package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// HintMode controls when a keybinding hint is shown based on ReadOnly state.
type HintMode int

const (
	ModeAny       HintMode = iota // shown in both ReadOnly and ReadWrite (default zero value)
	ModeReadOnly                  // shown only in ReadOnly mode
	ModeReadWrite                 // shown only in ReadWrite mode
)

// KeyHint represents a single keybinding hint for the status bar.
type KeyHint struct {
	Key      string
	Desc     string
	Mode     HintMode // zero value = ModeAny (backward compatible)
	Category string   // for help overlay grouping; empty = uncategorized
}

// StatusBarData holds the values needed to render the status bar.
type StatusBarData struct {
	Keys  []KeyHint
	Error string
	Width int
}

// RenderStatusBar renders the bottom bar with contextual keybindings.
func RenderStatusBar(data StatusBarData) string {
	s := S

	if data.Error != "" {
		errMsg := s.Error.Render(" " + data.Error + " ")
		errWidth := lipgloss.Width(errMsg)
		if data.Width > errWidth {
			padding := s.StatusBarBase.Width(data.Width - errWidth).Render("")
			errMsg += padding
		}
		return errMsg
	}

	var parts []string
	for _, k := range data.Keys {
		if k.Mode == ModeReadWrite && ReadOnly {
			continue
		}
		if k.Mode == ModeReadOnly && !ReadOnly {
			continue
		}
		hint := s.StatusKey.Render("<"+k.Key+">") + " " + s.StatusDesc.Render(k.Desc)
		parts = append(parts, hint)
	}

	bar := strings.Join(parts, "  ")

	barWidth := lipgloss.Width(bar)
	if data.Width > barWidth {
		padding := s.StatusBarBase.Width(data.Width - barWidth).Render("")
		bar += padding
	}

	return bar
}
