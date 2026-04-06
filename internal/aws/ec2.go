package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// InstancePage holds one page of EC2 instances for progressive loading.
type InstancePage struct {
	Instances    []Instance
	HasMorePages bool
	Token        *string
}

// EC2Service defines all EC2 operations that views depend on.
type EC2Service interface {
	ListInstances(ctx context.Context) ([]Instance, error)
	ListInstancesPage(ctx context.Context, token *string) (*InstancePage, error)
	GetInstanceDetail(ctx context.Context, instanceID string) (*InstanceDetail, error)
	StartInstance(ctx context.Context, instanceID string) error
	StopInstance(ctx context.Context, instanceID string) error
	RebootInstance(ctx context.Context, instanceID string) error
	TerminateInstance(ctx context.Context, instanceID string) error
	StartInstances(ctx context.Context, ids []string) error
	StopInstances(ctx context.Context, ids []string) error
	RebootInstances(ctx context.Context, ids []string) error
	TerminateInstances(ctx context.Context, ids []string) error
	ListOwnedAMIs(ctx context.Context) ([]AMI, error)
	SearchAMIs(ctx context.Context, query string) ([]AMI, error)
	ListSecurityGroups(ctx context.Context) ([]SecurityGroup, error)
	GetSecurityGroup(ctx context.Context, groupID string) (*SecurityGroup, error)
}

// EC2ServiceImpl is the real AWS-backed implementation of EC2Service.
type EC2ServiceImpl struct {
	client *Client
}

// NewEC2Service creates a real EC2 service backed by the given AWS client.
func NewEC2Service(client *Client) *EC2ServiceImpl {
	return &EC2ServiceImpl{client: client}
}

var _ EC2Service = (*EC2ServiceImpl)(nil)

// EC2Client returns an EC2 service client configured for the current profile/region/endpoint.
func (c *Client) EC2Client() *ec2.Client {
	return ec2.NewFromConfig(c.Config, func(o *ec2.Options) {
		if c.Endpoint != "" {
			o.BaseEndpoint = aws.String(c.Endpoint)
		}
	})
}

// SSMSessionCmd returns an exec.Cmd to start an SSM session for the given instance.
// Requires the AWS CLI and session-manager-plugin to be installed.
// The label is displayed as a banner before the session starts (e.g. "my-server (i-abc123)").
func (c *Client) SSMSessionCmd(instanceID, label string) *exec.Cmd {
	args := []string{"ssm", "start-session", "--target", instanceID}
	if c.Region != "" {
		args = append(args, "--region", c.Region)
	}
	if c.Profile != "" {
		args = append(args, "--profile", c.Profile)
	}
	// Build a shell command that prints a banner then execs the session
	fullArgs := append([]string{"aws"}, args...)
	awsCmd := strings.Join(fullArgs, " ")
	banner := fmt.Sprintf("\033[1;36m── SSM Session: %s ──\033[0m\n", label)
	shell := fmt.Sprintf("printf '%s' && %s", banner, awsCmd)
	return exec.Command("sh", "-c", shell) //nolint:gosec // SSM session command is constructed from trusted AWS SDK args
}

// SSMPluginAvailable returns true if the session-manager-plugin is installed.
func SSMPluginAvailable() bool {
	_, err := exec.LookPath("session-manager-plugin")
	return err == nil
}

// Instance represents an EC2 instance in list views.
type Instance struct {
	ID               string
	Name             string
	State            string
	Type             string
	PrivateIP        string
	PublicIP         string
	LaunchTime       time.Time
	VpcID            string
	SubnetID         string
	AvailabilityZone string
	KeyName          string
	Platform         string
	Architecture     string
}

