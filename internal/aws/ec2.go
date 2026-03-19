package aws

import (
	"context"
	"encoding/json"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// EC2Service defines all EC2 operations that views depend on.
type EC2Service interface {
	ListInstances(ctx context.Context) ([]Instance, error)
	GetInstanceDetail(ctx context.Context, instanceID string) (*InstanceDetail, error)
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
