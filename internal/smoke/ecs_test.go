//go:build smoke

package smoke

import (
	"context"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// :ecs NAME opens the detail of the seeded cluster, enter opens its
// services, and d opens the detail of web with its deployments
// (docs/design/ecs.md, PR 2).
func TestECSClusterServicesAndDetail(t *testing.T) {
	h := start(t, "")
	h.keys(":ecs prod-cluster")
	h.enter()
	h.waitFor("Insights", "prod-cluster")

	h.esc() // close the panel
	h.waitGone("Insights")
	h.enter()
	h.waitFor("prod-cluster services", "3/3 services")
	h.waitFor("web", "worker", "scheduler")

	h.filter("web")
	h.keys("d")
	h.waitFor("Circuit breaker", "on, with rollback")
	h.keys("2")
	h.waitFor("PRIMARY", "web:1")
}

// enter on web opens its tasks. t on the cluster opens every task of the
// cluster, and x shows a stopped task with its stop code and reason inline
// (docs/design/ecs.md, PR 3). The test stops a task of its own, because
// LocalStack forgets a stopped task after a while. A task from RunTask is
// not part of a service, so only the cluster list has it.
func TestECSTasksAndStoppedTasks(t *testing.T) {
	reason := stopOwnTask(t)
	h := start(t, "")
	h.open("ecs", "ECS Clusters")
	h.filter("prod-cluster")
	h.enter()
	h.waitFor("prod-cluster services", "3/3 services")
	h.filter("web")
	h.enter()
	h.waitFor("web tasks", "RUNNING", "1/1 tasks")

	h.esc()
	h.waitGone("web tasks")
	h.esc()
	h.waitGone("prod-cluster services")
	h.keys("t")
	h.waitFor("prod-cluster tasks", "RUNNING")
	h.keys("x")
	h.waitFor("prod-cluster stopped tasks", "UserInitiated", reason, "AWS keeps stopped tasks")
	h.filter(reason)
	h.keys("d")
	h.waitFor("Stop reason", "lazycloud-smoke")
}

// stopOwnTask runs a task of web in prod-cluster and stops it with a reason
// that no other task has, and returns the reason.
func stopOwnTask(t *testing.T) string {
	t.Helper()
	svc := services(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	subnets, err := svc.Client.EC2Client().DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []ec2types.Filter{{Name: sdkaws.String("default-for-az"), Values: []string{"true"}}},
	})
	if err != nil || len(subnets.Subnets) == 0 {
		t.Fatalf("finding a default subnet: %v", err)
	}
	client := svc.Client.ECSClient()
	run, err := client.RunTask(ctx, &ecs.RunTaskInput{
		Cluster:        sdkaws.String("prod-cluster"),
		TaskDefinition: sdkaws.String("web"),
		LaunchType:     ecstypes.LaunchTypeFargate,
		NetworkConfiguration: &ecstypes.NetworkConfiguration{AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{
			Subnets: []string{sdkaws.ToString(subnets.Subnets[0].SubnetId)},
		}},
		StartedBy: sdkaws.String("lazycloud-smoke"),
	})
	if err != nil || len(run.Tasks) == 0 {
		t.Fatalf("running a task: %v", err)
	}
	reason := uniqueName("stop")
	if _, err := client.StopTask(ctx, &ecs.StopTaskInput{
		Cluster: sdkaws.String("prod-cluster"), Task: run.Tasks[0].TaskArn, Reason: sdkaws.String(reason),
	}); err != nil {
		t.Fatalf("stopping the task: %v", err)
	}
	return reason
}
