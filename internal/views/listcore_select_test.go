package views

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
)

// Unit tests of the core parts that PR 6 adds: other loads (loadWith),
// OnLoaded, selection, selective refresh and focus on a missing ID.

// refreshCall records the calls of a fake refresh.
type refreshCall struct {
	items []fakeItem
	err   error
	got   [][]string
}

func (r *refreshCall) fn(_ context.Context, keys []string) ([]fakeItem, error) {
	r.got = append(r.got, keys)
	return r.items, r.err
}

// newSelectCore returns a loaded MultiSelect core with fakeA and fakeB and
// a fake refresh.
func newSelectCore(t *testing.T, r *refreshCall) *listCore[fakeItem] {
	t.Helper()
	cfg := fakeConfig(fetchReturns(nil, fakeA, fakeB))
	cfg.MultiSelect = true
	cfg.CopyNoun = "fake ID"
	cfg.Refresh = r.fn
	c := newListCore(cfg)
	c.handle(tea.WindowSizeMsg{Width: 160, Height: 20})
	c.handle(listPageMsg[fakeItem]{items: []fakeItem{fakeA, fakeB}})
	require.False(t, c.loading)
	return &c
}

// selectBoth selects fakeA and fakeB. It uses update, because the core
// hands the down key to the table.
func selectBoth(c *listCore[fakeItem]) {
	c.update(keyPress(' '))
	c.update(tea.KeyPressMsg{Code: tea.KeyDown})
	c.update(keyPress(' '))
}

// --- loadWith, reload and OnLoaded ---

func TestListCore_LoadWithTagsOnLoadedAndReloadGoesBack(t *testing.T) {
	var tags []string
	cfg := fakeConfig(fetchReturns(nil, fakeA))
	cfg.OnLoaded = func(items []fakeItem, tag string) tea.Cmd {
		tags = append(tags, fmt.Sprintf("%s:%d", tag, len(items)))
		return nil
	}
	c := newListCore(cfg)
	c.handle(tea.WindowSizeMsg{Width: 160, Height: 20})
	c.handle(loadMsg(t, c.init()))
	require.Equal(t, []string{":1"}, tags, "the default load has the tag \"\"")

	cmd := c.loadWith(loadSpec[fakeItem]{
		fetch:      fetchReturns(nil, fakeB, fakeC),
		errContext: "searching fakes",
		label:      "Searching fakes...",
		tag:        "q",
	})
	assert.True(t, c.loading)
	assert.Equal(t, "1/1 fakes  ⣾  Searching fakes...", plain(c.footer()))
	c.handle(loadMsg(t, cmd))
	assert.Equal(t, []string{":1", "q:2"}, tags)

	// reload goes back to the default load, with its own context.
	c.cfg.Fetch = fetchReturns(errors.New("boom"))
	cmd = c.reload()
	assert.Equal(t, "2/2 fakes  ⣾  Loading fakes...", plain(c.footer()))
	failed := loadMsg(t, cmd)
	require.IsType(t, listFailedMsg{}, failed)
	assert.Equal(t, "listing fakes", failed.(listFailedMsg).context)
}

func TestListCore_LoadWithFailureHasItsContext(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	cmd := c.loadWith(loadSpec[fakeItem]{fetch: fetchReturns(errors.New("x")), errContext: "searching fakes", label: "s"})
	failed := loadMsg(t, cmd)
	cmd, _ = c.handle(failed)
	em, ok := firstOfType[msg.ErrorMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "searching fakes", em.Context)
}

// A load started with loadWith supersedes the load before it, and the
// other way round.
func TestListCore_LastStartedLoadWins(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil, fakeA))
	c.handle(loadMsg(t, c.init()))

	first := loadMsg(t, c.reload())
	second := loadMsg(t, c.loadWith(loadSpec[fakeItem]{fetch: fetchReturns(nil, fakeC), label: "s", tag: "q"}))
	c.handle(second)
	c.handle(first)
	assert.Equal(t, []string{"c"}, c.table.VisibleKeys())
	assert.Equal(t, "q", c.load.tag)
}

