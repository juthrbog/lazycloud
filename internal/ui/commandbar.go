package ui

import (
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
	input     string
	visible   bool
	commands  []CommandEntry
	filtered  []int  // indices into commands matching input
	selected  int    // -1 = no selection, 0+ = suggestion index
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
	c.input = ""
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
			if c.input == "" {
				c.visible = false
				return c, func() tea.Msg {
					return CommandBarResultMsg{Cancelled: true}
				}
			}
			c.visible = false
			value := c.input
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
				c.input = c.commands[c.filtered[idx]].Name
				c.selected = -1
				c.histIdx = -1
				c.rebuildFiltered()
			}
			return c, nil

		case "up":
			if len(c.filtered) > 0 && c.selected > 0 {
				c.selected--
			} else if len(c.filtered) > 0 && c.selected == 0 {
				c.selected = -1 // deselect
			} else if c.selected == -1 && len(c.history) > 0 {
				// Start browsing history
				if c.histIdx == -1 {
					c.histDraft = c.input
					c.histIdx = len(c.history) - 1
				} else if c.histIdx > 0 {
					c.histIdx--
				}
				c.input = c.history[c.histIdx]
				c.rebuildFiltered()
			}
			return c, nil

		case "down":
			if c.histIdx >= 0 {
				// Browsing history forward
				if c.histIdx < len(c.history)-1 {
					c.histIdx++
					c.input = c.history[c.histIdx]
				} else {
					// Back to draft
					c.histIdx = -1
					c.input = c.histDraft
				}
				c.rebuildFiltered()
			} else if c.selected >= 0 && c.selected < len(c.filtered)-1 {
				c.selected++
			} else if c.selected == -1 && len(c.filtered) > 0 {
				c.selected = 0
			}
			return c, nil

		case "backspace":
			if len(c.input) > 0 {
				c.input = c.input[:len(c.input)-1]
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
			c.input = ""
			c.selected = -1
			c.histIdx = -1
			c.rebuildFiltered()
			return c, nil

		default:
			// Single printable character
			if len(key) == 1 && key[0] >= 32 && key[0] < 127 {
				c.input += key
				c.selected = -1
				c.histIdx = -1
				c.rebuildFiltered()
				return c, nil
			}
		}
	}
	return c, nil
}

// ViewInput renders the command input line (replaces the status bar).
func (c CommandBar) ViewInput(width int) string {
	if !c.visible {
		return ""
	}
	t := ActiveTheme

	prefix := lipgloss.NewStyle().Foreground(t.Accent).Bold(true).Render(":")
	inputText := lipgloss.NewStyle().Foreground(t.Text).Render(c.input)
	cursor := lipgloss.NewStyle().Foreground(t.Accent).Render("█")
	hint := lipgloss.NewStyle().Foreground(t.Muted).Render("  tab complete  ↑↓ history  enter execute  esc cancel")

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
	t := ActiveTheme

	nameStyle := lipgloss.NewStyle().Foreground(t.Text).Width(16)
	descStyle := lipgloss.NewStyle().Foreground(t.Muted)
	selectedNameStyle := lipgloss.NewStyle().Foreground(t.BrightText).Bold(true).Width(16)
	selectedDescStyle := lipgloss.NewStyle().Foreground(t.Text)
	indicatorSelected := lipgloss.NewStyle().Foreground(t.Accent).Render("▸ ")
	indicatorNormal := "  "

	limit := len(c.filtered)
	if limit > maxSuggestions {
		limit = maxSuggestions
	}

	var b strings.Builder
	for i := 0; i < limit; i++ {
		entry := c.commands[c.filtered[i]]
		if i == c.selected {
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
		BorderForeground(t.Secondary).
		Padding(0, 1)

	return box.Render(b.String())
}

// rebuildFiltered updates the filtered list based on current input.
func (c *CommandBar) rebuildFiltered() {
	c.filtered = c.filtered[:0]
	query := strings.ToLower(c.input)

	if query == "" {
		for i := range c.commands {
			c.filtered = append(c.filtered, i)
		}
		return
	}

	var prefix, contains, fuzzyOnly []int
	for i, cmd := range c.commands {
		name := strings.ToLower(cmd.Name)
		desc := strings.ToLower(cmd.Description)

		// Also check aliases
		aliasMatch := false
		for _, alias := range cmd.Aliases {
			a := strings.ToLower(alias)
			if strings.HasPrefix(a, query) || strings.Contains(a, query) {
				aliasMatch = true
				break
			}
		}

		if strings.HasPrefix(name, query) {
			prefix = append(prefix, i)
		} else if aliasMatch || strings.Contains(name, query) || strings.Contains(desc, query) {
			contains = append(contains, i)
		} else if cmdFuzzyMatch(name, query) || cmdFuzzyMatch(desc, query) {
			fuzzyOnly = append(fuzzyOnly, i)
		}
	}

	c.filtered = append(c.filtered, prefix...)
	c.filtered = append(c.filtered, contains...)
	c.filtered = append(c.filtered, fuzzyOnly...)

	if c.selected >= len(c.filtered) {
		c.selected = -1
	}
}

// cmdFuzzyMatch checks if all chars in pattern appear in s in order.
func cmdFuzzyMatch(s, pattern string) bool {
	pi := 0
	for i := 0; i < len(s) && pi < len(pattern); i++ {
		if s[i] == pattern[pi] {
			pi++
		}
	}
	return pi == len(pattern)
}
