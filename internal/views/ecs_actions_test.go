package views

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
)

// Tests of the ECS write actions (docs/design/ecs.md, PR 7).

// newServicesWith builds the service list over svc with services loaded.
func newServicesWith(t *testing.T, svc *awstest.MockECSService, services ...aws.ClusterService) *ECSServices {
	t.Helper()
	svc.On("ListServicesPage", mock.Anything, testClusterARN, mock.Anything).Return(&aws.ServicePage{Services: services}, nil)
	v := NewECSServices(svc, testClusterARN)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	pump(v, v.Init(), 20)
	return v
}

func plainService() aws.ClusterService {
	s := testService
	s.CurrentDeployment = ""
	return s
}

func onlyToast(t *testing.T, msgs []tea.Msg) msg.ToastMsg {
	t.Helper()
	ts := toasts(msgs)
	require.Len(t, ts, 1)
	return ts[0]
}

func TestECSForceDeploy(t *testing.T) {
	setReadOnly(t, false)
	svc := new(awstest.MockECSService)
	svc.Test(t)
	v := newServicesWith(t, svc, plainService())

	_, cmd := v.Update(keyPress('f'))
	require.NotNil(t, cmd)
	req, ok := cmd().(msg.RequestConfirmMsg)
	require.True(t, ok)
	assert.True(t, req.Quick, "a y/n prompt")
	assert.Contains(t, req.Message, "Force a new deployment of web?")
	assert.Contains(t, req.Message, "Every task of the service is replaced")
	tag := req.Tag.(*ecsForceTag)
	assert.Equal(t, testService.ARN, tag.arn)

	// A no does nothing.
	_, cmd = v.Update(msg.ConfirmResultMsg{Tag: tag})
	assert.Nil(t, cmd)

	// A yes deploys, then shows the result and loads the list again.
	svc.On("ForceDeployment", mock.Anything, testClusterARN, testService.ARN).Return(nil).Once()
	_, cmd = v.Update(msg.ConfirmResultMsg{Confirmed: true, Tag: tag})
	msgs := pump(v, cmd, 20)
	assert.Equal(t, msg.ToastSuccess("Started a new deployment of web"), onlyToast(t, msgs))
	svc.AssertNumberOfCalls(t, "ListServicesPage", 2)
	assert.Zero(t, v.tasks, "the task ended")

	// A failure shows the AWS error.
	svc.On("ForceDeployment", mock.Anything, testClusterARN, testService.ARN).Return(errors.New("AccessDeniedException")).Once()
	_, cmd = v.Update(msg.ConfirmResultMsg{Confirmed: true, Tag: tag})
	ts := onlyToast(t, pump(v, cmd, 20))
	assert.Equal(t, msg.ToastError("").Level, ts.Level)
	assert.Contains(t, ts.Text, "Force deploy of web failed: AccessDeniedException")
}

