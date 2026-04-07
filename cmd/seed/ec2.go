package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"

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

	// Phase 1: VPCs and subnets (must complete before SGs and instances).
	vpcMap, subnetMap, err := e.seedVPCsAndSubnets(ctx, ec2c)
	if err != nil {
		return err
	}

	// Phase 2: AMIs (must complete before instances).
	amiMap, err := e.seedAMIs(ctx, ec2c)
	if err != nil {
		return err
	}

	// Phase 3: Security groups (must complete before instances).
	sgMap, err := e.seedSecurityGroups(ctx, ec2c, vpcMap)
	if err != nil {
		return err
	}

	// Phase 4: Instances (depends on AMI, SG, and subnet IDs).
	return e.seedInstances(ctx, ec2c, amiMap, sgMap, subnetMap)
}

// seedVPCsAndSubnets creates VPCs and subnets, returning name→ID maps for both.
func (e *ec2Seeder) seedVPCsAndSubnets(ctx context.Context, ec2c *ec2.Client) (map[string]string, map[string]string, error) {
	existingVPCs, err := e.listExistingVPCs(ctx, ec2c)
	if err != nil {
		return nil, nil, fmt.Errorf("listing VPCs: %w", err)
	}
	existingSubnets, err := e.listExistingSubnets(ctx, ec2c)
	if err != nil {
		return nil, nil, fmt.Errorf("listing subnets: %w", err)
	}

	fmt.Printf("  EC2 VPCs...")

	// Create base VPCs.
	for _, v := range e.config.VPCs {
		id, err := e.ensureVPC(ctx, ec2c, v.Name, v.CIDR, v.Tenancy, existingVPCs)
		if err != nil {
			return nil, nil, err
		}
		existingVPCs[v.Name] = id
	}

	// Create extra generated VPCs, each with 2 subnets.
	for i := range e.config.ExtraVPCs {
		name := fmt.Sprintf("gen-vpc-%03d", i+1)
		cidr := fmt.Sprintf("10.%d.0.0/16", 100+i)
		id, err := e.ensureVPC(ctx, ec2c, name, cidr, "default", existingVPCs)
		if err != nil {
			return nil, nil, err
		}
		existingVPCs[name] = id

		// Create 2 subnets per extra VPC.
		for j, suffix := range []string{"a", "b"} {
			subnetName := fmt.Sprintf("%s-subnet-%s", name, suffix)
			subnetCIDR := fmt.Sprintf("10.%d.%d.0/24", 100+i, j+1)
			subnetID, err := e.ensureSubnet(ctx, ec2c, subnetName, id, subnetCIDR, "us-east-1"+suffix, j == 0, existingSubnets)
			if err != nil {
				return nil, nil, err
			}
			existingSubnets[subnetName] = subnetID
		}
	}

	fmt.Println(" done")

	fmt.Printf("  EC2 subnets...")

	// Create base subnets.
	for _, s := range e.config.Subnets {
		vpcID, ok := existingVPCs[s.VPCRef]
		if !ok {
			return nil, nil, fmt.Errorf("subnet %q references unknown VPC %q", s.Name, s.VPCRef)
		}
		id, err := e.ensureSubnet(ctx, ec2c, s.Name, vpcID, s.CIDR, s.AZ, s.Public, existingSubnets)
		if err != nil {
			return nil, nil, err
		}
		existingSubnets[s.Name] = id
	}

	fmt.Println(" done")

	return existingVPCs, existingSubnets, nil
}

func (e *ec2Seeder) listExistingVPCs(ctx context.Context, ec2c *ec2.Client) (map[string]string, error) {
	out, err := ec2c.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{})
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(out.Vpcs))
	for _, v := range out.Vpcs {
		for _, tag := range v.Tags {
			if aws.ToString(tag.Key) == "Name" {
				m[aws.ToString(tag.Value)] = aws.ToString(v.VpcId)
			}
		}
	}
	return m, nil
}

func (e *ec2Seeder) listExistingSubnets(ctx context.Context, ec2c *ec2.Client) (map[string]string, error) {
	out, err := ec2c.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{})
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(out.Subnets))
	for _, s := range out.Subnets {
		for _, tag := range s.Tags {
			if aws.ToString(tag.Key) == "Name" {
				m[aws.ToString(tag.Value)] = aws.ToString(s.SubnetId)
			}
		}
	}
	return m, nil
}

func (e *ec2Seeder) ensureVPC(ctx context.Context, ec2c *ec2.Client, name, cidr, tenancy string, existing map[string]string) (string, error) {
	if id, ok := existing[name]; ok {
		return id, nil
	}
	t := ec2types.TenancyDefault
	if tenancy == "dedicated" {
		t = ec2types.TenancyDedicated
	}
	out, err := ec2c.CreateVpc(ctx, &ec2.CreateVpcInput{
		CidrBlock:       aws.String(cidr),
		InstanceTenancy: t,
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeVpc,
				Tags: []ec2types.Tag{
					{Key: aws.String("Name"), Value: aws.String(name)},
				},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("creating VPC %q: %w", name, err)
	}
	return aws.ToString(out.Vpc.VpcId), nil
}

func (e *ec2Seeder) ensureSubnet(ctx context.Context, ec2c *ec2.Client, name, vpcID, cidr, az string, public bool, existing map[string]string) (string, error) {
	if id, ok := existing[name]; ok {
		return id, nil
	}
	out, err := ec2c.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId:            aws.String(vpcID),
		CidrBlock:        aws.String(cidr),
		AvailabilityZone: aws.String(az),
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeSubnet,
				Tags: []ec2types.Tag{
					{Key: aws.String("Name"), Value: aws.String(name)},
					{Key: aws.String("Network"), Value: aws.String(publicOrPrivate(public))},
				},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("creating subnet %q: %w", name, err)
	}
	subnetID := aws.ToString(out.Subnet.SubnetId)

	// Set MapPublicIpOnLaunch for public subnets.
	if public {
		_, err = ec2c.ModifySubnetAttribute(ctx, &ec2.ModifySubnetAttributeInput{
			SubnetId:            aws.String(subnetID),
			MapPublicIpOnLaunch: &ec2types.AttributeBooleanValue{Value: aws.Bool(true)},
		})
		if err != nil {
			return "", fmt.Errorf("setting public IP on subnet %q: %w", name, err)
		}
	}

	return subnetID, nil
}

