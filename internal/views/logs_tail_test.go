package views

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
)

// Tests for live tail in the event viewer (docs/design/cwlogs.md, PR 6).

// fakeSession is a live tail session. Next returns the queued items in
// order, and waits on ctx when none is queued.
type fakeSession struct {
	ch chan tailItem
}

type tailItem struct {
	u   *aws.TailUpdate
	err error
}

func (s *fakeSession) Next(ctx context.Context) (*aws.TailUpdate, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case it := <-s.ch:
		return it.u, it.err
	}
}

// tailFake adds StartLiveTail to fakeEvents.
type tailFake struct {
	*fakeEvents

	mu       sync.Mutex
	queries  []aws.TailQuery
	ctxs     []context.Context
	startErr error
	session  *fakeSession
}

func newTailFake(events ...aws.LogEvent) *tailFake {
	return &tailFake{fakeEvents: &fakeEvents{events: events},
		session: &fakeSession{ch: make(chan tailItem, 16)}}
}

func (f *tailFake) StartLiveTail(ctx context.Context, q aws.TailQuery) (aws.LiveTail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
	f.ctxs = append(f.ctxs, ctx)
	if f.startErr != nil {
		return nil, f.startErr
	}
	return f.session, nil
}

func (f *tailFake) lastCtx() context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ctxs[len(f.ctxs)-1]
}

func (f *tailFake) push(u *aws.TailUpdate, err error) { f.session.ch <- tailItem{u: u, err: err} }

// resetTailNotes makes the notes that show once show again.
func resetTailNotes(t *testing.T) {
	t.Helper()
	logsTailNoted.Store(false)
	logsPollNoted.Store(false)
	t.Cleanup(func() { logsTailNoted.Store(false); logsPollNoted.Store(false) })
}

// newTailView builds the viewer over f with its first load done.
func newTailView(t *testing.T, f *tailFake, p map[string]string) *LogEvents {
	t.Helper()
	resetTailNotes(t)
	useLogsClock(t, evNow)
	v := NewLogEvents(f, p)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	v.SetContext(ctx)
	v.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	pump(v, v.Init(), 20)
	return v
}

// step runs cmd once, gives its messages to v, and returns them with the
// commands that v returned. A read with nothing queued gives no message.
func step(v *LogEvents, cmd tea.Cmd) ([]tea.Msg, tea.Cmd) {
	msgs := collectMsgsTimed(cmd, 300*time.Millisecond)
	var next []tea.Cmd
	for _, m := range msgs {
		_, c := v.Update(m)
		next = append(next, c)
	}
	return msgs, tea.Batch(next...)
}

// startTail presses F and runs the start call. It returns the messages of
// the call and what the viewer returned for them: the first read or poll,
// and the toasts.
func startTail(t *testing.T, v *LogEvents) ([]tea.Msg, tea.Cmd) {
	t.Helper()
	_, cmd := v.Update(keyPress('F'))
	require.NotNil(t, cmd)
	return step(v, cmd)
}

func tailEv(ago time.Duration, text, stream string) aws.LogEvent {
	return aws.LogEvent{Time: evNow.Add(-ago), Ingested: evNow, Message: text, Stream: stream}
}

// F starts live tail for the group with the pattern of the viewer. The
// first start notes the cost once. Updates append at the end, after the
// history, and follow turns on.
func TestLogEvents_TailStartsAndAppends(t *testing.T) {
	f := newTailFake(evAt(10*time.Minute, "msg-old", "a", "1"))
	v := newTailView(t, f, groupParams())
	v.want.pattern = "ERROR"
	pressEv(v, keyPress('k'))

	_, cmd := v.Update(keyPress('F'))
	assert.Contains(t, plain(v.View().Content), "LIVE starting")
	msgs, read := step(v, cmd)
	require.Len(t, f.queries, 1)
	assert.Equal(t, aws.TailQuery{GroupARN: evGroup.ARN, Pattern: "ERROR"}, f.queries[0])
	require.Len(t, msgs, 1)

	f.push(&aws.TailUpdate{Events: []aws.LogEvent{tailEv(time.Second, "msg-new1", "b"), tailEv(0, "msg-new2", "b")}}, nil)
	_, read = step(v, read)
	f.push(&aws.TailUpdate{}, nil)
	step(v, read)

	assert.Equal(t, []string{"msg-old", "msg-new1", "msg-new2"}, messages(v))
	assert.True(t, v.lv.Following(), "follow turns on at the start")
	sel, _ := v.lv.Selected()
	assert.Equal(t, "msg-new2", sel.Message, "the cursor follows the end")
	assert.Contains(t, plain(v.View().Content), "LIVE 00:00 · last 1h")

	useLogsClock(t, evNow.Add(75*time.Second))
	assert.Contains(t, plain(v.View().Content), "LIVE 01:15")
	assert.Equal(t, "F", v.KeyMap()[0].Binding.Help().Key)
	assert.Equal(t, "stop live tail", v.KeyMap()[0].Binding.Help().Desc)
}

