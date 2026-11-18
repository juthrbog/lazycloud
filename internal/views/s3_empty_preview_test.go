package views

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
)

// L16: the preview of an empty text object opens an empty content view and
// reads nothing from S3. A range request on an empty object fails with 416.
func TestS3Objects_PreviewOfAnEmptyObject(t *testing.T) {
	view, m := newTestS3Objects()

	_, cmd := view.Update(s3PreviewCheckMsg{meta: &aws.ObjectMeta{Key: "dir/empty.txt", ContentType: "text/plain", Size: 0}})
	require.NotNil(t, cmd)
	got := cmd()
	require.Equal(t, s3ObjectContentMsg{key: "dir/empty.txt"}, got)

	_, cmd = view.Update(got)
	require.NotNil(t, cmd)
	nav, ok := cmd().(msg.NavigateMsg)
	require.True(t, ok)
	assert.Equal(t, "content", nav.ViewID)
	assert.Equal(t, "empty.txt", nav.Params["title"])
	assert.Empty(t, nav.Params["content"])
	m.AssertNotCalled(t, "GetObjectContent")
}

// A text object with content still reads it from S3.
func TestS3Objects_PreviewOfATextObjectReadsIt(t *testing.T) {
	view, m := newTestS3Objects()
	m.On("GetObjectContent", mock.Anything, "test-bucket", "a.txt", int64(1<<20)).Return("hello", nil).Once()

	_, cmd := view.Update(s3PreviewCheckMsg{meta: &aws.ObjectMeta{Key: "a.txt", ContentType: "text/plain", Size: 5}})
	require.NotNil(t, cmd)

	assert.Equal(t, s3ObjectContentMsg{key: "a.txt", content: "hello"}, cmd())
	m.AssertExpectations(t)
}
