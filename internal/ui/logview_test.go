package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	keyVisual = tea.KeyPressMsg{Code: 'V', Text: "V"}
	keyYank   = tea.KeyPressMsg{Code: 'y', Text: "y"}
	keySlash  = tea.KeyPressMsg{Code: '/', Text: "/"}
)

var logT0 = time.Date(2026, 11, 20, 14, 0, 0, 0, time.UTC)

// logLines returns n lines from "line from" on, one second apart.
func logLines(from, n int) []LogLine {
	out := make([]LogLine, n)
	for i := range out {
		k := from + i
		out[i] = LogLine{Time: logT0.Add(time.Duration(k) * time.Second), Message: fmt.Sprintf("line %d", k), ID: fmt.Sprint(k)}
	}
	return out
}

func newTestLogView(w, h int) LogView {
	lv := NewLogView()
	lv.SetLocation(time.UTC)
	lv.SetSize(w, h)
	return lv
}

func pressLog(lv *LogView, keys ...tea.KeyPressMsg) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		cmd = lv.Update(k)
	}
	return cmd
}

func typeLog(lv *LogView, s string) {
	for _, r := range s {
		lv.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func selectedMsg(t *testing.T, lv *LogView) string {
	t.Helper()
	l, ok := lv.Selected()
	require.True(t, ok)
	return l.Message
}

// plainRows returns the rows of the view without escape sequences.
func plainRows(lv *LogView) []string {
	return strings.Split(ansi.Strip(lv.View()), "\n")
}

// assertFits checks that the view has exactly its height and that no row is
// wider than its width.
func assertFits(t *testing.T, lv *LogView, w, h int) {
	t.Helper()
	rows := strings.Split(lv.View(), "\n")
	require.Len(t, rows, h)
	for i, r := range rows {
		assert.LessOrEqual(t, ansi.StringWidth(r), w, "row %d at width %d: %q", i, w, ansi.Strip(r))
	}
}

// runCmd runs cmd and the commands of a batch, and returns their messages.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	m := cmd()
	if b, ok := m.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, runCmd(c)...)
		}
		return out
	}
	return []tea.Msg{m}
}

// H4 of docs/design/cwlogs.md, fixed in PR 3: a component appends lines,
// caps them and follows the end. Before PR 3, ContentView and the event log
// view drew all lines again on each change.
func TestLogsH4_LogViewAppendsCapsAndFollows(t *testing.T) {
	lv := newTestLogView(80, 4)
	lv.SetCaps(5, LogMaxBytes)
	for i := range 20 {
		lv.Append(logLines(i, 1))
		assert.Equal(t, fmt.Sprintf("line %d", i), selectedMsg(t, &lv), "follow keeps the cursor on the new line")
	}
	assert.Equal(t, 5, lv.Len())
	assert.Equal(t, 15, lv.Dropped())
	assert.Contains(t, plainRows(&lv)[2], "line 19")
}

func TestLogViewLineCapDropsTheOldest(t *testing.T) {
	lv := newTestLogView(80, 10)
	lv.SetCaps(10, LogMaxBytes)
	lv.Append(logLines(0, 25))

	assert.Equal(t, 10, lv.Len())
	assert.Equal(t, 15, lv.Dropped())
	assert.Equal(t, "line 24", selectedMsg(t, &lv), "follow keeps the cursor on the last line")
	pressLog(&lv, keyTop)
	assert.Equal(t, "line 15", selectedMsg(t, &lv))
	assert.Contains(t, plainRows(&lv)[9], "15 older lines dropped")
}

func TestLogViewDefaultCapWith20000Lines(t *testing.T) {
	lv := newTestLogView(80, 20)
	lv.Append(logLines(0, 20000))

	assert.Equal(t, LogMaxLines, lv.Len())
	assert.Equal(t, 10000, lv.Dropped())
	assert.Equal(t, "line 19999", selectedMsg(t, &lv))
	pressLog(&lv, keyTop)
	assert.Equal(t, "line 10000", selectedMsg(t, &lv))
	assertFits(t, &lv, 80, 20)
}

func TestLogViewWith10Lines(t *testing.T) {
	lv := newTestLogView(80, 20)
	lv.Append(logLines(0, 10))
	rows := plainRows(&lv)
	assert.Equal(t, "14:00:00.000 line 0", strings.TrimSpace(rows[0]), "a short log fills from the top")
	assert.Equal(t, "14:00:09.000 line 9", strings.TrimSpace(rows[9]))
	assert.Empty(t, rows[10])
	assert.Equal(t, "10/10 · follow", rows[19])
	assert.Zero(t, lv.Dropped())
}

