package views

import (
	"errors"
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

// Tests of the ECS task list (docs/design/ecs.md, PR 3).

var taskNow = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

func useTaskClock(t *testing.T) {
	t.Helper()
	listNow = func() time.Time { return taskNow }
	t.Cleanup(func() { listNow = time.Now })
}

const testTaskARN = "arn:aws:ecs:us-east-1:1:task/prod-cluster/0123456789abcdef"

var exitZero = int32(0)

var testTask = aws.Task{
	ARN: testTaskARN, ID: "0123456789abcdef", ClusterARN: testClusterARN,
	TaskDefinition: "arn:aws:ecs:us-east-1:1:task-definition/web:7",
	LastStatus:     "RUNNING", DesiredStatus: "RUNNING", Health: "HEALTHY",
	LaunchType: "FARGATE", PlatformVersion: "1.4.0", PlatformFamily: "Linux",
	CPU: "256", Memory: "512", EphemeralGiB: 20, AvailabilityZone: "us-east-1a",
	Group: "service:web", StartedBy: "ecs-svc/3", Connectivity: "CONNECTED", ExecEnabled: true,
	CreatedAt: taskNow.Add(-3*time.Hour - 15*time.Minute), StartedAt: taskNow.Add(-3*time.Hour - 12*time.Minute),
	ENI: "eni-1", PrivateIP: "10.0.0.5", SubnetID: "subnet-1",
	Containers: []aws.Container{
		{Name: "app", Image: "nginx:alpine", LastStatus: "RUNNING", Health: "HEALTHY"},
		{Name: "init", Image: "busybox", LastStatus: "STOPPED", ExitCode: &exitZero, Reason: "done"},
	},
	Tags: map[string]string{"team": "web"},
}

var stoppedTask = aws.Task{
	ARN: "arn:aws:ecs:us-east-1:1:task/prod-cluster/fedcba9876543210", ID: "fedcba9876543210", ClusterARN: testClusterARN,
	TaskDefinition: "arn:aws:ecs:us-east-1:1:task-definition/web:6",
	LastStatus:     "STOPPED", StopCode: "EssentialContainerExited", StoppedReason: "Essential container in task exited",
	StartedAt: taskNow.Add(-50 * time.Minute), StoppedAt: taskNow.Add(-12 * time.Minute),
}

func newTasks(t *testing.T) (*ECSTasks, *awstest.MockECSService) {
	t.Helper()
	useTaskClock(t)
	svc := new(awstest.MockECSService)
	svc.Test(t)
	svc.On("ListTasksPage", mock.Anything, testClusterARN, "web", false, mock.Anything).Return(&aws.TaskPage{Tasks: []aws.Task{testTask}}, nil)
	svc.On("ListTasksPage", mock.Anything, testClusterARN, "web", true, mock.Anything).Return(&aws.TaskPage{Tasks: []aws.Task{stoppedTask}}, nil)
	v := NewECSTasks(svc, nil, testClusterARN, "web")
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	pump(v, v.Init(), 20)
	return v, svc
}

func TestECSAge(t *testing.T) {
	useTaskClock(t)
	for d, want := range map[time.Duration]string{
		45 * time.Second:               "45s",
		time.Minute:                    "1m",
		12 * time.Minute:               "12m",
		3*time.Hour + 12*time.Minute:   "3h12m",
		52*time.Hour + 30*time.Minute:  "2d4h",
		-5 * time.Minute:               "0s", // a clock skew shows 0
		24*time.Hour - time.Nanosecond: "23h59m",
	} {
		assert.Equal(t, want, ecsAge(taskNow.Add(-d)), d.String())
	}
	assert.Empty(t, ecsAge(time.Time{}))
	assert.Empty(t, ecsTimeKey(time.Time{}))
	assert.Less(t, ecsTimeKey(taskNow.Add(-time.Hour)), ecsTimeKey(taskNow), "the oldest sorts first")
	assert.Equal(t, ui.SortKeyInt(256), ecsNumberKey("256"))
	assert.Equal(t, "n/a", ecsNumberKey("n/a"))
}

func TestECSTasks_RunningRows(t *testing.T) {
	useTaskClock(t)
	r := ecsTaskRow(testTask, ui.TierWide)
	assert.Equal(t, testTaskARN, r.Key)
	assert.Equal(t, []string{"0123456789abcdef", "RUNNING", "HEALTHY", "web:7", "3h12m", "FARGATE", "256", "512", "us-east-1a", "service:web"},
		[]string(r.Cells))
	assert.Equal(t, ecsTimeKey(testTask.StartedAt), r.Sort[4], "Started sorts by the time")
	assert.Equal(t, ui.SortKeyInt(256), r.Sort[6], "CPU sorts as a number")
	assert.Equal(t, []string{"0123456789abcdef", "RUNNING", "HEALTHY", "web:7", "3h12m", "FARGATE"}, []string(ecsTaskRow(testTask, ui.TierMedium).Cells))
	assert.Equal(t, []string{"0123456789abcdef", "RUNNING", "3h12m"}, []string(ecsTaskRow(testTask, ui.TierNarrow).Cells))

	// The capacity provider wins over the launch type, and an empty value
	// stays an empty cell (H8).
	spot := aws.Task{ARN: "arn:t", ID: "t", LastStatus: "PROVISIONING", LaunchType: "FARGATE", CapacityProvider: "FARGATE_SPOT"}
	r = ecsTaskRow(spot, ui.TierWide)
	assert.Equal(t, []string{"t", "PROVISIONING", "", "", "", "FARGATE_SPOT", "", "", "", ""}, []string(r.Cells))
	assert.Empty(t, r.Sort[4], "no start time sorts first")

	bad := aws.Task{ARN: "arn:bad", ID: "bad", Err: "ACCESS_DENIED"}
	assert.Equal(t, "?", ecsTaskRow(bad, ui.TierNarrow).Cells[1])
}

func TestECSTasks_StoppedRows(t *testing.T) {
	useTaskClock(t)
	r := ecsStoppedRow(stoppedTask, ui.TierWide)
	assert.Equal(t, []string{"fedcba9876543210", "EssentialContainerExited", "Essential container in task exited", "12m", "web:6", "50m"},
		[]string(r.Cells))
	assert.Equal(t, ecsTimeKey(stoppedTask.StoppedAt), r.Sort[3])
	assert.Len(t, ecsStoppedRow(stoppedTask, ui.TierMedium).Cells, 5)
	assert.Equal(t, []string{"fedcba9876543210", "Essential container in task exited", "12m"}, []string(ecsStoppedRow(stoppedTask, ui.TierNarrow).Cells))

	// The stop time falls back to ExecutionStoppedAt, then StoppingAt.
	noStop := stoppedTask
	noStop.StoppedAt, noStop.ExecutionStoppedAt = time.Time{}, taskNow.Add(-20*time.Minute)
	assert.Equal(t, "20m", ecsStoppedRow(noStop, ui.TierNarrow).Cells[2])
	noStop.StoppingAt = taskNow.Add(-30 * time.Minute)
	assert.Equal(t, "20m", ecsStoppedRow(noStop, ui.TierNarrow).Cells[2], "ExecutionStoppedAt comes before StoppingAt")
	noStop.ExecutionStoppedAt = time.Time{}
	assert.Equal(t, "30m", ecsStoppedRow(noStop, ui.TierNarrow).Cells[2])
	noStop.StoppingAt = time.Time{}
	assert.Empty(t, ecsStoppedRow(noStop, ui.TierNarrow).Cells[2])

	// LocalStack leaves the stop code empty for an essential container exit.
	empty := aws.Task{ARN: "arn:e", ID: "e", LastStatus: "STOPPED"}
	assert.Equal(t, []string{"e", "", "", "", "", ""}, []string(ecsStoppedRow(empty, ui.TierWide).Cells))
	assert.Equal(t, "? ACCESS_DENIED", ecsStoppedRow(aws.Task{ARN: "arn:x", ID: "x", Err: "ACCESS_DENIED"}, ui.TierNarrow).Cells[1])
}

// x switches to the stopped tasks, with their columns, title and footer,
// and r stays in that mode. x again goes back.
func TestECSTasks_StoppedToggle(t *testing.T) {
	v, svc := newTasks(t)
	assert.Equal(t, "ecs_tasks:"+testClusterARN+":web", v.ID())
	assert.Equal(t, "web tasks", v.Title())
	assert.Contains(t, hintKeys(v.KeyMap()), "x")
	assert.Equal(t, "stopped tasks", v.KeyMap()[2].Binding.Help().Desc)
	assert.NotContains(t, v.Footer(), ecsStoppedNote)

	_, cmd := v.Update(keyPress('x'))
	pump(v, cmd, 20)
	assert.Equal(t, "web stopped tasks", v.Title())
	assert.Equal(t, "running tasks", v.KeyMap()[2].Binding.Help().Desc)
	assert.Contains(t, v.Footer(), "1/1 tasks · "+ecsStoppedNote)
	out := ansi.Strip(v.View().Content)
	assert.Contains(t, out, "Stop code")
	assert.Contains(t, out, "Essential container in task exited")
	assert.NotContains(t, out, "0123456789abcdef", "no running row stays")
	svc.AssertCalled(t, "ListTasksPage", mock.Anything, testClusterARN, "web", true, mock.Anything)

	_, cmd = v.Update(keyPress('r'))
	pump(v, cmd, 20)
	calls := 0
	for _, c := range svc.Calls {
		if c.Method == "ListTasksPage" && c.Arguments.Bool(3) {
			calls++
		}
	}
	assert.Equal(t, 2, calls, "r reads the stopped tasks")

	_, cmd = v.Update(keyPress('x'))
	pump(v, cmd, 20)
	assert.Equal(t, "web tasks", v.Title())
	out = ansi.Strip(v.View().Content)
	assert.Contains(t, out, "Health")
	assert.Contains(t, out, "0123456789abcdef")
}

// x works on the error screen, because it loads the other list, but not
// while the filter is open.
func TestECSTasks_ToggleOnTheErrorScreen(t *testing.T) {
	useTaskClock(t)
	svc := new(awstest.MockECSService)
	svc.On("ListTasksPage", mock.Anything, testClusterARN, "", false, mock.Anything).Return(nil, errors.New("boom"))
	svc.On("ListTasksPage", mock.Anything, testClusterARN, "", true, mock.Anything).Return(&aws.TaskPage{Tasks: []aws.Task{stoppedTask}}, nil)
	v := NewECSTasks(svc, nil, testClusterARN, "")
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	pump(v, v.Init(), 20)
	require.Error(t, v.err)
	assert.Equal(t, "prod-cluster tasks", v.Title(), "no service: the cluster name")
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Nil(t, cmd, "enter does nothing on the error screen")

	_, cmd = v.Update(keyPress('x'))
	pump(v, cmd, 20)
	require.NoError(t, v.err)
	assert.Len(t, v.items, 1)

	v.Update(keyPress('/'))
	v.Update(keyPress('x'))
	assert.True(t, v.stopped, "x goes to the filter while it is open")
}

func TestECSTasks_Keys(t *testing.T) {
	v, _ := newTasks(t)
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	assert.Equal(t, msg.NavigateMsg{ViewID: "ecs_containers",
		Params: map[string]string{"cluster": testClusterARN, "task": testTaskARN}}, cmd(), "enter opens the containers")
	_, cmd = v.Update(keyPress('d'))
	require.NotNil(t, cmd)
	d, ok := cmd().(listDetailMsg)
	require.True(t, ok)
	assert.Equal(t, "0123456789abcdef", d.panel.PanelTitle)

	_, cmd = v.Update(keyPress('y'))
	ts := toasts(collectMsgs(cmd))
	require.NotEmpty(t, ts)
	assert.Contains(t, ts[0].Text, testTaskARN)

	assert.Equal(t, []string{"enter", "d", "x", "o", "y", "l", "s/S", "/", "r"}, hintKeys(v.KeyMap()))
	assert.Contains(t, v.View().Content, ui.HealthColor("HEALTHY"), "the Health column has its style")
	assert.Contains(t, v.View().Content, ui.StateColor("RUNNING"), "the Status column has its style")
	v.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	assert.Contains(t, v.View().Content, ui.HealthColor("HEALTHY"), "the medium set has the style too")
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})

	v.Update(keyPress('/'))
	_, cmd = v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	for _, m := range collectMsgs(cmd) {
		_, isNav := m.(msg.NavigateMsg)
		assert.False(t, isNav, "enter closes the filter")
	}
}

