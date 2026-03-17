package ui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ConfirmResultMsg is emitted when the user responds to a confirmation dialog.
type ConfirmResultMsg struct {
	Confirmed bool
	Action    string
}

// Confirm is a yes/no confirmation dialog overlay.
type Confirm struct {
	message string
	action  string
	visible bool
}

// NewConfirm creates a hidden confirmation dialog.
func NewConfirm() Confirm {
	return Confirm{}
}

// Show displays the confirmation dialog with the given message and action ID.
func (c *Confirm) Show(message, action string) {
	c.message = message
	c.action = action
	c.visible = true
}

// Hide dismisses the dialog.
func (c *Confirm) Hide() {
	c.visible = false
}

// Visible returns whether the dialog is showing.
func (c Confirm) Visible() bool {
	return c.visible
}

// Update handles y/n/enter/esc input when the dialog is visible.
func (c Confirm) Update(msg tea.Msg) (Confirm, tea.Cmd) {
	if !c.visible {
		return c, nil
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "y", "enter":
			c.visible = false
			action := c.action
			return c, func() tea.Msg {
				return ConfirmResultMsg{Confirmed: true, Action: action}
			}
		case "n", "esc":
			c.visible = false
			action := c.action
			return c, func() tea.Msg {
				return ConfirmResultMsg{Confirmed: false, Action: action}
			}
		}
	}
	return c, nil
}

// View renders the confirmation dialog box.
func (c Confirm) View() string {
	if !c.visible {
		return ""
	}
	t := ActiveTheme

	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Warning).
		Padding(1, 3).
		Align(lipgloss.Center)

	hint := lipgloss.NewStyle().
		Foreground(t.Muted).
		Render("[y]es / [n]o")

	return style.Render(c.message + "\n\n" + hint)
}