func TestLogViewByteCapWith1MBLines(t *testing.T) {
	big := strings.Repeat("x", 1<<20) // one string, so the test does not hold 100 MB
	lines := make([]LogLine, 100)
	for i := range lines {
		lines[i] = LogLine{Time: logT0, Message: big, ID: fmt.Sprint(i)}
	}
	lv := newTestLogView(80, 10)
	lv.Append(lines)

	want := LogMaxBytes / (len(big) + 2 + logEntryCost) // the ID has two digits
	assert.Equal(t, want, lv.Len())
	assert.Equal(t, 100-want, lv.Dropped())
	assert.LessOrEqual(t, lv.bytes, LogMaxBytes)
	l, _ := lv.Selected()
	assert.Equal(t, "99", l.ID)
	assertFits(t, &lv, 80, 10)
}

func TestLogViewKeepsOneLineLargerThanTheByteCap(t *testing.T) {
	lv := newTestLogView(80, 10)
	lv.SetCaps(100, 10)
	lv.Append(logLines(0, 3))
	assert.Equal(t, 1, lv.Len(), "the newest line stays")
	assert.Equal(t, "line 2", selectedMsg(t, &lv))
}

func TestLogViewPrependKeepsTheCursorLine(t *testing.T) {
	lv := newTestLogView(80, 6)
	lv.Append(logLines(100, 20))
	pressLog(&lv, keyK, keyK, keyK)
	require.Equal(t, "line 116", selectedMsg(t, &lv))
	before := plainRows(&lv)[:5]

	assert.Equal(t, 50, lv.Prepend(logLines(50, 50)))

	assert.Equal(t, "line 116", selectedMsg(t, &lv))
	assert.Equal(t, before, plainRows(&lv)[:5], "the screen does not move")
	assert.Equal(t, 70, lv.Len())
	pressLog(&lv, keyTop)
	assert.Equal(t, "line 50", selectedMsg(t, &lv))
}

func TestLogViewPrependKeepsTheCapsWithTheNewestLines(t *testing.T) {
	lv := newTestLogView(80, 6)
	lv.SetCaps(12, LogMaxBytes)
	lv.Append(logLines(10, 10))

	assert.Equal(t, 2, lv.Prepend(logLines(0, 10)), "only two lines fit")
	assert.Equal(t, 12, lv.Len())
	assert.Zero(t, lv.Dropped(), "Prepend drops no line that the view had")
	pressLog(&lv, keyTop)
	assert.Equal(t, "line 8", selectedMsg(t, &lv))

	assert.Zero(t, lv.Prepend(logLines(0, 5)), "the view is full")
}

func TestLogViewPrependKeepsTheByteCap(t *testing.T) {
	lv := newTestLogView(80, 6)
	one := newLogEntry(logLines(10, 1)[0]).cost // the costliest line
	lv.SetCaps(100, 5*one)
	lv.Append(logLines(10, 3))
	assert.Equal(t, 2, lv.Prepend(logLines(0, 10)))
	assert.LessOrEqual(t, lv.bytes, 5*one)
}

func TestLogViewPrependIntoAnEmptyView(t *testing.T) {
	lv := newTestLogView(80, 6)
	assert.Equal(t, 3, lv.Prepend(logLines(0, 3)))
	assert.Equal(t, "line 2", selectedMsg(t, &lv), "follow puts the cursor on the last line")

	lv = newTestLogView(80, 6)
	lv.SetFollow(false)
	lv.Prepend(logLines(0, 3))
	assert.Equal(t, "line 0", selectedMsg(t, &lv))

	lv = newTestLogView(80, 6)
	lv.SetCaps(100, 10)
	assert.Equal(t, 1, lv.Prepend(logLines(0, 3)), "an empty view keeps one line larger than the byte cap")
	assert.Equal(t, "line 2", selectedMsg(t, &lv))
}

func TestLogViewFollow(t *testing.T) {
	lv := newTestLogView(80, 6)
	require.True(t, lv.Following())
	lv.Append(logLines(0, 3))
	assert.Equal(t, "line 2", selectedMsg(t, &lv))
	lv.Append(logLines(3, 10))
	assert.Equal(t, "line 12", selectedMsg(t, &lv), "new lines move the view to the end")
	assert.Contains(t, plainRows(&lv)[4], "line 12", "the last line is on the screen")

	pressLog(&lv, keyK)
	assert.False(t, lv.Following(), "a move up turns follow off")
	lv.Append(logLines(13, 2))
	assert.Equal(t, "line 11", selectedMsg(t, &lv))

	pressLog(&lv, keyJ, keyJ, keyJ)
	assert.False(t, lv.Following(), "j to the end does not turn follow on")
	lv.Append(logLines(15, 1))
	assert.Equal(t, "line 14", selectedMsg(t, &lv), "without follow, the cursor stays on the last line it had")

	pressLog(&lv, keyEnd)
	assert.True(t, lv.Following())
	assert.Equal(t, "line 15", selectedMsg(t, &lv))
	lv.Append(logLines(16, 1))
	assert.Equal(t, "line 16", selectedMsg(t, &lv))
}

