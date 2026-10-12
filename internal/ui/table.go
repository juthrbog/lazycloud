package ui

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// SortDirection controls table column sort order.
type SortDirection int

const (
	SortAsc SortDirection = iota
	SortDesc
)

// Row is one table row. Key identifies the resource in the row, for example
// an instance ID or an S3 object key. The cursor and the selection follow
// the key, so they stay on the same resource when the rows are reloaded,
// sorted, or filtered.
type Row struct {
	Key   string
	Cells table.Row
	// Sort holds optional sort values, one per column. The table sorts a
	// column on the cell text when Sort has no value for it.
	Sort table.Row
}

// selectMarker is drawn in front of the first cell of a selected row. It is
// display only: Row data and SelectedRow never contain it.
const selectMarker = "✓ "

// Table wraps bubbles/table with sort, filter, and multi-select support.
type Table struct {
	inner       table.Model
	rows        []Row
	search      []string       // per row: filter text, built on the first filter (see ensureSearch)
	index       map[string]int // key → index into rows
	order       []int          // indices into rows, in sort order
	visible     []int          // indices into rows that pass the filter, in display order
	display     []table.Row    // cells shown by inner, parallel to visible
	selected    map[string]bool
	selCount    int    // selected rows in visible
	keepCursor  string // cursor key to restore after SetColumns
	flexColumns []Column
	sortCol     int
	sortDir     SortDirection
	filterText  string
	width       int
	height      int
}

// NewTable creates an empty Table with the given columns.
func NewTable(columns []Column) Table {
	resolved := DistributeWidths(columns, 0)
	t := table.New(
		table.WithColumns(resolved),
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

	t.KeyMap = DefaultTableKeyMap()

	flex := make([]Column, len(columns))
	copy(flex, columns)

	return Table{
		inner:       t,
		index:       make(map[string]int),
		selected:    make(map[string]bool),
		flexColumns: flex,
		sortCol:     -1,
	}
}

// DefaultTableKeyMap returns the navigation keys of a Table. It leaves out
// the single letters that views bind to actions: space selects, d describes,
// and u presigns. A half page moves with ctrl+d and ctrl+u.
func DefaultTableKeyMap() table.KeyMap {
	km := table.DefaultKeyMap()
	km.PageDown.SetKeys("f", "pgdown")
	km.HalfPageUp = key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "½ page up"))
	km.HalfPageDown = key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "½ page down"))
	return km
}

// SetKeyMap replaces the navigation keys, for example to free a key that a
// view binds to an action.
func (t *Table) SetKeyMap(km table.KeyMap) {
	t.inner.KeyMap = km
}

// SetSize sets the table dimensions and redistributes column widths.
func (t *Table) SetSize(w, h int) {
	t.width = w
	t.height = h
	t.resolveColumns()
	t.inner.SetWidth(w)
	t.inner.SetHeight(h)
}

// Columns returns the flex column definitions.
func (t Table) Columns() []Column {
	return t.flexColumns
}

// SetColumns swaps the column set without recreating the table, for example
// when a change in width moves the view to another tier. It clears the rows.
// The sort stays on the column with the same title, at its new index, and is
// cleared when the new set has no such column. The selection and the cursor
// key are kept, because callers repopulate rows with the same data.
func (t *Table) SetColumns(columns []Column) {
	sortCol := -1
	if t.sorted() {
		sortCol = columnIndex(columns, t.flexColumns[t.sortCol].Title, t.sortCol)
	}
	flex := make([]Column, len(columns))
	copy(flex, columns)
	t.flexColumns = flex
	if k := t.CursorKey(); k != "" {
		t.keepCursor = k
	}
	t.rows, t.search, t.order, t.visible, t.display = nil, nil, nil, nil, nil
	t.index = make(map[string]int)
	t.selCount = 0
	t.inner.SetRows(nil)
	t.sortCol = sortCol
	if sortCol < 0 {
		t.sortDir = SortAsc
	}
	t.resolveColumns()
}

