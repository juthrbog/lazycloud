package views

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// newS3TransferView returns a ReadWrite S3 objects view with a.txt, b.txt,
// and the folder logs/. The cursor is on logs/, the first row.
func newS3TransferView(t *testing.T) (*S3Objects, *awstest.MockS3Service) {
	t.Helper()
	ui.SetReadOnly(false)
	t.Cleanup(func() { ui.SetReadOnly(true) })
	view, m := newTestS3Objects()
	view.Update(s3PageLoadedMsg{
		bucket: "test-bucket", prefixes: []string{"logs/"},
		objects: []aws.S3Object{{Key: "a.txt"}, {Key: "b.txt"}}, pageNum: 1,
	})
	return view, m
}

// startTransfer opens the copy or move input on a.txt, types dest, and
// presses enter. It returns the messages of the command that enter gives.
func startTransfer(view *S3Objects, mode rune, dest string) []tea.Msg {
	view.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}) // logs/ → a.txt
	view.Update(keyPress(mode))
	for _, r := range dest {
		view.Update(keyPress(r))
	}
	_, cmd := view.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return collectMsgs(cmd)
}

func TestCopyDestination(t *testing.T) {
	for _, tc := range []struct{ src, typed, want string }{
		{"a/b.txt", "", "a/b.txt_copy"},
		{"a/b.txt", "  ", "a/b.txt_copy"},
		{"a/b.txt", "archive/", "archive/b.txt"},
		{"a/b.txt", "c.txt", "c.txt"},
		{"a/b.txt", " c.txt ", "c.txt"},
	} {
		assert.Equal(t, tc.want, copyDestination(tc.src, tc.typed), "%q + %q", tc.src, tc.typed)
	}
}

// M2(b): a copy onto an existing object asks before it replaces it.
func TestS3Objects_CopyAsksBeforeOverwrite(t *testing.T) {
	view, m := newS3TransferView(t)
	m.On("CopyObject", mock.Anything, "test-bucket", "a.txt", "test-bucket", "b.txt", false).
		Return(aws.ErrDestinationExists).Once()
	m.On("CopyObject", mock.Anything, "test-bucket", "a.txt", "test-bucket", "b.txt", true).
		Return(nil).Once()

	done, ok := firstOfType[s3CopyCompleteMsg](startTransfer(view, 'c', "b.txt"))
	require.True(t, ok)
	_, cmd := view.Update(done)
	req, ok := firstOfType[msg.RequestConfirmMsg](collectMsgs(cmd))
	require.True(t, ok, "the user decides")
	assert.Equal(t, "s3_overwrite", req.Action)
	assert.False(t, req.Quick, "a replace destroys data, so the user types the phrase")
	assert.Equal(t, "s3://test-bucket/b.txt exists.\nReplace it with a.txt?", req.Message)

	_, cmd = view.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "s3_overwrite"})
	done, ok = firstOfType[s3CopyCompleteMsg](collectMsgs(cmd))
	require.True(t, ok)
	require.NoError(t, done.err)
	assert.Nil(t, view.pendingOverwrite)
	m.AssertExpectations(t)
}

func TestS3Objects_DeclinedOverwriteCopiesNothing(t *testing.T) {
	view, m := newS3TransferView(t)
	m.On("CopyObject", mock.Anything, "test-bucket", "a.txt", "test-bucket", "b.txt", false).
		Return(aws.ErrDestinationExists).Once()

	done, _ := firstOfType[s3CopyCompleteMsg](startTransfer(view, 'c', "b.txt"))
	view.Update(done)
	_, cmd := view.Update(ui.ConfirmResultMsg{Confirmed: false, Action: "s3_overwrite"})

	assert.Nil(t, cmd)
	assert.Nil(t, view.pendingOverwrite)
	require.NoError(t, view.err, "a declined prompt is not an error")
	m.AssertNumberOfCalls(t, "CopyObject", 1)
}

// MoveObject wraps the copy error. The prompt still comes.
func TestS3Objects_MoveAsksBeforeOverwrite(t *testing.T) {
	view, m := newS3TransferView(t)
	m.On("MoveObject", mock.Anything, "test-bucket", "a.txt", "test-bucket", "b.txt", false).
		Return(fmt.Errorf("copy: %w", aws.ErrDestinationExists)).Once()
	m.On("MoveObject", mock.Anything, "test-bucket", "a.txt", "test-bucket", "b.txt", true).
		Return(nil).Once()

	done, ok := firstOfType[s3MoveCompleteMsg](startTransfer(view, 'm', "b.txt"))
	require.True(t, ok)
	_, cmd := view.Update(done)
	req, ok := firstOfType[msg.RequestConfirmMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "s3_overwrite", req.Action)

	_, cmd = view.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "s3_overwrite"})
	_, ok = firstOfType[s3MoveCompleteMsg](collectMsgs(cmd))
	require.True(t, ok)
	m.AssertExpectations(t)
}

// M2(c): "archive/" made an object named "archive/", which the list never
// shows. The object now keeps its name in the folder.
func TestS3Objects_CopyIntoAFolder(t *testing.T) {
	view, m := newS3TransferView(t)
	m.On("CopyObject", mock.Anything, "test-bucket", "a.txt", "test-bucket", "archive/a.txt", false).
		Return(nil).Once()
	m.On("ListObjectsPage", mock.Anything, "test-bucket", "", mock.Anything).
		Return(&aws.ObjectPage{}, nil)

	done, ok := firstOfType[s3CopyCompleteMsg](startTransfer(view, 'c', "archive/"))
	require.True(t, ok)

	// The toast names the full key, so the folder shows.
	_, cmd := view.Update(done)
	got := toasts(collectMsgs(cmd))
	require.Len(t, got, 1)
	assert.Equal(t, "Copied → archive/a.txt", got[0].Text)
	m.AssertExpectations(t)
}

