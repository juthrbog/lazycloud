package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// KeyHint represents a single keybinding hint for the status bar.
type KeyHint struct {
	Key  string
	Desc string
}

// StatusBarData holds the values needed to render the status bar.
type StatusBarData struct {
	Keys  []KeyHint
	Error string
	Info  string
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
		hint := s.StatusKey.Render("<"+k.Key+">") + " " + s.StatusDesc.Render(k.Desc)
		parts = append(parts, hint)
	}

	bar := strings.Join(parts, "  ")

	if data.Info != "" {
		bar += "  " + s.StatusDesc.Render(data.Info)
	}

	barWidth := lipgloss.Width(bar)
	if data.Width > barWidth {
		padding := s.StatusBarBase.Width(data.Width - barWidth).Render("")
		bar += padding
	}

	return bar
}
