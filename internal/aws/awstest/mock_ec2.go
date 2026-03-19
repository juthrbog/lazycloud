package awstest

import (
	"context"

	"github.com/stretchr/testify/mock"

	"github.com/juthrbog/lazycloud/internal/aws"
)

// MockEC2Service is a testify mock implementing aws.EC2Service.
type MockEC2Service struct {
	mock.Mock
}

var _ aws.EC2Service = (*MockEC2Service)(nil)

func (m *MockEC2Service) ListInstances(ctx context.Context) ([]aws.Instance, error) {
	args := m.Called(ctx)
	return args.Get(0).([]aws.Instance), args.Error(1)
}

func (m *MockEC2Service) GetInstanceDetail(ctx context.Context, instanceID string) (*aws.InstanceDetail, error) {
	args := m.Called(ctx, instanceID)
	val, _ := args.Get(0).(*aws.InstanceDetail)
	return val, args.Error(1)
}

func (m *MockEC2Service) StartInstance(ctx context.Context, instanceID string) error {
	args := m.Called(ctx, instanceID)
	return args.Error(0)
}

func (m *MockEC2Service) StopInstance(ctx context.Context, instanceID string) error {
	args := m.Called(ctx, instanceID)
	return args.Error(0)
}

func (m *MockEC2Service) RebootInstance(ctx context.Context, instanceID string) error {
	args := m.Called(ctx, instanceID)
	return args.Error(0)
}

func (m *MockEC2Service) TerminateInstance(ctx context.Context, instanceID string) error {
	args := m.Called(ctx, instanceID)
	return args.Error(0)
}
