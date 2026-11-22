package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
	"github.com/juthrbog/lazycloud/internal/registry"
)

// Jump PR 6 (docs/design/jump.md, "Tag search (PR 6)").

// :go tag:... opens the tag search with the filter, with no read of its
// own: the list reads the resources.
func TestJumpTagsCommand(t *testing.T) {
	m := jumpModel(t)
	m, _ = m.executeCommand("go tag:team=orders tag:env")
	assert.Zero(t, m.ops.Count(), "no jump read")
	assert.Equal(t, "jump_tags:tag:team=orders tag:env", m.nav.Current().ID())

	m = jumpModel(t)
	m, cmd := m.executeCommand("tags")
	nav := findOf[msg.NavigateMsg](collectMsgs(cmd))
	require.NotNil(t, nav)
	m, _ = update(t, m, nav)
	assert.Equal(t, "jump_tags:", m.nav.Current().ID(), ":tags lists every tagged resource")
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