// The cost note shows before the first start in each run, not again.
func TestLogEvents_TailNotesTheCostOnce(t *testing.T) {
	f := newTailFake()
	v := newTailView(t, f, groupParams())
	_, next := startTail(t, v)
	assert.Equal(t, []msg.ToastMsg{msg.ToastInfo("Live tail is billed for each minute. F stops it.")},
		toasts(collectMsgsTimed(next, 300*time.Millisecond)))

	pressEv(v, keyPress('F')) // stop
	_, next = startTail(t, v)
	assert.Empty(t, toasts(collectMsgsTimed(next, 300*time.Millisecond)))
}

// A sampled update shows "sampled" in the header and a note in the footer.
func TestLogEvents_TailSampled(t *testing.T) {
	f := newTailFake()
	v := newTailView(t, f, groupParams())
	_, read := startTail(t, v)
	f.push(&aws.TailUpdate{Sampled: true}, nil)
	_, read = step(v, read)
	assert.Contains(t, plain(v.View().Content), "LIVE 00:00 · sampled")
	assert.Contains(t, v.Footer(), "AWS shows a sample above 500 events each second")

	f.push(&aws.TailUpdate{}, nil)
	step(v, read)
	assert.NotContains(t, plain(v.View().Content), "sampled")
	assert.NotContains(t, v.Footer(), "sample")
}

// F again stops live tail: the session context ends, so the stream
// closes. A late update of that session is dropped.
func TestLogEvents_TailStopWithF(t *testing.T) {
	f := newTailFake()
	v := newTailView(t, f, groupParams())
	startTail(t, v)
	ctx := f.lastCtx()
	old := v.tail.n

	msgs := pressEv(v, keyPress('F'))
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	assert.Equal(t, []msg.ToastMsg{msg.ToastInfo("Live tail stopped.")}, toasts(msgs))
	assert.NotContains(t, plain(v.View().Content), "LIVE")
	assert.Equal(t, "live tail", v.KeyMap()[0].Binding.Help().Desc)

	_, cmd := v.Update(logsTailUpdateMsg{n: old, u: &aws.TailUpdate{Events: []aws.LogEvent{tailEv(0, "msg-late", "a")}}})
	assert.Nil(t, cmd)
	assert.Empty(t, messages(v))
}

// A start reply that comes after F stopped the session is dropped. Its
// context has ended, so its stream closes (aws.LiveTail).
func TestLogEvents_TailLateStartIsDropped(t *testing.T) {
	f := newTailFake()
	v := newTailView(t, f, groupParams())
	_, start := v.Update(keyPress('F'))
	pressEv(v, keyPress('F'))
	msgs, next := step(v, start)
	require.Len(t, msgs, 1)
	assert.Nil(t, next)
	assert.Equal(t, tailOff, v.tail.mode)
	require.ErrorIs(t, f.lastCtx().Err(), context.Canceled)
}

