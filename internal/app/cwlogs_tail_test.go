package app

import (
	"context"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
)

// App tests of live tail (docs/design/cwlogs.md, PR 6).

// tailSession is a live tail session whose updates the test sends.
type tailSession struct{ ch chan *aws.TailUpdate }

func (s *tailSession) Next(ctx context.Context) (*aws.TailUpdate, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case u := <-s.ch:
		return u, nil
	}
}

// startLiveTail opens the viewer of a group, presses F, and delivers the
// start reply. It returns the context of the session.
func startLiveTail(t *testing.T, m Model) (Model, context.Context) {
	t.Helper()
	logs := new(awstest.MockLogsService)
	m.svc.Logs = logs
	s := &tailSession{ch: make(chan *aws.TailUpdate, 4)}
	var mu sync.Mutex
	var ctx context.Context
	logs.On("StartLiveTail", mock.Anything, aws.TailQuery{GroupARN: "arn:g"}).
		Run(func(a mock.Arguments) {
			mu.Lock()
			defer mu.Unlock()
			ctx, _ = a.Get(0).(context.Context)
		}).Return(s, nil)

	// F waits for the first load, so it runs, with no events.
	logs.On("FilterEvents", mock.Anything, mock.Anything).Return(&aws.EventPage{End: true}, nil)
	m, cmd := update(t, m, msg.NavigateMsg{ViewID: "logs_events",
		Params: map[string]string{"log_group": "g", "log_group_arn": "arn:g"}})
	require.Equal(t, "logs_events:g:", m.nav.Current().ID())
	for _, r := range collectMsgs(cmd) {
		m, _ = update(t, m, r)
	}
	m, cmd = update(t, m, press("F"))
	started := collectMsgs(cmd) // the start reply, routed to the viewer
	require.Len(t, started, 1)
	m, _ = update(t, m, started[0])
	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, ctx)
	require.NoError(t, ctx.Err())
	return m, ctx
}

// A profile switch while live tail runs closes the stream: the context of
// the session ends. A late result of the old viewer is dropped: the app
// does not run Update of a view that left the stack.
func TestLiveTailStopsOnAProfileSwitch(t *testing.T) {
	awsConfigEnv(t, "prod")
	m := modelWithProfile("")
	m, ctx := startLiveTail(t, m)
	viewer := m.nav.Current()

	m, _ = m.applyProfile("prod")
	require.NoError(t, m.svcErr)
	require.ErrorIs(t, ctx.Err(), context.Canceled, "the stream closes")

	before := viewer.View().Content
	m, cmd := update(t, m, nav.Routed{Origin: viewer, Msg: tea.KeyPressMsg{Code: 'F', Text: "F"}})
	assert.Nil(t, cmd)
	assert.Equal(t, before, viewer.View().Content, "the old viewer gets no message")
	assert.False(t, m.nav.Contains(viewer))
}

// A nav command that covers the viewer stops live tail. When the user
// comes back, the status bar says so.
func TestLiveTailStopsWhenCovered(t *testing.T) {
	m := newTestModel(140, 40)
	mockAllServices(&m)
	m, ctx := startLiveTail(t, m)

	m, cmd := m.executeCommand("sqs")
	m, _ = update(t, m, findOf[msg.NavigateMsg](collectMsgs(cmd)))
	require.Equal(t, "sqs_queues", m.nav.Current().ID())
	require.ErrorIs(t, ctx.Err(), context.Canceled, "the cover stops live tail")
	assert.False(t, m.toasts.HasActive())

	m, cmd = update(t, m, msg.NavigateBackMsg{})
	require.Equal(t, "logs_events:g:", m.nav.Current().ID())
	toast := findOf[msg.ToastMsg](collectMsgs(cmd))
	require.NotNil(t, toast, "the way back says that live tail stopped")
	m, _ = update(t, m, toast)
	assert.Contains(t, rendered(m), "Live tail stopped. F starts it again.")
}

// The side panel does not cover the viewer, so live tail keeps running.
func TestLiveTailKeepsRunningUnderThePanel(t *testing.T) {
	m := newTestModel(160, 40)
	mockAllServices(&m)
	m, ctx := startLiveTail(t, m)
	m, _ = update(t, m, msg.TabbedContentMsg{PanelTitle: "event", NoFocus: true,
		Tabs: []msg.TabContent{{Title: "Message", Content: "hello", Format: "text"}}})
	require.True(t, m.panels.IsOpen())
	require.NoError(t, ctx.Err())
	assert.Contains(t, rendered(m), "LIVE")
}
