package ui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// CommandBarResultMsg is emitted when the user executes or cancels the command bar.
type CommandBarResultMsg struct {
	Value     string // the command text entered
	Cancelled bool   // true if dismissed without executing
}

// CommandEntry is a single command available in the command bar.
type CommandEntry struct {
	Name        string
	Aliases     []string
	Description string
}

// CommandBar is a vim/k9s-style text input at the bottom of the screen
// with autocomplete suggestions and command history.
type CommandBar struct {
	input     queryInput
	visible   bool
	commands  []CommandEntry
	filtered  []int // indices into commands matching input
	selected  int   // -1 = no selection, 0+ = suggestion index
	history   []string
	histIdx   int    // -1 = not browsing history
	histDraft string // input before history browsing started
	width     int
}

const maxSuggestions = 8
const maxHistory = 50

// NewCommandBar creates a hidden command bar.
func NewCommandBar() CommandBar {
	return CommandBar{selected: -1, histIdx: -1}
}

// Show activates the command bar with the given commands.
func (c *CommandBar) Show(commands []CommandEntry, width int) {
	c.commands = commands
	c.width = width
	c.input = queryInput{}
	c.visible = true
	c.selected = -1
	c.histIdx = -1
	c.histDraft = ""
	c.rebuildFiltered()
}

// Hide dismisses the command bar.
func (c *CommandBar) Hide() {
	c.visible = false
}

// SetSize gives the command bar the terminal width.
func (c *CommandBar) SetSize(width, _ int) {
	c.width = width
}

// View returns the suggestions, which the app draws over the views. The
// input line replaces the status bar, so ViewInput draws it.
func (c CommandBar) View() string {
	return c.ViewSuggestions()
}

// Visible returns whether the command bar is currently shown.
func (c CommandBar) Visible() bool {
	return c.visible
}

// AddHistory adds a command to history, deduplicating.
func (c *CommandBar) AddHistory(cmd string) {
	if cmd == "" {
		return
	}
	// Remove previous occurrence
	for i, h := range c.history {
		if h == cmd {
			c.history = append(c.history[:i], c.history[i+1:]...)
			break
		}
	}
	c.history = append(c.history, cmd)
	if len(c.history) > maxHistory {
		c.history = c.history[len(c.history)-maxHistory:]
	}
}

// Update handles input when the command bar is visible.
func (c CommandBar) Update(msg tea.Msg) (CommandBar, tea.Cmd) {
	if !c.visible {
		return c, nil
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		key := msg.String()

		switch key {
		case "enter":
			if c.query() == "" {
				c.visible = false
				return c, func() tea.Msg {
					return CommandBarResultMsg{Cancelled: true}
				}
			}
			c.visible = false
			value := c.query()
			if len(c.filtered) > 0 {
				idx := 0
				if c.selected >= 0 && c.selected < len(c.filtered) {
					idx = c.selected
				}
				value = c.commands[c.filtered[idx]].Name
			}
			return c, func() tea.Msg {
				return CommandBarResultMsg{Value: value}
			}

		case "esc":
			c.visible = false
			return c, func() tea.Msg {
				return CommandBarResultMsg{Cancelled: true}
			}

		case "tab":
			// Fill input with top match (or selected suggestion)
			idx := 0
			if c.selected >= 0 && c.selected < len(c.filtered) {
				idx = c.selected
			}
			if len(c.filtered) > 0 {
				c.input.set(c.commands[c.filtered[idx]].Name)
				c.selected = -1
				c.histIdx = -1
				c.rebuildFiltered()
			}
			return c, nil

		case "up":
			switch {
			case len(c.filtered) > 0 && c.selected > 0:
				c.selected--
			case len(c.filtered) > 0 && c.selected == 0:
				c.selected = -1 // deselect
			case c.selected == -1 && len(c.history) > 0:
				// Start browsing history
				if c.histIdx == -1 {
					c.histDraft = c.input.String()
					c.histIdx = len(c.history) - 1
				} else if c.histIdx > 0 {
					c.histIdx--
				}
				c.input.set(c.history[c.histIdx])
				c.rebuildFiltered()
			}
			return c, nil

		case "down":
			switch {
			case c.histIdx >= 0:
				// Browsing history forward
				if c.histIdx < len(c.history)-1 {
					c.histIdx++
					c.input.set(c.history[c.histIdx])
				} else {
					// Back to draft
					c.histIdx = -1
					c.input.set(c.histDraft)
				}
				c.rebuildFiltered()
			case c.selected >= 0 && c.selected < len(c.filtered)-1:
				c.selected++
			case c.selected == -1 && len(c.filtered) > 1:
				c.selected = 1 // skip implicit top highlight
			case c.selected == -1 && len(c.filtered) == 1:
				c.selected = 0
			}
			return c, nil

		case "backspace":
			if c.input.update(msg) {
				c.selected = -1
				c.histIdx = -1
				c.rebuildFiltered()
			} else {
				// Backspace on empty dismisses (vim convention)
				c.visible = false
				return c, func() tea.Msg {
					return CommandBarResultMsg{Cancelled: true}
				}
			}
			return c, nil

		case "ctrl+u":
			c.input = queryInput{}
			c.selected = -1
			c.histIdx = -1
			c.rebuildFiltered()
			return c, nil

		default:
			c.typed(msg)
		}
	case tea.PasteMsg:
		c.typed(msg)
	}
	return c, nil
}

