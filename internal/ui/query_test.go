package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover the shared query input and matcher (A9, L12) of the
// picker, the command bar, and the help overlay.

func textKey(s string) tea.KeyPressMsg {
	r, _ := utf8.DecodeRuneInString(s)
	return tea.KeyPressMsg{Code: r, Text: s}
}

var (
	spaceKey     = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	backspaceKey = tea.KeyPressMsg{Code: tea.KeyBackspace}
)

func TestQueryInputTypesText(t *testing.T) {
	var q queryInput
	for _, k := range []tea.KeyPressMsg{textKey("a"), spaceKey, textKey("é"), textKey("日"), textKey("A")} {
		assert.True(t, q.update(k), "%q", k.Text)
	}
	assert.Equal(t, "a é日A", q.String())
	assert.Equal(t, "space", spaceKey.String(), "the old input dropped this key")
}

func TestQueryInputIgnoresShortcuts(t *testing.T) {
	var q queryInput
	for _, k := range []tea.KeyPressMsg{
		{Code: 'a', Mod: tea.ModCtrl},
		{Code: 'x', Mod: tea.ModAlt},
		{Code: 'v', Mod: tea.ModSuper},
		{Code: tea.KeySpace, Mod: tea.ModCtrl},
		{Code: tea.KeyTab},
		{Code: tea.KeyUp},
		{Code: tea.KeyEnter},
	} {
		assert.False(t, q.update(k), k.String())
	}
	assert.Empty(t, q.String())

	shifted := tea.KeyPressMsg{Code: 'a', ShiftedCode: 'A', Text: "A", Mod: tea.ModShift}
	assert.True(t, q.update(shifted), "shift types a capital letter")
	assert.Equal(t, "A", q.String())

	// On Windows, AltGr comes as ctrl+alt with the text it types.
	altGr := tea.KeyPressMsg{Code: '@', Text: "@", Mod: tea.ModCtrl | tea.ModAlt}
	assert.True(t, q.update(altGr), "AltGr types @")
	assert.Equal(t, "A@", q.String())
}

func TestQueryInputPaste(t *testing.T) {
	for _, tc := range []struct {
		name, paste, want string
	}{
		{"plain", "eu-west-2", "eu-west-2"},
		{"line end", "eu-west-2\n", "eu-west-2"},
		{"CRLF line end", "eu-west-2\r\n", "eu-west-2"},
		{"lines and tabs", "a\nb\tc\r\nd", "a b c d"},
		{"escape sequence", "\x1b[31mred\x1b[0m", "[31mred[0m"},
		{"C1 and DEL", "a\u009bb\x7fc", "abc"},
		{"bad UTF-8", "a\xffb", "ab"},
		{"unicode", "Grüße 東京", "Grüße 東京"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var q queryInput
			q.update(tea.PasteMsg{Content: tc.paste})
			assert.Equal(t, tc.want, q.String())
		})
	}

	var q queryInput
	assert.False(t, q.update(tea.PasteMsg{Content: "\x1b\n"}), "a paste with no text changes nothing")
}

func TestQueryInputLimitsLength(t *testing.T) {
	var q queryInput
	q.update(tea.PasteMsg{Content: strings.Repeat("ü", 1000)})
	assert.Equal(t, maxQueryRunes, utf8.RuneCountInString(q.String()))
	assert.False(t, q.update(textKey("x")), "a full input takes no more text")
}

func TestQueryInputBackspaceRemovesOneCharacter(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
	}{
		{"ASCII", "ab", "a"},
		{"two bytes", "aé", "a"},
		{"wide", "a東", "a"},
		{"combining accent", "aé", "a"},
		{"emoji with joiner", "a👩‍💻", "a"},
		{"flag", "a🇩🇪", "a"},
		{"one", "x", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var q queryInput
			q.set(tc.text)
			require.Equal(t, tc.text, q.String())
			assert.True(t, q.update(backspaceKey))
			assert.Equal(t, tc.want, q.String())
		})
	}

	var q queryInput
	assert.False(t, q.update(backspaceKey), "backspace on empty changes nothing")
}

func TestQueryInputViewShowsTheEnd(t *testing.T) {
	var q queryInput
	q.set("short")
	assert.Equal(t, "short", q.view(10))
	assert.Empty(t, q.view(0), "no room shows nothing")
	assert.Empty(t, q.view(-3))

	q.set("abcdefghijklmnopqrstuvwxyz")
	v := q.view(10)
	assert.Equal(t, 10, lipgloss.Width(v))
	assert.Equal(t, "…rstuvwxyz", v)

	// A cut on the left half of a wide character must not make the text
	// one cell too wide, at odd and even widths.
	for _, text := range []string{strings.Repeat("東", 20), "a" + strings.Repeat("日", 30), strings.Repeat("a東b", 10)} {
		q.set(text)
		for w := 1; w <= 30; w++ {
			v := q.view(w)
			assert.LessOrEqual(t, lipgloss.Width(v), w, "%q at width %d", text, w)
			assert.True(t, strings.HasPrefix(v, "…"), "%q at width %d", text, w)
		}
	}
	q.set("ab日本")
	assert.Equal(t, "…本", q.view(4))
}

