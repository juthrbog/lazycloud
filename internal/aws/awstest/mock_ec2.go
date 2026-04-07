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

func (m *MockEC2Service) ListInstancesPage(ctx context.Context, token *string) (*aws.InstancePage, error) {
	args := m.Called(ctx, token)
	val, _ := args.Get(0).(*aws.InstancePage)
	return val, args.Error(1)
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

func (m *MockEC2Service) StartInstances(ctx context.Context, ids []string) error {
	args := m.Called(ctx, ids)
	return args.Error(0)
}

func (m *MockEC2Service) StopInstances(ctx context.Context, ids []string) error {
	args := m.Called(ctx, ids)
	return args.Error(0)
}

func (m *MockEC2Service) RebootInstances(ctx context.Context, ids []string) error {
	args := m.Called(ctx, ids)
	return args.Error(0)
}

func (m *MockEC2Service) TerminateInstances(ctx context.Context, ids []string) error {
	args := m.Called(ctx, ids)
	return args.Error(0)
}

func (m *MockEC2Service) ListOwnedAMIs(ctx context.Context) ([]aws.AMI, error) {
	args := m.Called(ctx)
	return args.Get(0).([]aws.AMI), args.Error(1)
}

func (m *MockEC2Service) SearchAMIs(ctx context.Context, query string) ([]aws.AMI, error) {
	args := m.Called(ctx, query)
	return args.Get(0).([]aws.AMI), args.Error(1)
}

func (m *MockEC2Service) ListSecurityGroups(ctx context.Context) ([]aws.SecurityGroup, error) {
	args := m.Called(ctx)
	return args.Get(0).([]aws.SecurityGroup), args.Error(1)
}

func (m *MockEC2Service) GetSecurityGroup(ctx context.Context, groupID string) (*aws.SecurityGroup, error) {
	args := m.Called(ctx, groupID)
	val, _ := args.Get(0).(*aws.SecurityGroup)
	return val, args.Error(1)
}

func (m *MockEC2Service) ListVPCsPage(ctx context.Context, token *string) (*aws.VPCPage, error) {
	args := m.Called(ctx, token)
	val, _ := args.Get(0).(*aws.VPCPage)
	return val, args.Error(1)
}

func (m *MockEC2Service) GetVPC(ctx context.Context, vpcID string) (*aws.VPC, error) {
	args := m.Called(ctx, vpcID)
	val, _ := args.Get(0).(*aws.VPC)
	return val, args.Error(1)
}

func (m *MockEC2Service) ListSubnetsPage(ctx context.Context, token *string, vpcID string) (*aws.SubnetPage, error) {
	args := m.Called(ctx, token, vpcID)
	val, _ := args.Get(0).(*aws.SubnetPage)
	return val, args.Error(1)
}

func (m *MockEC2Service) GetSubnet(ctx context.Context, subnetID string) (*aws.Subnet, error) {
	args := m.Called(ctx, subnetID)
	val, _ := args.Get(0).(*aws.Subnet)
	return val, args.Error(1)
}