// The session context is a child of the view context: when the view
// leaves the stack, or the session changes, the stream closes.
func TestLogEvents_TailEndsWithTheView(t *testing.T) {
	f := newTailFake()
	resetTailNotes(t)
	useLogsClock(t, evNow)
	v := NewLogEvents(f, groupParams())
	viewCtx, cancel := context.WithCancel(context.Background())
	v.SetContext(viewCtx)
	v.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	pump(v, v.Init(), 20)
	startTail(t, v)
	ctx := f.lastCtx()
	_, hasDeadline := ctx.Deadline()
	assert.False(t, hasDeadline, "the session has no timeout")
	require.NoError(t, ctx.Err())
	cancel()
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

// A stream error stops live tail and keeps the lines. The status bar says
// why, and F starts a new session.
func TestLogEvents_TailErrors(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{&cwltypes.SessionTimeoutException{Message: awssdk.String("timeout")}, "Live tail stopped after 3 hours, the limit of AWS. F starts it again."},
		{aws.ErrLiveTailEnded, "AWS ended the live tail session. F starts it again."},
		{errors.New("boom"), "Live tail stopped: boom. F starts it again."},
	}
	for _, c := range cases {
		f := newTailFake(evAt(time.Minute, "msg-kept", "a", "1"))
		v := newTailView(t, f, groupParams())
		_, read := startTail(t, v)
		f.push(nil, c.err)
		_, next := step(v, read)
		assert.Equal(t, []tea.Msg{msg.ToastError(c.want)}, collectMsgs(next), "a toast, and no read after the error")
		assert.Equal(t, tailOff, v.tail.mode)
		assert.Equal(t, []string{"msg-kept"}, messages(v))
		require.ErrorIs(t, f.lastCtx().Err(), context.Canceled)
	}
}

// The account limit of 15 sessions has its own text.
func TestLogEvents_TailLimit(t *testing.T) {
	f := newTailFake()
	f.startErr = &cwltypes.LimitExceededException{Message: awssdk.String("limit")}
	v := newTailView(t, f, groupParams())
	_, next := startTail(t, v)
	assert.Equal(t, []tea.Msg{msg.ToastError("The account already runs 15 live tail sessions.")}, collectMsgs(next))
	assert.Equal(t, tailOff, v.tail.mode)
}

// A stream tails with its name, and its lines have no source column.
func TestLogEvents_TailOfAStream(t *testing.T) {
	f := newTailFake()
	v := newTailView(t, f, streamParams())
	_, read := startTail(t, v)
	assert.Equal(t, aws.TailQuery{GroupARN: evGroup.ARN, Stream: "s1"}, f.queries[0])
	f.push(&aws.TailUpdate{Events: []aws.LogEvent{tailEv(0, "msg-s", "s1")}}, nil)
	step(v, read)
	sel, ok := v.lv.Selected()
	require.True(t, ok)
	assert.Empty(t, sel.Source)
}

// F does not start a session on a group of another class, or before the
// group record is known.
func TestLogEvents_TailNeedsAStandardGroup(t *testing.T) {
	f := newTailFake()
	g := evGroup
	g.Class = "INFREQUENT_ACCESS"
	v := newTailView(t, f, logGroupParams(g))
	msgs := pressEv(v, keyPress('F'))
	assert.Empty(t, f.queries)
	require.Len(t, toasts(msgs), 1)
	assert.Contains(t, toasts(msgs)[0].Text, "Standard class")

	// The lookup is still running.
	f2 := newTailFake()
	v2 := NewLogEvents(f2, map[string]string{"log_group": "/app"})
	v2.Init() // the lookup command does not run
	msgs = pressEv(v2, keyPress('F'))
	assert.Empty(t, f2.queries)
	assert.Equal(t, []msg.ToastMsg{msg.ToastInfo("The log group is still loading. Press F again in a moment.")}, toasts(msgs))

	// The lookup failed.
	f3 := newTailFake()
	f3.groupErr = errors.New("denied")
	v3 := newTailView(t, f3, map[string]string{"log_group": "/app"})
	msgs = pressEv(v3, keyPress('F'))
	assert.Empty(t, f3.queries)
	require.Len(t, toasts(msgs), 1)
	assert.Contains(t, toasts(msgs)[0].Text, "group record did not load")
}

// Another view on top stops live tail (decision D11). The status bar says
// so when the user comes back, once.
func TestLogEvents_TailStopsWhenCovered(t *testing.T) {
	f := newTailFake()
	v := newTailView(t, f, groupParams())
	startTail(t, v)
	v.Covered()
	require.ErrorIs(t, f.lastCtx().Err(), context.Canceled)
	assert.Equal(t, tailOff, v.tail.mode)
	assert.Equal(t, []tea.Msg{msg.ToastInfo(logsTailStopped)}, collectMsgs(v.Uncovered()))
	assert.Nil(t, v.Uncovered())

	// Without live tail, a cover says nothing.
	v.Covered()
	assert.Nil(t, v.Uncovered())
}

