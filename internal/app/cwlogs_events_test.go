package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/msg"
)

// App tests of the log event viewer (docs/design/cwlogs.md, PR 5).

// :logs NAME opens the events of that group. :logs alone opens the list.
func TestLogsCommandTakesAGroupName(t *testing.T) {
	m := newTestModel(140, 40)
	_, cmd := m.executeCommand("logs /aws/lambda/orders")
	assert.Equal(t, []any{msg.NavigateMsg{ViewID: "logs_events",
		Params: map[string]string{"log_group": "/aws/lambda/orders"}}}, toAny(collectMsgs(cmd)))

	_, cmd = m.executeCommand("log  /ecs/web ")
	assert.Equal(t, []any{msg.NavigateMsg{ViewID: "logs_events",
		Params: map[string]string{"log_group": "/ecs/web"}}}, toAny(collectMsgs(cmd)), "the alias takes it too")

	_, cmd = m.executeCommand("logs")
	assert.Equal(t, []any{msg.NavigateMsg{ViewID: "logs_groups"}}, toAny(collectMsgs(cmd)))

	m2, _ := m.executeCommand("sqs orders")
	assert.True(t, m2.toasts.HasActive(), "other commands still take no argument")
}

func toAny[T any](in []T) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

// The event detail opens without the focus, so the keys of the viewer
// still reach it: f opens the pattern input.
func TestLogEventDetailKeepsTheFocusOnTheViewer(t *testing.T) {
	m := newTestModel(160, 40)
	mockAllServices(&m)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "logs_events",
		Params: map[string]string{"log_group": "g", "log_group_arn": "arn"}})
	require.Equal(t, "logs_events:g:", m.nav.Current().ID())

	m, _ = update(t, m, msg.TabbedContentMsg{PanelTitle: "event", NoFocus: true,
		Tabs: []msg.TabContent{{Title: "Message", Content: "hello", Format: "text"}}})
	require.True(t, m.panels.IsOpen())
	assert.False(t, m.panels.Focused(), "the panel opens without the focus")

	m, _ = update(t, m, press("f"))
	assert.True(t, m.capturingInput(), "f reaches the viewer")

	// Other panels still take the focus.
	m2 := newTestModel(160, 40)
	m2, _ = update(t, m2, msg.TabbedContentMsg{PanelTitle: "x",
		Tabs: []msg.TabContent{{Title: "Info", Content: "x", Format: "text"}}})
	assert.True(t, m2.panels.Focused())
}
