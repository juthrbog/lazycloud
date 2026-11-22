//go:build smoke

package smoke

import (
	"testing"

	"github.com/juthrbog/lazycloud/internal/registry"
)

// TestListsLoad opens every list and filters it to one seed resource, then
// opens the detail of that resource. The filter keeps the test independent
// of other resources in LocalStack.
func TestListsLoad(t *testing.T) {
	cases := []struct {
		command, title string
		seed           string // a resource from cmd/seed/tiers.toml
		detail         string // a text that only the detail shows
	}{
		{"ec2", "EC2 Instances", "web-server-1", "1:Info"},
		{"amis", "AMIs", "lazycloud-minimal-x86_64", `"OwnerID": "000000000000"`},
		{"sgs", "Security Groups", "bastion-sg", "1:Info"},
		{"vpcs", "VPCs", "staging-vpc", "View subnets"},
		{"subnets", "Subnets", "staging-private-1a", "1:Info"},
		{"sqs", "SQS Queues", "notifications", "1:Info"},
		{"s3", "S3", "logs-bucket", `"Encryption": "AES256"`},
		{"logs", "Log Groups", "/aws/lambda/orders-api", "Delete protected"},
		{"lambda", "Lambda Functions", "orders-api", "Last update"},
		{"ecs", "ECS Clusters", "prod-cluster", "Insights"},
		{"taskdefs", "ECS Task Definitions", "worker", "Network mode"},
	}
	// Each list of the registry has a case, so a new service gets a smoke
	// test with a seed resource.
	// The tag search asks for a filter when it opens, so it has its own
	// smoke test: TestJumpTagSearchForm in jump_test.go.
	covered := map[string]bool{"jump_tags": true}
	for _, c := range cases {
		if cmd := registry.LookupCommand(c.command); cmd != nil {
			covered[cmd.ViewID] = true
		}
	}
	for _, s := range registry.Services {
		for _, f := range s.Features {
			if !covered[f.ViewID] {
				t.Errorf("the view %q of %s has no smoke case", f.ViewID, s.Name)
			}
		}
	}
	for _, c := range cases {
		t.Run(c.command, func(t *testing.T) {
			h := start(t, "")
			h.open(c.command, c.title)
			h.filter(c.seed)
			h.waitFor(c.seed)
			h.keys("d")
			h.waitFor(c.detail)
		})
	}
}
