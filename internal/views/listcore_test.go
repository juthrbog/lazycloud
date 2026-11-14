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

	c.reload(c.cfg.LoadingLabel)
	c.reload(c.cfg.LoadingLabel) // gen 2
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

	c.reload(c.cfg.LoadingLabel)
	c.handle(listPageMsg[fakeItem]{gen: c.loadGen, items: []fakeItem{fakeC, fakeA, fakeB}})
	assert.Equal(t, "b", cursorID(c), "the cursor follows the key")
}

func TestListCore_ReloadKeepsItemsAndTableUsable(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	c.handle(listPageMsg[fakeItem]{items: []fakeItem{fakeA, fakeB}})
	c.reload(c.cfg.LoadingLabel)
	assert.Len(t, c.items, 2)
	assert.NotContains(t, plain(c.render("")), "Loading", "rows stay on screen")
	assert.Contains(t, plain(c.footer()), "Loading fakes...")

	c.update(tea.KeyPressMsg{Code: tea.KeyDown})
	assert.Equal(t, "b", cursorID(c), "nav keys move the cursor during a reload")
}

func TestListCore_StaleFailureDroppedWithoutErrorMsg(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	c.reload(c.cfg.LoadingLabel)
	c.reload(c.cfg.LoadingLabel) // gen 2
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
	c.reload(c.cfg.LoadingLabel)
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
	assert.Equal(t, "\n  Error: denied", plain(c.render("")))
}

func TestListCore_SuccessClearsError(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	c.handle(msg.ErrorMsg{Err: errors.New("denied")})
	c.handle(listPageMsg[fakeItem]{items: []fakeItem{fakeA}})
	require.NoError(t, c.err)
	assert.NotContains(t, plain(c.render("")), "Error")
}

