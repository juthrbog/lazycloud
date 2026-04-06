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

	// Phase 2: Instances (depends on AMI IDs).
	return e.seedInstances(ctx, ec2c, amiMap)
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
func (e *ec2Seeder) seedInstances(ctx context.Context, ec2c *ec2.Client, amiMap map[string]string) error {
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
		id, err := e.ensureInstance(ctx, ec2c, inst.Name, amiID, inst.InstanceType, existing)
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

		id, err := e.ensureInstance(ctx, ec2c, name, amiID, iType, existing)
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

func (e *ec2Seeder) ensureInstance(ctx context.Context, ec2c *ec2.Client, name, amiID, instanceType string, existing map[string]string) (string, error) {
	if id, ok := existing[name]; ok {
		return id, nil
	}
	out, err := ec2c.RunInstances(ctx, &ec2.RunInstancesInput{
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
	})
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
