package views

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/stretchr/testify/mock"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
)

func TestTeatest_S3ListLoadsBuckets(t *testing.T) {
	mockS3 := new(awstest.MockS3Service)
	mockS3.On("ListBuckets", mock.Anything).Return([]aws.Bucket{
		{Name: "alpha-bucket", CreationDate: time.Now()},
		{Name: "beta-bucket", CreationDate: time.Now()},
	}, nil)

	view := NewS3List(mockS3, "us-east-1")
	tm := teatest.NewTestModel(t, view, teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		return bytes.Contains(bts, []byte("alpha-bucket")) &&
			bytes.Contains(bts, []byte("beta-bucket"))
	}, teatest.WithDuration(5*time.Second))

	if err := tm.Quit(); err != nil {
		t.Fatal(err)
	}
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	mockS3.AssertExpectations(t)
}

func TestTeatest_S3ListFilter(t *testing.T) {
	mockS3 := new(awstest.MockS3Service)
	mockS3.On("ListBuckets", mock.Anything).Return([]aws.Bucket{
		{Name: "alpha-bucket", CreationDate: time.Now()},
		{Name: "beta-bucket", CreationDate: time.Now()},
		{Name: "gamma-bucket", CreationDate: time.Now()},
	}, nil)

	view := NewS3List(mockS3, "us-east-1")
	tm := teatest.NewTestModel(t, view, teatest.WithInitialTermSize(80, 24))

	// Wait for all buckets to load
	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		return bytes.Contains(bts, []byte("gamma-bucket"))
	}, teatest.WithDuration(5*time.Second))

	// Activate filter and type
	tm.Send(keyPress('/'))
	tm.Type("alpha")

	// Verify filter narrows results — only alpha-bucket visible
	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		return bytes.Contains(bts, []byte("alpha")) && !bytes.Contains(bts, []byte("gamma"))
	}, teatest.WithDuration(3*time.Second))

	if err := tm.Quit(); err != nil {
		t.Fatal(err)
	}
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

func TestTeatest_S3ObjectsLoads(t *testing.T) {
	mockS3 := new(awstest.MockS3Service)
	mockS3.On("ListObjectsPage", mock.Anything, "test-bucket", "", (*string)(nil)).Return(&aws.ObjectPage{
		Objects: []aws.S3Object{
			{Key: "readme.md", Size: 1024, LastModified: time.Now(), StorageClass: "STANDARD"},
		},
		Prefixes: []string{"docs/"},
	}, nil)

	view := NewS3Objects(mockS3, "test-bucket", "")
	tm := teatest.NewTestModel(t, view, teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		return bytes.Contains(bts, []byte("readme.md")) &&
			bytes.Contains(bts, []byte("docs/"))
	}, teatest.WithDuration(5*time.Second))

	if err := tm.Quit(); err != nil {
		t.Fatal(err)
	}
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	mockS3.AssertExpectations(t)
}

func TestTeatest_S3ObjectsError(t *testing.T) {
	mockS3 := new(awstest.MockS3Service)
	mockS3.On("ListObjectsPage", mock.Anything, "test-bucket", "", (*string)(nil)).Return(
		(*aws.ObjectPage)(nil), fmt.Errorf("access denied"),
	)

	view := NewS3Objects(mockS3, "test-bucket", "")
	tm := teatest.NewTestModel(t, view, teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		return bytes.Contains(bts, []byte("access denied"))
	}, teatest.WithDuration(5*time.Second))

	if err := tm.Quit(); err != nil {
		t.Fatal(err)
	}
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}
