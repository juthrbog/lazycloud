package app

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
	"github.com/juthrbog/lazycloud/internal/ui"
	"github.com/juthrbog/lazycloud/internal/views"
)

// answerView records the confirm answers it gets.
type answerView struct {
	*views.EventLog
	answers []ui.ConfirmResultMsg
}

func (v *answerView) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	if a, ok := m.(ui.ConfirmResultMsg); ok {
		v.answers = append(v.answers, a)
	}
	return v, nil
}

// A question that the app does not show counts as a no for its view, so
// the view forgets the action that waits for the answer.
func TestSkippedPromptIsANoForItsView(t *testing.T) {
	m := newTestModel(140, 40)
	v := &answerView{EventLog: views.NewEventLog()}
	m.pushView(v)
	m.confirm.ShowQuick("Quit?", confirmQuit)

	m, _ = update(t, m, nav.Routed{Origin: v, Msg: msg.RequestConfirmMsg{Message: "Replace it?", Action: "s3_overwrite:1"}})
	require.Len(t, v.answers, 1)
	assert.Equal(t, ui.ConfirmResultMsg{Confirmed: false, Action: "s3_overwrite:1"}, v.answers[0])
	assert.Equal(t, confirmQuit, m.confirm.Action())
}

// These tests cover M3: view contexts, background operations, and the
// prompts that protect running operations.

// startTestOp registers a background operation from the current view, as a
// view does with msg.StartOpMsg. The test does not run the operation.
func startTestOp(t *testing.T, m Model) Model {
	t.Helper()
	start := msg.StartOpMsg{Label: "Download a.txt", Run: func(ctx context.Context) tea.Msg {
		<-ctx.Done()
		return nil
	}}
	m, _ = update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: start})
	require.Equal(t, 1, m.ops.Count())
	return m
}

func TestLeavingAViewCancelsItsLoad(t *testing.T) {
	got := make(chan context.Context, 1)
	e := new(awstest.MockEC2Service)
	e.On("ListInstancesPage", mock.Anything, (*string)(nil)).Run(func(a mock.Arguments) {
		ctx := a.Get(0).(context.Context)
		got <- ctx
		<-ctx.Done()
	}).Return((*aws.InstancePage)(nil), context.Canceled)

	m := newTestModel(140, 40)
	m.svc.EC2 = e
	m, cmd := update(t, m, msg.NavigateMsg{ViewID: "ec2_list"})
	go collectMsgs(cmd) // the load blocks until its context ends

	var ctx context.Context
	select {
	case ctx = <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("the load did not start")
	}
	_, hasDeadline := ctx.Deadline()
	assert.True(t, hasDeadline, "a load has a time limit")
	require.NoError(t, ctx.Err())

	update(t, m, press("q"))
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("leaving the view did not cancel its load")
	}
	require.ErrorIs(t, ctx.Err(), context.Canceled, "cancelled by the pop, not by the time limit")
}

func TestLeavingAViewDoesNotCancelAMutation(t *testing.T) {
	ui.SetReadOnly(false)
	defer ui.SetReadOnly(true)
	var callErr error
	var hasDeadline bool
	e := new(awstest.MockEC2Service)
	e.On("ListInstancesPage", mock.Anything, (*string)(nil)).Return(&aws.InstancePage{
		Instances: []aws.Instance{{ID: "i-abc", State: "running"}},
	}, nil)
	e.On("StopInstances", mock.Anything, []string{"i-abc"}).Run(func(a mock.Arguments) {
		ctx := a.Get(0).(context.Context)
		callErr = ctx.Err()
		_, hasDeadline = ctx.Deadline()
	}).Return(nil)

	m := newTestModel(140, 40)
	m.svc.EC2 = e
	m, c := update(t, m, msg.NavigateMsg{ViewID: "ec2_list"})
	m, _ = update(t, m, findMsg(collectMsgs(c), "views.ec2PageLoadedMsg"))
	m, c = update(t, m, press("m"))
	m, _ = update(t, m, findMsg(collectMsgs(c), "msg.RequestActionPickerMsg"))
	m.picker = ui.NewPicker()
	m, c = update(t, m, ui.PickerResultMsg{ID: "action", Selected: 0, Value: "Stop"})
	m, _ = update(t, m, findMsg(collectMsgs(c), "msg.RequestConfirmMsg"))
	m.confirm = ui.NewConfirm()
	m, stopCmd := update(t, m, ui.ConfirmResultMsg{Confirmed: true, Action: "ec2_stop"})

	update(t, m, press("q")) // leave the view before the call runs
	require.NotNil(t, findMsg(collectMsgs(stopCmd), "views.ec2InstanceMutatedMsg"))
	require.NoError(t, callErr, "the mutation runs after the view is gone")
	assert.True(t, hasDeadline, "a mutation has a time limit")
}

func TestBackgroundOperationSurvivesLeavingAndSwitching(t *testing.T) {
	m := newTestModel(140, 40)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
	m = startTestOp(t, m)
	ctx := m.ops.Running()[0].Ctx

	m, _ = update(t, m, press("q"))
	require.NoError(t, ctx.Err(), "leaving the view keeps the operation")

	m, _ = m.applyRegion("eu-west-1")
	require.True(t, m.confirm.Visible(), "a switch with a running operation asks first")
	assert.Equal(t, confirmSessionSwitch, m.confirm.Action())
	assert.Contains(t, m.confirm.View(), "keeps running in the background")
	assert.NotEqual(t, "eu-west-1", m.activeRegion(), "nothing switches before the answer")

	m.confirm = ui.NewConfirm()
	m, _ = update(t, m, ui.ConfirmResultMsg{Confirmed: true, Action: confirmSessionSwitch})
	assert.Equal(t, "eu-west-1", m.activeRegion())
	require.NoError(t, ctx.Err(), "the operation keeps running after the switch")
	assert.Equal(t, 1, m.ops.Count())
}

