package views

import (
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// applySortPick applies the reply of a sort picker to t: a pick sorts by
// the picked column, and a clear removes the sort. It reports whether r is
// the reply of a sort picker.
func applySortPick(t *ui.Table, r msg.PickerResultMsg) bool {
	if r.ID != "sort" {
		return false
	}
	switch r.Outcome {
	case msg.Picked:
		t.Sort(r.Selected)
	case msg.PickCleared:
		t.ClearSort()
	case msg.PickCancelled:
	}
	return true
}
