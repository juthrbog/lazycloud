package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

// Pin tests for the jump design (docs/design/jump.md). H1, H6 and H10 are
// pinned in internal/app, H2, H3 and H5 in internal/views, and H11 in
// internal/aws.

// H7, fixed in PR 1: the input keeps at most 256 runes and drops the rest of
// a paste with no sign, so a long S3 object ARN is cut.
func TestJumpHazard_InputStopsAt256Runes(t *testing.T) {
	long := "arn:aws:s3:::logs-bucket/" + strings.Repeat("a", 300)
	var q queryInput
	q.add(long)
	assert.Len(t, []rune(q.String()), 256, "the rest of the paste is gone")
	assert.Equal(t, long[:256], q.String())
}

// The input keeps format characters such as U+200B and U+00A0 on purpose,
// and turns a line break into a space. The jump parser cleans them
// (docs/design/jump.md, "What the parser accepts"), so this stays.
func TestQueryInputKeepsFormatCharacters(t *testing.T) {
	var q queryInput
	q.set("arn:aws:lambda:us-east-1:1:function:\u200borders\u00a0")
	assert.Equal(t, "arn:aws:lambda:us-east-1:1:function:\u200borders\u00a0", q.String())
	q.set("arn:aws:lambda:us-east-1:1:\nfunction:orders\n")
	assert.Equal(t, "arn:aws:lambda:us-east-1:1: function:orders", q.String())
}

// H8, a guard: a bare word can match a command by fuzzy match, so Enter
// runs that command. "api" runs "lambda". So a name after a bare ":" can
// never be a jump; it needs ":go". This stays true.
func TestJumpGuard_BareWordRunsAFuzzyCommand(t *testing.T) {
	cmds := append(testCommands(),
		CommandEntry{Name: "lambda", Aliases: []string{"functions", "fn"}, Description: "Lambda functions (:lambda NAME to open a function)"})
	c := NewCommandBar()
	c.Show(cmds, 120)
	for _, r := range "api" {
		c, _ = c.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	_, cmd := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, CommandBarResultMsg{Value: "lambda"}, cmd())

	// An ARN matches no command, so it goes as typed.
	c = NewCommandBar()
	c.Show(cmds, 120)
	arn := "arn:aws:lambda:us-east-1:1:function:orders"
	for _, r := range arn {
		c, _ = c.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	_, cmd = c.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, CommandBarResultMsg{Value: arn}, cmd())
}