func TestListCore_TierRebuild(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	c.handle(listPageMsg[fakeItem]{items: []fakeItem{fakeA}})
	assert.Equal(t, ui.TierWide, c.widthTier)
	assert.Len(t, c.table.Columns(), 2)
	assert.Contains(t, plain(c.render("")), "alpha")

	c.handle(tea.WindowSizeMsg{Width: 60, Height: 20})
	assert.Equal(t, ui.TierNarrow, c.widthTier)
	assert.Len(t, c.table.Columns(), 1)
	assert.NotContains(t, plain(c.render("")), "alpha", "rows are rebuilt for the tier")

	c.handle(tea.WindowSizeMsg{Width: 160, Height: 20})
	assert.Len(t, c.table.Columns(), 2)
	assert.Contains(t, plain(c.render("")), "alpha")
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
	assert.Equal(t, listPageMsg[fakeItem]{gen: 1, items: []fakeItem{fakeA}, page: 1}, got)
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

// --- paging ---

// fakePages is a paged fetchFunc. pages maps a token ("" for the first
// page) to the items of that page; next maps a token to the next token.
func fakePages(pages map[string][]fakeItem, next map[string]string) fetchFunc[fakeItem] {
	return func(_ context.Context, tok *string) ([]fakeItem, *string, error) {
		k := ""
		if tok != nil {
			k = *tok
		}
		items, ok := pages[k]
		if !ok {
			return nil, nil, errors.New("unknown token " + k)
		}
		if n, more := next[k]; more {
			return items, &n, nil
		}
		return items, nil, nil
	}
}

func TestListCore_PagesAppendAndDedupe(t *testing.T) {
	c := newFakeCore(t, fakePages(
		map[string][]fakeItem{"": {fakeA, fakeB, fakeA}, "t2": {fakeB, fakeC}},
		map[string]string{"": "t2"},
	))
	page1 := loadMsg(t, c.init())
	require.Equal(t, 1, page1.(listPageMsg[fakeItem]).page)

	cmd, _ := c.handle(page1)
	assert.Equal(t, []fakeItem{fakeA, fakeB}, c.items, "a duplicate inside page 1 is dropped")
	assert.True(t, c.loading)
	page2 := loadMsg(t, cmd)
	require.Equal(t, 2, page2.(listPageMsg[fakeItem]).page)

	cmd, _ = c.handle(page2)
	assert.Nil(t, cmd, "the last page ends the load")
	assert.Equal(t, []fakeItem{fakeA, fakeB, fakeC}, c.items, "b is not appended twice")
	_, total := c.table.RowCount()
	assert.Len(t, c.items, total, "the items stay equal to the table rows")
	assert.False(t, c.loading)
	assert.Equal(t, 2, c.pages)
}

func TestListCore_ProgressLabelWhileMorePagesLoad(t *testing.T) {
	c := newFakeCore(t, fakePages(
		map[string][]fakeItem{"": {fakeA}, "t2": {fakeB}, "t3": {fakeC}},
		map[string]string{"": "t2", "t2": "t3"},
	))
	cmd := c.init()
	assert.Equal(t, "0/0 fakes  ⣾  Loading fakes...", plain(c.footer()), "no label before the first page")

	cmd, _ = c.handle(loadMsg(t, cmd))
	assert.Equal(t, "1/1 fakes  (loading... 1 so far)  ⣾  Loading fakes... 1 so far", plain(c.footer()))

	cmd, _ = c.handle(loadMsg(t, cmd))
	assert.Equal(t, "2/2 fakes  (loading... 2 so far)  ⣾  Loading fakes... 2 so far", plain(c.footer()), "the label counts on page 2")

	c.handle(loadMsg(t, cmd))
	assert.Equal(t, "3/3 fakes", plain(c.footer()), "no label after the last page")
}

func TestListCore_PagedReloadReplacesAndKeepsCursor(t *testing.T) {
	c := newFakeCore(t, fakePages(
		map[string][]fakeItem{"": {fakeC, fakeB}, "t2": {fakeA}},
		map[string]string{"": "t2"},
	))
	c.handle(listPageMsg[fakeItem]{items: []fakeItem{fakeA, fakeB}})
	c.update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, "b", cursorID(c))

	cmd := c.reload(c.cfg.LoadingLabel)
	assert.Equal(t, 0, c.pages, "a new load counts its pages from zero")
	assert.NotContains(t, plain(c.footer()), "so far", "old rows are not progress")

	cmd, _ = c.handle(loadMsg(t, cmd))
	assert.Equal(t, []fakeItem{fakeC, fakeB}, c.items, "page 1 replaces the rows of the last load")
	assert.Equal(t, "b", cursorID(c), "the cursor follows the key")

	c.handle(loadMsg(t, cmd))
	assert.Equal(t, []fakeItem{fakeC, fakeB, fakeA}, c.items)
	assert.Equal(t, "b", cursorID(c))
}

func TestListCore_StalePageOfOldLoadDropped(t *testing.T) {
	c := newFakeCore(t, fakePages(
		map[string][]fakeItem{"": {fakeA}, "t2": {fakeB}},
		map[string]string{"": "t2"},
	))
	oldNext, _ := c.handle(loadMsg(t, c.init())) // gen 1, page 1 applied
	oldPage2 := loadMsg(t, oldNext)

	cmd := c.reload(c.cfg.LoadingLabel) // gen 2
	cmd, _ = c.handle(loadMsg(t, cmd))
	require.Equal(t, []fakeItem{fakeA}, c.items)

	got, ok := c.handle(oldPage2)
	assert.True(t, ok)
	assert.Nil(t, got, "page 2 of the old load starts no fetch")
	assert.Equal(t, []fakeItem{fakeA}, c.items, "page 2 of the old load is dropped")
	assert.True(t, c.loading)

	c.handle(loadMsg(t, cmd))
	assert.Equal(t, []fakeItem{fakeA, fakeB}, c.items)
	assert.False(t, c.loading)
}

func TestListCore_FailureOnLaterPage(t *testing.T) {
	c := newFakeCore(t, fakePages(
		map[string][]fakeItem{"": {fakeA}}, // t2 is unknown: the fetch fails
		map[string]string{"": "t2"},
	))
	cmd, _ := c.handle(loadMsg(t, c.init()))
	failed := loadMsg(t, cmd)
	fm, ok := failed.(listFailedMsg)
	require.True(t, ok, "%T", failed)
	assert.Equal(t, "listing fakes", fm.context)

	c.handle(failed)
	require.Error(t, c.err)
	assert.False(t, c.loading)
	assert.NotContains(t, plain(c.footer()), "so far")
}

