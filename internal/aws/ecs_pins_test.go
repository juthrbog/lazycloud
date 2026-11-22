package aws

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Pin tests for the ECS design (docs/design/ecs.md).

// H6, fixed in PR 4: GetStreamEvents passed ResourceNotFoundException for a
// stream that does not exist as a plain error. It now returns
// ErrLogStreamNotFound, with the stream and the group in the text. Other
// errors stay as they are.
func TestECSH6_MissingStreamHasASentinel(t *testing.T) {
	f, svc := newFakeLogs(t)
	f.replies["GetLogEvents"] = func(map[string]any) (int, string) {
		return http.StatusBadRequest, `{"__type":"ResourceNotFoundException","message":"The specified log stream does not exist."}`
	}
	q := EventQuery{
		Group: "/ecs/web-app", Stream: "ecs/app/0123456789abcdef",
		Start: time.UnixMilli(1763590000000), End: time.UnixMilli(1763610000000), Limit: 100,
	}
	_, err := svc.GetStreamEvents(context.Background(), q)
	require.ErrorIs(t, err, ErrLogStreamNotFound)
	require.NotErrorIs(t, err, ErrLogGroupNotFound)
	assert.Contains(t, err.Error(), "ecs/app/0123456789abcdef in /ecs/web-app")
	_, isAWS := errors.AsType[*cwltypes.ResourceNotFoundException](err)
	assert.True(t, isAWS, "the AWS error stays in the chain")
	assert.Contains(t, ErrorText(err), "The specified log stream does not exist.")

	f.replies["GetLogEvents"] = func(map[string]any) (int, string) {
		return http.StatusBadRequest, `{"__type":"InvalidParameterException","message":"bad time"}`
	}
	_, err = svc.GetStreamEvents(context.Background(), q)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrLogStreamNotFound)
	assert.Contains(t, err.Error(), "InvalidParameterException")
}

// H7, fixed in PR 6: SSMSessionCmd passes no --endpoint-url, so an SSM session
// started against LocalStack goes to AWS. An ECS exec built the same way
// would do the same.
func TestECSHazard_SSMSessionHasNoEndpoint(t *testing.T) {
	c := &Client{Region: "us-east-1", Endpoint: "http://localhost:4566"}
	assert.NotContains(t, c.SSMSessionCmd("i-abc123", "i-abc123").Args(), "--endpoint-url")
}
