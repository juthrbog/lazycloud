package views

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/msg"
)

// Unit tests of the core parts that PR 7 adds: backgroundReload, the label
// of reload, and updateItems.

func TestListCore_BackgroundReloadLeavesLoadingAndErrorAlone(t *testing.T) {
	c := newSelectCore(t, &refreshCall{})
	old := errors.New("old")
	c.err = old
	gen := c.loadGen

	cmd := c.backgroundReload("Refreshing fakes...")

	assert.False(t, c.loading, "the footer shows no progress and a focus does not wait")
	require.ErrorIs(t, c.err, old)
	assert.Equal(t, gen+1, c.loadGen)
	assert.True(t, c.running)
	assert.Equal(t, "2/2 fakes  ⣾  Refreshing fakes...", plain(c.footer()))

	focus, _ := c.handle(msg.FocusResourceMsg{ResourceID: "b"})
	assert.Equal(t, []tea.Msg{msg.TabbedContentMsg{PanelTitle: "b"}}, collectMsgs(focus), "opens at once")
	assert.Empty(t, c.pendingFocus)

	c.handle(loadMsg(t, cmd))
	assert.False(t, c.running)
	assert.False(t, c.spinner.Visible())
	require.NoError(t, c.err, "a good load clears the error")
}

func TestListCore_BackgroundReloadGoesBackToDefaultLoad(t *testing.T) {
	c := newSelectCore(t, &refreshCall{})
	c.loadWith(loadSpec[fakeItem]{fetch: fetchReturns(nil), label: "s", tag: "q", errContext: "searching"})

	cmd := c.backgroundReload("Refreshing fakes...")

	assert.Empty(t, c.load.tag)
	assert.Equal(t, "listing fakes", c.load.errContext)
	assert.IsType(t, listPageMsg[fakeItem]{}, loadMsg(t, cmd))
}

// The end of a side task keeps the spinner of a background reload, with its
// text, because the reload is still running.
func TestListCore_IdleKeepsBackgroundReloadSpinner(t *testing.T) {
	c := newSelectCore(t, &refreshCall{})
	c.backgroundReload("Refreshing fakes...")
	c.busy("Side task...")

	c.idle()

	assert.Contains(t, plain(c.footer()), "Refreshing fakes...")
}

func TestListCore_FailedBackgroundReloadStopsRunning(t *testing.T) {
	c := newSelectCore(t, &refreshCall{})
	c.backgroundReload("Refreshing fakes...")

	c.handle(listFailedMsg{gen: c.loadGen, err: errors.New("x"), context: "c"})

	assert.False(t, c.running)
	c.busy("Side task...")
	c.idle()
	assert.False(t, c.spinner.Visible(), "no load runs, so idle hides the spinner")
}

func TestListCore_ReloadUsesLabel(t *testing.T) {
	c := newSelectCore(t, &refreshCall{})

	c.reload("Refreshing fakes...")

	assert.True(t, c.loading)
	assert.Equal(t, "2/2 fakes  ⣾  Refreshing fakes...", plain(c.footer()))
	assert.Equal(t, "Refreshing fakes...", c.load.label)
	assert.Equal(t, "listing fakes", c.load.errContext)
}

func TestListCore_UpdateItems(t *testing.T) {
	c := newSelectCore(t, &refreshCall{})

	c.updateItems([]string{"b", "missing"}, func(it *fakeItem) { it.Name = "changed" })

	assert.Equal(t, []fakeItem{fakeA, {"b", "changed"}}, c.items)
	assert.Contains(t, plain(c.render("")), "changed", "the rows follow")
}
