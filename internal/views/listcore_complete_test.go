package views

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
)

// Unit tests of the core parts that PR 8 adds: the second load phase
// (Complete), Derive, FocusKey, removeKeys and the refreshEach adapter.

// twoPages is a fetchFunc with page 1 = first and page 2 = second.
func twoPages(first, second []fakeItem) fetchFunc[fakeItem] {
	return func(_ context.Context, token *string) ([]fakeItem, *string, error) {
		if token == nil {
			next := "t2"
			return first, &next, nil
		}
		return second, nil, nil
	}
}

// completeCall records the calls of a Complete.Run. It names each item
// "<ID>-done", so a test sees that the result of Run made the rows.
type completeCall struct {
	mu       sync.Mutex
	got      [][]fakeItem
	deadline bool
	err      error
}

func (r *completeCall) run(ctx context.Context, items []fakeItem) ([]fakeItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, items)
	_, r.deadline = ctx.Deadline()
	if r.err != nil {
		return nil, r.err
	}
	out := make([]fakeItem, len(items))
	for i, it := range items {
		out[i] = fakeItem{ID: it.ID, Name: it.ID + "-done"}
	}
	return out, nil
}

func newCompleteCore(t *testing.T, fetch fetchFunc[fakeItem], r *completeCall) *listCore[fakeItem] {
	t.Helper()
	cfg := fakeConfig(fetch)
	cfg.Complete = &completePhase[fakeItem]{
		Run:     r.run,
		Timeout: time.Minute,
		Label:   "Completing %d fakes...",
		Context: "completing fakes",
	}
	c := newListCore(cfg)
	c.handle(tea.WindowSizeMsg{Width: 160, Height: 20})
	return &c
}

// runCore delivers the messages of cmd to the core, and the messages that
// follow, until no new core message comes. It returns the non-core
// messages and every msg.ErrorMsg (the app sees those too).
func runCore(c *listCore[fakeItem], cmd tea.Cmd) []tea.Msg {
	var out []tea.Msg
	queue := netNoTicks(collectMsgs(cmd))
	for len(queue) > 0 {
		m := queue[0]
		queue = queue[1:]
		if _, isErr := m.(msg.ErrorMsg); isErr {
			out = append(out, m)
		}
		next, ok := c.handle(m)
		if !ok {
			out = append(out, m)
			continue
		}
		queue = append(queue, netNoTicks(collectMsgs(next))...)
	}
	return out
}

func TestListCore_CompleteBuffersPagesUntilRunReturns(t *testing.T) {
	r := &completeCall{}
	c := newCompleteCore(t, twoPages([]fakeItem{fakeA}, []fakeItem{fakeB, fakeA}), r)

	page1 := loadMsg(t, c.init())
	page2 := loadMsg(t, mustHandle(t, c, page1))
	assert.Empty(t, c.items, "no rows before Run returns")
	assert.Equal(t, []fakeItem{fakeA}, c.buf)
	assert.Equal(t, "0/0 fakes  ⣾  Loading fakes... 1 so far", plain(c.footer()),
		"the footer shows no 'so far' with Complete; the spinner has the progress")

	complete := loadMsg(t, mustHandle(t, c, page2))
	assert.Equal(t, []fakeItem{fakeA, fakeB}, c.buf, "the buffer drops a key it already has")
	assert.True(t, c.completing)
	assert.True(t, c.loading, "a focus waits for the second phase too")
	assert.Equal(t, "0/0 fakes  ⣾  Completing 2 fakes...", plain(c.footer()))
	require.Len(t, r.got, 1)
	assert.Equal(t, []fakeItem{fakeA, fakeB}, r.got[0])
	assert.True(t, r.deadline, "Run has the Complete timeout")

	assert.Empty(t, collectMsgs(mustHandle(t, c, complete)))
	assert.Equal(t, []fakeItem{{"a", "a-done"}, {"b", "b-done"}}, c.items)
	assert.Empty(t, c.buf)
	assert.False(t, c.completing)
	assert.False(t, c.loading)
	assert.False(t, c.spinner.Visible())
}

func mustHandle(t *testing.T, c *listCore[fakeItem], m tea.Msg) tea.Cmd {
	t.Helper()
	cmd, ok := c.handle(m)
	require.True(t, ok, "%T", m)
	return cmd
}

