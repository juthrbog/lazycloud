package aws

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Pin test for the jump design (docs/design/jump.md). The other hazards are
// pinned in internal/app, internal/views and internal/ui.

// H11, fixed in PR 3: GetBucketRegion does not confirm that a bucket
// exists. With an endpoint (LocalStack) it makes no call at all, so a
// missing bucket gives the session Region and no error. On AWS it also takes
// the Region from a denied HeadBucket (s3_region.go:55-56). PR 3 adds a read
// that confirms one bucket, and this test then asserts its not-found error.
func TestJumpHazard_BucketRegionDoesNotConfirmABucket(t *testing.T) {
	// Port 1 refuses every connection, so a call would fail.
	c := &Client{Region: "eu-west-1", Endpoint: "http://127.0.0.1:1"}
	region, err := NewS3Service(c).GetBucketRegion(context.Background(), "no-such-bucket-at-all")
	require.NoError(t, err)
	assert.Equal(t, "eu-west-1", region)
}
