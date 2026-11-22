package views

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
)

// Tests of ECS Exec on o (docs/design/ecs.md, PR 6).

// execCall is one call of the exec command.
type execCall struct{ cluster, task, container string }

// recordExec replaces the exec command and records its calls.
func recordExec(t *testing.T) *[]execCall {
	t.Helper()
	var calls []execCall
	ecsExecCommand = func(c *aws.Client, cluster, task, container, label string) *aws.CLIExec {
		calls = append(calls, execCall{cluster, task, container})
		return c.ECSExecCmd(cluster, task, container, label)
	}
	t.Cleanup(func() { ecsExecCommand = (*aws.Client).ECSExecCmd })
	return &calls
}

// Each path sends the cluster ARN, the task ARN and the container name.
func TestECSExec_CommandGetsTheNames(t *testing.T) {
	stubCLI(t, true, true)
	calls := recordExec(t)
	want := execCall{testClusterARN, testTaskARN, "app"}

	ecsExecTask(execClient, testClusterARN, execTask(execApp))
	cmd := ecsExecTask(execClient, testClusterARN, execTask(execApp, aws.Container{Name: "init"}))
	tag := cmd().(msg.RequestActionPickerMsg).Tag.(*ecsExecTag)
	onExecPicked(execClient, tag, msg.PickerResultMsg{Outcome: msg.Picked, Value: "app"})

	task := execTask(execApp)
	v, _, _ := newContainers(t, &task, testTaskDef, nil)
	v.client = execClient
	v.Update(keyPress('o'))

	assert.Equal(t, []execCall{want, want, want}, *calls)
}

// A reload that fails keeps the old rows behind the error screen. o must
// not act on them.
func TestECSExec_ErrorScreenWithOldRows(t *testing.T) {
	stubCLI(t, true, true)
	calls := recordExec(t)

	svc := new(awstest.MockECSService)
	svc.On("ListTasksPage", mock.Anything, testClusterARN, "web", false, mock.Anything).Return(&aws.TaskPage{Tasks: []aws.Task{execTask(execApp)}}, nil).Once()
	svc.On("ListTasksPage", mock.Anything, testClusterARN, "web", false, mock.Anything).Return(nil, errors.New("ThrottlingException"))
	v := NewECSTasks(svc, execClient, testClusterARN, "web")
	pump(v, v.Init(), 20)
	require.Len(t, v.items, 1)
	pump(v, func() tea.Msg { return keyPress('r') }, 20)
	require.Error(t, v.err)
	require.Len(t, v.items, 1, "the old row stays")
	_, cmd := v.Update(keyPress('o'))
	assert.Nil(t, cmd)

	task := execTask(execApp)
	c := new(awstest.MockECSService)
	c.On("DescribeTask", mock.Anything, testClusterARN, testTaskARN).Return(&task, nil).Once()
	c.On("DescribeTask", mock.Anything, testClusterARN, testTaskARN).Return(nil, fmt.Errorf("%w: x", aws.ErrTaskNotFound))
	c.On("DescribeTaskDefinition", mock.Anything, mock.Anything).Return(testTaskDef, nil)
	w := NewECSContainers(c, execClient, testClusterARN, testTaskARN, "us-east-1")
	pump(w, w.Init(), 20)
	require.Len(t, w.items, 1)
	pump(w, func() tea.Msg { return keyPress('r') }, 20)
	require.ErrorIs(t, w.err, errTaskGone)
	require.Len(t, w.items, 1, "the old row stays")
	_, cmd = w.Update(keyPress('o'))
	assert.Nil(t, cmd)
	assert.Empty(t, *calls)
}

var execClient = &aws.Client{Region: "us-east-1"}

// stubCLI sets the checks for the AWS CLI and the plugin.
func stubCLI(t *testing.T, cli, plugin bool) {
	t.Helper()
	ecsCLIAvailable = func() bool { return cli }
	ecsPluginAvailable = func() bool { return plugin }
	t.Cleanup(func() {
		ecsCLIAvailable, ecsPluginAvailable = aws.AWSCLIAvailable, aws.SSMPluginAvailable
	})
}

var execAgent = aws.ManagedAgent{Name: "ExecuteCommandAgent", Status: "RUNNING"}

func execTask(containers ...aws.Container) aws.Task {
	t := testTask
	t.Containers = containers
	return t
}

var execApp = aws.Container{Name: "app", LastStatus: "RUNNING", Agents: []aws.ManagedAgent{execAgent}}