// A larger screen shows more of the lines above, so no rows stay empty
// while there are lines to show.
func TestLogViewGrowShowsMoreLinesAbove(t *testing.T) {
	lv := newTestLogView(80, 6)
	lv.Append(logLines(0, 20))
	require.Contains(t, plainRows(&lv)[0], "line 15")
	lv.SetSize(80, 30)
	rows := plainRows(&lv)
	assert.Contains(t, rows[0], "line 0")
	assert.Contains(t, rows[19], "line 19")
}

func TestLogViewFollowNeedsTheCursorOnTheLastLine(t *testing.T) {
	lv := newTestLogView(80, 6)
	lv.Append(logLines(0, 5))
	pressLog(&lv, keyK)
	lv.SetFollow(true)
	lv.Append(logLines(5, 1))
	assert.Equal(t, "line 3", selectedMsg(t, &lv))
}

// FollowEnd moves to the last line and turns follow on, so the next lines
// move the cursor. With no lines, it only turns follow on.
func TestLogViewFollowEnd(t *testing.T) {
	lv := newTestLogView(80, 6)
	lv.FollowEnd()
	assert.True(t, lv.Following())
	lv.Append(logLines(0, 5))
	pressLog(&lv, keyK, keyK)
	require.False(t, lv.Following())
	lv.FollowEnd()
	assert.True(t, lv.Following())
	assert.Equal(t, "line 4", selectedMsg(t, &lv))
	lv.Append(logLines(5, 1))
	assert.Equal(t, "line 5", selectedMsg(t, &lv))
}

func TestLogViewMoves(t *testing.T) {
	lv := newTestLogView(80, 11) // 10 rows for lines
	lv.Append(logLines(0, 40))
	pressLog(&lv, keyTop)
	assert.False(t, lv.Following(), "g turns follow off")
	pressLog(&lv, keyCtrlD)
	assert.Equal(t, "line 5", selectedMsg(t, &lv))
	pressLog(&lv, keyCtrlD, keyCtrlD)
	assert.Equal(t, "line 15", selectedMsg(t, &lv))
	assert.Contains(t, plainRows(&lv)[9], "line 15", "the cursor line is the last row")
	pressLog(&lv, keyCtrlU)
	assert.Equal(t, "line 10", selectedMsg(t, &lv))
	pressLog(&lv, keyK, keyK, keyK, keyK, keyK, keyK, keyK, keyK, keyK, keyK, keyK)
	assert.Equal(t, "line 0", selectedMsg(t, &lv), "k stops at the first line")
	pressLog(&lv, keyEnd, keyJ)
	assert.Equal(t, "line 39", selectedMsg(t, &lv), "j stops at the last line")

	lv.SetFollow(true)
	pressLog(&lv, keyCtrlU)
	assert.False(t, lv.Following(), "ctrl+u turns follow off")
}

func TestLogViewMarksAMessageWithMoreLines(t *testing.T) {
	lv := newTestLogView(60, 6)
	lv.Append([]LogLine{
		{Time: logT0, Message: "first\nsecond\n"},
		{Time: logT0, Message: "plain\n"},
	})
	rows := plainRows(&lv)
	assert.Contains(t, rows[0], "first ↵")
	assert.NotContains(t, rows[0], "second")
	assert.NotContains(t, rows[1], "↵", "a line feed at the end is not a second line")

	pressLog(&lv, keyWrap)
	rows = plainRows(&lv)
	assert.Contains(t, rows[0], "first")
	assert.Contains(t, rows[1], "second")
	assert.Equal(t, strings.Repeat(" ", 13)+"second", strings.TrimRight(rows[1], " "), "the next row has empty columns")
}

func TestLogViewWrap(t *testing.T) {
	msg := strings.Repeat("word ", 30)
	lv := newTestLogView(40, 8)
	lv.Append([]LogLine{{Time: logT0, Message: msg}, {Time: logT0, Message: "next"}})
	rows := plainRows(&lv)
	assert.True(t, strings.HasSuffix(strings.TrimRight(rows[0], " "), "…"), "wrap off cuts the row: %q", rows[0])
	assert.Contains(t, rows[1], "next")

	pressLog(&lv, keyWrap)
	require.True(t, lv.Wrapped())
	rows = plainRows(&lv)
	assert.NotContains(t, rows[0], "…")
	assert.Contains(t, rows[7], "wrap", "the status row")
	assertFits(t, &lv, 40, 8)
	joined := ""
	for _, r := range rows[:6] {
		joined += strings.TrimSpace(strings.TrimPrefix(r, "14:00:00.000")) + " "
	}
	assert.Contains(t, joined, "word word")
}

