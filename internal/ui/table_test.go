package ui

import (
	"testing"

	"charm.land/bubbles/v2/table"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	// Ensure styles are initialized for tests
	RebuildStyles()
}

func testTable() Table {
	columns := []Column{
		{Title: "Name", Width: 20},
		{Title: "Size", Width: 10},
		{Title: "Date", Width: 12},
	}
	tbl := NewTable(columns)
	tbl.SetRows(keyed(
		table.Row{"charlie", "100", "2024-03-01"},
		table.Row{"alpha", "300", "2024-01-01"},
		table.Row{"bravo", "200", "2024-02-01"},
	))
	return tbl
}

// keyed builds rows that use their first cell as the key.
func keyed(cells ...table.Row) []Row {
	rows := make([]Row, len(cells))
	for i, c := range cells {
		rows[i] = Row{Key: c[0], Cells: c}
	}
	return rows
}

func TestSortNext(t *testing.T) {
	tbl := testTable()

	// Initially unsorted
	assert.Equal(t, -1, tbl.sortCol)

	// First press: sort by column 0 (Name) ascending
	tbl.SortNext()
	assert.Equal(t, 0, tbl.sortCol)
	assert.Equal(t, SortAsc, tbl.sortDir)
	rows := tbl.inner.Rows()
	assert.Equal(t, "alpha", rows[0][0])
	assert.Equal(t, "bravo", rows[1][0])
	assert.Equal(t, "charlie", rows[2][0])

	// Second press: sort by column 1 (Size) ascending
	tbl.SortNext()
	assert.Equal(t, 1, tbl.sortCol)
	assert.Equal(t, SortAsc, tbl.sortDir)
	rows = tbl.inner.Rows()
	assert.Equal(t, "charlie", rows[0][0]) // "100" < "200" < "300"
	assert.Equal(t, "bravo", rows[1][0])
	assert.Equal(t, "alpha", rows[2][0])

	// Third press: sort by column 2 (Date)
	tbl.SortNext()
	assert.Equal(t, 2, tbl.sortCol)

	// Fourth press: back to unsorted
	tbl.SortNext()
	assert.Equal(t, -1, tbl.sortCol)
}

func TestSortReverse(t *testing.T) {
	tbl := testTable()

	// No-op when unsorted
	tbl.SortReverse()
	assert.Equal(t, -1, tbl.sortCol)

	// Sort by Name ascending, then reverse
	tbl.SortNext()
	assert.Equal(t, SortAsc, tbl.sortDir)

	tbl.SortReverse()
	assert.Equal(t, SortDesc, tbl.sortDir)
	rows := tbl.inner.Rows()
	assert.Equal(t, "charlie", rows[0][0])
	assert.Equal(t, "bravo", rows[1][0])
	assert.Equal(t, "alpha", rows[2][0])

	// Reverse again
	tbl.SortReverse()
	assert.Equal(t, SortAsc, tbl.sortDir)
	rows = tbl.inner.Rows()
	assert.Equal(t, "alpha", rows[0][0])
}

func TestSortWithSortKeys(t *testing.T) {
	columns := []Column{
		{Title: "Name", Width: 20},
		{Title: "Size", Width: 10},
	}
	// Sort values use zero-padded byte counts for correct numeric ordering
	rows := []Row{
		{Key: "small", Cells: table.Row{"small", "1.5 KB"}, Sort: table.Row{"small", SortKeyBytes(1536)}},
		{Key: "large", Cells: table.Row{"large", "512 MB"}, Sort: table.Row{"large", SortKeyBytes(536870912)}},
		{Key: "medium", Cells: table.Row{"medium", "2.0 MB"}, Sort: table.Row{"medium", SortKeyBytes(2097152)}},
	}

	tbl := NewTable(columns)
	tbl.SetRows(rows)

	// Sort by Size (column 1) ascending
	tbl.Sort(1)
	displayed := tbl.inner.Rows()
	assert.Equal(t, "small", displayed[0][0])  // 1.5 KB
	assert.Equal(t, "medium", displayed[1][0]) // 2 MB
	assert.Equal(t, "large", displayed[2][0])  // 512 MB

	// Reverse: descending
	tbl.SortReverse()
	displayed = tbl.inner.Rows()
	assert.Equal(t, "large", displayed[0][0])
	assert.Equal(t, "medium", displayed[1][0])
	assert.Equal(t, "small", displayed[2][0])
}

