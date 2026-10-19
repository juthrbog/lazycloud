package ui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests in this file give each component AWS text with terminal control
// sequences and check that the rendered output is safe (M23).

func TestTableShowsCellsSafely(t *testing.T) {
	var styled []string
	tbl := NewTable([]Column{
		{Title: "Name", Width: 30},
		{Title: "State", Width: 16, Style: func(s string) string {
			styled = append(styled, s)
			return "<" + s + ">"
		}},
	})
	tbl.SetSize(80, 10)
	raw := "key-" + dcs + "\r.txt"
	tbl.SetRows([]Row{{Key: raw, Cells: table.Row{raw, "run" + csi8}}})

	out := tbl.View()
	assertTerminalSafe(t, out)
	assert.Contains(t, out, "key-␛P$qm␛\\␍.txt")
	assert.Contains(t, out, "<run�7m>")
	assert.Equal(t, []string{"run�7m"}, styled, "Style gets the safe text")

	assert.Equal(t, raw, tbl.CursorKey())
	assert.Equal(t, raw, tbl.SelectedRow()[0], "SelectedRow keeps the original text")

	tbl.ToggleSelect()
	out = tbl.View()
	assertTerminalSafe(t, out)
	assert.Contains(t, out, selectMarker+"key-␛P$qm␛\\␍.txt")
}

func TestTableKeepsCleanRowsUnchanged(t *testing.T) {
	tbl := testTable()
	tbl.SetSize(80, 10)
	for i := range tbl.visible {
		assert.Equal(t, tbl.rows[tbl.visible[i]].Cells, tbl.display[i])
	}
}

func TestContentViewShowsSafeTextAndKeepsRaw(t *testing.T) {
	content := "line 1 DCS[" + dcs + "]\nline 2 C1[" + csi8 + "]\r\n" +
		"line 3 CR[visible\rHIDDEN]\nline 4 OSC[" + osc52 + "]"
	for _, format := range []ContentFormat{FormatText, FormatAuto, FormatJSON, FormatShell} {
		t.Run(string(format), func(t *testing.T) {
			cv := NewContentView("notes-"+dcs+".txt", content, format)
			cv.SetSize(100, 20)

			out := cv.View()
			assertTerminalSafe(t, out)
			plain := ansi.Strip(out)
			assert.Contains(t, plain, "notes-␛P$qm␛\\.txt")
			assert.Contains(t, plain, "DCS[␛P$qm␛\\]")
			assert.Contains(t, plain, "C1[�7m]")
			assert.Contains(t, plain, "visible␍HIDDEN")
			assert.Contains(t, plain, "OSC[␛]52;c;cHduZWQ=␇]")

			assert.Equal(t, content, cv.Raw(), "the editor gets the original text")
			assert.Equal(t, 4, cv.lineCount())
			assert.Contains(t, cv.rawLines[0], dcs, "yank copies the original text")
		})
	}
}

func TestContentViewSetContentIsSafe(t *testing.T) {
	cv := NewContentView("a", "a", FormatText)
	cv.SetSize(80, 10)
	cv.SetContent("b-"+csi8, "x"+dcs, FormatText)
	out := cv.View()
	assertTerminalSafe(t, out)
	assert.Contains(t, ansi.Strip(out), "b-�7m")
	assert.Contains(t, ansi.Strip(out), "x␛P$qm␛\\")
}

func TestTabbedPanelShowsSafeTitles(t *testing.T) {
	tp := NewTabbedPanel("panel-"+dcs, []TabInput{
		{Title: "Info" + csi8, Content: "Key: k-" + dcs},
		{Title: "Tags\r", Content: "Name: " + osc52},
	})
	tp.SetSize(80, 20)

	out := tp.View()
	assertTerminalSafe(t, out)
	plain := ansi.Strip(out)
	assert.Contains(t, plain, "1:Info�7m")
	assert.Contains(t, plain, "2:Tags␍")
	assert.Contains(t, plain, "Key: k-␛P$qm␛\\")
	assert.Equal(t, "panel-␛P$qm␛\\", tp.title)
}

