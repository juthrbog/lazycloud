//go:build smoke

package smoke

import (
	"context"
	"strings"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// The jump to a resource against the seed (docs/design/jump.md, PR 3). All
// LocalStack ARNs have the account 000000000000.

const lsAccount = "000000000000"

// A bare instance ID after : opens the instance list with the detail of
// that instance.
func TestJumpToAnInstance(t *testing.T) {
	svc := services(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	page, err := svc.EC2.ListInstancesPage(ctx, nil)
	if err != nil || len(page.Instances) == 0 {
		t.Fatalf("no seeded instance: %v", err)
	}
	id := page.Instances[0].ID

	h := start(t, "")
	h.keys(":" + id)
	h.enter()
	h.waitFor("Instance ID", "Security Groups", id)
}

// :go with a Lambda ARN opens the function list with the detail, and a
// version ARN opens the version list.
func TestJumpToALambdaFunction(t *testing.T) {
	arn := "arn:aws:lambda:" + region + ":" + lsAccount + ":function:orders-api"
	h := start(t, "")
	h.keys(":go " + arn)
	h.enter()
	h.waitFor("Last update", "Active", "Lambda Functions")

	h = start(t, "")
	h.keys(":" + arn + ":$LATEST")
	h.enter()
	h.waitFor("orders-api versions", "orders-api:$LATEST")
}

// A queue URL, in the form that LocalStack 2026.10.4 gives, opens the queue
// list with the detail of the queue.
func TestJumpToAQueue(t *testing.T) {
	h := start(t, "")
	h.keys(":http://sqs." + region + ".localhost.localstack.cloud:4566/" + lsAccount + "/notifications")
	h.enter()
	h.waitFor("SQS Queues", "arn:aws:sqs:"+region+":"+lsAccount+":notifications")
}

// A log group path opens its events, as :logs does.
func TestJumpToALogGroup(t *testing.T) {
	h := start(t, "")
	h.keys(":go /aws/lambda/orders-api")
	h.enter()
	h.waitFor("/aws/lambda/orders-api events")
}

// An ECS service ARN opens the services of its cluster with the detail,
// and a task definition ARN opens the revisions of its family.
func TestJumpToECS(t *testing.T) {
	h := start(t, "")
	h.keys(":arn:aws:ecs:" + region + ":" + lsAccount + ":service/prod-cluster/web")
	h.enter()
	h.waitFor("prod-cluster services", "Circuit breaker")

	h = start(t, "")
	h.keys(":jump arn:aws:ecs:" + region + ":" + lsAccount + ":task-definition/worker:1")
	h.enter()
	h.waitFor("worker revisions", "Network mode")
}

// An S3 object ARN with a space in its key opens its folder with the
// cursor on it. A missing bucket gives a message and opens nothing.
func TestJumpToAnObject(t *testing.T) {
	s3c := services(t).Client.S3Client()
	dir := "jump-smoke-" + uniqueName("d") + "/"
	key := dir + "report 2026.txt"
	// The first row of the folder is another object, so the cursor must
	// move to reach the key.
	first := dir + "0-first.txt"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, k := range []string{first, key} {
		if _, err := s3c.PutObject(ctx, &s3.PutObjectInput{
			Bucket: sdkaws.String("test-bucket-1"), Key: sdkaws.String(k), Body: strings.NewReader("hello"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, k := range []string{first, key} {
			_, _ = s3c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: sdkaws.String("test-bucket-1"), Key: sdkaws.String(k)})
		}
	})

	h := start(t, "")
	h.keys(":arn:aws:s3:::test-bucket-1/" + key)
	h.enter()
	h.waitFor("0-first.txt", "report 2026.txt")
	h.keys("y")
	h.waitFor("Copied: s3://test-bucket-1/" + key)

	h = start(t, "")
	h.keys(":s3://no-such-bucket-" + uniqueName("b"))
	h.enter()
	h.waitFor("No bucket named no-such-bucket-")
}
