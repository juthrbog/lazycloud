package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/msg"
)

// M10: a DLQ link from one queue to another targets the queue list that is
// already on top. It moves the cursor and must not push the view again.
func TestFocusLinkToCurrentViewDoesNotPush(t *testing.T) {
	m := newTestModel(140, 40)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "sqs_queues"})
	depth := m.nav.Depth()

	m, cmd := update(t, m, msg.NavigateMsg{ViewID: "sqs_queues", Params: map[string]string{"focus": "orders-dlq"}})
	assert.Equal(t, depth, m.nav.Depth(), "the view is not pushed a second time")
	f := findMsg(collectMsgs(cmd), "msg.FocusResourceMsg")
	require.NotNil(t, f)
	assert.Equal(t, "orders-dlq", f.(msg.FocusResourceMsg).ResourceID)
}

// A focus link to another view still pushes it.
func TestFocusLinkToOtherViewPushes(t *testing.T) {
	m := newTestModel(140, 40)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "ec2_list"})
	depth := m.nav.Depth()

	m, cmd := update(t, m, msg.NavigateMsg{ViewID: "sg_list", Params: map[string]string{"focus": "sg-1"}})
	assert.Equal(t, depth+1, m.nav.Depth())
	assert.Equal(t, "sg_list", m.nav.Current().ID())
	assert.NotNil(t, findMsg(collectMsgs(cmd), "msg.FocusResourceMsg"))
}
