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

	"github.com/juthrbog/lazycloud/internal/msg"
)

// Tests for PageBudget, MoreKey and FetchDetail of the list core
// (docs/design/cwlogs.md, PR 1).

// newMoreCore builds a fake core with MoreKey and the given budget.
func newMoreCore(t *testing.T, budget int, fetch fetchFunc[fakeItem]) *listCore[fakeItem] {
	t.Helper()
	cfg := fakeConfig(fetch)
	cfg.PageBudget = budget
	cfg.MoreKey = true
	c := newListCore(cfg)
	c.handle(tea.WindowSizeMsg{Width: 160, Height: 20})
	return &c
}

// numberedPages is a paged fetch of n pages with one item each. It records
// the tokens it gets ("" for nil).
func numberedPages(n int, tokens *[]string) fetchFunc[fakeItem] {
	return func(_ context.Context, tok *string) ([]fakeItem, *string, error) {
		i := 0
		k := ""
		if tok != nil {
			k = *tok
			if _, err := fmt.Sscanf(k, "t%d", &i); err != nil {
				return nil, nil, err
			}
		}
		if tokens != nil {
			*tokens = append(*tokens, k)
		}
		items := []fakeItem{{ID: fmt.Sprintf("id%d", i), Name: "x"}}
		if i == n-1 {
			return items, nil, nil
		}
		next := fmt.Sprintf("t%d", i+1)
		return items, &next, nil
	}
}

// drain handles page messages until the core stops loading.
func drain(t *testing.T, c *listCore[fakeItem], cmd tea.Cmd) {
	t.Helper()
	for c.running {
		cmd, _ = c.handle(loadMsg(t, cmd))
	}
}

func TestListMore_BudgetStopsAndLMoreContinues(t *testing.T) {
	var tokens []string
	c := newMoreCore(t, 2, numberedPages(5, &tokens))
	gen := c.loadGen + 1

	drain(t, c, c.init())
	assert.Equal(t, 2, c.pages, "the budget stops after 2 pages")
	assert.Len(t, c.items, 2)
	assert.False(t, c.loading)
	assert.Equal(t, gen, c.loadGen)
	assert.Contains(t, plain(c.footer()), "2/2 fakes · l for more")

	cmd, ok := c.handle(keyPress('l'))
	require.True(t, ok)
	require.NotNil(t, cmd)
	assert.Nil(t, mustNil(c.handle(keyPress('l'))), "a second l while pages load does nothing")
	assert.NotContains(t, plain(c.footer()), "l for more", "no hint while pages load")
	drain(t, c, cmd)
	assert.Equal(t, 4, c.pages)
	assert.Len(t, c.items, 4, "the pages append")
	assert.Equal(t, gen, c.loadGen, "load more keeps the generation")

	cmd, _ = c.handle(keyPress('l'))
	drain(t, c, cmd)
	assert.Len(t, c.items, 5)
	assert.Nil(t, c.more, "the last page has no next token")
	assert.NotContains(t, plain(c.footer()), "l for more")
	assert.Nil(t, mustNil(c.handle(keyPress('l'))), "l does nothing at the end")
	assert.Equal(t, []string{"", "t1", "t2", "t3", "t4"}, tokens, "each page once, in order")
}

// mustNil returns the cmd of handle, for asserts on the cmd only.
func mustNil(cmd tea.Cmd, _ bool) tea.Cmd { return cmd }

func TestListMore_LaterFailureKeepsRowsAndLRetries(t *testing.T) {
	fail := true
	var tokens []string
	c := newMoreCore(t, 0, func(ctx context.Context, tok *string) ([]fakeItem, *string, error) {
		if tok != nil && *tok == "t2" && fail {
			tokens = append(tokens, "t2!")
			return nil, nil, errors.New("throttled")
		}
		return numberedPages(4, &tokens)(ctx, tok)
	})
	cmd, _ := c.handle(loadMsg(t, c.init()))
	cmd, _ = c.handle(loadMsg(t, cmd))
	failed := loadMsg(t, cmd)
	cmd, _ = c.handle(failed)
	em, ok := firstOfType[msg.ErrorMsg](collectMsgs(cmd))
	require.True(t, ok, "the error goes to the status bar")
	require.EqualError(t, em.Err, "throttled")

	require.NoError(t, c.err, "no error screen")
	assert.False(t, c.loading)
	out := plain(c.render("", ""))
	assert.NotContains(t, out, "Error:")
	assert.Contains(t, out, "id0")
	assert.Contains(t, out, "id1")
	assert.Contains(t, plain(c.footer()), "· l for more")

	fail = false
	cmd, _ = c.handle(keyPress('l'))
	drain(t, c, cmd)
	assert.Len(t, c.items, 4)
	assert.Equal(t, []string{"", "t1", "t2!", "t2", "t3"}, tokens, "l asks for the failed page again")
}

