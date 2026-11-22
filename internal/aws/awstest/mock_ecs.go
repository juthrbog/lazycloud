package awstest

import (
	"context"

	"github.com/stretchr/testify/mock"

	"github.com/juthrbog/lazycloud/internal/aws"
)

// MockECSService is a testify mock implementing aws.ECSService.
type MockECSService struct {
	mock.Mock
}

var _ aws.ECSService = (*MockECSService)(nil)

func (m *MockECSService) ListClustersPage(ctx context.Context, token *string) (*aws.ClusterPage, error) {
	args := m.Called(ctx, token)
	val, _ := args.Get(0).(*aws.ClusterPage)
	return val, args.Error(1)
}

func (m *MockECSService) ListServicesPage(ctx context.Context, cluster string, token *string) (*aws.ServicePage, error) {
	args := m.Called(ctx, cluster, token)
	val, _ := args.Get(0).(*aws.ServicePage)
	return val, args.Error(1)
}

func (m *MockECSService) ListTasksPage(ctx context.Context, cluster, service string, stopped bool, token *string) (*aws.TaskPage, error) {
	args := m.Called(ctx, cluster, service, stopped, token)
	val, _ := args.Get(0).(*aws.TaskPage)
	return val, args.Error(1)
}

func (m *MockECSService) DescribeTask(ctx context.Context, cluster, taskARN string) (*aws.Task, error) {
	args := m.Called(ctx, cluster, taskARN)
	val, _ := args.Get(0).(*aws.Task)
	return val, args.Error(1)
}

func (m *MockECSService) ListTaskFamiliesPage(ctx context.Context, token *string) (*aws.FamilyPage, error) {
	args := m.Called(ctx, token)
	val, _ := args.Get(0).(*aws.FamilyPage)
	return val, args.Error(1)
}

func (m *MockECSService) ListTaskDefinitionsPage(ctx context.Context, family string, token *string) (*aws.TaskDefinitionPage, error) {
	args := m.Called(ctx, family, token)
	val, _ := args.Get(0).(*aws.TaskDefinitionPage)
	return val, args.Error(1)
}

func (m *MockECSService) DescribeTaskDefinition(ctx context.Context, name string) (*aws.TaskDefinition, error) {
	args := m.Called(ctx, name)
	val, _ := args.Get(0).(*aws.TaskDefinition)
	return val, args.Error(1)
}

func (m *MockECSService) DescribeServiceDeployment(ctx context.Context, arn string) (*aws.ServiceDeployment, error) {
	args := m.Called(ctx, arn)
	val, _ := args.Get(0).(*aws.ServiceDeployment)
	return val, args.Error(1)
}

func (m *MockECSService) ForceDeployment(ctx context.Context, cluster, service string) error {
	return m.Called(ctx, cluster, service).Error(0)
}

func (m *MockECSService) SetDesiredCount(ctx context.Context, cluster, service string, count int32) error {
	return m.Called(ctx, cluster, service, count).Error(0)
}

func (m *MockECSService) StopTask(ctx context.Context, cluster, taskARN, reason string) error {
	return m.Called(ctx, cluster, taskARN, reason).Error(0)
}
