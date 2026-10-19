package views

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
)

func newTestS3Versions() *S3Versions {
	m := new(awstest.MockS3Service)
	view := NewS3Versions(m, "test-bucket", "file.txt")
	view.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return view
}

func TestS3Versions_LoadedMsg(t *testing.T) {
	view := newTestS3Versions()
	assert.True(t, view.loading)

	versions := []aws.ObjectVersion{
		{Key: "file.txt", VersionID: "v1", Size: 100, LastModified: time.Now(), IsLatest: true},
		{Key: "file.txt", VersionID: "v2", Size: 200, LastModified: time.Now()},
	}
	view.Update(s3VersionsLoadedMsg{versions: versions})

	assert.False(t, view.loading)
	assert.Len(t, view.versions, 2)
	assert.Equal(t, "v1", view.versions[0].VersionID)
}

// Unversioned buckets return the version ID "null". Viewing it must not panic.
func TestS3Versions_ViewNullVersionID(t *testing.T) {
	view := newTestS3Versions()
	view.Update(s3VersionsLoadedMsg{versions: []aws.ObjectVersion{
		{Key: "file.txt", VersionID: "null", IsLatest: true},
	}})

	_, cmd := view.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	var result tea.Msg
	require.NotPanics(t, func() { result = cmd() })

	nav, ok := result.(msg.NavigateMsg)
	require.True(t, ok, "expected NavigateMsg, got %T", result)
	assert.Equal(t, "file.txt (vnull)", nav.Params["title"])
}

// After a sort, enter must show the version under the cursor, not the one at
// the same position in the unsorted slice.
func TestS3Versions_ViewAfterSortShowsCursorVersion(t *testing.T) {
	view := newTestS3Versions()
	view.Update(s3VersionsLoadedMsg{versions: []aws.ObjectVersion{
		{Key: "file.txt", VersionID: "aaaaaaaaaaaa"},
		{Key: "file.txt", VersionID: "zzzzzzzzzzzz"},
	}})
	view.table.Sort(0)
	view.table.Sort(0)                              // second press sorts descending
	view.Update(tea.KeyPressMsg{Code: tea.KeyHome}) // top row of the sorted table
	require.Equal(t, "zzzzzzzzzzzz", view.table.SelectedRow()[0])

	_, cmd := view.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	nav := cmd().(msg.NavigateMsg)
	assert.Contains(t, nav.Params["content"], "zzzzzzzzzzzz")
	assert.NotContains(t, nav.Params["content"], "aaaaaaaaaaaa")
	assert.Equal(t, "file.txt (vzzzzzzzz)", nav.Params["title"])
}

func TestS3Versions_ErrorClearsLoading(t *testing.T) {
	view := newTestS3Versions()
	assert.True(t, view.loading)

	view.Update(msg.ErrorMsg{Err: fmt.Errorf("forbidden"), Context: "versions"})

	assert.False(t, view.loading)
	assert.Error(t, view.err)
}

func TestS3Versions_EscNavigatesBack(t *testing.T) {
	view := newTestS3Versions()
	view.loading = false // skip spinner handling

	_, cmd := view.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.NotNil(t, cmd)

	result := cmd()
	_, ok := result.(msg.NavigateBackMsg)
	assert.True(t, ok, "expected NavigateBackMsg, got %T", result)
}