// A focus that waits for a load more acts on the kept rows when that page
// fails. It must not fire later, at the end of the next good l.
func TestListMore_LaterFailureAppliesPendingFocus(t *testing.T) {
	fail := true
	c := newMoreCore(t, 1, func(ctx context.Context, tok *string) ([]fakeItem, *string, error) {
		if tok != nil && *tok == "t1" && fail {
			return nil, nil, errors.New("throttled")
		}
		return numberedPages(3, nil)(ctx, tok)
	})
	drain(t, c, c.init())
	more, _ := c.handle(keyPress('l'))
	failed := loadMsg(t, more)
	_, _ = c.handle(msg.FocusResourceMsg{ResourceID: "id0"})
	require.Equal(t, "id0", c.pendingFocus, "the focus waits while l loads")

	cmd, _ := c.handle(failed)
	assert.Empty(t, c.pendingFocus)
	got, ok := firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
	require.True(t, ok, "the focus opens on the kept rows")
	assert.Equal(t, "id0", got.PanelTitle)

	fail = false
	cmd, _ = c.handle(keyPress('l'))
	cmd, _ = c.handle(loadMsg(t, cmd))
	_, again := firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
	assert.False(t, again, "the next good l opens no old focus")
}

func TestListMore_FirstPageFailureShowsErrorScreen(t *testing.T) {
	c := newMoreCore(t, 2, fetchReturns(errors.New("denied")))
	c.handle(loadMsg(t, c.init()))
	require.Error(t, c.err)
	assert.Contains(t, plain(c.render("", "")), "Error:")
	assert.Nil(t, mustNil(c.handle(keyPress('l'))), "l has no token to send")
}

func TestListMore_WithoutMoreKeyLaterFailureShowsErrorScreen(t *testing.T) {
	c := newFakeCore(t, fakePages(map[string][]fakeItem{"": {fakeA}}, map[string]string{"": "t2"}))
	cmd, _ := c.handle(loadMsg(t, c.init()))
	c.handle(loadMsg(t, cmd))
	require.Error(t, c.err)
	assert.Contains(t, plain(c.render("", "")), "Error:")
	assert.NotContains(t, plain(c.footer()), "l for more", "no hint without MoreKey")
	_, ok := c.handle(keyPress('l'))
	assert.False(t, ok, "l is not a key of a list without MoreKey")
}

func TestListMore_OldTokenIsNotSent(t *testing.T) {
	start := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	now := start
	listNow = func() time.Time { return now }
	t.Cleanup(func() { listNow = time.Now })

	var tokens []string
	c := newMoreCore(t, 1, numberedPages(3, &tokens))
	drain(t, c, c.init())
	require.NotNil(t, c.more)

	now = start.Add(tokenMaxAge + time.Minute)
	cmd, ok := c.handle(keyPress('l'))
	require.True(t, ok)
	got := collectMsgs(cmd)
	require.Len(t, got, 1)
	assert.Equal(t, msg.ToastError("The page token expired. Press r to load again."), got[0])
	assert.False(t, c.running)
	assert.Equal(t, []string{""}, tokens, "the old token is not sent")

	now = start.Add(tokenMaxAge - time.Minute)
	cmd, _ = c.handle(keyPress('l'))
	drain(t, c, cmd)
	assert.Equal(t, []string{"", "t1"}, tokens, "a token younger than the limit is sent")
}

func TestListMore_RefreshAfterLoadMoreStartsFromPageOne(t *testing.T) {
	var tokens []string
	c := newMoreCore(t, 1, numberedPages(3, &tokens))
	drain(t, c, c.init())
	more, _ := c.handle(keyPress('l'))
	late := loadMsg(t, more) // page 2 of the old generation, not yet handled

	cmd, _ := c.handle(keyPress('r'))
	assert.Nil(t, c.more, "r drops the kept token")
	c.handle(late)
	assert.Equal(t, []fakeItem{{ID: "id0", Name: "x"}}, c.items, "a late page of the old load is dropped")
	drain(t, c, cmd)
	assert.Equal(t, []fakeItem{{ID: "id0", Name: "x"}}, c.items)
	assert.Empty(t, tokens[len(tokens)-1], "r asks for page 1 with a nil token")
	assert.NotNil(t, c.more)
}

func TestListMore_RepeatedTokenEndsCompleteLoad(t *testing.T) {
	cfg := fakeConfig(func(_ context.Context, tok *string) ([]fakeItem, *string, error) {
		k := "same"
		return []fakeItem{fakeA}, &k, nil
	})
	ran := false
	cfg.Complete = &completePhase[fakeItem]{
		Run: func(_ context.Context, items []fakeItem) ([]fakeItem, error) {
			ran = true
			return items, nil
		},
		Label:   "Completing %d fakes...",
		Context: "completing fakes",
	}
	c := newListCore(cfg)
	cmd, _ := c.handle(loadMsg(t, c.init()))
	cmd, _ = c.handle(loadMsg(t, cmd))
	cmd, _ = c.handle(loadMsg(t, cmd)) // the complete phase
	assert.True(t, ran, "the repeated token starts the second phase")
	assert.Equal(t, []tea.Msg{msg.LoadedMsg{}}, collectMsgs(cmd))
}