func TestListCore_LoadWithTimeout(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	var has bool
	spy := func(ctx context.Context, _ *string) ([]fakeItem, *string, error) {
		_, has = ctx.Deadline()
		return nil, nil, nil
	}
	loadMsg(t, c.loadWith(loadSpec[fakeItem]{fetch: spy, label: "s"}))
	assert.False(t, has, "timeout 0: no deadline")

	loadMsg(t, c.loadWith(loadSpec[fakeItem]{fetch: spy, label: "s", timeout: time.Minute}))
	assert.True(t, has)
}

// OnLoaded runs before a waiting focus opens, so a focus sees the state
// that OnLoaded set.
func TestListCore_OnLoadedRunsBeforePendingFocus(t *testing.T) {
	var order []string
	cfg := fakeConfig(fetchReturns(nil))
	cfg.OnLoaded = func([]fakeItem, string) tea.Cmd {
		order = append(order, "loaded")
		return nil
	}
	cfg.OpenDetail = func(it fakeItem) tea.Cmd {
		order = append(order, "detail "+it.ID)
		return nil
	}
	c := newListCore(cfg)
	c.handle(msg.FocusResourceMsg{ResourceID: "a"})
	c.handle(listPageMsg[fakeItem]{items: []fakeItem{fakeA}})
	assert.Equal(t, []string{"loaded", "detail a"}, order)
}

// --- selection ---