// InstanceDetail holds the full metadata for a single EC2 instance,
// suitable for JSON serialization and display in the detail panel.
type InstanceDetail struct {
	InstanceID       string            `json:"instance_id"`
	Name             string            `json:"name,omitempty"`
	State            string            `json:"state"`
	StateReason      string            `json:"state_reason,omitempty"`
	InstanceType     string            `json:"instance_type"`
	Platform         string            `json:"platform,omitempty"`
	Architecture     string            `json:"architecture,omitempty"`
	PrivateIP        string            `json:"private_ip,omitempty"`
	PublicIP         string            `json:"public_ip,omitempty"`
	PrivateDNS       string            `json:"private_dns,omitempty"`
	PublicDNS        string            `json:"public_dns,omitempty"`
	VpcID            string            `json:"vpc_id,omitempty"`
	SubnetID         string            `json:"subnet_id,omitempty"`
	AvailabilityZone string            `json:"availability_zone,omitempty"`
	KeyName          string            `json:"key_name,omitempty"`
	AMI              string            `json:"ami,omitempty"`
	LaunchTime       string            `json:"launch_time,omitempty"`
	SecurityGroups   []SecurityGroupRef `json:"security_groups,omitempty"`
	IAMRole          string            `json:"iam_role,omitempty"`
	RootDeviceType   string            `json:"root_device_type,omitempty"`
	RootDeviceName   string            `json:"root_device_name,omitempty"`
	Tags             map[string]string `json:"tags,omitempty"`
}

// SecurityGroupRef is a lightweight reference to a security group.
type SecurityGroupRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SecurityGroup represents an EC2 security group with its rules.
type SecurityGroup struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Description   string              `json:"description"`
	VpcID         string              `json:"vpc_id"`
	OwnerID       string              `json:"owner_id"`
	ARN           string              `json:"arn"`
	InboundRules  []SecurityGroupRule `json:"inbound_rules"`
	OutboundRules []SecurityGroupRule `json:"outbound_rules"`
	Tags          map[string]string   `json:"tags,omitempty"`
}

// SecurityGroupRule represents a single inbound or outbound rule.
type SecurityGroupRule struct {
	Protocol    string   `json:"protocol"`
	FromPort    int32    `json:"from_port"`
	ToPort      int32    `json:"to_port"`
	CIDRs       []string `json:"cidrs,omitempty"`
	IPv6CIDRs   []string `json:"ipv6_cidrs,omitempty"`
	SGRefs      []string `json:"sg_refs,omitempty"`
	PrefixLists []string `json:"prefix_lists,omitempty"`
	Description string   `json:"description,omitempty"`
}

// DetailJSON returns the security group as indented JSON.
func (sg *SecurityGroup) DetailJSON() string {
	b, _ := json.MarshalIndent(sg, "", "  ")
	return string(b)
}

// DetailJSON returns the instance detail as indented JSON.
func (d *InstanceDetail) DetailJSON() string {
	b, _ := json.MarshalIndent(d, "", "  ")
	return string(b)
}

// ListInstances returns all EC2 instances, handling pagination automatically.
func (svc *EC2ServiceImpl) ListInstances(ctx context.Context) ([]Instance, error) {
	ec2c := svc.client.EC2Client()

	var instances []Instance
	var nextToken *string

	for {
		input := &ec2.DescribeInstancesInput{
			NextToken: nextToken,
		}
		output, err := ec2c.DescribeInstances(ctx, input)
		if err != nil {
			return nil, err
		}

		for _, reservation := range output.Reservations {
			for _, inst := range reservation.Instances {
				instances = append(instances, mapInstance(inst))
			}
		}

		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	return instances, nil
}

// ListInstancesPage returns a single page of EC2 instances for progressive loading.
func (svc *EC2ServiceImpl) ListInstancesPage(ctx context.Context, token *string) (*InstancePage, error) {
	ec2c := svc.client.EC2Client()

	output, err := ec2c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		NextToken: token,
	})
	if err != nil {
		return nil, err
	}

	var instances []Instance
	for _, reservation := range output.Reservations {
		for _, inst := range reservation.Instances {
			instances = append(instances, mapInstance(inst))
		}
	}

	return &InstancePage{
		Instances:    instances,
		HasMorePages: output.NextToken != nil,
		Token:        output.NextToken,
	}, nil
}

