package views

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// Live tail of the event viewer. See docs/design/cwlogs.md, "Live tail".

const (
	// The poll replaces live tail when the endpoint has none (decision D5).
	logsPollEvery   = 2 * time.Second
	logsPollOverlap = 30 * time.Second // a poll reads from this long before the newest event
	logsPollKeep    = 2 * time.Minute  // the poll keeps the IDs of this span
	logsPollPages   = 2
)

// The notes that show once in each run of the app.
var (
	logsTailNoted atomic.Bool // the cost of live tail
	logsPollNoted atomic.Bool // the quota of the poll
)

// logsPollTick schedules the next poll, so tests can run it at once.
var logsPollTick = tea.Tick

const logsTailStopped = "Live tail stopped. F starts it again."

type tailMode int

const (
	tailOff tailMode = iota
	tailStarting
	tailLive
	tailPoll
)

// tailState is the live tail session of the viewer. Each session has its
// own context, a child of the view context. When it ends, the stream
// closes (see aws.LiveTail).
type tailState struct {
	mode    tailMode
	n       int // the session number; a result of an older session is dropped
	ctx     context.Context
	cancel  context.CancelFunc
	session aws.LiveTail
	at      time.Time // the start, for the timer
	sampled bool
	// The poll reads FilterLogEvents from pollFrom. It drops the events
	// that the view already shows (LogEvents.head).
	pollFrom time.Time
	newest   time.Time
}

type logsTailStartMsg struct {
	n    int
	tail aws.LiveTail
	err  error
}

type logsTailUpdateMsg struct {
	n   int
	u   *aws.TailUpdate
	err error
}

type logsPollTickMsg struct{ n int }

type logsPollMsg struct {
	n      int
	events []aws.LogEvent
	err    error
}

// Covered implements nav.Coverable. Another view covers the viewer, so live
// tail stops (decision D11).
func (v *LogEvents) Covered() {
	if v.tail.mode == tailOff {
		return
	}
	v.endTail()
	v.tailCovered = true
	eventlog.Infof(eventlog.CatAWS, "Live tail of %s stopped: another view covers it", v.groupName)
}

// Uncovered implements nav.Coverable.
func (v *LogEvents) Uncovered() tea.Cmd {
	if !v.tailCovered {
		return nil
	}
	v.tailCovered = false
	return toastCmd(msg.ToastInfo(logsTailStopped))
}

// toggleTail starts or stops live tail (F).
func (v *LogEvents) toggleTail() tea.Cmd {
	if v.tail.mode != tailOff {
		v.endTail()
		eventlog.Infof(eventlog.CatAWS, "Live tail of %s stopped with F", v.groupName)
		return toastCmd(msg.ToastInfo("Live tail stopped."))
	}
	switch {
	case v.notFound:
		return nil
	case v.lookingUp:
		return toastCmd(msg.ToastInfo("The log group is still loading. Press F again in a moment."))
	case v.group == nil || v.group.ARN == "":
		return toastCmd(msg.ToastError("Live tail needs the ARN of the log group, and the group record did not load."))
	case v.group.Class != "" && v.group.Class != string(cwltypes.LogGroupClassStandard):
		return toastCmd(msg.ToastError(fmt.Sprintf("Live tail needs a log group of the Standard class. This group is %s.", logClassText(v.group.Class))))
	case v.loading && v.replacing:
		// The result of the query replaces the lines, also the lines of
		// live tail.
		return toastCmd(msg.ToastInfo("The events are still loading. Press F again in a moment."))
	}
	ctx, cancel := context.WithCancel(v.viewContext())
	v.tail = tailState{mode: tailStarting, n: v.tail.n + 1, ctx: ctx, cancel: cancel}
	v.tailCovered = false
	v.lv.FollowEnd()
	svc, n, group := v.logs, v.tail.n, v.groupName
	// The stream closes when ctx ends (aws.LiveTail). The event log shows
	// it, for the manual checks of a stop.
	context.AfterFunc(ctx, func() { eventlog.Debugf(eventlog.CatAWS, "Live tail session %d of %s closed", n, group) })
	q := aws.TailQuery{GroupARN: v.group.ARN, Stream: v.stream, Pattern: v.want.pattern}
	return func() tea.Msg {
		if svc == nil {
			return logsTailStartMsg{n: n, err: errNoClient}
		}
		t, err := svc.StartLiveTail(ctx, q)
		return logsTailStartMsg{n: n, tail: t, err: err}
	}
}

