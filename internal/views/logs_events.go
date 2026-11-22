package views

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The event viewer of CloudWatch Logs: the events of a log group, or of
// one stream. See docs/design/cwlogs.md, "The event viewer".

const (
	// logsLoadEvents and logsLoadPages end one load (one l).
	logsLoadEvents = 1000
	logsLoadPages  = 10
	// logsDetailMax is the part of a message that the detail shows.
	logsDetailMax = 64 << 10
)

// logsEpoch is the oldest start time of a newest-first FilterLogEvents
// query. AWS rejects an earlier one with InvalidParameterException.
var logsEpoch = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// logsBackfill is how old an event can be when PutLogEvents takes it. So
// a group can hold events from up to 14 days before its creation.
const logsBackfill = 14 * 24 * time.Hour

// logsNow is the clock of the viewer, so tests can set it.
var logsNow = time.Now

// logRange is the time range of a query: the last dur, or a custom range.
type logRange struct {
	dur        time.Duration // relative: the length; 0 for a custom range
	start, end time.Time     // custom: both ends, inclusive
}

func (r logRange) custom() bool { return r.dur == 0 }

// length is the length of the range. l moves a window back by it.
func (r logRange) length() time.Duration {
	if r.custom() {
		return max(r.end.Sub(r.start), time.Second)
	}
	return r.dur
}

// logRangePresets are the ranges of the t picker (decision D3).
var logRangePresets = []struct {
	label string
	dur   time.Duration
}{
	{"5m", 5 * time.Minute}, {"15m", 15 * time.Minute}, {"1h", time.Hour},
	{"3h", 3 * time.Hour}, {"12h", 12 * time.Hour}, {"1d", 24 * time.Hour},
	{"3d", 72 * time.Hour}, {"7d", 7 * 24 * time.Hour},
}

const logRangeCustom = "custom"

// label shows the range for the header: "last 1h", or the custom times.
func (r logRange) label() string {
	if r.custom() {
		return logClock(r.start, true) + " to " + logClock(r.end, true)
	}
	for _, p := range logRangePresets {
		if p.dur == r.dur {
			return "last " + p.label
		}
	}
	return "last " + r.dur.String()
}

// eventWalk is the state of a query as it walks back in time. A window is
// a part of the range. Pages of a window come with a token. When a window
// ends, l moves to the window before it, of the same length.
type eventWalk struct {
	rng     logRange
	pattern string
	// filter is true when the window reads with FilterLogEvents (both ends
	// inclusive), false for GetLogEvents of a stream (end exclusive).
	filter           bool
	winStart, winEnd time.Time // winEnd zero: open, the first window of a relative range
	token            *string
	tokenAt          time.Time
	done             bool      // the window has no more pages
	atStart          bool      // no older window can hold events
	spanEnd          time.Time // the end of the loaded span, for the footer
	// After a stream token expired, the window reads with FilterLogEvents
	// up to the oldest loaded event. It drops an event at that time whose
	// message the view already has.
	dropAt   time.Time
	dropMsgs map[string]bool
}

// logsEventsMsg is the result of one load.
type logsEventsMsg struct {
	gen     int
	walk    eventWalk
	events  []aws.LogEvent
	err     error
	replace bool // a new query: the events replace the lines
}

// logsGroupMsg is the result of the group lookup.
type logsGroupMsg struct {
	group *aws.LogGroup
	err   error
}

// logsRangeTag marks the replies of the t picker.
type logsRangeTag struct{}

// PromptTag marks logsRangeTag as a prompt tag.
func (*logsRangeTag) PromptTag() {}

// The sources of the text inputs of the viewer.
const (
	logsPatternSource = "logs-pattern"
	logsStartSource   = "logs-start"
	logsEndSource     = "logs-end"
)

