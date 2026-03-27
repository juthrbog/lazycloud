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
	filter   string
	hints    []KeyHint
	width    int
	height   int
}

// NewHelpOverlay creates a hidden help overlay.
func NewHelpOverlay() HelpOverlay {
	return HelpOverlay{}
}

// Show opens the help overlay with the given hints.
func (h *HelpOverlay) Show(hints []KeyHint, width, height int) {
	h.hints = hints
	h.width = width
	h.height = height
	h.filter = ""
	h.visible = true
	h.viewport = viewport.New()
	h.viewport.SetWidth(h.contentWidth())
	h.viewport.SetHeight(h.contentHeight())
	h.viewport.SetContent(h.renderContent())
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
			if len(h.filter) > 0 {
				h.filter = h.filter[:len(h.filter)-1]
				h.viewport.SetContent(h.renderContent())
				h.viewport.GotoTop()
			}
			return h, nil
		default:
			// Single printable character → append to filter
			if len(key) == 1 && key[0] >= 32 && key[0] < 127 {
				h.filter += key
				h.viewport.SetContent(h.renderContent())
				h.viewport.GotoTop()
				return h, nil
			}
		}

		// Forward scroll keys to viewport
		var cmd tea.Cmd
		h.viewport, cmd = h.viewport.Update(msg)
		return h, cmd
	}

	return h, nil
}

// View renders the help overlay as a bordered popup.
func (h HelpOverlay) View() string {
	if !h.visible {
		return ""
	}
	t := ActiveTheme

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(t.Primary)
	filterStyle := lipgloss.NewStyle().Foreground(t.Accent)
	dimStyle := lipgloss.NewStyle().Foreground(t.Muted)

	var header strings.Builder
	header.WriteString(titleStyle.Render("Keybindings"))

	if h.filter != "" {
		header.WriteString("  " + filterStyle.Render("/ "+h.filter))
	} else {
		header.WriteString("  " + dimStyle.Render("type to filter"))
	}
	header.WriteString("\n\n")

	footer := "\n" + dimStyle.Render("j/k scroll  ? close  esc close")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Primary).
		Padding(0, 1).
		Width(h.boxWidth())

	return box.Render(header.String() + h.viewport.View() + footer)
}

func (h HelpOverlay) boxWidth() int {
	w := h.width * 3 / 4
	if w > 70 {
		w = 70
	}
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
	ch := h.height - 12
	if ch < 5 {
		ch = 5
	}
	return ch
}

// renderContent builds the grouped, filtered hint content.
func (h HelpOverlay) renderContent() string {
	t := ActiveTheme

	groupStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(t.Accent)
	keyStyle := lipgloss.NewStyle().
		Foreground(t.Primary).
		Width(14)
	descStyle := lipgloss.NewStyle().
		Foreground(t.Text)
	rwBadge := lipgloss.NewStyle().
		Foreground(t.Warning).
		Bold(true)

	// Group hints by category
	type group struct {
		name  string
		hints []KeyHint
	}

	orderMap := map[string]int{
		"":           0, // Current View first
		"Navigation": 1,
		"Panel":      2,
		"Global":     3,
	}

	groups := make(map[string][]KeyHint)
	for _, hint := range h.hints {
		if h.filter != "" {
			lower := strings.ToLower(h.filter)
			if !strings.Contains(strings.ToLower(hint.Key), lower) &&
				!strings.Contains(strings.ToLower(hint.Desc), lower) &&
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
			badge := ""
			if hint.Mode == ModeReadWrite {
				badge = " " + rwBadge.Render("[RW]")
			}
			line := keyStyle.Render(hint.Key) + descStyle.Render(hint.Desc) + badge
			b.WriteString(line + "\n")
		}
	}

	if b.Len() == 0 {
		return lipgloss.NewStyle().Foreground(t.Muted).Render("  no matches")
	}

	return b.String()
}
