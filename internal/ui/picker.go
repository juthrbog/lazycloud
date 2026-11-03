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
	hint     string         // key hint under the options; "" for the default
	visible  bool

	// maxHeight is the most rows the whole dialog may use, border
	// included. 0 means no limit.
	maxHeight int
	// offset is the first option row on screen, as a position in the
	// filtered list. It moves so that the cursor is always on screen.
	offset int
}

// Rows of the dialog that are not option rows: the top and bottom border,
// the title, and the filter line. The full layout also has a blank line
// over the options, and a blank line and the hint under them.
const (
	pickerCompactChrome = 4
	pickerFullChrome    = pickerCompactChrome + 3
)

// pickerLayout says how the picker uses its rows.
type pickerLayout struct {
	rows     int  // option rows on screen
	scroll   bool // show the "more" lines, because not all options fit
	showHint bool // show the blank lines and the key hint
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
	p.hint = ""
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
	p.offset = 0
	p.keepCursorVisible()
}

// SetMaxHeight sets the most rows the dialog may use, border included.
// The app gives it the terminal height. 0 means no limit.
func (p *Picker) SetMaxHeight(h int) {
	p.maxHeight = max(h, 0)
	p.keepCursorVisible()
}

// layout finds how many option rows fit in maxHeight. When all options do
// not fit, one line above and one line below the options say how many
// more there are. When the height is very small, the blank lines and the
// hint go first, and then the "more" lines. There is always at least one option row, so on a tiny
// terminal the dialog can be taller than maxHeight.
func (p Picker) layout() pickerLayout {
	n := max(len(p.filtered), 1) // "no matches" uses one row
	if p.maxHeight <= 0 || n <= p.maxHeight-pickerFullChrome {
		return pickerLayout{rows: n, showHint: true}
	}
	if rows := p.maxHeight - pickerFullChrome - 2; rows >= 1 {
		return pickerLayout{rows: rows, scroll: true, showHint: true}
	}
	if n <= p.maxHeight-pickerCompactChrome {
		return pickerLayout{rows: n}
	}
	if rows := p.maxHeight - pickerCompactChrome - 2; rows >= 1 {
		return pickerLayout{rows: rows, scroll: true}
	}
	return pickerLayout{rows: max(p.maxHeight-pickerCompactChrome, 1)}
}

// keepCursorVisible moves the window of option rows so that the cursor is
// in it, and so that the window does not go past the end of the list.
func (p *Picker) keepCursorVisible() {
	rows := p.layout().rows
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+rows {
		p.offset = p.cursor - rows + 1
	}
	p.offset = min(p.offset, max(len(p.filtered)-rows, 0))
	p.offset = max(p.offset, 0)
}

// SetHint replaces the key hint under the options until the next Show.
func (p *Picker) SetHint(hint string) {
	p.hint = hint
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
			p.keepCursorVisible()
			return p, nil
		case "up", "ctrl+p":
			if p.cursor > 0 {
				p.cursor--
			}
			p.keepCursorVisible()
			return p, nil
		case "pgdown":
			p.cursor = min(p.cursor+p.layout().rows, max(len(p.filtered)-1, 0))
			p.keepCursorVisible()
			return p, nil
		case "pgup":
			p.cursor = max(p.cursor-p.layout().rows, 0)
			p.keepCursorVisible()
			return p, nil
		case "home":
			p.cursor = 0
			p.keepCursorVisible()
			return p, nil
		case "end":
			p.cursor = max(len(p.filtered)-1, 0)
			p.keepCursorVisible()
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
			switch {
			case strings.HasPrefix(label, query) || strings.HasPrefix(value, query):
				prefix = append(prefix, i)
			case strings.Contains(label, query) || strings.Contains(value, query):
				contains = append(contains, i)
			case fuzzyMatch(label, query) || fuzzyMatch(value, query):
				fuzzyOnly = append(fuzzyOnly, i)
			}
		}
		p.filtered = append(p.filtered, prefix...)
		p.filtered = append(p.filtered, contains...)
		p.filtered = append(p.filtered, fuzzyOnly...)
	}
	// Reset cursor to the top match when the filter changes.
	p.cursor = 0
	p.offset = 0
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
	b.WriteString(titleStyle.Render(Sanitize(p.title)) + "\n")

	// Search input
	if p.filter != "" {
		b.WriteString(filterStyle.Render("/ "+p.filter) + "\n")
	} else {
		b.WriteString(dimStyle.Render("type to search...") + "\n")
	}
	lay := p.layout()
	if lay.showHint {
		b.WriteString("\n")
	}

	// The window of options on screen. View has a value receiver, so it
	// fixes the offset on a copy in case the height changed since Update.
	p.keepCursorVisible()
	end := min(p.offset+lay.rows, len(p.filtered))
	if lay.scroll {
		b.WriteString(dimStyle.Render(moreLine("↑", p.offset)) + "\n")
	}

	// When the list scrolls, pad each option row to the widest label in
	// the whole filtered list. Then the box keeps the same width, and it
	// does not move left and right as long labels scroll in and out.
	padTo := 0
	if lay.scroll {
		for _, fi := range p.filtered {
			padTo = max(padTo, lipgloss.Width(normalStyle.Render("  "+Sanitize(p.options[fi].Label))))
		}
	}

	// Options list
	if len(p.filtered) == 0 {
		b.WriteString(dimStyle.Render("  no matches") + "\n")
	} else {
		for i := p.offset; i < end; i++ {
			opt := p.options[p.filtered[i]]
			indicator := "  "
			style := normalStyle
			if i == p.cursor {
				indicator = "▸ "
				style = selectedStyle
			}
			line := fmt.Sprintf("%s%s", indicator, Sanitize(opt.Label))
			row := style.Render(line)
			if pad := padTo - lipgloss.Width(row); pad > 0 {
				row += strings.Repeat(" ", pad)
			}
			b.WriteString(row + "\n")
		}
	}
	if lay.scroll {
		b.WriteString(dimStyle.Render(moreLine("↓", len(p.filtered)-end)) + "\n")
	}

	if lay.showHint {
		hint := p.hint
		if hint == "" {
			hint = "↑↓ navigate  enter select  ctrl+x clear  esc cancel"
		}
		b.WriteString("\n" + hintStyle.Render(hint))
	}

	return s.DialogBorder.Render(strings.TrimSuffix(b.String(), "\n"))
}

// moreLine says how many options are hidden above or below the window.
// It is blank when there are none, so the dialog keeps the same height.
func moreLine(arrow string, n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("  %s %d more", arrow, n)
}
