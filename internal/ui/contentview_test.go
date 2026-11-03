package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	keyJ     = tea.KeyPressMsg{Code: 'j', Text: "j"}
	keyK     = tea.KeyPressMsg{Code: 'k', Text: "k"}
	keyTop   = tea.KeyPressMsg{Code: 'g', Text: "g"}
	keyEnd   = tea.KeyPressMsg{Code: 'G', Text: "G"}
	keyCtrlD = tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
	keyCtrlU = tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
	keyWrap  = tea.KeyPressMsg{Code: 'w', Text: "w"}
)

func press(cv ContentView, keys ...tea.KeyPressMsg) ContentView {
	for _, k := range keys {
		cv, _ = cv.Update(k)
	}
	return cv
}

// minifiedJSON returns one JSON line of about n bytes, like a minified
// policy document.
func minifiedJSON(n int) string {
	var b strings.Builder
	b.WriteString(`{"Statement":[`)
	for i := 0; b.Len() < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"Sid":"S%d","Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::bucket-%d/*"}`, i, i)
	}
	b.WriteString(`]}`)
	return b.String()
}

// visibleLineNumbers returns the line numbers in the gutter of the rows on
// the screen.
func visibleLineNumbers(cv ContentView) []int {
	var nums []int
	for row := range strings.SplitSeq(cv.viewport.View(), "\n") {
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(ansi.Strip(row)), "%d │", &n); err == nil {
			nums = append(nums, n)
		}
	}
	return nums
}

func TestContentViewScrollsThroughOneLongWrappedLine(t *testing.T) {
	cv := NewContentView("policy.json", minifiedJSON(38000), FormatJSON)
	cv.SetSize(80, 22) // viewport of 20 rows
	h := cv.viewport.Height()
	require.Equal(t, 20, h)

	rows := cv.viewport.TotalLineCount()
	require.Greater(t, rows, 400, "the line must wrap to many rows")
	maxOff := rows - h

	cv = press(cv, keyJ)
	assert.Equal(t, 0, cv.cursor)
	assert.Equal(t, 1, cv.viewport.YOffset(), "j scrolls one row in a tall line")

	cv = press(cv, keyCtrlD)
	assert.Equal(t, 1+h/2, cv.viewport.YOffset(), "ctrl+d scrolls half a screen")

	cv = press(cv, keyCtrlU, keyK)
	assert.Equal(t, 0, cv.viewport.YOffset())

	cv = press(cv, keyEnd)
	assert.Equal(t, 0, cv.cursor)
	assert.Equal(t, maxOff, cv.viewport.YOffset(), "G shows the end of the line")

	// At the end of the only line, j has nowhere to go.
	cv = press(cv, keyJ)
	assert.Equal(t, maxOff, cv.viewport.YOffset())

	cv = press(cv, keyTop)
	assert.Equal(t, 0, cv.viewport.YOffset())
}

func TestContentViewCursorStaysVisibleBelowWrappedLine(t *testing.T) {
	lines := []string{"first", strings.Repeat("word ", 100)} // line 2 takes many rows
	for i := range 30 {
		lines = append(lines, fmt.Sprintf("line %d", i+3))
	}
	cv := NewContentView("notes.txt", strings.Join(lines, "\n"), FormatText)
	cv.SetSize(60, 12) // viewport of 10 rows

	for range 12 {
		cv = press(cv, keyJ)
		assert.Contains(t, visibleLineNumbers(cv), cv.cursor+1,
			"cursor line %d is off the screen", cv.cursor+1)
	}

	// Go back up over the tall line.
	for range 12 {
		cv = press(cv, keyK)
		assert.Contains(t, visibleLineNumbers(cv), cv.cursor+1,
			"cursor line %d is off the screen", cv.cursor+1)
	}
	assert.Equal(t, 0, cv.cursor)
	assert.Equal(t, 0, cv.viewport.YOffset())

	cv = press(cv, keyCtrlD, keyCtrlD, keyCtrlD)
	assert.Contains(t, visibleLineNumbers(cv), cv.cursor+1)

	cv = press(cv, keyEnd)
	assert.Equal(t, len(lines)-1, cv.cursor)
	assert.Contains(t, visibleLineNumbers(cv), len(lines))
}

func TestContentViewWrapOffScrollIsByLine(t *testing.T) {
	var lines []string
	for i := range 50 {
		lines = append(lines, fmt.Sprintf("line %d %s", i+1, strings.Repeat("x", 200)))
	}
	cv := NewContentView("notes.txt", strings.Join(lines, "\n"), FormatText)
	cv.SetSize(60, 12)
	cv = press(cv, keyWrap)
	h := cv.viewport.Height()
	require.Equal(t, 50, cv.viewport.TotalLineCount(), "with wrap off, each line is one row")

	cv = press(cv, keyCtrlD)
	assert.Equal(t, h/2, cv.cursor)
	assert.Equal(t, 0, cv.viewport.YOffset())

	cv = press(cv, keyCtrlD, keyCtrlD)
	assert.Equal(t, 3*(h/2), cv.cursor)
	assert.Equal(t, 3*(h/2)-h+1, cv.viewport.YOffset())

	cv = press(cv, keyEnd)
	assert.Equal(t, 49, cv.cursor)
	assert.Equal(t, 50-h, cv.viewport.YOffset())

	cv = press(cv, keyTop)
	assert.Equal(t, 0, cv.cursor)
	assert.Equal(t, 0, cv.viewport.YOffset())
}

func TestContentViewCursorHighlightCoversWholeLine(t *testing.T) {
	content := `{"name": "web", "count": 3, "enabled": true}` + "\nsecond"
	cv := NewContentView("a.json", content, FormatJSON)
	cv.SetSize(80, 10)

	cursorBg := ansi.NewStyle().BackgroundColor(ActiveTheme.Overlay).String()
	lines := strings.Split(cv.viewport.GetContent(), "\n")
	require.GreaterOrEqual(t, len(lines), 2)

	// Look only at the text after the gutter.
	idx := strings.Index(lines[0], cursorBg)
	require.GreaterOrEqual(t, idx, 0, "the cursor line has no background")
	row := lines[0][idx:]
	require.Contains(t, row, "\x1b[0m", "chroma output has token resets")
	assertStyleAfterEachReset(t, row, cursorBg)

	// The next line has no background.
	assert.NotContains(t, lines[1], cursorBg)
}

func TestContentViewSelectionHighlightCoversWholeLines(t *testing.T) {
	content := "{\n  \"a\": 1,\n  \"b\": \"two\"\n}"
	cv := NewContentView("a.json", content, FormatJSON)
	cv.SetSize(80, 10)
	cv = press(cv, tea.KeyPressMsg{Code: 'V', Text: "V"}, keyJ, keyJ)

	selectBg := ansi.NewStyle().BackgroundColor(ActiveTheme.Secondary).String()
	lines := strings.Split(cv.viewport.GetContent(), "\n")
	for _, i := range []int{1, 2} {
		idx := strings.Index(lines[i], selectBg)
		require.GreaterOrEqual(t, idx, 0, "line %d is not selected", i+1)
		assertStyleAfterEachReset(t, lines[i][idx:], selectBg)
	}
}

func TestContentViewWrapKeepsLayoutCacheOnKeyPress(t *testing.T) {
	cv := NewContentView("policy.json", minifiedJSON(38000), FormatJSON)
	cv.SetSize(80, 22)
	before := &cv.layout.rows[0]

	cv = press(cv, keyJ, keyCtrlD, keyEnd, keyTop)
	assert.Same(t, before, &cv.layout.rows[0], "a key press must not wrap the text again")

	// A new width makes a new layout.
	cv.SetSize(60, 22)
	assert.NotSame(t, before, &cv.layout.rows[0])
}

func TestContentViewWrapKeepsIndent(t *testing.T) {
	cv := NewContentView("a.yaml", "root:\n    child: value", FormatYAML)
	cv.SetSize(80, 10)
	lines := strings.Split(ansi.Strip(cv.viewport.GetContent()), "\n")
	require.Len(t, lines, 2)
	assert.Contains(t, lines[1], "│     child: value")
}

// wrappingLines returns n lines that each take two rows at width 80 with
// line numbers on, and one row with line numbers off.
func wrappingLines(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = strings.Repeat("x", 75)
	}
	return strings.Join(lines, "\n")
}

func TestContentViewToggleLineNumbersKeepsCursorVisible(t *testing.T) {
	cv := NewContentView("notes.txt", wrappingLines(2000), FormatText)
	cv.SetSize(80, 22)
	for range 600 {
		cv = press(cv, keyJ)
	}
	require.Equal(t, 600, cv.cursor)
	require.Equal(t, 2, cv.layout.lineStart[601]-cv.layout.lineStart[600], "a line takes two rows")

	cv = press(cv, tea.KeyPressMsg{Code: 'n', Text: "n"})
	require.Equal(t, 1, cv.layout.lineStart[601]-cv.layout.lineStart[600], "a line takes one row")
	assert.Equal(t, 600, cv.cursor)
	r := cv.viewport.YOffset()
	assert.LessOrEqual(t, r, 600, "the cursor line is below the screen")
	assert.Greater(t, r+cv.viewport.Height(), 600, "the cursor line is above the screen")
}

func TestContentViewSetLinksKeepsTopLine(t *testing.T) {
	// 77 cells fit in one row of 78, but not after the link prefix takes 2.
	lines := make([]string, 300)
	for i := range lines {
		lines[i] = strings.Repeat("y", 77)
	}
	cv := NewContentView("notes.txt", strings.Join(lines, "\n"), FormatText)
	cv.lineNumbers = false
	cv.SetSize(80, 22)
	for range 150 {
		cv = press(cv, keyJ)
	}
	// Move the cursor up to the top line of the screen.
	for cv.cursor > cv.viewport.YOffset() {
		cv = press(cv, keyK)
	}
	top := cv.lineAtRow(cv.viewport.YOffset())
	require.Equal(t, 1, cv.layout.lineStart[1], "a line takes one row")

	cv.SetLinks(map[int]ContentLink{0: {ViewID: "x"}})
	require.Equal(t, 2, cv.layout.lineStart[1], "a line takes two rows")
	assert.Equal(t, top, cv.lineAtRow(cv.viewport.YOffset()), "the top line moved")
	start, end := cv.lineRows(cv.cursor)
	off := cv.viewport.YOffset()
	assert.True(t, start >= off && end <= off+cv.viewport.Height(), "the cursor line is not on the screen")
}

func TestContentViewWrapOffCursorBackgroundAfterScrollRight(t *testing.T) {
	content := "{\n" + strings.Repeat("z", 280) + "\n}"
	cv := NewContentView("a.txt", content, FormatText)
	cv.SetSize(60, 12)
	cv = press(cv, keyWrap, keyJ, keyJ)
	right := tea.KeyPressMsg{Code: 'l', Text: "l"}
	for cv.viewport.XOffset() < 150 {
		before := cv.viewport.XOffset()
		cv = press(cv, right)
		require.Greater(t, cv.viewport.XOffset(), before, "l does not scroll right")
	}

	cursorBg := ansi.NewStyle().BackgroundColor(ActiveTheme.Overlay).String()
	rows := strings.Split(cv.viewport.View(), "\n")
	require.GreaterOrEqual(t, len(rows), 3)
	row := rows[2]
	// All the visible cells of the cursor row come after the background
	// and before the reset that ends it.
	idx := strings.LastIndex(row, cursorBg)
	require.GreaterOrEqual(t, idx, 0, "the cursor row has no background on the screen")
	assert.Equal(t, 0, ansi.StringWidth(row[:idx]), "cells before the background")
	painted := row[idx:]
	if end := strings.Index(painted, ansi.ResetStyle); end >= 0 {
		painted = painted[:end]
	}
	assert.Equal(t, cv.viewport.Width(), ansi.StringWidth(painted), "cells without the background")

	// The background fill does not make the text wider, so l still stops
	// at the end of the longest line.
	for range 100 {
		cv = press(cv, right)
	}
	assert.Equal(t, ansi.StringWidth(cv.screen.plain[1])-cv.viewport.Width(), cv.viewport.XOffset())
}

func TestContentViewKeyPressPaintsOnlyChangedRows(t *testing.T) {
	cv := NewContentView("items.json", manyLinesJSON(100), FormatJSON)
	cv.SetSize(80, 22)
	cursorBg := ansi.NewStyle().BackgroundColor(ActiveTheme.Overlay).String()
	plain := &cv.screen.plain[0]

	cv = press(cv, keyJ, keyJ)
	assert.Same(t, plain, &cv.screen.plain[0], "a key press must not make the gutter rows again")

	lines := strings.Split(cv.viewport.GetContent(), "\n")
	for i, line := range lines[:5] {
		assert.Equal(t, i == 2, strings.Contains(line, cursorBg), "line %d", i+1)
	}

	// Line numbers off makes new rows.
	cv = press(cv, tea.KeyPressMsg{Code: 'n', Text: "n"})
	assert.NotSame(t, plain, &cv.screen.plain[0])
	assert.NotContains(t, ansi.Strip(cv.viewport.GetContent()), "│")
}

func TestContentViewSetContentShowsTop(t *testing.T) {
	cv := NewContentView("a.txt", wrappingLines(200), FormatText)
	cv.SetSize(80, 22)
	cv = press(cv, keyEnd)
	require.Positive(t, cv.viewport.YOffset())

	cv.SetContent("b.txt", wrappingLines(150), FormatText)
	assert.Equal(t, 0, cv.cursor)
	assert.Equal(t, 0, cv.viewport.YOffset())
}

func BenchmarkContentViewKeyPressLongLine(b *testing.B) {
	cv := NewContentView("policy.json", minifiedJSON(38000), FormatJSON)
	cv.SetSize(120, 40)
	for b.Loop() {
		cv = press(cv, keyJ, keyK)
		_ = cv.View()
	}
}

// manyLinesJSON returns pretty JSON with about n lines.
func manyLinesJSON(n int) string {
	var b strings.Builder
	b.WriteString("{\n  \"Items\": [\n")
	for i := range n {
		if i > 0 {
			b.WriteString(",\n")
		}
		fmt.Fprintf(&b, `    {"Id": %d, "Name": "item-%d", "Enabled": true}`, i, i)
	}
	b.WriteString("\n  ]\n}")
	return b.String()
}

func BenchmarkContentViewKeyPressManyLines(b *testing.B) {
	cv := NewContentView("items.json", manyLinesJSON(50000), FormatJSON)
	cv.SetSize(120, 40)
	for b.Loop() {
		cv = press(cv, keyJ, keyK)
		_ = cv.View()
	}
}
