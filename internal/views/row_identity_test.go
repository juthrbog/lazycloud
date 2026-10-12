package views

import (
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

// These tests replay the review repros for rows keyed by resource ID (A2):
// selections and the cursor follow the resource, not the row position.

func newBucketList(t *testing.T, names ...string) (*S3List, *awstest.MockS3Service) {
	t.Helper()
	svc := new(awstest.MockS3Service)
	v := NewS3List(svc, "us-east-1")
	v.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	buckets := make([]aws.Bucket, len(names))
	for i, n := range names {
		buckets[i] = aws.Bucket{Name: n}
	}
	v.Update(s3BucketsLoadedMsg{buckets: buckets})
	return v, svc
}

// C4: a bucket that the filter hides is never deleted.
func TestS3List_DeleteSkipsBucketHiddenByFilter(t *testing.T) {
	ui.SetReadOnly(false)
	defer func() { ui.SetReadOnly(true) }()
	v, svc := newBucketList(t, "alpha-prod", "test-1")
	v.Update(keyPress(' ')) // select alpha-prod
	v.Update(ui.FilterChangedMsg{Text: "test"})
	v.Update(keyPress(' ')) // select test-1

	_, cmd := v.Update(keyPress('x'))
	req, ok := firstOfType[msg.RequestConfirmMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Contains(t, req.Message, "Delete bucket 'test-1'?")
	assert.NotContains(t, req.Message, "alpha-prod")
	assert.Equal(t, "test-1", req.Phrase, "the user types the bucket name")

	svc.On("EmptyAndDeleteBucket", mock.Anything, "test-1").Return(nil)
	_, cmd = v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "delete_bucket"})
	collectMsgs(cmd)
	svc.AssertNotCalled(t, "EmptyAndDeleteBucket", mock.Anything, "alpha-prod")
	svc.AssertCalled(t, "EmptyAndDeleteBucket", mock.Anything, "test-1")
}

func TestS3List_DeletePromptForSeveralBuckets(t *testing.T) {
	ui.SetReadOnly(false)
	defer func() { ui.SetReadOnly(true) }()
	v, _ := newBucketList(t, "a-logs", "b-tmp")
	v.table.SelectAll()

	_, cmd := v.Update(keyPress('x'))
	req, ok := firstOfType[msg.RequestConfirmMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Contains(t, req.Message, "Delete 2 buckets?\n  a-logs\n  b-tmp\n")
	assert.Contains(t, req.Message, "permanently")
	assert.Equal(t, "delete 2 buckets", req.Phrase)
}

// C3: creating a bucket that sorts first must not move the selection.
func TestS3List_SelectionStaysAfterCreate(t *testing.T) {
	v, _ := newBucketList(t, "a-prod", "b-tmp")
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	v.Update(keyPress(' ')) // select b-tmp
	require.Equal(t, []string{"b-tmp"}, v.selectedBucketNames())

	v.Update(s3BucketCreatedMsg{name: "0new"})
	v.Update(s3BucketsLoadedMsg{buckets: []aws.Bucket{{Name: "0new"}, {Name: "a-prod"}, {Name: "b-tmp"}}})
	assert.Equal(t, []string{"b-tmp"}, v.selectedBucketNames())
}

// M8: the selection marker never reaches a lookup.
func TestS3List_BrowseSelectedBucket(t *testing.T) {
	v, _ := newBucketList(t, "alpha")
	v.Update(keyPress(' '))

	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	nav, ok := firstOfType[msg.NavigateMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "alpha", nav.Params["bucket"])
}

func newSGList(groups ...string) *SGList {
	v := NewSGList(new(awstest.MockEC2Service))
	v.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	sgs := make([]aws.SecurityGroup, len(groups))
	for i, id := range groups {
		sgs[i] = aws.SecurityGroup{ID: id}
	}
	v.Update(sgListLoadedMsg{groups: sgs})
	return v
}

// M8: y copies the ID, not "✓ sg-…".
func TestSGList_CopyIDOfSelectedRow(t *testing.T) {
	v := newSGList("sg-a")
	v.Update(keyPress(' '))
	_, cmd := v.Update(keyPress('y'))
	toast := toasts(collectMsgs(cmd))
	require.Len(t, toast, 1)
	assert.Equal(t, "Copied: sg-a", toast[0].Text)
}

// M10: focus lands on the right row in a sorted list.
func TestSGList_FocusInSortedList(t *testing.T) {
	v := newSGList("sg-a", "sg-b", "sg-c")
	v.table.Sort(0)
	v.table.Sort(0) // descending: c, b, a

	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "sg-a"})
	panel, ok := firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "sg-a", panel.PanelTitle)
	assert.Equal(t, "sg-a", v.table.CursorKey(), "the cursor matches the panel")
}

func newQueueList() (*SQSQueues, *awstest.MockSQSService) {
	svc := new(awstest.MockSQSService)
	v := NewSQSQueues(svc)
	v.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return v, svc
}

