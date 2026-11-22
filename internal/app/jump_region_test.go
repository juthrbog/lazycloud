package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
)

// Jump PR 4 (docs/design/jump.md, "Another Region"): the read runs in the
// Region of the resource, a prompt asks before the switch, and the switch
// opens the resource in place of the root view (H6).

const otherFn = "arn:aws:lambda:eu-west-1:" + jumpAcct + ":function:orders"

// otherRegion makes the jump reads of eu-west-1 use a mock Lambda service,
// and returns it with the Regions that the reads asked for.
func otherRegion(t *testing.T) (*awstest.MockLambdaService, *[]string) {
	t.Helper()
	lambda := new(awstest.MockLambdaService)
	lambda.Test(t)
	var asked []string
	before := regionServices
	regionServices = func(_ aws.Services, region string) aws.Services {
		asked = append(asked, region)
		return aws.Services{Lambda: lambda}
	}
	t.Cleanup(func() { regionServices = before })
	return lambda, &asked
}

// regionModel is a test model whose session can switch: an isolated AWS
// config and a fake STS, as in the H6 guard.
func regionModel(t *testing.T) Model {
	t.Helper()
	awsConfigEnv(t)
	fakeIdentity(t, aws.Identity{Account: jumpAcct}, nil)
	m := jumpModel(t)
	m.svc.Lambda.(*awstest.MockLambdaService).Test(t) // the session Region gets no read
	return m
}

// readResult runs the jump to otherFn and returns the result of its read.
func readResult(t *testing.T, m Model) (Model, jumpResultMsg) {
	t.Helper()
	m, cmd := m.executeCommand(otherFn)
	r := findOf[jumpResultMsg](collectMsgs(cmd))
	require.NotNil(t, r, "the read ran")
	return m, r.(jumpResultMsg)
}

// confirmYes answers y on the prompt and returns the commands of the reply.
func confirmYes(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	require.True(t, m.confirm.Visible())
	m, cmd := update(t, m, press("y"))
	require.NotNil(t, cmd)
	return update(t, m, cmd())
}

func TestJumpToAnotherRegionAsksAndSwitches(t *testing.T) {
	lambda, asked := otherRegion(t)
	lambda.On("GetFunction", mock.Anything, "orders", "").
		Return(&aws.FunctionDetail{Function: aws.Function{Name: "orders", ARN: otherFn}}, nil)
	m := regionModel(t)
	// A plain switch would reopen this root list (the H6 guard).
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "sqs_queues"})
	require.Equal(t, "sqs_queues", m.topLevelViewID())

	m, r := readResult(t, m)
	assert.Equal(t, []string{"eu-west-1"}, *asked, "the read runs in the Region of the resource")
	assert.Equal(t, "eu-west-1", r.region)

	m, _ = update(t, m, r)
	assert.Contains(t, rendered(m), "The resource is in eu-west-1. Switch to eu-west-1 and open it?")
	assert.Equal(t, "us-east-1", m.activeRegion(), "no switch before the answer")

	m, cmd := confirmYes(t, m)
	assert.Equal(t, "eu-west-1", m.activeRegion())
	assert.Equal(t, 1, m.nav.Depth(), "the switch drops the stack")
	var navs []msg.NavigateMsg
	for _, x := range collectMsgs(cmd) {
		if n, ok := x.(msg.NavigateMsg); ok {
			navs = append(navs, n)
		}
	}
	assert.Equal(t, []msg.NavigateMsg{{ViewID: "lambda_functions", Params: map[string]string{"focus": otherFn}}}, navs,
		"the resource opens in place of the root view")
}

// No at the prompt keeps the session and the stack.
func TestJumpToAnotherRegionNo(t *testing.T) {
	lambda, _ := otherRegion(t)
	lambda.On("GetFunction", mock.Anything, "orders", "").
		Return(&aws.FunctionDetail{Function: aws.Function{Name: "orders", ARN: otherFn}}, nil)
	m := regionModel(t)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
	m, r := readResult(t, m)
	m, _ = update(t, m, r)
	require.True(t, m.confirm.Visible())
	m, cmd := update(t, m, press("n"))
	require.NotNil(t, cmd)
	m, cmd = update(t, m, cmd())
	assert.Nil(t, findOf[msg.NavigateMsg](collectMsgs(cmd)))
	assert.False(t, m.confirm.Visible())
	assert.Equal(t, "us-east-1", m.activeRegion())
	assert.Equal(t, 2, m.nav.Depth())
	assert.Equal(t, "eventlog", m.nav.Current().ID())
}

// A yes that comes after the session changed, for example after a sign-in
// to another profile while the prompt was open, does nothing: it must not
// switch back to the old profile.
func TestJumpToAnotherRegionLateYes(t *testing.T) {
	lambda, _ := otherRegion(t)
	lambda.On("GetFunction", mock.Anything, "orders", "").
		Return(&aws.FunctionDetail{Function: aws.Function{Name: "orders", ARN: otherFn}}, nil)
	awsConfigEnv(t, "prod")
	fakeIdentity(t, aws.Identity{Account: jumpAcct}, nil)
	m := jumpModel(t)
	m, r := readResult(t, m)
	m, _ = update(t, m, r)
	require.True(t, m.confirm.Visible())

	m, _ = m.switchSession(sessionChange{profile: "prod", region: "us-east-1", kind: "Profile", value: "prod"})
	require.Equal(t, "prod", m.config.AWS.Profile)
	m, cmd := confirmYes(t, m)
	assert.Nil(t, findOf[msg.NavigateMsg](collectMsgs(cmd)))
	assert.Equal(t, "prod", m.config.AWS.Profile, "the sign-in stays")
	assert.Equal(t, "us-east-1", m.activeRegion())
	assert.Contains(t, rendered(m), "The session changed, so the switch did not run.")
}

