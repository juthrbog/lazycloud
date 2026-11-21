package views

import (
	"errors"
	"testing"

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

// Tests of the ECS cluster list (docs/design/ecs.md, PR 2).

const testClusterARN = "arn:aws:ecs:us-east-1:1:cluster/prod-cluster"

var testCluster = aws.Cluster{
	Name: "prod-cluster", ARN: testClusterARN, Status: "ACTIVE",
	ActiveServices: 3, RunningTasks: 12, PendingTasks: 2, Instances: 4,
	CapacityProviders: []string{"FARGATE", "FARGATE_SPOT"},
	DefaultStrategy:   []aws.CapacityProviderItem{{Name: "FARGATE", Base: 1, Weight: 2}, {Name: "FARGATE_SPOT", Weight: 1}},
	ContainerInsights: "enhanced", ExecLogging: "OVERRIDE", ExecLogGroup: "/ecs/exec", ExecS3Bucket: "exec-logs",
	Tags: map[string]string{"team": "platform"},
}

func newClusters(t *testing.T, clusters ...aws.Cluster) (*ECSClusters, *awstest.MockECSService) {
	t.Helper()
	svc := new(awstest.MockECSService)
	svc.Test(t)
	svc.On("ListClustersPage", mock.Anything, mock.Anything).Return(&aws.ClusterPage{Clusters: clusters}, nil)
	v := NewECSClusters(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	pump(v, v.Init(), 20)
	return v, svc
}

func TestECSClusters_RowsAndCounts(t *testing.T) {
	v, _ := newClusters(t, testCluster)
	out := ansi.Strip(v.View().Content)
	for _, s := range []string{"prod-cluster", "ACTIVE", "12", "FARGATE, FARGATE_SPOT"} {
		assert.Contains(t, out, s)
	}
	assert.Contains(t, v.Footer(), "1/1 clusters")
	assert.Equal(t, "ECS Clusters", v.Title())
	assert.Equal(t, "ecs_clusters", v.ID())

	r := ecsClusterRow(testCluster, ui.TierWide)
	assert.Equal(t, testClusterARN, r.Key)
	assert.Equal(t, []string{"prod-cluster", "ACTIVE", "3", "12", "2", "4", "FARGATE, FARGATE_SPOT"}, []string(r.Cells))
	assert.Equal(t, ui.SortKeyInt(12), r.Sort[3], "the counts sort as numbers")
	assert.Len(t, ecsClusterRow(testCluster, ui.TierMedium).Cells, 5)
	assert.Equal(t, []string{"prod-cluster", "ACTIVE", "12"}, []string(ecsClusterRow(testCluster, ui.TierNarrow).Cells))
}

// A cluster that AWS could not describe is a row with ? and one toast.
func TestECSClusters_UnreadableCluster(t *testing.T) {
	bad := aws.Cluster{Name: "other", ARN: "arn:aws:ecs:us-east-1:1:cluster/other", Err: "ACCESS_DENIED"}
	r := ecsClusterRow(bad, ui.TierWide)
	assert.Equal(t, []string{"other", "?", "?", "?", "?", "?", ""}, []string(r.Cells))
	assert.Empty(t, r.Sort[2], "an unknown count sorts first")

	cmd := ecsUnreadable("cluster", []aws.Cluster{testCluster, bad}, func(c aws.Cluster) (string, string) { return c.Name, c.Err })
	require.NotNil(t, cmd)
	assert.Equal(t, msg.ToastError("Could not describe 1 cluster(s): other (ACCESS_DENIED). Their counts show ?."), cmd())
	assert.Nil(t, ecsUnreadable("cluster", []aws.Cluster{testCluster}, func(c aws.Cluster) (string, string) { return c.Name, c.Err }))

	var many []aws.Cluster
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		many = append(many, aws.Cluster{Name: n, Err: "X"})
	}
	text := ecsUnreadable("cluster", many, func(c aws.Cluster) (string, string) { return c.Name, c.Err })().(msg.ToastMsg)
	assert.Contains(t, text.Text, "Could not describe 5 cluster(s): a (X), b (X), c (X) and 2 more.")

	// The list shows the toast after its load.
	svc := new(awstest.MockECSService)
	svc.On("ListClustersPage", mock.Anything, mock.Anything).Return(&aws.ClusterPage{Clusters: []aws.Cluster{testCluster, bad}}, nil)
	v := NewECSClusters(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	ts := toasts(pump(v, v.Init(), 20))
	require.Len(t, ts, 1)
	assert.Contains(t, ts[0].Text, "other (ACCESS_DENIED)")
	assert.Contains(t, v.Footer(), "2/2 clusters")
}

func TestECSClusters_Detail(t *testing.T) {
	d := ecsClusterDetail(testCluster)
	assert.Equal(t, "prod-cluster", d.PanelTitle)
	require.Len(t, d.Tabs, 2)
	info := d.Tabs[0].Content
	for _, s := range []string{testClusterARN, "Insights", "enhanced", "OVERRIDE (log group /ecs/exec, S3 bucket exec-logs)",
		"FARGATE (base 1, weight 2), FARGATE_SPOT (weight 1)", "Pending tasks    2"} {
		assert.Contains(t, info, s)
	}
	assert.Contains(t, d.Tabs[1].Content, "platform")

	bare := ecsClusterDetail(aws.Cluster{Name: "c", ARN: "arn:c", Status: "ACTIVE"})
	require.Len(t, bare.Tabs, 1, "no tags, no Tags tab")
	assert.Contains(t, bare.Tabs[0].Content, "Insights         -")

	failed := ecsClusterDetail(aws.Cluster{Name: "x", ARN: "arn:x", Err: "ACCESS_DENIED"})
	assert.Contains(t, failed.Tabs[0].Content, "Error            ACCESS_DENIED")
}

// enter opens the services of the cluster, d the detail, with no call.
func TestECSClusters_Keys(t *testing.T) {
	v, svc := newClusters(t, testCluster)
	_, cmd := v.Update(keyPress('d'))
	require.NotNil(t, cmd)
	d, ok := cmd().(listDetailMsg)
	require.True(t, ok)
	assert.Equal(t, "prod-cluster", d.panel.PanelTitle)

	_, cmd = v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	assert.Equal(t, msg.NavigateMsg{ViewID: "ecs_services", Params: map[string]string{"cluster": testClusterARN}}, cmd())
	svc.AssertNumberOfCalls(t, "ListClustersPage", 1)

	// A focus on a missing name gives a toast.
	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: "nope"})
	assert.Equal(t, []msg.ToastMsg{msg.ToastInfo("No cluster named nope in the loaded clusters")}, toasts(pump(v, cmd, 3)))

	_, cmd = v.Update(keyPress('y'))
	ts := toasts(collectMsgs(cmd))
	require.NotEmpty(t, ts)
	assert.Contains(t, ts[0].Text, testClusterARN)

	assert.Equal(t, []string{"enter", "d", "y", "s/S", "/", "r"}, hintKeys(NewECSClusters(nil).KeyMap()))
	assert.Contains(t, v.View().Content, ui.StateColor("ACTIVE"), "the Status column has its style")

	// enter waits while the filter is open.
	v.Update(keyPress('/'))
	_, cmd = v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Empty(t, navigations(collectMsgs(cmd)))
}

func TestECSClusters_ErrorScreenBlocksEnter(t *testing.T) {
	svc := new(awstest.MockECSService)
	svc.On("ListClustersPage", mock.Anything, mock.Anything).Return(nil, errors.New("boom"))
	v := NewECSClusters(svc)
	v.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	pump(v, v.Init(), 20)
	require.Error(t, v.err)
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Nil(t, cmd)
}

func TestECSStrategyText(t *testing.T) {
	assert.Empty(t, ecsStrategyText(nil))
	assert.Equal(t, "FARGATE (weight 0)", ecsStrategyText([]aws.CapacityProviderItem{{Name: "FARGATE"}}))
}

// hintKeys returns the keys of the hints, in order.
func hintKeys(hs []ui.HintBinding) []string {
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		out = append(out, h.Binding.Help().Key)
	}
	return out
}
