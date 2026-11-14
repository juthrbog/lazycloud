//go:build smoke

package smoke

import (
	"context"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// TestS3CreateDelete creates a bucket with n and deletes it with x.
func TestS3CreateDelete(t *testing.T) {
	name := uniqueName("s3")
	s3c := services(t).Client.S3Client()
	t.Cleanup(func() {
		// The test deletes the bucket itself. This delete is for a test that
		// fails before that.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = s3c.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: sdkaws.String(name)})
	})

	h := start(t, "")
	h.open("s3", "S3")
	h.keys("n")
	h.waitFor("new bucket")
	h.keys(name)
	h.enter()
	h.waitFor("Bucket created: " + name)

	h.filter(name)
	h.waitFor("│ " + name)
	h.keys("x")
	h.confirm(name)
	h.waitFor("Deleted 1 bucket(s)")
	h.waitGone("│ " + name)
}