// GetInstanceDetail returns full metadata for a single instance.
func (svc *EC2ServiceImpl) GetInstanceDetail(ctx context.Context, instanceID string) (*InstanceDetail, error) {
	ec2c := svc.client.EC2Client()

	output, err := ec2c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return nil, err
	}

	for _, reservation := range output.Reservations {
		for _, inst := range reservation.Instances {
			return mapInstanceDetail(inst), nil
		}
	}

	return nil, nil
}

// StartInstance starts a stopped EC2 instance.
func (svc *EC2ServiceImpl) StartInstance(ctx context.Context, instanceID string) error {
	ec2c := svc.client.EC2Client()
	_, err := ec2c.StartInstances(ctx, &ec2.StartInstancesInput{
		InstanceIds: []string{instanceID},
	})
	return err
}

// StopInstance stops a running EC2 instance.
func (svc *EC2ServiceImpl) StopInstance(ctx context.Context, instanceID string) error {
	ec2c := svc.client.EC2Client()
	_, err := ec2c.StopInstances(ctx, &ec2.StopInstancesInput{
		InstanceIds: []string{instanceID},
	})
	return err
}

// RebootInstance reboots a running EC2 instance.
func (svc *EC2ServiceImpl) RebootInstance(ctx context.Context, instanceID string) error {
	ec2c := svc.client.EC2Client()
	_, err := ec2c.RebootInstances(ctx, &ec2.RebootInstancesInput{
		InstanceIds: []string{instanceID},
	})
	return err
}

// TerminateInstance terminates an EC2 instance. This is irreversible.
func (svc *EC2ServiceImpl) TerminateInstance(ctx context.Context, instanceID string) error {
	ec2c := svc.client.EC2Client()
	_, err := ec2c.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	})
	return err
}

// StartInstances starts multiple stopped EC2 instances.
func (svc *EC2ServiceImpl) StartInstances(ctx context.Context, ids []string) error {
	ec2c := svc.client.EC2Client()
	_, err := ec2c.StartInstances(ctx, &ec2.StartInstancesInput{
		InstanceIds: ids,
	})
	return err
}

// StopInstances stops multiple running EC2 instances.
func (svc *EC2ServiceImpl) StopInstances(ctx context.Context, ids []string) error {
	ec2c := svc.client.EC2Client()
	_, err := ec2c.StopInstances(ctx, &ec2.StopInstancesInput{
		InstanceIds: ids,
	})
	return err
}

// RebootInstances reboots multiple running EC2 instances.
func (svc *EC2ServiceImpl) RebootInstances(ctx context.Context, ids []string) error {
	ec2c := svc.client.EC2Client()
	_, err := ec2c.RebootInstances(ctx, &ec2.RebootInstancesInput{
		InstanceIds: ids,
	})
	return err
}

// TerminateInstances terminates multiple EC2 instances. This is irreversible.
func (svc *EC2ServiceImpl) TerminateInstances(ctx context.Context, ids []string) error {
	ec2c := svc.client.EC2Client()
	_, err := ec2c.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: ids,
	})
	return err
}

// AMI represents an EC2 Amazon Machine Image.
type AMI struct {
	ID           string
	Name         string
	OwnerID      string
	OwnerAlias   string
	Architecture string
	State        string
	CreationDate string
}

// ListOwnedAMIs returns all AMIs owned by the current account.
func (svc *EC2ServiceImpl) ListOwnedAMIs(ctx context.Context) ([]AMI, error) {
	ec2c := svc.client.EC2Client()
	output, err := ec2c.DescribeImages(ctx, &ec2.DescribeImagesInput{
		Owners: []string{"self"},
	})
	if err != nil {
		return nil, err
	}
	amis := make([]AMI, 0, len(output.Images))
	for _, img := range output.Images {
		amis = append(amis, mapAMI(img))
	}
	return amis, nil
}