func TestSortHeaderIndicators(t *testing.T) {
	tbl := testTable()

	// No indicator when unsorted
	cols := tbl.inner.Columns()
	assert.Equal(t, "Name", cols[0].Title)
	assert.Equal(t, "Size", cols[1].Title)

	// Sort by Name ascending
	tbl.SortNext()
	cols = tbl.inner.Columns()
	assert.Equal(t, "Name ▲", cols[0].Title)
	assert.Equal(t, "Size", cols[1].Title)

	// Sort by Size ascending
	tbl.SortNext()
	cols = tbl.inner.Columns()
	assert.Equal(t, "Name", cols[0].Title)
	assert.Equal(t, "Size ▲", cols[1].Title)

	// Reverse to descending
	tbl.SortReverse()
	cols = tbl.inner.Columns()
	assert.Equal(t, "Size ▼", cols[1].Title)

	// Back to unsorted — no indicators
	tbl.SortNext() // col 2
	tbl.SortNext() // unsorted
	cols = tbl.inner.Columns()
	assert.Equal(t, "Name", cols[0].Title)
	assert.Equal(t, "Size", cols[1].Title)
	assert.Equal(t, "Date", cols[2].Title)
}

func TestSortKeepsCursorOnRow(t *testing.T) {
	tbl := testTable()
	require.Equal(t, "charlie", tbl.CursorKey())

	// Sort by Name ascending: alpha, bravo, charlie. The cursor follows charlie.
	tbl.SortNext()
	assert.Equal(t, []string{"alpha", "bravo", "charlie"}, tbl.VisibleKeys())
	assert.Equal(t, "charlie", tbl.CursorKey())
	assert.Equal(t, 2, tbl.inner.Cursor())
}

func TestSortWithFilter(t *testing.T) {
	tbl := testTable()

	// Filter to only rows containing "a" (alpha, bravo, charlie — all have 'a')
	tbl.Filter("al") // only "alpha"
	filtered, total := tbl.RowCount()
	assert.Equal(t, 1, filtered)
	assert.Equal(t, 3, total)

	// Sort while filtered
	tbl.SortNext() // sort by Name
	rows := tbl.inner.Rows()
	assert.Equal(t, 1, len(rows))
	assert.Equal(t, "alpha", rows[0][0])

	// Clear filter — sort should still apply
	tbl.Filter("")
	rows = tbl.inner.Rows()
	assert.Equal(t, 3, len(rows))
	assert.Equal(t, "alpha", rows[0][0])
	assert.Equal(t, "bravo", rows[1][0])
	assert.Equal(t, "charlie", rows[2][0])
}

func TestSortKeyBytes(t *testing.T) {
	assert.Equal(t, "00000000000000000000", SortKeyBytes(0))
	assert.Equal(t, "00000000000000001536", SortKeyBytes(1536))
	assert.Less(t, SortKeyBytes(1536), SortKeyBytes(2097152))
	assert.Less(t, SortKeyBytes(2097152), SortKeyBytes(536870912))
}

func TestSetColumnsSwapsColumns(t *testing.T) {
	tbl := testTable()

	newCols := []Column{
		{Title: "Name", Width: 20},
		{Title: "Size", Width: 10},
	}
	tbl.SetColumns(newCols)

	assert.Equal(t, 2, len(tbl.flexColumns))
	assert.Equal(t, -1, tbl.sortCol, "an unsorted table stays unsorted")
}

func TestSetColumnsClearsSortWhenColumnIsGone(t *testing.T) {
	tbl := testTable()
	tbl.Sort(0) // sort by Name
	assert.Equal(t, 0, tbl.sortCol)

	tbl.SetColumns([]Column{
		{Title: "A", Width: 10},
		{Title: "B", Width: 10},
	})

	assert.Equal(t, -1, tbl.sortCol, "sort column should reset")
}

// A change of width tier can move the sorted column to another index. The
// sort follows the column title.
func TestSetColumnsKeepsSortOnMovedColumn(t *testing.T) {
	tbl := testTable()
	tbl.Sort(1)
	tbl.SortReverse() // Size, descending

	tbl.SetColumns([]Column{
		{Title: "Name", Width: 20},
		{Title: "Date", Width: 12},
		{Title: "Size", Width: 10},
	})
	tbl.SetRows(keyed(
		table.Row{"charlie", "2024-03-01", "100"},
		table.Row{"alpha", "2024-01-01", "300"},
		table.Row{"bravo", "2024-02-01", "200"},
	))

	assert.Equal(t, 2, tbl.sortCol)
	assert.Equal(t, SortDesc, tbl.sortDir)
	assert.Equal(t, "Size ▼", tbl.inner.Columns()[2].Title)
	assert.Equal(t, []string{"alpha", "bravo", "charlie"}, tbl.VisibleKeys())
}