// logsEventKeys are the keys of the viewer. The LogView keys come after
// them in the hints.
var logsEventKeys = struct {
	Older   ui.HintBinding
	Pattern ui.HintBinding
	Range   ui.HintBinding
	Reload  ui.HintBinding
	Local   ui.HintBinding
	Open    ui.HintBinding
	Copy    ui.HintBinding
	Visual  ui.HintBinding
	Wrap    ui.HintBinding
	Follow  ui.HintBinding
	Tail    ui.HintBinding
}{
	Older:   ui.NewHintBinding([]string{"l"}, "l", "older events"),
	Pattern: ui.NewHintBinding([]string{"f"}, "f", "filter pattern (AWS)"),
	Range:   ui.NewHintBinding([]string{"t"}, "t", "time range"),
	Reload:  ui.NewHintBinding([]string{"r"}, "r", "reload"),
	Local:   ui.NewHintBinding([]string{"/"}, "/", "filter loaded lines"),
	Open:    ui.NewHintBinding([]string{"enter"}, "enter", "event detail"),
	Copy:    ui.NewHintBinding([]string{"y"}, "y", "copy"),
	Visual:  ui.NewHintBinding([]string{"V"}, "V", "select lines"),
	Wrap:    ui.NewHintBinding([]string{"w"}, "w", "wrap"),
	Follow:  ui.NewHintBinding([]string{"G"}, "G", "end and follow"),
	Tail:    ui.NewHintBinding([]string{"F"}, "F", "live tail"),
}

// logQuery is what a query asks for: the range and the filter pattern.
type logQuery struct {
	rng     logRange
	pattern string
}

// LogEvents shows the events of a log group, or of one stream.
type LogEvents struct {
	viewCtx

	logs      aws.LogsService
	groupName string
	stream    string // "": the whole group
	// missingHint follows the text for a group that does not exist, and
	// missingStreamHint the text for a stream that does not exist.
	missingHint, missingStreamHint string
	group                          *aws.LogGroup // nil until known
	streamAt                       time.Time     // the first event of the stream, when known

	lv          ui.LogView
	input       ui.Filter // the pattern or a custom time; one at a time
	inputSource string    // which input is open: logsPatternSource, ...
	// customStart holds the start time while the end time input is open.
	customStart time.Time

	walk eventWalk
	// want is the range and pattern of the last query that the user asked
	// for. It is ahead of walk while that query loads, so t, f and r build
	// on it. A query that fails with no events puts it back to walk.
	want     logQuery
	gen      int
	started  bool
	loading  bool
	loaded   bool // a query showed its first result
	failed   bool // the first query failed with no events
	full     bool // the view has no room for older lines
	notFound bool
	// noStream is true when the first query found no stream (H6). r tries
	// again, because the stream can come later.
	noStream bool

	oldest     time.Time       // the time of the oldest loaded event
	oldestMsgs map[string]bool // the messages of the events at oldest
	seen       map[string]bool // the event IDs of the last two loads
	seenPrev   map[string]bool
	// head holds the newest events that the view shows: the first load,
	// and the events of the poll (see headKey). The poll does not show
	// them again, also in a later poll session. A new query clears it.
	head map[string]time.Time
	// lookingUp is true while the group lookup runs. replacing is true
	// while the load of a new query runs.
	lookingUp, replacing bool

	tail        tailState
	tailCovered bool // another view stopped live tail; say so on the way back

	width, height int
}

// NewLogEvents creates the viewer from the parameters of its route:
// log_group, and log_stream for one stream. The group list also sends
// log_group_arn, log_class, log_created (ms) and log_retention (days), so
// the viewer needs no lookup. The stream list sends stream_first (ms).
// log_start and log_end (ms) open a custom range, for example the lines of
// one Lambda invocation. missing_hint follows the text for a group that
// does not exist, for example why a Lambda function has no group yet.
// missing_stream_hint follows the text for a stream that does not exist.
func NewLogEvents(logs aws.LogsService, p map[string]string) *LogEvents {
	rng := logRange{dur: time.Hour}
	if start, end := paramMillis(p["log_start"]), paramMillis(p["log_end"]); !start.IsZero() && end.After(start) {
		rng = logRange{start: start, end: end}
	}
	v := &LogEvents{
		logs:              logs,
		groupName:         p["log_group"],
		stream:            p["log_stream"],
		missingHint:       p["missing_hint"],
		missingStreamHint: p["missing_stream_hint"],
		lv:                ui.NewLogView(),
		walk:              eventWalk{rng: rng},
		want:              logQuery{rng: rng},
	}
	if arn := p["log_group_arn"]; arn != "" {
		g := aws.LogGroup{Name: v.groupName, ARN: arn, Class: p["log_class"]}
		g.Created = paramMillis(p["log_created"])
		g.RetentionDays, _ = strconv.Atoi(p["log_retention"])
		v.group = &g
	}
	v.streamAt = paramMillis(p["stream_first"])
	v.lv.SetEmptyText("No events")
	return v
}

