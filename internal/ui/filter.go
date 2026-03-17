package ui

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// FilterChangedMsg is emitted when the filter text changes.
type FilterChangedMsg struct {
	Text string
}

// Filter provides an inline search input activated by "/".
type Filter struct {
	input  textinput.Model
	active bool
	width  int
}

// NewFilter creates an inactive filter.
func NewFilter() Filter {
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.Placeholder = "filter..."
	return Filter{input: ti}
}

// Activate shows and focuses the filter input.
func (f *Filter) Activate() {
	f.active = true
	f.input.Focus()
}

// Deactivate hides the filter and clears its value.
func (f *Filter) Deactivate() {
	f.active = false
	f.input.SetValue("")
	f.input.Blur()
}

// DeactivateKeepValue hides the filter but keeps the text applied.
func (f *Filter) DeactivateKeepValue() {
	f.active = false
	f.input.Blur()
}

// Active returns whether the filter is currently shown.
func (f Filter) Active() bool {
	return f.active
}

// Value returns the current filter text.
func (f Filter) Value() string {
	return f.input.Value()
}

// SetWidth sets the filter input width.
func (f *Filter) SetWidth(w int) {
	f.width = w
	f.input.SetWidth(w - 4)
}

// Update handles input messages when the filter is active.
func (f Filter) Update(msg tea.Msg) (Filter, tea.Cmd) {
	if !f.active {
		return f, nil
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc":
			f.Deactivate()
			return f, func() tea.Msg { return FilterChangedMsg{Text: ""} }
		case "enter":
			f.DeactivateKeepValue()
			return f, nil
		}
	}

	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)

	text := f.input.Value()
	return f, tea.Batch(cmd, func() tea.Msg {
		return FilterChangedMsg{Text: text}
	})
}

// View renders the filter input. Returns "" when inactive.
func (f Filter) View() string {
	if !f.active {
		return ""
	}
	return S.FilterPrompt.Render("/") + " " + f.input.View()
}