func TestDeclinedSessionSwitchKeepsTheSession(t *testing.T) {
	m := newTestModel(140, 40)
	before := m.activeRegion()
	m = startTestOp(t, m)
	m, _ = m.applyRegion("eu-west-1")
	m.confirm = ui.NewConfirm()
	m, _ = update(t, m, ui.ConfirmResultMsg{Confirmed: false, Action: confirmSessionSwitch})
	assert.Equal(t, before, m.activeRegion())
	assert.Nil(t, m.pendingSession)
}

func TestSessionSwitchWithoutOperationsDoesNotAsk(t *testing.T) {
	m := newTestModel(140, 40)
	m, _ = m.applyRegion("eu-west-1")
	assert.False(t, m.confirm.Visible())
	assert.Equal(t, "eu-west-1", m.activeRegion())
}

func TestQuitWithARunningOperationAsks(t *testing.T) {
	m := newTestModel(140, 40)
	m = startTestOp(t, m)
	ctx := m.ops.Running()[0].Ctx

	m, cmd := update(t, m, press("q"))
	assert.Nil(t, cmd, "no quit before the answer")
	require.True(t, m.confirm.Visible())
	assert.Equal(t, confirmQuit, m.confirm.Action())
	require.NoError(t, ctx.Err())

	m, _ = update(t, m, press("n"))
	m, cmd = update(t, m, ui.ConfirmResultMsg{Confirmed: false, Action: confirmQuit})
	assert.Nil(t, cmd)
	require.NoError(t, ctx.Err(), "no keeps the operation")

	m, _ = update(t, m, keyCtrlC)
	require.True(t, m.confirm.Visible(), "ctrl+c asks too")
	_, cmd = update(t, m, ui.ConfirmResultMsg{Confirmed: true, Action: confirmQuit})
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
	require.ErrorIs(t, ctx.Err(), context.Canceled, "quitting cancels the operation")
}

func TestSecondCtrlCForcesTheQuit(t *testing.T) {
	m := newTestModel(140, 40)
	m = startTestOp(t, m)
	m, _ = update(t, m, keyCtrlC)
	require.True(t, m.confirm.Visible())
	_, cmd := update(t, m, keyCtrlC)
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
}

func TestQuitWithoutOperationsDoesNotAsk(t *testing.T) {
	m := newTestModel(140, 40)
	_, cmd := update(t, m, press("q"))
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
}

// A late prompt must not replace an open one: y meant to quit would then
// answer "replace the object?".
func TestLatePromptDoesNotReplaceAnOpenPrompt(t *testing.T) {
	m := newTestModel(140, 40)
	m = startTestOp(t, m)
	m, _ = update(t, m, press("q"))
	require.Equal(t, confirmQuit, m.confirm.Action())

	late := msg.RequestConfirmMsg{Message: "s3://b/k exists. Replace it?", Action: "s3_overwrite"}
	m, cmd := update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: late})
	assert.True(t, m.confirm.Visible())
	assert.Equal(t, confirmQuit, m.confirm.Action(), "the open prompt stays")
	require.NotNil(t, cmd)
	assert.True(t, m.toasts.HasActive(), "the user learns about the skipped prompt")
}

func TestPromptFromAViewNotOnTopIsSkippedWithAToast(t *testing.T) {
	m := newTestModel(140, 40)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
	below := m.nav.Current()
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "ec2_list"})

	m, _ = update(t, m, nav.Routed{Origin: below, Msg: msg.RequestConfirmMsg{Message: "Replace it?", Action: "s3_overwrite"}})
	assert.False(t, m.confirm.Visible())
	assert.True(t, m.toasts.HasActive())
}

func TestCancelledErrorIsNotShown(t *testing.T) {
	m := newTestModel(140, 40)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
	m, _ = update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: msg.ErrorMsg{Err: context.Canceled, Context: "listing"}})
	assert.Empty(t, m.visibleErr())
	m, _ = update(t, m, msg.ErrorMsg{Err: context.Canceled, Context: "listing"})
	assert.Empty(t, m.visibleErr())
}

func TestTimeoutErrorText(t *testing.T) {
	m := newTestModel(140, 40)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
	err := errors.Join(errors.New("operation error EC2: DescribeInstances"), context.DeadlineExceeded)
	m, _ = update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: msg.ErrorMsg{Err: err, Context: "listing EC2 instances"}})
	assert.Equal(t, "listing EC2 instances: no answer from AWS in time", m.visibleErr())
}

func TestOperationRegistersBeforeItRuns(t *testing.T) {
	m := newTestModel(140, 40)
	ran := make(chan struct{})
	start := msg.StartOpMsg{Label: "Copy a", Run: func(context.Context) tea.Msg {
		close(ran)
		return nil
	}}
	m, cmd := update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: start})
	assert.Equal(t, 1, m.ops.Count(), "registered on the event loop")
	assert.Equal(t, "Copy a", m.ops.Running()[0].Label)
	assert.Contains(t, m.ops.Running()[0].Session, "/")
	collectMsgs(cmd)
	<-ran
	assert.Equal(t, 0, m.ops.Count(), "the operation ends with its command")
}
