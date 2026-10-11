package views

import (
	"errors"
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

const testQueueURL = "https://sqs.us-east-1.amazonaws.com/123/orders"

var testRedrive = &aws.RedrivePolicy{DeadLetterTargetArn: "arn:aws:sqs:us-east-1:123:orders-dlq", MaxReceiveCount: 3}

func newTestSQSMessages() (*SQSMessages, *awstest.MockSQSService) {
	m := new(awstest.MockSQSService)
	view := NewSQSMessages(m, testQueueURL, "orders", false)
	view.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	return view, m
}

// loadAttrs delivers the queue settings that Init fetches.
func loadAttrs(view *SQSMessages, redrive *aws.RedrivePolicy) {
	view.Update(sqsQueueAttrsMsg{queue: &aws.Queue{
		URL: testQueueURL, ARN: "arn:aws:sqs:us-east-1:123:orders",
		VisibilityTimeout: 30, RedrivePolicy: redrive,
	}})
}

// collectMsgs runs a command and every command inside a tea.BatchMsg.
func collectMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	res := cmd()
	batch, ok := res.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{res}
	}
	var out []tea.Msg
	for _, c := range batch {
		out = append(out, collectMsgs(c)...)
	}
	return out
}

func toasts(msgs []tea.Msg) []msg.ToastMsg {
	var out []msg.ToastMsg
	for _, m := range msgs {
		if t, ok := m.(msg.ToastMsg); ok {
			out = append(out, t)
		}
	}
	return out
}

func TestPeekBlockReason(t *testing.T) {
	loadErr := errors.New("AccessDenied")
	tests := []struct {
		name        string
		readOnly    bool
		attrsLoaded bool
		attrsErr    error
		redrive     *aws.RedrivePolicy
		blocked     bool
	}{
		{name: "ReadWrite always allows", readOnly: false, redrive: testRedrive},
		{name: "ReadWrite allows before settings load", readOnly: false},
		{name: "ReadOnly allows a queue without DLQ", readOnly: true, attrsLoaded: true},
		{name: "ReadOnly blocks a queue with DLQ", readOnly: true, attrsLoaded: true, redrive: testRedrive, blocked: true},
		{name: "ReadOnly blocks while settings load", readOnly: true, blocked: true},
		{name: "ReadOnly blocks when settings are unknown", readOnly: true, attrsLoaded: true, attrsErr: loadErr, blocked: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason := peekBlockReason(tt.readOnly, tt.attrsLoaded, tt.attrsErr, tt.redrive)
			assert.Equal(t, tt.blocked, reason != "", "reason: %q", reason)
		})
	}
}

// In ReadOnly mode, l and r must not call ReceiveMessage on a queue whose
// redrive policy could move peeked messages to the DLQ.
func TestSQSMessages_ReadOnlyBlocksPeekOnRedriveQueue(t *testing.T) {
	ui.ReadOnly = true
	defer func() { ui.ReadOnly = true }()

	for _, k := range []rune{'l', 'r'} {
		view, svc := newTestSQSMessages()
		loadAttrs(view, testRedrive)

		_, cmd := view.Update(keyPress(k))
		got := toasts(collectMsgs(cmd))
		require.Len(t, got, 1, "key %q", k)
		assert.Equal(t, 2, got[0].Level)
		assert.Contains(t, got[0].Text, "DLQ limit (3)")
		assert.False(t, view.loading)
		svc.AssertNotCalled(t, "ReceiveMessages", mock.Anything, mock.Anything, mock.Anything)
	}
}

func TestSQSMessages_ReadOnlyAllowsPeekWithoutRedrive(t *testing.T) {
	ui.ReadOnly = true
	defer func() { ui.ReadOnly = true }()

	view, svc := newTestSQSMessages()
	loadAttrs(view, nil)
	svc.On("ReceiveMessages", mock.Anything, testQueueURL, int32(10)).
		Return([]aws.SQSMessage{{MessageID: "m1", Body: "hi", ApproximateReceiveCount: 1}}, nil)

	_, cmd := view.Update(keyPress('l'))
	var loaded *sqsMessagesLoadedMsg
	for _, m := range collectMsgs(cmd) {
		if l, ok := m.(sqsMessagesLoadedMsg); ok {
			loaded = &l
		}
	}
	require.NotNil(t, loaded)
	svc.AssertExpectations(t)

	view.Update(*loaded)
	assert.Len(t, view.messages, 1)
}