// A new query replaces the lines, so it stops live tail and says so.
func TestLogEvents_TailStopsForANewQuery(t *testing.T) {
	f := newTailFake()
	v := newTailView(t, f, groupParams())
	startTail(t, v)
	msgs := pressEv(v, keyPress('r'))
	require.ErrorIs(t, f.lastCtx().Err(), context.Canceled)
	assert.Contains(t, toasts(msgs), msg.ToastInfo(logsTailStopped))
	assert.Equal(t, tailOff, v.tail.mode)
}

// pollNow makes the poll schedule run at once, as a message.
func pollNow(t *testing.T) {
	t.Helper()
	old := logsPollTick
	logsPollTick = func(_ time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
		return func() tea.Msg { return fn(evNow) }
	}
	t.Cleanup(func() { logsPollTick = old })
}

// With no live tail at the endpoint, the viewer polls FilterLogEvents.
// The header shows POLL, and the status bar notes the quota once. The
// first poll reads from 30 seconds before the start, and drops the events
// that the first load showed.
func TestLogEvents_PollWhenNoLiveTail(t *testing.T) {
	pollNow(t)
	f := newTailFake(evAt(10*time.Second, "msg-head", "a", "h1"))
	f.startErr = aws.ErrLiveTailUnavailable
	v := newTailView(t, f, groupParams())
	v.want.pattern = ""
	f.calls = nil
	// An event 10 seconds ahead of the local clock.
	f.events = append(f.events, evAt(-10*time.Second, "msg-p1", "a", "p1"))

	_, next := startTail(t, v)
	msgs, next := step(v, next)
	assert.Contains(t, toasts(msgs), msg.ToastInfo("The endpoint has no live tail, so the viewer polls FilterLogEvents every 2 seconds. The poll uses the FilterLogEvents quota of the account."))
	assert.Contains(t, plain(v.View().Content), "POLL 00:00 · last 1h")
	require.NotEmpty(t, f.calls)
	q := f.calls[0].q
	assert.True(t, q.Start.Equal(evNow.Add(-30*time.Second)), "start %v", q.Start)
	assert.True(t, q.End.IsZero())
	assert.Equal(t, []string{"msg-head", "msg-p1"}, messages(v), "the head event shows once")

	// msg-p1 is ahead of the clock, so the start stays at the clock.
	useLogsClock(t, evNow.Add(20*time.Second))
	f.calls = nil
	f.events = append(f.events, evAt(time.Second, "msg-p2", "a", "p2")) // late
	_, next = step(v, next)                                             // the tick
	_, next = step(v, next)                                             // the poll
	require.NotEmpty(t, f.calls)
	assert.True(t, f.calls[0].q.Start.Equal(evNow.Add(-30*time.Second)), "start %v", f.calls[0].q.Start)
	assert.Equal(t, []string{"msg-head", "msg-p1", "msg-p2"}, messages(v), "a late event shows, the others once")

	// The clock passed msg-p1: the next poll reads from 30 seconds before
	// it, and the start does not move back after that.
	for range 2 {
		f.calls = nil
		_, next = step(v, next)
		_, next = step(v, next)
		require.NotEmpty(t, f.calls)
		assert.True(t, f.calls[0].q.Start.Equal(evNow.Add(-20*time.Second)), "start %v", f.calls[0].q.Start)
	}
	assert.Equal(t, []string{"msg-head", "msg-p1", "msg-p2"}, messages(v))

	// The note shows once in a run. A new poll session drops the events
	// that the last one showed.
	pressEv(v, keyPress('F'))
	_, next = startTail(t, v)
	msgs, _ = step(v, next)
	assert.Empty(t, toasts(msgs))
	assert.Equal(t, []string{"msg-head", "msg-p1", "msg-p2"}, messages(v), "no event shows twice after a restart")
}

