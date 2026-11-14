//go:build smoke

package smoke

import "testing"

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
