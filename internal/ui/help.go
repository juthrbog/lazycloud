package ui

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// HelpOverlay is a scrollable, filterable overlay that displays keybindings
// grouped by category.
type HelpOverlay struct {
	viewport viewport.Model
	visible  bool
	filter   queryInput
	hints    []HintBinding
	width    int
	height   int
}

// NewHelpOverlay creates a hidden help overlay.
func NewHelpOverlay() HelpOverlay {
	return HelpOverlay{}
}

// Show opens the help overlay with the given hints.
func (h *HelpOverlay) Show(hints []HintBinding, width, height int) {
	h.hints = hints
	h.width = width
	h.height = height
	h.filter = queryInput{}
	h.visible = true
	h.viewport = viewport.New()
	h.viewport.SetWidth(h.contentWidth())
	h.viewport.SetHeight(h.contentHeight())
	h.viewport.SetContent(h.renderContent())
}

// SetSize gives the help overlay the terminal size. While it is open, it
// lays out the text again for the new size.
func (h *HelpOverlay) SetSize(width, height int) {
	h.width = width
	h.height = height
	if !h.visible {
		return
	}
	h.viewport.SetWidth(h.contentWidth())
	h.viewport.SetHeight(h.contentHeight())
	h.viewport.SetContent(h.renderContent())
	// SetContent keeps the vertical offset in range, but not the
	// horizontal one. Lines that fit the new width must start at column 0.
	h.viewport.SetXOffset(h.viewport.XOffset())
}

// Hide dismisses the help overlay.
func (h *HelpOverlay) Hide() {
	h.visible = false
}

// Visible returns whether the help overlay is currently shown.
func (h HelpOverlay) Visible() bool {
	return h.visible
}

// Update handles input when the help overlay is visible.
func (h HelpOverlay) Update(msg tea.Msg) (HelpOverlay, tea.Cmd) {
	if !h.visible {
		return h, nil
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		key := msg.String()
		switch key {
		case "?", "esc":
			h.visible = false
			return h, nil
		case "backspace":
			h.typed(msg)
			return h, nil
		default:
			if h.typed(msg) {
				return h, nil
			}
		}

		// Forward scroll keys to viewport
		var cmd tea.Cmd
		h.viewport, cmd = h.viewport.Update(msg)
		return h, cmd
	case tea.PasteMsg:
		h.typed(msg)
	}

	return h, nil
}

// typed applies typed or pasted text to the filter. It returns true when
// the filter changed.
func (h *HelpOverlay) typed(msg tea.Msg) bool {
	if !h.filter.update(msg) {
		return false
	}
	h.viewport.SetContent(h.renderContent())
	h.viewport.GotoTop()
	return true
}

// View renders the help overlay as a bordered popup.
func (h HelpOverlay) View() string {
	if !h.visible {
		return ""
	}
	s := S

	var header strings.Builder
	header.WriteString(s.Title.Render("Keybindings"))

	if h.filter.String() != "" {
		// The title, the gap, and "/ " come before the text.
		room := h.contentWidth() - lipgloss.Width(header.String()) - 4
		header.WriteString("  " + s.FilterPrompt.Render("/ "+h.filter.view(room)))
	} else {
		header.WriteString("  " + s.Muted.Render("type to filter"))
	}
	header.WriteString("\n\n")

	footer := "\n" + s.Muted.Render("type to filter  ? close  esc close")

	box := s.DialogBorder.Width(h.boxWidth())

	return box.Render(header.String() + h.viewport.View() + footer)
}

func (h HelpOverlay) boxWidth() int {
	w := min(h.width*3/4, 70)
	if w < 40 {
		w = h.width - 4
	}
	return w
}

func (h HelpOverlay) contentWidth() int {
	// box width minus border (2) minus padding (6)
	return h.boxWidth() - 8
}

func (h HelpOverlay) contentHeight() int {
	// screen height minus box border/padding (4) minus header (2) minus footer (1) minus some margin
	return max(h.height-12, 5)
}

// renderContent builds the grouped, filtered hint content.
func (h HelpOverlay) renderContent() string {
	s := S

	groupStyle := s.HelpGroupHeader
	keyStyle := s.HelpKeyColumn
	descStyle := lipgloss.NewStyle().Foreground(ActiveTheme.Text)
	rwBadge := s.ReadWriteBadge

	// Group hints by category
	type group struct {
		name  string
		hints []HintBinding
	}

	orderMap := map[string]int{
		"":           0, // Current View first
		"Navigation": 1,
		"Panel":      2,
		"Global":     3,
	}

	groups := make(map[string][]HintBinding)
	for _, hint := range h.hints {
		help := hint.Help()
		if h.filter.String() != "" {
			lower := strings.ToLower(h.filter.String())
			if !strings.Contains(strings.ToLower(help.Key), lower) &&
				!strings.Contains(strings.ToLower(help.Desc), lower) &&
				!strings.Contains(strings.ToLower(hint.Category), lower) {
				continue
			}
		}
		groups[hint.Category] = append(groups[hint.Category], hint)
	}

	// Sort groups by defined order
	var orderedGroups []group
	for i := 0; i <= 3; i++ {
		for cat, hints := range groups {
			if orderMap[cat] == i {
				name := cat
				if name == "" {
					name = "Current View"
				}
				orderedGroups = append(orderedGroups, group{name: name, hints: hints})
				delete(groups, cat)
			}
		}
	}
	// Any remaining unknown categories
	for cat, hints := range groups {
		orderedGroups = append(orderedGroups, group{name: cat, hints: hints})
	}

	var b strings.Builder
	for i, g := range orderedGroups {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(groupStyle.Render(g.name) + "\n")
		for _, hint := range g.hints {
			help := hint.Help()
			badge := ""
			if hint.Mode == ModeReadWrite {
				badge = " " + rwBadge.Render("[RW]")
			}
			line := keyStyle.Render(help.Key) + descStyle.Render(help.Desc) + badge
			b.WriteString(line + "\n")
		}
	}

	if b.Len() == 0 {
		return s.Muted.Render("  no matches")
	}

	return b.String()
}