// isExec reports whether cmd starts a tea.Exec session (and nothing else).
func isExec(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	return strings.Contains(fmt.Sprintf("%T", cmd()), "exec")
}

func TestECSTaskExecProblem(t *testing.T) {
	stubCLI(t, true, true)
	assert.Empty(t, ecsTaskExecProblem(execClient, testTask))
	assert.Equal(t, "AWS client not available — check the profile", ecsTaskExecProblem(nil, testTask))

	stopped := testTask
	stopped.LastStatus = "STOPPED"
	assert.Equal(t, "The task is STOPPED. Exec needs a running task.", ecsTaskExecProblem(execClient, stopped))
	stopped.LastStatus = ""
	assert.Equal(t, "The task is not running. Exec needs a running task.", ecsTaskExecProblem(execClient, stopped))

	off := testTask
	off.ExecEnabled = false
	assert.Equal(t, "Exec is off for this task. Turn it on for the service, then start new tasks.", ecsTaskExecProblem(execClient, off))

	stubCLI(t, false, true)
	assert.Equal(t, "aws CLI not found — install it first", ecsTaskExecProblem(execClient, testTask))
	stubCLI(t, true, false)
	assert.Equal(t, "session-manager-plugin not found — install it first", ecsTaskExecProblem(execClient, testTask))
}

func TestECSContainerExecProblem(t *testing.T) {
	assert.Empty(t, ecsContainerExecProblem(execApp))
	assert.Empty(t, ecsContainerExecProblem(aws.Container{Name: "app"}), "no status and no agents: AWS decides")

	stopped := execApp
	stopped.LastStatus = "STOPPED"
	assert.Equal(t, "The container app is STOPPED. Exec needs a running container.", ecsContainerExecProblem(stopped))

	other := aws.Container{Name: "app", LastStatus: "RUNNING", Agents: []aws.ManagedAgent{{Name: "SomeOtherAgent", Status: "RUNNING"}}}
	assert.Equal(t, "The container app has no exec agent.", ecsContainerExecProblem(other))

	pending := aws.Container{Name: "app", LastStatus: "RUNNING", Agents: []aws.ManagedAgent{{Name: "ExecuteCommandAgent", Status: "PENDING", Reason: "starting"}}}
	assert.Equal(t, "The exec agent of app is PENDING: starting.", ecsContainerExecProblem(pending))
	pending.Agents[0].Status, pending.Agents[0].Reason = "", ""
	assert.Equal(t, "The exec agent of app is not running.", ecsContainerExecProblem(pending))
}

func TestECSExecTask(t *testing.T) {
	stubCLI(t, true, true)
	assert.True(t, isExec(ecsExecTask(execClient, testClusterARN, execTask(execApp))), "one container: no picker")

	ts := toasts(collectMsgs(ecsExecTask(execClient, testClusterARN, execTask())))
	require.Len(t, ts, 1)
	assert.Equal(t, "The task has no containers.", ts[0].Text)

	off := execTask(execApp, aws.Container{Name: "init"})
	off.ExecEnabled = false
	ts = toasts(collectMsgs(ecsExecTask(execClient, testClusterARN, off)))
	require.Len(t, ts, 1)
	assert.Contains(t, ts[0].Text, "Exec is off", "the task checks run before the picker")

	// Two containers: a picker, then the chosen one.
	stopped := aws.Container{Name: "init", LastStatus: "STOPPED"}
	task := execTask(execApp, stopped)
	cmd := ecsExecTask(execClient, testClusterARN, task)
	require.NotNil(t, cmd)
	req, ok := cmd().(msg.RequestActionPickerMsg)
	require.True(t, ok)
	assert.Equal(t, []string{"app", "init"}, req.Options)
	tag, ok := req.Tag.(*ecsExecTag)
	require.True(t, ok)
	assert.Equal(t, testClusterARN, tag.cluster)

	assert.True(t, isExec(onExecPicked(execClient, tag, msg.PickerResultMsg{Outcome: msg.Picked, Value: "app"})))
	ts = toasts(collectMsgs(onExecPicked(execClient, tag, msg.PickerResultMsg{Outcome: msg.Picked, Value: "init"})))
	require.Len(t, ts, 1)
	assert.Contains(t, ts[0].Text, "The container init is STOPPED")
	assert.Nil(t, onExecPicked(execClient, tag, msg.PickerResultMsg{}), "a cancel does nothing")
	assert.Nil(t, onExecPicked(execClient, tag, msg.PickerResultMsg{Outcome: msg.Picked, Value: "gone"}))
}