func TestECSTasks_Detail(t *testing.T) {
	d := ecsTaskDetail(testTask)
	titles := make([]string, 0, len(d.Tabs))
	for _, tab := range d.Tabs {
		titles = append(titles, tab.Title)
	}
	assert.Equal(t, []string{"Info", "Containers", "Network", "Tags"}, titles)
	info := d.Tabs[0].Content
	for _, s := range []string{testTaskARN, "RUNNING", "HEALTHY", "web:7", "Linux 1.4.0", "20 GiB", "service:web", "ecs-svc/3", "Exec             Yes"} {
		assert.Contains(t, info, s)
	}
	assert.NotContains(t, info, "desired", "the desired status shows only when it differs")
	containers := d.Tabs[1].Content
	for _, s := range []string{"app", "nginx:alpine", "init", "Exit code      0", "done"} {
		assert.Contains(t, containers, s)
	}
	net := d.Tabs[2]
	assert.Contains(t, net.Content, "eni-1")
	assert.Contains(t, net.Content, "10.0.0.5")
	require.Len(t, net.Links, 1)
	assert.Equal(t, msg.TabLink{Line: 2, ViewID: "subnet_list", Params: map[string]string{"focus": "subnet-1"}}, net.Links[0])

	stopping := stoppedTask
	stopping.LastStatus, stopping.DesiredStatus = "STOPPING", "STOPPED"
	d = ecsTaskDetail(stopping)
	require.Len(t, d.Tabs, 3, "no tags, no Tags tab")
	assert.Contains(t, d.Tabs[0].Content, "STOPPING (desired STOPPED)")
	assert.Contains(t, d.Tabs[0].Content, "EssentialContainerExited")
	assert.Contains(t, d.Tabs[1].Content, "No containers")
	assert.Contains(t, d.Tabs[2].Content, "No network interface")

	failed := ecsTaskDetail(aws.Task{ARN: "arn:x", ID: "x", Err: "ACCESS_DENIED"})
	require.Len(t, failed.Tabs, 1)
	assert.Contains(t, failed.Tabs[0].Content, "ACCESS_DENIED")
}

