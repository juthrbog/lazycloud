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
// Hints that don't fit the available width are progressively dropped from the
// right (lowest priority), since currentKeyHints() orders view-specific hints
// first and global hints last.
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

	// Filter by mode
	var filtered []KeyHint
	for _, k := range data.Keys {
		if k.Mode == ModeReadWrite && ReadOnly {
			continue
		}
		if k.Mode == ModeReadOnly && !ReadOnly {
			continue
		}
		filtered = append(filtered, k)
	}

	// Progressively drop hints from the right until they fit
	bar := renderHints(s, filtered, data.Width)

	barWidth := lipgloss.Width(bar)
	if data.Width > barWidth {
		padding := s.StatusBarBase.Width(data.Width - barWidth).Render("")
		bar += padding
	}

	return bar
}

// renderHints renders as many hints as fit within maxWidth, dropping from the right.
func renderHints(s Styles, hints []KeyHint, maxWidth int) string {
	for n := len(hints); n > 0; n-- {
		var parts []string
		for _, k := range hints[:n] {
			keyLabel := "<" + k.Key + ">"
			if strings.ContainsAny(k.Key, "<>") {
				keyLabel = k.Key // already contains angle brackets, don't double-wrap
			}
			parts = append(parts, s.StatusKey.Render(keyLabel)+" "+s.StatusDesc.Render(k.Desc))
		}
		bar := strings.Join(parts, "  ")
		if lipgloss.Width(bar) <= maxWidth || n == 1 {
			return bar
		}
	}
	return ""
}
