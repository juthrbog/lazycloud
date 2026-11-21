package awstest

import (
	"context"

	"github.com/stretchr/testify/mock"

	"github.com/juthrbog/lazycloud/internal/aws"
)

// MockLambdaService is a testify mock implementing aws.LambdaService.
type MockLambdaService struct {
	mock.Mock
}

var _ aws.LambdaService = (*MockLambdaService)(nil)

func (m *MockLambdaService) ListFunctionsPage(ctx context.Context, token *string) (*aws.FunctionPage, error) {
	args := m.Called(ctx, token)
	val, _ := args.Get(0).(*aws.FunctionPage)
	return val, args.Error(1)
}

func (m *MockLambdaService) GetFunction(ctx context.Context, name, qualifier string) (*aws.FunctionDetail, error) {
	args := m.Called(ctx, name, qualifier)
	val, _ := args.Get(0).(*aws.FunctionDetail)
	return val, args.Error(1)
}

func (m *MockLambdaService) GetFunctionURL(ctx context.Context, name string) (*aws.FunctionURL, error) {
	args := m.Called(ctx, name)
	val, _ := args.Get(0).(*aws.FunctionURL)
	return val, args.Error(1)
}

func (m *MockLambdaService) ListAliasesPage(ctx context.Context, name string, token *string) (*aws.AliasPage, error) {
	args := m.Called(ctx, name, token)
	val, _ := args.Get(0).(*aws.AliasPage)
	return val, args.Error(1)
}

func (m *MockLambdaService) ListVersionsPage(ctx context.Context, name string, token *string) (*aws.FunctionPage, error) {
	args := m.Called(ctx, name, token)
	val, _ := args.Get(0).(*aws.FunctionPage)
	return val, args.Error(1)
}

func (m *MockLambdaService) ListTriggersPage(ctx context.Context, name string, token *string) (*aws.TriggerPage, error) {
	args := m.Called(ctx, name, token)
	val, _ := args.Get(0).(*aws.TriggerPage)
	return val, args.Error(1)
}