func TestToastShowsSafeText(t *testing.T) {
	tm := NewToastManager()
	tm.Add("Copied s3://b/k-"+dcs+"\nDeleted everything", ToastSuccess, time.Minute)

	out := tm.View(120)
	assertTerminalSafe(t, out)
	assert.Contains(t, ansi.Strip(out), "k-␛P$qm␛\\␊Deleted everything", "a key cannot add a line to a toast")
}

func TestStatusBarShowsSafeError(t *testing.T) {
	out := RenderStatusBar(StatusBarData{
		Error: "head s3://b/cr-a\nb-" + dcs + ".txt: NotFound" + csi8,
		Width: 160,
	})
	assertTerminalSafe(t, out)
	assert.Contains(t, ansi.Strip(out), "cr-a␊b-␛P$qm␛\\.txt: NotFound�7m")
	assert.Equal(t, 1, lipgloss.Height(out), "a key cannot add a line to the status bar")
}

func TestConfirmShowsSafeMessageAndKeepsLines(t *testing.T) {
	c := NewConfirm()
	c.Show("Delete 1 object(s)?\n  key-"+dcs+"\nA folder is deleted.", "delete_objects", "")

	out := c.View()
	assertTerminalSafe(t, out)
	lines := strings.Split(ansi.Strip(out), "\n")
	assert.True(t, hasLine(lines, "Delete 1 object(s)?"), "the message keeps its lines: %q", lines)
	assert.True(t, hasLine(lines, "key-␛P$qm␛\\"))
	assert.True(t, hasLine(lines, "A folder is deleted."))

	c.ShowQuick("Move all messages of q"+csi8+"?", "sqs_redrive")
	out = c.View()
	assertTerminalSafe(t, out)
	assert.Contains(t, ansi.Strip(out), "q�7m?")
}

func TestConfirmShowsSafePhrase(t *testing.T) {
	c := NewConfirm()
	c.Show("Delete?", "delete_bucket", "b"+csi8)
	out := c.View()
	assertTerminalSafe(t, out)
	assert.Contains(t, ansi.Strip(out), "type 'b�7m' + enter")

	c, _ = c.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	c, _ = c.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	out = c.View()
	assertTerminalSafe(t, out)
	assert.Contains(t, ansi.Strip(out), "Type 'b�7m' to proceed", "a wrong phrase shows the error")
}

func TestHeaderShowsSafeBreadcrumbs(t *testing.T) {
	out := RenderHeader(HeaderData{
		Profile:           "dev" + csi8,
		Breadcrumbs:       []string{"S3 Buckets", "bucket", "a-" + dcs + "\nb"},
		CrossNavBackLabel: "k" + csi8,
		Width:             160,
	})
	assertTerminalSafe(t, out)
	plain := ansi.Strip(out)
	assert.Contains(t, plain, "a-␛P$qm␛\\␊b")
	assert.Contains(t, plain, "k�7m")
	assert.Contains(t, plain, "dev�7m")
	assert.Equal(t, 2, lipgloss.Height(out), "a crumb cannot add a line to the header")
}

func TestPickerShowsSafeText(t *testing.T) {
	p := NewPicker()
	p.Show("action", "Manage k-"+dcs, []PickerOption{{Label: "Copy " + csi8, Value: "copy"}}, 0)

	out := p.View()
	assertTerminalSafe(t, out)
	plain := ansi.Strip(out)
	assert.Contains(t, plain, "Manage k-␛P$qm␛\\")
	assert.Contains(t, plain, "Copy �7m")
}

// hasLine reports whether one of lines holds want.
func hasLine(lines []string, want string) bool {
	for _, l := range lines {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}

func TestStateColorStillColorsTheStateColumn(t *testing.T) {
	tbl := NewTable([]Column{
		{Title: "ID", Width: 12},
		{Title: "State", Width: 16, Style: StateColor},
	})
	tbl.SetSize(60, 10)
	tbl.SetRows([]Row{{Key: "i-1", Cells: table.Row{"i-1", "running"}}})
	require.NotEqual(t, "running", StateColor("running"), "running has a color")
	assert.Contains(t, tbl.View(), StateColor("running"))
}
