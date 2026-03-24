package ui

import (
	"testing"

	"charm.land/bubbles/v2/table"
	"github.com/stretchr/testify/assert"
)

func init() {
	// Ensure styles are initialized for tests
	RebuildStyles()
}

func testTable() Table {
	columns := []table.Column{
		{Title: "Name", Width: 20},
		{Title: "Size", Width: 10},
		{Title: "Date", Width: 12},
	}
	rows := []table.Row{
		{"charlie", "100", "2024-03-01"},
		{"alpha", "300", "2024-01-01"},
		{"bravo", "200", "2024-02-01"},
	}
	return NewTable(columns, rows)
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
	columns := []table.Column{
		{Title: "Name", Width: 20},
		{Title: "Size", Width: 10},
	}
	rows := []table.Row{
		{"small", "1.5 KB"},
		{"large", "512 MB"},
		{"medium", "2.0 MB"},
	}
	// Sort keys use zero-padded byte counts for correct numeric ordering
	sortKeys := []table.Row{
		{"small", SortKeyBytes(1536)},       // 1.5 KB
		{"large", SortKeyBytes(536870912)},  // 512 MB
		{"medium", SortKeyBytes(2097152)},   // 2 MB
	}

	tbl := NewTable(columns, rows)
	tbl.SetRowsWithSortKeys(rows, sortKeys)

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
	cols := tbl.columns
	assert.Equal(t, "Name", cols[0].Title)
	assert.Equal(t, "Size", cols[1].Title)

	// Sort by Name ascending
	tbl.SortNext()
	assert.Equal(t, "Name ▲", tbl.columns[0].Title)
	assert.Equal(t, "Size", tbl.columns[1].Title)

	// Sort by Size ascending
	tbl.SortNext()
	assert.Equal(t, "Name", tbl.columns[0].Title)
	assert.Equal(t, "Size ▲", tbl.columns[1].Title)

	// Reverse to descending
	tbl.SortReverse()
	assert.Equal(t, "Size ▼", tbl.columns[1].Title)

	// Back to unsorted — no indicators
	tbl.SortNext() // col 2
	tbl.SortNext() // unsorted
	assert.Equal(t, "Name", tbl.columns[0].Title)
	assert.Equal(t, "Size", tbl.columns[1].Title)
	assert.Equal(t, "Date", tbl.columns[2].Title)
}

func TestSortPreservesFilteredMap(t *testing.T) {
	tbl := testTable()

	// Sort by Name ascending: alpha(1), bravo(2), charlie(0)
	tbl.SortNext()

	// Verify filteredMap tracks original indices correctly
	assert.Equal(t, 1, tbl.filteredMap[0]) // alpha was allRows[1]
	assert.Equal(t, 2, tbl.filteredMap[1]) // bravo was allRows[2]
	assert.Equal(t, 0, tbl.filteredMap[2]) // charlie was allRows[0]

	// SelectedAllRowIndex should return the original allRows index
	// (cursor defaults to 0, which should be alpha = allRows[1])
	assert.Equal(t, 1, tbl.SelectedAllRowIndex())
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

	newCols := []table.Column{
		{Title: "Name", Width: 20},
		{Title: "Size", Width: 10},
	}
	tbl.SetColumns(newCols)

	assert.Equal(t, 2, len(tbl.columns))
	assert.Equal(t, -1, tbl.sortCol, "sort should be reset after SetColumns")
}

func TestSetColumnsResetsSortState(t *testing.T) {
	tbl := testTable()
	tbl.Sort(0) // sort by Name
	assert.Equal(t, 0, tbl.sortCol)

	tbl.SetColumns([]table.Column{
		{Title: "A", Width: 10},
		{Title: "B", Width: 10},
	})

	assert.Equal(t, -1, tbl.sortCol, "sort column should reset")
}
