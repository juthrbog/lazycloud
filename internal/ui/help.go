package ui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// HelpOverlay is a scrollable, filterable overlay that displays keybindings
// grouped by category. The scroll keys of the viewport, such as j and k,
// scroll it. / starts the filter, as in the lists: then printable keys type,
// enter keeps the text, and esc clears it.
type HelpOverlay struct {
	viewport  viewport.Model
	visible   bool
	filter    queryInput
	filtering bool // the keys go to the filter
	hints     []HintBinding
	width     int
	height    int
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
	h.filtering = false
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

// SetHints replaces the hints of the help overlay. While it is open, it lays
// out the text again. The filter and the scroll position stay.
func (h *HelpOverlay) SetHints(hints []HintBinding) {
	h.hints = hints
	if h.visible {
		h.viewport.SetContent(h.renderContent())
	}
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
		if h.filtering {
			return h.updateFilter(msg)
		}
		switch msg.String() {
		case "?", "esc", "q":
			h.visible = false
			return h, nil
		case "/":
			h.filtering = true
			return h, nil
		}
		var cmd tea.Cmd
		h.viewport, cmd = h.viewport.Update(msg)
		return h, cmd
	case tea.PasteMsg:
		// A paste starts the filter.
		h.filtering = true
		h.typed(msg)
	}

	return h, nil
}

// updateFilter handles a key while the filter takes the keys. A key that
// does not change the text, such as an arrow, scrolls.
func (h HelpOverlay) updateFilter(msg tea.KeyPressMsg) (HelpOverlay, tea.Cmd) {
	switch msg.String() {
	case "enter":
		h.filtering = false
		return h, nil
	case "esc":
		h.filtering = false
		h.filter = queryInput{}
		h.viewport.SetContent(h.renderContent())
		h.viewport.GotoTop()
		return h, nil
	}
	if h.typed(msg) {
		return h, nil
	}
	var cmd tea.Cmd
	h.viewport, cmd = h.viewport.Update(msg)
	return h, cmd
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

	switch {
	case h.filtering:
		// The title, the gap, and "/ " come before the text.
		room := h.contentWidth() - lipgloss.Width(header.String()) - 4
		header.WriteString("  " + s.FilterPrompt.Render("/ "+h.filter.view(room)))
	case h.filter.String() != "":
		header.WriteString("  " + s.FilterPrompt.Render("/ "+h.filter.String()))
	}
	header.WriteString("\n\n")

	keys := "j/k scroll  / filter  ? close"
	if h.filtering {
		keys = "enter keep filter  esc clear filter"
	}
	footer := "\n" + s.Muted.Render(keys)

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

	// The known categories come first, in their order. Other categories
	// follow in the order of their names.
	cats := make([]string, 0, len(groups))
	for cat := range groups {
		cats = append(cats, cat)
	}
	rank := func(cat string) int {
		if r, ok := orderMap[cat]; ok {
			return r
		}
		return len(orderMap)
	}
	slices.SortFunc(cats, func(a, b string) int {
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra - rb
		}
		return strings.Compare(a, b)
	})
	orderedGroups := make([]group, 0, len(cats))
	for _, cat := range cats {
		name := cat
		if name == "" {
			name = "Current View"
		}
		orderedGroups = append(orderedGroups, group{name: name, hints: groups[cat]})
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