// columnIndex returns the index of the column with the given title, or -1.
// It prefers index hint, so that columns with the same title (for example
// two empty titles) keep their position.
func columnIndex(columns []Column, title string, hint int) int {
	if hint >= 0 && hint < len(columns) && columns[hint].Title == title {
		return hint
	}
	for i, c := range columns {
		if c.Title == title {
			return i
		}
	}
	return -1
}

// resolveColumns computes fixed-width columns from flex definitions
// and applies them (with sort indicators) to the inner table.
func (t *Table) resolveColumns() {
	resolved := DistributeWidths(t.flexColumns, t.width)
	// Apply sort indicators
	if t.sortCol >= 0 && t.sortCol < len(resolved) {
		if t.sortDir == SortAsc {
			resolved[t.sortCol].Title = t.flexColumns[t.sortCol].Title + " ▲"
		} else {
			resolved[t.sortCol].Title = t.flexColumns[t.sortCol].Title + " ▼"
		}
	}
	t.inner.SetColumns(resolved)
}

// SetRows replaces the table data and reapplies sort and filter. The cursor
// stays on the same key when that key is still present. The selection is
// kept by key: a selected key that is missing does not count, and it is
// selected again when a later page or reload brings its row back. A row
// whose key appeared earlier is ignored. When rows is empty, for example
// while a view reloads, the table remembers the cursor key for the next rows.
func (t *Table) SetRows(rows []Row) {
	cursor := t.cursorToKeep()
	t.rows = make([]Row, 0, len(rows))
	t.search = nil
	t.index = make(map[string]int, len(rows))
	t.addRows(rows)
	t.sortOrder()
	t.refresh(cursor)
	if len(t.rows) == 0 {
		t.keepCursor = cursor
	}
}

// AppendRows adds rows after the current ones, for example the next page of
// a paginated list. It sorts only the new rows and merges them into the
// existing order. A row whose key is already present is ignored.
func (t *Table) AppendRows(rows []Row) {
	cursor := t.cursorToKeep()
	start := len(t.rows)
	t.addRows(rows)
	added := make([]int, 0, len(t.rows)-start)
	for i := start; i < len(t.rows); i++ {
		added = append(added, i)
	}
	if t.sorted() {
		sort.SliceStable(added, func(a, b int) bool { return t.less(added[a], added[b]) })
		t.order = t.mergeOrder(t.order, added)
	} else {
		t.order = append(t.order, added...)
	}
	t.refresh(cursor)
}

func (t *Table) addRows(rows []Row) {
	for _, r := range rows {
		if _, dup := t.index[r.Key]; dup {
			continue
		}
		t.index[r.Key] = len(t.rows)
		t.rows = append(t.rows, r)
	}
}

// ensureSearch builds the filter text for the rows that do not have it yet.
// A table that is never filtered never builds it.
func (t *Table) ensureSearch() {
	for i := len(t.search); i < len(t.rows); i++ {
		t.search = append(t.search, searchText(t.rows[i].Cells))
	}
}

// cursorToKeep returns the key the cursor must stay on after new rows arrive.
func (t *Table) cursorToKeep() string {
	k := t.CursorKey()
	if k == "" {
		k = t.keepCursor
	}
	t.keepCursor = ""
	return k
}

// searchText returns the text the filter matches for a row: the cells in
// lowercase, without ANSI codes, joined with NUL so that a query never
// matches across two cells.
func searchText(cells table.Row) string {
	n := len(cells)
	for _, c := range cells {
		n += len(c)
	}
	var b strings.Builder
	b.Grow(n)
	for i, c := range cells {
		if i > 0 {
			b.WriteByte(0)
		}
		if strings.IndexByte(c, ansi.ESC) >= 0 {
			c = ansi.Strip(c)
		}
		writeLower(&b, c)
	}
	return b.String()
}

// writeLower writes s in lowercase. ASCII text needs no allocation.
func writeLower(b *strings.Builder, s string) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= utf8.RuneSelf {
			b.WriteString(strings.ToLower(s[i:]))
			return
		}
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
}

// SelectedRow returns the cells of the row under the cursor, without the
// selection marker. It returns nil when no row is visible.
func (t Table) SelectedRow() table.Row {
	if i, ok := t.cursorRow(); ok {
		return t.rows[i].Cells
	}
	return nil
}

