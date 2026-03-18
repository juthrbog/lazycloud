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

func keyPress(char rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: char, Text: string(char)}
}

func newTestS3List() (*S3List, *awstest.MockS3Service) {
	m := new(awstest.MockS3Service)
	view := NewS3List(m, "us-east-1")
	// Give it a window size so table is usable
	view.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return view, m
}

func loadBuckets(view *S3List) {
	view.Update(s3BucketsLoadedMsg{buckets: []aws.Bucket{
		{Name: "test-bucket", CreationDate: time.Now()},
		{Name: "other-bucket", CreationDate: time.Now()},
	}})
}

func TestS3List_ReadOnlyBlocksCreate(t *testing.T) {
	ui.ReadOnly = true
	defer func() { ui.ReadOnly = true }()

	view, _ := newTestS3List()
	loadBuckets(view)

	_, cmd := view.Update(keyPress('n'))
	require.NotNil(t, cmd)

	result := cmd()
	toast, ok := result.(msg.ToastMsg)
	require.True(t, ok, "expected ToastMsg, got %T", result)
	assert.Equal(t, 2, toast.Level) // error
	assert.Contains(t, toast.Text, "ReadOnly")
}

func TestS3List_ReadOnlyBlocksDelete(t *testing.T) {
	ui.ReadOnly = true
	defer func() { ui.ReadOnly = true }()

	view, _ := newTestS3List()
	loadBuckets(view)

	_, cmd := view.Update(keyPress('x'))
	require.NotNil(t, cmd)

	result := cmd()
	toast, ok := result.(msg.ToastMsg)
	require.True(t, ok, "expected ToastMsg, got %T", result)
	assert.Equal(t, 2, toast.Level)
	assert.Contains(t, toast.Text, "ReadOnly")
}

func TestS3List_ReadWriteAllowsCreate(t *testing.T) {
	ui.ReadOnly = false
	defer func() { ui.ReadOnly = true }()

	view, _ := newTestS3List()
	loadBuckets(view)

	_, cmd := view.Update(keyPress('n'))
	assert.True(t, view.creating, "expected creating mode to be active")
	assert.Nil(t, cmd)
}

func TestS3List_BucketsLoadedMsg(t *testing.T) {
	view, _ := newTestS3List()
	assert.True(t, view.loading)

	buckets := []aws.Bucket{
		{Name: "alpha", CreationDate: time.Now()},
		{Name: "beta", CreationDate: time.Now()},
	}
	view.Update(s3BucketsLoadedMsg{buckets: buckets})

	assert.False(t, view.loading)
	assert.Len(t, view.buckets, 2)
	assert.Equal(t, "alpha", view.buckets[0].Name)
}

func TestS3List_ErrorMsg(t *testing.T) {
	view, _ := newTestS3List()
	assert.True(t, view.loading)

	view.Update(msg.ErrorMsg{Err: fmt.Errorf("access denied"), Context: "S3"})

	assert.False(t, view.loading)
	assert.Error(t, view.err)
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
	ui.ReadOnly = false
	defer func() { ui.ReadOnly = true }()

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

func TestS3List_KeyMapHidesMutatingInReadOnly(t *testing.T) {
	ui.ReadOnly = true
	defer func() { ui.ReadOnly = true }()

	view, _ := newTestS3List()
	hints := view.KeyMap()

	descs := make([]string, len(hints))
	for i, h := range hints {
		descs[i] = h.Desc
	}
	assert.NotContains(t, descs, "new bucket")
	assert.NotContains(t, descs, "delete bucket")
	assert.Contains(t, descs, "browse")
}

func TestS3List_KeyMapShowsMutatingInReadWrite(t *testing.T) {
	ui.ReadOnly = false
	defer func() { ui.ReadOnly = true }()

	view, _ := newTestS3List()
	hints := view.KeyMap()

	descs := make([]string, len(hints))
	for i, h := range hints {
		descs[i] = h.Desc
	}
	assert.Contains(t, descs, "new bucket")
	assert.Contains(t, descs, "delete bucket")
}
