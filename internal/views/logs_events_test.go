package views

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// Tests for the log event viewer (docs/design/cwlogs.md, PR 5).

// evNow is the clock of these tests.
var evNow = time.Date(2026, 11, 20, 15, 0, 0, 0, time.Local)

func useLogsClock(t *testing.T, now time.Time) {
	t.Helper()
	old := logsNow
	logsNow = func() time.Time { return now }
	t.Cleanup(func() { logsNow = old })
}

// evCall is one event call of the fake.
type evCall struct {
	kind string // "filter" or "get"
	q    aws.EventQuery
}

// fakeEvents is an aws.LogsService for the viewer. By default, both calls
// read events: FilterEvents gives the events of the range (both ends
// inclusive), newest first, Limit at a time, with a token for the rest.
// GetStreamEvents does the same with an exclusive end, and at the start it
// returns the token it got, as AWS does.
type fakeEvents struct {
	aws.LogsService

	events   []aws.LogEvent
	group    *aws.LogGroup
	groupErr error
	lookups  int
	calls    []evCall
	// filter and get replace the default answers when set.
	filter func(q aws.EventQuery) (*aws.EventPage, error)
	get    func(q aws.EventQuery) (*aws.EventPage, error)
}

func (f *fakeEvents) GetLogGroup(_ context.Context, name string) (*aws.LogGroup, error) {
	f.lookups++
	if f.groupErr != nil {
		return nil, f.groupErr
	}
	if f.group == nil || f.group.Name != name {
		return nil, fmt.Errorf("%w: %s", aws.ErrLogGroupNotFound, name)
	}
	return f.group, nil
}

func (f *fakeEvents) FilterEvents(ctx context.Context, q aws.EventQuery) (*aws.EventPage, error) {
	f.calls = append(f.calls, evCall{"filter", q})
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("no time limit")
	}
	if f.filter != nil {
		return f.filter(q)
	}
	return f.page(q, true)
}

func (f *fakeEvents) GetStreamEvents(ctx context.Context, q aws.EventQuery) (*aws.EventPage, error) {
	f.calls = append(f.calls, evCall{"get", q})
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("no time limit")
	}
	if f.get != nil {
		return f.get(q)
	}
	return f.page(q, false)
}

// page answers from f.events. The token is the count of the newer events
// that earlier pages returned.
func (f *fakeEvents) page(q aws.EventQuery, inclusive bool) (*aws.EventPage, error) {
	var in []aws.LogEvent
	for _, e := range f.events {
		if !q.Start.IsZero() && e.Time.Before(q.Start) {
			continue
		}
		if !q.End.IsZero() && (e.Time.After(q.End) || (!inclusive && e.Time.Equal(q.End))) {
			continue
		}
		if q.Pattern != "" && !strings.Contains(e.Message, q.Pattern) {
			continue
		}
		if len(q.Streams) > 0 && !slices.Contains(q.Streams, e.Stream) {
			continue
		}
		in = append(in, e)
	}
	slices.SortStableFunc(in, func(a, b aws.LogEvent) int { return a.Time.Compare(b.Time) })
	off := 0
	if q.Token != nil {
		off, _ = strconv.Atoi(*q.Token)
	}
	hi := max(len(in)-off, 0)
	lo := max(hi-int(q.Limit), 0)
	if q.Limit == 0 {
		lo = 0
	}
	p := &aws.EventPage{Events: slices.Clone(in[lo:hi])}
	tok := strconv.Itoa(off + hi - lo)
	switch {
	case lo > 0:
		p.Next = &tok
	case inclusive:
		p.End = true
	default:
		// GetLogEvents: a token that repeats at the start.
		p.Next = &tok
		p.End = q.Token != nil && *q.Token == tok
	}
	return p, nil
}

// evAt makes an event at now minus ago.
func evAt(ago time.Duration, msgText, stream, id string) aws.LogEvent {
	return aws.LogEvent{Time: evNow.Add(-ago), Ingested: evNow.Add(-ago + time.Second), Message: msgText, Stream: stream, ID: id}
}

// newEvents builds the viewer with the group record and runs its first load.
func newEvents(t *testing.T, f *fakeEvents, p map[string]string) *LogEvents {
	t.Helper()
	useLogsClock(t, evNow)
	v := NewLogEvents(f, p)
	v.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	pump(v, v.Init(), 20)
	return v
}

var evGroup = aws.LogGroup{Name: "/app", ARN: "arn:aws:logs:r:1:log-group:/app", Class: "STANDARD"}

func groupParams() map[string]string { return logGroupParams(evGroup) }

func streamParams() map[string]string {
	return map[string]string{"log_group": "/app", "log_group_arn": evGroup.ARN, "log_stream": "s1"}
}

// press sends a key and runs what follows.
func pressEv(v *LogEvents, k tea.KeyPressMsg) []tea.Msg {
	_, cmd := v.Update(k)
	return pump(v, cmd, 20)
}

func messages(v *LogEvents) []string {
	var out []string
	for row := range strings.SplitSeq(plain(v.View().Content), "\n") {
		if i := strings.Index(row, "msg-"); i >= 0 {
			out = append(out, strings.Fields(row[i:])[0])
		}
	}
	return out
}

// A group loads the newest hour first. A relative range sends only the
// start time, so a slow local clock does not hide the newest events.
func TestLogEvents_GroupLoadsTheLastHour(t *testing.T) {
	f := &fakeEvents{events: []aws.LogEvent{
		evAt(90*time.Minute, "msg-old", "s1", "e0"),
		evAt(30*time.Minute, "msg-a", "s1", "e1"),
		evAt(10*time.Minute, "msg-b", "s2", "e2"),
	}}
	v := newEvents(t, f, groupParams())
	require.Len(t, f.calls, 1)
	q := f.calls[0].q
	assert.Equal(t, "filter", f.calls[0].kind)
	assert.Equal(t, "/app", q.Group)
	assert.True(t, q.Start.Equal(evNow.Add(-time.Hour)))
	assert.True(t, q.End.IsZero(), "a relative range sends no end time")
	assert.Equal(t, int32(logsLoadEvents), q.Limit)
	assert.Nil(t, q.Token)

	assert.Equal(t, []string{"msg-a", "msg-b"}, messages(v))
	assert.Equal(t, "2 events · 14:00 to 15:00 · l for older", v.Footer())
	head := plain(strings.SplitN(v.View().Content, "\n", 2)[0])
	assert.Equal(t, "last 1h · "+logZone(evNow), head)
	assert.Contains(t, plain(v.View().Content), "s2", "the source column names the stream")
}