func TestListCore_CompleteZeroItemsClearsRowsWithoutRun(t *testing.T) {
	r := &completeCall{}
	c := newCompleteCore(t, fetchReturns(nil), r)
	c.setItems([]fakeItem{fakeA})
	runCore(c, c.reload("Loading fakes..."))
	assert.Empty(t, c.items)
	assert.Equal(t, "0/0 fakes", plain(c.footer()))
	assert.Empty(t, r.got)
	assert.False(t, c.loading)
}

func TestListCore_CompleteStaleReplyDropped(t *testing.T) {
	r := &completeCall{}
	c := newCompleteCore(t, fetchReturns(nil, fakeA), r)
	mustHandle(t, c, loadMsg(t, c.init()))
	oldComplete := listCompleteMsg[fakeItem]{gen: c.loadGen, items: []fakeItem{{"old", "old"}}}
	runCore(c, c.reload("Loading fakes..."))
	require.Equal(t, []fakeItem{{"a", "a-done"}}, c.items)

	c.handle(oldComplete)
	assert.Equal(t, []fakeItem{{"a", "a-done"}}, c.items, "the reply of the first load is dropped")
}

// A refresh that ends while the second phase runs keeps its items: Run may
// have read them before the refresh did (a purge between the two).
func TestListCore_RefreshDuringCompleteIsKept(t *testing.T) {
	r := &completeCall{}
	cfg := fakeConfig(fetchReturns(nil, fakeA, fakeB))
	cfg.Complete = &completePhase[fakeItem]{Run: r.run, Label: "Completing %d fakes...", Context: "completing fakes"}
	cfg.Refresh = func(context.Context, []string) ([]fakeItem, error) { return nil, nil }
	cfg.MultiSelect = true
	core := newListCore(cfg)
	c := &core
	c.handle(tea.WindowSizeMsg{Width: 160, Height: 20})
	runCore(c, c.init())
	require.Equal(t, []fakeItem{{"a", "a-done"}, {"b", "b-done"}}, c.items)

	complete := loadMsg(t, mustHandle(t, c, loadMsg(t, c.reload("Loading fakes..."))))
	require.True(t, c.completing)
	c.handle(listRefreshedMsg[fakeItem]{gen: c.loadGen, items: []fakeItem{{"a", "purged"}}})
	assert.Equal(t, []fakeItem{{"a", "purged"}, {"b", "b-done"}}, c.items)

	c.handle(complete)
	assert.Equal(t, []fakeItem{{"a", "purged"}, {"b", "b-done"}}, c.items, "the refresh read wins over the older Run read")
	assert.Empty(t, c.refreshed, "the next load starts clean")

	runCore(c, c.reload("Loading fakes..."))
	assert.Equal(t, []fakeItem{{"a", "a-done"}, {"b", "b-done"}}, c.items)
}

// The same holds for a paged load: a page that arrives after the refresh
// does not undo it.
func TestListCore_RefreshDuringPagesIsKept(t *testing.T) {
	c := newSelectCore(t, &refreshCall{})
	cmd := c.reload("Loading fakes...")
	page := loadMsg(t, cmd)
	c.handle(listRefreshedMsg[fakeItem]{gen: c.loadGen, items: []fakeItem{{"a", "fresh"}}})
	c.handle(page)
	got := c.find("a")
	require.NotNil(t, got)
	assert.Equal(t, "fresh", got.Name)
	assert.Empty(t, c.refreshed)
}

func TestListCore_TaskSpinnerSurvivesRefreshReply(t *testing.T) {
	c := newSelectCore(t, &refreshCall{})
	c.startTask("Deleting 1 fakes...")
	c.refreshKeys([]string{"a"})
	assert.Equal(t, "2/2 fakes  ⣾  Refreshing 1 fakes...", plain(c.footer()))

	c.handle(listRefreshedMsg[fakeItem]{gen: c.loadGen})
	assert.Equal(t, "2/2 fakes  ⣾  Deleting 1 fakes...", plain(c.footer()), "the task still runs")

	c.endTask()
	assert.False(t, c.spinner.Visible())
	c.endTask()
	assert.Zero(t, c.tasks, "endTask never goes below zero")

	// A task that ends during a full load gives the spinner back to the load.
	c.startTask("Purging 1 fakes...")
	c.reload("Loading fakes...")
	c.endTask()
	assert.Equal(t, "2/2 fakes  ⣾  Loading fakes...", plain(c.footer()))
}

