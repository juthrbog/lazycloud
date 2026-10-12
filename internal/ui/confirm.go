package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// ConfirmResultMsg is emitted when the user responds to a confirmation dialog.
type ConfirmResultMsg struct {
	Confirmed bool
	Action    string
}

// defaultPhrase is the text the user types when a request names no phrase.
const defaultPhrase = "confirm"

// Confirm is a confirmation dialog. In normal mode, the user must type a
// phrase ("confirm" unless the request names one) and press enter. In quick
// mode, a single y/n keypress suffices.
type Confirm struct {
	message string
	action  string
	phrase  string
	input   textinput.Model
	visible bool
	quick   bool   // true = y/n mode, false = type-the-phrase mode
	err     string // shown when user types wrong text
}

// NewConfirm creates a hidden confirmation dialog.
func NewConfirm() Confirm {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.Placeholder = "type 'confirm' to proceed"
	return Confirm{input: ti}
}

// Show displays the type-to-confirm dialog with the given message and action
// ID. The user must type phrase, or "confirm" when phrase is empty.
func (c *Confirm) Show(message, action, phrase string) {
	if phrase == "" {
		phrase = defaultPhrase
	}
	c.message = message
	c.action = action
	c.phrase = phrase
	c.err = ""
	c.visible = true
	c.quick = false
	c.input.Placeholder = fmt.Sprintf("type '%s' to proceed", phrase)
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
			// Only an explicit "y" confirms. Enter takes the safe default
			// (cancel), like a [y/N] shell prompt.
			switch msg.String() {
			case "y", "Y":
				c.visible = false
				return c, func() tea.Msg {
					return ConfirmResultMsg{Confirmed: true, Action: action}
				}
			case "n", "N", "esc", "enter":
				c.visible = false
				return c, func() tea.Msg {
					return ConfirmResultMsg{Confirmed: false, Action: action}
				}
			}
			return c, nil
		}

		switch msg.String() {
		case "enter":
			if strings.EqualFold(strings.TrimSpace(c.input.Value()), c.phrase) {
				c.visible = false
				c.input.Blur()
				action := c.action
				return c, func() tea.Msg {
					return ConfirmResultMsg{Confirmed: true, Action: action}
				}
			}
			c.err = fmt.Sprintf("Type '%s' to proceed", c.phrase)
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
			keyStyle.Render("n/enter") + descStyle.Render(" cancel")
	} else {
		content += c.input.View() + "\n"
		if c.err != "" {
			content += s.Error.Render(c.err) + "\n"
		}
		content += "\n" + s.Muted.Render(fmt.Sprintf("type '%s' + enter to proceed  esc cancel", c.phrase))
	}

	box := s.DialogErrorBorder

	return box.Render(content)
}
