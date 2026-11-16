package views

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// sortTag is the tag of a sort picker request. It holds the column titles
// when the picker opened, so a pick names a column by its title, also when
// the columns change while the picker is open.
type sortTag struct {
	columns []string
}

// PromptTag marks sortTag as a prompt tag.
func (*sortTag) PromptTag() {}

// askSort returns a command that asks for the sort picker of t.
func askSort(t *ui.Table) tea.Cmd {
	columns, current := t.SortColumnNames()
	req := msg.RequestSortPickerMsg{
		Columns:    columns,
		CurrentCol: current,
		Tag:        &sortTag{columns: slices.Clone(columns)},
	}
	return func() tea.Msg { return req }
}

// applySortPick applies the reply of a sort picker to t. A pick sorts by
// the picked column, found by its title. A pick of a column that is gone
// keeps the current sort. A clear removes the sort, and a cancel keeps it.
// It reports whether r is the reply of a sort picker.
func applySortPick(t *ui.Table, r msg.PickerResultMsg) bool {
	tag, ok := r.Tag.(*sortTag)
	if !ok || tag == nil {
		return false
	}
	switch r.Outcome {
	case msg.Picked:
		if r.Selected >= 0 && r.Selected < len(tag.columns) {
			t.SortByTitle(tag.columns[r.Selected], r.Selected)
		}
	case msg.PickCleared:
		t.ClearSort()
	case msg.PickCancelled:
	}
	return true
}