func TestECSTasks_UnreadableTaskToast(t *testing.T) {
	useTaskClock(t)
	svc := new(awstest.MockECSService)
	bad := aws.Task{ARN: "arn:bad", ID: "bad", Err: "ACCESS_DENIED"}
	svc.On("ListTasksPage", mock.Anything, testClusterARN, "web", false, mock.Anything).Return(&aws.TaskPage{Tasks: []aws.Task{testTask, bad}}, nil)
	v := NewECSTasks(svc, nil, testClusterARN, "web")
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	ts := toasts(pump(v, v.Init(), 20))
	require.Len(t, ts, 1)
	assert.Contains(t, ts[0].Text, "Could not describe 1 task(s): bad (ACCESS_DENIED)")
}

func TestECSTaskColumnsFitTheirTiers(t *testing.T) {
	for _, cols := range []func(ui.WidthTier) []ui.Column{ecsTaskColumns, ecsStoppedColumns} {
		_, tier := ui.BestFitTier(80, cols)
		assert.Equal(t, ui.TierMedium, tier)
		_, tier = ui.BestFitTier(120, cols)
		assert.Equal(t, ui.TierWide, tier)
	}
	v := NewECSTasks(nil, nil, testClusterARN, "web")
	assert.Equal(t, ecsTaskPageBudget, v.cfg.PageBudget)
	assert.Equal(t, 10, ecsTaskPageBudget)
	assert.True(t, v.cfg.MoreKey)
}