// paramMillis reads a time in milliseconds since the epoch, or gives the
// zero time.
func paramMillis(s string) time.Time {
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil || ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// LogGroupParams are the route parameters that open the events of g. The
// jump of the app uses them too.
func LogGroupParams(g aws.LogGroup) map[string]string {
	p := map[string]string{
		"log_group":     g.Name,
		"log_group_arn": g.ARN,
		"log_class":     g.Class,
		"log_retention": strconv.Itoa(g.RetentionDays),
	}
	if !g.Created.IsZero() {
		p["log_created"] = strconv.FormatInt(g.Created.UnixMilli(), 10)
	}
	return p
}

func (v *LogEvents) ID() string { return "logs_events:" + v.groupName + ":" + v.stream }

func (v *LogEvents) Title() string {
	if v.stream != "" {
		return v.stream
	}
	return v.groupName + " events"
}

// CapturingInput implements nav.InputCapturer.
func (v *LogEvents) CapturingInput() bool { return v.input.Active() || v.lv.CapturingInput() }

// Restyle implements ui.Restyler.
func (v *LogEvents) Restyle() { v.lv.Restyle() }

func (v *LogEvents) KeyMap() []ui.HintBinding {
	k := logsEventKeys
	tail := k.Tail
	if v.tail.mode != tailOff {
		tail = ui.NewHintBinding([]string{"F"}, "F", "stop live tail")
	}
	return []ui.HintBinding{tail, k.Older, k.Pattern, k.Local, k.Range, k.Reload, k.Open, k.Copy, k.Visual, k.Wrap, k.Follow}
}

func (v *LogEvents) Init() tea.Cmd {
	if v.started {
		return nil
	}
	v.started = true
	if v.group == nil {
		return v.lookup()
	}
	return v.query(v.want.rng, v.want.pattern)
}

// lookup reads the group record: its ARN, class, creation time and
// retention.
func (v *LogEvents) lookup() tea.Cmd {
	svc, ctx, name := v.logs, v.viewContext(), v.groupName
	v.loading, v.lookingUp = true, true
	return func() tea.Msg {
		if svc == nil {
			return logsGroupMsg{err: errNoClient}
		}
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		g, err := svc.GetLogGroup(ctx, name)
		return logsGroupMsg{group: g, err: err}
	}
}

func (v *LogEvents) onGroup(m logsGroupMsg) tea.Cmd {
	v.loading, v.lookingUp = false, false
	switch {
	case errors.Is(m.err, aws.ErrLogGroupNotFound):
		v.notFound = true
		v.gen++ // drop a load that the user started during the lookup
		v.lv.Clear()
		text := "No log group named " + v.groupName
		if v.missingHint != "" {
			text += ". " + v.missingHint
		}
		v.lv.SetEmptyText(text)
		return nil
	case m.err != nil:
		// The events can still load. The walk back then stops only at
		// 2024-01-01 or at the start of the stream.
		e := msg.ErrorMsg{Err: m.err, Context: "reading log group " + v.groupName}
		return tea.Batch(func() tea.Msg { return e }, v.query(v.want.rng, v.want.pattern))
	}
	// Start the query again with the record, so the walk knows where the
	// group starts. This keeps a range or pattern set during the lookup.
	v.group = m.group
	return v.query(v.want.rng, v.want.pattern)
}

// floor is the oldest time that can hold events: 14 days before the
// creation of the group (PutLogEvents takes events up to 14 days old, also
// right after the group was made), now minus its retention, and the first
// event of the stream. A FilterLogEvents window also starts at 2024-01-01
// or later.
func (v *LogEvents) floor(filter bool, now time.Time) time.Time {
	var f time.Time
	if filter {
		f = logsEpoch
	}
	later := func(t time.Time) {
		if t.After(f) {
			f = t
		}
	}
	if v.group != nil {
		if !v.group.Created.IsZero() {
			later(v.group.Created.Add(-logsBackfill))
		}
		if d := v.group.RetentionDays; d > 0 {
			later(now.Add(-time.Duration(d) * 24 * time.Hour))
		}
	}
	later(v.streamAt)
	return f
}

// query starts a new query for rng and pattern. The lines stay until the
// first result comes, so a bad pattern keeps them.
func (v *LogEvents) query(rng logRange, pattern string) tea.Cmd {
	if v.notFound {
		return nil
	}
	v.want, v.failed, v.noStream = logQuery{rng: rng, pattern: pattern}, false, false
	now := logsNow()
	filter := v.stream == "" || pattern != ""
	w := eventWalk{rng: rng, pattern: pattern, filter: filter}
	// A new query replaces the lines, also the lines of live tail.
	cmds := []tea.Cmd{v.stopTailFor()}
	if rng.custom() {
		w.winStart, w.winEnd, w.spanEnd = rng.start, rng.end, rng.end
		if !filter {
			// GetLogEvents does not include its end time.
			w.winEnd = rng.end.Add(time.Millisecond)
		}
	} else {
		// A relative range sends no end time, so a slow local clock does
		// not hide the newest events.
		w.winStart, w.spanEnd = now.Add(-rng.dur), now
	}
	if f := v.floor(filter, now); w.winStart.Before(f) {
		if rng.custom() && filter && rng.start.Before(logsEpoch) {
			cmds = append(cmds, toastCmd(msg.ToastInfo("AWS reads newest events first only from 2024-01-01, so the range starts there")))
		}
		w.winStart = f
		if !w.winEnd.IsZero() && w.winEnd.Before(f) {
			// No part of the range can hold events.
			v.gen++
			v.reset(w)
			v.walk.done, v.walk.atStart = true, true
			return tea.Batch(cmds...)
		}
	}
	v.lv.SetLongTimes(rng.length() > 24*time.Hour)
	return tea.Batch(append(cmds, v.load(w, true))...)
}

// reset clears the lines and starts a query with w.
func (v *LogEvents) reset(w eventWalk) {
	v.lv.Clear()
	v.walk = w
	v.loaded, v.full, v.loading, v.replacing = true, false, false, false
	v.oldest, v.oldestMsgs, v.seen, v.seenPrev, v.head = time.Time{}, nil, nil, nil, nil
	v.lv.SetEmptyText("No events")
}

// load reads one load of w: at most logsLoadEvents events or
// logsLoadPages pages, until the window ends.
func (v *LogEvents) load(w eventWalk, replace bool) tea.Cmd {
	v.gen++
	v.loading, v.replacing = true, replace
	gen, svc, ctx := v.gen, v.logs, v.viewContext()
	group, stream := v.groupName, v.stream
	return func() tea.Msg {
		if svc == nil {
			return logsEventsMsg{gen: gen, walk: w, err: errNoClient, replace: replace}
		}
		var got []aws.LogEvent
		for range logsLoadPages {
			if len(got) >= logsLoadEvents {
				break
			}
			q := aws.EventQuery{Group: group, Start: w.winStart, End: w.winEnd,
				Limit: int32(logsLoadEvents - len(got)), Token: w.token} //nolint:gosec // at most 1000
			p, err := readEvents(ctx, svc, q, stream, w)
			if err != nil {
				return logsEventsMsg{gen: gen, walk: w, events: got, err: err, replace: replace}
			}
			// The pages go back in time, so a later page is older.
			got = append(slices.Clip(p.Events), got...)
			w.token, w.tokenAt = p.Next, logsNow()
			if p.End {
				w.done = true
				break
			}
		}
		eventlog.Debugf(eventlog.CatAWS, "Loaded %d log events of %s", len(got), group)
		return logsEventsMsg{gen: gen, walk: w, events: got, replace: replace}
	}
}

// readEvents reads one page with the call of the window.
func readEvents(ctx context.Context, svc aws.LogsService, q aws.EventQuery, stream string, w eventWalk) (*aws.EventPage, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if stream != "" && !w.filter {
		q.Stream = stream
		return svc.GetStreamEvents(ctx, q)
	}
	if stream != "" {
		q.Streams = []string{stream}
	}
	q.Pattern = w.pattern
	return svc.FilterEvents(ctx, q)
}

func (v *LogEvents) onEvents(m logsEventsMsg) tea.Cmd {
	if m.gen != v.gen {
		return nil
	}
	v.loading, v.replacing = false, false
	var cmds []tea.Cmd
	if m.err != nil {
		if v.streamMissing(m) {
			return nil
		}
		err := m.err
		cmds = append(cmds, func() tea.Msg { return msg.ErrorMsg{Err: err, Context: "loading log events"} })
		if len(m.events) == 0 {
			if !v.loaded {
				v.failed = true
				v.lv.SetEmptyText("Error: " + aws.ErrorText(err))
			}
			// The lines and the query stay. l tries the same page again.
			if m.replace {
				v.want = logQuery{rng: v.walk.rng, pattern: v.walk.pattern}
			}
			return tea.Batch(cmds...)
		}
	}
	if m.replace {
		v.reset(m.walk)
	}
	v.walk = m.walk
	lines := v.take(m.events)
	if m.replace {
		v.noteHead(lines)
	}
	if n := v.lv.Prepend(lines); n < len(lines) || (len(lines) > 0 && v.lv.Dropped() > 0) {
		v.full = true
	}
	if v.walk.done && !v.walk.winStart.After(v.floor(v.walk.filter, logsNow())) {
		v.walk.atStart = true
	}
	return tea.Batch(cmds...)
}

// streamMissing shows the text for a missing stream when the first query
// of a stream found none (H6). AWS gives the same error for a missing
// group, so it counts only when the group is known. It returns false for
// any other failure.
func (v *LogEvents) streamMissing(m logsEventsMsg) bool {
	if v.loaded || v.group == nil || v.stream == "" || len(m.events) > 0 ||
		!errors.Is(m.err, aws.ErrLogStreamNotFound) {
		return false
	}
	v.noStream = true
	if m.replace {
		v.want = logQuery{rng: v.walk.rng, pattern: v.walk.pattern}
	}
	text := "No log stream named " + v.stream + " in " + v.groupName
	if v.missingStreamHint != "" {
		text += ". " + v.missingStreamHint
	}
	v.lv.SetEmptyText(text)
	return true
}

// noteHead keeps the newest lines of a first load, for the poll.
func (v *LogEvents) noteHead(lines []ui.LogLine) {
	keep := logsNow().Add(-logsPollKeep)
	for _, l := range slices.Backward(lines) {
		if l.Time.Before(keep) {
			break
		}
		if v.head == nil {
			v.head = map[string]time.Time{}
		}
		v.head[headKey(l.ID, l.Time, l.Message)] = l.Time
	}
}

// take sorts the events by time and event ID, drops the events that the
// view already has, and returns the lines.
func (v *LogEvents) take(events []aws.LogEvent) []ui.LogLine {
	slices.SortStableFunc(events, func(a, b aws.LogEvent) int {
		if c := a.Time.Compare(b.Time); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	cur := map[string]bool{}
	lines := make([]ui.LogLine, 0, len(events))
	for _, e := range events {
		if e.ID != "" {
			if v.seen[e.ID] || v.seenPrev[e.ID] || cur[e.ID] {
				continue
			}
			cur[e.ID] = true
		}
		if !v.walk.dropAt.IsZero() && e.Time.Equal(v.walk.dropAt) && v.walk.dropMsgs[e.Message] {
			continue
		}
		if !v.oldest.IsZero() && !e.Time.Before(v.oldest) && !e.Time.Equal(v.oldest) {
			continue // newer than the oldest line: Prepend needs older lines
		}
		src := e.Stream
		if v.stream != "" {
			src = "" // one stream: the title names it
		}
		lines = append(lines, ui.LogLine{Time: e.Time, Source: src, Message: e.Message, ID: e.ID, Data: e})
	}
	v.seenPrev, v.seen = v.seen, cur
	if len(lines) > 0 {
		v.oldest = lines[0].Time
		v.oldestMsgs = map[string]bool{}
		for _, l := range lines {
			if !l.Time.Equal(v.oldest) {
				break
			}
			v.oldestMsgs[l.Message] = true
		}
	}
	return lines
}

// older starts the next load back in time (l).
func (v *LogEvents) older() tea.Cmd {
	if v.loading || !v.loaded || v.notFound || v.full || v.walk.atStart {
		return nil
	}
	w := v.walk
	now := logsNow()
	switch {
	case w.done:
		next, ok := v.shift(w, now)
		if !ok {
			v.walk.atStart = true
			return nil
		}
		w = next
	case w.token != nil && now.Sub(w.tokenAt) > tokenMaxAge:
		// The token expired (AWS keeps it 24 hours). A new query starts
		// at the oldest loaded event.
		w.token = nil
		switch {
		case v.oldest.IsZero():
		case w.filter:
			w.winEnd = v.oldest.Add(-time.Millisecond)
		case w.winStart.Before(logsEpoch):
			// FilterLogEvents cannot read newest first before 2024, so
			// GetLogEvents starts again before the oldest event. An event
			// in the same millisecond that did not load yet is missed.
			w.winEnd = v.oldest
		default:
			// GetLogEvents has an exclusive end and no event ID, so the
			// stream reads with FilterLogEvents up to the oldest event,
			// and drops the events at that time that the view has.
			w.filter = true
			w.winEnd = v.oldest
			w.dropAt, w.dropMsgs = v.oldest, maps.Clone(v.oldestMsgs)
		}
	}
	return v.load(w, false)
}

// shift returns the window before the window of w, of the same length,
// or false when no older window can hold events.
func (v *LogEvents) shift(w eventWalk, now time.Time) (eventWalk, bool) {
	filter := v.stream == "" || w.pattern != ""
	f := v.floor(filter, now)
	if !w.winStart.After(f) {
		return w, false
	}
	end := w.winStart // GetLogEvents does not include its end
	if filter {
		end = w.winStart.Add(-time.Millisecond) // both ends are inclusive
	}
	start := w.winStart.Add(-w.rng.length())
	if start.Before(f) {
		start = f
	}
	return eventWalk{rng: w.rng, pattern: w.pattern, filter: filter,
		winStart: start, winEnd: end, spanEnd: w.spanEnd}, true
}

// ─── Update ─────────────────────────────────────────────────────────────────

func (v *LogEvents) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case logsGroupMsg:
		return v, v.onGroup(m)
	case logsEventsMsg:
		return v, v.onEvents(m)
	case logsTailStartMsg:
		return v, v.onTailStart(m)
	case logsTailUpdateMsg:
		return v, v.onTailUpdate(m)
	case logsPollMsg:
		return v, v.onPoll(m)
	case logsPollTickMsg:
		return v, v.onPollTick(m)
	case msg.ErrorMsg:
		return v, nil // the echo of an error of the viewer; the status bar shows it
	case ui.LogLineOpenedMsg:
		return v, eventDetail(m.Line)
	case msg.PickerResultMsg:
		if _, ok := m.Tag.(*logsRangeTag); ok {
			return v, v.onRangePick(m)
		}
		return v, nil
	case ui.FilterChangedMsg:
		return v, nil // LogView filters as the user types; the inputs act on enter
	case tea.WindowSizeMsg:
		v.width, v.height = m.Width, m.Height
		v.input.SetWidth(m.Width)
		v.layout()
		return v, nil
	case tea.KeyPressMsg:
		return v, v.key(m)
	}
	if v.input.Active() {
		var cmd tea.Cmd
		v.input, cmd = v.input.Update(m) // a paste
		v.layout()
		return v, cmd
	}
	return v, v.lv.Update(m)
}

// layout gives the LogView the rows under the header and the input.
// The input shows its prompt on a row of its own, and a long value
// wraps, so layout measures it. Call it after each change of the input.
func (v *LogEvents) layout() {
	h := v.height - 1
	if v.input.Active() {
		h -= lipgloss.Height(v.input.View())
	}
	v.lv.SetSize(v.width, max(h, 1))
}

func (v *LogEvents) key(m tea.KeyPressMsg) tea.Cmd {
	if v.input.Active() {
		return v.inputKey(m)
	}
	if v.lv.CapturingInput() || v.lv.InVisualMode() {
		return v.lv.Update(m)
	}
	switch {
	case m.String() == "esc":
		if v.lv.WantsEsc() {
			return v.lv.Update(m)
		}
		return func() tea.Msg { return msg.NavigateBackMsg{} }
	case key.Matches(m, logsEventKeys.Older.Binding):
		return v.older()
	case key.Matches(m, logsEventKeys.Tail.Binding):
		return v.toggleTail()
	case key.Matches(m, logsEventKeys.Reload.Binding):
		return v.query(v.want.rng, v.want.pattern)
	case key.Matches(m, logsEventKeys.Pattern.Binding):
		v.openInput(logsPatternSource, "filter pattern", "ERROR, \"timeout\", { $.level = \"error\" }")
		v.input.SetValue(v.want.pattern)
		v.layout()
		return nil
	case key.Matches(m, logsEventKeys.Range.Binding):
		opts := make([]string, 0, len(logRangePresets)+1)
		for _, p := range logRangePresets {
			opts = append(opts, p.label)
		}
		opts = append(opts, logRangeCustom)
		return func() tea.Msg {
			return msg.RequestActionPickerMsg{Title: "Time range", Options: opts, Tag: &logsRangeTag{}}
		}
	}
	return v.lv.Update(m)
}

func (v *LogEvents) openInput(source, prompt, placeholder string) {
	v.input = ui.NewFilterWithPrompt(prompt, placeholder).WithSource(source)
	v.inputSource = source
	v.input.SetWidth(v.width)
	v.input.Activate()
	v.layout()
}

func (v *LogEvents) closeInput() {
	v.input.Deactivate()
	v.layout()
}

// inputKey handles a key of the open input. Enter applies it, esc closes it.
func (v *LogEvents) inputKey(m tea.KeyPressMsg) tea.Cmd {
	switch m.String() {
	case "esc":
		v.closeInput()
		return nil
	case "enter":
		return v.applyInput(strings.TrimSpace(v.input.Value()))
	}
	var cmd tea.Cmd
	v.input, cmd = v.input.Update(m)
	v.layout()
	return cmd
}

func (v *LogEvents) applyInput(text string) tea.Cmd {
	switch v.inputSource {
	case logsPatternSource:
		v.closeInput()
		return v.query(v.want.rng, text)
	case logsStartSource:
		t, ok := parseLogTime(text, logsNow())
		if !ok {
			return toastCmd(msg.ToastError(badLogTime(text)))
		}
		v.customStart = t
		v.openInput(logsEndSource, "end time", "empty for now")
		return nil
	case logsEndSource:
		end := logsNow()
		if text != "" {
			t, ok := parseLogTime(text, end)
			if !ok {
				return toastCmd(msg.ToastError(badLogTime(text)))
			}
			end = t
		}
		if !end.After(v.customStart) {
			return toastCmd(msg.ToastError("The end time must be after the start time"))
		}
		v.closeInput()
		return v.query(logRange{start: v.customStart, end: end}, v.want.pattern)
	}
	v.closeInput()
	return nil
}

func (v *LogEvents) onRangePick(m msg.PickerResultMsg) tea.Cmd {
	if !m.Picked() {
		return nil
	}
	if m.Value == logRangeCustom {
		v.openInput(logsStartSource, "start time", "2006-01-02 15:04 (local) or RFC 3339")
		return nil
	}
	for _, p := range logRangePresets {
		if p.label == m.Value {
			return v.query(logRange{dur: p.dur}, v.want.pattern)
		}
	}
	return nil
}

// parseLogTime reads a custom time. A time with Z or an offset in RFC 3339
// form is read as written. Other forms are local time.
func parseLogTime(s string, now time.Time) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, now.Location()); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// badLogTime is the toast for a time that parseLogTime cannot read.
func badLogTime(s string) string {
	return fmt.Sprintf("Unknown time %q. Use 2006-01-02 15:04 or RFC 3339.", s)
}