func TestFuzzyMatch(t *testing.T) {
	assert.True(t, fuzzyMatch("eu-west-2", "ew2"))
	assert.True(t, fuzzyMatch("grüße", "gß"), "runes outside ASCII")
	assert.True(t, fuzzyMatch("東京都", "東都"))
	assert.False(t, fuzzyMatch("grüße", "gu"), "ü is not u")
	assert.False(t, fuzzyMatch("ü¤", "ä"), "the bytes of ä are in ü¤, but ä is not")
	assert.False(t, fuzzyMatch("abc", "ba"), "order counts")
	assert.True(t, fuzzyMatch("abc", ""))
}

func TestRankMatches(t *testing.T) {
	type entry struct{ lead, rest []string }
	entries := []entry{
		{[]string{"zone-a"}, []string{"has east in it"}}, // contains (rest)
		{[]string{"east-1"}, nil},                        // prefix
		{[]string{"e-a-s-t"}, nil},                       // fuzzy
		{[]string{"west"}, []string{"east-alias"}},       // prefix on rest: contains only
		{[]string{"north"}, nil},                         // no match
		{[]string{"EAST-2"}, nil},                        // prefix, any case
	}
	fields := func(i int) (lead, rest []string) { return entries[i].lead, entries[i].rest }

	assert.Equal(t, []int{1, 5, 0, 3, 2}, rankMatches(nil, "East", len(entries), fields))
	assert.Equal(t, []int{0, 1, 2, 3, 4, 5}, rankMatches(nil, "", len(entries), fields))
	assert.Empty(t, rankMatches(nil, "south", len(entries), fields))

	dst := make([]int, 0, 8)
	got := rankMatches(dst, "east", len(entries), fields)
	assert.Equal(t, &dst[:1][0], &got[:1][0], "reuses the storage of dst")
}

// L12: the picker takes space, unicode, and paste.
func TestPickerTakesSpaceUnicodeAndPaste(t *testing.T) {
	p := NewPicker()
	p.Show("Access Mode", []PickerOption{
		{Label: "ReadOnly    Browse only, no mutations", Value: "readonly"},
		{Label: "ReadWrite   Allow create/delete/copy/move", Value: "readwrite"},
		{Label: "Größe", Value: "size"},
	}, 0)

	p = typeText(p, "browse only")
	require.Len(t, p.filtered, 1)
	assert.Equal(t, "readonly", p.options[p.filtered[0]].Value)
	assert.Contains(t, ansi.Strip(p.View()), "/ browse only")

	p.filter = queryInput{}
	p.rebuildFiltered()
	p = typeText(p, "röß")
	require.Len(t, p.filtered, 1)
	assert.Equal(t, "size", p.options[p.filtered[0]].Value)

	p.filter = queryInput{}
	p, _ = p.Update(tea.PasteMsg{Content: "allow\n"})
	require.Len(t, p.filtered, 1)
	assert.Equal(t, "readwrite", p.options[p.filtered[0]].Value)
	assert.Equal(t, 0, p.cursor)

	p, _ = p.Update(backspaceKey)
	assert.Equal(t, "allo", p.filter.String())
}

// A long query does not make the picker box wider.
func TestPickerLongQueryKeepsTheWidth(t *testing.T) {
	p := NewPicker()
	p.Show("Select Region", []PickerOption{{Label: "us-east-1", Value: "us-east-1"}}, 0)
	before := lipgloss.Width(p.View())
	p, _ = p.Update(tea.PasteMsg{Content: strings.Repeat("x", 200)})
	assert.Empty(t, p.filtered)
	assert.Equal(t, before, lipgloss.Width(p.View()))
	assert.Contains(t, ansi.Strip(p.View()), "/ …xxx")
}

// L12: the command bar takes space, unicode, and paste.
func TestCommandBarTakesSpaceUnicodeAndPaste(t *testing.T) {
	c := NewCommandBar()
	c.Show(testCommands(), 120)
	for _, k := range []tea.KeyPressMsg{textKey("e"), textKey("c"), textKey("2"), spaceKey, textKey("a")} {
		c, _ = c.Update(k)
	}
	assert.Equal(t, "ec2 a", c.input.String())
	require.NotEmpty(t, c.filtered)
	assert.Equal(t, "ec2/amis", c.commands[c.filtered[0]].Name, "matches the description EC2 AMIs")

	c.Show(testCommands(), 120)
	c, _ = c.Update(tea.PasteMsg{Content: "buckets\n"})
	assert.Equal(t, "buckets", c.input.String())
	require.NotEmpty(t, c.filtered)
	assert.Equal(t, "s3", c.commands[c.filtered[0]].Name)

	c.Show(testCommands(), 120)
	c, _ = c.Update(textKey("ü"))
	assert.Equal(t, "ü", c.input.String())
	assert.Empty(t, c.filtered)
	c, _ = c.Update(backspaceKey)
	assert.Empty(t, c.input.String())
	assert.True(t, c.Visible(), "backspace removed the ü and did not close the bar")
}

