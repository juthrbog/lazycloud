package main

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	awslib "github.com/juthrbog/lazycloud/internal/aws"
)

// The ECS seeder makes clusters, task definitions and services. On
// LocalStack each running task is a real Docker container, so most
// services have a desired count of 0, and the seeder never makes a service
// whose task crashes (docs/design/ecs.md). Each container writes to its own
// log group with awslogs-create-group, because the seeders run in parallel.

// ecsAPI is the part of the ECS client that the seeder uses, so a test can
// give a fake.
type ecsAPI interface {
	CreateCluster(ctx context.Context, in *ecs.CreateClusterInput, opts ...func(*ecs.Options)) (*ecs.CreateClusterOutput, error)
	ListTaskDefinitions(ctx context.Context, in *ecs.ListTaskDefinitionsInput, opts ...func(*ecs.Options)) (*ecs.ListTaskDefinitionsOutput, error)
	RegisterTaskDefinition(ctx context.Context, in *ecs.RegisterTaskDefinitionInput, opts ...func(*ecs.Options)) (*ecs.RegisterTaskDefinitionOutput, error)
	DescribeServices(ctx context.Context, in *ecs.DescribeServicesInput, opts ...func(*ecs.Options)) (*ecs.DescribeServicesOutput, error)
	CreateService(ctx context.Context, in *ecs.CreateServiceInput, opts ...func(*ecs.Options)) (*ecs.CreateServiceOutput, error)
	ListTasks(ctx context.Context, in *ecs.ListTasksInput, opts ...func(*ecs.Options)) (*ecs.ListTasksOutput, error)
	RunTask(ctx context.Context, in *ecs.RunTaskInput, opts ...func(*ecs.Options)) (*ecs.RunTaskOutput, error)
	StopTask(ctx context.Context, in *ecs.StopTaskInput, opts ...func(*ecs.Options)) (*ecs.StopTaskOutput, error)
}

// subnetAPI finds the subnets that awsvpc tasks need.
type subnetAPI interface {
	DescribeSubnets(ctx context.Context, in *ec2.DescribeSubnetsInput, opts ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error)
}

// ecsStopReason is the reason of the seeded stopped task.
const ecsStopReason = "Stopped by the lazycloud seed"

type ecsSeeder struct {
	client *awslib.Client
	config ECSSeedConfig
	region string
}

func newECSSeeder(client *awslib.Client, cfg ECSSeedConfig, region string) *ecsSeeder {
	return &ecsSeeder{client: client, config: cfg, region: region}
}

func (s *ecsSeeder) Name() string { return "ecs" }

func (s *ecsSeeder) Seed(ctx context.Context) error {
	fmt.Printf("  ECS clusters...")
	if err := seedECS(ctx, s.client.ECSClient(), s.client.EC2Client(), s.config, s.region); err != nil {
		return err
	}
	fmt.Println(" done")
	return nil
}

func seedECS(ctx context.Context, api ecsAPI, subnets subnetAPI, cfg ECSSeedConfig, region string) error {
	if len(cfg.Clusters) == 0 && cfg.ExtraServices == 0 {
		return nil
	}
	subnet, err := defaultSubnet(ctx, subnets)
	if err != nil {
		return err
	}
	for _, c := range cfg.Clusters {
		if err := seedCluster(ctx, api, c, subnet, region); err != nil {
			return err
		}
	}
	if cfg.ExtraServices > 0 {
		bulk := ecsClusterDef{Name: "bulk-cluster"}
		for i := range cfg.ExtraServices {
			bulk.Services = append(bulk.Services, ecsServiceDef{
				Name: fmt.Sprintf("generated-svc-%04d", i+1), Family: "generated", Image: "nginx:alpine",
			})
		}
		if err := seedCluster(ctx, api, bulk, subnet, region); err != nil {
			return err
		}
	}
	return nil
}

// defaultSubnet returns a subnet of the default VPC. RunTask and
// CreateService with awsvpc need a subnet that exists.
func defaultSubnet(ctx context.Context, api subnetAPI) (string, error) {
	out, err := api.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []ec2types.Filter{{Name: aws.String("default-for-az"), Values: []string{"true"}}},
	})
	if err != nil {
		return "", fmt.Errorf("finding a default subnet: %w", err)
	}
	ids := make([]string, 0, len(out.Subnets))
	for _, s := range out.Subnets {
		ids = append(ids, aws.ToString(s.SubnetId))
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("finding a default subnet: the default VPC has no subnet")
	}
	slices.Sort(ids) // the same subnet on each run
	return ids[0], nil
}