// A stream with no pattern loads with GetLogEvents, which gives no event
// IDs. The poll drops an event with the time and message of a loaded line.
func TestLogEvents_PollOfAStreamDropsLoadedLines(t *testing.T) {
	pollNow(t)
	f := newTailFake(evAt(10*time.Second, "msg-s1", "s1", "id1"))
	f.startErr = aws.ErrLiveTailUnavailable
	f.get = func(q aws.EventQuery) (*aws.EventPage, error) {
		p, err := f.page(q, false)
		for i := range p.Events {
			p.Events[i].ID, p.Events[i].Stream = "", "" // GetLogEvents gives neither
		}
		return p, err
	}
	v := newTailView(t, f, streamParams())
	require.Equal(t, "get", f.calls[0].kind)
	require.Equal(t, []string{"msg-s1"}, messages(v))
	_, next := startTail(t, v)
	step(v, next)
	assert.Equal(t, []string{"msg-s1"}, messages(v))
}

// An event with a time ahead of the local clock does not move the start
// of the poll into the future.
func TestLogEvents_PollIgnoresAFutureTime(t *testing.T) {
	pollNow(t)
	f := newTailFake()
	f.startErr = aws.ErrLiveTailUnavailable
	v := newTailView(t, f, groupParams())
	f.events = []aws.LogEvent{evAt(-time.Hour, "msg-future", "a", "fut")}
	f.calls = nil
	_, next := startTail(t, v)
	_, next = step(v, next) // the poll
	f.calls = nil
	_, next = step(v, next) // the tick
	step(v, next)
	require.NotEmpty(t, f.calls)
	assert.True(t, f.calls[0].q.Start.Equal(evNow.Add(-30*time.Second)), "start %v", f.calls[0].q.Start)
	assert.Contains(t, v.head, "fut", "the ID stays")
}

// A poll with no events keeps its start.
func TestLogEvents_PollStartStaysWithNoEvents(t *testing.T) {
	pollNow(t)
	f := newTailFake()
	f.startErr = aws.ErrLiveTailUnavailable
	v := newTailView(t, f, groupParams())
	f.calls = nil
	_, next := startTail(t, v)
	_, next = step(v, next)
	_, next = step(v, next)
	step(v, next)
	require.Len(t, f.calls, 2)
	assert.True(t, f.calls[1].q.Start.Equal(evNow.Add(-30*time.Second)), "start %v", f.calls[1].q.Start)
}

// F waits while the load of a new query runs, because its result replaces
// the lines. A load of older events (l) does not block it.
func TestLogEvents_TailWaitsForANewQuery(t *testing.T) {
	f := newTailFake()
	v := newTailView(t, f, groupParams())
	_, pending := v.Update(keyPress('r')) // the load does not run yet
	require.NotNil(t, pending)
	msgs := pressEv(v, keyPress('F'))
	assert.Empty(t, f.queries)
	assert.Equal(t, []msg.ToastMsg{msg.ToastInfo("The events are still loading. Press F again in a moment.")}, toasts(msgs))
	pump(v, pending, 20)
	_, cmd := v.Update(keyPress('F'))
	assert.NotNil(t, cmd)
	assert.Equal(t, tailStarting, v.tail.mode)
}

// After a failed lookup, F says that the record did not load, also while
// events load.
func TestLogEvents_TailAfterAFailedLookup(t *testing.T) {
	f := newTailFake()
	f.groupErr = errors.New("denied")
	resetTailNotes(t)
	useLogsClock(t, evNow)
	v := NewLogEvents(f, map[string]string{"log_group": "/app"})
	v.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	lookup := v.Init()
	msgs := collectMsgs(lookup)
	require.Len(t, msgs, 1)
	v.Update(msgs[0]) // the events load starts; its command does not run
	require.True(t, v.loading)
	got := toasts(pressEv(v, keyPress('F')))
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Text, "group record did not load")
}