// M10: a DLQ link to another queue moves the cursor to that queue.
func TestSQSQueues_FocusMovesCursor(t *testing.T) {
	v, svc := newQueueList()
	v.Update(sqsAllAttrsLoadedMsg{queues: []aws.Queue{{Name: "qa", URL: "ua"}, {Name: "qb", URL: "ub"}}})
	svc.On("GetQueueAttributes", mock.Anything, "ub").Return(&aws.Queue{Name: "qb", URL: "ub"}, nil)
	svc.On("ListDeadLetterSourceQueues", mock.Anything, "ub").Return([]string(nil), nil)

	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "qb"})
	assert.Equal(t, "ub", v.table.CursorKey())
	require.NotNil(t, cmd, "focus shows the queue details")
	collectMsgs(cmd)
	svc.AssertCalled(t, "GetQueueAttributes", mock.Anything, "ub")
}

func TestSQSQueues_FocusWaitsForLoad(t *testing.T) {
	v, svc := newQueueList()
	svc.On("GetQueueAttributes", mock.Anything, "ub").Return(&aws.Queue{Name: "qb", URL: "ub"}, nil)
	svc.On("ListDeadLetterSourceQueues", mock.Anything, "ub").Return([]string(nil), nil)

	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "qb"})
	assert.Nil(t, cmd)
	_, cmd = v.Update(sqsAllAttrsLoadedMsg{queues: []aws.Queue{{Name: "qa", URL: "ua"}, {Name: "qb", URL: "ub"}}})
	assert.Equal(t, "ub", v.table.CursorKey())
	assert.NotNil(t, cmd)
}

// Selection by index used the unsorted slice, so a sorted list sent the
// wrong queue URLs.
func TestSQSQueues_SelectedURLsInSortedList(t *testing.T) {
	v, _ := newQueueList()
	v.Update(sqsAllAttrsLoadedMsg{queues: []aws.Queue{{Name: "qb", URL: "ub"}, {Name: "qa", URL: "ua"}}})
	v.table.Sort(0)                              // qa, qb; the cursor stays on qb
	v.Update(tea.KeyPressMsg{Code: tea.KeyHome}) // qa
	v.Update(keyPress(' '))
	assert.Equal(t, []string{"ua"}, v.selectedQueueURLs())
}

// Opening a details panel makes the list narrower, and a narrower tier has
// fewer columns. The sort stays on its column, so a DLQ link lands in the
// same sorted list.
func TestSQSQueues_SortSurvivesTierChange(t *testing.T) {
	v, _ := newQueueList()
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	v.Update(sqsAllAttrsLoadedMsg{queues: []aws.Queue{
		{Name: "qa", URL: "ua", ApproximateMessageCount: 3},
		{Name: "qb", URL: "ub", ApproximateMessageCount: 1},
		{Name: "qc", URL: "uc", ApproximateMessageCount: 2},
	}})
	require.Len(t, v.table.Columns(), 7)
	v.table.Sort(2) // Messages
	require.Equal(t, []string{"ub", "uc", "ua"}, v.table.VisibleKeys())

	v.Update(tea.WindowSizeMsg{Width: 40, Height: 30})
	require.Len(t, v.table.Columns(), 2, "the narrow tier")
	assert.Equal(t, []string{"ub", "uc", "ua"}, v.table.VisibleKeys())
	names, col := v.table.SortColumnNames()
	require.GreaterOrEqual(t, col, 0)
	assert.Equal(t, "Messages", names[col])
}

// C3: the refresh after an SSM session reloads the list. The selection
// stays on the same instances when the order changes.
func TestEC2List_SelectionFollowsInstanceAcrossReload(t *testing.T) {
	v := NewEC2List(new(awstest.MockEC2Service), nil)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	v.Update(ec2PageLoadedMsg{instances: []aws.Instance{{ID: "i-1"}, {ID: "i-2"}}, pageNum: 1})
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	v.Update(keyPress(' ')) // select i-2

	v.startLoad()
	v.Update(ec2PageLoadedMsg{gen: v.loadGen, instances: []aws.Instance{{ID: "i-0"}, {ID: "i-1"}, {ID: "i-2"}}, pageNum: 1})
	assert.Equal(t, []string{"i-2"}, v.selectedInstanceIDs())
	assert.Equal(t, "i-2", v.table.CursorKey())
}

func TestEC2List_LaterPagesAppend(t *testing.T) {
	v := NewEC2List(new(awstest.MockEC2Service), nil)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	v.Update(ec2PageLoadedMsg{instances: []aws.Instance{{ID: "i-1"}}, pageNum: 1, hasMorePages: true})
	v.Update(ec2PageLoadedMsg{instances: []aws.Instance{{ID: "i-2"}}, pageNum: 2})
	assert.Equal(t, []string{"i-1", "i-2"}, v.table.VisibleKeys())
}

func TestAMIList_SelectedIDsInSortedList(t *testing.T) {
	v := NewAMIList(new(awstest.MockEC2Service))
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	v.Update(amiListLoadedMsg{amis: []aws.AMI{{ID: "ami-b"}, {ID: "ami-a"}}, owned: true})
	v.table.Sort(0)                              // ami-a, ami-b; the cursor stays on ami-b
	v.Update(tea.KeyPressMsg{Code: tea.KeyHome}) // ami-a
	v.Update(keyPress(' '))
	assert.Equal(t, []string{"ami-a"}, v.selectedAMIIDs())
}
