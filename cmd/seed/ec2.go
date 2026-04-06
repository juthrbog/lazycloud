package main

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	awslib "github.com/juthrbog/lazycloud/internal/aws"
)

type ec2Seeder struct {
	client *awslib.Client
	config EC2SeedConfig
}

func newEC2Seeder(client *awslib.Client, cfg EC2SeedConfig) *ec2Seeder {
	return &ec2Seeder{client: client, config: cfg}
}

func (e *ec2Seeder) Name() string { return "ec2" }

func (e *ec2Seeder) Seed(ctx context.Context) error {
	ec2c := e.client.EC2Client()

	// Phase 1: AMIs (must complete before instances).
	amiMap, err := e.seedAMIs(ctx, ec2c)
	if err != nil {
		return err
	}

	// Phase 2: Security groups (must complete before instances).
	sgMap, err := e.seedSecurityGroups(ctx, ec2c)
	if err != nil {
		return err
	}

	// Phase 3: Instances (depends on AMI and SG IDs).
	return e.seedInstances(ctx, ec2c, amiMap, sgMap)
}

// seedAMIs registers base and extra AMIs, returning a name→ID map of all AMIs.
func (e *ec2Seeder) seedAMIs(ctx context.Context, ec2c *ec2.Client) (map[string]string, error) {
	existing, err := e.listExistingAMIs(ctx, ec2c)
	if err != nil {
		return nil, fmt.Errorf("listing AMIs: %w", err)
	}

	fmt.Printf("  EC2 AMIs...")

	// Register base AMIs.
	for _, a := range e.config.AMIs {
		id, err := e.ensureAMI(ctx, ec2c, a.Name, a.Architecture, existing)
		if err != nil {
			return nil, err
		}
		existing[a.Name] = id
	}

	// Register extra generated AMIs.
	archs := []string{"x86_64", "arm64"}
	for i := range e.config.ExtraAMIs {
		name := fmt.Sprintf("lazycloud-gen-%03d", i+1)
		arch := archs[i%len(archs)]
		id, err := e.ensureAMI(ctx, ec2c, name, arch, existing)
		if err != nil {
			return nil, err
		}
		existing[name] = id
	}

	fmt.Println(" done")
	return existing, nil
}

// seedInstances launches base and extra instances, stopping some as configured.
func (e *ec2Seeder) seedInstances(ctx context.Context, ec2c *ec2.Client, amiMap, sgMap map[string]string) error {
	existing, err := e.listExistingInstances(ctx, ec2c)
	if err != nil {
		return fmt.Errorf("listing instances: %w", err)
	}

	fmt.Printf("  EC2 instances...")

	var toStop []string

	// Launch base instances.
	for _, inst := range e.config.Instances {
		amiID, ok := amiMap[inst.AMIRef]
		if !ok {
			return fmt.Errorf("instance %q references unknown AMI %q", inst.Name, inst.AMIRef)
		}
		var sgIDs []string
		for _, ref := range inst.SGRefs {
			if sgID, ok := sgMap[ref]; ok {
				sgIDs = append(sgIDs, sgID)
			}
		}
		id, err := e.ensureInstance(ctx, ec2c, inst.Name, amiID, inst.InstanceType, sgIDs, existing)
		if err != nil {
			return err
		}
		if inst.Stopped {
			toStop = append(toStop, id)
		}
	}

	// Launch extra generated instances.
	// Pick AMIs round-robin from the base AMI list.
	baseAMINames := make([]string, len(e.config.AMIs))
	for i, a := range e.config.AMIs {
		baseAMINames[i] = a.Name
	}
	instanceTypes := []string{"t3.micro", "t3.small", "t3.medium", "t2.micro"}
	stopCount := int(e.config.StopFraction * float64(e.config.ExtraInstances))

	for i := range e.config.ExtraInstances {
		name := fmt.Sprintf("worker-%03d", i+1)
		amiName := baseAMINames[i%len(baseAMINames)]
		amiID := amiMap[amiName]
		iType := instanceTypes[i%len(instanceTypes)]

		id, err := e.ensureInstance(ctx, ec2c, name, amiID, iType, nil, existing)
		if err != nil {
			return err
		}
		if i < stopCount {
			toStop = append(toStop, id)
		}
	}

	// Stop instances that should be in stopped state.
	if len(toStop) > 0 {
		if err := e.stopInstances(ctx, ec2c, toStop); err != nil {
			return fmt.Errorf("stopping instances: %w", err)
		}
	}

	fmt.Println(" done")
	return nil
}

