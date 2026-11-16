package views

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

func TestS3List_DeleteTagHoldsTargets(t *testing.T) {
	setReadOnly(t, false)
	view, _ := newTestS3List()
	loadBuckets(view)
	view.Update(keyPress(' '))
	view.Update(keyPress('j'))
	view.Update(keyPress(' '))

	_, cmd := view.Update(keyPress('x'))
	require.NotNil(t, cmd)
	req, ok := cmd().(msg.RequestConfirmMsg)
	require.True(t, ok)
	tag, ok := req.Tag.(*s3BucketDeleteTag)
	require.True(t, ok, "the confirm carries an s3BucketDeleteTag")
	assert.Equal(t, []string{"test-bucket", "other-bucket"}, tag.names)
}

func TestS3List_UntaggedConfirmDoesNotDelete(t *testing.T) {
	setReadOnly(t, false)
	view, svc := newTestS3List()
	loadBuckets(view)
	view.Update(keyPress('x'))

	_, cmd := view.Update(msg.ConfirmResultMsg{Confirmed: true})
	assert.Nil(t, cmd, "a reply without the tag of the prompt runs nothing")
	svc.AssertNotCalled(t, "EmptyAndDeleteBucket", mock.Anything, mock.Anything)
}

func TestS3List_ReadOnlyBetweenPromptAndConfirmDoesNotDelete(t *testing.T) {
	setReadOnly(t, false)
	view, svc := newTestS3List()
	loadBuckets(view)
	_, cmd := view.Update(keyPress('x'))
	req := requestOf[msg.RequestConfirmMsg](t, cmd)

	ui.SetReadOnly(true)
	_, cmd = view.Update(confirmReply(req, true))
	require.NotNil(t, cmd)
	assert.Equal(t, msg.ToastError("ReadOnly mode — press W to switch"), cmd())
	svc.AssertNotCalled(t, "EmptyAndDeleteBucket", mock.Anything, mock.Anything)
}
