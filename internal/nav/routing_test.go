package nav

import (
	"context"
	"os/exec"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/ui"
)

// recView is a pointer view that records the messages it receives and
// replies to each one with a command that returns reply.
type recView struct {
	id    string
	got   []tea.Msg
	reply tea.Msg
}

func (v *recView) ID() string               { return v.id }
func (v *recView) Title() string            { return v.id }
func (v *recView) KeyMap() []ui.HintBinding { return nil }
func (v *recView) Footer() string           { return "" }
func (v *recView) Init() tea.Cmd            { return func() tea.Msg { return "init:" + v.id } }
func (v *recView) View() tea.View           { return tea.NewView("") }
func (v *recView) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	v.got = append(v.got, m)
	if v.reply == nil {
		return v, nil
	}
	reply := v.reply
	return v, func() tea.Msg { return reply }
}

type dataMsg struct{ n int }

// ctxView records the context the navigator gives it.
type ctxView struct {
	recView
	ctx       context.Context
	setBefore bool // SetContext ran before Init
}

func (v *ctxView) SetContext(ctx context.Context) { v.ctx = ctx }
func (v *ctxView) Init() tea.Cmd {
	v.setBefore = v.ctx != nil
	return nil
}

func TestPushSetsContextBeforeInit(t *testing.T) {
	root, a := &ctxView{recView: recView{id: "root"}}, &ctxView{recView: recView{id: "a"}}
	n := New(root)
	require.NotNil(t, root.ctx, "the root view gets a context too")
	n.Push(a)
	assert.True(t, a.setBefore)
	require.NoError(t, a.ctx.Err())
}

func TestPopCancelsTheViewContext(t *testing.T) {
	root := &ctxView{recView: recView{id: "root"}}
	a, b, c := &ctxView{recView: recView{id: "a"}}, &ctxView{recView: recView{id: "b"}}, &ctxView{recView: recView{id: "c"}}
	n := New(root)
	n.Push(a)
	n.Push(b)
	n.Push(c)

	n.Pop()
	require.ErrorIs(t, c.ctx.Err(), context.Canceled)
	require.NoError(t, b.ctx.Err())

	n.PopTo(1)
	require.ErrorIs(t, b.ctx.Err(), context.Canceled)
	require.ErrorIs(t, a.ctx.Err(), context.Canceled)
	require.NoError(t, root.ctx.Err(), "the root stays")

	n.Pop()
	require.NoError(t, root.ctx.Err(), "popping at the root does nothing")
}

func TestCloseCancelsEveryContext(t *testing.T) {
	root, a := &ctxView{recView: recView{id: "root"}}, &ctxView{recView: recView{id: "a"}}
	n := New(root)
	n.Push(a)
	n.Close()
	require.ErrorIs(t, root.ctx.Err(), context.Canceled)
	require.ErrorIs(t, a.ctx.Err(), context.Canceled)
}

func TestPushSameIDBuildsNewView(t *testing.T) {
	n := New(&recView{id: "root"})
	first := &ctxView{recView: recView{id: "page"}}
	n.Push(first)
	n.Pop()

	second := &ctxView{recView: recView{id: "page"}}
	n.Push(second)
	assert.True(t, n.IsCurrent(second), "the new instance is pushed, not the popped one")
	require.ErrorIs(t, first.ctx.Err(), context.Canceled)
	require.NoError(t, second.ctx.Err())
}

// swapCtxView returns a new instance from Update and records its context.
type swapCtxView struct{ ctxView }

func (v *swapCtxView) Update(tea.Msg) (tea.Model, tea.Cmd) {
	return &swapCtxView{ctxView{recView: recView{id: v.id}}}, nil
}

func TestSwappedInstanceKeepsTheContext(t *testing.T) {
	old := &swapCtxView{ctxView{recView: recView{id: "a"}}}
	n := New(&recView{id: "root"})
	n.Push(old)
	n.UpdateCurrent(dataMsg{1})

	next, ok := n.Current().(*swapCtxView)
	require.True(t, ok)
	require.NotSame(t, old, next)
	assert.Same(t, old.ctx, next.ctx, "the new instance gets the same context")
	n.Pop()
	require.ErrorIs(t, next.ctx.Err(), context.Canceled)
}

func TestTagNil(t *testing.T) {
	assert.Nil(t, Tag(&recView{id: "a"}, nil))
}

func TestTagWrapsResultWithOrigin(t *testing.T) {
	a := &recView{id: "a"}
	got := Tag(a, func() tea.Msg { return dataMsg{1} })()

	r, ok := got.(Routed)
	require.True(t, ok, "expected Routed, got %T", got)
	assert.Same(t, a, r.Origin)
	assert.Equal(t, dataMsg{1}, r.Msg)
}

func TestTagNilResult(t *testing.T) {
	assert.Nil(t, Tag(&recView{id: "a"}, func() tea.Msg { return nil })())
}

