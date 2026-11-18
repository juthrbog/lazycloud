package views

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
)

// runDelete runs the background operation of deleteEntries and returns its
// result.
func runDelete(t *testing.T, v *S3Objects, entries []s3TableEntry) s3DeleteCompleteMsg {
	t.Helper()
	start, ok := v.deleteEntries(entries)().(msg.StartOpMsg)
	require.True(t, ok)
	res, ok := start.Run(t.Context()).(s3DeleteCompleteMsg)
	require.True(t, ok)
	return res
}

// T7: if the listing of a folder fails, nothing is deleted, also not the
// files that were selected with the folder.
func TestS3Objects_DeleteStopsWhenAFolderListingFails(t *testing.T) {
	svc := new(awstest.MockS3Service)
	svc.On("ListAllKeys", mock.Anything, "b", "logs/").Return([]string(nil), errors.New("AccessDenied"))
	v := NewS3Objects(svc, "b", "")

	res := runDelete(t, v, []s3TableEntry{{fullPath: "a.txt"}, {isFolder: true, fullPath: "logs/"}})

	require.Error(t, res.err)
	assert.Contains(t, res.err.Error(), "listing objects in logs/")
	assert.Contains(t, res.err.Error(), "AccessDenied")
	assert.Zero(t, res.count)
	svc.AssertNotCalled(t, "DeleteObjects")
	svc.AssertExpectations(t)
}

// T7: an error of the delete reaches the result, with the number of keys.
func TestS3Objects_DeleteReportsTheDeleteError(t *testing.T) {
	svc := new(awstest.MockS3Service)
	svc.On("DeleteObjects", mock.Anything, "b", []string{"a.txt", "b.txt"}).Return(errors.New("partial failure"))
	v := NewS3Objects(svc, "b", "dir/")

	res := runDelete(t, v, []s3TableEntry{{fullPath: "a.txt"}, {fullPath: "b.txt"}})

	require.EqualError(t, res.err, "partial failure")
	assert.Equal(t, 2, res.count)
	assert.Equal(t, "b", res.bucket)
	assert.Equal(t, "dir/", res.prefix)
	svc.AssertExpectations(t)
}

// T7: an empty folder gives no keys, and no delete call is made.
func TestS3Objects_DeleteOfAnEmptyFolder(t *testing.T) {
	svc := new(awstest.MockS3Service)
	svc.On("ListAllKeys", mock.Anything, "b", "empty/").Return([]string{}, nil)
	v := NewS3Objects(svc, "b", "")

	res := runDelete(t, v, []s3TableEntry{{isFolder: true, fullPath: "empty/"}})

	require.NoError(t, res.err)
	assert.Zero(t, res.count)
	svc.AssertNotCalled(t, "DeleteObjects")
	svc.AssertExpectations(t)
}