// A wrapped line taller than the screen fills the screen and ends with …,
// and enter gives all of it.
func TestLogViewWrapCutsALineTallerThanTheScreen(t *testing.T) {
	lv := newTestLogView(40, 5)
	lv.Append([]LogLine{{Time: logT0, Message: strings.Repeat("x", 2000)}})
	pressLog(&lv, keyWrap)
	rows := plainRows(&lv)
	assert.True(t, strings.HasSuffix(rows[3], " …"), "%q", rows[3])
	assertFits(t, &lv, 40, 5)
	msgs := runCmd(pressLog(&lv, keyEnter))
	require.Len(t, msgs, 1)
	assert.Len(t, msgs[0].(LogLineOpenedMsg).Line.Message, 2000)
}

func TestLogViewCutsA1MBLineInItsRow(t *testing.T) {
	big := strings.Repeat("abcdefgh", 1<<17) // 1 MB
	lv := newTestLogView(80, 6)
	lv.Append([]LogLine{{Time: logT0, Message: big}})

	assert.Len(t, lv.entries[0].show, logShowMax, "the row text keeps 64 KB")
	assert.True(t, lv.entries[0].cut)
	rows := plainRows(&lv)
	assert.Contains(t, rows[0], "↵", "the mark says that the row has more")
	assertFits(t, &lv, 80, 6)
	pressLog(&lv, keyWrap)
	assert.True(t, strings.HasSuffix(plainRows(&lv)[4], " …"))
	assertFits(t, &lv, 80, 6)

	l, _ := lv.Selected()
	assert.Len(t, l.Message, len(big), "the line keeps all of the message")
}

// The cut at 64 KB does not split a character.
func TestLogViewCutKeepsCharactersWhole(t *testing.T) {
	msg := "ab" + strings.Repeat("日", logShowMax)
	e := newLogEntry(LogLine{Message: msg})
	assert.NotContains(t, e.show, "�")
	assert.LessOrEqual(t, len(e.show), logShowMax)
}

func TestLogViewRemovesControlCharacters(t *testing.T) {
	lv := newTestLogView(120, 8)
	lv.Append([]LogLine{
		{Time: logT0, Source: "stream-" + dcs, Message: "DCS[" + dcs + "] C1[" + csi8 + "] OSC[" + osc52 + "]"},
		{Time: logT0, Message: "CR[visible\rHIDDEN] tab[\t] bad[\xff]"},
	})
	pressLog(&lv, keyVisual, keyK)
	out := lv.View()
	assertTerminalSafe(t, out)
	plain := ansi.Strip(out)
	assert.Contains(t, plain, "␛P$qm␛\\")
	assert.Contains(t, plain, "stream-␛P$qm␛\\")
	assert.Contains(t, plain, "visible␍HIDDEN")
	assert.Contains(t, plain, "tab[    ]")

	pressLog(&lv, keyWrap)
	assertTerminalSafe(t, lv.View())
	l, _ := lv.Selected()
	assert.Contains(t, l.Message, dcs, "the line keeps the raw text for a copy")
}

func TestLogViewLocalFilter(t *testing.T) {
	lv := newTestLogView(80, 8)
	lv.Append([]LogLine{
		{Time: logT0, Message: "INFO start"},
		{Time: logT0, Message: "ERROR one"},
		{Time: logT0, Source: "err-stream", Message: "INFO two"},
		{Time: logT0, Message: "ERROR three"},
		{Time: logT0, Message: "INFO four"},
	})
	pressLog(&lv, keyK, keyK, keyK) // on "ERROR one"
	pressLog(&lv, keySlash)
	require.True(t, lv.CapturingInput())
	typeLog(&lv, "Err")

	rows := plainRows(&lv)
	assert.Contains(t, rows[0], "ERROR one")
	assert.Contains(t, rows[1], "INFO two", "the source matches too")
	assert.Contains(t, rows[2], "ERROR three")
	assert.Empty(t, rows[3])
	assert.Equal(t, "ERROR one", selectedMsg(t, &lv), "the cursor stays on its line")

	pressLog(&lv, keyEnter)
	assert.False(t, lv.CapturingInput())
	assert.Equal(t, "Err", lv.FilterText())
	assert.Contains(t, plainRows(&lv)[7], "1/3 of 5 · filter: Err")
	assert.True(t, lv.WantsEsc())

	lv.Append([]LogLine{{Time: logT0, Message: "ERROR five"}, {Time: logT0, Message: "INFO six"}})
	assert.Contains(t, plainRows(&lv)[3], "ERROR five", "a new line that matches shows")
	pressLog(&lv, keyEnd)
	assert.Equal(t, "ERROR five", selectedMsg(t, &lv))

	pressLog(&lv, keyEsc)
	assert.Empty(t, lv.FilterText())
	assert.False(t, lv.WantsEsc())
	assert.Equal(t, "ERROR five", selectedMsg(t, &lv), "the cursor keeps its line")
	assert.Contains(t, plainRows(&lv)[7], "6/7")
}