// A start reply or an update of an older session is dropped, also while a
// newer session starts or runs.
func TestLogEvents_TailDropsAnOlderSession(t *testing.T) {
	f := newTailFake()
	v := newTailView(t, f, groupParams())
	_, start1 := v.Update(keyPress('F'))
	v.Update(keyPress('F'))
	_, start2 := v.Update(keyPress('F'))
	m1 := collectMsgs(start1)
	require.Len(t, m1, 1)
	_, cmd := v.Update(m1[0])
	assert.Nil(t, cmd)
	assert.Equal(t, tailStarting, v.tail.mode)

	m2 := collectMsgs(start2)
	require.Len(t, m2, 1)
	ctx2 := f.lastCtx()
	require.NoError(t, ctx2.Err(), "the old reply did not stop the new session")
	v.Update(m2[0])
	require.Equal(t, tailLive, v.tail.mode)
	_, cmd = v.Update(logsTailUpdateMsg{n: v.tail.n - 1, err: aws.ErrLiveTailEnded})
	assert.Nil(t, cmd)
	assert.Equal(t, tailLive, v.tail.mode)
	require.NoError(t, ctx2.Err())
	_, cmd = v.Update(logsPollMsg{n: v.tail.n - 1, err: errors.New("old")})
	assert.Nil(t, cmd)
	assert.Equal(t, tailLive, v.tail.mode)
}

// A poll reads at most 2 pages, with the stream and the pattern.
func TestLogEvents_PollReadsTwoPages(t *testing.T) {
	pollNow(t)
	f := newTailFake()
	f.startErr = aws.ErrLiveTailUnavailable
	v := newTailView(t, f, streamParams())
	v.want.pattern = "ERROR"
	f.calls = nil
	n := 0
	f.filter = func(q aws.EventQuery) (*aws.EventPage, error) {
		n++
		tok := strconv.Itoa(n)
		return &aws.EventPage{Events: []aws.LogEvent{evAt(time.Duration(n)*time.Second, "msg-"+tok, "s1", tok)}, Next: &tok}, nil
	}
	_, next := startTail(t, v)
	step(v, next)
	require.Len(t, f.calls, 2)
	assert.Equal(t, []string{"s1"}, f.calls[0].q.Streams)
	assert.Equal(t, "ERROR", f.calls[0].q.Pattern)
	assert.Equal(t, "1", *f.calls[1].q.Token)
	assert.Equal(t, []string{"msg-2", "msg-1"}, messages(v), "in time order")
}

// The poll keeps the IDs of the last 2 minutes only.
func TestLogEvents_PollForgetsOldIDs(t *testing.T) {
	pollNow(t)
	f := newTailFake()
	f.startErr = aws.ErrLiveTailUnavailable
	v := newTailView(t, f, groupParams())
	f.filter = func(aws.EventQuery) (*aws.EventPage, error) {
		return &aws.EventPage{End: true, Events: []aws.LogEvent{
			evAt(3*time.Minute, "msg-a", "a", "old"), evAt(0, "msg-b", "a", "new")}}, nil
	}
	_, next := startTail(t, v)
	step(v, next)
	assert.NotContains(t, v.head, "old")
	assert.Contains(t, v.head, "new")
}

// A poll error stops the poll, and the lines stay. A tick of an old
// session is dropped.
func TestLogEvents_PollError(t *testing.T) {
	pollNow(t)
	f := newTailFake(evAt(time.Hour/2, "msg-kept", "a", "1"))
	f.startErr = aws.ErrLiveTailUnavailable
	v := newTailView(t, f, groupParams())
	f.filter = func(aws.EventQuery) (*aws.EventPage, error) { return nil, errors.New("throttled") }
	_, next := startTail(t, v)
	_, next = step(v, next)
	assert.Equal(t, []tea.Msg{msg.ToastError("Live tail stopped: throttled. F starts it again.")}, collectMsgs(next))
	assert.Equal(t, tailOff, v.tail.mode)
	assert.Equal(t, []string{"msg-kept"}, messages(v))

	_, cmd := v.Update(logsPollTickMsg{n: v.tail.n})
	assert.Nil(t, cmd, "no poll while stopped")
}

func TestTailClock(t *testing.T) {
	assert.Equal(t, "00:00", tailClock(-time.Second))
	assert.Equal(t, "01:15", tailClock(75*time.Second+500*time.Millisecond))
	assert.Equal(t, "59:59", tailClock(time.Hour-time.Second))
	assert.Equal(t, "1:02:03", tailClock(time.Hour+2*time.Minute+3*time.Second))
}
