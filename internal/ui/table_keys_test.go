package ui

import (
	"fmt"
	"testing"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover rows keyed by resource ID: the cursor and the selection
// follow the resource, not the row position.

func nameTable(names ...string) Table {
	tbl := NewTable([]Column{{Title: "Name", Width: 20}, {Title: "State", Width: 10}})
	tbl.SetSize(80, 20)
	tbl.SetRows(namedRows(names...))
	return tbl
}

func namedRows(names ...string) []Row {
	rows := make([]Row, len(names))
	for i, n := range names {
		rows[i] = Row{Key: n, Cells: table.Row{n, "ok"}}
	}
	return rows
}

func down(tbl *Table, n int) {
	for range n {
		*tbl, _ = tbl.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
}

// C3: a new row before the selected one must not move the selection.
func TestSelectionFollowsKeyWhenRowsShift(t *testing.T) {
	tbl := nameTable("a-prod", "b-tmp")
	down(&tbl, 1)
	tbl.ToggleSelect()
	require.Equal(t, []string{"b-tmp"}, tbl.SelectedKeys())

	tbl.SetRows(namedRows("0new", "a-prod", "b-tmp"))
	assert.Equal(t, []string{"b-tmp"}, tbl.SelectedKeys())
	assert.Equal(t, "b-tmp", tbl.CursorKey(), "the cursor stays on the same resource")
}

// A reload shows page 1 first. Selected rows on later pages must not count
// before they arrive, and must stay selected when they do.
func TestSelectionSurvivesPagedReload(t *testing.T) {
	tbl := nameTable("a", "b", "c")
	tbl.SelectAll()

	tbl.SetRows(namedRows("a"))
	assert.Equal(t, 1, tbl.SelectionCount())
	assert.Equal(t, []string{"a"}, tbl.SelectedKeys(), "a missing row is not a target")

	tbl.AppendRows(namedRows("b", "c"))
	assert.Equal(t, []string{"a", "b", "c"}, tbl.SelectedKeys())
}

func TestCursorKeepsPositionWhenItsRowIsGone(t *testing.T) {
	tbl := nameTable("a", "b", "c")
	down(&tbl, 1)
	tbl.SetRows(namedRows("a", "c"))
	assert.Equal(t, "c", tbl.CursorKey())
}

// M8: the marker is display only.
func TestSelectionMarkerIsNotInRowData(t *testing.T) {
	tbl := nameTable("sg-1", "sg-2")
	tbl.ToggleSelect()

	assert.Equal(t, table.Row{"sg-1", "ok"}, tbl.SelectedRow())
	assert.Equal(t, "sg-1", tbl.CursorKey())
	assert.Equal(t, "✓ sg-1", tbl.inner.Rows()[0][0], "the marker is drawn")
	assert.Equal(t, "sg-2", tbl.inner.Rows()[1][0])

	tbl.ToggleSelect()
	assert.Equal(t, "sg-1", tbl.inner.Rows()[0][0], "the marker is removed")
	assert.Equal(t, 0, tbl.SelectionCount())
}

// C4: an action never applies to a row the user cannot see. The filter
// deselects the rows it hides.
func TestFilterDeselectsHiddenRows(t *testing.T) {
	tbl := nameTable("alpha-prod", "test-1", "test-2")
	tbl.ToggleSelect() // alpha-prod
	tbl.Filter("test")
	tbl.ToggleSelect() // test-1

	assert.Equal(t, 1, tbl.SelectionCount())
	assert.Equal(t, []string{"test-1"}, tbl.SelectedKeys())
	assert.Equal(t, []string{"test-1"}, tbl.TargetKeys())

	tbl.Filter("")
	assert.Equal(t, []string{"test-1"}, tbl.SelectedKeys(), "alpha-prod stays deselected")
}

// A reload that brings a selected row back with text that no longer matches
// the filter deselects it too.
func TestReloadDeselectsRowsHiddenByFilter(t *testing.T) {
	tbl := nameTable("web-1", "web-2")
	tbl.Filter("web")
	tbl.SelectAll()
	tbl.SetRows([]Row{
		{Key: "web-1", Cells: table.Row{"web-1", "ok"}},
		{Key: "web-2", Cells: table.Row{"db-2", "ok"}}, // renamed
	})
	assert.Equal(t, []string{"web-1"}, tbl.SelectedKeys())
	tbl.Filter("")
	assert.Equal(t, []string{"web-1"}, tbl.SelectedKeys())
}

func TestSelectAllSelectsOnlyVisibleRows(t *testing.T) {
	tbl := nameTable("alpha", "beta", "gamma")
	tbl.Filter("ta") // beta
	tbl.SelectAll()
	tbl.Filter("")
	assert.Equal(t, []string{"beta"}, tbl.SelectedKeys())
}

func TestDeselectAllClearsRowsNotLoaded(t *testing.T) {
	tbl := nameTable("a", "b")
	tbl.SelectAll()
	tbl.SetRows(namedRows("a")) // page 1 of a reload
	tbl.DeselectAll()
	tbl.AppendRows(namedRows("b"))
	assert.Empty(t, tbl.SelectedKeys())
}

func TestTargetKeysFallsBackToCursor(t *testing.T) {
	tbl := nameTable("a", "b")
	down(&tbl, 1)
	assert.Equal(t, []string{"b"}, tbl.TargetKeys())
	assert.Nil(t, NewTable(nil).TargetKeys())
}

// M17: the filter matches the text, not the ANSI codes around it.
func TestFilterIgnoresANSICodes(t *testing.T) {
	tbl := NewTable([]Column{{Title: "ID", Width: 10}, {Title: "State", Width: 10}})
	tbl.SetRows([]Row{
		{Key: "i-1", Cells: table.Row{"i-1", "\x1b[38;2;166;227;161mrunning\x1b[m"}},
		{Key: "i-2", Cells: table.Row{"i-2", "\x1b[38;2;243;139;168mstopped\x1b[m"}},
	})
	for _, q := range []string{"38", "2;", ";", "[m"} {
		tbl.Filter(q)
		visible, _ := tbl.RowCount()
		assert.Zero(t, visible, "query %q matched an escape code", q)
	}
	tbl.Filter("RUN")
	assert.Equal(t, []string{"i-1"}, tbl.VisibleKeys())
}

func TestFilterDoesNotMatchAcrossCells(t *testing.T) {
	tbl := nameTable("ab")
	tbl.Filter("bo") // "ab" + "ok"
	visible, _ := tbl.RowCount()
	assert.Zero(t, visible)
}

func TestFilterKeepsCursorOnRow(t *testing.T) {
	tbl := nameTable("apple", "banana", "cherry")
	down(&tbl, 2)
	tbl.Filter("err") // only cherry is visible, at position 0
	assert.Equal(t, "cherry", tbl.CursorKey())
	tbl.Filter("")
	assert.Equal(t, "cherry", tbl.CursorKey(), "clearing the filter keeps the cursor on the row")
	assert.Equal(t, 2, tbl.inner.Cursor())
}

// M10: focus by key works in a sorted table.
func TestSetCursorByKeyInSortedTable(t *testing.T) {
	tbl := nameTable("sg-c", "sg-a", "sg-b")
	tbl.Sort(0)
	require.True(t, tbl.SetCursorByKey("sg-a"))
	assert.Equal(t, table.Row{"sg-a", "ok"}, tbl.SelectedRow())

	assert.False(t, tbl.SetCursorByKey("sg-x"))
	assert.Equal(t, "sg-a", tbl.CursorKey(), "an unknown key leaves the cursor")

	tbl.Filter("sg-b")
	assert.False(t, tbl.SetCursorByKey("sg-a"), "a hidden row cannot take the cursor")
}

func TestAppendRowsMergesIntoSortOrder(t *testing.T) {
	tbl := nameTable("b", "d")
	tbl.Sort(0)
	tbl.AppendRows(namedRows("c", "a", "e"))
	assert.Equal(t, []string{"a", "b", "c", "d", "e"}, tbl.VisibleKeys())

	tbl.SortReverse()
	tbl.AppendRows(namedRows("f"))
	assert.Equal(t, []string{"f", "e", "d", "c", "b", "a"}, tbl.VisibleKeys())
}

func TestAppendRowsUnsortedKeepsArrivalOrder(t *testing.T) {
	tbl := nameTable("b", "a")
	tbl.AppendRows(namedRows("d", "c"))
	assert.Equal(t, []string{"b", "a", "d", "c"}, tbl.VisibleKeys())
}

func TestAppendRowsMatchesFullSort(t *testing.T) {
	var all []Row
	for i := range 200 {
		n := fmt.Sprintf("k%03d", (i*37)%200)
		all = append(all, Row{Key: n, Cells: table.Row{n, fmt.Sprint(i % 7)}})
	}
	full := NewTable([]Column{{Title: "Name"}, {Title: "Group"}})
	full.Sort(1)
	full.SetRows(all)

	paged := NewTable([]Column{{Title: "Name"}, {Title: "Group"}})
	paged.Sort(1)
	for i := 0; i < len(all); i += 50 {
		paged.AppendRows(all[i : i+50])
	}
	assert.Equal(t, full.VisibleKeys(), paged.VisibleKeys(), "equal values keep their arrival order")
}

func TestDuplicateKeysAreIgnored(t *testing.T) {
	tbl := nameTable("a", "b", "a")
	_, total := tbl.RowCount()
	assert.Equal(t, 2, total)
	tbl.AppendRows(namedRows("b", "c"))
	assert.Equal(t, []string{"a", "b", "c"}, tbl.VisibleKeys())
}

func TestSetColumnsKeepsCursorAndSelection(t *testing.T) {
	tbl := nameTable("a", "b", "c")
	down(&tbl, 2)
	tbl.ToggleSelect()

	tbl.SetColumns([]Column{{Title: "Name"}, {Title: "Status"}})
	tbl.SetRows(namedRows("a", "b", "c"))
	assert.Equal(t, "c", tbl.CursorKey())
	assert.Equal(t, []string{"c"}, tbl.SelectedKeys())
}

// Some views clear the table before a reload. The cursor comes back to its
// row when the reload brings it.
func TestCursorSurvivesClearAndReload(t *testing.T) {
	tbl := nameTable("a", "b", "c")
	down(&tbl, 2)
	tbl.SetRows(nil)
	tbl.SetRows(namedRows("a", "b", "c"))
	assert.Equal(t, "c", tbl.CursorKey())
}

func TestSearchTextLowercasesMixedText(t *testing.T) {
	assert.Equal(t, "web-1\x00ärger übel\x00running",
		searchText(table.Row{"WEB-1", "Ärger ÜBEL", "\x1b[1mRUNNING\x1b[m"}))
}

func TestEmptyTable(t *testing.T) {
	tbl := NewTable([]Column{{Title: "Name"}, {Title: "Status"}})
	assert.Nil(t, tbl.SelectedRow())
	assert.Equal(t, "", tbl.CursorKey())
	tbl.ToggleSelect()
	tbl.SelectAll()
	assert.Zero(t, tbl.SelectionCount())

	tbl.SetRows(namedRows("a"))
	assert.Equal(t, "a", tbl.CursorKey(), "the cursor lands on the first row")
}

// D14: views bind d, u, and space to actions, so the table must not move on
// them. A half page moves with ctrl+d and ctrl+u.
func TestTableKeysLeaveActionLetters(t *testing.T) {
	names := make([]string, 40)
	for i := range names {
		names[i] = fmt.Sprintf("r%02d", i)
	}
	tbl := nameTable(names...)
	for _, k := range []tea.KeyPressMsg{
		{Code: 'd', Text: "d"},
		{Code: 'u', Text: "u"},
		{Code: tea.KeySpace, Text: " "},
	} {
		tbl, _ = tbl.Update(k)
		assert.Equal(t, "r00", tbl.CursorKey(), "key %q must not move the cursor", k.String())
	}
	tbl, _ = tbl.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	assert.NotEqual(t, "r00", tbl.CursorKey(), "ctrl+d moves half a page")
}

func TestSetKeyMapReplacesNavigationKeys(t *testing.T) {
	tbl := nameTable("a", "b")
	km := DefaultTableKeyMap()
	km.LineDown.SetKeys("n")
	tbl.SetKeyMap(km)

	tbl, _ = tbl.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	assert.Equal(t, "a", tbl.CursorKey(), "j is no longer bound")
	tbl, _ = tbl.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	assert.Equal(t, "b", tbl.CursorKey())
}