func TestListCore_LiteralPageZeroWithNextFetchesPageTwo(t *testing.T) {
	var gotTok *string
	c := newFakeCore(t, func(_ context.Context, tok *string) ([]fakeItem, *string, error) {
		gotTok = tok
		return []fakeItem{fakeB}, nil, nil
	})
	tok := "t2"
	cmd, _ := c.handle(listPageMsg[fakeItem]{items: []fakeItem{fakeA}, next: &tok})
	next := loadMsg(t, cmd)
	assert.Equal(t, &tok, gotTok, "the next token goes to the fetch")
	assert.Equal(t, 2, next.(listPageMsg[fakeItem]).page, "page 0 counts as page 1")
	c.handle(next)
	assert.Equal(t, []fakeItem{fakeA, fakeB}, c.items)
}

func TestDedupe(t *testing.T) {
	key := func(it fakeItem) string { return it.ID }
	assert.Empty(t, dedupe(nil, nil, key))
	assert.Equal(t, []fakeItem{fakeA, fakeB}, dedupe(nil, []fakeItem{fakeA, fakeB, fakeA}, key))
	assert.Equal(t, []fakeItem{fakeC}, dedupe([]fakeItem{fakeA, fakeB}, []fakeItem{fakeB, fakeC, fakeA}, key))

	fresh := []fakeItem{fakeA}
	out := dedupe(nil, fresh, key)
	out[0].Name = "changed"
	assert.Equal(t, "alpha", fresh[0].Name, "dedupe returns a new slice")
}

func TestNextToken(t *testing.T) {
	tok, empty := "t2", ""
	assert.Equal(t, &tok, nextToken(true, &tok))
	assert.Nil(t, nextToken(false, &tok), "no more pages")
	assert.Nil(t, nextToken(true, nil), "more pages but no token would load page 1 again")
	assert.Nil(t, nextToken(true, &empty), "an empty token too")
}

type fakePagedService interface {
	ListPage(ctx context.Context, token *string) (*aws.VPCPage, error)
}

type fakePagedImpl struct {
	err  error
	seen *string
}

func (f *fakePagedImpl) ListPage(_ context.Context, token *string) (*aws.VPCPage, error) {
	f.seen = token
	if f.err != nil {
		return nil, f.err
	}
	next := "t3"
	return &aws.VPCPage{VPCs: []aws.VPC{{ID: "vpc-1"}}, HasMorePages: true, Token: &next}, nil
}

func TestFetchPages_NilServiceGivesErrNoClient(t *testing.T) {
	split := func(*aws.VPCPage) ([]aws.VPC, *string) {
		t.Error("split must not run without a service")
		return nil, nil
	}
	items, next, err := fetchPages(aws.EC2Service(nil), aws.EC2Service.ListVPCsPage, split)(t.Context(), nil)
	require.ErrorIs(t, err, errNoClient)
	assert.Nil(t, items)
	assert.Nil(t, next)

	_, _, err = fetchPages(fakePagedService(nil), fakePagedService.ListPage, split)(t.Context(), nil)
	require.ErrorIs(t, err, errNoClient)
}

func TestFetchPages_CallsTheServiceWithTheToken(t *testing.T) {
	split := func(p *aws.VPCPage) ([]aws.VPC, *string) { return p.VPCs, nextToken(p.HasMorePages, p.Token) }
	svc := &fakePagedImpl{}
	tok := "t2"
	items, next, err := fetchPages[fakePagedService](svc, fakePagedService.ListPage, split)(t.Context(), &tok)
	require.NoError(t, err)
	assert.Equal(t, &tok, svc.seen)
	assert.Equal(t, []aws.VPC{{ID: "vpc-1"}}, items)
	require.NotNil(t, next)
	assert.Equal(t, "t3", *next)

	boom := errors.New("boom")
	items, next, err = fetchPages[fakePagedService](&fakePagedImpl{err: boom}, fakePagedService.ListPage, split)(t.Context(), nil)
	require.ErrorIs(t, err, boom)
	assert.Nil(t, items)
	assert.Nil(t, next)
}
