package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func init() {
	RebuildStyles()
}

func testCommands() []CommandEntry {
	return []CommandEntry{
		{Name: "ec2", Aliases: []string{"instances"}, Description: "EC2 instances"},
		{Name: "ec2/amis", Aliases: []string{"amis"}, Description: "EC2 AMIs"},
		{Name: "s3", Aliases: []string{"buckets"}, Description: "S3 buckets"},
		{Name: "quit", Aliases: []string{"q"}, Description: "Exit LazyCloud"},
	}
}

func TestCommandBarShowAndHide(t *testing.T) {
	c := NewCommandBar()
	assert.False(t, c.Visible())

	c.Show(testCommands(), 120)
	assert.True(t, c.Visible())

	c.Hide()
	assert.False(t, c.Visible())
}

func TestCommandBarEnterExecutes(t *testing.T) {
	c := NewCommandBar()
	c.Show(testCommands(), 120)

	// Type "ec2"
	c, _ = c.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	c, _ = c.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	c, _ = c.Update(tea.KeyPressMsg{Code: '2', Text: "2"})

	assert.Equal(t, "ec2", c.input)

	c, cmd := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.False(t, c.Visible())
	assert.NotNil(t, cmd)

	msg := cmd().(CommandBarResultMsg)
	assert.Equal(t, "ec2", msg.Value)
	assert.False(t, msg.Cancelled)
}

func TestCommandBarEscCancels(t *testing.T) {
	c := NewCommandBar()
	c.Show(testCommands(), 120)

	c, cmd := c.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.False(t, c.Visible())

	msg := cmd().(CommandBarResultMsg)
	assert.True(t, msg.Cancelled)
}

func TestCommandBarBackspaceOnEmptyDismisses(t *testing.T) {
	c := NewCommandBar()
	c.Show(testCommands(), 120)

	c, cmd := c.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	assert.False(t, c.Visible())

	msg := cmd().(CommandBarResultMsg)
	assert.True(t, msg.Cancelled)
}

func TestCommandBarFilterPrefixFirst(t *testing.T) {
	c := NewCommandBar()
	c.Show(testCommands(), 120)

	// Type "ec" — "ec2" should rank before "ec2/amis"
	c, _ = c.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	c, _ = c.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})

	assert.True(t, len(c.filtered) >= 2)
	assert.Equal(t, "ec2", c.commands[c.filtered[0]].Name)
	assert.Equal(t, "ec2/amis", c.commands[c.filtered[1]].Name)
}

func TestCommandBarFilterByAlias(t *testing.T) {
	c := NewCommandBar()
	c.Show(testCommands(), 120)

	// Type "instances" — should match ec2 via alias
	for _, ch := range "instances" {
		c, _ = c.Update(tea.KeyPressMsg{Code: rune(ch), Text: string(ch)})
	}

	assert.True(t, len(c.filtered) > 0)
	assert.Equal(t, "ec2", c.commands[c.filtered[0]].Name)
}

func TestCommandBarTabCompletes(t *testing.T) {
	c := NewCommandBar()
	c.Show(testCommands(), 120)

	// Type "ec" then Tab — should fill with "ec2"
	c, _ = c.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	c, _ = c.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	c, _ = c.Update(tea.KeyPressMsg{Code: tea.KeyTab})

	assert.Equal(t, "ec2", c.input)
	assert.True(t, c.Visible()) // Tab fills but does NOT execute
}

func TestCommandBarTabCompletesSelectedSuggestion(t *testing.T) {
	c := NewCommandBar()
	c.Show(testCommands(), 120)

	// Type "ec", then Down to select ec2/amis, then Tab
	c, _ = c.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	c, _ = c.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	c, _ = c.Update(tea.KeyPressMsg{Code: tea.KeyDown})  // select ec2
	c, _ = c.Update(tea.KeyPressMsg{Code: tea.KeyDown})  // select ec2/amis
	c, _ = c.Update(tea.KeyPressMsg{Code: tea.KeyTab})

	assert.Equal(t, "ec2/amis", c.input)
}

func TestCommandBarHistory(t *testing.T) {
	c := NewCommandBar()
	c.AddHistory("ec2")
	c.AddHistory("s3")

	c.Show(testCommands(), 120)

	// Up arrow recalls most recent ("s3")
	c, _ = c.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	assert.Equal(t, "s3", c.input)

	// Up again recalls "ec2"
	c, _ = c.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	assert.Equal(t, "ec2", c.input)

	// Down returns to "s3"
	c, _ = c.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	assert.Equal(t, "s3", c.input)

	// Down again returns to empty draft
	c, _ = c.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	assert.Equal(t, "", c.input)
}

func TestCommandBarHistoryDeduplicates(t *testing.T) {
	c := NewCommandBar()
	c.AddHistory("ec2")
	c.AddHistory("s3")
	c.AddHistory("ec2")

	assert.Equal(t, []string{"s3", "ec2"}, c.history)
}

func TestCommandBarCtrlUClearsInput(t *testing.T) {
	c := NewCommandBar()
	c.Show(testCommands(), 120)

	c, _ = c.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	c, _ = c.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	assert.Equal(t, "ec", c.input)

	c, _ = c.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	assert.Equal(t, "", c.input)
}

func TestCommandBarEnterOnEmptyDismisses(t *testing.T) {
	c := NewCommandBar()
	c.Show(testCommands(), 120)

	c, cmd := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.False(t, c.Visible())

	msg := cmd().(CommandBarResultMsg)
	assert.True(t, msg.Cancelled)
}

func TestCommandBarViewInputNotVisibleReturnsEmpty(t *testing.T) {
	c := NewCommandBar()
	assert.Equal(t, "", c.ViewInput(120))
}

func TestCommandBarViewSuggestionsNotVisibleReturnsEmpty(t *testing.T) {
	c := NewCommandBar()
	assert.Equal(t, "", c.ViewSuggestions())
}
