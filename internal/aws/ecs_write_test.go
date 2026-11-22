package aws

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests of the ECS writes (docs/design/ecs.md, PR 7).

const (
	writeCluster = "arn:aws:ecs:us-east-1:1:cluster/prod"
	writeService = "arn:aws:ecs:us-east-1:1:service/prod/web"
	writeTask    = "arn:aws:ecs:us-east-1:1:task/prod/abc"
)

// ForceDeployment sends ForceNewDeployment and nothing else, so it does
// not change the count or the task definition.
func TestECSForceDeployment(t *testing.T) {
	f, svc := newFakeECS(t)
	f.reply("UpdateService", `{"service":{}}`)
	require.NoError(t, svc.ForceDeployment(context.Background(), writeCluster, writeService))
	require.Len(t, f.calls, 1)
	assert.Equal(t, map[string]any{"cluster": writeCluster, "service": writeService, "forceNewDeployment": true}, f.calls[0].in)

	f.replies["UpdateService"] = func(map[string]any) (int, string) {
		return http.StatusBadRequest, `{"__type":"InvalidParameterException","message":"Unable to update a CODE_DEPLOY service"}`
	}
	err := svc.ForceDeployment(context.Background(), writeCluster, writeService)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "UpdateService")
	assert.Equal(t, "InvalidParameterException", ErrorCode(err))
}

// SetDesiredCount sends the count, also 0, and nothing else.
func TestECSSetDesiredCount(t *testing.T) {
	f, svc := newFakeECS(t)
	f.reply("UpdateService", `{"service":{}}`)
	require.NoError(t, svc.SetDesiredCount(context.Background(), writeCluster, writeService, 0))
	require.NoError(t, svc.SetDesiredCount(context.Background(), writeCluster, writeService, 3))
	require.Len(t, f.calls, 2)
	assert.Equal(t, map[string]any{"cluster": writeCluster, "service": writeService, "desiredCount": float64(0)}, f.calls[0].in)
	assert.InDelta(t, 3, f.calls[1].in["desiredCount"], 0)

	f.replies["UpdateService"] = func(map[string]any) (int, string) {
		return http.StatusBadRequest, `{"__type":"ServiceNotActiveException","message":"Service was not ACTIVE."}`
	}
	err := svc.SetDesiredCount(context.Background(), writeCluster, writeService, 1)
	require.Error(t, err)
	assert.Equal(t, "ServiceNotActiveException", ErrorCode(err))
}

// StopTask sends the full task ARN and the reason, and leaves out an
// empty reason.
func TestECSStopTask(t *testing.T) {
	f, svc := newFakeECS(t)
	f.reply("StopTask", `{"task":{}}`)
	require.NoError(t, svc.StopTask(context.Background(), writeCluster, writeTask, "Stopped by lazycloud"))
	require.NoError(t, svc.StopTask(context.Background(), writeCluster, writeTask, ""))
	require.Len(t, f.calls, 2)
	assert.Equal(t, map[string]any{"cluster": writeCluster, "task": writeTask, "reason": "Stopped by lazycloud"}, f.calls[0].in)
	assert.Equal(t, map[string]any{"cluster": writeCluster, "task": writeTask}, f.calls[1].in)

	f.replies["StopTask"] = func(map[string]any) (int, string) {
		return http.StatusBadRequest, `{"__type":"InvalidParameterException","message":"The referenced task was not found."}`
	}
	err := svc.StopTask(context.Background(), writeCluster, writeTask, "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "StopTask")
	assert.Equal(t, "InvalidParameterException", ErrorCode(err), "the AWS error stays wrapped")
}
