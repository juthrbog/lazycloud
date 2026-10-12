package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pressConfirm sends one key to the dialog and returns the result message, if any.
func pressConfirm(t *testing.T, c Confirm, key tea.KeyPressMsg) (Confirm, *ConfirmResultMsg) {
	t.Helper()
	c, cmd := c.Update(key)
	if cmd == nil {
		return c, nil
	}
	res, ok := cmd().(ConfirmResultMsg)
	require.True(t, ok, "expected ConfirmResultMsg")
	return c, &res
}

func TestConfirmQuickKeys(t *testing.T) {
	tests := []struct {
		name      string
		key       tea.KeyPressMsg
		confirmed bool
	}{
		{name: "y confirms", key: tea.KeyPressMsg{Code: 'y', Text: "y"}, confirmed: true},
		{name: "Y confirms", key: tea.KeyPressMsg{Code: 'Y', Text: "Y"}, confirmed: true},
		{name: "n cancels", key: tea.KeyPressMsg{Code: 'n', Text: "n"}},
		{name: "esc cancels", key: tea.KeyPressMsg{Code: tea.KeyEscape}},
		{name: "enter takes the safe default", key: tea.KeyPressMsg{Code: tea.KeyEnter}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewConfirm()
			c.ShowQuick("Discard unsaved changes?", "form_discard")

			c, res := pressConfirm(t, c, tt.key)
			require.NotNil(t, res)
			assert.Equal(t, tt.confirmed, res.Confirmed)
			assert.Equal(t, "form_discard", res.Action)
			assert.False(t, c.Visible())
		})
	}
}

func TestConfirmQuickIgnoresOtherKeys(t *testing.T) {
	c := NewConfirm()
	c.ShowQuick("Continue?", "x")

	c, res := pressConfirm(t, c, tea.KeyPressMsg{Code: 'q', Text: "q"})
	assert.Nil(t, res)
	assert.True(t, c.Visible())
}

func TestConfirmTypedRequiresWord(t *testing.T) {
	c := NewConfirm()
	c.Show("Delete 3 objects?", "delete", "")

	// Enter on an empty input must not confirm.
	c, res := pressConfirm(t, c, tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Nil(t, res)
	assert.True(t, c.Visible())

	for _, r := range "confirm" {
		c, _ = c.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	c, res = pressConfirm(t, c, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, res)
	assert.True(t, res.Confirmed)
	assert.False(t, c.Visible())
}

// C4: a bucket delete asks for the bucket name, and "confirm" does not work.
func TestConfirmShowsWholePlaceholder(t *testing.T) {
	c := NewConfirm()
	c.Show("Delete bucket 'alpha-prod'?", "delete_bucket", "alpha-prod")
	assert.Contains(t, ansi.Strip(c.View()), "> type 'alpha-prod' to proceed")
}

func TestConfirmTypedCustomPhrase(t *testing.T) {
	c := NewConfirm()
	c.Show("Delete bucket 'alpha-prod'?", "delete_bucket", "alpha-prod")
	assert.Contains(t, c.View(), "type 'alpha-prod' + enter")

	for _, r := range "confirm" {
		c, _ = c.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	c, res := pressConfirm(t, c, tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Nil(t, res, "the default word must not confirm")
	assert.Contains(t, c.View(), "Type 'alpha-prod' to proceed")

	c.Show("Delete bucket 'alpha-prod'?", "delete_bucket", "alpha-prod")
	for _, r := range "alpha-prod" {
		c, _ = c.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	_, res = pressConfirm(t, c, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, res)
	assert.True(t, res.Confirmed)
}