// endTail stops the session. Its context ends, so its stream closes.
func (v *LogEvents) endTail() {
	if v.tail.cancel != nil {
		v.tail.cancel()
	}
	v.tail = tailState{n: v.tail.n}
}

// stopTailFor stops live tail for a new query, and says so.
func (v *LogEvents) stopTailFor() tea.Cmd {
	if v.tail.mode == tailOff {
		return nil
	}
	v.endTail()
	eventlog.Infof(eventlog.CatAWS, "Live tail of %s stopped for a new query", v.groupName)
	return toastCmd(msg.ToastInfo(logsTailStopped))
}

func (v *LogEvents) onTailStart(m logsTailStartMsg) tea.Cmd {
	if m.n != v.tail.n || v.tail.mode != tailStarting {
		return nil // an old session: its context ended, so its stream is closed
	}
	now := logsNow()
	if errors.Is(m.err, aws.ErrLiveTailUnavailable) {
		eventlog.Infof(eventlog.CatAWS, "No live tail at the endpoint, polling FilterLogEvents: %v", m.err)
		v.tail.mode, v.tail.at = tailPoll, now
		v.tail.pollFrom = now.Add(-logsPollOverlap)
		if v.head == nil {
			v.head = map[string]time.Time{}
		}
		cmds := []tea.Cmd{v.poll()}
		if !logsPollNoted.Swap(true) {
			cmds = append(cmds, toastCmd(msg.ToastInfo("The endpoint has no live tail, so the viewer polls FilterLogEvents every 2 seconds. The poll uses the FilterLogEvents quota of the account.")))
		}
		return tea.Batch(cmds...)
	}
	if m.err != nil {
		return v.failTail(m.err)
	}
	v.tail.mode, v.tail.at, v.tail.session = tailLive, now, m.tail
	eventlog.Infof(eventlog.CatAWS, "Live tail of %s started", v.groupName)
	cmds := []tea.Cmd{v.readTail()}
	if !logsTailNoted.Swap(true) {
		cmds = append(cmds, toastCmd(msg.ToastInfo("Live tail is billed for each minute. F stops it.")))
	}
	return tea.Batch(cmds...)
}

// readTail waits for the next update of the session.
func (v *LogEvents) readTail() tea.Cmd {
	t, ctx, n := v.tail.session, v.tail.ctx, v.tail.n
	return func() tea.Msg {
		u, err := t.Next(ctx)
		return logsTailUpdateMsg{n: n, u: u, err: err}
	}
}

func (v *LogEvents) onTailUpdate(m logsTailUpdateMsg) tea.Cmd {
	if m.n != v.tail.n || v.tail.mode != tailLive {
		return nil
	}
	if m.err != nil {
		return v.failTail(m.err)
	}
	v.tail.sampled = m.u.Sampled
	v.appendTail(m.u.Events)
	return v.readTail()
}

// failTail stops live tail after an error. The lines stay.
func (v *LogEvents) failTail(err error) tea.Cmd {
	v.endTail()
	eventlog.Errorf(eventlog.CatAWS, "Live tail of %s stopped: %v", v.groupName, err)
	return toastCmd(msg.ToastError(tailErrorText(err)))
}

// tailErrorText says why a session stopped.
func tailErrorText(err error) string {
	if _, ok := errors.AsType[*cwltypes.LimitExceededException](err); ok {
		return "The account already runs 15 live tail sessions."
	}
	if _, ok := errors.AsType[*cwltypes.SessionTimeoutException](err); ok {
		return "Live tail stopped after 3 hours, the limit of AWS. F starts it again."
	}
	if errors.Is(err, aws.ErrLiveTailEnded) {
		return "AWS ended the live tail session. F starts it again."
	}
	return "Live tail stopped: " + strings.TrimSuffix(aws.ErrorText(err), ".") + ". F starts it again."
}