// seedSecurityGroups creates base and extra security groups, returning a name→ID map.
func (e *ec2Seeder) seedSecurityGroups(ctx context.Context, ec2c *ec2.Client) (map[string]string, error) {
	existing, err := e.listExistingSecurityGroups(ctx, ec2c)
	if err != nil {
		return nil, fmt.Errorf("listing security groups: %w", err)
	}

	fmt.Printf("  EC2 security groups...")

	// Pass 1: Create all security groups (no rules yet).
	for _, sg := range e.config.SecurityGroups {
		id, err := e.ensureSecurityGroup(ctx, ec2c, sg.Name, sg.Description, existing)
		if err != nil {
			return nil, err
		}
		existing[sg.Name] = id
	}

	for i := range e.config.ExtraSGs {
		name := fmt.Sprintf("gen-sg-%03d", i+1)
		id, err := e.ensureSecurityGroup(ctx, ec2c, name, fmt.Sprintf("Generated security group %d", i+1), existing)
		if err != nil {
			return nil, err
		}
		existing[name] = id
	}

	// Pass 2: Add rules (now all SG IDs are known for cross-references).
	for _, sg := range e.config.SecurityGroups {
		sgID := existing[sg.Name]
		if err := e.addIngressRules(ctx, ec2c, sgID, sg.Inbound, existing); err != nil {
			return nil, fmt.Errorf("adding inbound rules to %q: %w", sg.Name, err)
		}
		if err := e.addEgressRules(ctx, ec2c, sgID, sg.Outbound, existing); err != nil {
			return nil, fmt.Errorf("adding outbound rules to %q: %w", sg.Name, err)
		}
	}

	// Extra SGs get a simple TCP 443 inbound rule.
	for i := range e.config.ExtraSGs {
		name := fmt.Sprintf("gen-sg-%03d", i+1)
		sgID := existing[name]
		if err := e.addIngressRules(ctx, ec2c, sgID, []sgRuleDef{
			{Protocol: "tcp", FromPort: 443, ToPort: 443, CIDR: "0.0.0.0/0", Description: "HTTPS"},
		}, existing); err != nil {
			return nil, fmt.Errorf("adding rules to %q: %w", name, err)
		}
	}

	fmt.Println(" done")
	return existing, nil
}

func (e *ec2Seeder) listExistingSecurityGroups(ctx context.Context, ec2c *ec2.Client) (map[string]string, error) {
	out, err := ec2c.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{})
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(out.SecurityGroups))
	for _, sg := range out.SecurityGroups {
		m[aws.ToString(sg.GroupName)] = aws.ToString(sg.GroupId)
	}
	return m, nil
}

func (e *ec2Seeder) ensureSecurityGroup(ctx context.Context, ec2c *ec2.Client, name, description string, existing map[string]string) (string, error) {
	if id, ok := existing[name]; ok {
		return id, nil
	}
	out, err := ec2c.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String(name),
		Description: aws.String(description),
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeSecurityGroup,
				Tags: []ec2types.Tag{
					{Key: aws.String("Name"), Value: aws.String(name)},
				},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("creating security group %q: %w", name, err)
	}
	return aws.ToString(out.GroupId), nil
}

func (e *ec2Seeder) buildIPPermissions(rules []sgRuleDef, sgMap map[string]string) []ec2types.IpPermission {
	perms := make([]ec2types.IpPermission, 0, len(rules))
	for _, r := range rules {
		perm := ec2types.IpPermission{
			IpProtocol: aws.String(r.Protocol),
			FromPort:   aws.Int32(r.FromPort),
			ToPort:     aws.Int32(r.ToPort),
		}
		if r.CIDR != "" {
			perm.IpRanges = []ec2types.IpRange{
				{CidrIp: aws.String(r.CIDR), Description: aws.String(r.Description)},
			}
		}
		if r.SGRef != "" {
			if sgID, ok := sgMap[r.SGRef]; ok {
				perm.UserIdGroupPairs = []ec2types.UserIdGroupPair{
					{GroupId: aws.String(sgID), Description: aws.String(r.Description)},
				}
			}
		}
		perms = append(perms, perm)
	}
	return perms
}