func toastCmd(t msg.ToastMsg) tea.Cmd { return func() tea.Msg { return t } }

// ─── Detail ─────────────────────────────────────────────────────────────────

// eventDetail opens the event in the side panel without the focus, so the
// keys of the viewer still work.
func eventDetail(l ui.LogLine) tea.Cmd {
	e, _ := l.Data.(aws.LogEvent)
	text, format, note := e.Message, "text", ""
	if len(text) > logsDetailMax {
		n := logsDetailMax
		for n > 0 && !utf8.RuneStart(text[n]) {
			n-- // cut before a character, not in it
		}
		cut := text[:n]
		note = fmt.Sprintf("The message has %s. The panel shows the first 64 KB. Press y in the viewer to copy all of it.", aws.FormatBytes(int64(len(text))))
		text = note + "\n\n" + cut
	} else if json.Valid([]byte(text)) {
		var b bytes.Buffer
		if json.Indent(&b, []byte(text), "", "  ") == nil {
			text, format = b.String(), "json"
		}
	}
	info, _ := buildInfo([]infoField{
		{K: "Time", V: logTimeMillis(e.Time)},
		{K: "Ingested", V: logTimeMillis(e.Ingested)},
		{K: "Stream", V: e.Stream},
		{K: "Event ID", V: e.ID},
		{K: "Size", V: aws.FormatBytes(int64(len(e.Message)))},
		{K: "Note", V: note},
	})
	title := logTimeMillis(e.Time)
	return func() tea.Msg {
		return msg.TabbedContentMsg{PanelTitle: title, NoFocus: true, Tabs: []msg.TabContent{
			{Title: "Message", Content: text, Format: format},
			{Title: "Info", Content: info, Format: "text"},
		}}
	}
}

