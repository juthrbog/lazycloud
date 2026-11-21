package views

import (
	"context"
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

// Tests of the ECS service list (docs/design/ecs.md, PR 2).

const testServiceARN = "arn:aws:ecs:us-east-1:1:service/prod-cluster/web"

var testService = aws.ClusterService{
	Name: "web", ARN: testServiceARN, ClusterARN: testClusterARN, Status: "ACTIVE",
	Desired: 3, Running: 2, Pending: 1, LaunchType: "FARGATE", PlatformVersion: "1.4.0",
	TaskDefinition: "arn:aws:ecs:us-east-1:1:task-definition/web:7", SchedulingStrategy: "REPLICA",
	Controller: "ECS", Strategy: "ROLLING", CircuitBreaker: true, CircuitRollback: true,
	MinHealthyPercent: 100, MaxPercent: 200, ExecEnabled: true,
	CreatedAt: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC), CreatedBy: "arn:aws:iam::1:role/ci",
	CurrentDeployment: "arn:aws:ecs:us-east-1:1:service-deployment/prod-cluster/web/d1",
	Deployments: []aws.Deployment{
		{ID: "ecs-svc/3", Status: "PRIMARY", TaskDefinition: "arn:aws:ecs:us-east-1:1:task-definition/web:7",
			Desired: 3, Running: 2, Pending: 1, Failed: 2, RolloutState: "IN_PROGRESS", RolloutReason: "ECS deployment ecs-svc/3 in progress."},
		{ID: "ecs-svc/2", Status: "ACTIVE", TaskDefinition: "arn:aws:ecs:us-east-1:1:task-definition/web:6", Desired: 1, Running: 1},
	},
	Events: []aws.ServiceEvent{
		{Time: time.Date(2026, 11, 1, 9, 5, 0, 0, time.UTC), Message: "(service web) has started 1 tasks."},
	},
	Subnets: []string{"subnet-1"}, SecurityGroups: []string{"sg-1"}, PublicIP: "DISABLED",
	LoadBalancers: []aws.ServiceLoadBalancer{{TargetGroupARN: "arn:tg/web", Container: "app", Port: 80}},
	Registries:    []string{"arn:sd/web"},
	Tags:          map[string]string{"team": "web"},
}

var testDeployment = &aws.ServiceDeployment{
	ARN: testService.CurrentDeployment, Status: "IN_PROGRESS", LifecycleStage: "SCALE_UP",
	TargetRevision: "arn:aws:ecs:us-east-1:1:service-revision/prod-cluster/web/r9", TargetRequested: 3, TargetRunning: 2, TargetPending: 1,
	CircuitStatus: "MONITORING", CircuitFailures: 1, CircuitThreshold: 10,
	AlarmStatus: "TRIGGERED", TriggeredAlarms: []string{"5xx"},
	StartedAt: time.Date(2026, 11, 1, 9, 1, 0, 0, time.UTC),
}

func newServices(t *testing.T, services ...aws.ClusterService) *ECSServices {
	t.Helper()
	svc := new(awstest.MockECSService)
	svc.Test(t)
	svc.On("ListServicesPage", mock.Anything, testClusterARN, mock.Anything).Return(&aws.ServicePage{Services: services}, nil)
	v := NewECSServices(svc, testClusterARN)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	pump(v, v.Init(), 20)
	return v
}