func TestLogViewFilterWithNoMatch(t *testing.T) {
	lv := newTestLogView(80, 6)
	lv.Append(logLines(0, 3))
	pressLog(&lv, keySlash)
	typeLog(&lv, "zzz")
	assert.Contains(t, plainRows(&lv)[0], "No lines match the filter")
	_, ok := lv.Selected()
	assert.False(t, ok)
	pressLog(&lv, keyEsc)
	assert.False(t, lv.CapturingInput())
	assert.Empty(t, lv.FilterText(), "esc in the input clears the filter")
	assert.Equal(t, "line 2", selectedMsg(t, &lv))
}

// A paste goes into the open filter and filters at once. Without the
// filter open, a paste does nothing.
func TestLogViewPasteIntoTheFilter(t *testing.T) {
	lv := newTestLogView(80, 6)
	lv.Append(logLines(0, 3))
	lv.Update(tea.PasteMsg{Content: "line 1"})
	assert.Empty(t, lv.FilterText())
	pressLog(&lv, keySlash)
	lv.Update(tea.PasteMsg{Content: "line 1"})
	assert.Equal(t, "line 1", lv.FilterText())
	assert.Equal(t, "line 1", selectedMsg(t, &lv))
	assert.NotContains(t, strings.Join(plainRows(&lv), "\n"), "line 2")
}

// The cap drops lines that match the filter, and the filter keeps the rest.
func TestLogViewFilterWithTheCap(t *testing.T) {
	lv := newTestLogView(80, 6)
	lv.SetCaps(5, LogMaxBytes)
	lv.Append(logLines(0, 5))
	pressLog(&lv, keySlash)
	typeLog(&lv, "line")
	pressLog(&lv, keyEnter)
	lv.Append(logLines(5, 3))
	assert.Equal(t, []int{3, 4, 5, 6, 7}, lv.visible)
	pressLog(&lv, keyTop)
	assert.Equal(t, "line 3", selectedMsg(t, &lv))
	lv.Prepend(logLines(0, 1))
	assert.Equal(t, 5, lv.count(), "the view is full")
}

func TestLogViewFilterAndPrepend(t *testing.T) {
	lv := newTestLogView(80, 6)
	lv.Append([]LogLine{{Message: "b ERROR"}, {Message: "c"}})
	pressLog(&lv, keySlash)
	typeLog(&lv, "error")
	pressLog(&lv, keyEnter)
	lv.Prepend([]LogLine{{Message: "a ERROR"}, {Message: "a2"}})
	assert.Equal(t, 2, lv.count())
	assert.Equal(t, "b ERROR", selectedMsg(t, &lv))
	pressLog(&lv, keyTop)
	assert.Equal(t, "a ERROR", selectedMsg(t, &lv))
}

func TestLogViewVisualSelectAndCopy(t *testing.T) {
	lv := newTestLogView(80, 8)
	lv.Append(logLines(0, 5))
	lv.Append([]LogLine{{Message: "with end\n"}})
	pressLog(&lv, keyK, keyK, keyVisual, keyJ, keyJ)
	require.True(t, lv.InVisualMode())
	assert.Contains(t, plainRows(&lv)[7], "VISUAL (3 lines)")

	msgs := runCmd(pressLog(&lv, keyYank))
	assert.Contains(t, msgs, YankedMsg{Lines: 3})
	assert.Contains(t, fmt.Sprint(msgs), "line 3\nline 4\nwith end", "the clipboard gets the messages")
	assert.NotContains(t, fmt.Sprint(msgs), "with end\n", "without the line feed at the end")
	assert.False(t, lv.InVisualMode())
	assert.Contains(t, plainRows(&lv)[7], "3 line(s) copied")

	msgs = runCmd(pressLog(&lv, keyYank))
	assert.Contains(t, msgs, YankedMsg{Lines: 1})

	pressLog(&lv, keyVisual)
	assert.True(t, lv.WantsEsc())
	pressLog(&lv, keyEsc)
	assert.False(t, lv.InVisualMode(), "esc ends visual mode")
}

