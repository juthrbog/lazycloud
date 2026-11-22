package aws

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Pin test for the jump design (docs/design/jump.md). The other hazards are
// pinned in internal/app, internal/views and internal/ui. PR 3 flips H11.

// H11, fixed in PR 3: GetBucketRegion does not confirm that a bucket
// exists. With an endpoint (LocalStack) it makes no call, so a missing
// bucket gives the session Region and no error. HeadBucket makes the call
// and says that the bucket is missing.
func TestJumpHeadBucketConfirmsABucket(t *testing.T) {
	_, svc := newS3Head(t, map[string]int{"logs": http.StatusOK})
	region, err := svc.GetBucketRegion(context.Background(), "no-such-bucket-at-all")
	require.NoError(t, err)
	assert.Equal(t, "us-east-1", region, "GetBucketRegion still answers with the session Region")

	err = svc.HeadBucket(context.Background(), "no-such-bucket-at-all", "")
	require.ErrorIs(t, err, ErrBucketNotFound)
	require.NoError(t, svc.HeadBucket(context.Background(), "logs", ""))
}
