//go:build smoke

package smoke

import (
	"context"
	"strings"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
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
	h.waitFor("prod-cluster stopped tasks", "UserInitiated", "AWS keeps stopped tasks")
	// Older runs can leave more stopped tasks than the screen shows, so the
	// test filters before it looks for its own task.
	h.filter(reason)
	h.keys("d")
	h.waitFor("Stop reason", "lazycloud-smoke")
}

// enter on a task opens its containers, and enter on app opens the log
// stream of the container in the event viewer (docs/design/ecs.md, PR 4).
// The stream is <prefix>/<container>/<task ID>. The test runs a task of its
// own and waits for its first lines, because the lines of the seeded task
// can be older than the first hour of the viewer.
func TestECSContainerLogs(t *testing.T) {
	arn := runOwnTask(t)
	id := arn[strings.LastIndex(arn, "/")+1:]
	stream := "ecs/app/" + id
	waitForStream(t, "/ecs/web-app", stream)

	h := start(t, "")
	h.open("ecs", "ECS Clusters")
	h.filter("prod-cluster")
	h.keys("t")
	h.waitFor("prod-cluster tasks", id)
	h.filter(id)
	h.enter()
	h.waitFor(id+" containers", "nginx:alpine", "/ecs/web-app", "1/1 containers")
	h.enter()
	h.waitFor(stream, "docker-entrypoint")
}

// :taskdefs lists the families. enter on worker opens its two revisions,
// newest first, and d opens the detail of one. $ on web shows the values
// of its environment, which d hides (docs/design/ecs.md, PR 5).
func TestECSTaskDefinitions(t *testing.T) {
	h := start(t, "")
	h.open("taskdefs", "ECS Task Definitions")
	h.filter("worker")
	h.enter()
	h.waitFor("worker revisions", "2/2 revisions", "task-definition/worker:2", "task-definition/worker:1")
	h.keys("d")
	h.waitFor("worker:2", "Network mode", "awsvpc")

	h = start(t, "")
	h.open("taskdefs", "ECS Task Definitions")
	h.filter("web")
	// A family from an older research run can also match, and LocalStack
	// lists it first. j moves to web then; with one row it does nothing.
	if strings.Contains(h.screenText(), "zz-research-web") {
		h.keys("j")
	}
	h.keys("d")
	h.waitFor("Environment")
	h.keys("3")
	h.waitFor("DB_HOST", "••••", "Press $ in the list")
	h.esc()
	h.keys("$")
	h.waitFor("web.db.internal")
}

// c sets the desired count of scheduler, f forces a new deployment of it,
// and X stops a task after the typed phrase (docs/design/ecs.md, PR 7).
// scheduler has a desired count of 0, so the count stays 0 and the
// deployment starts no container. The test stops a task of its own.
func TestECSWriteActions(t *testing.T) {
	arn := runOwnTask(t)
	id := arn[strings.LastIndex(arn, "/")+1:]

	h := start(t, "")
	h.open("ecs", "ECS Clusters")
	h.filter("prod-cluster")
	h.enter()
	h.waitFor("prod-cluster services", "3/3 services")
	h.filter("scheduler")
	h.keys("c")
	h.waitFor("Desired count of scheduler", "Running 0, desired 0.")
	h.submitForm()
	h.waitFor("Set the desired count of scheduler to 0")
	h.keys("f")
	h.confirm("scheduler")
	h.waitFor("Started a new deployment of scheduler")

	h.esc()
	h.waitGone("prod-cluster services")
	h.keys("t")
	h.waitFor("prod-cluster tasks", id)
	h.filter(id)
	h.keys("X")
	h.confirm("stop 1 task")
	h.waitFor("Stopped 1 task(s)")

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := services(t).Client.ECSClient().DescribeTasks(ctx, &ecs.DescribeTasksInput{
		Cluster: sdkaws.String("prod-cluster"), Tasks: []string{arn},
	})
	if err != nil || len(out.Tasks) != 1 {
		t.Fatalf("reading the task: %v", err)
	}
	if got := sdkaws.ToString(out.Tasks[0].StoppedReason); got != "Stopped by lazycloud" {
		t.Errorf("the stop reason is %q", got)
	}
}

// runOwnTask runs a task of web in prod-cluster and returns its ARN. The
// task stops at the end of the test.
func runOwnTask(t *testing.T) string {
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
	arn := sdkaws.ToString(run.Tasks[0].TaskArn)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		// A task that the test stopped itself gives an error here; that
		// is fine.
		_, _ = client.StopTask(ctx, &ecs.StopTaskInput{Cluster: sdkaws.String("prod-cluster"), Task: &arn})
	})
	return arn
}

// waitForStream waits until the stream of the group has events.
func waitForStream(t *testing.T, group, stream string) {
	t.Helper()
	client := services(t).Client.LogsClient()
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		out, err := client.DescribeLogStreams(ctx, &cloudwatchlogs.DescribeLogStreamsInput{
			LogGroupName: sdkaws.String(group), LogStreamNamePrefix: sdkaws.String(stream),
		})
		cancel()
		if err == nil && len(out.LogStreams) > 0 && out.LogStreams[0].LastEventTimestamp != nil {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("no events in %s %s after a minute", group, stream)
}

// stopOwnTask runs a task of web in prod-cluster and stops it with a reason
// that no other task has, and returns the reason.
func stopOwnTask(t *testing.T) string {
	t.Helper()
	arn := runOwnTask(t)
	reason := uniqueName("stop")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := services(t).Client.ECSClient().StopTask(ctx, &ecs.StopTaskInput{
		Cluster: sdkaws.String("prod-cluster"), Task: &arn, Reason: sdkaws.String(reason),
	}); err != nil {
		t.Fatalf("stopping the task: %v", err)
	}
	return reason
}
