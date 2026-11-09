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
)

// These tests cover load generations: a full reload supersedes the load in
// progress, so two overlapping loads never append the same rows twice.

func TestEC2List_DoubleRefreshNoDuplicates(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListInstancesPage", mock.Anything, (*string)(nil)).Return(&aws.InstancePage{
		Instances: []aws.Instance{{ID: "i-1", State: "running"}, {ID: "i-2", State: "running"}},
	}, nil)
	v := NewEC2List(svc, nil)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})

	_, first := v.Update(keyPress('r'))
	_, second := v.Update(keyPress('r'))
	p1, ok1 := firstOfType[ec2PageLoadedMsg](collectMsgs(first))
	p2, ok2 := firstOfType[ec2PageLoadedMsg](collectMsgs(second))
	require.True(t, ok1 && ok2)

	deliverAll(v, p1, p2) // the older load's page arrives first
	assert.Len(t, v.instances, 2)
}

func TestSQSQueues_DoubleRefreshNoDuplicates(t *testing.T) {
	svc := new(awstest.MockSQSService)
	svc.On("ListQueuesPage", mock.Anything, (*string)(nil)).
		Return(&aws.QueuePage{QueueURLs: []string{"u1", "u2"}}, nil)
	svc.On("FetchAllQueueAttributes", mock.Anything, []string{"u1", "u2"}).
		Return([]aws.Queue{{URL: "u1", Name: "q1"}, {URL: "u2", Name: "q2"}}, nil)
	v := NewSQSQueues(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})

	_, first := v.Update(keyPress('r'))
	_, second := v.Update(keyPress('r'))
	p1, ok1 := firstOfType[sqsQueuesPageMsg](collectMsgs(first))
	p2, ok2 := firstOfType[sqsQueuesPageMsg](collectMsgs(second))
	require.True(t, ok1 && ok2)

	assert.Empty(t, deliverAll(v, p1), "a superseded page starts no attribute fetch")
	attrs, ok := firstOfType[sqsAllAttrsLoadedMsg](deliverAll(v, p2))
	require.True(t, ok)
	deliverAll(v, attrs)

	assert.Equal(t, []string{"u1", "u2"}, v.queueURLs)
	assert.Len(t, v.queues, 2)
	svc.AssertNumberOfCalls(t, "FetchAllQueueAttributes", 1)
}

func TestSQSQueues_StaleAttributesDropped(t *testing.T) {
	v := NewSQSQueues(new(awstest.MockSQSService))
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	v.loadGen = 2
	v.Update(sqsAllAttrsLoadedMsg{gen: 1, queues: []aws.Queue{{URL: "old"}}})
	assert.Empty(t, v.queues, "attributes from an older load must not replace the list")
}

func TestS3Objects_DoubleRefreshNoDuplicates(t *testing.T) {
	svc := new(awstest.MockS3Service)
	svc.On("ListObjectsPage", mock.Anything, "b", "", (*string)(nil)).Return(&aws.ObjectPage{
		Objects: []aws.S3Object{{Key: "a.txt"}, {Key: "b.txt"}},
	}, nil)
	v := NewS3Objects(svc, "b", "")
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})

	_, first := v.Update(keyPress('r'))
	_, second := v.Update(keyPress('r'))
	p1, ok1 := firstOfType[s3PageLoadedMsg](collectMsgs(first))
	p2, ok2 := firstOfType[s3PageLoadedMsg](collectMsgs(second))
	require.True(t, ok1 && ok2)

	deliverAll(v, p1, p2)
	assert.Len(t, v.objects, 2)
}

// C2: the folder expansion and the delete run in one operation, with the
// bucket fixed when it starts, so keys cannot reach another bucket.
func TestS3Objects_DeleteStaysInItsBucket(t *testing.T) {
	svc := new(awstest.MockS3Service)
	svc.On("ListAllKeys", mock.Anything, "bucket-b", "tmp/").Return([]string{"tmp/1"}, nil)
	svc.On("DeleteObjects", mock.Anything, "bucket-b", []string{"tmp/1", "x"}).Return(nil)
	v := NewS3Objects(svc, "bucket-b", "")

	start, ok := v.deleteEntries([]s3TableEntry{{isFolder: true, fullPath: "tmp/"}, {fullPath: "x"}})().(msg.StartOpMsg)
	require.True(t, ok, "the delete runs as a background operation")
	res, ok := start.Run(t.Context()).(s3DeleteCompleteMsg)
	require.True(t, ok)
	require.NoError(t, res.err)
	assert.Equal(t, "bucket-b", res.bucket)
	assert.Equal(t, 2, res.count)
	svc.AssertExpectations(t)
}