// CursorKey returns the key of the row under the cursor, or "" when no row
// is visible.
func (t Table) CursorKey() string {
	if i, ok := t.cursorRow(); ok {
		return t.rows[i].Key
	}
	return ""
}

// cursorRow returns the index into rows of the row under the cursor.
func (t Table) cursorRow() (int, bool) {
	n := t.inner.Cursor()
	if n < 0 || n >= len(t.visible) {
		return 0, false
	}
	return t.visible[n], true
}

// SetCursorByKey moves the cursor to the row with the given key. It returns
// false, and leaves the cursor where it is, when no visible row has the key.
func (t *Table) SetCursorByKey(key string) bool {
	i, ok := t.index[key]
	if !ok {
		return false
	}
	for n, v := range t.visible {
		if v == i {
			t.inner.SetCursor(n)
			return true
		}
	}
	return false
}

// ToggleSelect toggles the selection of the row under the cursor.
func (t *Table) ToggleSelect() {
	n := t.inner.Cursor()
	if n < 0 || n >= len(t.visible) {
		return
	}
	key := t.rows[t.visible[n]].Key
	if t.selected[key] {
		delete(t.selected, key)
		t.selCount--
	} else {
		t.selected[key] = true
		t.selCount++
	}
	// Only the marker of this row changes.
	t.display[n] = t.displayRow(t.visible[n])
	t.inner.SetRows(t.display)
}

// SelectAll selects all visible rows.
func (t *Table) SelectAll() {
	for _, i := range t.visible {
		t.selected[t.rows[i].Key] = true
	}
	t.redraw()
}

// DeselectAll clears the selection, also for selected keys whose rows are
// not loaded.
func (t *Table) DeselectAll() {
	clear(t.selected)
	t.redraw()
}

// ToggleSelectAll selects all visible rows if none is selected, otherwise
// deselects all.
func (t *Table) ToggleSelectAll() {
	if t.selCount > 0 {
		t.DeselectAll()
	} else {
		t.SelectAll()
	}
}

// SelectionCount returns the number of selected rows. The filter deselects
// the rows it hides, so every counted row is visible.
func (t Table) SelectionCount() int {
	return t.selCount
}

// SelectedKeys returns the keys of the selected rows, in display order.
func (t Table) SelectedKeys() []string {
	if t.selCount == 0 {
		return nil
	}
	keys := make([]string, 0, t.selCount)
	for _, i := range t.visible {
		if k := t.rows[i].Key; t.selected[k] {
			keys = append(keys, k)
		}
	}
	return keys
}

// TargetKeys returns the keys an action applies to: the selected rows, or
// the row under the cursor when no row is selected.
func (t Table) TargetKeys() []string {
	if keys := t.SelectedKeys(); len(keys) > 0 {
		return keys
	}
	if k := t.CursorKey(); k != "" {
		return []string{k}
	}
	return nil
}

// VisibleKeys returns the keys of all visible rows, in display order.
func (t Table) VisibleKeys() []string {
	keys := make([]string, len(t.visible))
	for n, i := range t.visible {
		keys[n] = t.rows[i].Key
	}
	return keys
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
	t.resort()
}

// SortColumnNames returns the base column titles and the currently sorted column index.
func (t Table) SortColumnNames() ([]string, int) {
	names := make([]string, len(t.flexColumns))
	for i, c := range t.flexColumns {
		names[i] = c.Title
	}
	return names, t.sortCol
}

// ClearSort removes any active sort, restoring the original row order.
func (t *Table) ClearSort() {
	t.sortCol = -1
	t.sortDir = SortAsc
	t.resort()
}

// SortNext advances to the next sort column (ascending), or clears sort after the last column.
func (t *Table) SortNext() {
	next := t.sortCol + 1
	if next >= len(t.flexColumns) {
		t.sortCol = -1
	} else {
		t.sortCol = next
		t.sortDir = SortAsc
	}
	t.resort()
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
	t.resort()
}

// Filter shows only the rows that contain text in any cell, ignoring case
// and ANSI codes. Selected rows that the filter hides are deselected.
func (t *Table) Filter(text string) {
	if text == t.filterText {
		return
	}
	cursor := t.CursorKey()
	t.filterText = text
	t.refresh(cursor)
}

