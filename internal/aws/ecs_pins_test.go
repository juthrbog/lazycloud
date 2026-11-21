package aws

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Pin tests for the ECS design (docs/design/ecs.md).

// H6, fixed in PR 4: GetStreamEvents passes ResourceNotFoundException for a
// stream that does not exist as a plain error. PR 4 maps it to a new
// ErrLogStreamNotFound, and this test then asserts ErrorIs with it.
func TestECSHazard_MissingStreamHasNoSentinel(t *testing.T) {
	f, svc := newFakeLogs(t)
	f.replies["GetLogEvents"] = func(map[string]any) (int, string) {
		return http.StatusBadRequest, `{"__type":"ResourceNotFoundException","message":"The specified log stream does not exist."}`
	}
	_, err := svc.GetStreamEvents(context.Background(), EventQuery{
		Group: "/ecs/web-app", Stream: "ecs/app/0123456789abcdef",
		Start: time.UnixMilli(1763590000000), End: time.UnixMilli(1763610000000), Limit: 100,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ResourceNotFoundException")
	assert.NotErrorIs(t, err, ErrLogGroupNotFound, "no sentinel for a missing stream")
}

// H7, fixed in PR 6: SSMSessionCmd passes no --endpoint-url, so an SSM session
// started against LocalStack goes to AWS. An ECS exec built the same way
// would do the same.
func TestECSHazard_SSMSessionHasNoEndpoint(t *testing.T) {
	c := &Client{Region: "us-east-1", Endpoint: "http://localhost:4566"}
	assert.NotContains(t, c.SSMSessionCmd("i-abc123", "i-abc123").Args(), "--endpoint-url")
}
