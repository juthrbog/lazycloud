package aws

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTail is an event stream of the SDK. Close closes the channel, as the
// stream of the SDK does.
type fakeTail struct {
	ch     chan cwltypes.StartLiveTailResponseStream
	mu     sync.Mutex
	closes int
	err    error
	once   sync.Once
	closed chan struct{}
}

func newFakeTail() *fakeTail {
	return &fakeTail{ch: make(chan cwltypes.StartLiveTailResponseStream, 8), closed: make(chan struct{})}
}

func (f *fakeTail) Events() <-chan cwltypes.StartLiveTailResponseStream { return f.ch }

func (f *fakeTail) Close() error {
	f.mu.Lock()
	f.closes++
	f.mu.Unlock()
	f.once.Do(func() { close(f.ch); close(f.closed) })
	return nil
}

func (f *fakeTail) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

func (f *fakeTail) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closes
}

// end ends the stream from the server side, with err.
func (f *fakeTail) end(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
	f.once.Do(func() { close(f.ch); close(f.closed) })
}

func tailUpdate(sampled bool, events ...cwltypes.LiveTailSessionLogEvent) cwltypes.StartLiveTailResponseStream {
	return &cwltypes.StartLiveTailResponseStreamMemberSessionUpdate{Value: cwltypes.LiveTailSessionUpdate{
		SessionMetadata: &cwltypes.LiveTailSessionMetadata{Sampled: sampled},
		SessionResults:  events,
	}}
}

func tailEvent(ms int64, stream, text string) cwltypes.LiveTailSessionLogEvent {
	return cwltypes.LiveTailSessionLogEvent{Timestamp: aws.Int64(ms), IngestionTime: aws.Int64(ms + 5),
		LogStreamName: aws.String(stream), Message: aws.String(text)}
}

// The session skips SessionStart, and returns each update in time order,
// with its events and the sampled flag. An update with no events comes too.
func TestLiveTailReadsUpdates(t *testing.T) {
	ctx := t.Context()
	f := newFakeTail()
	tail := newLiveTail(ctx, f)
	f.ch <- &cwltypes.StartLiveTailResponseStreamMemberSessionStart{}
	f.ch <- tailUpdate(false, tailEvent(2000, "b", "second"), tailEvent(1000, "a", "first"))
	f.ch <- tailUpdate(true)

	u, err := tail.Next(ctx)
	require.NoError(t, err)
	assert.False(t, u.Sampled)
	require.Len(t, u.Events, 2)
	assert.Equal(t, LogEvent{Time: time.UnixMilli(1000), Ingested: time.UnixMilli(1005), Message: "first", Stream: "a"}, u.Events[0])
	assert.Equal(t, "second", u.Events[1].Message)

	u, err = tail.Next(ctx)
	require.NoError(t, err)
	assert.True(t, u.Sampled)
	assert.Empty(t, u.Events)
	assert.Zero(t, f.closeCount())
}

// A stream error ends the session with that error, for example the
// timeout after 3 hours. A stream that closes with no error gives
// ErrLiveTailEnded.
func TestLiveTailStreamErrors(t *testing.T) {
	ctx := t.Context()
	f := newFakeTail()
	tail := newLiveTail(ctx, f)
	f.end(&cwltypes.SessionTimeoutException{Message: aws.String("3 hours")})
	_, err := tail.Next(ctx)
	_, ok := errors.AsType[*cwltypes.SessionTimeoutException](err)
	assert.True(t, ok, "%v", err)
	assert.Contains(t, err.Error(), "StartLiveTail")

	f2 := newFakeTail()
	tail = newLiveTail(ctx, f2)
	f2.end(nil)
	_, err = tail.Next(ctx)
	assert.ErrorIs(t, err, ErrLiveTailEnded)
}

// H6: when the context of the session ends, the stream closes once, and a
// waiting Next returns. Nothing has to read the stream for this.
func TestLiveTailContextClosesTheStreamOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := newFakeTail()
	tail := newLiveTail(ctx, f)
	done := make(chan error, 1)
	go func() {
		_, err := tail.Next(ctx)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("Next did not return")
	}
	<-f.closed
	cancel()
	time.Sleep(10 * time.Millisecond)
	assert.Equal(t, 1, f.closeCount())
}

