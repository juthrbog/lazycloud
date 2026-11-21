package main

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	awslib "github.com/juthrbog/lazycloud/internal/aws"
)

// fakeECSAPI keeps clusters, revisions, services and tasks in memory.
type fakeECSAPI struct {
	clusters  map[string]*ecs.CreateClusterInput
	revisions map[string][]*ecs.RegisterTaskDefinitionInput // family -> revisions, oldest first
	services  map[string]*ecs.CreateServiceInput            // "cluster/name"
	inactive  map[string]bool                               // "cluster/name"
	stopped   map[string][]string                           // "cluster/family" -> task ARNs
	runs      []*ecs.RunTaskInput
	stops     []*ecs.StopTaskInput
	describes [][]string        // the services of each DescribeServices call
	taskDefs  map[string]string // task ARN -> task definition ARN
	listDefs  int               // ListTaskDefinitions calls
}

func newFakeECSAPI() *fakeECSAPI {
	return &fakeECSAPI{
		clusters:  map[string]*ecs.CreateClusterInput{},
		revisions: map[string][]*ecs.RegisterTaskDefinitionInput{},
		services:  map[string]*ecs.CreateServiceInput{},
		inactive:  map[string]bool{},
		stopped:   map[string][]string{},
		taskDefs:  map[string]string{},
	}
}

func fakeClusterARN(name string) string { return "arn:aws:ecs:us-east-1:000000000000:cluster/" + name }

func fakeRevisionARN(family string, n int) string {
	return fmt.Sprintf("arn:aws:ecs:us-east-1:000000000000:task-definition/%s:%d", family, n)
}

func (f *fakeECSAPI) CreateCluster(_ context.Context, in *ecs.CreateClusterInput, _ ...func(*ecs.Options)) (*ecs.CreateClusterOutput, error) {
	name := aws.ToString(in.ClusterName)
	if _, ok := f.clusters[name]; !ok {
		f.clusters[name] = in
	}
	return &ecs.CreateClusterOutput{Cluster: &ecstypes.Cluster{ClusterArn: aws.String(fakeClusterARN(name)), ClusterName: in.ClusterName}}, nil
}

func (f *fakeECSAPI) ListTaskDefinitions(_ context.Context, in *ecs.ListTaskDefinitionsInput, _ ...func(*ecs.Options)) (*ecs.ListTaskDefinitionsOutput, error) {
	f.listDefs++
	family := aws.ToString(in.FamilyPrefix)
	var arns []string
	for n := 1; n <= len(f.revisions[family]); n++ { // oldest first, as AWS sorts by default
		arns = append(arns, fakeRevisionARN(family, n))
	}
	if in.Sort == ecstypes.SortOrderDesc {
		slices.Reverse(arns)
	}
	return &ecs.ListTaskDefinitionsOutput{TaskDefinitionArns: arns}, nil
}

func (f *fakeECSAPI) RegisterTaskDefinition(_ context.Context, in *ecs.RegisterTaskDefinitionInput, _ ...func(*ecs.Options)) (*ecs.RegisterTaskDefinitionOutput, error) {
	family := aws.ToString(in.Family)
	f.revisions[family] = append(f.revisions[family], in)
	arn := fakeRevisionARN(family, len(f.revisions[family]))
	return &ecs.RegisterTaskDefinitionOutput{TaskDefinition: &ecstypes.TaskDefinition{TaskDefinitionArn: aws.String(arn)}}, nil
}

func (f *fakeECSAPI) DescribeServices(_ context.Context, in *ecs.DescribeServicesInput, _ ...func(*ecs.Options)) (*ecs.DescribeServicesOutput, error) {
	if len(in.Services) > 10 {
		return nil, fmt.Errorf("InvalidParameterException: more than 10 services")
	}
	f.describes = append(f.describes, in.Services)
	cluster := aws.ToString(in.Cluster)
	out := &ecs.DescribeServicesOutput{}
	for _, name := range in.Services {
		key := cluster + "/" + name
		if _, ok := f.services[key]; !ok {
			out.Failures = append(out.Failures, ecstypes.Failure{Arn: aws.String(name), Reason: aws.String("MISSING")})
			continue
		}
		status := "ACTIVE"
		if f.inactive[key] {
			status = "INACTIVE"
		}
		out.Services = append(out.Services, ecstypes.Service{ServiceName: aws.String(name), Status: aws.String(status)})
	}
	return out, nil
}

func (f *fakeECSAPI) CreateService(_ context.Context, in *ecs.CreateServiceInput, _ ...func(*ecs.Options)) (*ecs.CreateServiceOutput, error) {
	key := aws.ToString(in.Cluster) + "/" + aws.ToString(in.ServiceName)
	f.services[key] = in
	delete(f.inactive, key)
	return &ecs.CreateServiceOutput{}, nil
}

