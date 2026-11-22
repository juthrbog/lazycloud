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

// H7, fixed in PR 6: SSMSessionCmd passed no --endpoint-url, so an SSM
// session started against LocalStack went to AWS. It now passes the
// endpoint of the client, after the Region and the profile, and ECSExecCmd
// does the same. Without an endpoint, the flag stays out.
func TestECSH7_SessionsPassTheEndpoint(t *testing.T) {
	c := &Client{Region: "us-east-1", Endpoint: "http://localhost:4566"}
	assert.Equal(t, []string{"aws", "ssm", "start-session", "--target=i-abc123",
		"--region=us-east-1", "--endpoint-url=http://localhost:4566"}, c.SSMSessionCmd("i-abc123", "i-abc123").Args())
	exec := c.ECSExecCmd("arn:c", "arn:t", "app", "app")
	assert.Equal(t, "--endpoint-url=http://localhost:4566", exec.Args()[len(exec.Args())-1])

	plain := &Client{Region: "us-east-1"}
	for _, args := range [][]string{plain.SSMSessionCmd("i-abc123", "i-abc123").Args(), plain.ECSExecCmd("arn:c", "arn:t", "app", "app").Args()} {
		for _, a := range args {
			assert.NotContains(t, a, "--endpoint-url")
		}
	}
}

// ECSExecCmd runs /bin/sh in the container with ECS Exec. Each name is its
// own argument, and the label stays out of the arguments (decision D6).
func TestECSExecCmd(t *testing.T) {
	c := &Client{Region: "us-west-2", Profile: "staging"}
	label := "app' ; touch /tmp/pwned ; echo '\x1b]52;c;aGk=\x07"
	e := c.ECSExecCmd("arn:aws:ecs:us-west-2:1:cluster/prod", "arn:aws:ecs:us-west-2:1:task/prod/abc", "app; rm -rf /", label)
	assert.Equal(t, []string{
		"aws", "ecs", "execute-command",
		"--cluster=arn:aws:ecs:us-west-2:1:cluster/prod",
		"--task=arn:aws:ecs:us-west-2:1:task/prod/abc",
		"--container=app; rm -rf /",
		"--interactive", "--command=/bin/sh",
		"--region=us-west-2", "--profile=staging",
	}, e.Args())
	assert.Contains(t, e.Banner(), "Type exit to return.")

	// A name that starts with "-" stays in the argument of its flag, so
	// the CLI does not read it as a flag.
	dash := (&Client{Profile: "-dev"}).ECSExecCmd("c", "t", "-worker", "x")
	assert.Contains(t, dash.Args(), "--container=-worker")
	assert.Contains(t, dash.Args(), "--profile=-dev")
	assert.NotContains(t, dash.Args(), "-worker")
	assert.Contains(t, e.Banner(), "ECS Exec: app' ; touch")
	assert.NotContains(t, e.Banner(), "\x1b]52", "control characters are stripped")
	assert.Nil(t, e.Env(), "the session uses the environment of the app")
}