func TestListMore_ConfigPanics(t *testing.T) {
	complete := &completePhase[fakeItem]{
		Run:   func(_ context.Context, it []fakeItem) ([]fakeItem, error) { return it, nil },
		Label: "x %d", Context: "x",
	}
	for name, edit := range map[string]func(*listConfig[fakeItem]){
		"budget with complete":  func(c *listConfig[fakeItem]) { c.PageBudget, c.MoreKey, c.Complete = 2, true, complete },
		"morekey with complete": func(c *listConfig[fakeItem]) { c.MoreKey, c.Complete = true, complete },
		"budget without more":   func(c *listConfig[fakeItem]) { c.PageBudget = 2 },
		"two details": func(c *listConfig[fakeItem]) {
			c.FetchDetail = func(context.Context, fakeItem) (msg.TabbedContentMsg, error) {
				return msg.TabbedContentMsg{}, nil
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := fakeConfig(fetchReturns(nil, fakeA))
			edit(&cfg)
			assert.Panics(t, func() { newListCore(cfg) })
		})
	}
}

// newDetailCore builds a fake core with FetchDetail in place of OpenDetail.
func newDetailCore(t *testing.T, fetch fetchFunc[fakeItem],
	detail func(context.Context, fakeItem) (msg.TabbedContentMsg, error),
) *listCore[fakeItem] {
	t.Helper()
	cfg := fakeConfig(fetch)
	cfg.OpenDetail = nil
	cfg.FetchDetail = detail
	c := newListCore(cfg)
	c.handle(tea.WindowSizeMsg{Width: 160, Height: 20})
	return &c
}

func panelOf(it fakeItem) (msg.TabbedContentMsg, error) {
	return msg.TabbedContentMsg{PanelTitle: it.ID, Tabs: []msg.TabContent{{Title: "Info", Content: it.Name}}}, nil
}

// runDetail runs the fetch cmd and gives its reply to the core. It returns
// the messages of the reply.
func runDetail(t *testing.T, c *listCore[fakeItem], cmd tea.Cmd) []tea.Msg {
	t.Helper()
	got := collectMsgs(cmd)
	require.Len(t, got, 1)
	dm, ok := got[0].(listDetailMsg)
	require.True(t, ok, "%T", got[0])
	reply, handled := c.handle(dm)
	require.True(t, handled)
	return collectMsgs(reply)
}

func TestListDetail_DOpensFetchedPanel(t *testing.T) {
	var gotCtx context.Context
	c := newDetailCore(t, fetchReturns(nil, fakeA, fakeB), func(ctx context.Context, it fakeItem) (msg.TabbedContentMsg, error) {
		gotCtx = ctx
		return panelOf(it)
	})
	c.handle(loadMsg(t, c.init()))

	cmd, ok := c.handle(keyPress('d'))
	require.True(t, ok)
	got := runDetail(t, c, cmd)
	require.Len(t, got, 1)
	assert.Equal(t, "a", got[0].(msg.TabbedContentMsg).PanelTitle)
	_, hasDeadline := gotCtx.Deadline()
	assert.True(t, hasDeadline, "the fetch has a time limit")
}

func TestListDetail_EnterGoesToTheView(t *testing.T) {
	c := newDetailCore(t, fetchReturns(nil, fakeA), func(_ context.Context, it fakeItem) (msg.TabbedContentMsg, error) {
		return panelOf(it)
	})
	c.handle(loadMsg(t, c.init()))
	_, ok := c.handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.False(t, ok, "the core leaves enter to the view")
}

func TestListDetail_FocusOpensFetchedPanel(t *testing.T) {
	c := newDetailCore(t, fetchReturns(nil, fakeA, fakeB), func(_ context.Context, it fakeItem) (msg.TabbedContentMsg, error) {
		return panelOf(it)
	})
	c.handle(loadMsg(t, c.init()))
	cmd, _ := c.handle(msg.FocusResourceMsg{ResourceID: "b"})
	got := runDetail(t, c, cmd)
	assert.Equal(t, "b", got[0].(msg.TabbedContentMsg).PanelTitle)
	assert.Equal(t, "b", cursorID(c))
}

func TestListDetail_ErrorGivesToast(t *testing.T) {
	c := newDetailCore(t, fetchReturns(nil, fakeA), func(context.Context, fakeItem) (msg.TabbedContentMsg, error) {
		return msg.TabbedContentMsg{}, errors.New("denied")
	})
	c.handle(loadMsg(t, c.init()))
	cmd, _ := c.handle(keyPress('d'))
	got := runDetail(t, c, cmd)
	require.Len(t, got, 1)
	assert.Equal(t, msg.ToastError("Details failed: denied"), got[0])
}

func TestListDetail_ErrorScreenBlocksD(t *testing.T) {
	called := false
	c := newDetailCore(t, fetchReturns(errors.New("denied")), func(_ context.Context, it fakeItem) (msg.TabbedContentMsg, error) {
		called = true
		return panelOf(it)
	})
	c.handle(loadMsg(t, c.init()))
	require.Error(t, c.err)
	cmd, ok := c.handle(keyPress('d'))
	assert.True(t, ok)
	assert.Nil(t, cmd)
	assert.False(t, called)
}