// appendTail adds the events of live tail or the poll at the end.
func (v *LogEvents) appendTail(events []aws.LogEvent) {
	lines := make([]ui.LogLine, 0, len(events))
	for _, e := range events {
		src := e.Stream
		if v.stream != "" {
			src = "" // one stream: the title names it
		}
		lines = append(lines, ui.LogLine{Time: e.Time, Source: src, Message: e.Message, ID: e.ID, Data: e})
	}
	v.lv.Append(lines)
}

// poll reads the newest events with FilterLogEvents, at most
// logsPollPages pages.
func (v *LogEvents) poll() tea.Cmd {
	svc, ctx, n := v.logs, v.tail.ctx, v.tail.n
	group, stream := v.groupName, v.stream
	w := eventWalk{filter: true, pattern: v.want.pattern}
	q := aws.EventQuery{Group: group, Start: v.tail.pollFrom, Limit: logsLoadEvents}
	return func() tea.Msg {
		if svc == nil {
			return logsPollMsg{n: n, err: errNoClient}
		}
		var got []aws.LogEvent
		for range logsPollPages {
			p, err := readEvents(ctx, svc, q, stream, w)
			if err != nil {
				return logsPollMsg{n: n, events: got, err: err}
			}
			got = append(got, p.Events...)
			if p.End {
				break
			}
			q.Token = p.Next
		}
		return logsPollMsg{n: n, events: got}
	}
}

func (v *LogEvents) onPoll(m logsPollMsg) tea.Cmd {
	if m.n != v.tail.n || v.tail.mode != tailPoll {
		return nil
	}
	if m.err != nil {
		return v.failTail(m.err)
	}
	events := slices.Clone(m.events)
	slices.SortStableFunc(events, func(a, b aws.LogEvent) int {
		if c := a.Time.Compare(b.Time); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	fresh := events[:0]
	for _, e := range events {
		if v.shown(e) {
			continue
		}
		v.head[headKey(e.ID, e.Time, e.Message)] = e.Time
		fresh = append(fresh, e)
		if e.Time.After(v.tail.newest) {
			v.tail.newest = e.Time
		}
	}
	v.appendTail(fresh)
	// An event with a time ahead of the local clock does not move the
	// start into the future, where no new event would match.
	base := v.tail.newest
	if now := logsNow(); base.After(now) {
		base = now
	}
	if from := base.Add(-logsPollOverlap); from.After(v.tail.pollFrom) {
		v.tail.pollFrom = from
	}
	keep := base.Add(-logsPollKeep)
	maps.DeleteFunc(v.head, func(_ string, t time.Time) bool { return t.Before(keep) })
	n := v.tail.n
	return logsPollTick(logsPollEvery, func(time.Time) tea.Msg { return logsPollTickMsg{n: n} })
}

// headKey is the key of an event in LogEvents.head: its ID, or its time
// and message when it has no ID (GetLogEvents gives none).
func headKey(id string, t time.Time, message string) string {
	if id != "" {
		return id
	}
	return "\x00" + strconv.FormatInt(t.UnixMilli(), 10) + "\x00" + message
}

// shown reports whether the view already shows e, by its ID, or by its
// time and message for a line that has no ID.
func (v *LogEvents) shown(e aws.LogEvent) bool {
	if _, ok := v.head[headKey(e.ID, e.Time, e.Message)]; ok {
		return true
	}
	_, ok := v.head[headKey("", e.Time, e.Message)]
	return ok
}

func (v *LogEvents) onPollTick(m logsPollTickMsg) tea.Cmd {
	if m.n != v.tail.n || v.tail.mode != tailPoll {
		return nil
	}
	return v.poll()
}

// tailHeader is the start of the header while live tail runs:
// "LIVE 12:34 · sampled", "POLL 0:05", or "".
func (v *LogEvents) tailHeader() string {
	var s string
	switch v.tail.mode {
	case tailOff:
		return ""
	case tailStarting:
		return "LIVE starting"
	case tailLive:
		s = "LIVE "
	case tailPoll:
		s = "POLL "
	}
	s += tailClock(logsNow().Sub(v.tail.at))
	if v.tail.mode == tailLive && v.tail.sampled {
		s += " · sampled"
	}
	return s
}

// tailClock shows a duration as "12:34", or "1:02:03" from one hour on.
func tailClock(d time.Duration) string {
	d = max(d, 0).Truncate(time.Second)
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}
