package views

import (
	"context"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/msg"
)

// Pin tests for the CloudWatch Logs design (docs/design/cwlogs.md). They
// pin the hazards of the list core as they are today (TestLogsHazard_*)
// and the behavior that must stay (TestLogsKeep_*). The PR that fixes a
// hazard flips its test. H5 (the host prefix of StartLiveTail) needs the
// cloudwatchlogs module, so PR 2 pins it.

// H1: a page whose next token equals its request token makes the core
// fetch the same page again. GetLogEvents does this at the end of each
// stream, so the load never ends.
func TestLogsHazard_RepeatedTokenFetchesAgain(t *testing.T) {
	var tokens []string
	c := newFakeCore(t, func(_ context.Context, tok *string) ([]fakeItem, *string, error) {
		k := "end"
		if tok != nil {
			k = *tok
		}
		tokens = append(tokens, k)
		return []fakeItem{fakeA}, &k, nil // the same token, as GetLogEvents does at the end
	})
	cmd := c.init()
	for range 4 {
		var ok bool
		cmd, ok = c.handle(loadMsg(t, cmd))
		require.True(t, ok)
		require.NotNil(t, cmd, "the core asks for the next page")
	}
	assert.Equal(t, []string{"end", "end", "end", "end"}, tokens, "the same token again and again")
	assert.True(t, c.loading, "the load does not end")
}

// H2: the core has no page budget. It loads every page of a long list.
func TestLogsHazard_NoPageBudget(t *testing.T) {
	const n = 30
	pages, next := map[string][]fakeItem{}, map[string]string{}
	for i := range n {
		tok := ""
		if i > 0 {
			tok = fmt.Sprintf("t%d", i)
		}
		pages[tok] = []fakeItem{{ID: fmt.Sprintf("id%d", i), Name: "x"}}
		if i < n-1 {
			next[tok] = fmt.Sprintf("t%d", i+1)
		}
	}
	c := newFakeCore(t, fakePages(pages, next))
	cmd := c.init()
	for c.loading {
		cmd, _ = c.handle(loadMsg(t, cmd))
	}
	assert.Equal(t, n, c.pages, "all pages load, with no stop")
	assert.Len(t, c.items, n)
}

// H3: a failure on a later page hides the rows that already loaded. The
// error screen replaces the table.
func TestLogsHazard_LaterFailureHidesRows(t *testing.T) {
	c := newFakeCore(t, fakePages(
		map[string][]fakeItem{"": {fakeA, fakeB}}, // t2 is unknown: the fetch fails
		map[string]string{"": "t2"},
	))
	cmd, _ := c.handle(loadMsg(t, c.init()))
	require.Len(t, c.items, 2, "page 1 loaded")
	c.handle(loadMsg(t, cmd))

	out := plain(c.render("", ""))
	assert.Contains(t, out, "Error:")
	assert.NotContains(t, out, "alpha", "the loaded rows are hidden")
	assert.NotContains(t, out, "beta")
}

// A nil next token ends a load.
func TestLogsKeep_NilTokenEndsLoad(t *testing.T) {
	c := newFakeCore(t, fakePages(
		map[string][]fakeItem{"": {fakeA}, "t2": {fakeB}},
		map[string]string{"": "t2"},
	))
	cmd, _ := c.handle(loadMsg(t, c.init()))
	cmd, _ = c.handle(loadMsg(t, cmd))
	assert.Equal(t, []tea.Msg{msg.LoadedMsg{}}, collectMsgs(cmd))
	assert.False(t, c.loading)
	assert.Equal(t, []fakeItem{fakeA, fakeB}, c.items)
}

// r starts a new load from page 1, with a nil token.
func TestLogsKeep_RefreshStartsFromPageOne(t *testing.T) {
	var tokens []*string
	c := newFakeCore(t, func(_ context.Context, tok *string) ([]fakeItem, *string, error) {
		tokens = append(tokens, tok)
		return []fakeItem{fakeA}, nil, nil
	})
	c.handle(loadMsg(t, c.init()))
	gen := c.loadGen

	cmd, ok := c.handle(keyPress('r'))
	require.True(t, ok)
	page := loadMsg(t, cmd)
	assert.Equal(t, 1, page.(listPageMsg[fakeItem]).page)
	assert.Greater(t, c.loadGen, gen, "r starts a new load generation")
	require.Len(t, tokens, 2)
	assert.Nil(t, tokens[1], "page 1 gets a nil token")
}

// A failure of the first page shows the error screen.
func TestLogsKeep_FirstPageFailureShowsErrorScreen(t *testing.T) {
	c := newFakeCore(t, fetchReturns(assert.AnError))
	c.handle(loadMsg(t, c.init()))
	require.Error(t, c.err)
	assert.Contains(t, plain(c.render("", "")), "Error:")
}

// enter and d both open OpenDetail.
func TestLogsKeep_EnterAndDOpenDetail(t *testing.T) {
	for name, k := range map[string]tea.KeyPressMsg{
		"enter": {Code: tea.KeyEnter},
		"d":     keyPress('d'),
	} {
		t.Run(name, func(t *testing.T) {
			c := newFakeCore(t, fetchReturns(nil, fakeA))
			c.handle(loadMsg(t, c.init()))
			cmd, ok := c.handle(k)
			require.True(t, ok)
			got, isPanel := firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
			require.True(t, isPanel, "%s opens the detail", name)
			assert.Equal(t, "a", got.PanelTitle)
		})
	}
}