func TestECSServices_RowsAndTitle(t *testing.T) {
	v := newServices(t, testService)
	assert.Equal(t, "ecs_services:"+testClusterARN, v.ID())
	assert.Equal(t, "prod-cluster services", v.Title())
	out := ansi.Strip(v.View().Content)
	for _, s := range []string{"web", "2/3", "FARGATE", "web:7", "IN_PROGRESS", "ROLLING"} {
		assert.Contains(t, out, s)
	}
	assert.Contains(t, v.Footer(), "1/1 services")
	assert.Contains(t, v.View().Content, ui.RatioColor("2/3"), "the Tasks column has its style")
	assert.Contains(t, v.View().Content, ui.RolloutColor("IN_PROGRESS"), "the Rollout column has its style")

	r := ecsServiceRow(testService, ui.TierWide)
	assert.Equal(t, testServiceARN, r.Key)
	assert.Equal(t, []string{"web", "ACTIVE", "2/3", "1", "FARGATE", "web:7", "IN_PROGRESS", "ROLLING", logDate(testService.CreatedAt.Local())},
		[]string(r.Cells))
	assert.Equal(t, ui.SortKeyInt(2), r.Sort[2], "Tasks sorts by the running count")
	assert.Equal(t, []string{"web", "2/3", "IN_PROGRESS"}, []string(ecsServiceRow(testService, ui.TierNarrow).Cells))
	assert.Equal(t, []string{"web", "ACTIVE", "2/3", "FARGATE", "web:7", "IN_PROGRESS"}, []string(ecsServiceRow(testService, ui.TierMedium).Cells))
}

// A service with a capacity provider strategy has no launch type, so the
// cell shows the providers. A service with no PRIMARY deployment has no
// rollout. A service that AWS could not describe shows ?.
func TestECSServices_RowEdges(t *testing.T) {
	s := aws.ClusterService{Name: "spot", ARN: "arn:spot", Status: "ACTIVE", Desired: 1, Running: 1,
		CapacityStrategy: []aws.CapacityProviderItem{{Name: "FARGATE"}, {Name: "FARGATE_SPOT"}}}
	r := ecsServiceRow(s, ui.TierMedium)
	assert.Equal(t, "FARGATE,FARGATE_SPOT", r.Cells[3])
	assert.Empty(t, r.Cells[5], "no PRIMARY deployment")

	bad := aws.ClusterService{Name: "gone", ARN: "arn:gone", Err: "ACCESS_DENIED"}
	r = ecsServiceRow(bad, ui.TierWide)
	assert.Equal(t, []string{"gone", "?", "?", "?", "", "", "", "", ""}, []string(r.Cells))
	assert.Empty(t, r.Sort[2])
}

func TestECSServices_Detail(t *testing.T) {
	svc := new(awstest.MockECSService)
	svc.Test(t)
	svc.On("DescribeServiceDeployment", mock.Anything, testService.CurrentDeployment).Return(testDeployment, nil)
	d, err := ecsServiceDetail(context.Background(), svc, testService)
	require.NoError(t, err)
	assert.Equal(t, "web", d.PanelTitle)
	titles := make([]string, 0, len(d.Tabs))
	for _, tab := range d.Tabs {
		titles = append(titles, tab.Title)
	}
	assert.Equal(t, []string{"Info", "Deployments", "Events", "Network", "Tags"}, titles, "Info first, for a narrow terminal")

	info := d.Tabs[0].Content
	for _, s := range []string{testServiceARN, "2 running, 3 desired, 1 pending", "web:7", "ROLLING (min 100%, max 200%)",
		"on, with rollback", "Exec             Yes", "arn:aws:iam::1:role/ci"} {
		assert.Contains(t, info, s)
	}
	assert.NotContains(t, info, "Express Mode", "only for a managed service")

	deps := d.Tabs[1].Content
	for _, s := range []string{"Current deployment", "IN_PROGRESS", "SCALE_UP", "r9 (2 running of 3, 1 pending)",
		"MONITORING (1 of 10 failures)", "TRIGGERED, triggered: 5xx",
		"PRIMARY ecs-svc/3", "Failed tasks   2", "IN_PROGRESS (ECS deployment ecs-svc/3 in progress.)", "ACTIVE ecs-svc/2", "web:6"} {
		assert.Contains(t, deps, s)
	}

	events := d.Tabs[2].Content
	assert.Contains(t, events, ecsEventsNote)
	assert.Contains(t, events, "(service web) has started 1 tasks.")

	net := d.Tabs[3]
	for _, s := range []string{"subnet-1", "sg-1", "DISABLED", "arn:tg/web → app:80", "arn:sd/web"} {
		assert.Contains(t, net.Content, s)
	}
	require.Len(t, net.Links, 2)
	assert.Equal(t, msg.TabLink{Line: 0, ViewID: "subnet_list", Params: map[string]string{"focus": "subnet-1"}}, net.Links[0])
	assert.Equal(t, msg.TabLink{Line: 1, ViewID: "sg_list", Params: map[string]string{"focus": "sg-1"}}, net.Links[1])
	assert.Contains(t, d.Tabs[4].Content, "web")
}

