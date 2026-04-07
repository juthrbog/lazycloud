package awstest

import (
	"context"

	"github.com/stretchr/testify/mock"

	"github.com/juthrbog/lazycloud/internal/aws"
)

// MockSQSService is a testify mock implementing aws.SQSService.
type MockSQSService struct {
	mock.Mock
}

var _ aws.SQSService = (*MockSQSService)(nil)

func (m *MockSQSService) ListQueuesPage(ctx context.Context, token *string) (*aws.QueuePage, error) {
	args := m.Called(ctx, token)
	val, _ := args.Get(0).(*aws.QueuePage)
	return val, args.Error(1)
}

func (m *MockSQSService) GetQueueAttributes(ctx context.Context, queueURL string) (*aws.Queue, error) {
	args := m.Called(ctx, queueURL)
	val, _ := args.Get(0).(*aws.Queue)
	return val, args.Error(1)
}

func (m *MockSQSService) FetchAllQueueAttributes(ctx context.Context, urls []string) ([]aws.Queue, error) {
	args := m.Called(ctx, urls)
	val, _ := args.Get(0).([]aws.Queue)
	return val, args.Error(1)
}

func (m *MockSQSService) ReceiveMessages(ctx context.Context, queueURL string, maxMessages int32) ([]aws.SQSMessage, error) {
	args := m.Called(ctx, queueURL, maxMessages)
	val, _ := args.Get(0).([]aws.SQSMessage)
	return val, args.Error(1)
}

func (m *MockSQSService) SendMessage(ctx context.Context, queueURL, body string, delaySeconds int32, groupID, dedupID string) error {
	args := m.Called(ctx, queueURL, body, delaySeconds, groupID, dedupID)
	return args.Error(0)
}

func (m *MockSQSService) DeleteMessage(ctx context.Context, queueURL, receiptHandle string) error {
	args := m.Called(ctx, queueURL, receiptHandle)
	return args.Error(0)
}

func (m *MockSQSService) DeleteMessageBatch(ctx context.Context, queueURL string, receiptHandles []string) error {
	args := m.Called(ctx, queueURL, receiptHandles)
	return args.Error(0)
}

func (m *MockSQSService) PurgeQueue(ctx context.Context, queueURL string) error {
	args := m.Called(ctx, queueURL)
	return args.Error(0)
}

func (m *MockSQSService) DeleteQueue(ctx context.Context, queueURL string) error {
	args := m.Called(ctx, queueURL)
	return args.Error(0)
}

func (m *MockSQSService) ListDeadLetterSourceQueues(ctx context.Context, queueURL string) ([]string, error) {
	args := m.Called(ctx, queueURL)
	val, _ := args.Get(0).([]string)
	return val, args.Error(1)
}

func (m *MockSQSService) StartMessageMoveTask(ctx context.Context, sourceArn, destArn string) (string, error) {
	args := m.Called(ctx, sourceArn, destArn)
	return args.String(0), args.Error(1)
}

func (m *MockSQSService) ListMessageMoveTasks(ctx context.Context, sourceArn string) ([]aws.MessageMoveTask, error) {
	args := m.Called(ctx, sourceArn)
	val, _ := args.Get(0).([]aws.MessageMoveTask)
	return val, args.Error(1)
}