// The full stop codes, FARGATE_SPOT and DEPROVISIONING fit their columns.
func TestECSTasks_LongValuesFit(t *testing.T) {
	useTaskClock(t)
	sched := stoppedTask
	sched.StopCode = "ServiceSchedulerInitiated"
	crash := stoppedTask
	crash.ARN, crash.ID = "arn:crash", "crash"
	v := NewECSTasks(nil, nil, testClusterARN, "web")
	v.stopped = true
	v.cfg.Columns, v.cfg.Row = ecsStoppedColumns, ecsStoppedRow
	v.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	deliverItems(v, &v.listCore, []aws.Task{sched, crash})
	assert.Contains(t, ansi.Strip(v.View().Content), "ServiceSchedulerInitiated")
	v.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	assert.Contains(t, ansi.Strip(v.View().Content), "EssentialContainerExited")

	spot := testTask
	spot.CapacityProvider, spot.LastStatus = "FARGATE_SPOT", "DEPROVISIONING"
	for _, w := range []int{100, 120} {
		r := NewECSTasks(nil, nil, testClusterARN, "web")
		r.Update(tea.WindowSizeMsg{Width: w, Height: 30})
		deliverItems(r, &r.listCore, []aws.Task{spot})
		out := ansi.Strip(r.View().Content)
		assert.Contains(t, out, "FARGATE_SPOT", "width %d", w)
		assert.Contains(t, out, "DEPROVISIONING", "width %d", w)
	}
}