// The same holds for the second prompt, which asks when an operation runs.
func TestJumpToAnotherRegionLateYesWhileAnOperationRuns(t *testing.T) {
	awsConfigEnv(t, "prod")
	fakeIdentity(t, aws.Identity{Account: jumpAcct}, nil)
	m := jumpModel(t)
	m = startTestOp(t, m)
	t.Cleanup(m.ops.CancelAll)
	nav := msg.NavigateMsg{ViewID: "lambda_functions", Params: map[string]string{"focus": otherFn}}
	m, _ = m.askRegionSwitch("eu-west-1", nav)
	m, _ = confirmYes(t, m)
	require.Contains(t, rendered(m), "Switch the region to eu-west-1?")

	m, _ = m.switchSession(sessionChange{profile: "prod", region: "us-east-1", kind: "Profile", value: "prod"})
	m, cmd := confirmYes(t, m)
	assert.Nil(t, findOf[msg.NavigateMsg](collectMsgs(cmd)))
	assert.Equal(t, "prod", m.config.AWS.Profile)
	assert.Equal(t, "us-east-1", m.activeRegion())
}

// A miss in the other Region names that Region, asks nothing and keeps the
// session and the stack.
func TestJumpToAnotherRegionMiss(t *testing.T) {
	lambda, _ := otherRegion(t)
	lambda.On("GetFunction", mock.Anything, "orders", "").Return(nil, apiErr("ResourceNotFoundException", "Function not found"))
	m := regionModel(t)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
	m, r := readResult(t, m)
	assert.Equal(t, "No Lambda function named orders in eu-west-1.", r.text)
	m, _ = update(t, m, r)
	assert.False(t, m.confirm.Visible(), "no prompt")
	assert.Contains(t, rendered(m), "No Lambda function named orders in eu-west-1.")
	assert.Equal(t, "us-east-1", m.activeRegion())
	assert.Equal(t, 2, m.nav.Depth(), "the stack stays")
}

// When an operation runs, the switch asks again, as R does. The resource
// waits for that answer.
func TestJumpToAnotherRegionWaitsForARunningOperation(t *testing.T) {
	lambda, _ := otherRegion(t)
	lambda.On("GetFunction", mock.Anything, "orders", "").
		Return(&aws.FunctionDetail{Function: aws.Function{Name: "orders", ARN: otherFn}}, nil)
	m := regionModel(t)
	m, r := readResult(t, m)
	m = startTestOp(t, m)
	t.Cleanup(m.ops.CancelAll)
	m, _ = update(t, m, r)

	m, cmd := confirmYes(t, m)
	assert.Nil(t, findOf[msg.NavigateMsg](collectMsgs(cmd)), "nothing opens yet")
	assert.Contains(t, rendered(m), "Switch the region to eu-west-1?")
	assert.Equal(t, "us-east-1", m.activeRegion())

	m, cmd = confirmYes(t, m)
	assert.Equal(t, "eu-west-1", m.activeRegion())
	nav := findOf[msg.NavigateMsg](collectMsgs(cmd))
	require.NotNil(t, nav)
	assert.Equal(t, msg.NavigateMsg{ViewID: "lambda_functions", Params: map[string]string{"focus": otherFn}}, nav)
}

// When the switch fails, the resource does not open and the session stays.
func TestJumpToAnotherRegionFailedSwitch(t *testing.T) {
	m := regionModel(t)
	m.config.AWS.Profile = "no-such-profile" // NewServices fails for it
	nav := msg.NavigateMsg{ViewID: "lambda_functions", Params: map[string]string{"focus": otherFn}}
	m, _ = m.askRegionSwitch("eu-west-1", nav)
	m, cmd := confirmYes(t, m)
	assert.Nil(t, findOf[msg.NavigateMsg](collectMsgs(cmd)))
	assert.Contains(t, rendered(m), "Cannot switch region to eu-west-1")
	assert.Equal(t, "us-east-1", m.activeRegion())
}

// The services for another Region keep the profile and the endpoint of the
// session, change the Region, and refuse every write.
func TestServicesForRegion(t *testing.T) {
	awsConfigEnv(t, "dev")
	m := modelWithProfile("dev")
	require.NotNil(t, m.svc.Client)
	before := m.svc.Client.Config.Region
	require.NotEqual(t, "eu-west-1", before)
	s := m.svc.ForRegion("eu-west-1")
	assert.Equal(t, "eu-west-1", s.Client.Region)
	assert.Equal(t, "eu-west-1", s.Client.Config.Region)
	assert.Equal(t, "http://127.0.0.1:1", s.Client.Endpoint)
	assert.Equal(t, "dev", s.Client.Profile)
	assert.Equal(t, before, m.svc.Client.Config.Region, "the session client stays")
	require.ErrorIs(t, s.EC2.StopInstance(t.Context(), "i-1"), aws.ErrReadOnly)
}
