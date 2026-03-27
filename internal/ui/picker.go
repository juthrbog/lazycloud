package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
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
		case "ctrl+x":
			// Clear/reset — dismiss picker with a special value
			p.visible = false
			id := p.id
			return p, func() tea.Msg {
				return PickerResultMsg{ID: id, Selected: -2, Value: "_clear"}
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
		// Rank: prefix > contains > fuzzy-only
		var prefix, contains, fuzzyOnly []int
		for i, opt := range p.options {
			label := strings.ToLower(opt.Label)
			value := strings.ToLower(opt.Value)
			if strings.HasPrefix(label, query) || strings.HasPrefix(value, query) {
				prefix = append(prefix, i)
			} else if strings.Contains(label, query) || strings.Contains(value, query) {
				contains = append(contains, i)
			} else if fuzzyMatch(label, query) || fuzzyMatch(value, query) {
				fuzzyOnly = append(fuzzyOnly, i)
			}
		}
		p.filtered = append(p.filtered, prefix...)
		p.filtered = append(p.filtered, contains...)
		p.filtered = append(p.filtered, fuzzyOnly...)
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
	s := S

	titleStyle := s.Title
	filterStyle := s.FilterPrompt
	normalStyle := s.PickerOption
	selectedStyle := s.PickerOptionSelected
	dimStyle := s.Muted
	hintStyle := s.Muted

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

	b.WriteString("\n" + hintStyle.Render("↑↓ navigate  enter select  ctrl+x clear  esc cancel"))

	return s.DialogBorder.Render(b.String())
}