func (f *fakeECSAPI) ListTasks(_ context.Context, in *ecs.ListTasksInput, _ ...func(*ecs.Options)) (*ecs.ListTasksOutput, error) {
	if in.DesiredStatus != ecstypes.DesiredStatusStopped {
		return &ecs.ListTasksOutput{}, nil
	}
	return &ecs.ListTasksOutput{TaskArns: f.stopped[aws.ToString(in.Cluster)+"/"+aws.ToString(in.Family)]}, nil
}

func (f *fakeECSAPI) RunTask(_ context.Context, in *ecs.RunTaskInput, _ ...func(*ecs.Options)) (*ecs.RunTaskOutput, error) {
	f.runs = append(f.runs, in)
	arn := fmt.Sprintf("arn:aws:ecs:us-east-1:000000000000:task/x/t%d", len(f.runs))
	f.taskDefs[arn] = aws.ToString(in.TaskDefinition)
	return &ecs.RunTaskOutput{Tasks: []ecstypes.Task{{TaskArn: aws.String(arn), TaskDefinitionArn: in.TaskDefinition}}}, nil
}

func (f *fakeECSAPI) StopTask(_ context.Context, in *ecs.StopTaskInput, _ ...func(*ecs.Options)) (*ecs.StopTaskOutput, error) {
	f.stops = append(f.stops, in)
	task := aws.ToString(in.Task)
	family, _ := awslib.ParseTaskDefinition(f.taskDefs[task])
	key := aws.ToString(in.Cluster) + "/" + family
	f.stopped[key] = append(f.stopped[key], task)
	return &ecs.StopTaskOutput{}, nil
}

type fakeSubnets struct{ ids []string }

func (f fakeSubnets) DescribeSubnets(_ context.Context, in *ec2.DescribeSubnetsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	out := &ec2.DescribeSubnetsOutput{}
	if len(in.Filters) != 1 || aws.ToString(in.Filters[0].Name) != "default-for-az" {
		return out, nil
	}
	for _, id := range f.ids {
		out.Subnets = append(out.Subnets, ec2types.Subnet{SubnetId: aws.String(id)})
	}
	return out, nil
}

func smallECS(t *testing.T) ECSSeedConfig {
	t.Helper()
	cfg, err := loadConfig("small")
	require.NoError(t, err)
	return cfg.ECS
}

func TestSeedECSMakesTheClusterRevisionsAndServices(t *testing.T) {
	api := newFakeECSAPI()
	subnets := fakeSubnets{ids: []string{"subnet-b", "subnet-a"}}
	require.NoError(t, seedECS(context.Background(), api, subnets, smallECS(t), "us-east-1"))

	require.Contains(t, api.clusters, "prod-cluster")
	c := api.clusters["prod-cluster"]
	assert.Equal(t, []string{"FARGATE", "FARGATE_SPOT"}, c.CapacityProviders)
	assert.Equal(t, "team", aws.ToString(c.Tags[0].Key))

	assert.Len(t, api.revisions["web"], 1)
	assert.Len(t, api.revisions["worker"], 2)
	assert.Len(t, api.revisions["scheduler"], 2)
	web := api.revisions["web"][0]
	assert.Equal(t, ecstypes.NetworkModeAwsvpc, web.NetworkMode)
	assert.Equal(t, ecstypes.CPUArchitectureX8664, web.RuntimePlatform.CpuArchitecture, "no arm64 task on an x86_64 host")
	app := web.ContainerDefinitions[0]
	assert.Equal(t, "nginx:alpine", aws.ToString(app.Image))
	assert.Equal(t, ecstypes.LogDriverAwslogs, app.LogConfiguration.LogDriver)
	assert.Equal(t, map[string]string{"awslogs-group": "/ecs/web-app", "awslogs-region": "us-east-1",
		"awslogs-stream-prefix": "ecs", "awslogs-create-group": "true"}, app.LogConfiguration.Options,
		"the container makes its own group, because the seeders run in parallel")
	env := map[string]string{}
	for _, kv := range app.Environment {
		env[aws.ToString(kv.Name)] = aws.ToString(kv.Value)
	}
	assert.Equal(t, "not-a-real-secret", env["DB_PASSWORD"])
	assert.Equal(t, "1", env["SEED_REVISION"])
	assert.Nil(t, api.revisions["scheduler"][0].ContainerDefinitions[0].LogConfiguration, "no log group, no log configuration")
	assert.NotEqual(t, api.revisions["worker"][0].ContainerDefinitions[0].Environment,
		api.revisions["worker"][1].ContainerDefinitions[0].Environment, "the revisions differ")

	cluster := fakeClusterARN("prod-cluster")
	require.Len(t, api.services, 3)
	ws := api.services[cluster+"/web"]
	require.NotNil(t, ws)
	assert.Equal(t, int32(1), aws.ToInt32(ws.DesiredCount))
	assert.True(t, ws.EnableExecuteCommand)
	assert.Equal(t, fakeRevisionARN("web", 1), aws.ToString(ws.TaskDefinition))
	assert.Equal(t, []string{"subnet-a"}, ws.NetworkConfiguration.AwsvpcConfiguration.Subnets, "the first default subnet")
	assert.True(t, ws.DeploymentConfiguration.DeploymentCircuitBreaker.Enable)
	wk := api.services[cluster+"/worker"]
	assert.Equal(t, int32(0), aws.ToInt32(wk.DesiredCount), "no container for worker")
	assert.Equal(t, fakeRevisionARN("worker", 2), aws.ToString(wk.TaskDefinition), "the newest revision")

	require.Len(t, api.runs, 1, "one task of web runs and stops")
	assert.Equal(t, "lazycloud-seed", aws.ToString(api.runs[0].StartedBy))
	require.Len(t, api.stops, 1)
	assert.Equal(t, ecsStopReason, aws.ToString(api.stops[0].Reason))
}