func TestSQSMessages_ReadOnlyBlocksPeekUntilSettingsLoad(t *testing.T) {
	ui.ReadOnly = true
	defer func() { ui.ReadOnly = true }()

	view, svc := newTestSQSMessages()
	_, cmd := view.Update(keyPress('l'))
	got := toasts(collectMsgs(cmd))
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Text, "loading")

	// A failed settings load keeps the peek blocked, because it is not known
	// to be safe.
	view.Update(sqsQueueAttrsMsg{err: errors.New("AccessDenied")})
	_, cmd = view.Update(keyPress('l'))
	got = toasts(collectMsgs(cmd))
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Text, "unknown")
	svc.AssertNotCalled(t, "ReceiveMessages", mock.Anything, mock.Anything, mock.Anything)
}

func TestSQSMessages_ReadWriteAllowsPeekOnRedriveQueue(t *testing.T) {
	ui.ReadOnly = false
	defer func() { ui.ReadOnly = true }()

	view, svc := newTestSQSMessages()
	loadAttrs(view, testRedrive)
	svc.On("ReceiveMessages", mock.Anything, testQueueURL, int32(10)).Return([]aws.SQSMessage{}, nil)

	_, cmd := view.Update(keyPress('l'))
	collectMsgs(cmd)
	svc.AssertExpectations(t)
}

// After a peek in ReadWrite mode, warn about messages whose next receive
// moves them to the DLQ.
func TestSQSMessages_WarnsAtDLQLimit(t *testing.T) {
	ui.ReadOnly = false
	defer func() { ui.ReadOnly = true }()

	view, _ := newTestSQSMessages()
	loadAttrs(view, testRedrive)

	_, cmd := view.Update(sqsMessagesLoadedMsg{queueURL: testQueueURL, messages: []aws.SQSMessage{
		{MessageID: "m1", ApproximateReceiveCount: 1},
		{MessageID: "m2", ApproximateReceiveCount: 3},
		{MessageID: "m3", ApproximateReceiveCount: 4},
	}})
	got := toasts(collectMsgs(cmd))
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Text, "2 message(s) at the DLQ limit (3)")
}

func TestSQSMessages_NoWarningBelowDLQLimit(t *testing.T) {
	view, _ := newTestSQSMessages()
	loadAttrs(view, testRedrive)

	_, cmd := view.Update(sqsMessagesLoadedMsg{queueURL: testQueueURL, messages: []aws.SQSMessage{
		{MessageID: "m1", ApproximateReceiveCount: 2},
	}})
	assert.Empty(t, toasts(collectMsgs(cmd)))
}

// When visibility could not be reset, keep the messages and say how long
// they stay hidden.
func TestSQSMessages_ResetFailureKeepsMessagesAndWarns(t *testing.T) {
	ui.ReadOnly = false
	defer func() { ui.ReadOnly = true }()

	view, svc := newTestSQSMessages()
	loadAttrs(view, nil)
	resetErr := &aws.VisibilityResetError{Failed: 2, Err: errors.New("AccessDenied")}
	svc.On("ReceiveMessages", mock.Anything, testQueueURL, int32(10)).
		Return([]aws.SQSMessage{{MessageID: "m1"}, {MessageID: "m2"}}, error(resetErr))

	loaded, ok := view.fetchMessages()().(sqsMessagesLoadedMsg)
	require.True(t, ok, "a reset failure must not turn into an ErrorMsg")
	require.NotNil(t, loaded.resetErr)

	_, cmd := view.Update(loaded)
	assert.Len(t, view.messages, 2)
	assert.Nil(t, view.err)
	got := toasts(collectMsgs(cmd))
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Text, "2 message(s) stay hidden from consumers for up to 30s")
}

func TestSQSMessages_ReceiveErrorIsAnError(t *testing.T) {
	view, svc := newTestSQSMessages()
	svc.On("ReceiveMessages", mock.Anything, testQueueURL, int32(10)).
		Return(nil, errors.New("AccessDenied"))

	_, isErr := view.fetchMessages()().(msg.ErrorMsg)
	assert.True(t, isErr)
}

func TestSQSMessages_BannerExplainsPeek(t *testing.T) {
	ui.ReadOnly = true
	defer func() { ui.ReadOnly = true }()

	view, _ := newTestSQSMessages()
	assert.NotContains(t, view.bannerText(), "VisibilityTimeout=0", "the old banner was false")
	assert.Contains(t, view.bannerText(), "receive count")

	loadAttrs(view, testRedrive)
	assert.Contains(t, view.bannerText(), "blocked in ReadOnly")

	ui.ReadOnly = false
	assert.Contains(t, view.bannerText(), "DLQ after 3")
}
