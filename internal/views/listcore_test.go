package views

import (
	"context"
	"errors"
	"testing"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// Unit tests of the shared list core, with a fake item type.

type fakeItem struct{ ID, Name string }

func fakeColumns(tier ui.WidthTier) []ui.Column {
	if tier == ui.TierNarrow {
		return []ui.Column{{Title: "ID", Width: 10}}
	}
	return []ui.Column{{Title: "ID", Width: 10}, {Title: "Name", Width: 10}}
}

func fakeRow(it fakeItem, tier ui.WidthTier) ui.Row {
	if tier == ui.TierNarrow {
		return ui.Row{Key: it.ID, Cells: table.Row{it.ID}}
	}
	return ui.Row{Key: it.ID, Cells: table.Row{it.ID, it.Name}}
}

func fakeConfig(fetch fetchFunc[fakeItem]) listConfig[fakeItem] {
	return listConfig[fakeItem]{
		Noun:         "fakes",
		LoadingLabel: "Loading fakes...",
		ErrContext:   "listing fakes",
		Key:          func(it fakeItem) string { return it.ID },
		Columns:      fakeColumns,
		Row:          fakeRow,
		Fetch:        fetch,
		OpenDetail: func(it fakeItem) tea.Cmd {
			return func() tea.Msg { return msg.TabbedContentMsg{PanelTitle: it.ID} }
		},
		Copy: true,
	}
}

// fetchReturns is a fetchFunc that returns items and err.
func fetchReturns(err error, items ...fakeItem) fetchFunc[fakeItem] {
	return func(context.Context, *string) ([]fakeItem, *string, error) {
		return items, nil, err
	}
}

func newFakeCore(t *testing.T, fetch fetchFunc[fakeItem]) *listCore[fakeItem] {
	t.Helper()
	c := newListCore(fakeConfig(fetch))
	c.handle(tea.WindowSizeMsg{Width: 160, Height: 20})
	return &c
}

// loadMsg runs a load cmd (a batch of a tick and the fetch) and returns
// the message of the fetch.
func loadMsg(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	msgs := netNoTicks(collectMsgs(cmd))
	require.Len(t, msgs, 1)
	return msgs[0]
}

func cursorID(c *listCore[fakeItem]) string { return c.table.CursorKey() }

var fakeA, fakeB, fakeC = fakeItem{"a", "alpha"}, fakeItem{"b", "beta"}, fakeItem{"c", "gamma"}

func TestListCore_NewStartsLoadingAtGenZero(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	assert.True(t, c.loading)
	assert.Equal(t, 0, c.loadGen)
	assert.True(t, c.spinner.Visible())

	c.handle(listPageMsg[fakeItem]{items: []fakeItem{fakeA}})
	assert.False(t, c.loading, "gen 0 is accepted on a fresh core")
	assert.Len(t, c.items, 1)
}

func TestListCore_InitLoadsOnlyWhileLoading(t *testing.T) {
	calls := 0
	c := newFakeCore(t, func(context.Context, *string) ([]fakeItem, *string, error) {
		calls++
		return []fakeItem{fakeA}, nil, nil
	})
	c.handle(loadMsg(t, c.init()))
	assert.Equal(t, 1, calls)
	assert.Equal(t, 1, c.loadGen)
	assert.Nil(t, c.init(), "no reload when shown again")
}

func TestListCore_StalePageDropped(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil, fakeA))
	c.handle(listPageMsg[fakeItem]{items: []fakeItem{fakeA}})

	c.reload()
	c.reload() // gen 2
	cmd, ok := c.handle(listPageMsg[fakeItem]{gen: 1, items: []fakeItem{fakeB}})
	assert.True(t, ok)
	assert.Nil(t, cmd)
	assert.Equal(t, []fakeItem{fakeA}, c.items, "the old page is dropped")
	assert.True(t, c.loading, "the current load still runs")

	c.handle(listPageMsg[fakeItem]{gen: 2, items: []fakeItem{fakeC}})
	assert.Equal(t, []fakeItem{fakeC}, c.items)
	assert.False(t, c.loading)
	assert.False(t, c.spinner.Visible())
}

func TestListCore_PageReplaceKeepsCursor(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	c.handle(listPageMsg[fakeItem]{items: []fakeItem{fakeA, fakeB}})
	c.update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, "b", cursorID(c))

	c.reload()
	c.handle(listPageMsg[fakeItem]{gen: c.loadGen, items: []fakeItem{fakeC, fakeA, fakeB}})
	assert.Equal(t, "b", cursorID(c), "the cursor follows the key")
}

func TestListCore_ReloadKeepsItemsAndTableUsable(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	c.handle(listPageMsg[fakeItem]{items: []fakeItem{fakeA, fakeB}})
	c.reload()
	assert.Len(t, c.items, 2)
	assert.NotContains(t, plain(c.render()), "Loading", "rows stay on screen")
	assert.Contains(t, plain(c.footer()), "Loading fakes...")

	c.update(tea.KeyPressMsg{Code: tea.KeyDown})
	assert.Equal(t, "b", cursorID(c), "nav keys move the cursor during a reload")
}

