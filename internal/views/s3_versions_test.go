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

func newTestS3Versions() (*S3Versions, *awstest.MockS3Service) {
	m := new(awstest.MockS3Service)
	view := NewS3Versions(m, "test-bucket", "file.txt")
	view.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return view, m
}

func TestS3Versions_LoadedMsg(t *testing.T) {
	view, _ := newTestS3Versions()
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

func TestS3Versions_ErrorClearsLoading(t *testing.T) {
	view, _ := newTestS3Versions()
	assert.True(t, view.loading)

	view.Update(msg.ErrorMsg{Err: fmt.Errorf("forbidden"), Context: "versions"})

	assert.False(t, view.loading)
	assert.Error(t, view.err)
}

func TestS3Versions_EscNavigatesBack(t *testing.T) {
	view, _ := newTestS3Versions()
	view.loading = false // skip spinner handling

	_, cmd := view.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.NotNil(t, cmd)

	result := cmd()
	_, ok := result.(msg.NavigateBackMsg)
	assert.True(t, ok, "expected NavigateBackMsg, got %T", result)
}
