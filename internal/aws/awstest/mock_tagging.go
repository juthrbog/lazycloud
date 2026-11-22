package awstest

import (
	"context"

	"github.com/stretchr/testify/mock"

	"github.com/juthrbog/lazycloud/internal/aws"
)

// MockTaggingService is a testify mock implementing aws.TaggingService.
type MockTaggingService struct {
	mock.Mock
}

var _ aws.TaggingService = (*MockTaggingService)(nil)

func (m *MockTaggingService) GetResourcesPage(ctx context.Context, filters []aws.TagFilter, token *string) (*aws.TaggedPage, error) {
	args := m.Called(ctx, filters, token)
	val, _ := args.Get(0).(*aws.TaggedPage)
	return val, args.Error(1)
}
