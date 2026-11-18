package ui

import (
	"fmt"
	"image/color"
	"testing"

	"charm.land/bubbles/v2/table"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// useTheme makes th active and puts the default theme back after the test.
func useTheme(t *testing.T, th Theme) {
	t.Helper()
	t.Cleanup(func() { ActiveTheme = DefaultTheme; RebuildStyles() })
	ActiveTheme = th
	RebuildStyles()
}

// fg returns the SGR parameters that set c as the foreground color.
func fg(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)
}

// stateTable gives a sorted, filtered table with a styled state column, a
// selected row and the cursor on another row.
func stateTable(t *testing.T) Table {
	t.Helper()
	tbl := NewTable([]Column{
		{Title: "Name", Width: 12},
		{Title: "State", Width: 16, Style: StateColor},
	})
	tbl.SetSize(60, 10)
	tbl.SetRows([]Row{
		{Key: "a", Cells: table.Row{"web-a", "running"}},
		{Key: "b", Cells: table.Row{"web-b", "stopped"}},
		{Key: "c", Cells: table.Row{"web-c", "running"}},
		{Key: "x", Cells: table.Row{"db-x", "running"}},
	})
	tbl.Sort(0)
	tbl.SortReverse()
	tbl.Filter("web")
	require.True(t, tbl.SetCursorByKey("b"))
	tbl.ToggleSelect()
	require.True(t, tbl.SetCursorByKey("a"))
	return tbl
}

// A8: Restyle draws the header, the cursor row and the styled cells with
// the new theme. The table then looks the same as a table made with the
// new theme, and keeps its rows, sort, filter, selection and cursor.
func TestTableRestyleTakesTheNewTheme(t *testing.T) {
	useTheme(t, Catppuccin)
	tbl := stateTable(t)
	require.Contains(t, tbl.View(), fg(Catppuccin.StateRunning))

	useTheme(t, Nord)
	require.Contains(t, tbl.View(), fg(Catppuccin.StateRunning), "the cells keep the old colors until Restyle")
	tbl.Restyle()

	assert.Equal(t, stateTable(t).View(), tbl.View())
	assert.NotContains(t, tbl.View(), fg(Catppuccin.StateRunning))
	assert.Contains(t, tbl.View(), fg(Nord.StateRunning))
	assert.Contains(t, tbl.View(), fg(Nord.Primary), "the header")
	assert.Equal(t, "a", tbl.CursorKey())
	assert.Equal(t, []string{"b"}, tbl.SelectedKeys())
	assert.Equal(t, []string{"web-c", "web-b", "web-a"}, cellsOf(tbl.VisibleKeys(), tbl))
}

// A8: Restyle also takes the NoColor setting, so the cursor row uses
// reverse video.
func TestTableRestyleTakesNoColor(t *testing.T) {
	tbl := stateTable(t)
	withNoColor(t)
	require.NotEqual(t, stateTable(t).View(), tbl.View())

	tbl.Restyle()

	assert.Equal(t, stateTable(t).View(), tbl.View())
}

// A8: Restyle on an empty table does nothing wrong.
func TestTableRestyleWithoutRows(t *testing.T) {
	tbl := NewTable([]Column{{Title: "Name", Width: 12}})
	tbl.Restyle()
	assert.Empty(t, tbl.VisibleKeys())
}

// cellsOf returns the first cell of the rows with the given keys.
func cellsOf(keys []string, tbl Table) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = tbl.rows[tbl.index[k]].Cells[0]
	}
	return out
}
