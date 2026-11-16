package views

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
)

// Tests of the confirm tags of S3 buckets and S3 objects (A12 PR 4).

// While a delete confirm is open, a yes with no tag, a typed-nil tag or a
// foreign tag runs nothing in S3 buckets, also with the Action of the open
// confirm (I4, R4). A tag with no buckets runs nothing either.
func TestS3BucketDeleteIgnoresForeignTags(t *testing.T) {
	charS3SetReadOnly(t, false)
	v, _ := charS3Loaded(t, "a-logs", "b-tmp")
	req := charS3AskDelete(t, v)
	for name, tag := range foreignReplies((*s3BucketDeleteTag)(nil)) {
		require.NotPanics(t, func() {
			_, cmd := v.Update(msg.ConfirmResultMsg{Confirmed: true, Action: req.Action, Tag: tag})
			assert.Nil(t, cmd, name)
		}, name)
	}
	_, cmd := v.Update(msg.ConfirmResultMsg{Confirmed: true, Action: req.Action, Tag: &s3BucketDeleteTag{}})
	assert.Nil(t, cmd, "a tag with no buckets runs nothing")
}

// A yes deletes the buckets of its own tag (I3). The test opens a second
// delete confirm for another bucket and answers the first one.
func TestS3BucketDeleteActsOnItsOwnTag(t *testing.T) {
	charS3SetReadOnly(t, false)
	v, _ := charS3Loaded(t, "a-logs", "b-tmp")
	first := charS3AskDelete(t, v)
	tag, ok := first.Tag.(*s3BucketDeleteTag)
	require.True(t, ok)
	assert.Equal(t, []string{"a-logs"}, tag.names)

	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	second := charS3AskDelete(t, v)
	require.Equal(t, []string{"b-tmp"}, second.Tag.(*s3BucketDeleteTag).names)

	op := charS3ConfirmDeleteOf(t, v, first)
	assert.Equal(t, "Delete bucket a-logs", op.Label)
}

// While an overwrite question or a delete confirm is open, a yes with no
// tag, a typed-nil tag or a foreign tag runs nothing in S3 objects, also
// with the Action of the open prompt (I4, R4).
func TestS3ObjectsIgnoresForeignTags(t *testing.T) {
	v, _ := newS3TransferView(t)
	_, cmd := v.Update(s3CopyCompleteMsg{bucket: "test-bucket", srcKey: "a.txt", dstKey: "b.txt", err: aws.ErrDestinationExists})
	overwrite := requestOf[msg.RequestConfirmMsg](t, cmd)
	v.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}) // logs/ → a.txt
	_, cmd = v.Update(keyPress('x'))
	del := requestOf[msg.RequestConfirmMsg](t, cmd)

	for _, c := range []struct {
		action   string
		typedNil msg.PromptTag
	}{
		{overwrite.Action, (*s3OverwriteTag)(nil)},
		{del.Action, (*s3DeleteTag)(nil)},
	} {
		for name, tag := range foreignReplies(c.typedNil) {
			require.NotPanics(t, func() {
				_, cmd := v.Update(msg.ConfirmResultMsg{Confirmed: true, Action: c.action, Tag: tag})
				assert.Nil(t, cmd, "%s %s", c.action, name)
			})
		}
	}
	_, cmd = v.Update(msg.ConfirmResultMsg{Confirmed: true, Action: del.Action, Tag: &s3DeleteTag{}})
	assert.Nil(t, cmd, "a tag with no entries runs nothing")
	assert.False(t, v.spinner.Visible())
}

// A yes deletes the entries of its own tag (I3). The test opens a second
// delete confirm for b.txt and answers the first one, for a.txt. Before
// A12, the second x replaced the entries that the view held.
func TestS3ObjectsDeleteActsOnItsOwnTag(t *testing.T) {
	v, svc := newS3TransferView(t)
	v.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}) // logs/ → a.txt
	_, cmd := v.Update(keyPress('x'))
	first := requestOf[msg.RequestConfirmMsg](t, cmd)
	v.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}) // a.txt → b.txt
	_, cmd = v.Update(keyPress('x'))
	second := requestOf[msg.RequestConfirmMsg](t, cmd)
	require.Contains(t, second.Message, "b.txt")

	svc.On("DeleteObjects", mock.Anything, "test-bucket", []string{"a.txt"}).Return(nil).Once()
	answerYes(t, v, first)
	svc.AssertExpectations(t)
}

// The overwrite question carries the transfer in its tag, and its Action
// has no number any more.
func TestS3OverwriteRequestCarriesTheTransfer(t *testing.T) {
	v, _ := newS3TransferView(t)
	_, cmd := v.Update(s3MoveCompleteMsg{bucket: "test-bucket", srcKey: "a.txt", dstKey: "b.txt", err: aws.ErrDestinationExists})
	req := requestOf[msg.RequestConfirmMsg](t, cmd)
	assert.Equal(t, "s3_overwrite", req.Action)
	assert.Equal(t, &s3OverwriteTag{mode: "move", srcKey: "a.txt", dstKey: "b.txt"}, req.Tag)
}

// The delete confirm carries the entries of the key press in its tag.
func TestS3DeleteRequestCarriesTheEntries(t *testing.T) {
	v, _ := newS3TransferView(t)
	v.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}) // logs/ → a.txt
	_, cmd := v.Update(keyPress('x'))
	req := requestOf[msg.RequestConfirmMsg](t, cmd)
	tag, ok := req.Tag.(*s3DeleteTag)
	require.True(t, ok)
	require.Len(t, tag.entries, 1)
	assert.Equal(t, "a.txt", tag.entries[0].fullPath)
	assert.False(t, tag.entries[0].isFolder)
}
