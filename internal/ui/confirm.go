package ui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// ConfirmResultMsg is emitted when the user responds to a confirmation dialog.
type ConfirmResultMsg struct {
	Confirmed bool
	Action    string
}

// Confirm is a confirmation dialog. In normal mode, the user must type
// "confirm" and press enter. In quick mode, a single y/n keypress suffices.
type Confirm struct {
	message string
	action  string
	input   textinput.Model
	visible bool
	quick   bool   // true = y/n mode, false = type "confirm" mode
	err     string // shown when user types wrong text
}

// NewConfirm creates a hidden confirmation dialog.
func NewConfirm() Confirm {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.Placeholder = "type 'confirm' to proceed"
	return Confirm{input: ti}
}

// Show displays the type-to-confirm dialog with the given message and action ID.
func (c *Confirm) Show(message, action string) {
	c.message = message
	c.action = action
	c.err = ""
	c.visible = true
	c.quick = false
	c.input.SetValue("")
	c.input.Focus()
}

// ShowQuick displays a y/n confirmation dialog for non-destructive actions.
func (c *Confirm) ShowQuick(message, action string) {
	c.message = message
	c.action = action
	c.err = ""
	c.visible = true
	c.quick = true
	c.input.Blur()
}

// Hide dismisses the dialog.
func (c *Confirm) Hide() {
	c.visible = false
	c.input.Blur()
}

// Visible returns whether the dialog is showing.
func (c Confirm) Visible() bool {
	return c.visible
}

// Update handles text input and confirmation logic.
func (c Confirm) Update(msg tea.Msg) (Confirm, tea.Cmd) {
	if !c.visible {
		return c, nil
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if c.quick {
			action := c.action
			switch msg.String() {
			case "y", "Y", "enter":
				c.visible = false
				return c, func() tea.Msg {
					return ConfirmResultMsg{Confirmed: true, Action: action}
				}
			case "n", "N", "esc":
				c.visible = false
				return c, func() tea.Msg {
					return ConfirmResultMsg{Confirmed: false, Action: action}
				}
			}
			return c, nil
		}

		switch msg.String() {
		case "enter":
			if strings.EqualFold(strings.TrimSpace(c.input.Value()), "confirm") {
				c.visible = false
				c.input.Blur()
				action := c.action
				return c, func() tea.Msg {
					return ConfirmResultMsg{Confirmed: true, Action: action}
				}
			}
			c.err = "Type 'confirm' to proceed"
			return c, nil
		case "esc":
			c.visible = false
			c.input.Blur()
			action := c.action
			return c, func() tea.Msg {
				return ConfirmResultMsg{Confirmed: false, Action: action}
			}
		default:
			c.err = "" // clear error on new input
		}
	}

	var cmd tea.Cmd
	c.input, cmd = c.input.Update(msg)
	return c, cmd
}

// View renders the confirmation dialog box.
func (c Confirm) View() string {
	if !c.visible {
		return ""
	}
	s := S

	content := s.ModeIndicator.Render(c.message) + "\n\n"

	if c.quick {
		keyStyle := s.StatusKey
		descStyle := s.Muted
		content += keyStyle.Render("y") + descStyle.Render(" yes") +
			descStyle.Render("  ") +
			keyStyle.Render("n") + descStyle.Render(" cancel")
	} else {
		content += c.input.View() + "\n"
		if c.err != "" {
			content += s.Error.Render(c.err) + "\n"
		}
		content += "\n" + s.Muted.Render("type 'confirm' + enter to proceed  esc cancel")
	}

	box := s.DialogErrorBorder

	return box.Render(content)
}