func TestListCore_CompleteFailureUsesItsContext(t *testing.T) {
	boom := errors.New("boom")
	r := &completeCall{err: boom}
	c := newCompleteCore(t, fetchReturns(nil, fakeA), r)
	out := runCore(c, c.init())
	e, ok := firstOfType[msg.ErrorMsg](out)
	require.True(t, ok)
	assert.Equal(t, "completing fakes", e.Context)
	require.ErrorIs(t, e.Err, boom)
	assert.False(t, c.completing)
	assert.False(t, c.loading)
	require.ErrorIs(t, c.err, boom)

	// A stale failure of the second phase is dropped.
	r.err = nil
	runCore(c, c.reload("Loading fakes..."))
	c.handle(listFailedMsg{gen: c.loadGen - 1, err: boom, context: "completing fakes"})
	require.NoError(t, c.err)
}

func TestListCore_CompleteIdleShowsSecondPhaseLabel(t *testing.T) {
	r := &completeCall{}
	c := newCompleteCore(t, fetchReturns(nil, fakeA), r)
	mustHandle(t, c, loadMsg(t, c.init()))
	require.True(t, c.completing)
	c.busy("Refreshing 1 fakes...")
	c.idle()
	assert.Equal(t, "0/0 fakes  ⣾  Completing 1 fakes...", plain(c.footer()))
}

func TestListCore_CompleteNeedsItsFields(t *testing.T) {
	cfg := fakeConfig(fetchReturns(nil))
	cfg.Complete = &completePhase[fakeItem]{Label: "x %d", Context: "y"}
	assert.Panics(t, func() { newListCore(cfg) })
}

// deriveConfig marks every item whose ID is the Name of another item.
func deriveCore(t *testing.T, fetch fetchFunc[fakeItem]) (*listCore[fakeItem], *int) {
	t.Helper()
	calls := 0
	cfg := fakeConfig(fetch)
	cfg.Derive = func(items []fakeItem) {
		calls++
		named := map[string]bool{}
		for _, it := range items {
			named[strings.TrimSuffix(it.Name, "*")] = true
		}
		for i := range items {
			items[i].Name = strings.TrimSuffix(items[i].Name, "*")
			if named[items[i].ID] {
				items[i].Name += "*"
			}
		}
	}
	c := newListCore(cfg)
	c.handle(tea.WindowSizeMsg{Width: 160, Height: 20})
	return &c, &calls
}

func TestListCore_DeriveRunsOnEachDataChange(t *testing.T) {
	c, calls := deriveCore(t, twoPages([]fakeItem{{"a", "x"}}, []fakeItem{{"b", "a"}}))
	runCore(c, c.init())
	assert.Equal(t, []fakeItem{{"a", "x*"}, {"b", "a"}}, c.items, "a later page changes an earlier row")

	n := *calls
	c.mergeItems([]fakeItem{{"b", "none"}})
	assert.Equal(t, []fakeItem{{"a", "x"}, {"b", "none"}}, c.items, "a merge clears a stale mark")
	assert.Equal(t, n+1, *calls)

	c.updateItems([]string{"b"}, func(it *fakeItem) { it.Name = "a" })
	assert.Equal(t, []fakeItem{{"a", "x*"}, {"b", "a"}}, c.items)

	c.removeKeys([]string{"b"})
	assert.Equal(t, []fakeItem{{"a", "x"}}, c.items, "a remove clears the mark too")
}

func TestListCore_RemoveKeys(t *testing.T) {
	c := newFakeCore(t, fetchReturns(nil))
	c.setItems([]fakeItem{fakeA, fakeB, fakeC})
	old := c.items
	c.removeKeys([]string{"b", "missing"})
	assert.Equal(t, []fakeItem{fakeA, fakeC}, c.items)
	_, total := c.table.RowCount()
	assert.Equal(t, 2, total)
	assert.Equal(t, fakeB, old[1], "the old slice is not changed")
	c.removeKeys(nil)
	assert.Len(t, c.items, 2)
}