func TestECSExecFinished(t *testing.T) {
	ts := toasts(collectMsgs(onExecFinished(ecsExecFinishedMsg{label: "app (t1)"})))
	require.Len(t, ts, 1)
	assert.Equal(t, msg.ToastInfo("Exec session ended: app (t1)"), ts[0])

	ts = toasts(collectMsgs(onExecFinished(ecsExecFinishedMsg{label: "app (t1)", err: errors.New("TargetNotConnectedException")})))
	require.Len(t, ts, 1)
	assert.Equal(t, msg.ToastError("").Level, ts[0].Level)
	assert.Contains(t, ts[0].Text, "Exec session failed: TargetNotConnectedException")

	// Ctrl+C (exit code 130) is not a failure.
	err := exec.CommandContext(t.Context(), "sh", "-c", "exit 130").Run()
	require.Error(t, err)
	ts = toasts(collectMsgs(onExecFinished(ecsExecFinishedMsg{label: "app (t1)", err: err})))
	require.Len(t, ts, 1)
	assert.Contains(t, ts[0].Text, "Exec session ended")
}

func TestECSTasks_ExecKey(t *testing.T) {
	stubCLI(t, true, true)
	v, _ := newTasks(t)
	v.client = execClient
	// testTask has two containers, so o asks for one.
	_, cmd := v.Update(keyPress('o'))
	require.NotNil(t, cmd)
	req, ok := cmd().(msg.RequestActionPickerMsg)
	require.True(t, ok)
	tag := req.Tag.(*ecsExecTag)
	assert.Equal(t, testTaskARN, tag.task.ARN)

	// The reply of the picker goes to the view. init is stopped.
	_, cmd = v.Update(msg.PickerResultMsg{Outcome: msg.Picked, Value: "init", Tag: tag})
	ts := toasts(collectMsgs(cmd))
	require.Len(t, ts, 1)
	assert.Contains(t, ts[0].Text, "init is STOPPED")
	// The reply of another picker does not.
	_, cmd = v.Update(msg.PickerResultMsg{Outcome: msg.Picked, Value: "init", Tag: &logsRangeTag{}})
	assert.Empty(t, toasts(collectMsgs(cmd)))

	_, cmd = v.Update(ecsExecFinishedMsg{label: "app (t1)"})
	ts = toasts(collectMsgs(cmd))
	require.Len(t, ts, 1)
	assert.Contains(t, ts[0].Text, "Exec session ended")

	// The filter takes o.
	v.Update(keyPress('/'))
	_, cmd = v.Update(keyPress('o'))
	for _, m := range collectMsgs(cmd) {
		_, isPicker := m.(msg.RequestActionPickerMsg)
		assert.False(t, isPicker, "the filter takes o")
	}
}

func TestECSTasks_ExecKeyOnTheErrorScreen(t *testing.T) {
	stubCLI(t, true, true)
	v := NewECSTasks(nil, execClient, testClusterARN, "web")
	pump(v, v.Init(), 20)
	require.Error(t, v.err)
	_, cmd := v.Update(keyPress('o'))
	assert.Nil(t, cmd)
}

func TestECSContainers_ExecKey(t *testing.T) {
	stubCLI(t, true, true)
	task := execTask(execApp, aws.Container{Name: "init", LastStatus: "STOPPED"})
	v, _, _ := newContainers(t, &task, testTaskDef, nil)
	v.client = execClient
	_, cmd := v.Update(keyPress('o'))
	assert.True(t, isExec(cmd), "o on a container opens the shell at once")

	v.Update(keyPress('j'))
	_, cmd = v.Update(keyPress('o'))
	ts := toasts(collectMsgs(cmd))
	require.Len(t, ts, 1)
	assert.Contains(t, ts[0].Text, "init is STOPPED")

	_, cmd = v.Update(ecsExecFinishedMsg{label: "app (t1)", err: errors.New("boom")})
	ts = toasts(collectMsgs(cmd))
	require.Len(t, ts, 1)
	assert.Contains(t, ts[0].Text, "Exec session failed: boom")

	// Without a client (no session), o says so.
	v.client = nil
	v.Update(keyPress('k'))
	_, cmd = v.Update(keyPress('o'))
	ts = toasts(collectMsgs(cmd))
	require.Len(t, ts, 1)
	assert.Contains(t, ts[0].Text, "AWS client not available")
}