// A failure of DescribeServiceDeployment shows on its line, and the panel
// still opens (LocalStack does not support the call).
func TestECSServices_DetailWithoutTheDeploymentAPI(t *testing.T) {
	svc := new(awstest.MockECSService)
	svc.On("DescribeServiceDeployment", mock.Anything, mock.Anything).Return(nil, errors.New("InternalFailure: not supported"))
	d, err := ecsServiceDetail(context.Background(), svc, testService)
	require.NoError(t, err)
	assert.Contains(t, d.Tabs[1].Content, "Current deployment: failed:")
	assert.Contains(t, d.Tabs[1].Content, "PRIMARY ecs-svc/3", "the deployments of the service still show")

	// No current deployment: no call.
	plain := testService
	plain.CurrentDeployment, plain.Deployments, plain.Events, plain.Tags = "", nil, nil, nil
	plain.Subnets, plain.SecurityGroups, plain.PublicIP, plain.LoadBalancers, plain.Registries = nil, nil, "", nil, nil
	plain.Managed = true
	d, err = ecsServiceDetail(context.Background(), nil, plain)
	require.NoError(t, err)
	require.Len(t, d.Tabs, 4, "no tags, no Tags tab")
	assert.Contains(t, d.Tabs[0].Content, "Express Mode manages this service")
	assert.Contains(t, d.Tabs[1].Content, "No deployments")
	assert.NotContains(t, d.Tabs[1].Content, "Current deployment")
	assert.Contains(t, d.Tabs[2].Content, "No events")
	assert.Contains(t, d.Tabs[3].Content, "No awsvpc configuration")

	// A current deployment and no service: no client.
	_, err = ecsServiceDetail(context.Background(), nil, testService)
	require.ErrorIs(t, err, errNoClient)

	failed, err := ecsServiceDetail(context.Background(), nil, aws.ClusterService{Name: "x", ARN: "arn:x", Err: "ACCESS_DENIED"})
	require.NoError(t, err)
	require.Len(t, failed.Tabs, 1)
	assert.Contains(t, failed.Tabs[0].Content, "ACCESS_DENIED")
}

// enter opens the detail until the task list exists (PR 3), as d does.
func TestECSServices_EnterOpensTheDetail(t *testing.T) {
	plain := testService
	plain.CurrentDeployment = ""
	v := newServices(t, plain)
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, keyPress('d')} {
		_, cmd := v.Update(k)
		require.NotNil(t, cmd)
		d, ok := cmd().(listDetailMsg)
		require.True(t, ok)
		require.NoError(t, d.err)
		assert.Equal(t, "web", d.panel.PanelTitle)
	}
}

func TestECSServices_UsesTheListTimeoutAndBudget(t *testing.T) {
	v := NewECSServices(nil, testClusterARN)
	assert.Equal(t, listTimeout, v.cfg.FetchTimeout, "a page is up to 11 calls")
	assert.Equal(t, ecsServicePageBudget, v.cfg.PageBudget)
	assert.Equal(t, 10, ecsServicePageBudget)
	assert.True(t, v.cfg.MoreKey)
}