// SearchAMIs searches public AMIs by name (max 100 results).
func (svc *EC2ServiceImpl) SearchAMIs(ctx context.Context, query string) ([]AMI, error) {
	ec2c := svc.client.EC2Client()
	output, err := ec2c.DescribeImages(ctx, &ec2.DescribeImagesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("name"), Values: []string{"*" + query + "*"}},
			{Name: aws.String("state"), Values: []string{"available"}},
		},
		MaxResults: aws.Int32(100),
	})
	if err != nil {
		return nil, err
	}
	amis := make([]AMI, 0, len(output.Images))
	for _, img := range output.Images {
		amis = append(amis, mapAMI(img))
	}
	return amis, nil
}

// mapAMI extracts list-view fields from an SDK image.
func mapAMI(img ec2types.Image) AMI {
	return AMI{
		ID:           aws.ToString(img.ImageId),
		Name:         aws.ToString(img.Name),
		OwnerID:      aws.ToString(img.OwnerId),
		OwnerAlias:   aws.ToString(img.ImageOwnerAlias),
		Architecture: string(img.Architecture),
		State:        string(img.State),
		CreationDate: aws.ToString(img.CreationDate),
	}
}

// ListSecurityGroups returns all security groups, handling pagination automatically.
func (svc *EC2ServiceImpl) ListSecurityGroups(ctx context.Context) ([]SecurityGroup, error) {
	ec2c := svc.client.EC2Client()

	var groups []SecurityGroup
	var nextToken *string

	for {
		output, err := ec2c.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
			NextToken: nextToken,
		})
		if err != nil {
			return nil, err
		}

		for _, sg := range output.SecurityGroups {
			groups = append(groups, mapSecurityGroup(sg))
		}

		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	return groups, nil
}

// GetSecurityGroup returns a single security group by ID.
func (svc *EC2ServiceImpl) GetSecurityGroup(ctx context.Context, groupID string) (*SecurityGroup, error) {
	ec2c := svc.client.EC2Client()

	output, err := ec2c.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		GroupIds: []string{groupID},
	})
	if err != nil {
		return nil, err
	}

	if len(output.SecurityGroups) == 0 {
		return nil, nil
	}

	sg := mapSecurityGroup(output.SecurityGroups[0])
	return &sg, nil
}

