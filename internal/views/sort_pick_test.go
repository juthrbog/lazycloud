package views

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// sortedTable returns a table with the columns A and B, sorted by A
// ascending. Its rows k1, k2 and k3 have a different order for each sort:
// k1 k2 k3 by A ascending, k3 k2 k1 by A descending, and k3 k1 k2 by B.
func sortedTable() ui.Table {
	tb := ui.NewTable([]ui.Column{{Title: "A", Width: 4}, {Title: "B", Width: 4}})
	tb.SetRows([]ui.Row{
		{Key: "k1", Cells: []string{"1", "2"}},
		{Key: "k2", Cells: []string{"2", "3"}},
		{Key: "k3", Cells: []string{"3", "1"}},
	})
	tb.Sort(0)
	return tb
}

// unchanged reports whether tb still sorts by A ascending.
func unchanged(tb ui.Table) bool {
	return sortCol(tb) == 0 && slices.Equal(tb.VisibleKeys(), []string{"k1", "k2", "k3"})
}

// sortCol returns the index of the sorted column of tb, or -1.
func sortCol(tb ui.Table) int { _, c := tb.SortColumnNames(); return c }

// askSortOf runs askSort on tb and returns the request.
func askSortOf(t *testing.T, tb *ui.Table) msg.RequestSortPickerMsg {
	t.Helper()
	req, ok := askSort(tb)().(msg.RequestSortPickerMsg)
	require.True(t, ok)
	return req
}

// askSort sends the column titles, the sorted column and a sortTag that
// holds a copy of the titles.
func TestAskSort(t *testing.T) {
	tb := sortedTable()
	req := askSortOf(t, &tb)
	assert.Equal(t, []string{"A", "B"}, req.Columns)
	assert.Equal(t, 0, req.CurrentCol)
	tag, ok := req.Tag.(*sortTag)
	require.True(t, ok)
	assert.Equal(t, []string{"A", "B"}, tag.columns)
	req.Columns[0] = "changed"
	assert.Equal(t, "A", tag.columns[0], "the tag keeps its own copy")
}

// A clear removes the sort and a cancel keeps it, whatever Selected holds.
// Selected is 1 here, a real column, so a clear that sorted would show.
func TestApplySortPick(t *testing.T) {
	tb := sortedTable()
	req := askSortOf(t, &tb)

	tb = sortedTable()
	assert.True(t, applySortPick(&tb, pickColumn(t, req, "B")))
	assert.Equal(t, 1, sortCol(tb), "a pick sorts by the picked column")

	tb = sortedTable()
	cleared := clearSort(req)
	cleared.Selected = 1
	assert.True(t, applySortPick(&tb, cleared))
	assert.Equal(t, -1, sortCol(tb), "a clear removes the sort")

	tb = sortedTable()
	cancelled := cancelPick(req)
	cancelled.Selected = 1
	assert.True(t, applySortPick(&tb, cancelled))
	assert.True(t, unchanged(tb), "a cancel keeps the sort")
}

// A reply without a sortTag is not a sort reply: another tag, a nil tag and
// a typed-nil tag (I4). A reply without the tag of the prompt runs nothing.
func TestApplySortPickIgnoresOtherTags(t *testing.T) {
	for name, r := range map[string]msg.PickerResultMsg{
		"untagged":  {Selected: 1},
		"other tag": {Selected: 1, Tag: &helperTag{}},
		"typed nil": {Selected: 1, Tag: (*sortTag)(nil)},
	} {
		tb := sortedTable()
		assert.NotPanics(t, func() { assert.False(t, applySortPick(&tb, r), name) })
		assert.True(t, unchanged(tb), name)
	}
}

// A pick with an index outside the tag names no column and keeps the sort.
func TestApplySortPickOutOfRange(t *testing.T) {
	tb := sortedTable()
	req := askSortOf(t, &tb)
	for _, i := range []int{-1, 2} {
		r := pickColumn(t, req, "B")
		r.Selected = i
		assert.True(t, applySortPick(&tb, r))
		assert.True(t, unchanged(tb), "index %d", i)
	}
}

// Each view with a sort picker sends a tagged request and applies the pick.
// A reply without the sort tag changes nothing and does not panic (I4, R4).
func TestSortPickerIsTaggedInEachView(t *testing.T) {
	ec2, _ := newTestEC2List()
	s3o, _ := newTestS3Objects()
	sqsm, _ := newTestSQSMessages()
	for name, v := range map[string]tea.Model{
		"list core":    ec2,
		"S3 objects":   s3o,
		"S3 versions":  newTestS3Versions(),
		"SQS messages": sqsm,
	} {
		req := sortRequest(t, v)
		require.IsType(t, &sortTag{}, req.Tag, name)
		v.Update(pickColumn(t, req, req.Columns[1]))
		cols, sorted := sortedColumns(v)
		require.GreaterOrEqual(t, sorted, 0, name)
		assert.Equal(t, req.Columns[1], cols[sorted], name)

		for _, r := range []msg.PickerResultMsg{
			{Selected: 0},
			{Selected: 0, Tag: &helperTag{}},
			{Selected: 0, Tag: (*sortTag)(nil)},
		} {
			assert.NotPanics(t, func() { v.Update(r) }, name)
			_, now := sortedColumns(v)
			assert.Equal(t, sorted, now, "%s: %T is ignored", name, r.Tag)
		}
	}
}

// sortedColumns returns the sort columns and the sorted column of v.
func sortedColumns(v tea.Model) ([]string, int) {
	switch v := v.(type) {
	case *EC2List:
		return v.table.SortColumnNames()
	case *S3Objects:
		return v.table.SortColumnNames()
	case *S3Versions:
		return v.table.SortColumnNames()
	case *SQSMessages:
		return v.table.SortColumnNames()
	}
	panic("no table")
}

// sortAsked checks that each sort request in msgs carries a sortTag with
// the columns of the request. It returns msgs with those tags removed, so a
// test can compare the rest of each request.
func sortAsked(t *testing.T, msgs ...tea.Msg) []tea.Msg {
	t.Helper()
	out := make([]tea.Msg, len(msgs))
	for i, m := range msgs {
		if req, ok := m.(msg.RequestSortPickerMsg); ok {
			tag, ok := req.Tag.(*sortTag)
			require.True(t, ok, "the sort request carries a sortTag")
			assert.Equal(t, req.Columns, tag.columns)
			req.Tag = nil
			m = req
		}
		out[i] = m
	}
	return out
}