// typed applies typed or pasted text to the input.
func (c *CommandBar) typed(msg tea.Msg) {
	if c.input.update(msg) {
		c.selected = -1
		c.histIdx = -1
		c.rebuildFiltered()
	}
}

// ViewInput renders the command input line (replaces the status bar).
func (c CommandBar) ViewInput(width int) string {
	if !c.visible {
		return ""
	}
	s := S

	prefix := s.FilterPrompt.Render(":")
	cursor := s.CommandCursor.Render("█")
	hint := s.Muted.Render("  tab complete  ↑↓ history  enter execute  esc cancel")
	// A long input takes the room of the hint, and then shows its end.
	room := width - lipgloss.Width(prefix) - lipgloss.Width(cursor)
	if lipgloss.Width(c.input.String()) > room-lipgloss.Width(hint) {
		hint = ""
	}
	inputText := lipgloss.NewStyle().Foreground(ActiveTheme.Text).Render(c.input.view(room))

	bar := prefix + inputText + cursor + hint

	barWidth := lipgloss.Width(bar)
	if width > barWidth {
		padding := lipgloss.NewStyle().Width(width - barWidth).Render("")
		bar += padding
	}

	return bar
}

// ViewSuggestions renders the suggestion dropdown. Returns "" if no suggestions.
func (c CommandBar) ViewSuggestions() string {
	if !c.visible || len(c.filtered) == 0 {
		return ""
	}
	s := S

	nameStyle := s.SuggestionName
	descStyle := s.SuggestionDesc
	selectedNameStyle := s.SuggestionSelected
	selectedDescStyle := lipgloss.NewStyle().Foreground(ActiveTheme.Text)
	indicatorSelected := s.CommandCursor.Render("▸ ")
	indicatorNormal := "  "

	limit := min(len(c.filtered), maxSuggestions)

	// Default to top match when no manual selection (matches Enter behavior)
	effectiveSelected := c.selected
	if effectiveSelected < 0 && len(c.filtered) > 0 {
		effectiveSelected = 0
	}

	var b strings.Builder
	for i := range limit {
		entry := c.commands[c.filtered[i]]
		if i == effectiveSelected {
			b.WriteString(indicatorSelected + selectedNameStyle.Render(entry.Name) + selectedDescStyle.Render(entry.Description))
		} else {
			b.WriteString(indicatorNormal + nameStyle.Render(entry.Name) + descStyle.Render(entry.Description))
		}
		if i < limit-1 {
			b.WriteString("\n")
		}
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ActiveTheme.Secondary).
		Padding(0, 1)

	return box.Render(b.String())
}

// query returns the input without the spaces around it. A space before or
// after a command name is not part of the command.
func (c CommandBar) query() string {
	return strings.TrimSpace(c.input.String())
}

// rebuildFiltered updates the filtered list based on current input. A
// prefix counts only on the name of a command.
func (c *CommandBar) rebuildFiltered() {
	c.filtered = rankMatches(c.filtered, c.query(), len(c.commands), func(i int) (lead, rest []string) {
		cmd := c.commands[i]
		return []string{cmd.Name}, slices.Concat(cmd.Aliases, []string{cmd.Description})
	})
	if c.selected >= len(c.filtered) {
		c.selected = -1
	}
}