// seedCluster makes the cluster, then the task definitions and the services
// in it. CreateCluster with the name of a cluster that exists returns it, so
// a second run adds nothing.
func seedCluster(ctx context.Context, api ecsAPI, c ecsClusterDef, subnet, region string) error {
	out, err := api.CreateCluster(ctx, &ecs.CreateClusterInput{
		ClusterName: aws.String(c.Name),
		Tags:        ecsSeedTags(c.Tags),
		Settings: []ecstypes.ClusterSetting{{
			Name: ecstypes.ClusterSettingNameContainerInsights, Value: aws.String("enabled"),
		}},
		CapacityProviders: []string{"FARGATE", "FARGATE_SPOT"},
	})
	if err != nil {
		return fmt.Errorf("creating cluster %s: %w", c.Name, err)
	}
	cluster := c.Name
	if out.Cluster != nil && out.Cluster.ClusterArn != nil {
		cluster = *out.Cluster.ClusterArn
	}
	existing, err := activeServices(ctx, api, cluster, c.Services)
	if err != nil {
		return err
	}
	families := map[string]string{} // family -> the ARN of its latest revision
	for _, s := range c.Services {
		family := s.familyName()
		if _, ok := families[family]; !ok {
			arn, err := ensureRevisions(ctx, api, s, region)
			if err != nil {
				return err
			}
			families[family] = arn
		}
		if !existing[s.Name] {
			if err := createService(ctx, api, cluster, s, families[family], subnet); err != nil {
				return err
			}
		}
		if s.StoppedTask {
			if err := ensureStoppedTask(ctx, api, cluster, families[family], subnet); err != nil {
				return err
			}
		}
	}
	return nil
}

// activeServices returns the names of the services that exist and are not
// INACTIVE. DescribeServices takes at most 10 services a call.
func activeServices(ctx context.Context, api ecsAPI, cluster string, defs []ecsServiceDef) (map[string]bool, error) {
	names := make([]string, 0, len(defs))
	for _, s := range defs {
		names = append(names, s.Name)
	}
	have := map[string]bool{}
	for batch := range slices.Chunk(names, 10) {
		out, err := api.DescribeServices(ctx, &ecs.DescribeServicesInput{Cluster: aws.String(cluster), Services: batch})
		if err != nil {
			return nil, fmt.Errorf("reading the services of %s: %w", cluster, err)
		}
		for _, s := range out.Services {
			if aws.ToString(s.Status) != "INACTIVE" {
				have[aws.ToString(s.ServiceName)] = true
			}
		}
	}
	return have, nil
}

// ensureRevisions registers revisions of the family of s until it has
// s.Revisions active ones, and returns the ARN of the newest. Each new
// revision has its number in the environment, so the revisions differ.
func ensureRevisions(ctx context.Context, api ecsAPI, s ecsServiceDef, region string) (string, error) {
	family := s.familyName()
	out, err := api.ListTaskDefinitions(ctx, &ecs.ListTaskDefinitionsInput{
		FamilyPrefix: aws.String(family), Status: ecstypes.TaskDefinitionStatusActive, Sort: ecstypes.SortOrderDesc,
	})
	if err != nil {
		return "", fmt.Errorf("listing the revisions of %s: %w", family, err)
	}
	have, latest := len(out.TaskDefinitionArns), ""
	if have > 0 {
		latest = out.TaskDefinitionArns[0]
	}
	want := max(s.Revisions, 1)
	for n := have + 1; n <= want; n++ {
		in := taskDefinitionInput(s, family, n, region)
		r, err := api.RegisterTaskDefinition(ctx, in)
		if err != nil {
			return "", fmt.Errorf("registering %s: %w", family, err)
		}
		latest = aws.ToString(r.TaskDefinition.TaskDefinitionArn)
	}
	return latest, nil
}