func TestListCore_SpaceAndEscWithMultiSelect(t *testing.T) {
	c := newSelectCore(t, &refreshCall{})
	cmd, ok := c.handle(keyPress(' '))
	assert.True(t, ok)
	assert.Nil(t, cmd)
	assert.Equal(t, "2/2 fakes  (1 selected)", plain(c.footer()))

	cmd, _ = c.handle(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Nil(t, cmd, "esc deselects first")
	assert.Equal(t, "2/2 fakes", plain(c.footer()))

	cmd, _ = c.handle(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Equal(t, []tea.Msg{msg.NavigateBackMsg{}}, collectMsgs(cmd))
}

func TestListCore_SpaceIsNotTheCoresWithoutMultiSelect(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	c.handle(listPageMsg[fakeItem]{items: []fakeItem{fakeA}})
	_, ok := c.handle(keyPress(' '))
	assert.False(t, ok)
	assert.Equal(t, 0, c.table.SelectionCount())
}

func TestListCore_CopyTargetsWithMultiSelect(t *testing.T) {
	c := newSelectCore(t, &refreshCall{})
	cmd, _ := c.handle(keyPress('y'))
	msgs := collectMsgs(cmd)
	require.Len(t, toasts(msgs), 1)
	assert.Equal(t, msg.ToastSuccess("Copied 1 fake ID(s)"), toasts(msgs)[0])
	text, ok := charClipboard(msgs)
	require.True(t, ok)
	assert.Equal(t, "a", text)

	selectBoth(c)
	cmd, _ = c.handle(keyPress('y'))
	msgs = collectMsgs(cmd)
	text, _ = charClipboard(msgs)
	assert.Equal(t, "a\nb", text)
	assert.Equal(t, "Copied 2 fake ID(s)", toasts(msgs)[0].Text)

	c.setItems(nil)
	cmd, ok = c.handle(keyPress('y'))
	assert.True(t, ok)
	assert.Nil(t, cmd, "nothing to copy")
}

func TestListCore_FooterWithOrder(t *testing.T) {
	c := newSelectCore(t, &refreshCall{})
	c.handle(keyPress(' '))
	c.loading, c.pages = true, 1
	c.busy("Working...")
	assert.Equal(t, "base  (loading... 2 so far)  (1 selected)  ⣾  Working...", plain(c.footerWith("base")))
}

// --- selective refresh ---

func TestListCore_RefreshTargetsAndMerge(t *testing.T) {
	newer := fakeItem{"b", "beta-2"}
	r := &refreshCall{items: []fakeItem{newer, {"zzz", "unknown"}}}
	c := newSelectCore(t, r)
	selectBoth(c)
	gen := c.loadGen

	cmd, _ := c.handle(keyPress('r'))
	assert.False(t, c.loading, "a selective refresh is no full load")
	assert.Equal(t, "2/2 fakes  (2 selected)  ⣾  Refreshing 2 fakes...", plain(c.footer()))
	reply := loadMsg(t, cmd)
	assert.Equal(t, [][]string{{"a", "b"}}, r.got)
	assert.Equal(t, listRefreshedMsg[fakeItem]{gen: gen, items: r.items}, reply)

	cmd, _ = c.handle(reply)
	assert.Nil(t, cmd)
	assert.Equal(t, []fakeItem{fakeA, newer}, c.items, "merged in place; the unknown item is ignored")
	assert.Equal(t, "2/2 fakes  (2 selected)", plain(c.footer()), "spinner hidden")
}

func TestListCore_RefreshWithoutSelectionReloads(t *testing.T) {
	r := &refreshCall{}
	c := newSelectCore(t, r)
	cmd, _ := c.handle(keyPress('r'))
	assert.True(t, c.loading)
	assert.IsType(t, listPageMsg[fakeItem]{}, loadMsg(t, cmd))
	assert.Empty(t, r.got)
}

func TestListCore_RefreshPartialFailureMergesAndToasts(t *testing.T) {
	newer := fakeItem{"a", "alpha-2"}
	c := newSelectCore(t, &refreshCall{})
	cmd, _ := c.handle(listRefreshedMsg[fakeItem]{gen: c.loadGen, items: []fakeItem{newer}, err: errors.New("1 of 2 failed: denied")})
	assert.Equal(t, []fakeItem{newer, fakeB}, c.items)
	msgs := collectMsgs(cmd)
	require.Len(t, toasts(msgs), 1)
	assert.Equal(t, msg.ToastError("Refresh failed: 1 of 2 failed: denied"), toasts(msgs)[0])
}

func TestListCore_RefreshCanceledIsSilent(t *testing.T) {
	c := newSelectCore(t, &refreshCall{})
	c.busy("Refreshing 1 fakes...")
	cmd, _ := c.handle(listRefreshedMsg[fakeItem]{gen: c.loadGen, err: context.Canceled})
	assert.Nil(t, cmd)
	assert.False(t, c.spinner.Visible())
}

// A refresh reply from before a full load is dropped, and the spinner shows
// the text of the full load again.
func TestListCore_StaleRefreshDroppedKeepsLoadSpinner(t *testing.T) {
	c := newSelectCore(t, &refreshCall{})
	gen := c.loadGen
	c.handle(keyPress(' '))
	c.handle(keyPress('r')) // selective refresh of a
	c.handle(tea.KeyPressMsg{Code: tea.KeyEscape})
	c.reload()
	c.busy("Refreshing 1 fakes...") // as if the refresh had shown its label last

	cmd, _ := c.handle(listRefreshedMsg[fakeItem]{gen: gen, items: []fakeItem{{"a", "stale"}}, err: errors.New("x")})
	assert.Nil(t, cmd, "no toast for a stale reply")
	assert.Equal(t, "alpha", c.find("a").Name)
	assert.Equal(t, "2/2 fakes  ⣾  Loading fakes...", plain(c.footer()))
}

func TestListCore_IdleRestoresProgressLabel(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	tok := "t"
	c.handle(listPageMsg[fakeItem]{gen: c.loadGen, items: []fakeItem{fakeA}, next: &tok, page: 1})
	c.busy("Loading fake...")
	c.idle()
	assert.Contains(t, plain(c.footer()), "Loading fakes... 1 so far")
}

func TestListCore_MergeItems(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	c.setItems([]fakeItem{fakeA, fakeB, fakeC})
	c.mergeItems(nil)
	assert.Equal(t, []fakeItem{fakeA, fakeB, fakeC}, c.items)

	c.mergeItems([]fakeItem{{"c", "gamma-2"}, {"a", "alpha-2"}, {"x", "new"}})
	assert.Equal(t, []fakeItem{{"a", "alpha-2"}, fakeB, {"c", "gamma-2"}}, c.items)
	assert.Contains(t, plain(c.render("")), "gamma-2", "the rows follow")
}

// --- focus on a missing ID ---

func TestListCore_FocusMissing(t *testing.T) {
	var asked []string
	cfg := fakeConfig(fetchReturns(nil))
	cfg.FocusMissing = func(id string) tea.Cmd {
		asked = append(asked, id)
		return func() tea.Msg { return msg.ToastInfo("fetching " + id) }
	}
	c := newListCore(cfg)
	c.handle(listPageMsg[fakeItem]{items: []fakeItem{fakeA}})

	cmd, _ := c.handle(msg.FocusResourceMsg{ResourceID: "zzz"})
	assert.Equal(t, []tea.Msg{msg.ToastInfo("fetching zzz")}, collectMsgs(cmd))
	assert.Equal(t, []string{"zzz"}, asked)
	assert.Equal(t, "a", cursorID(&c), "the cursor stays")

	cmd, _ = c.handle(msg.FocusResourceMsg{ResourceID: "a"})
	assert.Equal(t, []tea.Msg{msg.TabbedContentMsg{PanelTitle: "a"}}, collectMsgs(cmd))
	assert.Equal(t, []string{"zzz"}, asked, "a listed ID does not ask")
}

// --- render ---

func TestListCore_RenderAbove(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	assert.Equal(t, "\n  ⣾  Loading fakes...", plain(c.render("? search\n")), "no side input on the spinner")
	c.handle(listPageMsg[fakeItem]{items: []fakeItem{fakeA}})
	assert.Regexp(t, `^\? search\n ID`, plain(c.render("? search\n")))
	c.handle(msg.ErrorMsg{Err: errors.New("denied")})
	assert.Equal(t, "\n  Error: denied", plain(c.render("? search\n")), "no side input on the error")
}

// --- refreshBy ---

type fakeRefresher interface {
	Refresh(ctx context.Context, keys []string) ([]fakeItem, error)
}

type fakeRefresherImpl struct {
	got      []string
	deadline bool
}

func (f *fakeRefresherImpl) Refresh(ctx context.Context, keys []string) ([]fakeItem, error) {
	f.got = keys
	_, f.deadline = ctx.Deadline()
	return []fakeItem{fakeA}, nil
}

func TestRefreshBy_NilServiceGivesErrNoClient(t *testing.T) {
	items, err := refreshBy(aws.EC2Service(nil), aws.EC2Service.RefreshAMIs, callTimeout)(t.Context(), []string{"ami-1"})
	require.ErrorIs(t, err, errNoClient)
	assert.Nil(t, items)
}

func TestRefreshBy_CallsTheServiceWithTimeout(t *testing.T) {
	f := &fakeRefresherImpl{}
	items, err := refreshBy[fakeRefresher](f, fakeRefresher.Refresh, time.Minute)(t.Context(), []string{"a", "b"})
	require.NoError(t, err)
	assert.Equal(t, []fakeItem{fakeA}, items)
	assert.Equal(t, []string{"a", "b"}, f.got)
	assert.True(t, f.deadline)

	_, err = refreshBy[fakeRefresher](f, fakeRefresher.Refresh, 0)(t.Context(), nil)
	require.NoError(t, err)
	assert.False(t, f.deadline, "timeout 0: no deadline")
}