func mapSecurityGroup(sg ec2types.SecurityGroup) SecurityGroup {
	tags := make(map[string]string, len(sg.Tags))
	for _, tag := range sg.Tags {
		tags[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}

	g := SecurityGroup{
		ID:            aws.ToString(sg.GroupId),
		Name:          aws.ToString(sg.GroupName),
		Description:   aws.ToString(sg.Description),
		VpcID:         aws.ToString(sg.VpcId),
		OwnerID:       aws.ToString(sg.OwnerId),
		ARN:           aws.ToString(sg.SecurityGroupArn),
		InboundRules:  mapIpPermissions(sg.IpPermissions),
		OutboundRules: mapIpPermissions(sg.IpPermissionsEgress),
	}
	if len(tags) > 0 {
		g.Tags = tags
	}
	return g
}

func mapIpPermissions(perms []ec2types.IpPermission) []SecurityGroupRule {
	rules := make([]SecurityGroupRule, 0, len(perms))
	for _, p := range perms {
		r := SecurityGroupRule{
			Protocol: aws.ToString(p.IpProtocol),
			FromPort: aws.ToInt32(p.FromPort),
			ToPort:   aws.ToInt32(p.ToPort),
		}
		for _, cidr := range p.IpRanges {
			r.CIDRs = append(r.CIDRs, aws.ToString(cidr.CidrIp))
			if r.Description == "" && cidr.Description != nil {
				r.Description = *cidr.Description
			}
		}
		for _, cidr := range p.Ipv6Ranges {
			r.IPv6CIDRs = append(r.IPv6CIDRs, aws.ToString(cidr.CidrIpv6))
			if r.Description == "" && cidr.Description != nil {
				r.Description = *cidr.Description
			}
		}
		for _, pair := range p.UserIdGroupPairs {
			r.SGRefs = append(r.SGRefs, aws.ToString(pair.GroupId))
			if r.Description == "" && pair.Description != nil {
				r.Description = *pair.Description
			}
		}
		for _, pl := range p.PrefixListIds {
			r.PrefixLists = append(r.PrefixLists, aws.ToString(pl.PrefixListId))
			if r.Description == "" && pl.Description != nil {
				r.Description = *pl.Description
			}
		}
		rules = append(rules, r)
	}
	return rules
}

// mapInstance extracts list-view fields from an SDK instance.
func mapInstance(inst ec2types.Instance) Instance {
	i := Instance{
		ID:    aws.ToString(inst.InstanceId),
		State: string(inst.State.Name),
		Type:  string(inst.InstanceType),
	}

	for _, tag := range inst.Tags {
		if aws.ToString(tag.Key) == "Name" {
			i.Name = aws.ToString(tag.Value)
			break
		}
	}

	if inst.PrivateIpAddress != nil {
		i.PrivateIP = *inst.PrivateIpAddress
	}
	if inst.PublicIpAddress != nil {
		i.PublicIP = *inst.PublicIpAddress
	}
	if inst.Placement != nil {
		i.AvailabilityZone = aws.ToString(inst.Placement.AvailabilityZone)
	}
	if inst.LaunchTime != nil {
		i.LaunchTime = *inst.LaunchTime
	}
	i.VpcID = aws.ToString(inst.VpcId)
	i.SubnetID = aws.ToString(inst.SubnetId)
	i.KeyName = aws.ToString(inst.KeyName)
	i.Platform = aws.ToString(inst.PlatformDetails)
	i.Architecture = string(inst.Architecture)

	return i
}

// mapInstanceDetail builds the full detail struct from an SDK instance.
func mapInstanceDetail(inst ec2types.Instance) *InstanceDetail {
	d := &InstanceDetail{
		InstanceID:   aws.ToString(inst.InstanceId),
		State:        string(inst.State.Name),
		InstanceType: string(inst.InstanceType),
		Architecture: string(inst.Architecture),
		Platform:     aws.ToString(inst.PlatformDetails),
		PrivateIP:    aws.ToString(inst.PrivateIpAddress),
		PublicIP:     aws.ToString(inst.PublicIpAddress),
		PrivateDNS:   aws.ToString(inst.PrivateDnsName),
		PublicDNS:    aws.ToString(inst.PublicDnsName),
		VpcID:        aws.ToString(inst.VpcId),
		SubnetID:     aws.ToString(inst.SubnetId),
		KeyName:      aws.ToString(inst.KeyName),
		AMI:          aws.ToString(inst.ImageId),
	}

	if inst.StateReason != nil {
		d.StateReason = aws.ToString(inst.StateReason.Message)
	}

	if inst.Placement != nil {
		d.AvailabilityZone = aws.ToString(inst.Placement.AvailabilityZone)
	}

	if inst.LaunchTime != nil {
		d.LaunchTime = inst.LaunchTime.Format(time.RFC3339)
	}

	if inst.IamInstanceProfile != nil {
		d.IAMRole = aws.ToString(inst.IamInstanceProfile.Arn)
	}

	d.RootDeviceType = string(inst.RootDeviceType)
	d.RootDeviceName = aws.ToString(inst.RootDeviceName)

	for _, sg := range inst.SecurityGroups {
		d.SecurityGroups = append(d.SecurityGroups, SecurityGroupRef{
			ID:   aws.ToString(sg.GroupId),
			Name: aws.ToString(sg.GroupName),
		})
	}

	tags := make(map[string]string, len(inst.Tags))
	for _, tag := range inst.Tags {
		k := aws.ToString(tag.Key)
		v := aws.ToString(tag.Value)
		tags[k] = v
		if k == "Name" {
			d.Name = v
		}
	}
	if len(tags) > 0 {
		d.Tags = tags
	}

	return d
}
