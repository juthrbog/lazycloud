package views

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
)

// These tests cover load generations: a full reload supersedes the load in
// progress, so two overlapping loads never append the same rows twice.

// firstOfType returns the first message in msgs with the same type as want.
func firstOfType[T any](msgs []tea.Msg) (T, bool) {
	for _, m := range msgs {
		if v, ok := m.(T); ok {
			return v, true
		}
	}
	var zero T
	return zero, false
}

// deliverAll sends each message to the view and returns the produced messages.
func deliverAll(v tea.Model, msgs ...tea.Msg) []tea.Msg {
	var out []tea.Msg
	for _, m := range msgs {
		_, cmd := v.Update(m)
		out = append(out, collectMsgs(cmd)...)
	}
	return out
}

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

func TestEC2List_ReinitWhileLoadingDoesNotRestart(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListInstancesPage", mock.Anything, (*string)(nil)).Return(&aws.InstancePage{
		Instances: []aws.Instance{{ID: "i-1", State: "running"}},
	}, nil)
	v := NewEC2List(svc, nil)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})

	first := collectMsgs(v.Init())
	again := collectMsgs(v.Init()) // the navigator reopens the cached view
	_, restarted := firstOfType[ec2PageLoadedMsg](again)
	assert.False(t, restarted, "Init must not start a second load while one runs")

	p, ok := firstOfType[ec2PageLoadedMsg](first)
	require.True(t, ok)
	deliverAll(v, p)
	assert.Len(t, v.instances, 1)
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

// C2 defense in depth: delete keys resolved in another bucket are refused.
func TestS3Objects_RefusesDeleteKeysFromOtherBucket(t *testing.T) {
	svc := new(awstest.MockS3Service)
	v := NewS3Objects(svc, "bucket-b", "")
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})

	_, cmd := v.Update(s3DeleteResolvedMsg{bucket: "bucket-a", keys: []string{"tmp/1"}})
	collectMsgs(cmd)
	svc.AssertNotCalled(t, "DeleteObjects", mock.Anything, mock.Anything, mock.Anything)
}