func TestListCore_StaleFailureDroppedWithoutErrorMsg(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	c.reload()
	c.reload() // gen 2
	cmd, ok := c.handle(listFailedMsg{gen: 1, err: errors.New("old"), context: "listing fakes"})
	assert.True(t, ok)
	assert.Nil(t, cmd, "no ErrorMsg reaches the status bar")
	require.NoError(t, c.err)
	assert.True(t, c.loading)
	assert.True(t, c.spinner.Visible())
}

func TestListCore_CurrentFailureEmitsOneErrorMsg(t *testing.T) {
	boom := errors.New("boom")
	c := newFakeCore(t, fetchReturns(boom))
	failed := loadMsg(t, c.init())
	fm, ok := failed.(listFailedMsg)
	require.True(t, ok, "%T", failed)
	assert.Equal(t, "listing fakes", fm.context)

	cmd, _ := c.handle(failed)
	require.ErrorIs(t, c.err, boom)
	assert.False(t, c.loading)
	assert.False(t, c.spinner.Visible())

	msgs := collectMsgs(cmd)
	require.Len(t, msgs, 1)
	em, ok := msgs[0].(msg.ErrorMsg)
	require.True(t, ok)
	assert.Equal(t, "listing fakes", em.Context)
	require.ErrorIs(t, em.Err, boom, "the app and aws.ErrorText see the AWS error")
	assert.Equal(t, "boom", em.Err.Error())
	assert.Equal(t, "boom", aws.ErrorText(em.Err))

	canceled := msg.ErrorMsg{Err: &loadError{err: context.Canceled}}
	assert.ErrorIs(t, canceled.Err, context.Canceled, "the app's Canceled check still works")
}

func TestListCore_EchoNotAppliedTwice(t *testing.T) {
	c := newFakeCore(t, fetchReturns(errors.New("boom")))
	cmd, _ := c.handle(loadMsg(t, c.init()))
	echo := collectMsgs(cmd)[0]

	// A reload starts before the echo comes back. The echo must not
	// overwrite it.
	c.reload()
	cmd, ok := c.handle(echo)
	assert.True(t, ok)
	assert.Nil(t, cmd)
	require.NoError(t, c.err)
	assert.True(t, c.loading)
	assert.True(t, c.spinner.Visible())
}

func TestListCore_BareErrorMsgApplied(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	c.handle(msg.ErrorMsg{Err: errors.New("denied"), Context: "x"})
	require.EqualError(t, c.err, "denied")
	assert.False(t, c.loading)
	assert.False(t, c.spinner.Visible())
	assert.Equal(t, "\n  Error: denied", plain(c.render()))
}

func TestListCore_SuccessClearsError(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	c.handle(msg.ErrorMsg{Err: errors.New("denied")})
	c.handle(listPageMsg[fakeItem]{items: []fakeItem{fakeA}})
	require.NoError(t, c.err)
	assert.NotContains(t, plain(c.render()), "Error")
}

func TestListCore_TierRebuild(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	c.handle(listPageMsg[fakeItem]{items: []fakeItem{fakeA}})
	assert.Equal(t, ui.TierWide, c.widthTier)
	assert.Len(t, c.table.Columns(), 2)
	assert.Contains(t, plain(c.render()), "alpha")

	c.handle(tea.WindowSizeMsg{Width: 60, Height: 20})
	assert.Equal(t, ui.TierNarrow, c.widthTier)
	assert.Len(t, c.table.Columns(), 1)
	assert.NotContains(t, plain(c.render()), "alpha", "rows are rebuilt for the tier")

	c.handle(tea.WindowSizeMsg{Width: 160, Height: 20})
	assert.Len(t, c.table.Columns(), 2)
	assert.Contains(t, plain(c.render()), "alpha")
}

func TestListCore_SpinnerTicksWhileVisible(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	c.handle(listPageMsg[fakeItem]{items: []fakeItem{fakeA}})
	require.False(t, c.loading)

	tick := c.spinner.Tick()()
	assert.Nil(t, c.update(tick), "a hidden spinner does not tick")

	c.spinner.Show("Working...") // visible while not loading
	assert.NotNil(t, c.update(c.spinner.Tick()()), "a visible spinner keeps ticking")
}

func TestListCore_HandleIgnoresOtherPicker(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	_, ok := c.handle(ui.PickerResultMsg{ID: "action", Selected: 0})
	assert.False(t, ok)
	_, ok = c.handle(ui.PickerResultMsg{ID: "sort", Selected: 0})
	assert.True(t, ok)
}

func TestListCore_SourcedFilterIgnored(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	c.handle(listPageMsg[fakeItem]{items: []fakeItem{fakeA, fakeB}})
	c.handle(ui.FilterChangedMsg{Text: "alpha", Source: "side"})
	shown, _ := c.table.RowCount()
	assert.Equal(t, 2, shown)
	c.handle(ui.FilterChangedMsg{Text: "alpha"})
	shown, _ = c.table.RowCount()
	assert.Equal(t, 1, shown)
}