// logTimeMillis shows t with milliseconds, or "" for a zero time.
func logTimeMillis(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05.000")
}

// ─── View ───────────────────────────────────────────────────────────────────

func (v *LogEvents) View() tea.View {
	var b strings.Builder
	b.WriteString(ansi.Truncate(ui.S.Info.Render(ui.Sanitize(v.header())), max(v.width, 1), "…"))
	b.WriteByte('\n')
	if v.input.Active() {
		b.WriteString(v.input.View())
		b.WriteByte('\n')
	}
	b.WriteString(v.lv.View())
	return tea.NewView(b.String())
}

// header names live tail, the range, the zone and the pattern:
// "LIVE 02:13 · last 1h · UTC-05:00 · filter: ERROR".
func (v *LogEvents) header() string {
	var parts []string
	if t := v.tailHeader(); t != "" {
		parts = append(parts, t)
	}
	parts = append(parts, v.want.rng.label(), logZone(logsNow()))
	if v.want.pattern != "" {
		parts = append(parts, "filter: "+v.want.pattern)
	}
	return strings.Join(parts, " · ")
}

// logZone names the zone of t: "UTC" or "UTC-05:00".
func logZone(t time.Time) string {
	if _, off := t.Zone(); off == 0 {
		return "UTC"
	}
	return "UTC" + t.Format("-07:00")
}

