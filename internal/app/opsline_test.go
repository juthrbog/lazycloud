package app

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
	"github.com/juthrbog/lazycloud/internal/ops"
	"github.com/juthrbog/lazycloud/internal/ui"
	"github.com/juthrbog/lazycloud/internal/views"
)

// These tests cover the progress and cancel part of M25: the operations
// line, its tick, and ctrl+x.

var keyCtrlX = tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}

func TestProgressText(t *testing.T) {
	start := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	now := start.Add(65 * time.Second)
	cases := []struct {
		name string
		op   ops.Op
		want string
	}{
		{"no progress shows the time", ops.Op{Started: start}, "1m5s"},
		{"bytes", ops.Op{Progress: ops.Progress{Done: 4 << 20, Total: 10 << 20, Bytes: true}}, "4.0 MB / 10.0 MB (40%)"},
		{"items", ops.Op{Progress: ops.Progress{Done: 250, Total: 1000}}, "250 / 1000 (25%)"},
		{"no total", ops.Op{Progress: ops.Progress{Done: 3 << 10, Bytes: true}}, "3.0 KB"},
		{"cancelling", ops.Op{Canceling: true, Progress: ops.Progress{Done: 1, Total: 2}}, "cancelling..."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, progressText(c.op, now))
		})
	}
}

func TestOpsLine(t *testing.T) {
	now := time.Now()
	running := []ops.Op{
		{Label: "Download a.zip", Session: "prod/eu-west-1", Started: now, Progress: ops.Progress{Done: 1, Total: 4}},
		{Label: "Delete 3 buckets", Session: "default/us-east-1", Started: now},
	}

	assert.Empty(t, renderOpsLine(nil, "default/us-east-1", 120, now), "no line without operations")

	line := ansi.Strip(renderOpsLine(running, "default/us-east-1", 120, now))
	assert.Contains(t, line, "Download a.zip 1 / 4 (25%) [prod/eu-west-1]", "names a session that is not the current one")
	assert.Contains(t, line, "+1 more")
	assert.Contains(t, line, "ctrl+x cancel")
	assert.Equal(t, 120, lipgloss.Width(line))

	narrow := ansi.Strip(renderOpsLine(running, "prod/eu-west-1", 30, now))
	assert.Equal(t, 30, lipgloss.Width(narrow), "a long label is cut, the line keeps its width")
	assert.Contains(t, narrow, "ctrl+x cancel")
	assert.Contains(t, narrow, "…")
}

func TestOpsLineShowsInTheView(t *testing.T) {
	m := newTestModel(140, 40)
	assert.NotContains(t, ansi.Strip(m.View().Content), "ctrl+x cancel")

	m = startTestOp(t, m)
	ops.Report(m.ops.Running()[0].Ctx, ops.Progress{Done: 5, Total: 10})
	view := ansi.Strip(m.View().Content)
	assert.Contains(t, view, "Download a.txt 5 / 10 (50%)")
	assert.Contains(t, view, "ctrl+x cancel")
	assert.Equal(t, 40, lipgloss.Height(view), "the line takes its room from the content")
}

func TestOpsTickRunsWhileOperationsRun(t *testing.T) {
	m := newTestModel(140, 40)
	start := msg.StartOpMsg{Label: "Download a.txt", Run: func(ctx context.Context) tea.Msg {
		<-ctx.Done()
		return nil
	}}
	m, cmd := update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: start})
	require.True(t, m.opsTicking, "the first operation starts the tick")
	go collectMsgs(cmd)

	m, cmd = update(t, m, opsTickMsg{})
	require.NotNil(t, cmd, "the tick goes on while an operation runs")

	m.ops.Cancel(m.ops.Running()[0].ID)
	require.Eventually(t, func() bool { return m.ops.Count() == 0 }, 2*time.Second, 5*time.Millisecond)
	m, cmd = update(t, m, opsTickMsg{})
	assert.Nil(t, cmd, "the tick stops when no operation runs")
	assert.False(t, m.opsTicking)
}

func TestCtrlXCancelsThePickedOperation(t *testing.T) {
	m := newTestModel(140, 40)
	var ctxs []context.Context
	for _, label := range []string{"Download a.txt", "Download b.txt"} {
		got := make(chan context.Context, 1)
		start := msg.StartOpMsg{Label: label, Run: func(ctx context.Context) tea.Msg {
			got <- ctx
			<-ctx.Done()
			return nil
		}}
		var cmd tea.Cmd
		m, cmd = update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: start})
		go collectMsgs(cmd)
		ctxs = append(ctxs, <-got)
	}
	defer m.ops.CancelAll()

	m, _ = update(t, m, keyCtrlX)
	require.True(t, m.picker.Visible(), "ctrl+x asks which operation to cancel")
	assert.Contains(t, ansi.Strip(m.picker.View()), "Download a.txt")
	assert.Contains(t, ansi.Strip(m.picker.View()), "Download b.txt")

	// The second option is the newer operation.
	m, cmd := update(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	assert.Nil(t, cmd)
	m, cmd = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	res := findMsg(collectMsgs(cmd), "ui.PickerResultMsg")
	require.NotNil(t, res)
	m, _ = update(t, m, res)

	assert.True(t, ops.CanceledByUser(ctxs[1]), "the picked operation is cancelled")
	require.NoError(t, ctxs[0].Err(), "the other one goes on")
	assert.True(t, m.toasts.HasActive())
}

func TestCtrlXWithoutOperations(t *testing.T) {
	m := newTestModel(140, 40)
	m, _ = update(t, m, keyCtrlX)
	assert.False(t, m.picker.Visible())
	assert.True(t, m.toasts.HasActive(), "a toast says that nothing runs")
}

func TestEscKeepsTheOperation(t *testing.T) {
	m := newTestModel(140, 40)
	m = startTestOp(t, m)
	m, _ = update(t, m, keyCtrlX)
	m, _ = update(t, m, ui.PickerResultMsg{ID: cancelPickerID, Selected: -1})
	require.NoError(t, m.ops.Running()[0].Ctx.Err())
	m.ops.CancelAll()
}

// sizeView records the heights that the app gives it.
type sizeView struct {
	*views.EventLog
	heights []int
}

func (v *sizeView) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	if s, ok := m.(tea.WindowSizeMsg); ok {
		v.heights = append(v.heights, s.Height)
	}
	return v, nil
}

// The operations line takes a row from the view. The view must learn it,
// or the box cuts its last row (a table row the cursor can move onto).
func TestOpsLineResizesTheView(t *testing.T) {
	m := newTestModel(140, 40)
	v := &sizeView{EventLog: views.NewEventLog()}
	m.pushView(v)
	m.resizeViews()
	full := v.heights[len(v.heights)-1]

	start := msg.StartOpMsg{Label: "Download a.txt", Run: func(ctx context.Context) tea.Msg {
		<-ctx.Done()
		return nil
	}}
	m, cmd := update(t, m, nav.Routed{Origin: v, Msg: start})
	assert.Equal(t, full-1, v.heights[len(v.heights)-1], "the line takes one row")

	go collectMsgs(cmd)
	m.ops.Cancel(m.ops.Running()[0].ID)
	require.Eventually(t, func() bool { return m.ops.Count() == 0 }, 2*time.Second, 5*time.Millisecond)
	m, _ = update(t, m, nav.Routed{Origin: v, Msg: msg.ToastInfo("done")})
	assert.Equal(t, full, v.heights[len(v.heights)-1], "the row comes back when the last operation ends")
	assert.NotContains(t, ansi.Strip(m.View().Content), "ctrl+x cancel")
}
