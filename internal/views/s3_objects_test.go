package views

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

func newTestS3Objects() (*S3Objects, *awstest.MockS3Service) {
	m := new(awstest.MockS3Service)
	view := NewS3Objects(m, "test-bucket", "")
	view.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return view, m
}

func loadObjects(view *S3Objects) {
	view.Update(s3PageLoadedMsg{
		bucket: "test-bucket",
		prefix: "",
		objects: []aws.S3Object{
			{Key: "file1.txt", Size: 1024, LastModified: time.Now(), StorageClass: "STANDARD"},
			{Key: "file2.json", Size: 2048, LastModified: time.Now(), StorageClass: "STANDARD"},
		},
		prefixes:     []string{"folder1/"},
		hasMorePages: false,
		pageNum:      1,
	})
}

// --- isPreviewable tests ---

func TestIsPreviewable(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		key         string
		size        int64
		want        bool
	}{
		{"text/plain small", "text/plain", "file.txt", 100, true},
		{"text/html", "text/html", "page.html", 500, true},
		{"application/json", "application/json", "data.json", 1000, true},
		{"image/png rejected", "image/png", "photo.png", 100, false},
		{"video rejected", "video/mp4", "clip.mp4", 100, false},
		{"too large", "text/plain", "huge.txt", 2 << 20, false},
		{"octet-stream with .json ext", "application/octet-stream", "data.json", 100, true},
		{"octet-stream with .exe ext", "application/octet-stream", "app.exe", 100, false},
		{"empty content type with .go", "", "main.go", 500, true},
		{"empty content type with .bin", "", "data.bin", 500, false},
		{"application/yaml", "application/yaml", "config.yaml", 100, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isPreviewable(tt.contentType, tt.key, tt.size))
		})
	}
}

// --- ReadOnly guard tests ---

func TestS3Objects_ReadOnlyBlocksDelete(t *testing.T) {
	ui.ReadOnly = true
	defer func() { ui.ReadOnly = true }()

	view, _ := newTestS3Objects()
	loadObjects(view)

	_, cmd := view.Update(keyPress('x'))
	require.NotNil(t, cmd)

	result := cmd()
	toast, ok := result.(msg.ToastMsg)
	require.True(t, ok, "expected ToastMsg, got %T", result)
	assert.Equal(t, 2, toast.Level)
	assert.Contains(t, toast.Text, "ReadOnly")
}

func TestS3Objects_ReadOnlyBlocksCopy(t *testing.T) {
	ui.ReadOnly = true
	defer func() { ui.ReadOnly = true }()

	view, _ := newTestS3Objects()
	loadObjects(view)

	_, cmd := view.Update(keyPress('c'))
	require.NotNil(t, cmd)

	result := cmd()
	toast, ok := result.(msg.ToastMsg)
	require.True(t, ok, "expected ToastMsg, got %T", result)
	assert.Contains(t, toast.Text, "ReadOnly")
}

func TestS3Objects_ReadOnlyBlocksMove(t *testing.T) {
	ui.ReadOnly = true
	defer func() { ui.ReadOnly = true }()

	view, _ := newTestS3Objects()
	loadObjects(view)

	_, cmd := view.Update(keyPress('m'))
	require.NotNil(t, cmd)

	result := cmd()
	toast, ok := result.(msg.ToastMsg)
	require.True(t, ok, "expected ToastMsg, got %T", result)
	assert.Contains(t, toast.Text, "ReadOnly")
}

// --- Page loading tests ---

func TestS3Objects_PageLoadedBuildsEntries(t *testing.T) {
	view, _ := newTestS3Objects()

	view.Update(s3PageLoadedMsg{
		bucket: "test-bucket",
		prefix: "",
		objects: []aws.S3Object{
			{Key: "file.txt", Size: 100},
		},
		prefixes:     []string{"docs/"},
		hasMorePages: false,
		pageNum:      1,
	})

	assert.Len(t, view.entries, 2)

	// First entry is folder
	assert.True(t, view.entries[0].isFolder)
	assert.Equal(t, "docs/", view.entries[0].fullPath)

	// Second entry is object
	assert.False(t, view.entries[1].isFolder)
	assert.Equal(t, "file.txt", view.entries[1].fullPath)
}

func TestS3Objects_StalePageDiscarded(t *testing.T) {
	view, _ := newTestS3Objects()

	// Send page for a different bucket
	view.Update(s3PageLoadedMsg{
		bucket:  "wrong-bucket",
		prefix:  "",
		objects: []aws.S3Object{{Key: "should-not-appear.txt"}},
		pageNum: 1,
	})

	assert.Empty(t, view.entries)
	assert.Empty(t, view.objects)
}

// --- KeyMap tests ---

func TestS3Objects_KeyMapMutatingHintsAreReadWriteOnly(t *testing.T) {
	view, _ := newTestS3Objects()
	hints := view.KeyMap()

	rwDescs := []string{"delete", "copy", "move"}
	for _, h := range hints {
		for _, rw := range rwDescs {
			if h.Desc == rw {
				assert.Equal(t, ui.ModeReadWrite, h.Mode, "%q hint should require ReadWrite mode", rw)
			}
		}
	}
}

func TestS3Objects_KeyMapAlwaysContainsAllHints(t *testing.T) {
	view, _ := newTestS3Objects()
	hints := view.KeyMap()

	descs := make([]string, len(hints))
	for i, h := range hints {
		descs[i] = h.Desc
	}
	assert.Contains(t, descs, "view")
	assert.Contains(t, descs, "download")
	assert.Contains(t, descs, "delete")
	assert.Contains(t, descs, "copy")
	assert.Contains(t, descs, "move")
}
