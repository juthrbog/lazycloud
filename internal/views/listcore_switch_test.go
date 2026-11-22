package views

import (
	"context"
	"testing"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/ui"
)

// switchList changes the fetch, the columns and the row function, and
// loads again. The rows, the selection and the filter of the old kind go,
// and r then uses the new fetch (the ECS stopped toggle, H12).
func TestListCore_SwitchList(t *testing.T) {
	c := newListCore(fakeConfig(fetchReturns(nil, fakeA, fakeB)))
	c.cfg.MultiSelect = true
	c.handle(tea.WindowSizeMsg{Width: 160, Height: 20})
	c.handle(listPageMsg[fakeItem]{items: []fakeItem{fakeA, fakeB}})
	c.table.ToggleSelect()
	c.filter.Activate()
	c.table.Filter("alpha")
	require.Equal(t, 1, c.table.SelectionCount())

	calls := 0
	other := func(context.Context, *string) ([]fakeItem, *string, error) {
		calls++
		return []fakeItem{fakeC, fakeA}, nil, nil
	}
	threeCols := func(ui.WidthTier) []ui.Column {
		return []ui.Column{{Title: "ID", Width: 8}, {Title: "Name", Width: 8}, {Title: "Extra", Width: 8}}
	}
	threeRow := func(it fakeItem, _ ui.WidthTier) ui.Row {
		return ui.Row{Key: it.ID, Cells: table.Row{it.ID, it.Name, "x"}}
	}
	cmd := c.switchList(other, threeCols, threeRow, "Loading others...")
	assert.Empty(t, c.items, "no rows of the old kind stay")
	assert.Equal(t, 0, c.table.SelectionCount())
	assert.False(t, c.filter.Active())
	_, total := c.table.RowCount()
	assert.Equal(t, 0, total)
	assert.Len(t, c.table.Columns(), 3, "the new columns, for the width")
	assert.True(t, c.loading)

	c.handle(loadMsg(t, cmd))
	assert.Equal(t, 1, calls)
	assert.Equal(t, []fakeItem{fakeC, fakeA}, c.items)
	shown, _ := c.table.RowCount()
	assert.Equal(t, 2, shown, "the old filter text is gone")
	assert.Equal(t, 0, c.table.SelectionCount(), "a row of the same key is not selected again")

	c.handle(loadMsg(t, c.reload(c.cfg.LoadingLabel)))
	assert.Equal(t, 2, calls, "r uses the new fetch")

	// A resize keeps the new columns.
	c.handle(tea.WindowSizeMsg{Width: 100, Height: 20})
	require.Len(t, c.table.Columns(), 3)
	assert.Equal(t, "Extra", c.table.Columns()[2].Title)

	// Before the first resize, the columns are the medium set.
	d := newListCore(fakeConfig(fetchReturns(nil)))
	d.switchList(other, fakeColumns, fakeRow, "x")
	assert.Equal(t, ui.TierMedium, d.widthTier)
	assert.Len(t, d.table.Columns(), 2)
	// After a resize to a narrow width, BestFitTier picks the set.
	d.handle(tea.WindowSizeMsg{Width: 30, Height: 20})
	d.switchList(other, fakeColumns, fakeRow, "x")
	assert.Equal(t, ui.TierNarrow, d.widthTier)
	assert.Len(t, d.table.Columns(), 1)
}
