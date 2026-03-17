package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// PickerResultMsg is emitted when the user selects an option or cancels.
type PickerResultMsg struct {
	ID       string // picker identifier
	Selected int    // -1 if cancelled
	Value    string // selected option value, empty if cancelled
}

// PickerOption is a single selectable item in the picker.
type PickerOption struct {
	Label string
	Value string
}

// Picker is a popup selection dialog with j/k navigation and fuzzy search.
type Picker struct {
	id       string
	title    string
	options  []PickerOption // all options
	filtered []int          // indices into options that match the filter
	cursor   int            // position within filtered list
	filter   string         // current search text
	visible  bool
}

// NewPicker creates a hidden picker.
func NewPicker() Picker {
	return Picker{}
}

// Show displays the picker with the given options. The cursor starts on initialIdx.
func (p *Picker) Show(id, title string, options []PickerOption, initialIdx int) {
	p.id = id
	p.title = title
	p.options = options
	p.filter = ""
	p.visible = true
	p.rebuildFiltered()

	// Place cursor on the initialIdx within the filtered list
	p.cursor = 0
	for i, fi := range p.filtered {
		if fi == initialIdx {
			p.cursor = i
			break
		}
	}
}

// Hide dismisses the picker.
func (p *Picker) Hide() {
	p.visible = false
}

// Visible returns whether the picker is currently shown.
func (p Picker) Visible() bool {
	return p.visible
}

// Update handles navigation, search, and selection input.
func (p Picker) Update(msg tea.Msg) (Picker, tea.Cmd) {
	if !p.visible {
		return p, nil
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		key := msg.String()

		switch key {
		case "down", "ctrl+n":
			if p.cursor < len(p.filtered)-1 {
				p.cursor++
			}
			return p, nil
		case "up", "ctrl+p":
			if p.cursor > 0 {
				p.cursor--
			}
			return p, nil
		case "enter":
			if len(p.filtered) == 0 {
				return p, nil
			}
			p.visible = false
			id := p.id
			origIdx := p.filtered[p.cursor]
			value := p.options[origIdx].Value
			return p, func() tea.Msg {
				return PickerResultMsg{ID: id, Selected: origIdx, Value: value}
			}
		case "esc":
			p.visible = false
			id := p.id
			return p, func() tea.Msg {
				return PickerResultMsg{ID: id, Selected: -1}
			}
		case "backspace":
			if len(p.filter) > 0 {
				p.filter = p.filter[:len(p.filter)-1]
				p.rebuildFiltered()
			}
			return p, nil
		default:
			// Single printable character → append to filter
			if len(key) == 1 && key[0] >= 32 && key[0] < 127 {
				p.filter += key
				p.rebuildFiltered()
				return p, nil
			}
		}
	}
	return p, nil
}

func (p *Picker) rebuildFiltered() {
	p.filtered = p.filtered[:0]
	query := strings.ToLower(p.filter)
	if query == "" {
		for i := range p.options {
			p.filtered = append(p.filtered, i)
		}
	} else {
		// Value matches first, then label-only matches
		var labelOnly []int
		for i, opt := range p.options {
			if fuzzyMatch(strings.ToLower(opt.Value), query) {
				p.filtered = append(p.filtered, i)
			} else if fuzzyMatch(strings.ToLower(opt.Label), query) {
				labelOnly = append(labelOnly, i)
			}
		}
		p.filtered = append(p.filtered, labelOnly...)
	}
	if p.cursor >= len(p.filtered) {
		p.cursor = max(0, len(p.filtered)-1)
	}
}

// fuzzyMatch returns true if all characters in pattern appear in s in order.
func fuzzyMatch(s, pattern string) bool {
	pi := 0
	for i := 0; i < len(s) && pi < len(pattern); i++ {
		if s[i] == pattern[pi] {
			pi++
		}
	}
	return pi == len(pattern)
}

// View renders the picker as a bordered popup.
func (p Picker) View() string {
	if !p.visible {
		return ""
	}
	t := ActiveTheme

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(t.Primary)

	filterStyle := lipgloss.NewStyle().
		Foreground(t.Accent)

	normalStyle := lipgloss.NewStyle().
		Foreground(t.Text).
		Padding(0, 2)

	selectedStyle := lipgloss.NewStyle().
		Foreground(t.BrightText).
		Background(t.Overlay).
		Bold(true).
		Padding(0, 2)

	dimStyle := lipgloss.NewStyle().
		Foreground(t.Muted)

	hintStyle := lipgloss.NewStyle().
		Foreground(t.Muted)

	var b strings.Builder
	b.WriteString(titleStyle.Render(p.title) + "\n")

	// Search input
	if p.filter != "" {
		b.WriteString(filterStyle.Render("/ "+p.filter) + "\n")
	} else {
		b.WriteString(dimStyle.Render("type to search...") + "\n")
	}
	b.WriteString("\n")

	// Options list
	if len(p.filtered) == 0 {
		b.WriteString(dimStyle.Render("  no matches") + "\n")
	} else {
		for i, fi := range p.filtered {
			opt := p.options[fi]
			indicator := "  "
			style := normalStyle
			if i == p.cursor {
				indicator = "▸ "
				style = selectedStyle
			}
			line := fmt.Sprintf("%s%s", indicator, opt.Label)
			b.WriteString(style.Render(line) + "\n")
		}
	}

	b.WriteString("\n" + hintStyle.Render("↑↓ navigate  enter select  esc cancel"))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Primary).
		Padding(1, 3)

	return box.Render(b.String())
}
