package awstest

import (
	"context"

	"github.com/stretchr/testify/mock"

	"github.com/juthrbog/lazycloud/internal/aws"
)

// MockLogsService is a testify mock implementing aws.LogsService.
type MockLogsService struct {
	mock.Mock
}

var _ aws.LogsService = (*MockLogsService)(nil)

func (m *MockLogsService) ListLogGroupsPage(ctx context.Context, token *string) (*aws.LogGroupPage, error) {
	args := m.Called(ctx, token)
	val, _ := args.Get(0).(*aws.LogGroupPage)
	return val, args.Error(1)
}

func (m *MockLogsService) GetLogGroup(ctx context.Context, name string) (*aws.LogGroup, error) {
	args := m.Called(ctx, name)
	val, _ := args.Get(0).(*aws.LogGroup)
	return val, args.Error(1)
}

func (m *MockLogsService) ListLogStreamsPage(ctx context.Context, group string, token *string) (*aws.LogStreamPage, error) {
	args := m.Called(ctx, group, token)
	val, _ := args.Get(0).(*aws.LogStreamPage)
	return val, args.Error(1)
}

func (m *MockLogsService) GetStreamEvents(ctx context.Context, q aws.EventQuery) (*aws.EventPage, error) {
	args := m.Called(ctx, q)
	val, _ := args.Get(0).(*aws.EventPage)
	return val, args.Error(1)
}

func (m *MockLogsService) FilterEvents(ctx context.Context, q aws.EventQuery) (*aws.EventPage, error) {
	args := m.Called(ctx, q)
	val, _ := args.Get(0).(*aws.EventPage)
	return val, args.Error(1)
}

func (m *MockLogsService) ListLogGroupTags(ctx context.Context, arn string) (map[string]string, error) {
	args := m.Called(ctx, arn)
	val, _ := args.Get(0).(map[string]string)
	return val, args.Error(1)
}
