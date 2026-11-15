package views

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/juthrbog/lazycloud/internal/ui"
)

// A clear removes the sort and a cancel keeps it, whatever Selected holds.
// Selected is 1 here, a real column, so a clear that sorted would show.
func TestApplySortPick(t *testing.T) {
	sorted := func() ui.Table {
		tb := ui.NewTable([]ui.Column{{Title: "A", Width: 4}, {Title: "B", Width: 4}})
		tb.Sort(0)
		return tb
	}
	col := func(tb ui.Table) int { _, c := tb.SortColumnNames(); return c }

	tb := sorted()
	assert.True(t, applySortPick(&tb, ui.PickerResultMsg{ID: "sort", Selected: 1, Value: "B"}))
	assert.Equal(t, 1, col(tb), "a pick sorts by the picked column")

	tb = sorted()
	assert.True(t, applySortPick(&tb, ui.PickerResultMsg{ID: "sort", Outcome: ui.PickCleared, Selected: 1}))
	assert.Equal(t, -1, col(tb), "a clear removes the sort")

	tb = sorted()
	assert.True(t, applySortPick(&tb, ui.PickerResultMsg{ID: "sort", Outcome: ui.PickCancelled, Selected: 1}))
	assert.Equal(t, 0, col(tb), "a cancel keeps the sort")

	tb = sorted()
	assert.False(t, applySortPick(&tb, ui.PickerResultMsg{ID: "action", Selected: 1}), "not a sort reply")
	assert.Equal(t, 0, col(tb))
}
