package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stretchr/testify/mock"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
	"github.com/juthrbog/lazycloud/internal/registry"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// Jump PR 6 (docs/design/jump.md, "Tag search (PR 6)").

// :go tag:... opens the tag search with the filter, with no read of its
// own: the list reads the resources.
func TestJumpTagsCommand(t *testing.T) {
	m := jumpModel(t)
	m, _ = m.executeCommand("go tag:team=orders tag:env")
	assert.Zero(t, m.ops.Count(), "no jump read")
	assert.Equal(t, "jump_tags:tag:team=orders tag:env", m.nav.Current().ID())

	// :tags opens the empty search, which asks for a filter: the app opens
	// the form over it. The answer goes back to the search, which loads in
	// place, so the stack does not grow.
	m = jumpModel(t)
	m, cmd := m.executeCommand("tags")
	navMsg := findOf[msg.NavigateMsg](collectMsgs(cmd))
	require.NotNil(t, navMsg)
	m, cmd = update(t, m, navMsg)
	search := m.nav.Current()
	assert.Equal(t, "jump_tags:", search.ID(), ":tags opens the empty tag search")
	depth := m.nav.Depth()
	form := findOf[msg.RequestFormMsg](collectMsgs(cmd))
	require.NotNil(t, form, "the search asks for a filter when it opens")
	m, _ = update(t, m, form)
	fv, ok := m.nav.Current().(*ui.FormView)
	require.True(t, ok, "the form is on top")

	m.svc.Tagging.(*awstest.MockTaggingService).On("GetResourcesPage", mock.Anything, mock.Anything, mock.Anything).
		Return(&aws.TaggedPage{}, nil).Maybe()
	m, _ = update(t, m, nav.Routed{Origin: fv, Msg: msg.FormResultMsg{ID: "jump_tags_filter", Values: map[string]string{"tags": "team=orders"}}})
	assert.Same(t, search, m.nav.Current(), "the same search is on top again")
	assert.Equal(t, depth, m.nav.Depth())
	assert.Equal(t, "jump_tags:team=orders", m.nav.Current().ID())
}

// Home has a Resources tile with Tag search, and :tags is a nav command.
func TestJumpTagsRegistry(t *testing.T) {
	cmd := registry.LookupCommand("tags")
	require.NotNil(t, cmd)
	assert.Equal(t, "jump_tags", cmd.ViewID)
	found := false
	for _, s := range registry.Services {
		for _, f := range s.Features {
			if f.ViewID == "jump_tags" {
				found = s.Name == "Resources" && f.Name == "Tag search"
			}
		}
	}
	assert.True(t, found)
}

// enter in the tag search sends a JumpMsg, which runs the jump as :go ARN
// does. A JumpMsg from a view that is not on top is dropped.
func TestJumpMsgRunsTheJump(t *testing.T) {
	m := jumpModel(t)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "jump_tags"})
	origin := m.nav.Current()
	m, _ = update(t, m, nav.Routed{Origin: origin, Msg: msg.JumpMsg{Text: "i-0abc1234"}})
	assert.Equal(t, 1, m.ops.Count(), "the read of the jump runs")
	assert.Contains(t, rendered(m), "Find EC2 instance i-0abc1234")
	m.ops.CancelAll()

	m = jumpModel(t)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "jump_tags"})
	origin = m.nav.Current()
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
	m, _ = update(t, m, nav.Routed{Origin: origin, Msg: msg.JumpMsg{Text: "i-0abc1234"}})
	assert.Zero(t, m.ops.Count(), "a late JumpMsg does not move the user")
}