func TestLogViewEnterOpensTheLine(t *testing.T) {
	lv := newTestLogView(80, 6)
	assert.Nil(t, pressLog(&lv, keyEnter), "no line, no message")
	assert.Nil(t, pressLog(&lv, keyYank))
	lv.Append(logLines(0, 3))
	msgs := runCmd(pressLog(&lv, keyK, keyEnter))
	require.Len(t, msgs, 1)
	assert.Equal(t, LogLineOpenedMsg{Line: logLines(1, 1)[0]}, msgs[0])
}

func TestLogViewTimeAndSourceColumns(t *testing.T) {
	lv := newTestLogView(100, 4)
	lv.Append([]LogLine{
		{Time: logT0, Source: "app/1", Message: "a"},
		{Time: logT0, Source: strings.Repeat("s", 50), Message: "b"},
	})
	rows := plainRows(&lv)
	assert.Equal(t, "14:00:00.000 app/1"+strings.Repeat(" ", logSourceMax-5)+" a", rows[0])
	assert.Equal(t, "14:00:00.000 "+strings.Repeat("s", logSourceMax-1)+"… b", strings.TrimRight(rows[1], " "), "the cursor row fills the width")

	lv.SetLongTimes(true)
	assert.True(t, strings.HasPrefix(plainRows(&lv)[0], "2026-11-20 14:00:00.000 app/1"))

	lv.SetLocation(time.FixedZone("X", -5*3600))
	assert.True(t, strings.HasPrefix(plainRows(&lv)[0], "2026-11-20 09:00:00.000"))

	lv.SetLongTimes(false)
	lv.SetSize(60, 4)
	assert.Equal(t, "09:00:00.000 a", plainRows(&lv)[0], "a narrow screen hides the source")
}

func TestLogViewEmptyText(t *testing.T) {
	lv := newTestLogView(40, 4)
	assert.Equal(t, "No lines", plainRows(&lv)[0])
	lv.SetEmptyText("no events from 14:02 to 15:02")
	assert.Equal(t, "no events from 14:02 to 15:02", plainRows(&lv)[0])
	assertFits(t, &lv, 40, 4)
	pressLog(&lv, keyJ, keyK, keyTop, keyEnd, keyCtrlD, keyVisual, keyWrap)
	assertFits(t, &lv, 40, 4)
}

func TestLogViewClear(t *testing.T) {
	lv := newTestLogView(80, 6)
	lv.SetCaps(3, LogMaxBytes)
	lv.Append(logLines(0, 5))
	pressLog(&lv, keyK, keySlash)
	typeLog(&lv, "line")
	pressLog(&lv, keyEnter)
	lv.Clear()
	assert.Zero(t, lv.Len())
	assert.Zero(t, lv.Dropped())
	assert.Equal(t, "line", lv.FilterText(), "the filter stays")
	assert.True(t, lv.Following())
	lv.Append(logLines(10, 2))
	lv.Append([]LogLine{{Message: "other"}})
	assert.Equal(t, 2, lv.count(), "the filter acts on the new lines")
	assert.Equal(t, "line 11", selectedMsg(t, &lv))
}

// A reload clears the lines while the user types a filter. The next keys
// still go to the filter.
func TestLogViewClearKeepsTheFilterInput(t *testing.T) {
	lv := newTestLogView(80, 6)
	lv.Append(logLines(0, 3))
	pressLog(&lv, keySlash)
	typeLog(&lv, "li")
	lv.Clear()
	require.True(t, lv.CapturingInput())
	typeLog(&lv, "ne")
	assert.Equal(t, "line", lv.FilterText())
	lv.Append(logLines(5, 1))
	assert.Equal(t, 1, lv.count())
}

// After the caps dropped lines, an older line would leave a gap, so
// Prepend adds none, also when the byte cap left room.
func TestLogViewPrependAddsNothingAfterADrop(t *testing.T) {
	lv := newTestLogView(80, 6)
	lv.SetCaps(100, 3*newLogEntry(LogLine{Message: strings.Repeat("x", 100)}).cost)
	lv.Append([]LogLine{{Message: strings.Repeat("x", 100)}, {Message: strings.Repeat("x", 100)}, {Message: strings.Repeat("x", 100)}})
	lv.Append([]LogLine{{Message: strings.Repeat("y", 150)}})
	require.Equal(t, 2, lv.Dropped())
	require.Less(t, lv.bytes+newLogEntry(LogLine{Message: "old"}).cost, lv.maxBytes, "the byte cap has room")
	assert.Zero(t, lv.Prepend([]LogLine{{Message: "old"}}))
	assert.Equal(t, 2, lv.Len())
}