func taskDefinitionInput(s ecsServiceDef, family string, revision int, region string) *ecs.RegisterTaskDefinitionInput {
	env := maps.Clone(s.Env)
	if env == nil {
		env = map[string]string{}
	}
	env["SEED_REVISION"] = strconv.Itoa(revision)
	var kv []ecstypes.KeyValuePair
	for _, k := range slices.Sorted(maps.Keys(env)) {
		kv = append(kv, ecstypes.KeyValuePair{Name: aws.String(k), Value: aws.String(env[k])})
	}
	container := ecstypes.ContainerDefinition{
		Name:         aws.String("app"),
		Image:        aws.String(s.Image),
		Essential:    aws.Bool(true),
		Environment:  kv,
		PortMappings: []ecstypes.PortMapping{{ContainerPort: aws.Int32(80), Protocol: ecstypes.TransportProtocolTcp}},
	}
	if s.LogGroup != "" {
		container.LogConfiguration = &ecstypes.LogConfiguration{
			LogDriver: ecstypes.LogDriverAwslogs,
			Options: map[string]string{
				"awslogs-group":         s.LogGroup,
				"awslogs-region":        region,
				"awslogs-stream-prefix": "ecs",
				"awslogs-create-group":  "true",
			},
		}
	}
	return &ecs.RegisterTaskDefinitionInput{
		Family:                  aws.String(family),
		NetworkMode:             ecstypes.NetworkModeAwsvpc,
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
		Cpu:                     aws.String("256"),
		Memory:                  aws.String("512"),
		ExecutionRoleArn:        aws.String(seedRole),
		// nginx:alpine is multi-arch. An arm64 task does not run on an
		// x86_64 host.
		RuntimePlatform: &ecstypes.RuntimePlatform{
			CpuArchitecture: ecstypes.CPUArchitectureX8664, OperatingSystemFamily: ecstypes.OSFamilyLinux,
		},
		ContainerDefinitions: []ecstypes.ContainerDefinition{container},
	}
}

func createService(ctx context.Context, api ecsAPI, cluster string, s ecsServiceDef, taskDef, subnet string) error {
	if _, err := api.CreateService(ctx, &ecs.CreateServiceInput{
		Cluster:              aws.String(cluster),
		ServiceName:          aws.String(s.Name),
		TaskDefinition:       aws.String(taskDef),
		DesiredCount:         aws.Int32(s.Desired),
		LaunchType:           ecstypes.LaunchTypeFargate,
		EnableExecuteCommand: s.Exec,
		NetworkConfiguration: ecsNetwork(subnet),
		DeploymentConfiguration: &ecstypes.DeploymentConfiguration{
			DeploymentCircuitBreaker: &ecstypes.DeploymentCircuitBreaker{Enable: true, Rollback: true},
		},
		Tags: ecsSeedTags(s.Tags),
	}); err != nil {
		return fmt.Errorf("creating service %s: %w", s.Name, err)
	}
	return nil
}

// ensureStoppedTask runs one task and stops it, so the stopped task list has
// a row with a stop code and a reason. It does nothing while a stopped task
// of that task definition is still listed: AWS keeps one for about an hour.
func ensureStoppedTask(ctx context.Context, api ecsAPI, cluster, taskDef, subnet string) error {
	family, _ := awslib.ParseTaskDefinition(taskDef)
	out, err := api.ListTasks(ctx, &ecs.ListTasksInput{
		Cluster: aws.String(cluster), Family: aws.String(family), DesiredStatus: ecstypes.DesiredStatusStopped,
	})
	if err != nil {
		return fmt.Errorf("listing the stopped tasks of %s: %w", family, err)
	}
	if len(out.TaskArns) > 0 {
		return nil
	}
	run, err := api.RunTask(ctx, &ecs.RunTaskInput{
		Cluster:              aws.String(cluster),
		TaskDefinition:       aws.String(taskDef),
		LaunchType:           ecstypes.LaunchTypeFargate,
		NetworkConfiguration: ecsNetwork(subnet),
		StartedBy:            aws.String("lazycloud-seed"),
	})
	if err != nil {
		return fmt.Errorf("running a task of %s: %w", family, err)
	}
	if len(run.Tasks) == 0 {
		return fmt.Errorf("running a task of %s: no task started", family)
	}
	if _, err := api.StopTask(ctx, &ecs.StopTaskInput{
		Cluster: aws.String(cluster), Task: run.Tasks[0].TaskArn, Reason: aws.String(ecsStopReason),
	}); err != nil {
		return fmt.Errorf("stopping the task of %s: %w", family, err)
	}
	return nil
}

func ecsNetwork(subnet string) *ecstypes.NetworkConfiguration {
	return &ecstypes.NetworkConfiguration{AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{
		Subnets: []string{subnet}, AssignPublicIp: ecstypes.AssignPublicIpDisabled,
	}}
}

func ecsSeedTags(tags map[string]string) []ecstypes.Tag {
	var out []ecstypes.Tag
	for _, k := range slices.Sorted(maps.Keys(tags)) {
		out = append(out, ecstypes.Tag{Key: aws.String(k), Value: aws.String(tags[k])})
	}
	return out
}
