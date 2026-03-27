package ui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// SortDirection controls table column sort order.
type SortDirection int

const (
	SortAsc SortDirection = iota
	SortDesc
)

// Table wraps bubbles/table with sort, filter, and multi-select support.
type Table struct {
	inner       table.Model
	allRows     []table.Row
	columns     []table.Column
	baseColumns []table.Column // original titles without sort indicators
	sortCol     int
	sortDir     SortDirection
	sortKeys    []table.Row // parallel to allRows; nil = use display value
	filterText  string
	selected    map[int]bool // indices into allRows
	filteredMap []int        // maps filtered row index → allRows index
	width       int
	height      int
}

// NewTable creates a Table with the given columns and rows.
func NewTable(columns []table.Column, rows []table.Row) Table {
	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(true),
	)

	theme := ActiveTheme
	st := table.DefaultStyles()
	st.Header = st.Header.
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(theme.Secondary).
		BorderBottom(true).
		Bold(true).
		Foreground(theme.Primary)
	st.Selected = st.Selected.
		Foreground(theme.BrightText).
		Background(theme.Overlay).
		Bold(false)
	t.SetStyles(st)

	baseCols := make([]table.Column, len(columns))
	copy(baseCols, columns)

	tbl := Table{
		inner:       t,
		allRows:     rows,
		columns:     columns,
		baseColumns: baseCols,
		sortCol:     -1,
		selected:    make(map[int]bool),
	}
	tbl.buildFilteredMap()
	return tbl
}

// SetSize sets the table dimensions.
func (t *Table) SetSize(w, h int) {
	t.width = w
	t.height = h
	t.inner.SetWidth(w)
	t.inner.SetHeight(h)
}

// Columns returns the current column definitions.
func (t Table) Columns() []table.Column {
	return t.columns
}

// SetColumns swaps the column set without recreating the table.
// Clears rows and resets sort state since column indices may have changed.
// Callers should call SetRows or SetRowsWithSortKeys after SetColumns.
func (t *Table) SetColumns(columns []table.Column) {
	t.columns = columns
	baseCols := make([]table.Column, len(columns))
	copy(baseCols, columns)
	t.baseColumns = baseCols
	t.allRows = nil
	t.sortKeys = nil
	t.selected = make(map[int]bool)
	t.inner.SetRows(nil)
	t.inner.SetColumns(columns)
	t.sortCol = -1
	t.sortDir = SortAsc
	t.buildFilteredMap()
}

// SetRows replaces the table data and reapplies sort/filter. Clears selection and sort keys.
func (t *Table) SetRows(rows []table.Row) {
	t.allRows = rows
	t.sortKeys = nil
	t.selected = make(map[int]bool)
	t.applyFilterAndSort()
}

// SetRowsWithSortKeys replaces the table data with parallel sort keys.
// Sort keys must be the same length as rows; each sort key row provides
// string values that sort correctly for each column.
func (t *Table) SetRowsWithSortKeys(rows []table.Row, sortKeys []table.Row) {
	t.allRows = rows
	t.sortKeys = sortKeys
	t.selected = make(map[int]bool)
	t.applyFilterAndSort()
}

// SelectedRow returns the currently highlighted row.
func (t Table) SelectedRow() table.Row {
	return t.inner.SelectedRow()
}

// SelectedIndex returns the cursor position in the filtered view.
func (t Table) SelectedIndex() int {
	return t.inner.Cursor()
}

// SelectedAllRowIndex returns the allRows index for the current cursor position.
// Returns -1 if no valid mapping exists.
func (t Table) SelectedAllRowIndex() int {
	idx := t.inner.Cursor()
	if idx < 0 || idx >= len(t.filteredMap) {
		return -1
	}
	return t.filteredMap[idx]
}

// ToggleSelect toggles selection on the current row and re-renders markers.
func (t *Table) ToggleSelect() {
	idx := t.SelectedAllRowIndex()
	if idx < 0 {
		return
	}
	if t.selected[idx] {
		delete(t.selected, idx)
	} else {
		t.selected[idx] = true
	}
	t.applyFilterAndSort()
}

// SelectAll selects all currently visible (filtered) rows.
func (t *Table) SelectAll() {
	for _, allIdx := range t.filteredMap {
		t.selected[allIdx] = true
	}
	t.applyFilterAndSort()
}

// DeselectAll clears all selections.
func (t *Table) DeselectAll() {
	t.selected = make(map[int]bool)
	t.applyFilterAndSort()
}

// ToggleSelectAll selects all if none are selected, otherwise deselects all.
func (t *Table) ToggleSelectAll() {
	if len(t.selected) > 0 {
		t.DeselectAll()
	} else {
		t.SelectAll()
	}
}

// SelectionCount returns the number of selected rows.
func (t Table) SelectionCount() int {
	return len(t.selected)
}

// SelectedIndices returns the allRows indices of all selected rows.
func (t Table) SelectedIndices() []int {
	indices := make([]int, 0, len(t.selected))
	for idx := range t.selected {
		indices = append(indices, idx)
	}
	sort.Ints(indices)
	return indices
}

// FilteredIndices returns the allRows indices of all currently visible (filtered) rows.
func (t Table) FilteredIndices() []int {
	out := make([]int, len(t.filteredMap))
	copy(out, t.filteredMap)
	return out
}

// IsSelected returns whether the given allRows index is selected.
func (t Table) IsSelected(allRowIdx int) bool {
	return t.selected[allRowIdx]
}