// Footer shows the count, the loaded span and what l does next:
// "1000 events · 14:02 to 15:02 · l for older".
func (v *LogEvents) Footer() string {
	switch {
	case v.notFound:
		return "No log group named " + v.groupName
	case !v.loaded && v.lv.Len() > 0:
		// Live tail shows lines after a first query that failed, for
		// example while it waits for a stream that did not exist yet.
		return fmt.Sprintf("%d events from live tail · r to load the range", v.lv.Len())
	case v.loading && !v.loaded:
		return "Loading events..."
	case v.noStream:
		return "No log stream named " + v.stream + " · r to try again"
	case v.failed:
		return "could not load events · r to try again"
	case !v.loaded:
		return "Loading events..."
	}
	span := v.span()
	s := fmt.Sprintf("%d events · %s", v.lv.Len(), span)
	if v.lv.Len() == 0 {
		s = "no events from " + span
	}
	noun := "group"
	if v.stream != "" {
		noun = "stream"
	}
	switch {
	case v.loading:
		s += " · loading..."
	case v.full:
		s += " · the view is full"
	case v.walk.atStart:
		s += " · start of " + noun
	default:
		s += " · l for older"
	}
	if v.tail.mode == tailLive && v.tail.sampled {
		s += " · AWS shows a sample above 500 events each second"
	}
	return s
}

// span shows the loaded part of the range: from the start of the window,
// or from the oldest event while the window has more pages, to the end.
func (v *LogEvents) span() string {
	start := v.walk.winStart
	if !v.walk.done && !v.oldest.IsZero() {
		start = v.oldest
	}
	end := v.walk.spanEnd
	long := v.walk.rng.length() > 24*time.Hour ||
		start.In(time.Local).YearDay() != end.In(time.Local).YearDay() ||
		start.Year() != end.Year()
	return logClock(start, long) + " to " + logClock(end, long)
}

// logClock shows t as "15:04", or with the date when long is true.
func logClock(t time.Time, long bool) string {
	t = t.In(time.Local)
	if long {
		return t.Format("2006-01-02 15:04")
	}
	return t.Format("15:04")
}