// A copy onto itself did nothing, and a move onto itself deleted the object.
func TestS3Objects_DestinationIsTheSource(t *testing.T) {
	for _, mode := range []rune{'c', 'm'} {
		view, m := newS3TransferView(t)

		msgs := startTransfer(view, mode, "a.txt")

		got := toasts(msgs)
		require.Len(t, got, 1, "mode %c", mode)
		assert.Equal(t, "The destination is the source object", got[0].Text)
		m.AssertNotCalled(t, "CopyObject", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		m.AssertNotCalled(t, "MoveObject", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	}
}

// M24: X did the same as x. It now deletes every item that the list shows,
// and the user types the number of items.
func TestS3Objects_DeleteAllAsksForTheCount(t *testing.T) {
	view, _ := newS3TransferView(t)

	_, cmd := view.Update(keyPress('X'))

	req, ok := firstOfType[msg.RequestConfirmMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "delete_objects", req.Action)
	assert.Equal(t, "delete 3 items", req.Phrase)
	assert.Equal(t, "Delete all 3 item(s) in s3://test-bucket/?\n  logs/\n  a.txt\n  b.txt\nA folder is deleted with every object in it.", req.Message)
	assert.Len(t, view.pendingDeleteEntries, 3)
}

func TestS3Objects_DeleteAllSkipsFilteredRows(t *testing.T) {
	view, _ := newS3TransferView(t)
	view.Update(ui.FilterChangedMsg{Text: "a.txt"})

	_, cmd := view.Update(keyPress('X'))

	req, ok := firstOfType[msg.RequestConfirmMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "delete 1 items", req.Phrase)
	require.Len(t, view.pendingDeleteEntries, 1)
	assert.Equal(t, "a.txt", view.pendingDeleteEntries[0].fullPath)
}

// x keeps its behavior: the cursor item, with the plain "confirm" phrase.
func TestS3Objects_DeleteCursorItemKeepsThePlainPhrase(t *testing.T) {
	view, _ := newS3TransferView(t)
	view.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})

	_, cmd := view.Update(keyPress('x'))

	req, ok := firstOfType[msg.RequestConfirmMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Empty(t, req.Phrase)
	assert.Len(t, view.pendingDeleteEntries, 1)
}

// M1: a delete with failed keys said "Deleted N object(s)". S3 deleted the
// other keys, so the list loads again, and the toast names the failures.
func TestS3Objects_PartialDeleteNamesTheFailedKeys(t *testing.T) {
	view, m := newS3TransferView(t)
	m.On("ListObjectsPage", mock.Anything, "test-bucket", "", mock.Anything).
		Return(&aws.ObjectPage{}, nil)

	_, cmd := view.Update(s3DeleteCompleteMsg{count: 3, err: &aws.DeleteObjectsError{
		Failed: []aws.KeyFailure{{Key: "b.txt", Code: "AccessDenied", Message: "Access Denied"}},
	}})

	got := toasts(collectMsgs(cmd))
	require.Len(t, got, 1)
	assert.Equal(t, msg.ToastError("x").Level, got[0].Level)
	assert.Equal(t, "Deleted 2 of 3 object(s). Failed: b.txt (AccessDenied)", got[0].Text)
	require.NoError(t, view.err)
	m.AssertCalled(t, "ListObjectsPage", mock.Anything, "test-bucket", "", mock.Anything)
}

func TestS3Objects_FailedDeleteCallKeepsTheList(t *testing.T) {
	view, _ := newS3TransferView(t)

	_, cmd := view.Update(s3DeleteCompleteMsg{count: 3, err: aws.ErrReadOnly})

	assert.Nil(t, cmd)
	require.ErrorIs(t, view.err, aws.ErrReadOnly)
	assert.Len(t, view.entries, 3)
}

// M25: the toast shows the path that the service wrote, which can be
// "name (1).ext".
func TestS3Objects_DownloadShowsTheWrittenPath(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	view, m := newS3TransferView(t)
	m.On("DownloadObject", mock.Anything, "test-bucket", "a.txt", aws.DefaultDownloadPath("a.txt")).
		Return("/home/u/Downloads/a (1).txt", nil)
	view.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})

	_, cmd := view.Update(keyPress('w'))
	done, ok := firstOfType[s3DownloadCompleteMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "/home/u/Downloads/a (1).txt", done.path)
	_, cmd = view.Update(done)

	got := toasts(collectMsgs(cmd))
	require.Len(t, got, 1)
	assert.Equal(t, "Downloaded → ~/Downloads/a (1).txt", got[0].Text)
}

func TestHomeRelative(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	for p, want := range map[string]string{
		"/home/u/Downloads/a.txt": "~/Downloads/a.txt",
		"/home/u/..x/a.txt":       "~/..x/a.txt",
		"/home/u2/a.txt":          "/home/u2/a.txt",
		"/tmp/a.txt":              "/tmp/a.txt",
	} {
		assert.Equal(t, want, homeRelative(p), p)
	}
}
