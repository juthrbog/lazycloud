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
	assert.True(t, view.entries["docs/"].isFolder)
	assert.False(t, view.entries["file.txt"].isFolder)
	assert.Equal(t, []string{"docs/", "file.txt"}, view.table.VisibleKeys(), "folders come first")
}

// C3: a later page with a new folder moves every object down by one row.
// The selection must stay on the selected object.
func TestS3Objects_SelectionFollowsObjectWhenFolderArrives(t *testing.T) {
	ui.SetReadOnly(false)
	defer func() { ui.SetReadOnly(true) }()
	view, _ := newTestS3Objects()
	view.Update(s3PageLoadedMsg{
		bucket: "test-bucket", objects: []aws.S3Object{{Key: "a.txt"}, {Key: "b.txt"}},
		hasMorePages: true, pageNum: 1,
	})
	view.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	view.Update(tea.KeyPressMsg{Code: ' ', Text: " "}) // select b.txt

	view.Update(s3PageLoadedMsg{
		bucket: "test-bucket", prefixes: []string{"new/"}, objects: []aws.S3Object{{Key: "c.txt"}},
		pageNum: 2,
	})
	require.Equal(t, []string{"new/", "a.txt", "b.txt", "c.txt"}, view.table.VisibleKeys())

	entries := view.collectDeleteEntries()
	require.Len(t, entries, 1)
	assert.Equal(t, "b.txt", entries[0].fullPath)
}

func TestS3Objects_DeletePromptListsNames(t *testing.T) {
	ui.SetReadOnly(false)
	defer func() { ui.SetReadOnly(true) }()
	view, _ := newTestS3Objects()
	view.Update(s3PageLoadedMsg{
		bucket: "test-bucket", prefixes: []string{"logs/"}, objects: []aws.S3Object{{Key: "a.txt"}},
		pageNum: 1,
	})
	view.table.SelectAll()

	_, cmd := view.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	req, ok := firstOfType[msg.RequestConfirmMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "Delete 1 folder(s) and 1 object(s)?\n  logs/\n  a.txt\nA folder is deleted with every object in it.", req.Message)
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
			if h.Help().Desc == rw {
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
		descs[i] = h.Help().Desc
	}
	assert.Contains(t, descs, "view")
	assert.Contains(t, descs, "download")
	assert.Contains(t, descs, "delete")
	assert.Contains(t, descs, "copy")
	assert.Contains(t, descs, "move")
}

// --- Responsive columns ---

func TestS3Objects_NarrowTierHidesColumns(t *testing.T) {
	cols := s3ObjectColumns(ui.TierNarrow)
	assert.Len(t, cols, 3, "narrow tier should show 3 columns")
	assert.Equal(t, "Size", cols[2].Title)
}

func TestS3Objects_WideTierShowsAllColumns(t *testing.T) {
	cols := s3ObjectColumns(ui.TierWide)
	assert.Len(t, cols, 5, "wide tier should show 5 columns")
	assert.Equal(t, "Class", cols[4].Title)
}

// --- Loading navigation tests ---

func TestS3Objects_NavigableDuringLoading(t *testing.T) {
	view, _ := newTestS3Objects()

	// Simulate first page loaded with more pages pending
	view.Update(s3PageLoadedMsg{
		bucket: "test-bucket",
		prefix: "",
		objects: []aws.S3Object{
			{Key: "file1.txt", Size: 1024, LastModified: time.Now(), StorageClass: "STANDARD"},
			{Key: "file2.json", Size: 2048, LastModified: time.Now(), StorageClass: "STANDARD"},
			{Key: "file3.go", Size: 512, LastModified: time.Now(), StorageClass: "STANDARD"},
		},
		hasMorePages: true,
		pageNum:      1,
		token:        nil,
	})

	// View should still be loading
	assert.True(t, view.loading, "view should still be loading after partial page")

	// Table should have rows
	_, total := view.table.RowCount()
	assert.Equal(t, 3, total, "table should have 3 rows from first page")

	// Press down arrow — should move cursor even while loading
	view.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	assert.Equal(t, "file2.json", view.table.CursorKey(), "cursor should move to row 1 during loading")

	// Press down again
	view.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	assert.Equal(t, "file3.go", view.table.CursorKey(), "cursor should move to row 2 during loading")
}

func TestS3Objects_CursorPreservedOnNewPage(t *testing.T) {
	view, _ := newTestS3Objects()

	// Load first page
	view.Update(s3PageLoadedMsg{
		bucket: "test-bucket",
		prefix: "",
		objects: []aws.S3Object{
			{Key: "file1.txt", Size: 1024, LastModified: time.Now(), StorageClass: "STANDARD"},
			{Key: "file2.json", Size: 2048, LastModified: time.Now(), StorageClass: "STANDARD"},
		},
		hasMorePages: true,
		pageNum:      1,
	})

	// Move cursor to row 1
	view.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	assert.Equal(t, "file2.json", view.table.CursorKey(), "cursor should be at row 1")

	// Second page arrives — cursor should stay at row 1
	view.Update(s3PageLoadedMsg{
		bucket: "test-bucket",
		prefix: "",
		objects: []aws.S3Object{
			{Key: "file3.go", Size: 512, LastModified: time.Now(), StorageClass: "STANDARD"},
		},
		hasMorePages: false,
		pageNum:      2,
	})

	assert.Equal(t, "file2.json", view.table.CursorKey(), "cursor should stay on its row after new page loads")
	_, total := view.table.RowCount()
	assert.Equal(t, 3, total, "table should now have 3 rows")
	assert.False(t, view.loading, "loading should be complete")
}
