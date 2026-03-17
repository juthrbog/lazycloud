package ui

import (
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

// Table wraps bubbles/table with sort and filter support.
type Table struct {
	inner      table.Model
	allRows    []table.Row
	columns    []table.Column
	sortCol    int
	sortDir    SortDirection
	filterText string
	width      int
	height     int
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
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(theme.Secondary).
		BorderBottom(true).
		Bold(true).
		Foreground(theme.Primary)
	st.Selected = st.Selected.
		Foreground(theme.BrightText).
		Background(theme.Overlay).
		Bold(false)
	t.SetStyles(st)

	return Table{
		inner:   t,
		allRows: rows,
		columns: columns,
		sortCol: -1,
	}
}

// SetSize sets the table dimensions.
func (t *Table) SetSize(w, h int) {
	t.width = w
	t.height = h
	t.inner.SetWidth(w)
	t.inner.SetHeight(h)
}

// SetRows replaces the table data and reapplies sort/filter.
func (t *Table) SetRows(rows []table.Row) {
	t.allRows = rows
	t.applyFilterAndSort()
}

// SelectedRow returns the currently selected row.
func (t Table) SelectedRow() table.Row {
	return t.inner.SelectedRow()
}

// SelectedIndex returns the cursor position.
func (t Table) SelectedIndex() int {
	return t.inner.Cursor()
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

func (t *Table) applyFilterAndSort() {
	rows := t.allRows

	// Filter
	if t.filterText != "" {
		lower := strings.ToLower(t.filterText)
		var filtered []table.Row
		for _, row := range rows {
			for _, cell := range row {
				if strings.Contains(strings.ToLower(cell), lower) {
					filtered = append(filtered, row)
					break
				}
			}
		}
		rows = filtered
	}

	// Sort
	if t.sortCol >= 0 && t.sortCol < len(t.columns) {
		col := t.sortCol
		dir := t.sortDir
		sort.SliceStable(rows, func(i, j int) bool {
			if dir == SortAsc {
				return rows[i][col] < rows[j][col]
			}
			return rows[i][col] > rows[j][col]
		})
	}

	t.inner.SetRows(rows)
}