func TestLogEvents_ZoneName(t *testing.T) {
	assert.Equal(t, "UTC", logZone(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	assert.Equal(t, "UTC-05:00", logZone(time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("x", -5*3600))))
	assert.Equal(t, "UTC+05:30", logZone(time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("x", 5*3600+1800))))
}

// l moves the range back by its length. The new window ends 1 ms before
// the old start, because both ends are inclusive, so the edge event shows
// once.
func TestLogEvents_WalkBackMovesTheRange(t *testing.T) {
	f := &fakeEvents{events: []aws.LogEvent{
		evAt(time.Hour, "msg-edge", "s1", "e-edge"), // exactly at the start of the first window
		evAt(90*time.Minute, "msg-older", "s1", "e-older"),
		evAt(10*time.Minute, "msg-new", "s1", "e-new"),
	}}
	v := newEvents(t, f, groupParams())
	assert.Equal(t, []string{"msg-edge", "msg-new"}, messages(v))

	pressEv(v, keyPress('l'))
	require.Len(t, f.calls, 2)
	q := f.calls[1].q
	assert.True(t, q.Start.Equal(evNow.Add(-2*time.Hour)), "start %v", q.Start)
	assert.True(t, q.End.Equal(evNow.Add(-time.Hour-time.Millisecond)), "end %v", q.End)
	assert.Nil(t, q.Token)
	assert.Equal(t, []string{"msg-older", "msg-edge", "msg-new"}, messages(v))
	assert.Equal(t, "3 events · 13:00 to 15:00 · l for older", v.Footer())
}

// An empty range says so, and l still walks back.
func TestLogEvents_EmptyRange(t *testing.T) {
	f := &fakeEvents{events: []aws.LogEvent{evAt(150*time.Minute, "msg-x", "s1", "e1")}}
	v := newEvents(t, f, groupParams())
	assert.Equal(t, "no events from 14:00 to 15:00 · l for older", v.Footer())
	pressEv(v, keyPress('l'))
	assert.Equal(t, "no events from 13:00 to 15:00 · l for older", v.Footer())
	pressEv(v, keyPress('l'))
	assert.Equal(t, []string{"msg-x"}, messages(v))
}

// The walk stops 14 days before the creation of the group: PutLogEvents
// takes events up to 14 days old, also right after the group was made.
func TestLogEvents_WalkStopsBeforeCreation(t *testing.T) {
	g := evGroup
	g.Created = evNow // made just now, with events from the last 14 days
	floor := evNow.Add(-logsBackfill)
	f := &fakeEvents{events: []aws.LogEvent{evAt(logsBackfill-10*time.Minute, "msg-a", "s1", "e1")}}
	v := newEvents(t, f, logGroupParams(g))
	_, cmd := v.Update(msg.PickerResultMsg{Value: "7d", Tag: &logsRangeTag{}})
	pump(v, cmd, 20)
	pressEv(v, keyPress('l'))
	last := f.calls[len(f.calls)-1].q
	assert.True(t, last.Start.Equal(floor), "the window starts 14 days before the creation: %v", last.Start)
	assert.Equal(t, []string{"msg-a"}, messages(v), "an event older than the group shows")
	assert.Contains(t, v.Footer(), "start of group")

	n := len(f.calls)
	pressEv(v, keyPress('l'))
	assert.Len(t, f.calls, n, "l sends nothing at the start")
}

// The walk stops at now minus the retention.
func TestLogEvents_WalkStopsAtRetention(t *testing.T) {
	g := evGroup
	g.RetentionDays = 1
	f := &fakeEvents{}
	v := newEvents(t, f, logGroupParams(g))
	pressEv(v, keyPress('t'))
	v.Update(msg.PickerResultMsg{Value: "1d", Tag: &logsRangeTag{}})
	pump(v, nil, 1)
	_, cmd := v.Update(msg.PickerResultMsg{Value: "3d", Tag: &logsRangeTag{}})
	pump(v, cmd, 20)
	last := f.calls[len(f.calls)-1].q
	assert.True(t, last.Start.Equal(evNow.Add(-24*time.Hour)), "3d starts at the retention: %v", last.Start)
	assert.Contains(t, v.Footer(), "start of group")
}

// AWS reads newest first only from 2024-01-01. A custom range that starts
// before moves there, and the status bar says so.
func TestLogEvents_CustomRangeBeforeTheEpoch(t *testing.T) {
	f := &fakeEvents{}
	v := newEvents(t, f, groupParams())
	_, cmd := v.Update(msg.PickerResultMsg{Value: logRangeCustom, Tag: &logsRangeTag{}})
	assert.Nil(t, cmd)
	require.True(t, v.CapturingInput(), "the start time input is open")
	typeInto(v, "2023-06-01 00:00")
	pressEv(v, tea.KeyPressMsg{Code: tea.KeyEnter})
	typeInto(v, "2024-01-02T00:00:00Z")
	seen := pressEv(v, tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.False(t, v.CapturingInput())

	ts := toasts(seen)
	require.Len(t, ts, 1)
	assert.Contains(t, ts[0].Text, "2024-01-01")
	q := f.calls[len(f.calls)-1].q
	assert.True(t, q.Start.Equal(logsEpoch), "start %v", q.Start)
	assert.True(t, q.End.Equal(time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)), "a custom range sends its end")
	assert.Contains(t, v.Footer(), "start of group")
	assert.Contains(t, plain(v.View().Content), "2023-06-01 00:00 to ", "the header shows the custom range")
}