// The sort key of each column lines up with its cell, in each tier and in
// both kinds of rows.
func TestECSTasks_SortKeysLineUp(t *testing.T) {
	useTaskClock(t)
	for _, tier := range []ui.WidthTier{ui.TierNarrow, ui.TierMedium, ui.TierWide} {
		for _, kind := range []struct {
			cols func(ui.WidthTier) []ui.Column
			row  ui.Row
		}{
			{ecsTaskColumns, ecsTaskRow(testTask, tier)},
			{ecsStoppedColumns, ecsStoppedRow(stoppedTask, tier)},
		} {
			cols := kind.cols(tier)
			require.Len(t, kind.row.Cells, len(cols), "tier %v", tier)
			require.Len(t, kind.row.Sort, len(cols), "tier %v", tier)
			for i, c := range cols {
				switch c.Title {
				case "Started":
					want := ecsTimeKey(testTask.StartedAt)
					if kind.cols(ui.TierWide)[1].Title == "Stop code" {
						want = ecsTimeKey(stoppedTask.StartedAt)
					}
					assert.Equal(t, want, kind.row.Sort[i], "tier %v %s", tier, c.Title)
				case "Stopped":
					assert.Equal(t, ecsTimeKey(stoppedTask.StoppedAt), kind.row.Sort[i], "tier %v", tier)
				case "CPU":
					assert.Equal(t, ecsNumberKey(testTask.CPU), kind.row.Sort[i])
				case "Memory":
					assert.Equal(t, ecsNumberKey(testTask.Memory), kind.row.Sort[i])
				default:
					assert.Equal(t, kind.row.Cells[i], kind.row.Sort[i], "tier %v %s sorts as text", tier, c.Title)
				}
			}
		}
	}
}

// An empty value leaves its line out of the detail (H8).
func TestECSTasks_DetailLeavesOutEmptyValues(t *testing.T) {
	info := ecsTaskDetail(stoppedTask).Tabs[0].Content
	assert.NotContains(t, info, "desired", "no desired status, no note")
	assert.NotContains(t, info, "Ephemeral")
	assert.NotContains(t, info, "Health")
	assert.NotContains(t, ecsContainersText([]aws.Container{{Name: "app", LastStatus: "RUNNING"}}), "Exit code",
		"a container that did not exit has no exit code")
}

// The stopped columns stay after a resize.
func TestECSTasks_ColumnsStayAfterAResize(t *testing.T) {
	v, _ := newTasks(t)
	_, cmd := v.Update(keyPress('x'))
	pump(v, cmd, 20)
	v.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	assert.Contains(t, ansi.Strip(v.View().Content), "Stop code")
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	assert.Contains(t, ansi.Strip(v.View().Content), "Stop code")
}