// ReadOnly at the reply refuses the action, also when the mode changed
// while the prompt was open.
func TestECSActions_ReadOnlyAtTheReply(t *testing.T) {
	setReadOnly(t, true)
	svc := new(awstest.MockECSService)
	svc.Test(t)
	v := newServicesWith(t, svc, plainService())
	_, cmd := v.Update(msg.ConfirmResultMsg{Confirmed: true, Tag: &ecsForceTag{arn: testService.ARN, name: "web"}})
	assert.Equal(t, readOnlyText, onlyToast(t, collectMsgs(cmd)).Text)
	_, cmd = v.Update(msg.FormResultMsg{ID: ecsDesiredCountForm + testService.ARN, Values: map[string]string{"count": "2"}})
	assert.Equal(t, readOnlyText, onlyToast(t, collectMsgs(cmd)).Text)

	tv, _ := newTasks(t)
	_, cmd = tv.Update(msg.ConfirmResultMsg{Confirmed: true, Tag: &ecsStopTag{arns: []string{testTaskARN}}})
	assert.Equal(t, readOnlyText, onlyToast(t, collectMsgs(cmd)).Text)
	svc.AssertNotCalled(t, "ForceDeployment", mock.Anything, mock.Anything, mock.Anything)
	svc.AssertNotCalled(t, "SetDesiredCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// H9: a CODE_DEPLOY or EXTERNAL service does not take a forced deployment.
// A row that AWS could not describe gets no action.
func TestECSForceDeploy_Refused(t *testing.T) {
	for _, c := range []string{"CODE_DEPLOY", "EXTERNAL"} {
		s := plainService()
		s.Controller = c
		v := newServicesWith(t, new(awstest.MockECSService), s)
		_, cmd := v.Update(keyPress('f'))
		ts := onlyToast(t, collectMsgs(cmd))
		assert.Contains(t, ts.Text, "web uses the "+c+" deployment controller")
	}
	bad := aws.ClusterService{Name: "bad", ARN: "arn:bad", Err: "ACCESS_DENIED"}
	v := newServicesWith(t, new(awstest.MockECSService), bad)
	for _, k := range []rune{'f', 'c'} {
		_, cmd := v.Update(keyPress(k))
		assert.Contains(t, onlyToast(t, collectMsgs(cmd)).Text, "AWS could not describe bad")
	}
}

func TestECSDesiredCount(t *testing.T) {
	setReadOnly(t, false)
	svc := new(awstest.MockECSService)
	svc.Test(t)
	s := plainService()
	s.Running, s.Desired = 2, 3
	v := newServicesWith(t, svc, s)

	_, cmd := v.Update(keyPress('c'))
	require.NotNil(t, cmd)
	req, ok := cmd().(msg.RequestFormMsg)
	require.True(t, ok)
	assert.Equal(t, ecsDesiredCountForm+s.ARN, req.ID)
	require.Len(t, req.Fields, 1)
	f := req.Fields[0]
	assert.Equal(t, "3", f.Default)
	assert.Contains(t, f.Description, "Running 2, desired 3.")
	assert.NotContains(t, f.Description, "Express Mode")
	assert.True(t, f.Required)
	require.NotNil(t, f.Validate)
	require.Error(t, f.Validate("-1"), "the form checks the value")

	// An aborted form, or the form of another view, does nothing.
	_, cmd = v.Update(msg.FormResultMsg{ID: req.ID, Aborted: true, Values: map[string]string{"count": "2"}})
	assert.Nil(t, cmd)
	_, cmd = v.Update(msg.FormResultMsg{ID: "sqs_send_message", Values: map[string]string{"count": "1"}})
	assert.Nil(t, cmd)
	_, cmd = v.Update(msg.FormResultMsg{ID: req.ID, Values: map[string]string{"count": "x"}})
	assert.Nil(t, cmd, "a value the form did not validate does nothing")

	svc.On("SetDesiredCount", mock.Anything, testClusterARN, s.ARN, int32(0)).Return(nil).Once()
	_, cmd = v.Update(msg.FormResultMsg{ID: req.ID, Values: map[string]string{"count": " 0 "}})
	assert.Equal(t, msg.ToastSuccess("Set the desired count of web to 0"), onlyToast(t, pump(v, cmd, 20)))
	svc.AssertNumberOfCalls(t, "ListServicesPage", 2)

	svc.On("SetDesiredCount", mock.Anything, testClusterARN, s.ARN, int32(5)).Return(errors.New("ServiceNotActiveException")).Once()
	_, cmd = v.Update(msg.FormResultMsg{ID: req.ID, Values: map[string]string{"count": "5"}})
	assert.Contains(t, onlyToast(t, pump(v, cmd, 20)).Text, "Desired count of web failed: ServiceNotActiveException")

	// A service that the list no longer has is named by its ARN.
	gone := "arn:aws:ecs:us-east-1:1:service/prod-cluster/gone"
	svc.On("SetDesiredCount", mock.Anything, testClusterARN, gone, int32(1)).Return(nil).Once()
	_, cmd = v.Update(msg.FormResultMsg{ID: ecsDesiredCountForm + gone, Values: map[string]string{"count": "1"}})
	assert.Equal(t, "Set the desired count of gone to 1", onlyToast(t, pump(v, cmd, 20)).Text)

	managed := plainService()
	managed.Managed = true
	w := newServicesWith(t, new(awstest.MockECSService), managed)
	_, cmd = w.Update(keyPress('c'))
	assert.Contains(t, cmd().(msg.RequestFormMsg).Fields[0].Description, "Express Mode manages this service")
}

func TestValidateDesiredCount(t *testing.T) {
	for _, ok := range []string{"0", "1", " 7 ", "2147483647"} {
		require.NoError(t, validateDesiredCount(ok), ok)
	}
	for in, want := range map[string]string{
		"-1": "must be 0 or more", "1.5": "must be a whole number", "x": "must be a whole number", "": "must be a whole number",
		"2147483648": "must be at most 2147483647",
	} {
		err := validateDesiredCount(in)
		require.Error(t, err, in)
		assert.Equal(t, want, err.Error(), in)
	}
}

func TestECSStopTasks(t *testing.T) {
	setReadOnly(t, false)
	v, svc := newTasks(t)

	_, cmd := v.Update(keyPress('X'))
	require.NotNil(t, cmd)
	req, ok := cmd().(msg.RequestConfirmMsg)
	require.True(t, ok)
	assert.Equal(t, "stop 1 task", req.Phrase)
	assert.False(t, req.Quick)
	assert.Contains(t, req.Message, "Stop 1 task(s)?")
	assert.Contains(t, req.Message, "0123456789abcdef")
	assert.Contains(t, req.Message, `"Stopped by lazycloud"`)
	tag := req.Tag.(*ecsStopTag)
	assert.Equal(t, []string{testTaskARN}, tag.arns)

	_, cmd = v.Update(msg.ConfirmResultMsg{Tag: tag})
	assert.Nil(t, cmd, "a no does nothing")

	svc.On("StopTask", mock.Anything, testClusterARN, testTaskARN, "Stopped by lazycloud").Return(nil).Once()
	v.Update(keyPress(' '))
	require.Equal(t, 1, v.table.SelectionCount())
	_, cmd = v.Update(msg.ConfirmResultMsg{Confirmed: true, Tag: tag})
	assert.Equal(t, msg.ToastSuccess("Stopped 1 task(s)"), onlyToast(t, pump(v, cmd, 20)))
	assert.Equal(t, 0, v.table.SelectionCount(), "the selection goes")
	svc.AssertNumberOfCalls(t, "ListTasksPage", 2)
}

// X stops every selected task, goes on after a failure, and names the
// failed tasks.
func TestECSStopTasks_Selection(t *testing.T) {
	setReadOnly(t, false)
	useTaskClock(t)
	a, b, c := testTask, testTask, testTask
	a.ARN, a.ID = "arn:aws:ecs:us-east-1:1:task/prod-cluster/aaa", "aaa"
	b.ARN, b.ID = "arn:aws:ecs:us-east-1:1:task/prod-cluster/bbb", "bbb"
	c.ARN, c.ID = "arn:aws:ecs:us-east-1:1:task/prod-cluster/ccc", "ccc"
	svc := new(awstest.MockECSService)
	svc.Test(t)
	svc.On("ListTasksPage", mock.Anything, testClusterARN, "web", false, mock.Anything).Return(&aws.TaskPage{Tasks: []aws.Task{a, b, c}}, nil)
	v := NewECSTasks(svc, nil, testClusterARN, "web")
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	pump(v, v.Init(), 20)
	for range 3 {
		v.Update(keyPress(' '))
		v.Update(keyPress('j'))
	}
	require.Equal(t, 3, v.table.SelectionCount())
	_, cmd := v.Update(keyPress('X'))
	req := cmd().(msg.RequestConfirmMsg)
	assert.Equal(t, "stop 3 tasks", req.Phrase)
	assert.Contains(t, req.Message, "Stop 3 task(s)?")

	svc.On("StopTask", mock.Anything, testClusterARN, a.ARN, ecsStopReason).Return(nil)
	svc.On("StopTask", mock.Anything, testClusterARN, b.ARN, ecsStopReason).Return(errors.New("AccessDeniedException"))
	svc.On("StopTask", mock.Anything, testClusterARN, c.ARN, ecsStopReason).Return(nil)
	_, cmd = v.Update(msg.ConfirmResultMsg{Confirmed: true, Tag: req.Tag})
	ts := onlyToast(t, pump(v, cmd, 20))
	assert.Equal(t, msg.ToastError("Stopped 2 of 3 task(s). Failed: bbb (AccessDeniedException)"), ts)
	svc.AssertNumberOfCalls(t, "StopTask", 3)
}

// The stopped list has no o: a draining task can still show RUNNING
// there, and o has no hint, so ReadOnly would not block it.
func TestECSTasks_NoExecOnTheStoppedList(t *testing.T) {
	stubCLI(t, true, true)
	calls := recordExec(t)
	draining := execTask(execApp)
	draining.DesiredStatus = "STOPPED"
	svc := new(awstest.MockECSService)
	svc.On("ListTasksPage", mock.Anything, testClusterARN, "web", false, mock.Anything).Return(&aws.TaskPage{}, nil)
	svc.On("ListTasksPage", mock.Anything, testClusterARN, "web", true, mock.Anything).Return(&aws.TaskPage{Tasks: []aws.Task{draining}}, nil)
	v := NewECSTasks(svc, execClient, testClusterARN, "web")
	pump(v, v.Init(), 20)
	pump(v, func() tea.Msg { return keyPress('x') }, 20)
	require.True(t, v.stopped)
	require.Len(t, v.items, 1)
	_, cmd := v.Update(keyPress('o'))
	assert.Nil(t, cmd)
	assert.Empty(t, *calls)
}

// The stopped list has no X, and X there says why.
func TestECSStopTasks_StoppedList(t *testing.T) {
	v, _ := newTasks(t)
	v.Update(keyPress('x'))
	pump(v, nil, 1)
	assert.NotContains(t, hintKeys(v.KeyMap()), "X")
	assert.NotContains(t, hintKeys(v.KeyMap()), "o")
	_, cmd := v.Update(keyPress('X'))
	assert.Contains(t, onlyToast(t, collectMsgs(cmd)).Text, "stopped already")
}

// The keys need a row, no error screen and a closed filter.
func TestECSActions_KeysNeedARow(t *testing.T) {
	svc := new(awstest.MockECSService)
	svc.On("ListServicesPage", mock.Anything, testClusterARN, mock.Anything).Return(nil, errors.New("boom"))
	v := NewECSServices(svc, testClusterARN)
	pump(v, v.Init(), 20)
	for _, k := range []rune{'f', 'c'} {
		_, cmd := v.Update(keyPress(k))
		assert.Nil(t, cmd)
	}
	w := newServicesWith(t, new(awstest.MockECSService), plainService())
	w.Update(keyPress('/'))
	for _, k := range []rune{'f', 'c'} {
		_, cmd := w.Update(keyPress(k))
		for _, m := range collectMsgs(cmd) {
			switch m.(type) {
			case msg.RequestConfirmMsg, msg.RequestFormMsg:
				t.Errorf("the filter did not take %c", k)
			}
		}
	}

	tv := NewECSTasks(nil, nil, testClusterARN, "web")
	pump(tv, tv.Init(), 20)
	_, cmd := tv.Update(keyPress('X'))
	assert.Nil(t, cmd)
}

// Without a service (no session), an action fails with a message.
func TestECSCall_NoClient(t *testing.T) {
	require.ErrorIs(t, ecsCall(nil, func(aws.ECSService) error { return nil }), errNoClient)
	called := false
	require.NoError(t, ecsCall(new(awstest.MockECSService), func(aws.ECSService) error { called = true; return nil }))
	assert.True(t, called)
}