// The command bar sends the text without the spaces around it when no
// command matches.
func TestCommandBarTrimsUnmatchedInput(t *testing.T) {
	c := NewCommandBar()
	c.Show(testCommands(), 120)
	c, _ = c.Update(tea.PasteMsg{Content: "  zzz  "})
	require.Empty(t, c.filtered)
	_, cmd := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	assert.Equal(t, CommandBarResultMsg{Value: "zzz"}, cmd())
}

// A long command input takes the room of the hint and shows its end.
func TestCommandBarLongInputFitsTheLine(t *testing.T) {
	c := NewCommandBar()
	c.Show(testCommands(), 60)
	short := ansi.Strip(c.ViewInput(60))
	assert.Contains(t, short, "tab complete")

	c, _ = c.Update(tea.PasteMsg{Content: strings.Repeat("y", 100) + "END"})
	line := c.ViewInput(60)
	assert.Equal(t, 60, lipgloss.Width(line))
	plain := ansi.Strip(line)
	assert.NotContains(t, plain, "tab complete")
	assert.Contains(t, plain, "…")
	assert.Contains(t, plain, "END█")
}

// L12: the help filter takes space, unicode, and paste.
func TestHelpFilterTakesSpaceAndPaste(t *testing.T) {
	h := NewHelpOverlay()
	h.Show(testHints(), 120, 40)
	h, _ = h.Update(slashKey)

	for _, r := range "quit" {
		h, _ = h.Update(textKey(string(r)))
	}
	h, _ = h.Update(spaceKey)
	assert.Equal(t, "quit ", h.filter.String())

	h.Show(testHints(), 120, 40)
	h, _ = h.Update(tea.PasteMsg{Content: "quit\n"})
	assert.Equal(t, "quit", h.filter.String())
	assert.Contains(t, h.renderContent(), "quit")
	assert.NotContains(t, h.renderContent(), "select")

	h, _ = h.Update(textKey("ü"))
	assert.Equal(t, "quitü", h.filter.String())
	h, _ = h.Update(backspaceKey)
	assert.Equal(t, "quit", h.filter.String())
}

// The command bar matches the aliases of a command too.
func TestCommandBarMatchesAliases(t *testing.T) {
	c := NewCommandBar()
	c.Show([]CommandEntry{
		{Name: "quit", Description: "Exit LazyCloud"},
		{Name: "home", Aliases: []string{"root"}, Description: "Go to the start"},
	}, 120)
	c, _ = c.Update(tea.PasteMsg{Content: "root"})
	require.Len(t, c.filtered, 1)
	assert.Equal(t, "home", c.commands[c.filtered[0]].Name)
}

// The command bar ignores the spaces around the input. Space and enter
// cancel, and " q" runs quit, not a command whose description has " q".
func TestCommandBarIgnoresSpacesAroundTheInput(t *testing.T) {
	cmds := []CommandEntry{
		{Name: "quit", Aliases: []string{"q"}, Description: "Exit LazyCloud"},
		{Name: "sqs", Description: "SQS queues"},
		{Name: "theme", Description: "Pick a theme"},
	}
	for _, tc := range []struct {
		name string
		keys []tea.KeyPressMsg
		want CommandBarResultMsg
	}{
		{"only space", []tea.KeyPressMsg{spaceKey}, CommandBarResultMsg{Cancelled: true}},
		{"space before", []tea.KeyPressMsg{spaceKey, textKey("q")}, CommandBarResultMsg{Value: "quit"}},
		{"space after", []tea.KeyPressMsg{textKey("q"), spaceKey}, CommandBarResultMsg{Value: "quit"}},
		{"name and space", []tea.KeyPressMsg{textKey("t"), textKey("h"), textKey("e"), spaceKey}, CommandBarResultMsg{Value: "theme"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := NewCommandBar()
			c.Show(cmds, 120)
			for _, k := range tc.keys {
				c, _ = c.Update(k)
			}
			_, cmd := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			require.NotNil(t, cmd)
			assert.Equal(t, tc.want, cmd())
		})
	}
}

// A long CJK input fits the command line at every width.
func TestCommandBarWideInputFitsTheLine(t *testing.T) {
	c := NewCommandBar()
	c.Show(testCommands(), 60)
	c, _ = c.Update(tea.PasteMsg{Content: "a" + strings.Repeat("日", 30)})
	for w := 3; w <= 40; w++ {
		assert.LessOrEqual(t, lipgloss.Width(c.ViewInput(w)), w, "width %d", w)
	}
}
