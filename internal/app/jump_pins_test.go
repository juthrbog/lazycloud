package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
)

// Pin tests for the jump design (docs/design/jump.md). They pin the hazards
// as they are today (TestJumpHazard_*). The PR that fixes a hazard flips its
// test. A guard (TestJumpGuard_*) pins a fact that stays. H2, H3 and H5 are
// pinned in internal/views, H7 and H8 in internal/ui, and H11 in
// internal/aws. H4 is pinned by TestLambdaFunctions_FocusMissing, which
// PR 2 flips. H9 comes with the Tagging API module in PR 6.

// H1, fixed in PR 3: :go and a bare ARN, ID or URL after : give "Unknown
// command".
func TestJumpHazard_NoJumpCommand(t *testing.T) {
	for _, input := range []string{
		"go i-0123456789abcdef0",
		"jump arn:aws:lambda:us-east-1:1:function:orders",
		"arn:aws:lambda:us-east-1:1:function:orders",
		"i-0123456789abcdef0",
		"https://sqs.us-east-1.amazonaws.com/1/orders",
	} {
		m := newTestModel(200, 40)
		m, cmd := m.executeCommand(input)
		assert.Contains(t, rendered(m), "Unknown command: "+input, input)
		assert.Nil(t, findOf[msg.NavigateMsg](collectMsgs(cmd)), input)
		assert.Equal(t, 1, m.nav.Depth(), input)
	}
}

// H6, a guard: a plain session switch reopens only the root list of the top
// view, with no focus. This stays true for R and P. PR 4 adds a test that a
// switch with a jump follow-up sends the follow-up in place of the root.
func TestJumpGuard_PlainSwitchReopensTheRoot(t *testing.T) {
	awsConfigEnv(t)
	fakeIdentity(t, aws.Identity{}, nil)
	m := newTestModel(140, 40)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "lambda_functions", Params: map[string]string{"focus": "orders-api"}})
	require.Equal(t, "lambda_functions", m.nav.Current().ID())

	m, cmd := m.applyRegion("eu-west-1")
	assert.Equal(t, 1, m.nav.Depth(), "the switch drops the stack")
	nav := findOf[msg.NavigateMsg](collectMsgs(cmd))
	require.NotNil(t, nav)
	assert.Equal(t, msg.NavigateMsg{ViewID: "lambda_functions"}, nav, "the root comes back with no parameters and no focus")
}

// H10, fixed in PR 2: a view whose ID has parameters never takes the
// same-view shortcut, so a second focus link to it pushes a second copy.
func TestJumpHazard_SecondFocusLinkPushesASecondCopy(t *testing.T) {
	m := newTestModel(140, 40)
	mockAllServices(&m)
	link := msg.NavigateMsg{ViewID: "ecs_task_definitions", Params: map[string]string{
		"family": "web", "focus": "arn:aws:ecs:us-east-1:1:task-definition/web:7",
	}}
	m, _ = update(t, m, link)
	depth := m.nav.Depth()
	require.Equal(t, "ecs_task_definitions:web", m.nav.Current().ID())

	m, _ = update(t, m, link)
	assert.Equal(t, depth+1, m.nav.Depth(), "the same family is on the stack twice")
	assert.Equal(t, "ecs_task_definitions:web", m.nav.Current().ID())
}
