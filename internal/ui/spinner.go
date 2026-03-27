package ui

import (
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Spinner wraps a bubbles spinner with show/hide and a label.
type Spinner struct {
	inner   spinner.Model
	label   string
	visible bool
}

// NewSpinner creates a Spinner with the given label.
func NewSpinner(label string) Spinner {
	s := spinner.New(spinner.WithSpinner(spinner.Dot))
	return Spinner{
		inner:   s,
		label:   label,
		visible: true,
	}
}

// Show makes the spinner visible with a new label.
func (s *Spinner) Show(label string) {
	s.label = label
	s.visible = true
}

// Hide makes the spinner invisible.
func (s *Spinner) Hide() {
	s.visible = false
}

// Visible returns whether the spinner is currently shown.
func (s Spinner) Visible() bool {
	return s.visible
}

// Tick returns the spinner's tick command for use in Init().
func (s Spinner) Tick() tea.Cmd {
	return s.inner.Tick
}

// Update handles spinner tick messages.
func (s Spinner) Update(msg tea.Msg) (Spinner, tea.Cmd) {
	if !s.visible {
		return s, nil
	}
	var cmd tea.Cmd
	s.inner, cmd = s.inner.Update(msg)
	return s, cmd
}

// View renders the spinner with its label. Returns "" when not visible.
func (s Spinner) View() string {
	if !s.visible {
		return ""
	}
	// Apply theme at render time so colors update on theme switch
	s.inner.Style = lipgloss.NewStyle().Foreground(ActiveTheme.Primary)
	return s.inner.View() + " " + S.Muted.Render(s.label)
}