func TestListCore_FocusKey(t *testing.T) {
	cfg := fakeConfig(fetchReturns(nil))
	cfg.FocusKey = func(it fakeItem) string { return it.Name }
	c := newListCore(cfg)
	c.handle(tea.WindowSizeMsg{Width: 160, Height: 20})
	c.loading = false
	c.setItems([]fakeItem{fakeA, fakeB})

	cmd, _ := c.handle(msg.FocusResourceMsg{ResourceID: "beta"})
	assert.Equal(t, []tea.Msg{msg.TabbedContentMsg{PanelTitle: "b"}}, collectMsgs(cmd))
	assert.Equal(t, "b", c.table.CursorKey(), "the cursor moves by Key")

	cmd, _ = c.handle(msg.FocusResourceMsg{ResourceID: "b"})
	assert.Nil(t, cmd, "a Key is not a focus ID when FocusKey is set")
}

// --- refreshEach ---

type fakeGetter struct {
	mu        sync.Mutex
	deadlines map[string]time.Duration
	fail      map[string]error
	active    atomic.Int32
	peak      atomic.Int32
	wait      time.Duration
}

func (g *fakeGetter) get(ctx context.Context, key string) (*fakeItem, error) {
	n := g.active.Add(1)
	defer g.active.Add(-1)
	for {
		p := g.peak.Load()
		if n <= p || g.peak.CompareAndSwap(p, n) {
			break
		}
	}
	if d, ok := ctx.Deadline(); ok {
		g.mu.Lock()
		g.deadlines[key] = time.Until(d)
		g.mu.Unlock()
	}
	time.Sleep(g.wait)
	if err := g.fail[key]; err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &fakeItem{ID: key, Name: "fresh"}, nil
}

func TestRefreshEach_PartialFailure(t *testing.T) {
	denied := errors.New("denied")
	g := &fakeGetter{deadlines: map[string]time.Duration{}, fail: map[string]error{"b": denied}}
	refresh := refreshEach(g, (*fakeGetter).get, 30*time.Second)
	items, err := refresh(context.Background(), []string{"a", "b", "c"})
	assert.Equal(t, []fakeItem{{"a", "fresh"}, {"c", "fresh"}}, items, "the read items, in key order")
	require.EqualError(t, err, "1 of 3 failed: denied")
	assert.Len(t, g.deadlines, 3, "each key has its own call")
	for k, d := range g.deadlines {
		assert.InDelta(t, 30, d.Seconds(), 2, k)
	}
}

func TestRefreshEach_ErrorTextKeepsCount(t *testing.T) {
	g := &fakeGetter{deadlines: map[string]time.Duration{}, fail: map[string]error{"a": context.DeadlineExceeded}}
	_, err := refreshEach(g, (*fakeGetter).get, time.Second)(context.Background(), []string{"a", "b"})
	assert.Equal(t, "1 of 2 failed: no answer from AWS in time", aws.ErrorText(err))
}

func TestRefreshEach_AllGoodAndLimit(t *testing.T) {
	g := &fakeGetter{deadlines: map[string]time.Duration{}, wait: 20 * time.Millisecond}
	keys := make([]string, 3*refreshLimit)
	for i := range keys {
		keys[i] = string(rune('a' + i))
	}
	items, err := refreshEach(g, (*fakeGetter).get, 0)(context.Background(), keys)
	require.NoError(t, err)
	assert.Len(t, items, len(keys))
	assert.LessOrEqual(t, int(g.peak.Load()), refreshLimit)
	assert.Greater(t, int(g.peak.Load()), 1, "the calls run in parallel")
	assert.Empty(t, g.deadlines, "a 0 timeout sets no deadline")
}

func TestRefreshEach_CancelIsSilent(t *testing.T) {
	g := &fakeGetter{deadlines: map[string]time.Duration{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := refreshEach(g, (*fakeGetter).get, time.Second)(ctx, []string{"a", "b"})
	require.ErrorIs(t, err, context.Canceled)

	c := newSelectCore(t, &refreshCall{})
	assert.Nil(t, mustHandle(t, c, listRefreshedMsg[fakeItem]{gen: c.loadGen, err: err}), "no toast")
}

// fakeGetterAPI is an interface, like the aws services the adapters take.
type fakeGetterAPI interface {
	get(ctx context.Context, key string) (*fakeItem, error)
}

func TestRefreshEach_NilService(t *testing.T) {
	_, err := refreshEach[fakeGetterAPI](nil, fakeGetterAPI.get, time.Second)(context.Background(), []string{"a"})
	require.ErrorIs(t, err, errNoClient)
}