// A custom range that ends before the group exists loads nothing.
func TestLogEvents_CustomRangeBeforeTheFloor(t *testing.T) {
	g := evGroup
	g.Created = evNow.Add(-time.Hour + logsBackfill)
	f := &fakeEvents{}
	v := newEvents(t, f, logGroupParams(g))
	n := len(f.calls)
	v.Update(msg.PickerResultMsg{Value: logRangeCustom, Tag: &logsRangeTag{}})
	typeInto(v, "2026-11-19 10:00")
	pressEv(v, tea.KeyPressMsg{Code: tea.KeyEnter})
	typeInto(v, "2026-11-19 11:00")
	pressEv(v, tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Len(t, f.calls, n, "no call")
	assert.Contains(t, v.Footer(), "no events from")
	assert.Contains(t, v.Footer(), "start of group")
}

func typeInto(v *LogEvents, s string) {
	for _, r := range s {
		v.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// A bad time keeps the input open, and an end before the start too.
func TestLogEvents_CustomTimeErrors(t *testing.T) {
	f := &fakeEvents{}
	v := newEvents(t, f, groupParams())
	v.Update(msg.PickerResultMsg{Value: logRangeCustom, Tag: &logsRangeTag{}})
	typeInto(v, "yesterday")
	ts := toasts(pressEv(v, tea.KeyPressMsg{Code: tea.KeyEnter}))
	require.Len(t, ts, 1)
	assert.Contains(t, ts[0].Text, `Unknown time "yesterday"`)
	assert.True(t, v.CapturingInput())

	v.input.SetValue("2026-11-20 14:00")
	pressEv(v, tea.KeyPressMsg{Code: tea.KeyEnter})
	typeInto(v, "2026-11-20 13:00")
	ts = toasts(pressEv(v, tea.KeyPressMsg{Code: tea.KeyEnter}))
	require.Len(t, ts, 1)
	assert.Contains(t, ts[0].Text, "after the start")
	assert.True(t, v.CapturingInput())

	// An empty end is now.
	v.input.SetValue("")
	n := len(f.calls)
	pressEv(v, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Len(t, f.calls, n+1)
	assert.True(t, f.calls[n].q.End.Equal(evNow))
	assert.True(t, f.calls[n].q.Start.Equal(time.Date(2026, 11, 20, 14, 0, 0, 0, time.Local)))

	// esc closes the input with no query.
	v.Update(msg.PickerResultMsg{Value: logRangeCustom, Tag: &logsRangeTag{}})
	pressEv(v, tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.False(t, v.CapturingInput())
	assert.Len(t, f.calls, n+1)
}

func TestLogEvents_ParseTime(t *testing.T) {
	now := time.Date(2026, 11, 20, 15, 0, 0, 0, time.FixedZone("EST", -5*3600))
	got, ok := parseLogTime("2026-11-20 14:00", now)
	require.True(t, ok)
	assert.Equal(t, "2026-11-20T14:00:00-05:00", got.Format(time.RFC3339), "read in local time")
	got, ok = parseLogTime("2026-11-20T14:00:00Z", now)
	require.True(t, ok)
	assert.Equal(t, "2026-11-20T14:00:00Z", got.Format(time.RFC3339), "Z is read as written")
	got, ok = parseLogTime("2026-11-20T14:00:00+02:00", now)
	require.True(t, ok)
	assert.Equal(t, "2026-11-20T12:00:00Z", got.UTC().Format(time.RFC3339))
	got, ok = parseLogTime("2026-11-20", now)
	require.True(t, ok)
	assert.Equal(t, "2026-11-20T00:00:00-05:00", got.Format(time.RFC3339))
	_, ok = parseLogTime("14:00", now)
	assert.False(t, ok)
}

// t offers the presets and custom. A preset starts a new query.
func TestLogEvents_RangePicker(t *testing.T) {
	f := &fakeEvents{}
	v := newEvents(t, f, groupParams())
	_, cmd := v.Update(keyPress('t'))
	req := requestOf[msg.RequestActionPickerMsg](t, cmd)
	assert.Equal(t, []string{"5m", "15m", "1h", "3h", "12h", "1d", "3d", "7d", "custom"}, req.Options)
	_, cmd = v.Update(pickLabel(t, req, "3h"))
	pump(v, cmd, 20)
	assert.True(t, f.calls[len(f.calls)-1].q.Start.Equal(evNow.Add(-3*time.Hour)))
	assert.Contains(t, plain(v.View().Content), "last 3h")

	// A cancel or a reply of another picker does nothing.
	n := len(f.calls)
	_, cmd = v.Update(cancelPick(req))
	assert.Nil(t, cmd)
	_, cmd = v.Update(msg.PickerResultMsg{Value: "1h"})
	assert.Nil(t, cmd)
	assert.Len(t, f.calls, n)
}

// A range over one day shows the date in the time column.
func TestLogEvents_LongRangeShowsDates(t *testing.T) {
	f := &fakeEvents{events: []aws.LogEvent{evAt(30*time.Hour, "msg-a", "s1", "e1")}}
	v := newEvents(t, f, groupParams())
	_, cmd := v.Update(msg.PickerResultMsg{Value: "3d", Tag: &logsRangeTag{}})
	pump(v, cmd, 20)
	assert.Contains(t, plain(v.View().Content), "2026-11-19 09:00:00.000")
	assert.Contains(t, v.Footer(), "2026-11-17 15:00 to 2026-11-20 15:00")
}

// One load stops after 1000 events. Each call asks for the space left.
func TestLogEvents_LoadStopsAt1000Events(t *testing.T) {
	var evs []aws.LogEvent
	for i := range 2500 {
		evs = append(evs, evAt(time.Duration(i)*time.Second, fmt.Sprintf("msg-%04d", i), "s1", fmt.Sprintf("e%04d", i)))
	}
	f := &fakeEvents{events: evs}
	f.filter = func(q aws.EventQuery) (*aws.EventPage, error) {
		q.Limit = min(q.Limit, 400) // AWS can return less than the limit
		return f.page(q, true)
	}
	v := newEvents(t, f, groupParams())
	var limits []int32
	for _, c := range f.calls {
		limits = append(limits, c.q.Limit)
	}
	assert.Equal(t, []int32{1000, 600, 200}, limits)
	assert.Equal(t, 1000, v.lv.Len())
	assert.Contains(t, v.Footer(), "l for older")

	pressEv(v, keyPress('l'))
	assert.Equal(t, 2000, v.lv.Len())
	assert.Equal(t, "1000", *f.calls[3].q.Token, "l goes on with the token")
}

// One load stops after 10 pages, also when the pages are empty.
func TestLogEvents_LoadStopsAt10Pages(t *testing.T) {
	f := &fakeEvents{}
	n := 0
	f.filter = func(aws.EventQuery) (*aws.EventPage, error) {
		n++
		tok := strconv.Itoa(n)
		return &aws.EventPage{Next: &tok}, nil // empty, but not the end
	}
	v := newEvents(t, f, groupParams())
	assert.Len(t, f.calls, logsLoadPages)
	assert.Equal(t, "no events from 14:00 to 15:00 · l for older", v.Footer())
	pressEv(v, keyPress('l'))
	assert.Len(t, f.calls, 2*logsLoadPages)
	assert.Equal(t, "10", *f.calls[logsLoadPages].q.Token)
}

// A page in another order, as LocalStack sends, still shows in order of
// time and then event ID. An event ID that the view has shows once.
func TestLogEvents_SortsAndDropsKnownIDs(t *testing.T) {
	calls := 0
	f := &fakeEvents{}
	f.filter = func(aws.EventQuery) (*aws.EventPage, error) {
		calls++
		if calls == 1 {
			tok := "t"
			return &aws.EventPage{Next: &tok, Events: []aws.LogEvent{
				evAt(5*time.Minute, "msg-c", "s2", "e3"),
				evAt(10*time.Minute, "msg-b", "s1", "e2"), // same time: e1 comes first
				evAt(10*time.Minute, "msg-a", "s2", "e1"),
			}}, nil
		}
		return &aws.EventPage{End: true, Events: []aws.LogEvent{
			evAt(20*time.Minute, "msg-z", "s1", "e0"),
			evAt(5*time.Minute, "msg-c", "s2", "e3"), // again
		}}, nil
	}
	v := newEvents(t, f, groupParams())
	assert.Equal(t, []string{"msg-z", "msg-a", "msg-b", "msg-c"}, messages(v))
}

// An event ID of the load before shows once too. It can come again at the
// time of the oldest line.
func TestLogEvents_DropsKnownIDsOfTheLoadBefore(t *testing.T) {
	calls := 0
	f := &fakeEvents{}
	f.filter = func(aws.EventQuery) (*aws.EventPage, error) {
		calls++
		tok := "t"
		if calls <= logsLoadPages {
			if calls > 1 {
				return &aws.EventPage{Next: &tok}, nil // empty pages to the end of the load
			}
			return &aws.EventPage{Next: &tok, Events: []aws.LogEvent{
				evAt(10*time.Minute, "msg-edge", "s1", "e1"),
				evAt(time.Minute, "msg-new", "s1", "e2"),
			}}, nil
		}
		return &aws.EventPage{End: true, Events: []aws.LogEvent{
			evAt(20*time.Minute, "msg-old", "s1", "e0"),
			evAt(10*time.Minute, "msg-edge", "s1", "e1"), // again, at the oldest time
		}}, nil
	}
	v := newEvents(t, f, groupParams())
	pressEv(v, keyPress('l'))
	assert.Equal(t, []string{"msg-old", "msg-edge", "msg-new"}, messages(v))
}

// A stream reads with GetLogEvents and goes back with the backward token.
// The token that repeats is the start of the stream.
func TestLogEvents_StreamPagesToTheStart(t *testing.T) {
	var evs []aws.LogEvent
	for i := range 1500 {
		evs = append(evs, evAt(time.Duration(i)*time.Second, fmt.Sprintf("msg-%04d", i), "", ""))
	}
	f := &fakeEvents{events: evs}
	p := streamParams()
	p["stream_first"] = strconv.FormatInt(evNow.Add(-1499*time.Second).UnixMilli(), 10)
	v := newEvents(t, f, p)
	require.NotEmpty(t, f.calls)
	assert.Equal(t, "get", f.calls[0].kind)
	assert.Equal(t, "s1", f.calls[0].q.Stream)
	assert.Equal(t, 1000, v.lv.Len())
	assert.Equal(t, "s1", v.Title())

	pressEv(v, keyPress('l'))
	assert.Equal(t, 1500, v.lv.Len())
	assert.Contains(t, v.Footer(), "start of stream")
	last := f.calls[len(f.calls)-1].q
	require.NotNil(t, last.Token)
	n := len(f.calls)
	pressEv(v, keyPress('l'))
	assert.Len(t, f.calls, n)
	assert.NotContains(t, plain(v.View().Content), "s1 ", "a stream shows no source column")

	// With a pattern, FilterLogEvents names the stream of each event. The
	// title names the stream, so the lines still show no source.
	for i := range f.events {
		f.events[i].Stream, f.events[i].ID = "s1", strconv.Itoa(i)
	}
	pressEv(v, keyPress('f'))
	typeInto(v, "msg-14")
	pressEv(v, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "filter", f.calls[len(f.calls)-1].kind)
	require.NotEmpty(t, messages(v))
	assert.NotContains(t, plain(v.View().Content), " s1 ")
}

// A stream with a filter pattern reads with FilterLogEvents and names the
// stream.
func TestLogEvents_StreamPatternUsesFilter(t *testing.T) {
	f := &fakeEvents{events: []aws.LogEvent{evAt(time.Minute, "msg-ERROR", "s1", "e1"), evAt(time.Minute, "msg-ok", "s1", "e2")}}
	v := newEvents(t, f, streamParams())
	pressEv(v, keyPress('f'))
	typeInto(v, "ERROR")
	pressEv(v, tea.KeyPressMsg{Code: tea.KeyEnter})
	last := f.calls[len(f.calls)-1]
	assert.Equal(t, "filter", last.kind)
	assert.Equal(t, []string{"s1"}, last.q.Streams)
	assert.Equal(t, "ERROR", last.q.Pattern)
	assert.Equal(t, []string{"msg-ERROR"}, messages(v))
}

// f sends a new pattern and replaces the lines. A bad pattern shows the
// error and keeps the old lines and the old pattern.
func TestLogEvents_PatternReplacesLinesOrKeepsThem(t *testing.T) {
	f := &fakeEvents{events: []aws.LogEvent{evAt(time.Minute, "msg-ERROR", "s1", "e1"), evAt(2*time.Minute, "msg-ok", "s1", "e2")}}
	v := newEvents(t, f, groupParams())
	assert.Equal(t, []string{"msg-ok", "msg-ERROR"}, messages(v))

	pressEv(v, keyPress('f'))
	assert.True(t, v.CapturingInput())
	typeInto(v, "ERROR")
	pressEv(v, tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, []string{"msg-ERROR"}, messages(v))
	assert.Contains(t, plain(v.View().Content), "filter: ERROR")

	f.filter = func(aws.EventQuery) (*aws.EventPage, error) {
		return nil, errors.New("InvalidParameterException: bad pattern")
	}
	pressEv(v, keyPress('f'))
	assert.Equal(t, "ERROR", v.input.Value(), "the input starts with the pattern")
	v.input.SetValue("{ bad")
	seen := pressEv(v, tea.KeyPressMsg{Code: tea.KeyEnter})
	em, ok := firstOfType[msg.ErrorMsg](seen)
	require.True(t, ok)
	assert.Contains(t, em.Err.Error(), "bad pattern")
	assert.Equal(t, []string{"msg-ERROR"}, messages(v), "the old lines stay")
	assert.Contains(t, plain(v.View().Content), "filter: ERROR", "the old pattern stays")

	// An empty pattern clears it.
	f.filter = nil
	pressEv(v, keyPress('f'))
	v.input.SetValue("")
	pressEv(v, tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, []string{"msg-ok", "msg-ERROR"}, messages(v))
	assert.NotContains(t, plain(v.View().Content), "filter:")
}

// A failure of l keeps the lines, and the next l asks for the same page.
func TestLogEvents_ThrottleKeepsLinesAndRetries(t *testing.T) {
	var evs []aws.LogEvent
	for i := range 1500 {
		evs = append(evs, evAt(time.Duration(i)*time.Second, fmt.Sprintf("msg-%04d", i), "s1", fmt.Sprintf("e%04d", i)))
	}
	f := &fakeEvents{events: evs}
	v := newEvents(t, f, groupParams())
	require.Equal(t, 1000, v.lv.Len())
	f.filter = func(aws.EventQuery) (*aws.EventPage, error) {
		return nil, errors.New("ThrottlingException: Rate exceeded")
	}
	seen := pressEv(v, keyPress('l'))
	_, ok := firstOfType[msg.ErrorMsg](seen)
	assert.True(t, ok)
	assert.Equal(t, 1000, v.lv.Len())
	assert.Contains(t, v.Footer(), "l for older")

	f.filter = nil
	pressEv(v, keyPress('l'))
	assert.Equal(t, "1000", *f.calls[len(f.calls)-1].q.Token)
	assert.Equal(t, 1500, v.lv.Len())
}

// A token older than 23 hours is not sent. For a group, a new query ends
// 1 ms before the oldest loaded event.
func TestLogEvents_ExpiredTokenGroup(t *testing.T) {
	var evs []aws.LogEvent
	for i := range 1500 {
		evs = append(evs, evAt(time.Duration(i)*time.Second, fmt.Sprintf("msg-%04d", i), "s1", fmt.Sprintf("e%04d", i)))
	}
	f := &fakeEvents{events: evs}
	v := newEvents(t, f, groupParams())
	oldest := evNow.Add(-999 * time.Second)
	useLogsClock(t, evNow.Add(24*time.Hour))
	pressEv(v, keyPress('l'))
	q := f.calls[len(f.calls)-1].q
	assert.Nil(t, q.Token)
	assert.True(t, q.End.Equal(oldest.Add(-time.Millisecond)), "end %v", q.End)
	assert.Equal(t, 1500, v.lv.Len())
}

// For a stream, a new query reads with FilterLogEvents up to the oldest
// loaded event, and drops the events at that time that the view has.
func TestLogEvents_ExpiredTokenStream(t *testing.T) {
	var evs []aws.LogEvent
	for i := range 1001 {
		evs = append(evs, evAt(time.Duration(i)*time.Second, fmt.Sprintf("msg-%04d", i), "s1", ""))
	}
	// Two events at the time of the oldest loaded event; only one loaded.
	evs = append(evs, aws.LogEvent{Time: evNow.Add(-999 * time.Second), Message: "msg-twin", Stream: "s1"})
	f := &fakeEvents{events: evs}
	f.get = func(q aws.EventQuery) (*aws.EventPage, error) {
		p, err := f.page(q, false)
		for i := range p.Events {
			p.Events[i].Stream, p.Events[i].ID = "", ""
		}
		return p, err
	}
	f.filter = func(q aws.EventQuery) (*aws.EventPage, error) {
		p, err := f.page(q, true)
		for i := range p.Events {
			p.Events[i].ID = "id-" + p.Events[i].Message
		}
		return p, err
	}
	v := newEvents(t, f, streamParams())
	require.Equal(t, 1000, v.lv.Len())
	have := messages(v)
	require.NotEmpty(t, have)
	useLogsClock(t, evNow.Add(24*time.Hour))
	pressEv(v, keyPress('l'))
	last := f.calls[len(f.calls)-1]
	assert.Equal(t, "filter", last.kind)
	assert.Equal(t, []string{"s1"}, last.q.Streams)
	assert.True(t, last.q.End.Equal(evNow.Add(-999*time.Second)), "end %v", last.q.End)
	got := messages(v)
	seen := map[string]int{}
	for _, m := range got {
		seen[m]++
	}
	for m, n := range seen {
		assert.Equal(t, 1, n, "%s shows once", m)
	}
	assert.Equal(t, 1002, v.lv.Len(), "every event shows once")
}

// :logs NAME looks the group up first. An unknown name loads nothing.
func TestLogEvents_LookupUnknownGroup(t *testing.T) {
	f := &fakeEvents{}
	v := newEvents(t, f, map[string]string{"log_group": "/nope"})
	assert.Equal(t, 1, f.lookups)
	assert.Empty(t, f.calls)
	assert.Equal(t, "No log group named /nope", v.Footer())
	assert.Contains(t, plain(v.View().Content), "No log group named /nope")
	pressEv(v, keyPress('l'))
	pressEv(v, keyPress('r'))
	assert.Empty(t, f.calls)
}

// A found group gives the record, so the walk stops at its floor.
func TestLogEvents_LookupFindsTheRecord(t *testing.T) {
	g := evGroup
	g.Created = evNow.Add(-30*time.Minute + logsBackfill)
	f := &fakeEvents{group: &g}
	v := newEvents(t, f, map[string]string{"log_group": "/app"})
	require.Len(t, f.calls, 1)
	assert.True(t, f.calls[0].q.Start.Equal(evNow.Add(-30*time.Minute)))
	assert.Contains(t, v.Footer(), "start of group")
	assert.Equal(t, "/app events", v.Title())
}

// A lookup that fails for another reason still loads the events.
func TestLogEvents_LookupErrorStillLoads(t *testing.T) {
	f := &fakeEvents{groupErr: errors.New("AccessDeniedException: no")}
	useLogsClock(t, evNow)
	v := NewLogEvents(f, map[string]string{"log_group": "/app"})
	v.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	seen := pump(v, v.Init(), 20)
	_, ok := firstOfType[msg.ErrorMsg](seen)
	assert.True(t, ok)
	assert.Len(t, f.calls, 1)
	assert.Nil(t, pump(v, v.Init(), 2), "Init runs once")
}

// Enter opens the event in the panel without the focus. JSON shows
// formatted.
func TestLogEvents_DetailOfAnEvent(t *testing.T) {
	f := &fakeEvents{events: []aws.LogEvent{evAt(time.Minute, `{"level":"error","msg-j":1}`, "s1", "e1")}}
	v := newEvents(t, f, groupParams())
	seen := pressEv(v, tea.KeyPressMsg{Code: tea.KeyEnter})
	p, ok := firstOfType[msg.TabbedContentMsg](seen)
	require.True(t, ok)
	assert.True(t, p.NoFocus)
	require.Len(t, p.Tabs, 2)
	assert.Equal(t, "json", p.Tabs[0].Format)
	assert.Contains(t, p.Tabs[0].Content, "\"level\": \"error\"")
	info := p.Tabs[1].Content
	for _, s := range []string{"2026-11-20 14:59:00.000", "2026-11-20 14:59:01.000", "s1", "e1"} {
		assert.Contains(t, info, s)
	}
}

// A message over 64 KB shows cut in the panel, with a note.
func TestLogEvents_DetailCutsALongMessage(t *testing.T) {
	long := strings.Repeat("é", logsDetailMax) // 2 bytes each: 128 KB
	cmd := eventDetail(ui.LogLine{Data: aws.LogEvent{Time: evNow, Message: long}})
	p := collectMsgs(cmd)[0].(msg.TabbedContentMsg)
	text := p.Tabs[0].Content
	assert.Equal(t, "text", p.Tabs[0].Format)
	assert.Contains(t, text, "The panel shows the first 64 KB. Press y in the viewer to copy all of it.")
	assert.Less(t, len(text), logsDetailMax+300)
	assert.True(t, strings.HasSuffix(text, "é"), "the cut keeps whole characters")
	assert.Contains(t, p.Tabs[1].Content, "128.0 KB")

	plainText := eventDetail(ui.LogLine{Data: aws.LogEvent{Time: evNow, Message: "not json"}})
	assert.Equal(t, "text", collectMsgs(plainText)[0].(msg.TabbedContentMsg).Tabs[0].Format)
}

// When the view is full, l stops and the footer says so.
func TestLogEvents_FullViewStopsL(t *testing.T) {
	var evs []aws.LogEvent
	for i := range 1500 {
		evs = append(evs, evAt(time.Duration(i)*time.Second, fmt.Sprintf("msg-%04d", i), "s1", fmt.Sprintf("e%04d", i)))
	}
	f := &fakeEvents{events: evs}
	useLogsClock(t, evNow)
	v := NewLogEvents(f, groupParams())
	v.lv.SetCaps(1200, ui.LogMaxBytes)
	v.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	pump(v, v.Init(), 20)
	pressEv(v, keyPress('l'))
	assert.Equal(t, 1200, v.lv.Len())
	assert.Contains(t, v.Footer(), "the view is full")
	n := len(f.calls)
	pressEv(v, keyPress('l'))
	assert.Len(t, f.calls, n)
}

// r loads the range again from its end. A late result of the old load is
// dropped.
func TestLogEvents_ReloadDropsTheOldLoad(t *testing.T) {
	f := &fakeEvents{events: []aws.LogEvent{evAt(time.Minute, "msg-a", "s1", "e1")}}
	useLogsClock(t, evNow)
	v := NewLogEvents(f, groupParams())
	v.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	first := v.Init()
	late := collectMsgs(first) // the first load reads only msg-a
	f.events = append(f.events, evAt(30*time.Second, "msg-b", "s1", "e2"))
	_, second := v.Update(keyPress('r'))
	pump(v, second, 20)
	deliverAll(v, late...) // its reply comes after the reload
	assert.Equal(t, []string{"msg-a", "msg-b"}, messages(v))
}

// While an input is open, the view captures the keys; esc closes it, and
// esc with nothing to cancel goes back.
func TestLogEvents_InputsAndEsc(t *testing.T) {
	f := &fakeEvents{events: []aws.LogEvent{evAt(time.Minute, "msg-a", "s1", "e1")}}
	v := newEvents(t, f, groupParams())
	pressEv(v, keyPress('f'))
	assert.True(t, v.CapturingInput())
	pressEv(v, keyPress('l'))
	assert.Equal(t, "l", v.input.Value(), "l types into the input")
	pressEv(v, tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.False(t, v.CapturingInput())

	pressEv(v, keyPress('/'))
	assert.True(t, v.CapturingInput(), "the local filter of LogView")
	pressEv(v, tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.False(t, v.CapturingInput())

	// A kept local filter: esc clears it first.
	pressEv(v, keyPress('/'))
	typeInto(v, "zz")
	pressEv(v, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "zz", v.lv.FilterText())
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Empty(t, navigations(collectMsgs(cmd)))
	assert.Empty(t, v.lv.FilterText())

	_, cmd = v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Equal(t, []tea.Msg{msg.NavigateBackMsg{}}, collectMsgs(cmd))
}

// Every row fits the width, and the header is cut with a mark.
func TestLogEvents_RowsFitTheWidth(t *testing.T) {
	f := &fakeEvents{events: []aws.LogEvent{evAt(time.Minute, strings.Repeat("x", 500), strings.Repeat("s", 80), "e1")}}
	v := newEvents(t, f, groupParams())
	pressEv(v, keyPress('f'))
	typeInto(v, strings.Repeat("p", 200))
	for _, w := range []int{20, 40, 80, 160} {
		v.Update(tea.WindowSizeMsg{Width: w, Height: 12})
		lines := strings.Split(v.View().Content, "\n")
		assert.LessOrEqual(t, len(lines), 12, "width %d", w)
		for _, l := range lines {
			assert.LessOrEqual(t, ansi.StringWidth(l), w, "width %d: %q", w, plain(l))
		}
	}
}

func TestLogEvents_NilServiceShowsTheError(t *testing.T) {
	useLogsClock(t, evNow)
	v := NewLogEvents(nil, groupParams())
	v.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	seen := pump(v, v.Init(), 5)
	em, ok := firstOfType[msg.ErrorMsg](seen)
	require.True(t, ok)
	require.ErrorIs(t, em.Err, errNoClient)
	assert.Contains(t, plain(v.View().Content), "Error: AWS client not initialized")
}

// A message and a stream name can hold any character. The view shows
// control characters as pictures. (The pattern input drops them.)
func TestLogEvents_ShowsTextSafely(t *testing.T) {
	f := &fakeEvents{events: []aws.LogEvent{evAt(time.Minute, "msg-"+dcs+csi8, "s-"+dcs, "e1")}}
	v := newEvents(t, f, groupParams())
	out := v.View().Content
	assertSafeOutput(t, out)
	assert.Contains(t, ansi.Strip(out), "msg-␛P$qm␛\\")
	assert.Contains(t, ansi.Strip(out), "s-␛P$qm␛\\")
}

func TestLogEvents_KeyMap(t *testing.T) {
	var keys []string
	for _, h := range NewLogEvents(nil, groupParams()).KeyMap() {
		keys = append(keys, h.Binding.Help().Key)
		assert.NotEqual(t, ui.ModeReadWrite, h.Mode, "every key reads")
	}
	assert.Equal(t, []string{"l", "f", "/", "t", "r", "enter", "y", "V", "w", "G"}, keys)
}

// A range or a pattern set while a query loads builds on that query, so
// f and then t keep both.
func TestLogEvents_PendingQueryKeepsTheUserChoice(t *testing.T) {
	f := &fakeEvents{events: []aws.LogEvent{
		evAt(2*time.Hour, "msg-ERROR-old", "s1", "e1"),
		evAt(time.Minute, "msg-ok", "s1", "e2"),
	}}
	v := newEvents(t, f, groupParams())

	pressEv(v, keyPress('f'))
	typeInto(v, "ERROR")
	_, pending := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // not run yet
	assert.Contains(t, plain(v.View().Content), "filter: ERROR", "the header shows the query that loads")
	pressEv(v, keyPress('f'))
	assert.Equal(t, "ERROR", v.input.Value(), "f starts with the pattern that loads")
	pressEv(v, tea.KeyPressMsg{Code: tea.KeyEscape})
	_, cmd := v.Update(msg.PickerResultMsg{Value: "3h", Tag: &logsRangeTag{}})
	pump(v, cmd, 20)
	last := f.calls[len(f.calls)-1].q
	pump(v, pending, 20) // the late reply of the pattern query
	head := plain(strings.SplitN(v.View().Content, "\n", 2)[0])
	assert.Equal(t, "last 3h · "+logZone(evNow)+" · filter: ERROR", head)
	assert.Equal(t, []string{"msg-ERROR-old"}, messages(v))
	assert.Equal(t, "ERROR", last.Pattern)
	assert.True(t, last.Start.Equal(evNow.Add(-3*time.Hour)))
}

// A query that fails with no events puts the header back to the query
// that the lines show.
func TestLogEvents_FailedQueryPutsTheHeaderBack(t *testing.T) {
	f := &fakeEvents{events: []aws.LogEvent{evAt(time.Minute, "msg-ok", "s1", "e1")}}
	v := newEvents(t, f, groupParams())
	f.filter = func(aws.EventQuery) (*aws.EventPage, error) { return nil, errors.New("denied") }
	_, cmd := v.Update(msg.PickerResultMsg{Value: "7d", Tag: &logsRangeTag{}})
	pump(v, cmd, 20)
	assert.Contains(t, plain(v.View().Content), "last 1h")
	pressEv(v, keyPress('f'))
	assert.Empty(t, v.input.Value())
}

// A range or pattern set during the group lookup stays when the lookup
// ends, and the query then knows where the group starts.
func TestLogEvents_QueryDuringTheLookup(t *testing.T) {
	g := evGroup
	g.Created = evNow.Add(-30*time.Minute + logsBackfill)
	f := &fakeEvents{group: &g, events: []aws.LogEvent{
		evAt(10*time.Minute, "msg-ERROR", "s1", "e1"),
		evAt(5*time.Minute, "msg-ok", "s1", "e2"),
	}}
	useLogsClock(t, evNow)
	v := NewLogEvents(f, map[string]string{"log_group": "/app"})
	v.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	lookup := v.Init()
	pressEv(v, keyPress('f'))
	typeInto(v, "ERROR")
	_, early := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	pump(v, lookup, 20)
	last := f.calls[len(f.calls)-1].q
	pump(v, early, 20) // the load that started before the record came
	assert.Contains(t, plain(v.View().Content), "filter: ERROR")
	assert.Equal(t, []string{"msg-ERROR"}, messages(v))
	assert.True(t, last.Start.Equal(evNow.Add(-30*time.Minute)), "the query starts at the floor of the group")
}

// When the lookup finds no group, a load that the user started during the
// lookup shows nothing.
func TestLogEvents_NotFoundDropsALoadOfTheLookup(t *testing.T) {
	f := &fakeEvents{events: []aws.LogEvent{evAt(time.Minute, "msg-a", "s1", "e1")}}
	useLogsClock(t, evNow)
	v := NewLogEvents(f, map[string]string{"log_group": "/nope"})
	v.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	lookup := v.Init()
	_, early := v.Update(keyPress('r'))
	pump(v, lookup, 20)
	pump(v, early, 20)
	assert.Empty(t, messages(v))
	assert.Equal(t, "No log group named /nope", v.Footer())
}

// A failed first load says so in the footer, and r tries again.
func TestLogEvents_FailedFirstLoadFooter(t *testing.T) {
	f := &fakeEvents{events: []aws.LogEvent{evAt(time.Minute, "msg-a", "s1", "e1")}}
	f.filter = func(aws.EventQuery) (*aws.EventPage, error) { return nil, errors.New("AccessDeniedException: no") }
	v := newEvents(t, f, groupParams())
	assert.Equal(t, "could not load events · r to try again", v.Footer())
	f.filter = nil
	pressEv(v, keyPress('r'))
	assert.Equal(t, []string{"msg-a"}, messages(v))
	assert.Equal(t, "1 events · 14:00 to 15:00 · l for older", v.Footer())
}

// A stream whose window starts before 2024 does not switch to
// FilterLogEvents when its token expires: AWS rejects that query.
func TestLogEvents_ExpiredStreamTokenBefore2024(t *testing.T) {
	base := time.Date(2023, 6, 1, 12, 0, 0, 0, time.UTC)
	var evs []aws.LogEvent
	for i := range 1500 {
		evs = append(evs, aws.LogEvent{Time: base.Add(-time.Duration(i) * time.Second), Message: fmt.Sprintf("msg-%04d", i)})
	}
	f := &fakeEvents{events: evs}
	v := newEvents(t, f, streamParams())
	v.Update(msg.PickerResultMsg{Value: logRangeCustom, Tag: &logsRangeTag{}})
	typeInto(v, "2023-06-01T11:00:00Z")
	pressEv(v, tea.KeyPressMsg{Code: tea.KeyEnter})
	typeInto(v, "2023-06-01T12:00:00Z")
	pressEv(v, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, 1000, v.lv.Len())
	first := f.calls[len(f.calls)-1].q
	assert.True(t, first.End.Equal(base.Add(time.Millisecond)), "GetLogEvents gets the custom end plus 1 ms: %v", first.End)

	useLogsClock(t, evNow.Add(24*time.Hour))
	n := len(f.calls)
	pressEv(v, keyPress('l'))
	last := f.calls[n]
	assert.Equal(t, "get", last.kind)
	assert.Nil(t, last.q.Token)
	assert.True(t, last.q.End.Equal(base.Add(-999*time.Second)), "end %v", last.q.End)
	assert.Equal(t, 1500, v.lv.Len())
}

// A stream walks back window by window. The end of GetLogEvents is
// exclusive, so the next window ends at the old start, and an event at
// that time shows once.
func TestLogEvents_StreamWalkBack(t *testing.T) {
	f := &fakeEvents{events: []aws.LogEvent{
		{Time: evNow.Add(-time.Hour), Message: "msg-edge"},
		{Time: evNow.Add(-90 * time.Minute), Message: "msg-older"},
		{Time: evNow.Add(-time.Minute), Message: "msg-new"},
	}}
	v := newEvents(t, f, streamParams())
	assert.Equal(t, []string{"msg-edge", "msg-new"}, messages(v))
	n := len(f.calls)
	pressEv(v, keyPress('l'))
	q := f.calls[n].q
	assert.True(t, q.End.Equal(evNow.Add(-time.Hour)), "end %v", q.End)
	assert.True(t, q.Start.Equal(evNow.Add(-2*time.Hour)))
	assert.Equal(t, []string{"msg-older", "msg-edge", "msg-new"}, messages(v))
}

// A window that would start before the floor starts at the floor.
func TestLogEvents_WalkBackStopsAtTheFloorInAWindow(t *testing.T) {
	g := evGroup
	g.Created = evNow.Add(-4*time.Hour + logsBackfill) // the floor is 4 hours ago
	f := &fakeEvents{}
	v := newEvents(t, f, logGroupParams(g))
	_, cmd := v.Update(msg.PickerResultMsg{Value: "3h", Tag: &logsRangeTag{}})
	pump(v, cmd, 20)
	pressEv(v, keyPress('l'))
	q := f.calls[len(f.calls)-1].q
	assert.True(t, q.Start.Equal(evNow.Add(-4*time.Hour)), "start %v", q.Start)
	assert.True(t, q.End.Equal(evNow.Add(-3*time.Hour-time.Millisecond)))
	assert.Contains(t, v.Footer(), "start of group")
}

// A stream with a pattern reads with FilterLogEvents, so its walk stops
// at 2024-01-01 too.
func TestLogEvents_StreamPatternStopsAtTheEpoch(t *testing.T) {
	f := &fakeEvents{}
	v := newEvents(t, f, streamParams())
	pressEv(v, keyPress('f'))
	typeInto(v, "x")
	pressEv(v, tea.KeyPressMsg{Code: tea.KeyEnter})
	v.Update(msg.PickerResultMsg{Value: logRangeCustom, Tag: &logsRangeTag{}})
	typeInto(v, "2024-01-01T06:00:00Z")
	pressEv(v, tea.KeyPressMsg{Code: tea.KeyEnter})
	typeInto(v, "2024-01-01T12:00:00Z")
	pressEv(v, tea.KeyPressMsg{Code: tea.KeyEnter})
	n := len(f.calls)
	pressEv(v, keyPress('l'))
	require.Len(t, f.calls, n+1)
	assert.True(t, f.calls[n].q.Start.Equal(logsEpoch), "start %v", f.calls[n].q.Start)
	assert.Contains(t, v.Footer(), "start of stream")
}

// While the window has more pages, the span starts at the oldest event.
func TestLogEvents_SpanStartsAtTheOldestEvent(t *testing.T) {
	var evs []aws.LogEvent
	for i := range 1500 {
		evs = append(evs, evAt(time.Duration(i)*time.Second, fmt.Sprintf("msg-%04d", i), "s1", fmt.Sprintf("e%04d", i)))
	}
	v := newEvents(t, &fakeEvents{events: evs}, groupParams())
	assert.Equal(t, "1000 events · 14:43 to 15:00 · l for older", v.Footer())
}
