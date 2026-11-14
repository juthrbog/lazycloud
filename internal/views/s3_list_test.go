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
	"github.com/juthrbog/lazycloud/internal/ui"
)

func newTestS3List() (*S3List, *awstest.MockS3Service) {
	m := new(awstest.MockS3Service)
	view := NewS3List(m, "us-east-1")
	// Give it a window size so table is usable
	view.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return view, m
}

func loadBuckets(view *S3List) {
	deliverItems(view, &view.listCore, []aws.Bucket{
		{Name: "test-bucket", CreationDate: time.Now()},
		{Name: "other-bucket", CreationDate: time.Now()},
	})
}

func TestS3List_ReadWriteAllowsCreate(t *testing.T) {
	ui.SetReadOnly(false)
	defer func() { ui.SetReadOnly(true) }()

	view, _ := newTestS3List()
	loadBuckets(view)

	_, cmd := view.Update(keyPress('n'))
	assert.True(t, view.createInput.Active(), "expected the create input to be open")
	assert.Nil(t, cmd)
}

func TestS3List_BucketsLoadedMsg(t *testing.T) {
	view, _ := newTestS3List()
	assert.True(t, view.loading)

	buckets := []aws.Bucket{
		{Name: "alpha", CreationDate: time.Now()},
		{Name: "beta", CreationDate: time.Now()},
	}
	deliverItems(view, &view.listCore, buckets)

	assert.False(t, view.loading)
	assert.Len(t, view.items, 2)
	assert.Equal(t, "alpha", view.items[0].Name)
}

func TestS3List_ErrorMsg(t *testing.T) {
	view, _ := newTestS3List()
	assert.True(t, view.loading)

	view.Update(msg.ErrorMsg{Err: fmt.Errorf("access denied"), Context: "S3"})

	assert.False(t, view.loading)
	require.Error(t, view.err)
	assert.Contains(t, view.err.Error(), "access denied")
}

func TestS3List_EnterNavigates(t *testing.T) {
	view, _ := newTestS3List()
	loadBuckets(view)

	_, cmd := view.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	result := cmd()
	nav, ok := result.(msg.NavigateMsg)
	require.True(t, ok, "expected NavigateMsg, got %T", result)
	assert.Equal(t, "s3_objects", nav.ViewID)
	assert.Equal(t, "test-bucket", nav.Params["bucket"])
}

func TestS3List_DeleteTriggersConfirm(t *testing.T) {
	ui.SetReadOnly(false)
	defer func() { ui.SetReadOnly(true) }()

	view, _ := newTestS3List()
	loadBuckets(view)

	_, cmd := view.Update(keyPress('x'))
	require.NotNil(t, cmd)

	result := cmd()
	confirm, ok := result.(msg.RequestConfirmMsg)
	require.True(t, ok, "expected RequestConfirmMsg, got %T", result)
	assert.Equal(t, "delete_bucket", confirm.Action)
	assert.Contains(t, confirm.Message, "test-bucket")
}

// --- Multi-select ---

func TestS3List_SpaceTogglesSelect(t *testing.T) {
	view, _ := newTestS3List()
	loadBuckets(view)

	view.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	assert.Equal(t, 1, view.table.SelectionCount())

	view.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	assert.Equal(t, 0, view.table.SelectionCount())
}

func TestS3List_FooterShowsSelectionCount(t *testing.T) {
	view, _ := newTestS3List()
	loadBuckets(view)

	view.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	assert.Contains(t, view.Footer(), "1 selected")
}

// --- KeyMap ---

func TestS3List_KeyMapMutatingHintsAreReadWriteOnly(t *testing.T) {
	view, _ := newTestS3List()
	hints := view.KeyMap()

	rwDescs := []string{"new bucket", "delete bucket"}
	for _, h := range hints {
		for _, rw := range rwDescs {
			if h.Help().Desc == rw {
				assert.Equal(t, ui.ModeReadWrite, h.Mode, "%q hint should require ReadWrite mode", rw)
			}
		}
	}
}

func TestS3List_KeyMapAlwaysContainsAllHints(t *testing.T) {
	view, _ := newTestS3List()
	hints := view.KeyMap()

	descs := make([]string, len(hints))
	for i, h := range hints {
		descs[i] = h.Help().Desc
	}
	assert.Contains(t, descs, "browse")
	assert.Contains(t, descs, "new bucket")
	assert.Contains(t, descs, "delete bucket")
}
