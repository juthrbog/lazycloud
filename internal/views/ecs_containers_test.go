package views

import (
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// Tests of the ECS container list and the jump to the logs
// (docs/design/ecs.md, PR 4).

var exit137 = int32(137)

// awslogs is the log configuration of the seeded web container.
var awslogs = &aws.LogConfig{Driver: "awslogs", Options: map[string]string{
	"awslogs-group": "/ecs/web-app", "awslogs-region": "us-east-1", "awslogs-stream-prefix": "ecs",
}}

var testTaskDef = &aws.TaskDefinition{
	ARN: testTask.TaskDefinition, Family: "web", Revision: 7,
	Containers: []aws.ContainerDefinition{
		{Name: "app", Log: awslogs},
		{Name: "init", Log: &aws.LogConfig{Driver: "awsfirelens"}},
	},
}

// testContainers are the rows of testTask with testTaskDef.
func testContainers() []ecsContainer {
	task := testTask
	return []ecsContainer{
		{Container: aws.Container{Name: "app", ARN: "arn:container/app", Image: "nginx:alpine", LastStatus: "RUNNING", Health: "HEALTHY"}, Task: &task, Log: awslogs},
		{Container: aws.Container{Name: "init", ARN: "arn:container/init", Image: "busybox", LastStatus: "STOPPED", ExitCode: &exitZero, Reason: "done"},
			Task: &task, Log: &aws.LogConfig{Driver: "awsfirelens"}},
	}
}

func newContainers(t *testing.T, task *aws.Task, def *aws.TaskDefinition, defErr error) (*ECSContainers, *awstest.MockECSService, []tea.Msg) {
	t.Helper()
	useTaskClock(t)
	svc := new(awstest.MockECSService)
	svc.Test(t)
	svc.On("DescribeTask", mock.Anything, testClusterARN, testTaskARN).Return(task, nil)
	if task.TaskDefinition != "" {
		svc.On("DescribeTaskDefinition", mock.Anything, task.TaskDefinition).Return(def, defErr)
	}
	v := NewECSContainers(svc, testClusterARN, testTaskARN, "us-east-1")
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	msgs := pump(v, v.Init(), 20)
	return v, svc, msgs
}

func withContainers(cs ...aws.Container) *aws.Task {
	task := testTask
	task.Containers = cs
	return &task
}

func TestECSContainers_RowsJoinTheLogConfig(t *testing.T) {
	v, svc, msgs := newContainers(t, &testTask, testTaskDef, nil)
	assert.Empty(t, toasts(msgs))
	svc.AssertNumberOfCalls(t, "DescribeTaskDefinition", 1)
	require.Len(t, v.items, 2)
	assert.Same(t, awslogs, v.items[0].Log)
	assert.Equal(t, "awsfirelens", v.items[1].Log.Driver)
	assert.Equal(t, "0123456789abcdef containers", v.Title())
	assert.Equal(t, "ecs_containers:"+testTaskARN, v.ID())

	out := ansi.Strip(v.View().Content)
	for _, s := range []string{"app", "nginx:alpine", "RUNNING", "HEALTHY", "/ecs/web-app", "init", "busybox", "STOPPED", "done", "awsfirelens"} {
		assert.Contains(t, out, s)
	}
	assert.Contains(t, v.View().Content, ui.StateColor("RUNNING"), "the Status column has its style")
	assert.Contains(t, v.View().Content, ui.HealthColor("HEALTHY"), "the Health column has its style")
	assert.Equal(t, "2/2 containers", v.Footer())

	// r repeats both calls.
	pump(v, func() tea.Msg { return keyPress('r') }, 20)
	svc.AssertNumberOfCalls(t, "DescribeTask", 2)
	svc.AssertNumberOfCalls(t, "DescribeTaskDefinition", 2)
}

func TestECSContainers_ContainerMissingFromTheDefinition(t *testing.T) {
	task := withContainers(aws.Container{Name: "sidecar", ARN: "arn:container/sidecar", LastStatus: "RUNNING"})
	v, _, _ := newContainers(t, task, testTaskDef, nil)
	require.Len(t, v.items, 1)
	assert.Nil(t, v.items[0].Log)
	assert.Empty(t, v.logsCell(v.items[0]))
}

// The task has no task definition ARN: no second call, and no logs.
func TestECSContainers_NoTaskDefinition(t *testing.T) {
	task := withContainers(aws.Container{Name: "app", ARN: "arn:container/app"})
	task.TaskDefinition = ""
	v, svc, _ := newContainers(t, task, nil, nil)
	svc.AssertNotCalled(t, "DescribeTaskDefinition", mock.Anything, mock.Anything)
	require.Len(t, v.items, 1)
	assert.Nil(t, v.items[0].Log)
	assert.NoError(t, v.items[0].LogErr)
}

// H5: when the task definition call fails, the containers still show. The
// Logs column shows ?, one toast shows the error, and enter shows it too.
func TestECSContainers_TaskDefinitionFails(t *testing.T) {
	v, _, msgs := newContainers(t, &testTask, nil, errors.New("AccessDeniedException: no DescribeTaskDefinition"))
	require.Len(t, v.items, 2)
	ts := toasts(msgs)
	require.Len(t, ts, 1)
	assert.Equal(t, msg.ToastError("").Level, ts[0].Level)
	assert.Contains(t, ts[0].Text, "Could not read the task definition web:7: AccessDeniedException: no DescribeTaskDefinition. The Logs column shows ?.")
	assert.Equal(t, "?", v.logsCell(v.items[0]))
	require.NoError(t, v.err, "the list stays")

	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	ts = toasts(collectMsgs(cmd))
	require.Len(t, ts, 1)
	assert.Equal(t, "Could not read the task definition: AccessDeniedException: no DescribeTaskDefinition", ts[0].Text)

	d := ecsContainerDetail(v.items[0])
	assert.Contains(t, d.Tabs[len(d.Tabs)-1].Content, "Could not read the task definition")
}

func TestECSContainers_TaskIsGone(t *testing.T) {
	useTaskClock(t)
	svc := new(awstest.MockECSService)
	svc.Test(t)
	svc.On("DescribeTask", mock.Anything, testClusterARN, testTaskARN).Return(nil, fmt.Errorf("%w: x", aws.ErrTaskNotFound))
	v := NewECSContainers(svc, testClusterARN, testTaskARN, "us-east-1")
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	pump(v, v.Init(), 20)
	assert.Contains(t, ansi.Strip(v.View().Content), "The task is gone. AWS keeps stopped tasks for about an hour.")
	svc.AssertNotCalled(t, "DescribeTaskDefinition", mock.Anything, mock.Anything)
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Nil(t, cmd, "enter does nothing on the error screen")

	// Another error stays as it is.
	other := new(awstest.MockECSService)
	other.On("DescribeTask", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("ThrottlingException"))
	w := NewECSContainers(other, testClusterARN, testTaskARN, "us-east-1")
	w.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	pump(w, w.Init(), 20)
	out := ansi.Strip(w.View().Content)
	assert.Contains(t, out, "ThrottlingException")
	assert.NotContains(t, out, "gone")
}

func TestECSContainers_LogsCell(t *testing.T) {
	v := NewECSContainers(nil, testClusterARN, testTaskARN, "us-east-1")
	west := &aws.LogConfig{Driver: "awslogs", Options: map[string]string{"awslogs-group": "/ecs/x", "awslogs-region": "eu-west-1"}}
	noRegion := &aws.LogConfig{Driver: "awslogs", Options: map[string]string{"awslogs-group": "/ecs/x"}}
	for _, tc := range []struct {
		c    ecsContainer
		want string
	}{
		{ecsContainer{Log: awslogs}, "/ecs/web-app"},
		{ecsContainer{Log: west}, "/ecs/x (eu-west-1)"},
		{ecsContainer{Log: noRegion}, "/ecs/x"},
		{ecsContainer{Log: &aws.LogConfig{Driver: "splunk"}}, "splunk"},
		{ecsContainer{}, ""},
		{ecsContainer{Log: awslogs, LogErr: errors.New("denied")}, "?"},
	} {
		assert.Equal(t, tc.want, v.logsCell(tc.c))
	}
	// Without a session Region, no Region shows.
	assert.Equal(t, "/ecs/x", NewECSContainers(nil, "c", "t", "").logsCell(ecsContainer{Log: west}))
}

// jumpParams runs ecsLogJump and returns the route parameters and toasts.
func jumpParams(t *testing.T, c ecsContainer, region string) (map[string]string, []msg.ToastMsg) {
	t.Helper()
	msgs := collectMsgs(ecsLogJump(c, region))
	var params map[string]string
	for _, m := range msgs {
		if n, ok := m.(msg.NavigateMsg); ok {
			assert.Equal(t, "logs_events", n.ViewID)
			params = n.Params
		}
	}
	return params, toasts(msgs)
}

func ms(t time.Time) string { return strconv.FormatInt(t.UnixMilli(), 10) }

func TestECSLogJump_Running(t *testing.T) {
	useTaskClock(t)
	task := testTask
	task.PullStartedAt = task.CreatedAt.Add(30 * time.Second)
	// LocalStack sets StoppedAt on a running task; the branch ignores it.
	task.StoppedAt = taskNow.Add(-time.Hour)
	p, ts := jumpParams(t, ecsContainer{Container: aws.Container{Name: "app"}, Task: &task, Log: awslogs}, "us-east-1")
	assert.Empty(t, ts)
	assert.Equal(t, map[string]string{
		"log_group":           "/ecs/web-app",
		"log_stream":          "ecs/app/0123456789abcdef",
		"stream_first":        ms(task.CreatedAt.Add(-time.Minute)),
		"missing_hint":        "The task definition sends the logs to this group, but the group does not exist.",
		"missing_stream_hint": "The task writes the stream when its container starts.",
	}, p)
}

// The start is the earliest of the pull, create and start times.
func TestECSLogStart(t *testing.T) {
	base := taskNow.Add(-time.Hour)
	for name, tc := range map[string]struct {
		task aws.Task
		want time.Time
	}{
		"pull first":    {aws.Task{PullStartedAt: base, CreatedAt: base.Add(time.Second), StartedAt: base.Add(2 * time.Second)}, base},
		"created first": {aws.Task{PullStartedAt: base.Add(time.Second), CreatedAt: base, StartedAt: base.Add(2 * time.Second)}, base},
		"started only":  {aws.Task{StartedAt: base}, base},
		"started first": {aws.Task{CreatedAt: base.Add(time.Second), StartedAt: base}, base},
	} {
		assert.Equal(t, tc.want.Add(-time.Minute), ecsLogStart(&tc.task), name)
	}
	assert.True(t, ecsLogStart(&aws.Task{}).IsZero())
}

func TestECSLogJump_Stopped(t *testing.T) {
	useTaskClock(t)
	task := stoppedTask
	task.CreatedAt = task.StartedAt.Add(-time.Minute)
	c := ecsContainer{Container: aws.Container{Name: "app"}, Task: &task, Log: awslogs}
	p, _ := jumpParams(t, c, "us-east-1")
	assert.Equal(t, ms(task.CreatedAt.Add(-time.Minute)), p["log_start"])
	assert.Equal(t, ms(task.StoppedAt.Add(time.Minute)), p["log_end"])
	assert.NotContains(t, p, "stream_first")
	assert.Equal(t, "ecs/app/fedcba9876543210", p["log_stream"])

	// The stop time falls back to ExecutionStoppedAt and StoppingAt.
	task.StoppedAt = time.Time{}
	task.ExecutionStoppedAt = taskNow.Add(-20 * time.Minute)
	p, _ = jumpParams(t, c, "us-east-1")
	assert.Equal(t, ms(task.ExecutionStoppedAt.Add(time.Minute)), p["log_end"])

	// No stop time: only stream_first.
	task.ExecutionStoppedAt = time.Time{}
	p, _ = jumpParams(t, c, "us-east-1")
	assert.NotContains(t, p, "log_start")
	assert.NotContains(t, p, "log_end")
	assert.Equal(t, ms(task.CreatedAt.Add(-time.Minute)), p["stream_first"])

	// A stop time with no start time: no range.
	p, _ = jumpParams(t, ecsContainer{Container: aws.Container{Name: "app"}, Task: &aws.Task{ID: "t", LastStatus: "STOPPED", StoppedAt: taskNow}, Log: awslogs}, "us-east-1")
	for _, k := range []string{"log_start", "log_end", "stream_first"} {
		assert.NotContains(t, p, k)
	}

	// No time at all: no range, the viewer shows the last hour.
	p, _ = jumpParams(t, ecsContainer{Container: aws.Container{Name: "app"}, Task: &aws.Task{ID: "t", LastStatus: "STOPPED"}, Log: awslogs}, "us-east-1")
	for _, k := range []string{"log_start", "log_end", "stream_first"} {
		assert.NotContains(t, p, k)
	}
	assert.Equal(t, "ecs/app/t", p["log_stream"])
}

// A stopping task reads as running: its stream can still grow.
func TestECSLogJump_Stopping(t *testing.T) {
	useTaskClock(t)
	task := stoppedTask
	task.LastStatus = "DEPROVISIONING"
	p, _ := jumpParams(t, ecsContainer{Container: aws.Container{Name: "app"}, Task: &task, Log: awslogs}, "us-east-1")
	assert.Equal(t, ms(task.StartedAt.Add(-time.Minute)), p["stream_first"])
	assert.NotContains(t, p, "log_end")
}

// Without a prefix (EC2 only), the whole group opens with the range, and a
// toast says why.
func TestECSLogJump_NoPrefix(t *testing.T) {
	useTaskClock(t)
	noPrefix := &aws.LogConfig{Driver: "awslogs", Options: map[string]string{"awslogs-group": "/ecs/ec2", "awslogs-region": "us-east-1"}}
	p, ts := jumpParams(t, ecsContainer{Container: aws.Container{Name: "app"}, Task: &testTask, Log: noPrefix}, "us-east-1")
	require.NotNil(t, p)
	assert.Equal(t, "/ecs/ec2", p["log_group"])
	assert.NotContains(t, p, "log_stream")
	assert.NotContains(t, p, "missing_stream_hint")
	assert.Equal(t, ms(testTask.CreatedAt.Add(-time.Minute)), p["stream_first"])
	require.Len(t, ts, 1)
	assert.Contains(t, ts[0].Text, "Docker container ID")
}

// H5: the cases with no stream show a toast and do not open the viewer.
func TestECSLogJump_NoStream(t *testing.T) {
	useTaskClock(t)
	west := &aws.LogConfig{Driver: "awslogs", Options: map[string]string{"awslogs-group": "/ecs/x", "awslogs-region": "eu-west-1", "awslogs-stream-prefix": "ecs"}}
	for _, tc := range []struct {
		c    ecsContainer
		want string
	}{
		{ecsContainer{Log: &aws.LogConfig{Driver: "splunk"}}, "The container sends its logs with splunk, not to CloudWatch Logs."},
		{ecsContainer{Log: west}, "The logs are in eu-west-1. Press R to switch the Region."},
		{ecsContainer{}, "The container has no log configuration."},
		{ecsContainer{Log: &aws.LogConfig{Driver: "awslogs"}}, "The log configuration of the container names no log group."},
		{ecsContainer{Log: awslogs, LogErr: errors.New("denied")}, "Could not read the task definition: denied"},
	} {
		tc.c.Name, tc.c.Task = "app", &testTask
		p, ts := jumpParams(t, tc.c, "us-east-1")
		assert.Nil(t, p, tc.want)
		require.Len(t, ts, 1)
		assert.Equal(t, tc.want, ts[0].Text)
	}
	// Without a session Region, the logs open.
	p, _ := jumpParams(t, ecsContainer{Container: aws.Container{Name: "app"}, Task: &testTask, Log: west}, "")
	assert.Equal(t, "/ecs/x", p["log_group"])

	// Without awslogs-region, the group is in the Region of the task, and
	// the logs open.
	noRegion := &aws.LogConfig{Driver: "awslogs", Options: map[string]string{"awslogs-group": "/ecs/x", "awslogs-stream-prefix": "ecs"}}
	p, ts := jumpParams(t, ecsContainer{Container: aws.Container{Name: "app"}, Task: &testTask, Log: noRegion}, "us-east-1")
	assert.Empty(t, ts)
	assert.Equal(t, "/ecs/x", p["log_group"])
	assert.Equal(t, "ecs/app/0123456789abcdef", p["log_stream"])
}

func TestECSContainers_Keys(t *testing.T) {
	v, _, _ := newContainers(t, &testTask, testTaskDef, nil)
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	n, ok := cmd().(msg.NavigateMsg)
	require.True(t, ok)
	assert.Equal(t, "ecs/app/0123456789abcdef", n.Params["log_stream"])

	_, cmd = v.Update(keyPress('d'))
	require.NotNil(t, cmd)
	d, ok := cmd().(listDetailMsg)
	require.True(t, ok)
	assert.Equal(t, "app", d.panel.PanelTitle)

	_, cmd = v.Update(keyPress('y'))
	ts := toasts(collectMsgs(cmd))
	require.NotEmpty(t, ts)
	assert.Equal(t, "Copied: app", ts[0].Text, "testTask sends no container ARN, so the key is the name")

	assert.Equal(t, []string{"enter", "d", "y", "s/S", "/", "r"}, hintKeys(v.KeyMap()))

	v.Update(keyPress('/'))
	_, cmd = v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	for _, m := range collectMsgs(cmd) {
		_, isNav := m.(msg.NavigateMsg)
		assert.False(t, isNav, "enter closes the filter")
	}
}

// A container with no ARN (LocalStack can leave it out) has its name as
// the key.
func TestECSContainers_KeyFallsBackToTheName(t *testing.T) {
	assert.Equal(t, "app", ecsContainer{Container: aws.Container{Name: "app"}}.key())
	assert.Equal(t, "arn:a", ecsContainer{Container: aws.Container{Name: "app", ARN: "arn:a"}}.key())
}

func TestECSContainers_Detail(t *testing.T) {
	task := testTask
	c := ecsContainer{Task: &task, Log: awslogs, Container: aws.Container{
		Name: "app", ARN: "arn:container/app", Image: "nginx:alpine", ImageDigest: "sha256:abc", RuntimeID: "rt-1",
		LastStatus: "STOPPED", ExitCode: &exit137, Reason: "OutOfMemoryError", CPU: "128", Memory: "256",
		PrivateIPs: []string{"10.0.0.5"},
		Bindings:   []aws.PortBinding{{ContainerPort: 80, HostPort: 8080, Protocol: "tcp", BindIP: "0.0.0.0"}, {ContainerPort: 53, HostPort: 53}},
		Agents:     []aws.ManagedAgent{{Name: "ExecuteCommandAgent", Status: "RUNNING", StartedAt: taskNow}},
	}}
	d := ecsContainerDetail(c)
	titles := make([]string, 0, len(d.Tabs))
	for _, tab := range d.Tabs {
		titles = append(titles, tab.Title)
	}
	assert.Equal(t, []string{"Info", "Network", "Agents", "Logs"}, titles)
	for _, s := range []string{"arn:container/app", "sha256:abc", "rt-1", "137", "OutOfMemoryError", "128", "256"} {
		assert.Contains(t, d.Tabs[0].Content, s)
	}
	for _, s := range []string{"10.0.0.5", "0.0.0.0:8080 -> 80/tcp", "53 -> 53\n"} {
		assert.Contains(t, d.Tabs[1].Content, s)
	}
	assert.Contains(t, d.Tabs[2].Content, "ExecuteCommandAgent")
	assert.Contains(t, d.Tabs[2].Content, "RUNNING")
	logs := d.Tabs[3].Content
	for _, s := range []string{"awslogs", "ecs/app/0123456789abcdef", "awslogs-group", "/ecs/web-app", "awslogs-stream-prefix"} {
		assert.Contains(t, logs, s)
	}

	bare := ecsContainerDetail(ecsContainer{Task: &task, Container: aws.Container{Name: "x"}})
	require.Len(t, bare.Tabs, 3, "no agents, no Agents tab")
	assert.Contains(t, bare.Tabs[1].Content, "No network interfaces or port bindings")
	assert.Contains(t, bare.Tabs[2].Content, "No log configuration")
	assert.NotContains(t, bare.Tabs[0].Content, "Exit code", "an empty value leaves out its line")
}

func TestECSContainerColumnsFitTheirTiers(t *testing.T) {
	_, tier := ui.BestFitTier(80, ecsContainerColumns)
	assert.Equal(t, ui.TierMedium, tier)
	_, tier = ui.BestFitTier(120, ecsContainerColumns)
	assert.Equal(t, ui.TierWide, tier)
	_, tier = ui.BestFitTier(60, ecsContainerColumns)
	assert.Equal(t, ui.TierNarrow, tier)
}

// The longest values fit at each width: the statuses, UNHEALTHY and a
// three-digit exit code.
func TestECSContainers_LongValuesFit(t *testing.T) {
	task := testTask
	cs := []ecsContainer{
		{Task: &task, Container: aws.Container{Name: "a", ARN: "arn:a", LastStatus: "STOPPED", Health: "UNHEALTHY", ExitCode: &exit137}},
		{Task: &task, Container: aws.Container{Name: "b", ARN: "arn:b", LastStatus: "PENDING", Health: "UNKNOWN"}},
	}
	for _, w := range []int{70, 100, 130} {
		v := NewECSContainers(nil, testClusterARN, testTaskARN, "us-east-1")
		v.Update(tea.WindowSizeMsg{Width: w, Height: 30})
		deliverItems(v, &v.listCore, cs)
		out := ansi.Strip(v.View().Content)
		assert.Contains(t, out, "STOPPED", "width %d", w)
		assert.Contains(t, out, "PENDING", "width %d", w)
		if w >= 80 {
			assert.Contains(t, out, "UNHEALTHY", "width %d", w)
			assert.Contains(t, out, "137", "width %d", w)
		}
	}
}

// The sort key of each column lines up with its cell, in each tier.
func TestECSContainers_SortKeysLineUp(t *testing.T) {
	v := NewECSContainers(nil, testClusterARN, testTaskARN, "us-east-1")
	c := testContainers()[1]
	c.ExitCode = &exit137
	for _, tier := range []ui.WidthTier{ui.TierNarrow, ui.TierMedium, ui.TierWide} {
		row := v.row(c, tier)
		cols := ecsContainerColumns(tier)
		require.Len(t, row.Cells, len(cols), "tier %v", tier)
		if row.Sort == nil {
			continue
		}
		require.Len(t, row.Sort, len(cols), "tier %v", tier)
		for i, col := range cols {
			if col.Title == "Exit" {
				assert.Equal(t, ui.SortKeyInt(137), row.Sort[i])
				assert.Equal(t, "137", row.Cells[i])
				continue
			}
			assert.Equal(t, row.Cells[i], row.Sort[i], "tier %v column %s", tier, col.Title)
		}
	}
}