// Follow waits in visual mode, so the selection does not grow when lines
// come in.
func TestLogViewFollowWaitsInVisualMode(t *testing.T) {
	lv := newTestLogView(80, 8)
	lv.Append(logLines(0, 5))
	pressLog(&lv, keyVisual)
	lv.Append(logLines(5, 100))
	lo, hi := lv.selection()
	assert.Equal(t, lo, hi, "the selection is still one line")
	assert.Equal(t, "line 4", selectedMsg(t, &lv))
	msgs := runCmd(pressLog(&lv, keyYank))
	assert.Contains(t, msgs, YankedMsg{Lines: 1})
	assert.True(t, lv.Following(), "follow is still on")
	pressLog(&lv, keyEnd)
	assert.Equal(t, "line 104", selectedMsg(t, &lv))
}

// cutCells cuts text with many bytes for each cell, such as combining marks
// and emoji joined by ZWJ, at a character start, with the mark.
func TestLogViewCutCellsWithManyBytesForEachCell(t *testing.T) {
	for _, s := range []string{
		"a" + strings.Repeat("́", 60) + "bcdefghijkl",
		strings.Repeat("👨‍👩‍👧‍👦", 10),
	} {
		got := cutCells(s, 30)
		assert.True(t, utf8.ValidString(got), "%q", got)
		assert.True(t, strings.HasSuffix(got, "…"), "the cut shows: %q", got)
		assert.LessOrEqual(t, ansi.StringWidth(got), 30)
	}
	lv := newTestLogView(30, 3)
	lv.Append([]LogLine{{Message: strings.Repeat("👨‍👩‍👧‍👦", 10)}})
	assertFits(t, &lv, 30, 3)
	assertTerminalSafe(t, lv.View())
}

// The filter row has styles. On a narrow view, its cut keeps the escape
// sequences whole and ends the styles.
func TestLogViewNarrowFilterRow(t *testing.T) {
	for _, w := range []int{5, 10, 20, 40} {
		lv := newTestLogView(w, 4)
		lv.Append(logLines(0, 2))
		pressLog(&lv, keySlash)
		typeLog(&lv, "hel")
		rows := strings.Split(lv.View(), "\n")
		last := rows[len(rows)-1]
		assert.LessOrEqual(t, ansi.StringWidth(last), w)
		assert.True(t, strings.HasSuffix(last, ansi.ResetStyle), "width %d: %q", w, last)
		assert.Equal(t, ansi.Strip(last), ansi.Strip(ansi.Strip(last)), "no broken sequence")
		assert.NotContains(t, ansi.Strip(last), "\x1b", "width %d: %q", w, last)
	}
}

// With wrap on and one cell for the message, a wide character does not
// make a row wider than the view.
func TestLogViewWrapWithOneCell(t *testing.T) {
	lv := newTestLogView(1, 8)
	lv.Append([]LogLine{{Message: "世界世界🎉"}})
	pressLog(&lv, keyWrap)
	assertFits(t, &lv, 1, 8)
}

// A part cut before the last part ends the rows with the mark, and the
// cut keeps characters whole.
func TestLogViewWrapCutOfAPartBeforeTheLast(t *testing.T) {
	lv := newTestLogView(40, 6)
	long := "a" + strings.Repeat("日", 2000) // the cut falls inside a character
	lv.Append([]LogLine{{Message: long + "\nafter"}})
	pressLog(&lv, keyWrap)
	rows := plainRows(&lv)
	assert.True(t, strings.HasSuffix(strings.TrimRight(rows[4], " "), " …"), "%q", rows[4])
	assert.NotContains(t, strings.Join(rows, "\n"), "after")
	assert.NotContains(t, strings.Join(rows, "\n"), "�")
}

func TestLogViewWidthsWithLongSources(t *testing.T) {
	lines := []LogLine{
		{Time: logT0, Source: strings.Repeat("stream-", 20), Message: strings.Repeat("ERROR wide 日本語 ", 20)},
		{Time: logT0, Source: "s", Message: "short\nsecond line " + strings.Repeat("x", 300)},
		{Time: logT0, Message: "📄 WARN ✓ " + strings.Repeat("é", 200)},
	}
	for _, w := range []int{40, 80, 160} {
		for _, wrap := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d wrap %v", w, wrap), func(t *testing.T) {
				lv := newTestLogView(w, 12)
				lv.Append(lines)
				if wrap {
					pressLog(&lv, keyWrap)
				}
				assertFits(t, &lv, w, 12)
				pressLog(&lv, keyVisual, keyTop)
				assertFits(t, &lv, w, 12)
				pressLog(&lv, keySlash)
				typeLog(&lv, "e")
				assertFits(t, &lv, w, 12)
			})
		}
	}
}