// A start reply that comes after the context ended closes the stream at
// once.
func TestLiveTailLateStartClosesAtOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := newFakeTail()
	newLiveTail(ctx, f)
	select {
	case <-f.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not close")
	}
	assert.Equal(t, 1, f.closeCount())
}

// The call sends the group ARN, the stream and the pattern. With an
// endpoint, it goes to the endpoint host, and a 501 (LocalStack) gives
// ErrLiveTailUnavailable.
func TestStartLiveTailWithAnEndpoint(t *testing.T) {
	f, svc := newFakeLogs(t)
	_, err := svc.StartLiveTail(context.Background(), TailQuery{GroupARN: "arn:g", Stream: "s", Pattern: "ERROR"})
	require.ErrorIs(t, err, ErrLiveTailUnavailable)
	require.Len(t, f.calls, 1)
	assert.Equal(t, "StartLiveTail", f.calls[0].op)
	assert.Equal(t, map[string]any{"logGroupIdentifiers": []any{"arn:g"}, "logStreamNames": []any{"s"},
		"logEventFilterPattern": "ERROR"}, f.calls[0].in)

	_, err = svc.StartLiveTail(context.Background(), TailQuery{GroupARN: "arn:g"})
	require.ErrorIs(t, err, ErrLiveTailUnavailable)
	assert.Equal(t, map[string]any{"logGroupIdentifiers": []any{"arn:g"}}, f.calls[1].in)
}

// An error of AWS is not a missing live tail: the 15 session limit keeps
// its type, so the viewer can name it.
func TestStartLiveTailLimit(t *testing.T) {
	f, svc := newFakeLogs(t)
	f.replies["StartLiveTail"] = func(map[string]any) (int, string) {
		return http.StatusBadRequest, `{"__type":"LimitExceededException","message":"too many sessions"}`
	}
	_, err := svc.StartLiveTail(context.Background(), TailQuery{GroupARN: "arn:g"})
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrLiveTailUnavailable)
	_, ok := errors.AsType[*cwltypes.LimitExceededException](err)
	assert.True(t, ok, "%v", err)
}

func TestLiveTailMissing(t *testing.T) {
	assert.True(t, liveTailMissing(&net.OpError{Op: "dial", Err: errors.New("refused")}))
	assert.True(t, liveTailMissing(&net.DNSError{Name: "stream-localhost"}))
	assert.False(t, liveTailMissing(errors.New("other")))
	assert.False(t, liveTailMissing(context.Canceled))
	assert.False(t, liveTailMissing(&net.OpError{Op: "dial", Err: context.Canceled}), "a cancelled dial is not a missing endpoint")
}

// dialFails fails each request with a connect error after it records the
// host.
type dialFails struct{ recordingHTTP }

func (d *dialFails) Do(req *http.Request) (*http.Response, error) {
	_, _ = d.recordingHTTP.Do(req)
	return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
}

// Without an endpoint, the call keeps the host prefix, and a connect
// error is an error: the viewer polls only with an endpoint override.
func TestStartLiveTailWithoutAnEndpointKeepsThePrefix(t *testing.T) {
	c := testClient(t, "")
	rec := &dialFails{}
	c.Config.HTTPClient = rec
	_, err := NewLogsService(c).StartLiveTail(context.Background(), TailQuery{GroupARN: "arn:g"})
	require.Error(t, err)
	_, isNet := errors.AsType[*net.OpError](err)
	require.True(t, isNet, "%v", err)
	require.NotErrorIs(t, err, ErrLiveTailUnavailable)
	assert.Equal(t, []string{"stream-logs.us-east-1.amazonaws.com"}, rec.hosts)
}

// With an endpoint, a connect error means that live tail is not there.
func TestStartLiveTailEndpointThatDoesNotAnswer(t *testing.T) {
	c := testClient(t, "http://localhost:4566")
	c.Config.HTTPClient = &dialFails{}
	_, err := NewLogsService(c).StartLiveTail(context.Background(), TailQuery{GroupARN: "arn:g"})
	require.ErrorIs(t, err, ErrLiveTailUnavailable)
}