func (e *ec2Seeder) addIngressRules(ctx context.Context, ec2c *ec2.Client, sgID string, rules []sgRuleDef, sgMap map[string]string) error {
	if len(rules) == 0 {
		return nil
	}
	_, err := ec2c.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId:       aws.String(sgID),
		IpPermissions: e.buildIPPermissions(rules, sgMap),
	})
	return err
}

func (e *ec2Seeder) addEgressRules(ctx context.Context, ec2c *ec2.Client, sgID string, rules []sgRuleDef, sgMap map[string]string) error {
	// Filter out rules matching the default egress (all traffic to 0.0.0.0/0)
	// that AWS creates automatically with every security group.
	var filtered []sgRuleDef
	for _, r := range rules {
		if r.Protocol == "-1" && r.CIDR == "0.0.0.0/0" {
			continue
		}
		filtered = append(filtered, r)
	}
	if len(filtered) == 0 {
		return nil
	}
	_, err := ec2c.AuthorizeSecurityGroupEgress(ctx, &ec2.AuthorizeSecurityGroupEgressInput{
		GroupId:       aws.String(sgID),
		IpPermissions: e.buildIPPermissions(filtered, sgMap),
	})
	return err
}

func (e *ec2Seeder) listExistingAMIs(ctx context.Context, ec2c *ec2.Client) (map[string]string, error) {
	out, err := ec2c.DescribeImages(ctx, &ec2.DescribeImagesInput{
		Owners: []string{"self"},
	})
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(out.Images))
	for _, img := range out.Images {
		m[aws.ToString(img.Name)] = aws.ToString(img.ImageId)
	}
	return m, nil
}

func (e *ec2Seeder) listExistingInstances(ctx context.Context, ec2c *ec2.Client) (map[string]string, error) {
	out, err := ec2c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("instance-state-name"),
				Values: []string{"pending", "running", "stopped", "stopping"},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	m := make(map[string]string)
	for _, r := range out.Reservations {
		for _, inst := range r.Instances {
			for _, tag := range inst.Tags {
				if aws.ToString(tag.Key) == "Name" {
					m[aws.ToString(tag.Value)] = aws.ToString(inst.InstanceId)
				}
			}
		}
	}
	return m, nil
}

func (e *ec2Seeder) ensureAMI(ctx context.Context, ec2c *ec2.Client, name, arch string, existing map[string]string) (string, error) {
	if id, ok := existing[name]; ok {
		return id, nil
	}
	out, err := ec2c.RegisterImage(ctx, &ec2.RegisterImageInput{
		Name:               aws.String(name),
		Description:        aws.String(name), // required by LocalStack 2026.03.0
		Architecture:       ec2types.ArchitectureValues(arch),
		RootDeviceName:     aws.String("/dev/xvda"),
		VirtualizationType: aws.String("hvm"),
		BlockDeviceMappings: []ec2types.BlockDeviceMapping{
			{
				DeviceName: aws.String("/dev/xvda"),
				Ebs: &ec2types.EbsBlockDevice{
					VolumeSize: aws.Int32(8),
					VolumeType: ec2types.VolumeTypeGp2,
				},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("registering AMI %q: %w", name, err)
	}
	return aws.ToString(out.ImageId), nil
}

func (e *ec2Seeder) ensureInstance(ctx context.Context, ec2c *ec2.Client, name, amiID, instanceType string, sgIDs []string, existing map[string]string) (string, error) {
	if id, ok := existing[name]; ok {
		return id, nil
	}
	input := &ec2.RunInstancesInput{
		ImageId:      aws.String(amiID),
		InstanceType: ec2types.InstanceType(instanceType),
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeInstance,
				Tags: []ec2types.Tag{
					{Key: aws.String("Name"), Value: aws.String(name)},
				},
			},
		},
	}
	if len(sgIDs) > 0 {
		input.SecurityGroupIds = sgIDs
	}
	out, err := ec2c.RunInstances(ctx, input)
	if err != nil {
		return "", fmt.Errorf("launching instance %q: %w", name, err)
	}
	return aws.ToString(out.Instances[0].InstanceId), nil
}

func (e *ec2Seeder) stopInstances(ctx context.Context, ec2c *ec2.Client, ids []string) error {
	_, err := ec2c.StopInstances(ctx, &ec2.StopInstancesInput{
		InstanceIds: ids,
	})
	return err
}