func TestECSParens(t *testing.T) {
	assert.Empty(t, parens(""))
	assert.Equal(t, "(x)", parens("x"))
	assert.Empty(t, ecsNonZero(0))
	assert.Equal(t, "3", ecsNonZero(3))
	assert.Empty(t, ecsRevisionText(&aws.ServiceDeployment{}))
	assert.Empty(t, ecsCircuitText(&aws.ServiceDeployment{}))
	assert.Empty(t, ecsAlarmText(&aws.ServiceDeployment{}))
	assert.Equal(t, "MONITORING", ecsAlarmText(&aws.ServiceDeployment{AlarmStatus: "MONITORING"}))
}

// The medium set fits 80 columns and the wide set 120, and the Tasks column
// holds 1000/1000.
func TestECSColumnSetsFitTheirTiers(t *testing.T) {
	_, tier := ui.BestFitTier(80, ecsServiceColumns)
	assert.Equal(t, ui.TierMedium, tier)
	_, tier = ui.BestFitTier(120, ecsServiceColumns)
	assert.Equal(t, ui.TierWide, tier)
	_, tier = ui.BestFitTier(80, ecsClusterColumns)
	assert.Equal(t, ui.TierMedium, tier)
	_, tier = ui.BestFitTier(120, ecsClusterColumns)
	assert.Equal(t, ui.TierWide, tier)
	for _, tr := range []ui.WidthTier{ui.TierNarrow, ui.TierMedium, ui.TierWide} {
		for _, c := range ecsServiceColumns(tr) {
			if c.Title == "Tasks" {
				assert.GreaterOrEqual(t, c.Width, len("1000/1000"), "tier %v", tr)
			}
		}
	}
	big := testService
	big.Running, big.Desired = 1000, 1000
	v := NewECSServices(nil, testClusterARN)
	v.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	deliverItems(v, &v.listCore, []aws.ClusterService{big})
	assert.Contains(t, ansi.Strip(v.View().Content), "1000/1000")
}

// A service that AWS could not describe gives one toast after the load.
func TestECSServices_UnreadableServiceToast(t *testing.T) {
	svc := new(awstest.MockECSService)
	bad := aws.ClusterService{Name: "gone", ARN: "arn:gone", Err: "ACCESS_DENIED"}
	svc.On("ListServicesPage", mock.Anything, testClusterARN, mock.Anything).Return(&aws.ServicePage{Services: []aws.ClusterService{testService, bad}}, nil)
	v := NewECSServices(svc, testClusterARN)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	ts := toasts(pump(v, v.Init(), 20))
	require.Len(t, ts, 1)
	assert.Equal(t, msg.ToastError("Could not describe 1 service(s): gone (ACCESS_DENIED). Their counts show ?."), ts[0])
}

func TestECSServices_CopyARNAndKeyMap(t *testing.T) {
	v := newServices(t, testService)
	_, cmd := v.Update(keyPress('y'))
	ts := toasts(collectMsgs(cmd))
	require.NotEmpty(t, ts)
	assert.Contains(t, ts[0].Text, testServiceARN)

	assert.Equal(t, []string{"enter", "d", "y", "l", "s/S", "/", "r"}, hintKeys(NewECSServices(nil, testClusterARN).KeyMap()))
	for _, h := range NewECSServices(nil, testClusterARN).KeyMap() {
		assert.NotEqual(t, ui.ModeReadWrite, h.Mode, h.Binding.Help().Key)
	}
}

// enter waits while the filter is open, and the Status column has its
// style in the medium and the wide set.
func TestECSServices_FilterAndStatusStyle(t *testing.T) {
	plain := testService
	plain.CurrentDeployment, plain.Status = "", "DRAINING"
	v := newServices(t, plain)
	assert.Contains(t, v.View().Content, ui.StateColor("DRAINING"))
	v.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	assert.Contains(t, v.View().Content, ui.StateColor("DRAINING"), "medium set")

	v.Update(keyPress('/'))
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	for _, m := range collectMsgs(cmd) {
		_, isDetail := m.(listDetailMsg)
		assert.False(t, isDetail, "enter closes the filter, it opens no detail")
	}
}
