package views

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ops"
)

// runRedrive starts the redrive operation of a view in reg and waits until
// it reports progress. It returns the operation, a channel with its result,
// and the On method of the SQS mock.
func runRedrive(t *testing.T, reg *ops.Registry) (*ops.Op, chan sqsRedriveDoneMsg, func(string, ...any) *mock.Call) {
	t.Helper()
	setVar(t, &redrivePollInterval, time.Millisecond)
	view, m := newTestSQSMessages()
	loadAttrs(view, nil)
	m.On("StartMessageMoveTask", mock.Anything, "arn:aws:sqs:us-east-1:123:orders", "").Return("h1", nil)
	m.On("ListMessageMoveTasks", mock.Anything, "arn:aws:sqs:us-east-1:123:orders").Return([]aws.MessageMoveTask{
		{Status: "RUNNING", ApproximateNumberOfMsgsMoved: 3, ApproximateNumberOfMessages: 10},
	}, nil)

	start, ok := view.startRedrive()().(msg.StartOpMsg)
	require.True(t, ok)
	op := reg.Begin(start.Label, "")
	out := make(chan sqsRedriveDoneMsg, 1)
	go func() {
		defer op.Done()
		out <- start.Run(op.Ctx).(sqsRedriveDoneMsg)
	}()
	require.Eventually(t, func() bool {
		r := reg.Running()
		return len(r) == 1 && r[0].Progress.Done == 3
	}, 2*time.Second, time.Millisecond, "the redrive reports the moved messages")
	assert.Equal(t, int64(10), reg.Running()[0].Progress.Total)
	return op, out, m.On
}

func setVar[T any](t *testing.T, v *T, val T) {
	t.Helper()
	old := *v
	*v = val
	t.Cleanup(func() { *v = old })
}

// ctrl+x on a redrive stops the move task in SQS.
func TestRedriveCancelStopsTheTask(t *testing.T) {
	reg := ops.NewRegistry()
	var cancelled string
	op, out, on := runRedrive(t, reg)
	on("CancelMessageMoveTask", mock.Anything, "h1").Run(func(a mock.Arguments) {
		cancelled = a.String(1)
	}).Return(nil)

	require.True(t, reg.Cancel(op.ID))
	done := <-out
	assert.True(t, done.canceled)
	require.NoError(t, done.err)
	assert.Equal(t, "h1", cancelled)
	assert.Equal(t, "Redrive of orders stopped. The moved messages stay in the source queue.", done.Outcome().Text)
}

// Quitting stops only the watch: the task goes on in SQS.
func TestQuitDoesNotStopTheRedriveTask(t *testing.T) {
	reg := ops.NewRegistry()
	_, out, _ := runRedrive(t, reg)

	reg.CancelAll()
	done := <-out
	assert.False(t, done.canceled)
	require.Error(t, done.err)
	// CancelMessageMoveTask has no expectation, so a call would panic.
}

// A cancel while SQS starts the task must still stop the task: the start
// runs to its end, so the app learns the handle.
func TestRedriveCancelDuringStart(t *testing.T) {
	view, m := newTestSQSMessages()
	loadAttrs(view, nil)
	release := make(chan struct{})
	m.On("StartMessageMoveTask", mock.Anything, "arn:aws:sqs:us-east-1:123:orders", "").Run(func(mock.Arguments) {
		<-release
	}).Return("h1", nil)
	m.On("CancelMessageMoveTask", mock.Anything, "h1").Return(nil)

	start, ok := view.startRedrive()().(msg.StartOpMsg)
	require.True(t, ok)
	reg := ops.NewRegistry()
	op := reg.Begin(start.Label, "")
	out := make(chan sqsRedriveDoneMsg, 1)
	go func() {
		defer op.Done()
		out <- start.Run(op.Ctx).(sqsRedriveDoneMsg)
	}()
	reg.Cancel(op.ID)
	close(release)

	done := <-out
	require.NoError(t, done.startErr, "the start is not cut off")
	assert.True(t, done.canceled)
	m.AssertCalled(t, "CancelMessageMoveTask", mock.Anything, "h1")
}

// When the task ended just before the cancel, SQS refuses the cancel. The
// end of the task is then the result, not "could not stop".
func TestRedriveThatEndedWinsOverTheCancel(t *testing.T) {
	_, m := newTestSQSMessages()
	m.On("CancelMessageMoveTask", mock.Anything, "h1").Return(errors.New("ResourceNotFoundException"))
	m.On("ListMessageMoveTasks", mock.Anything, "arn").Return([]aws.MessageMoveTask{
		{Status: "COMPLETED", ApproximateNumberOfMsgsMoved: 7},
	}, nil)
	reg := ops.NewRegistry()
	op := reg.Begin("Redrive orders", "")
	defer op.Done()
	reg.Cancel(op.ID)

	done := stopRedrive(op.Ctx, m, "arn", "orders", "h1")
	assert.Equal(t, "COMPLETED", done.status)
	assert.Equal(t, int64(7), done.moved)
	assert.False(t, done.canceled)
}