// RowCount returns (filtered count, total count).
func (t Table) RowCount() (int, int) {
	return len(t.visible), len(t.rows)
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

// resort applies a new sort column or direction.
func (t *Table) resort() {
	cursor := t.CursorKey()
	t.resolveColumns()
	t.sortOrder()
	t.refresh(cursor)
}

func (t *Table) sorted() bool {
	return t.sortCol >= 0 && t.sortCol < len(t.flexColumns)
}

func (t *Table) sortValue(i int) string {
	r := t.rows[i]
	if t.sortCol < len(r.Sort) {
		return r.Sort[t.sortCol]
	}
	if t.sortCol < len(r.Cells) {
		return r.Cells[t.sortCol]
	}
	return ""
}

func (t *Table) less(i, j int) bool {
	vi, vj := t.sortValue(i), t.sortValue(j)
	if t.sortDir == SortDesc {
		return vi > vj
	}
	return vi < vj
}

// sortOrder computes the order of all rows. Rows with equal values keep
// the order in which they were added.
func (t *Table) sortOrder() {
	t.order = make([]int, len(t.rows))
	for i := range t.order {
		t.order[i] = i
	}
	if t.sorted() {
		sort.SliceStable(t.order, func(a, b int) bool { return t.less(t.order[a], t.order[b]) })
	}
}

// mergeOrder merges two index lists that are each in sort order. On equal
// values the index from a comes first, which keeps the merge stable.
func (t *Table) mergeOrder(a, b []int) []int {
	out := make([]int, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if t.less(b[j], a[i]) {
			out = append(out, b[j])
			j++
		} else {
			out = append(out, a[i])
			i++
		}
	}
	out = append(out, a[i:]...)
	return append(out, b[j:]...)
}

// refresh applies the filter in one pass over the sort order, redraws the
// rows, and puts the cursor back on cursorKey when it is still visible.
// Otherwise the cursor keeps its position.
func (t *Table) refresh(cursorKey string) {
	pos := t.inner.Cursor()
	query := strings.ToLower(t.filterText)
	if query != "" {
		t.ensureSearch()
	}
	visible := make([]int, 0, len(t.order))
	for _, i := range t.order {
		if query == "" || strings.Contains(t.search[i], query) {
			visible = append(visible, i)
		}
	}
	t.visible = visible
	t.dropHiddenSelection()
	t.redraw()
	if len(t.visible) == 0 {
		return
	}
	if cursorKey != "" && t.SetCursorByKey(cursorKey) {
		return
	}
	t.inner.SetCursor(min(max(pos, 0), len(t.visible)-1))
}

// dropHiddenSelection deselects the rows that the filter hides, so that every
// selected row is visible and an action never applies to a row the user
// cannot see. A selected key whose row is missing stays selected, because a
// later page of a reload can bring the row back.
func (t *Table) dropHiddenSelection() {
	if len(t.selected) == 0 || len(t.visible) == len(t.rows) {
		return
	}
	shown := make([]bool, len(t.rows))
	for _, i := range t.visible {
		shown[i] = true
	}
	for k := range t.selected {
		if i, ok := t.index[k]; ok && !shown[i] {
			delete(t.selected, k)
		}
	}
}

// redraw rebuilds the displayed cells and the selection count from visible.
func (t *Table) redraw() {
	t.display = make([]table.Row, len(t.visible))
	t.selCount = 0
	for n, i := range t.visible {
		if t.selected[t.rows[i].Key] {
			t.selCount++
		}
		t.display[n] = t.displayRow(i)
	}
	t.inner.SetRows(t.display)
}

// displayRow returns the cells to show for a row, with the selection marker
// in front of the first cell when the row is selected.
func (t *Table) displayRow(i int) table.Row {
	r := t.rows[i]
	if !t.selected[r.Key] || len(r.Cells) == 0 {
		return r.Cells
	}
	marked := slices.Clone(r.Cells)
	marked[0] = selectMarker + marked[0]
	return marked
}

// SortKeyBytes returns a zero-padded string suitable for numeric byte sorting.
func SortKeyBytes(size int64) string {
	return fmt.Sprintf("%020d", size)
}
