package awstest

import (
	"context"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/juthrbog/lazycloud/internal/aws"
)

// MockS3Service is a testify mock implementing aws.S3Service.
type MockS3Service struct {
	mock.Mock
}

var _ aws.S3Service = (*MockS3Service)(nil)

func (m *MockS3Service) ListBuckets(ctx context.Context) ([]aws.Bucket, error) {
	args := m.Called(ctx)
	return args.Get(0).([]aws.Bucket), args.Error(1)
}

func (m *MockS3Service) ListObjectsPage(ctx context.Context, bucket, prefix string, token *string) (*aws.ObjectPage, error) {
	args := m.Called(ctx, bucket, prefix, token)
	val, _ := args.Get(0).(*aws.ObjectPage)
	return val, args.Error(1)
}

func (m *MockS3Service) HeadObject(ctx context.Context, bucket, key string) (*aws.ObjectMeta, error) {
	args := m.Called(ctx, bucket, key)
	val, _ := args.Get(0).(*aws.ObjectMeta)
	return val, args.Error(1)
}

func (m *MockS3Service) GetObjectContent(ctx context.Context, bucket, key string, maxBytes int64) (string, error) {
	args := m.Called(ctx, bucket, key, maxBytes)
	return args.String(0), args.Error(1)
}

func (m *MockS3Service) PresignGetObject(ctx context.Context, bucket, key string, expiry time.Duration) (string, error) {
	args := m.Called(ctx, bucket, key, expiry)
	return args.String(0), args.Error(1)
}

func (m *MockS3Service) ListAllKeys(ctx context.Context, bucket, prefix string) ([]string, error) {
	args := m.Called(ctx, bucket, prefix)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockS3Service) DownloadObject(ctx context.Context, bucket, key, destPath string) error {
	args := m.Called(ctx, bucket, key, destPath)
	return args.Error(0)
}

func (m *MockS3Service) ListObjectVersions(ctx context.Context, bucket, key string) ([]aws.ObjectVersion, error) {
	args := m.Called(ctx, bucket, key)
	return args.Get(0).([]aws.ObjectVersion), args.Error(1)
}

func (m *MockS3Service) GetBucketProperties(ctx context.Context, bucket string) (*aws.BucketProperties, error) {
	args := m.Called(ctx, bucket)
	val, _ := args.Get(0).(*aws.BucketProperties)
	return val, args.Error(1)
}

func (m *MockS3Service) GetBucketRegion(ctx context.Context, bucket string) (string, error) {
	args := m.Called(ctx, bucket)
	return args.String(0), args.Error(1)
}

func (m *MockS3Service) DeleteObject(ctx context.Context, bucket, key string) error {
	args := m.Called(ctx, bucket, key)
	return args.Error(0)
}

func (m *MockS3Service) DeleteObjects(ctx context.Context, bucket string, keys []string) error {
	args := m.Called(ctx, bucket, keys)
	return args.Error(0)
}

func (m *MockS3Service) DeleteBucket(ctx context.Context, bucket string) error {
	args := m.Called(ctx, bucket)
	return args.Error(0)
}

func (m *MockS3Service) EmptyAndDeleteBucket(ctx context.Context, bucket string) error {
	args := m.Called(ctx, bucket)
	return args.Error(0)
}

func (m *MockS3Service) CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error {
	args := m.Called(ctx, srcBucket, srcKey, dstBucket, dstKey)
	return args.Error(0)
}

func (m *MockS3Service) MoveObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error {
	args := m.Called(ctx, srcBucket, srcKey, dstBucket, dstKey)
	return args.Error(0)
}

func (m *MockS3Service) CreateBucket(ctx context.Context, name, region string) error {
	args := m.Called(ctx, name, region)
	return args.Error(0)
}