// Each command inside a batch must be tagged, so that results produced by
// tea.Batch(spinner.Tick(), fetch()) still find their view.
func TestTagBatchTagsEachCommand(t *testing.T) {
	a := &recView{id: "a"}
	batch := tea.Batch(
		func() tea.Msg { return dataMsg{1} },
		func() tea.Msg { return dataMsg{2} },
	)
	got := Tag(a, batch)()

	b, ok := got.(tea.BatchMsg)
	require.True(t, ok, "a batch must stay a batch for the runtime, got %T", got)
	require.Len(t, b, 2)
	for i, c := range b {
		r, ok := c().(Routed)
		require.True(t, ok)
		assert.Same(t, a, r.Origin)
		assert.Equal(t, dataMsg{i + 1}, r.Msg)
	}
}

// The runtime must see its own messages, or exec (SSM sessions), clipboard
// writes, and quit stop working.
func TestTagPassesRuntimeMessagesThrough(t *testing.T) {
	a := &recView{id: "a"}
	for name, cmd := range map[string]tea.Cmd{
		"quit":      tea.Quit,
		"clipboard": tea.SetClipboard("i-123"),
		"exec":      tea.ExecProcess(exec.CommandContext(t.Context(), "true"), nil), // SSM Connect; returning the msg does not run it
	} {
		got := Tag(a, cmd)()
		_, routed := got.(Routed)
		assert.False(t, routed, "%s message must not be wrapped (got %T)", name, got)
	}
}

func TestTagDoesNotWrapTwice(t *testing.T) {
	a, b := &recView{id: "a"}, &recView{id: "b"}
	got := Tag(b, Tag(a, func() tea.Msg { return dataMsg{1} }))()
	r := got.(Routed)
	assert.Same(t, a, r.Origin, "the innermost origin wins")
	assert.Equal(t, dataMsg{1}, r.Msg)
}

func TestContainsAndIsCurrent(t *testing.T) {
	root, a := &recView{id: "root"}, &recView{id: "a"}
	n := New(root)
	n.Push(a)
	assert.True(t, n.IsCurrent(a))
	assert.True(t, n.Contains(a))

	n.Pop()
	assert.False(t, n.IsCurrent(a))
	assert.False(t, n.Contains(a), "a popped view is gone")

	// Same ID, different instance (for example after a session switch).
	other := New(&recView{id: "root"})
	assert.False(t, other.Contains(a))
	assert.False(t, other.Contains(root))
}

func TestPushTagsInit(t *testing.T) {
	a := &recView{id: "a"}
	n := New(&recView{id: "root"})
	r, ok := n.Push(a)().(Routed)
	require.True(t, ok)
	assert.Same(t, a, r.Origin)
	assert.Equal(t, "init:a", r.Msg)
}

func TestUpdateViewReachesHiddenView(t *testing.T) {
	a := &recView{id: "a", reply: dataMsg{2}}
	b := &recView{id: "b"}
	n := New(&recView{id: "root"})
	n.Push(a)
	n.Push(b)

	cmd := n.UpdateView(a, dataMsg{1})
	assert.Equal(t, []tea.Msg{dataMsg{1}}, a.got)
	assert.Empty(t, b.got)
	r := cmd().(Routed)
	assert.Same(t, a, r.Origin, "the reply is tagged with the hidden view")
}

func TestUpdateViewIgnoresForeignView(t *testing.T) {
	stale := &recView{id: "a"}
	n := New(&recView{id: "root"})
	n.Push(&recView{id: "a"}) // same ID, new instance

	assert.Nil(t, n.UpdateView(stale, dataMsg{1}))
	assert.Empty(t, stale.got)
}

// swapView returns a new instance from Update, like a value-style model.
type swapView struct{ recView }

func (v *swapView) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	next := &swapView{recView{id: v.id, got: append(v.got, m)}}
	return next, func() tea.Msg { return dataMsg{9} }
}

func TestUpdateViewReplacesSwappedInstance(t *testing.T) {
	old := &swapView{recView{id: "a"}}
	n := New(&recView{id: "root"})
	n.Push(old)
	n.Push(&recView{id: "b"})

	r := n.UpdateView(old, dataMsg{1})().(Routed)
	assert.NotSame(t, old, r.Origin, "replies come from the new instance")
	assert.True(t, n.Contains(r.Origin))
	assert.False(t, n.Contains(old))

	n.Pop()
	assert.True(t, n.IsCurrent(r.Origin), "the stack holds the new instance")
}

func TestUpdateCurrentTagsCommand(t *testing.T) {
	a := &recView{id: "a", reply: dataMsg{3}}
	n := New(a)
	r := n.UpdateCurrent(dataMsg{1})().(Routed)
	assert.Same(t, a, r.Origin)
	assert.Equal(t, dataMsg{3}, r.Msg)
}
