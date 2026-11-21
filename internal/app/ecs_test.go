package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
)

// App tests of the ECS cluster and service lists (docs/design/ecs.md, PR 2).

// :ecs NAME opens the cluster list with a focus on that cluster. The alias
// takes it too. :ecs alone opens the list.
func TestECSCommandTakesAClusterName(t *testing.T) {
	m := newTestModel(140, 40)
	_, cmd := m.executeCommand("ecs prod-cluster")
	assert.Equal(t, []any{msg.NavigateMsg{ViewID: "ecs_clusters",
		Params: map[string]string{"focus": "prod-cluster"}}}, toAny(collectMsgs(cmd)))

	_, cmd = m.executeCommand("clusters  bulk-cluster ")
	assert.Equal(t, []any{msg.NavigateMsg{ViewID: "ecs_clusters",
		Params: map[string]string{"focus": "bulk-cluster"}}}, toAny(collectMsgs(cmd)))

	_, cmd = m.executeCommand("ecs")
	assert.Equal(t, []any{msg.NavigateMsg{ViewID: "ecs_clusters"}}, toAny(collectMsgs(cmd)))
}

// The focus of :ecs NAME reaches the list after its load and opens the
// detail of the cluster. enter then opens its services.
func TestECSFocusOpensTheDetailAndEnterTheServices(t *testing.T) {
	m := newTestModel(160, 40)
	mockAllServices(&m)
	svc := new(awstest.MockECSService)
	m.svc.ECS = svc
	arn := "arn:aws:ecs:us-east-1:1:cluster/prod-cluster"
	c := aws.Cluster{Name: "prod-cluster", ARN: arn, Status: "ACTIVE", ContainerInsights: "enabled"}
	svc.On("ListClustersPage", mock.Anything, mock.Anything).Return(&aws.ClusterPage{Clusters: []aws.Cluster{c}}, nil)
	svc.On("ListServicesPage", mock.Anything, arn, mock.Anything).Return(&aws.ServicePage{}, nil)

	m, cmd := update(t, m, msg.NavigateMsg{ViewID: "ecs_clusters", Params: map[string]string{"focus": "prod-cluster"}})
	require.Equal(t, "ecs_clusters", m.nav.Current().ID())
	m = pump(t, m, cmd)
	require.True(t, m.panels.IsOpen(), "the focus opens the detail")
	assert.Contains(t, rendered(m), "enabled")

	// The panel has the focus; esc gives it back to the list.
	m, _ = update(t, m, press("esc"))
	m, cmd = update(t, m, press("enter"))
	m = pump(t, m, cmd)
	assert.Equal(t, "ecs_services:"+arn, m.nav.Current().ID())
	assert.Contains(t, rendered(m), "prod-cluster services")
}