func publicOrPrivate(public bool) string {
	if public {
		return "public"
	}
	return "private"
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
func (e *ec2Seeder) seedInstances(ctx context.Context, ec2c *ec2.Client, amiMap, sgMap, subnetMap map[string]string) error {
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
		subnetID := subnetMap[inst.SubnetRef] // empty string if no ref
		id, err := e.ensureInstance(ctx, ec2c, inst.Name, amiID, inst.InstanceType, sgIDs, subnetID, existing)
		if err != nil {
			return err
		}
		if inst.Stopped {
			toStop = append(toStop, id)
		}
	}

	// Launch extra generated instances.
	// Pick AMIs and subnets round-robin from the base lists.
	baseAMINames := make([]string, len(e.config.AMIs))
	for i, a := range e.config.AMIs {
		baseAMINames[i] = a.Name
	}
	baseSubnetNames := make([]string, 0, len(e.config.Subnets))
	for _, s := range e.config.Subnets {
		baseSubnetNames = append(baseSubnetNames, s.Name)
	}
	instanceTypes := []string{"t3.micro", "t3.small", "t3.medium", "t2.micro"}
	stopCount := int(e.config.StopFraction * float64(e.config.ExtraInstances))

	for i := range e.config.ExtraInstances {
		name := fmt.Sprintf("worker-%03d", i+1)
		amiName := baseAMINames[i%len(baseAMINames)]
		amiID := amiMap[amiName]
		iType := instanceTypes[i%len(instanceTypes)]
		var subnetID string
		if len(baseSubnetNames) > 0 {
			subnetID = subnetMap[baseSubnetNames[i%len(baseSubnetNames)]]
		}

		id, err := e.ensureInstance(ctx, ec2c, name, amiID, iType, nil, subnetID, existing)
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
func (e *ec2Seeder) seedSecurityGroups(ctx context.Context, ec2c *ec2.Client, vpcMap map[string]string) (map[string]string, error) {
	existing, err := e.listExistingSecurityGroups(ctx, ec2c)
	if err != nil {
		return nil, fmt.Errorf("listing security groups: %w", err)
	}

	fmt.Printf("  EC2 security groups...")

	// Pass 1: Create all security groups (no rules yet).
	for _, sg := range e.config.SecurityGroups {
		vpcID := vpcMap[sg.VPCRef] // empty string if no ref
		id, err := e.ensureSecurityGroup(ctx, ec2c, sg.Name, sg.Description, vpcID, existing)
		if err != nil {
			return nil, err
		}
		existing[sg.Name] = id
	}

	// Pick a VPC for extra SGs round-robin from base VPCs.
	baseVPCNames := make([]string, 0, len(e.config.VPCs))
	for _, v := range e.config.VPCs {
		baseVPCNames = append(baseVPCNames, v.Name)
	}
	for i := range e.config.ExtraSGs {
		name := fmt.Sprintf("gen-sg-%03d", i+1)
		var vpcID string
		if len(baseVPCNames) > 0 {
			vpcID = vpcMap[baseVPCNames[i%len(baseVPCNames)]]
		}
		id, err := e.ensureSecurityGroup(ctx, ec2c, name, fmt.Sprintf("Generated security group %d", i+1), vpcID, existing)
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

func (e *ec2Seeder) ensureSecurityGroup(ctx context.Context, ec2c *ec2.Client, name, description, vpcID string, existing map[string]string) (string, error) {
	if id, ok := existing[name]; ok {
		return id, nil
	}
	input := &ec2.CreateSecurityGroupInput{
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
	}
	if vpcID != "" {
		input.VpcId = aws.String(vpcID)
	}
	out, err := ec2c.CreateSecurityGroup(ctx, input)
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
	if isDuplicatePermission(err) {
		return nil
	}
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
	if isDuplicatePermission(err) {
		return nil
	}
	return err
}

// isDuplicatePermission returns true if the error is an InvalidPermission.Duplicate
// from the EC2 API, meaning the rule already exists on the security group.
func isDuplicatePermission(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "InvalidPermission.Duplicate"
	}
	// LocalStack may not always use structured errors
	return strings.Contains(err.Error(), "InvalidPermission.Duplicate")
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
	m := make(map[string]string)
	var nextToken *string

	for {
		out, err := ec2c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			NextToken: nextToken,
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
		for _, r := range out.Reservations {
			for _, inst := range r.Instances {
				for _, tag := range inst.Tags {
					if aws.ToString(tag.Key) == "Name" {
						m[aws.ToString(tag.Value)] = aws.ToString(inst.InstanceId)
					}
				}
			}
		}
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
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

func (e *ec2Seeder) ensureInstance(ctx context.Context, ec2c *ec2.Client, name, amiID, instanceType string, sgIDs []string, subnetID string, existing map[string]string) (string, error) {
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
	if subnetID != "" {
		input.SubnetId = aws.String(subnetID)
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
