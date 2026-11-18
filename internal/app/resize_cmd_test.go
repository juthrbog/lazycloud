package app

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appmsg "github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
)

// resizedMsg is the message that cmdView returns for a resize.
type resizedMsg struct{ width, height int }

// cmdView returns a command for each resize.
type cmdView struct{ *rowView }

func (v cmdView) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	v.rowView.Update(m)
	if s, ok := m.(tea.WindowSizeMsg); ok {
		return v, func() tea.Msg { return resizedMsg{s.Width, s.Height} }
	}
	return v, nil
}

// collect runs cmd and every command in its batches, and returns the
// resizedMsg values, also the ones that the navigator routed.
func collect(cmd tea.Cmd) []resizedMsg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if r, ok := msg.(nav.Routed); ok {
		msg = r.Msg
	}
	switch m := msg.(type) {
	case resizedMsg:
		return []resizedMsg{m}
	case tea.BatchMsg:
		var out []resizedMsg
		for _, c := range m {
			out = append(out, collect(c)...)
		}
		return out
	}
	return nil
}

// L18: resizeViews dropped the commands that views return for a resize.
// Update now sends them.
func TestResizeSendsTheCommandsOfTheView(t *testing.T) {
	m := newTestModel(140, 40)
	m.pushView(cmdView{newRowView("")})

	next, cmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	assert.Equal(t, []resizedMsg{{118, 25}}, collect(cmd))
	assert.Empty(t, next.(Model).resizeCmds, "Update sends each command once")
}

// q pops the top view, and the view under it gets a resize. Its command
// is sent.
func TestResizeAfterAPopSendsTheCommandOfTheView(t *testing.T) {
	m := newTestModel(140, 40)
	m.pushView(cmdView{newRowView("")})
	m.pushView(cmdView{newRowView("")})

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	require.NotNil(t, cmd)

	assert.Equal(t, []resizedMsg{{138, 35}}, collect(cmd))
	assert.Equal(t, 2, next.(Model).nav.Depth())
}

// The update keeps its own command next to the resize commands. A started
// operation shows the operations line, which resizes the view, and the
// update also starts the operation and its tick.
func TestResizeCommandsKeepTheCommandOfTheUpdate(t *testing.T) {
	m := newTestModel(140, 40)
	v := cmdView{newRowView("")}
	m.pushView(v)
	// The operation returns at once, so that no command of the test waits.
	start := appmsg.StartOpMsg{Label: "Download a.txt", Run: func(context.Context) tea.Msg {
		return nil
	}}

	next, cmd := m.Update(nav.Routed{Origin: v, Msg: start})
	t.Cleanup(next.(Model).ops.CancelAll)
	require.NotNil(t, cmd)

	// Only the first command runs here. The others start the operation
	// and a timer.
	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok, "the resize command and the command of the update")
	require.Len(t, batch, 2)
	assert.Equal(t, []resizedMsg{{138, 34}}, collect(batch[0]))
	assert.NotNil(t, batch[1])
}