// Sort sorts the table by the given column index.
func (t *Table) Sort(col int) {
	if t.sortCol == col {
		if t.sortDir == SortAsc {
			t.sortDir = SortDesc
		} else {
			t.sortDir = SortAsc
		}
	} else {
		t.sortCol = col
		t.sortDir = SortAsc
	}
	t.updateColumnHeaders()
	t.applyFilterAndSort()
}

// SortColumnNames returns the base column titles and the currently sorted column index.
func (t Table) SortColumnNames() ([]string, int) {
	names := make([]string, len(t.baseColumns))
	for i, c := range t.baseColumns {
		names[i] = c.Title
	}
	return names, t.sortCol
}

// ClearSort removes any active sort, restoring the original row order.
func (t *Table) ClearSort() {
	t.sortCol = -1
	t.sortDir = SortAsc
	t.updateColumnHeaders()
	t.applyFilterAndSort()
}

// SortNext advances to the next sort column (ascending), or clears sort after the last column.
func (t *Table) SortNext() {
	next := t.sortCol + 1
	if next >= len(t.baseColumns) {
		t.sortCol = -1
	} else {
		t.sortCol = next
		t.sortDir = SortAsc
	}
	t.updateColumnHeaders()
	t.applyFilterAndSort()
}

// SortReverse reverses the current sort direction. No-op if no column is sorted.
func (t *Table) SortReverse() {
	if t.sortCol < 0 {
		return
	}
	if t.sortDir == SortAsc {
		t.sortDir = SortDesc
	} else {
		t.sortDir = SortAsc
	}
	t.updateColumnHeaders()
	t.applyFilterAndSort()
}

// Filter applies a text filter across all columns.
func (t *Table) Filter(text string) {
	t.filterText = text
	t.applyFilterAndSort()
}

// RowCount returns (filtered count, total count).
func (t Table) RowCount() (int, int) {
	return len(t.inner.Rows()), len(t.allRows)
}

// Update handles table navigation messages.
func (t Table) Update(msg tea.Msg) (Table, tea.Cmd) {
	var cmd tea.Cmd
	t.inner, cmd = t.inner.Update(msg)
	return t, cmd
}

// View renders the table.
func (t Table) View() string {
	return t.inner.View()
}

// updateColumnHeaders rebuilds column titles with sort indicators.
func (t *Table) updateColumnHeaders() {
	cols := make([]table.Column, len(t.baseColumns))
	for i, c := range t.baseColumns {
		cols[i] = c
		if i == t.sortCol {
			if t.sortDir == SortAsc {
				cols[i].Title = c.Title + " ▲"
			} else {
				cols[i].Title = c.Title + " ▼"
			}
		}
	}
	t.columns = cols
	t.inner.SetColumns(cols)
}

func (t *Table) applyFilterAndSort() {
	t.filteredMap = nil
	rows := t.allRows

	// Filter
	if t.filterText != "" {
		lower := strings.ToLower(t.filterText)
		var filtered []table.Row
		for i, row := range rows {
			for _, cell := range row {
				if strings.Contains(strings.ToLower(cell), lower) {
					filtered = append(filtered, row)
					t.filteredMap = append(t.filteredMap, i)
					break
				}
			}
		}
		rows = filtered
	} else {
		t.buildFilteredMap()
	}

	// Sort — reorder both rows and filteredMap in tandem
	if t.sortCol >= 0 && t.sortCol < len(t.columns) {
		col := t.sortCol
		dir := t.sortDir
		indices := make([]int, len(rows))
		for i := range indices {
			indices[i] = i
		}
		sort.SliceStable(indices, func(i, j int) bool {
			ii, jj := indices[i], indices[j]
			origI := t.filteredMap[ii]
			origJ := t.filteredMap[jj]
			vi, vj := rows[ii][col], rows[jj][col]
			if t.sortKeys != nil {
				if origI < len(t.sortKeys) && col < len(t.sortKeys[origI]) {
					vi = t.sortKeys[origI][col]
				}
				if origJ < len(t.sortKeys) && col < len(t.sortKeys[origJ]) {
					vj = t.sortKeys[origJ][col]
				}
			}
			if dir == SortAsc {
				return vi < vj
			}
			return vi > vj
		})
		sortedRows := make([]table.Row, len(rows))
		sortedMap := make([]int, len(rows))
		for i, idx := range indices {
			sortedRows[i] = rows[idx]
			sortedMap[i] = t.filteredMap[idx]
		}
		rows = sortedRows
		t.filteredMap = sortedMap
	}

	// Apply selection markers to display rows
	if len(t.selected) > 0 {
		marked := make([]table.Row, len(rows))
		for i, row := range rows {
			allIdx := t.filteredMap[i]
			if t.selected[allIdx] {
				// Clone the row and prepend checkmark to first cell
				newRow := make(table.Row, len(row))
				copy(newRow, row)
				newRow[0] = "✓ " + newRow[0]
				marked[i] = newRow
			} else {
				marked[i] = row
			}
		}
		rows = marked
	}

	t.inner.SetRows(rows)
}

func (t *Table) buildFilteredMap() {
	t.filteredMap = make([]int, len(t.allRows))
	for i := range t.allRows {
		t.filteredMap[i] = i
	}
}

// SortKeyBytes returns a zero-padded string suitable for numeric byte sorting.
func SortKeyBytes(size int64) string {
	return fmt.Sprintf("%020d", size)
}
