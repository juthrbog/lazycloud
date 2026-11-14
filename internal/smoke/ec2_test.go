//go:build smoke

package smoke

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// TestEC2StopStart stops and starts an instance through the Manage menu.
// The test launches its own instance, so the seed instances keep their
// state.
func TestEC2StopStart(t *testing.T) {
	name, id, ec2c := launchInstance(t)

	h := start(t, "")
	h.open("ec2", "EC2 Instances")
	h.filter(name)
	h.until("a running instance", instanceRow(id, "running").MatchString)

	h.keys("m")
	h.pick("Stop")
	h.confirm(id)
	h.waitFor("Instance stopped: " + id)
	waitState(t, ec2c, id, ec2types.InstanceStateNameStopped)
	h.refreshUntil("a stopped instance", instanceRow(id, "stopped").MatchString)

	h.keys("m")
	h.pick("Start")
	h.confirm(id)
	h.waitFor("Instance started: " + id)
	waitState(t, ec2c, id, ec2types.InstanceStateNameRunning)
	h.refreshUntil("a running instance", instanceRow(id, "running").MatchString)
}

// refreshUntil presses r every few seconds until cond is true. The list
// reloads once, 2 s after a Manage action. On a slow LocalStack the
// instance can still be in a transitional state then. r during a load
// starts the load again, so the helper presses r only between loads.
func (h *harness) refreshUntil(what string, cond func(string) bool) {
	h.t.Helper()
	last := time.Now()
	h.until(what, func(s string) bool {
		if cond(s) {
			return true
		}
		if strings.Contains(strings.ToLower(s), "loading") {
			last = time.Now() // r during a load starts the load again
			return false
		}
		if time.Since(last) > 3*time.Second {
			h.keys("r")
			last = time.Now()
		}
		return false
	})
}

// waitState waits until AWS reports the instance in state.
func waitState(t *testing.T, ec2c *ec2.Client, id string, state ec2types.InstanceStateName) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for {
		out, err := ec2c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{id}})
		if err == nil && len(out.Reservations) > 0 && len(out.Reservations[0].Instances) > 0 &&
			out.Reservations[0].Instances[0].State != nil && out.Reservations[0].Instances[0].State.Name == state {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("instance %s did not reach %s: %v", id, state, err)
		case <-time.After(time.Second):
		}
	}
}

// instanceRow matches the row of instance id in state. The state column
// follows the Name column.
func instanceRow(id, state string) *regexp.Regexp {
	return regexp.MustCompile(regexp.QuoteMeta(id) + `\s+\S+\s+\S*\s*` + state + `\s`)
}

// launchInstance launches an instance from a seed AMI and terminates it
// when the test ends. It returns the Name tag, the instance ID, and the
// client for later checks.
func launchInstance(t *testing.T) (name, id string, ec2c *ec2.Client) {
	t.Helper()
	ec2c = services(t).Client.EC2Client()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	images, err := ec2c.DescribeImages(ctx, &ec2.DescribeImagesInput{
		Owners:  []string{"self"},
		Filters: []ec2types.Filter{{Name: sdkaws.String("name"), Values: []string{"lazycloud-minimal-x86_64"}}},
	})
	if err != nil || len(images.Images) == 0 {
		t.Fatalf("no seed AMI lazycloud-minimal-x86_64 (run task localstack:seed): %v", err)
	}

	name = uniqueName("ec2")
	out, err := ec2c.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      images.Images[0].ImageId,
		InstanceType: ec2types.InstanceTypeT3Micro,
		MinCount:     sdkaws.Int32(1),
		MaxCount:     sdkaws.Int32(1),
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeInstance,
			Tags:         []ec2types.Tag{{Key: sdkaws.String("Name"), Value: sdkaws.String(name)}},
		}},
	})
	if err != nil {
		t.Fatalf("launching an instance: %v", err)
	}
	id = sdkaws.ToString(out.Instances[0].InstanceId)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := ec2c.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{id}}); err != nil {
			t.Errorf("terminating %s: %v", id, err)
		}
	})

	// LocalStack starts the instance after a moment.
	w := ec2.NewInstanceRunningWaiter(ec2c, func(o *ec2.InstanceRunningWaiterOptions) {
		o.MinDelay, o.MaxDelay = time.Second, 2*time.Second
	})
	if err := w.Wait(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{id}}, time.Minute); err != nil {
		t.Fatalf("waiting for %s to run: %v", id, err)
	}
	return name, id, ec2c
}
