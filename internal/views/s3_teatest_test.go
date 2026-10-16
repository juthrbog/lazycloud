package views

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/ui"
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

	// The renderer writes only the cells that change, so the output stream
	// does not have to contain "alpha" in one piece: on a slow machine each
	// letter comes in its own frame. Check the final model instead. The
	// keys reach the program before Quit, so the filter holds all of them.
	if err := tm.Quit(); err != nil {
		t.Fatal(err)
	}
	final, ok := tm.FinalModel(t, teatest.WithFinalTimeout(3*time.Second)).(*S3List)
	require.True(t, ok)
	assert.Equal(t, "alpha", final.filter.Value())

	// The table filters when a FilterChangedMsg arrives. A command delivers
	// it, so it can come after Quit. Deliver it here: a second delivery of
	// the same text changes nothing.
	final.Update(ui.FilterChangedMsg{Text: final.filter.Value()})
	assert.Equal(t, []string{"alpha-bucket"}, final.table.VisibleKeys())
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