func TestLogViewResizeWhileLinesAppend(t *testing.T) {
	lv := newTestLogView(160, 30)
	for i := range 50 {
		w := 40 + (i*37)%140
		h := 3 + (i*7)%30
		lv.SetSize(w, h)
		lv.Append([]LogLine{{Time: logT0, Source: fmt.Sprintf("src-%d", i), Message: strings.Repeat("m", i*10)}})
		if i%10 == 5 {
			pressLog(&lv, keyWrap)
		}
		assertFits(t, &lv, w, h)
		assert.Equal(t, fmt.Sprintf("src-%d", i), func() string { l, _ := lv.Selected(); return l.Source }())
		if w >= logSourceMinWidth {
			assert.Contains(t, ansi.Strip(lv.View()), fmt.Sprintf("src-%d ", i), "the last line shows")
		}
	}
}

func TestLogViewLevelWords(t *testing.T) {
	useTheme(t, Catppuccin)
	lv := newTestLogView(120, 6)
	lv.Append([]LogLine{
		{Time: logT0, Message: "ERROR failed NullPointerException"},
		{Time: logT0, Message: "WARNING disk"},
		{Time: logT0, Message: "INFO fine ERRORS"},
	})
	pressLog(&lv, keyTop)
	rows := strings.Split(lv.View(), "\n")
	assert.Contains(t, rows[0], fg(Catppuccin.Error))
	assert.Equal(t, 2, strings.Count(rows[0], fg(Catppuccin.Error)), "ERROR and the exception")
	assert.Contains(t, rows[1], fg(Catppuccin.Warning))
	assert.NotContains(t, rows[2], fg(Catppuccin.Error), "ERRORS is not the word ERROR")
}

// bg returns the SGR parameters that set c as the background color.
func bg(c interface{ RGBA() (r, g, b, a uint32) }) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("48;2;%d;%d;%d", r>>8, g>>8, b>>8)
}

func cursorLogView() LogView {
	lv := newTestLogView(80, 8)
	lv.Append([]LogLine{
		{Time: logT0, Source: "s", Message: "ERROR one"},
		{Time: logT0, Source: "s", Message: "two"},
		{Time: logT0, Source: "s", Message: "WARN three"},
	})
	return lv
}

func TestLogViewThemeSwitchAndRestyle(t *testing.T) {
	useTheme(t, Catppuccin)
	lv := cursorLogView()
	out := lv.View()
	require.Contains(t, out, bg(Catppuccin.Overlay), "the cursor")
	require.Contains(t, out, fg(Catppuccin.Error))

	useTheme(t, Nord)
	lv.Restyle()
	fresh := cursorLogView()
	assert.Equal(t, fresh.View(), lv.View())
	assert.Contains(t, lv.View(), bg(Nord.Overlay))
	assert.NotContains(t, lv.View(), bg(Catppuccin.Overlay))

	pressLog(&lv, keyVisual, keyK)
	out = lv.View()
	rows := strings.Split(out, "\n")
	visual := ansi.NewStyle().BackgroundColor(Nord.Secondary).String()
	assert.True(t, strings.HasPrefix(rows[1], visual), "the visual selection")
	assert.True(t, strings.HasPrefix(rows[2], visual))
	assert.False(t, strings.HasPrefix(rows[0], visual))
	assert.NotContains(t, out, bg(Nord.Overlay))
}

// A theme switch shows on the next draw even before Restyle, because the
// styles remember the theme they came from.
func TestLogViewThemeSwitchWithoutRestyle(t *testing.T) {
	useTheme(t, Catppuccin)
	lv := cursorLogView()
	_ = lv.View()
	useTheme(t, Nord)
	assert.Contains(t, lv.View(), bg(Nord.Overlay))
}

func TestLogViewNoColor(t *testing.T) {
	lv := cursorLogView()
	require.NotContains(t, lv.View(), reverseSGR)

	withNoColor(t)
	out := lv.View()
	rows := strings.Split(out, "\n")
	assert.True(t, strings.HasPrefix(rows[2], reverseSGR), "the cursor uses reverse video")
	assert.NotContains(t, out, "38;2;", "no colors")
	assert.NotContains(t, out, "48;2;")
	assert.Contains(t, rows[0], ansi.NewStyle().Bold().String()+"ERROR", "an error word is bold")
	assert.Contains(t, rows[2], ansi.NewStyle().Bold().String()+"WARN")

	pressLog(&lv, keyVisual, keyK)
	rows = strings.Split(lv.View(), "\n")
	assert.True(t, strings.HasPrefix(rows[1], reverseSGR), "the visual selection uses reverse video")
	assert.True(t, strings.HasPrefix(rows[2], reverseSGR))
	assert.False(t, strings.HasPrefix(rows[0], reverseSGR))
}

func TestLogViewIsARestyler(t *testing.T) {
	var _ Restyler = &LogView{}
}