func TestListCore_FocusAndPendingFocus(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	cmd, _ := c.handle(msg.FocusResourceMsg{ResourceID: "b"})
	assert.Nil(t, cmd, "stored while loading")

	cmd, _ = c.handle(listPageMsg[fakeItem]{items: []fakeItem{fakeA, fakeB}})
	assert.Equal(t, []tea.Msg{msg.TabbedContentMsg{PanelTitle: "b"}}, collectMsgs(cmd))
	assert.Equal(t, "b", cursorID(c))
	assert.Empty(t, c.pendingFocus)

	cmd, _ = c.handle(msg.FocusResourceMsg{ResourceID: "zzz"})
	assert.Nil(t, cmd, "an unknown ID does nothing")
	assert.Equal(t, "b", cursorID(c))
}

func TestListCore_FindAndCursorItem(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	assert.Nil(t, c.cursorItem())
	c.setItems([]fakeItem{fakeA, fakeB})
	require.NotNil(t, c.find("b"))
	assert.Equal(t, "beta", c.find("b").Name)
	assert.Nil(t, c.find("x"))
	require.NotNil(t, c.cursorItem())
	assert.Equal(t, "a", c.cursorItem().ID)
}

func TestListCore_CopyAndDetailFlags(t *testing.T) {
	cfg := fakeConfig(fetchReturns(nil))
	cfg.Copy, cfg.OpenDetail = false, nil
	c := newListCore(cfg)
	c.setItems([]fakeItem{fakeA})
	_, ok := c.handle(keyPress('y'))
	assert.False(t, ok, "y is not the core's without Copy")
	_, ok = c.handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.False(t, ok, "enter is not the core's without OpenDetail")
}

func TestListCore_FetchUsesTimeoutAndContext(t *testing.T) {
	type ctxKey struct{}
	cfg := fakeConfig(func(ctx context.Context, tok *string) ([]fakeItem, *string, error) {
		_, hasDeadline := ctx.Deadline()
		if ctx.Value(ctxKey{}) != "nav" || !hasDeadline || tok != nil {
			return nil, nil, errors.New("bad call")
		}
		return []fakeItem{fakeA}, nil, nil
	})
	cfg.FetchTimeout = listTimeout
	c := newListCore(cfg)
	c.SetContext(context.WithValue(t.Context(), ctxKey{}, "nav"))
	got := loadMsg(t, c.init())
	assert.Equal(t, listPageMsg[fakeItem]{gen: 1, items: []fakeItem{fakeA}}, got)
}

func TestListCore_ConstructorPanicsOnMissingField(t *testing.T) {
	cases := map[string]func(*listConfig[fakeItem]){
		"Noun":         func(c *listConfig[fakeItem]) { c.Noun = "" },
		"LoadingLabel": func(c *listConfig[fakeItem]) { c.LoadingLabel = "" },
		"ErrContext":   func(c *listConfig[fakeItem]) { c.ErrContext = "" },
		"Key":          func(c *listConfig[fakeItem]) { c.Key = nil },
		"Columns":      func(c *listConfig[fakeItem]) { c.Columns = nil },
		"Row":          func(c *listConfig[fakeItem]) { c.Row = nil },
		"Fetch":        func(c *listConfig[fakeItem]) { c.Fetch = nil },
	}
	for name, unset := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := fakeConfig(fetchReturns(nil))
			unset(&cfg)
			assert.PanicsWithValue(t, "views: listConfig."+name+" is required", func() { newListCore(cfg) })
		})
	}
	assert.NotPanics(t, func() { newListCore(fakeConfig(fetchReturns(nil))) }, "FetchTimeout and the optional fields may be zero")
}

type fakeService interface {
	List(ctx context.Context) ([]fakeItem, error)
}

func TestFetchAll_NilServiceGivesErrNoClient(t *testing.T) {
	items, next, err := fetchAll(aws.EC2Service(nil), aws.EC2Service.ListSecurityGroups)(t.Context(), nil)
	require.ErrorIs(t, err, errNoClient)
	assert.Nil(t, items)
	assert.Nil(t, next)

	_, _, err = fetchAll(fakeService(nil), fakeService.List)(t.Context(), nil)
	require.ErrorIs(t, err, errNoClient)
}

type fakeServiceImpl struct{ err error }

func (f fakeServiceImpl) List(context.Context) ([]fakeItem, error) {
	return []fakeItem{fakeA}, f.err
}

func TestFetchAll_CallsTheService(t *testing.T) {
	items, next, err := fetchAll[fakeService](fakeServiceImpl{}, fakeService.List)(t.Context(), nil)
	require.NoError(t, err)
	assert.Nil(t, next)
	assert.Equal(t, []fakeItem{fakeA}, items)

	boom := errors.New("boom")
	_, _, err = fetchAll[fakeService](fakeServiceImpl{err: boom}, fakeService.List)(t.Context(), nil)
	require.ErrorIs(t, err, boom)
}