func TestSeedECSTwiceAddsNothing(t *testing.T) {
	api := newFakeECSAPI()
	subnets := fakeSubnets{ids: []string{"subnet-a"}}
	cfg := smallECS(t)
	require.NoError(t, seedECS(context.Background(), api, subnets, cfg, "us-east-1"))
	revs := len(api.revisions["worker"])
	require.NoError(t, seedECS(context.Background(), api, subnets, cfg, "us-east-1"))
	assert.Len(t, api.revisions["worker"], revs, "no new revision")
	assert.Len(t, api.services, 3)
	assert.Len(t, api.runs, 1, "the stopped task is still listed")

	// An INACTIVE service (deleted) is made again.
	key := fakeClusterARN("prod-cluster") + "/scheduler"
	api.inactive[key] = true
	api.services[key].DesiredCount = aws.Int32(7)
	require.NoError(t, seedECS(context.Background(), api, subnets, cfg, "us-east-1"))
	assert.False(t, api.inactive[key])
	assert.Equal(t, int32(0), aws.ToInt32(api.services[key].DesiredCount))
	assert.Equal(t, fakeRevisionARN("scheduler", 2), aws.ToString(api.services[key].TaskDefinition), "the newest revision")
}

func TestSeedECSExtraServicesShareOneFamily(t *testing.T) {
	api := newFakeECSAPI()
	cfg := ECSSeedConfig{ExtraServices: 25}
	require.NoError(t, seedECS(context.Background(), api, fakeSubnets{ids: []string{"subnet-a"}}, cfg, "us-east-1"))
	assert.Contains(t, api.clusters, "bulk-cluster")
	assert.Len(t, api.services, 25)
	assert.Len(t, api.revisions["generated"], 1, "one family for all")
	assert.Equal(t, 1, api.listDefs, "the family is read once")
	require.Len(t, api.describes, 3, "DescribeServices takes at most 10 services")
	assert.Len(t, api.describes[2], 5)
	for _, s := range api.services {
		assert.Equal(t, int32(0), aws.ToInt32(s.DesiredCount))
	}
	assert.Empty(t, api.runs)
}

func TestSeedECSNeedsADefaultSubnet(t *testing.T) {
	err := seedECS(context.Background(), newFakeECSAPI(), fakeSubnets{}, smallECS(t), "us-east-1")
	require.ErrorContains(t, err, "default subnet")

	// Nothing to seed: no subnet lookup.
	require.NoError(t, seedECS(context.Background(), newFakeECSAPI(), fakeSubnets{}, ECSSeedConfig{}, "us-east-1"))
}

func TestECSTiers(t *testing.T) {
	small := smallECS(t)
	require.Len(t, small.Clusters, 1)
	assert.Equal(t, "prod-cluster", small.Clusters[0].Name)
	assert.Zero(t, small.ExtraServices)
	running := 0
	for _, s := range small.Clusters[0].Services {
		running += int(s.Desired)
	}
	assert.Equal(t, 1, running, "one running container in the small tier")
	for tier, want := range map[string]int{"medium": 30, "large": 120, "enterprise": 1100} {
		cfg, err := loadConfig(tier)
		require.NoError(t, err)
		assert.Equal(t, want, cfg.ECS.ExtraServices, tier)
	}
}
