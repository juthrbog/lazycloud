package views

import (
	"context"
	"errors"
	"fmt"
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

// Tests for the invocation list of a function (docs/design/lambda.md,
// PR 5).

func reportEvent(ago time.Duration, text string) aws.LogEvent {
	return aws.LogEvent{Time: evNow.Add(-ago), Stream: "2026/11/20/[$LATEST]abc", ID: "ev-" + text[:min(len(text), 30)], Message: text}
}

func textReport(id string, dur float64, init float64, tail string) string {
	s := fmt.Sprintf("REPORT RequestId: %s\tDuration: %.2f ms\tBilled Duration: %d ms\tMemory Size: 256 MB\tMax Memory Used: 75 MB\t",
		id, dur, int(dur)+1)
	if init > 0 {
		s += fmt.Sprintf("Init Duration: %.2f ms\t", init)
	}
	return s + tail + "\n"
}

func TestParseReportText(t *testing.T) {
	in := parseReport(reportEvent(0, textReport("r1", 26.73, 130.49, "")))
	assert.True(t, in.Parsed)
	assert.Equal(t, "r1", in.RequestID)
	assert.InDelta(t, 26.73, in.DurationMS, 1e-9)
	assert.InDelta(t, 27, in.BilledMS, 1e-9)
	assert.InDelta(t, 130.49, in.InitMS, 1e-9)
	assert.Equal(t, 256, in.MemoryMB)
	assert.Equal(t, 75, in.UsedMB)
	assert.Equal(t, "ok", in.Status)
	assert.True(t, in.cold())
	assert.False(t, in.failed())

	warm := parseReport(reportEvent(0, textReport("r2", 1, 0, "")))
	assert.False(t, warm.cold())

	errLine := parseReport(reportEvent(0, textReport("r3", 3, 0, "Status: error\tError Type: Runtime.Unknown\t")))
	assert.Equal(t, "error", errLine.Status)
	assert.Equal(t, "Runtime.Unknown", errLine.ErrorType)
	assert.True(t, errLine.failed())

	timeout := parseReport(reportEvent(0, textReport("r4", 3000, 0, "Status: timeout\t")))
	assert.Equal(t, "timeout", timeout.Status)

	// The spacing differs between AWS examples.
	spaced := parseReport(reportEvent(0, "REPORT RequestId: r5  Duration: 15.74 ms\tBilled Duration: 147 ms \tMemory Size: 128 MB\tMax Memory Used: 56 MB  \t\n"))
	assert.Equal(t, "r5  Duration: 15.74 ms", spaced.RequestID, "fields are split by tabs")
	tabbed := parseReport(reportEvent(0, "REPORT RequestId: r6\tDuration: 15.74 ms\tBilled Duration: 147 ms \tMemory Size: 128 MB\tMax Memory Used: 56 MB  \t\n"))
	assert.InDelta(t, 147, tabbed.BilledMS, 1e-9)
	assert.Equal(t, 56, tabbed.UsedMB)

	for _, raw := range []string{"INIT_REPORT Init Duration: 3.1 ms\tPhase: init\tStatus: error", "START RequestId: r7 Version: $LATEST", "REPORT nothing here", ""} {
		got := parseReport(reportEvent(0, raw+"x"))
		assert.False(t, got.Parsed, raw)
		assert.Empty(t, got.Status, raw)
		assert.False(t, got.failed(), "a line that did not parse is not an error: %q", raw)
	}
}

func TestParseReportJSON(t *testing.T) {
	j := `{"time":"2026-11-20T15:00:00Z","type":"platform.report","record":{"requestId":"j1","status":"success",` +
		`"metrics":{"durationMs":12.5,"billedDurationMs":13,"memorySizeMB":1024,"maxMemoryUsedMB":80,"initDurationMs":180.4}}}`
	in := parseReport(reportEvent(0, j))
	assert.True(t, in.Parsed)
	assert.Equal(t, "j1", in.RequestID)
	assert.Equal(t, "ok", in.Status)
	assert.InDelta(t, 12.5, in.DurationMS, 1e-9)
	assert.InDelta(t, 13, in.BilledMS, 1e-9)
	assert.InDelta(t, 180.4, in.InitMS, 1e-9)
	assert.Equal(t, 1024, in.MemoryMB)
	assert.Equal(t, 80, in.UsedMB)

	fail := parseReport(reportEvent(0, `{"type":"platform.report","record":{"requestId":"j2","status":"timeout","metrics":{"durationMs":3000}}}`))
	assert.Equal(t, "timeout", fail.Status)
	assert.True(t, fail.failed())
	e := parseReport(reportEvent(0, `{"type":"platform.report","record":{"requestId":"j3","status":"error","errorType":"Runtime.Unknown","metrics":{}}}`))
	assert.Equal(t, "Runtime.Unknown", e.ErrorType)

	// Managed Instances send only durationMs.
	mi := parseReport(reportEvent(0, `{"type":"platform.report","record":{"requestId":"j4","status":"success","metrics":{"durationMs":4}}}`))
	assert.True(t, mi.Parsed)
	assert.Zero(t, mi.MemoryMB)

	for _, raw := range []string{`{"type":"platform.start","record":{"requestId":"j5"}}`, `{"type":"platform.report","record":{}}`, `{not json`} {
		assert.False(t, parseReport(reportEvent(0, raw)).Parsed, raw)
	}
}

// invFake is the LogsService of the invocation list: a group record, and
// FilterEvents pages that the test sets for each call.
type invFake struct {
	aws.LogsService

	group    *aws.LogGroup
	groupErr error
	queries  []aws.EventQuery
	answer   func(n int, q aws.EventQuery) (*aws.EventPage, error)
}

func (f *invFake) GetLogGroup(context.Context, string) (*aws.LogGroup, error) {
	if f.groupErr != nil {
		return nil, f.groupErr
	}
	return f.group, nil
}

func (f *invFake) FilterEvents(ctx context.Context, q aws.EventQuery) (*aws.EventPage, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("no time limit")
	}
	f.queries = append(f.queries, q)
	return f.answer(len(f.queries)-1, q)
}

func invParams(format, level string) map[string]string {
	return map[string]string{"function": "orders-api", "log_group": "/aws/lambda/orders-api", "log_format": format, "system_log_level": level}
}

func newInvocations(t *testing.T, f *invFake, p map[string]string, rounds int) *LambdaInvocations {
	t.Helper()
	useLogsClock(t, evNow)
	v := NewLambdaInvocations(f, p)
	v.SetContext(t.Context())
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	pump(v, v.Init(), rounds)
	return v
}

// The walk reads the last hour first, newest first, with the pattern of
// the log format and 100 lines a call. A window that has more pages keeps
// its token; a window that ends moves one hour back, until the floor.
func TestLambdaInvocations_Walk(t *testing.T) {
	g := &aws.LogGroup{Name: "/aws/lambda/orders-api", RetentionDays: 1}
	f := &invFake{group: g, answer: func(n int, q aws.EventQuery) (*aws.EventPage, error) {
		switch n {
		case 0:
			return &aws.EventPage{Events: []aws.LogEvent{
				reportEvent(20*time.Minute, textReport("old", 5, 0, "")),
				reportEvent(10*time.Minute, textReport("new", 7, 100, "")),
			}, Next: new("aws-tok")}, nil
		default:
			return &aws.EventPage{End: true}, nil
		}
	}}
	v := newInvocations(t, f, invParams("Text", ""), 40)
	require.GreaterOrEqual(t, len(f.queries), 3)
	q0, q1, q2 := f.queries[0], f.queries[1], f.queries[2]
	assert.True(t, q0.Start.Equal(evNow.Add(-time.Hour)), "start %v", q0.Start)
	assert.True(t, q0.End.IsZero(), "the first window is open")
	assert.Equal(t, lambdaReportText, q0.Pattern)
	assert.Equal(t, int32(100), q0.Limit)
	assert.Equal(t, "/aws/lambda/orders-api", q0.Group)
	assert.Equal(t, "aws-tok", *q1.Token, "the window goes on with its token")
	assert.True(t, q1.Start.Equal(q0.Start))
	assert.Nil(t, q2.Token)
	assert.True(t, q2.End.Equal(evNow.Add(-time.Hour-time.Millisecond)), "end %v", q2.End)
	assert.True(t, q2.Start.Equal(evNow.Add(-2*time.Hour)))

	// One load reads 10 pages. With a retention of 1 day, l walks on until
	// now minus 1 day, and then stops.
	assert.Len(t, f.queries, lambdaInvocationPages)
	for range 5 {
		_, cmd := v.Update(keyPress('l'))
		if cmd == nil {
			break
		}
		pump(v, cmd, 40)
	}
	last := f.queries[len(f.queries)-1]
	assert.True(t, last.Start.Equal(evNow.Add(-24*time.Hour)), "start %v", last.Start)
	assert.Nil(t, v.more, "the walk reached the floor")
	assert.Len(t, f.queries, 1+24, "one page with a token, then 24 windows")

	assert.Equal(t, []string{"new", "old"}, invocationIDs(v), "newest first")
}

func invocationIDs(v *LambdaInvocations) []string {
	var ids []string
	for _, in := range v.items {
		ids = append(ids, in.RequestID)
	}
	return ids
}

// The JSON format reads platform.report lines.
func TestLambdaInvocations_JSONPattern(t *testing.T) {
	f := &invFake{group: &aws.LogGroup{}, answer: func(int, aws.EventQuery) (*aws.EventPage, error) {
		return &aws.EventPage{End: true}, nil
	}}
	newInvocations(t, f, invParams("JSON", "WARN"), 3)
	require.NotEmpty(t, f.queries)
	assert.Equal(t, lambdaReportJSON, f.queries[0].Pattern) //nolint:testifylint // a filter pattern, not a JSON document
}

// A load stops after 10 pages, and l loads the next 10.
func TestLambdaInvocations_Budget(t *testing.T) {
	f := &invFake{group: &aws.LogGroup{}, answer: func(n int, _ aws.EventQuery) (*aws.EventPage, error) {
		return &aws.EventPage{Events: []aws.LogEvent{reportEvent(time.Duration(n)*time.Second, textReport("r"+strconv.Itoa(n), 1, 0, ""))},
			Next: new("t" + strconv.Itoa(n))}, nil
	}}
	v := newInvocations(t, f, invParams("Text", ""), 40)
	assert.Len(t, f.queries, lambdaInvocationPages)
	assert.Len(t, v.items, lambdaInvocationPages)
	assert.Contains(t, v.Footer(), "l for more")
	_, cmd := v.Update(keyPress('l'))
	require.NotNil(t, cmd)
	pump(v, cmd, 40)
	assert.Len(t, f.queries, 2*lambdaInvocationPages)
}

// The footer counts the rows, the cold starts and the errors, and shows
// the loaded span and the WARN note.
func TestLambdaInvocations_FooterAndRows(t *testing.T) {
	f := &invFake{group: &aws.LogGroup{}, answer: func(n int, _ aws.EventQuery) (*aws.EventPage, error) {
		if n > 0 {
			return &aws.EventPage{End: true}, nil
		}
		return &aws.EventPage{Events: []aws.LogEvent{
			reportEvent(30*time.Minute, textReport("a", 12.5, 180, "")),
			reportEvent(20*time.Minute, textReport("b", 1500, 0, "Status: timeout\t")),
			reportEvent(10*time.Minute, textReport("c", 3, 0, "Status: error\tError Type: Runtime.Unknown\t")),
			reportEvent(5*time.Minute, "REPORT garbage"),
		}}, nil
	}}
	v := newInvocations(t, f, invParams("Text", "WARN"), 4)
	footer := v.Footer()
	assert.Contains(t, footer, "4/4 invocations · 1 cold · 2 errors · ")
	assert.Contains(t, footer, logClock(evNow.Add(-30*time.Minute), false)+" to "+logClock(evNow.Add(-5*time.Minute), false))
	assert.Contains(t, footer, "only failed invocations are logged")

	out := plain(v.View().Content)
	for _, s := range []string{"12.5 ms", "180 ms", "13 ms", "75/256 MB", "1.50 s", "timeout", "error Runtime.Unknown", "REPORT garbage", "[$LATEST]abc"} {
		assert.Contains(t, out, s)
	}
	row := lambdaInvocationRow(parseReport(reportEvent(0, textReport("x", 1500, 0, ""))), ui.TierWide)
	assert.Equal(t, ui.SortKeyInt(1500000), row.Sort[1], "the duration sorts as a number")
	assert.Len(t, lambdaInvocationRow(parseReport(reportEvent(0, textReport("x", 1, 0, ""))), ui.TierNarrow).Cells, 4)
	assert.Len(t, lambdaInvocationRow(parseReport(reportEvent(0, "bad")), ui.TierMedium).Cells, 6)
}

// Each row fits the window at every width.
func TestLambdaInvocations_FitsTheWindow(t *testing.T) {
	for _, w := range []int{40, 60, 80, 100, 120, 160, 200} {
		f := &invFake{group: &aws.LogGroup{}, answer: func(n int, _ aws.EventQuery) (*aws.EventPage, error) {
			return &aws.EventPage{End: true, Events: []aws.LogEvent{
				reportEvent(time.Minute, textReport("a-long-request-id-0000-0000-0000", 99999.99, 9999.99, "Status: error\tError Type: Runtime.ExitError.Something.Long\t")),
				reportEvent(2*time.Minute, strings.Repeat("unparsed ", 40)),
			}}, nil
		}}
		useLogsClock(t, evNow)
		v := NewLambdaInvocations(f, invParams("Text", ""))
		v.SetContext(t.Context())
		v.Update(tea.WindowSizeMsg{Width: w, Height: 20})
		pump(v, v.Init(), 3)
		for row := range strings.SplitSeq(v.View().Content, "\n") {
			assert.LessOrEqual(t, ansi.StringWidth(row), w, "width %d: %q", w, plain(row))
		}
	}
}

// Enter opens the stream of the invocation, from its start (minus its
// duration and init, and one second) to its REPORT line. d opens the
// detail with the raw line.
func TestLambdaInvocations_EnterAndDetail(t *testing.T) {
	at := evNow.Add(-10 * time.Minute)
	f := &invFake{group: &aws.LogGroup{}, answer: func(int, aws.EventQuery) (*aws.EventPage, error) {
		return &aws.EventPage{End: true, Events: []aws.LogEvent{reportEvent(10*time.Minute, textReport("r1", 500, 1500, ""))}}, nil
	}}
	v := newInvocations(t, f, invParams("Text", ""), 3)
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	start := at.Add(-2*time.Second - time.Second)
	assert.Equal(t, []msg.NavigateMsg{{ViewID: "logs_events", Params: map[string]string{
		"log_group": "/aws/lambda/orders-api", "log_stream": "2026/11/20/[$LATEST]abc",
		"log_start": strconv.FormatInt(start.UnixMilli(), 10), "log_end": strconv.FormatInt(at.UnixMilli(), 10),
		"missing_hint": lambdaMissingHint,
	}}}, navigations(collectMsgs(cmd)))

	_, cmd = v.Update(keyPress('d'))
	panel := detailPanel(t, v, cmd)
	assert.Equal(t, "orders-api r1", panel.PanelTitle)
	for _, s := range []string{"Request ID       r1", "Status           ok", "500 ms", "1.50 s (cold start)", "75 of 256 MB", "[$LATEST]abc"} {
		assert.Contains(t, panel.Tabs[0].Content, s)
	}
	assert.Contains(t, panel.Tabs[1].Content, "REPORT RequestId: r1")

	bad := lambdaInvocationDetail("fn", parseReport(reportEvent(0, "REPORT nothing")))
	assert.Contains(t, bad.PanelTitle, "fn ")
	assert.NotContains(t, bad.Tabs[0].Content, "Duration")
}

// A log group that does not exist gives a note, not the error screen, and
// no FilterLogEvents call.
func TestLambdaInvocations_MissingGroup(t *testing.T) {
	f := &invFake{groupErr: fmt.Errorf("%w: /aws/lambda/orders-api", aws.ErrLogGroupNotFound)}
	v := newInvocations(t, f, invParams("Text", ""), 3)
	assert.Empty(t, f.queries)
	out := plain(v.View().Content)
	assert.Contains(t, strings.Join(strings.Fields(out), " "), "No log group named /aws/lambda/orders-api. Lambda makes it at the first invocation of the function.")
	assert.NotContains(t, out, "Error")
	assert.Equal(t, "No log group named /aws/lambda/orders-api", v.Footer())
}

// Another lookup error still loads, with the floor at 2024-01-01.
func TestLambdaInvocations_LookupErrorStillLoads(t *testing.T) {
	f := &invFake{groupErr: errors.New("AccessDeniedException: no"), answer: func(int, aws.EventQuery) (*aws.EventPage, error) {
		return &aws.EventPage{End: true}, nil
	}}
	v := newInvocations(t, f, invParams("Text", ""), 3)
	assert.NotEmpty(t, f.queries)
	assert.True(t, v.floor.Equal(logsEpoch))
}

// With no line, the list says which format it read, and that l looks
// further back while the walk has more.
func TestLambdaInvocations_NoLines(t *testing.T) {
	f := &invFake{group: &aws.LogGroup{}, answer: func(n int, _ aws.EventQuery) (*aws.EventPage, error) {
		return &aws.EventPage{End: true}, nil
	}}
	v := newInvocations(t, f, invParams("JSON", ""), 40)
	out := strings.Join(strings.Fields(plain(v.View().Content)), " ")
	assert.Contains(t, out, "No REPORT lines in the JSON format in the loaded time.")
	assert.Contains(t, out, "l looks further back.")
}

// The floor comes from the creation of the group (minus 14 days) and its
// retention.
func TestLambdaInvocations_Floor(t *testing.T) {
	f := &invFake{group: &aws.LogGroup{Created: evNow.Add(-2 * time.Hour).Add(logsBackfill)}, answer: func(int, aws.EventQuery) (*aws.EventPage, error) {
		return &aws.EventPage{End: true}, nil
	}}
	v := newInvocations(t, f, invParams("Text", ""), 40)
	assert.True(t, v.floor.Equal(evNow.Add(-2*time.Hour)), "floor %v", v.floor)
	assert.Len(t, f.queries, 2, "two windows of one hour")
	assert.Nil(t, v.more)
}

func TestLambdaMS(t *testing.T) {
	assert.Equal(t, "12.5 ms", lambdaMS(12.5))
	assert.Equal(t, "999 ms", lambdaMS(999))
	assert.Equal(t, "1.00 s", lambdaMS(1000))
	assert.Equal(t, "3.25 s", lambdaMS(3250))
}

// i on a function opens its invocations with its logging config.
func TestLambdaFunctions_InvocationsKey(t *testing.T) {
	fn := testFunction
	fn.LogFormat, fn.SystemLogLevel = "JSON", "WARN"
	v := newFunctions(t, &fakeLambda{pages: [][]aws.Function{{fn}}}, 160)
	_, cmd := v.Update(keyPress('i'))
	assert.Equal(t, []msg.NavigateMsg{{ViewID: "lambda_invocations", Params: map[string]string{
		"function": "orders-api", "log_group": "/aws/lambda/orders-api", "log_format": "JSON", "system_log_level": "WARN",
	}}}, navigations(collectMsgs(cmd)))
}

// The keys wait on the error screen and with the filter open.
func TestLambdaInvocations_KeysWait(t *testing.T) {
	f := &invFake{group: &aws.LogGroup{}, answer: func(int, aws.EventQuery) (*aws.EventPage, error) {
		return &aws.EventPage{End: true, Events: []aws.LogEvent{reportEvent(time.Minute, textReport("r1", 1, 0, ""))}}, nil
	}}
	v := newInvocations(t, f, invParams("Text", ""), 3)
	v.Update(msg.ErrorMsg{Err: errors.New("throttled")})
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Empty(t, navigations(collectMsgs(cmd)))

	w := newInvocations(t, f, invParams("Text", ""), 3)
	w.Update(keyPress('/'))
	_, cmd = w.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Empty(t, navigations(collectMsgs(cmd)))
}

// No load starts before the group lookup ends, because fetch reads the
// floor that the lookup sets. r and l wait.
func TestLambdaInvocations_KeysWaitForTheLookup(t *testing.T) {
	f := &invFake{group: &aws.LogGroup{}, answer: func(int, aws.EventQuery) (*aws.EventPage, error) {
		return &aws.EventPage{End: true}, nil
	}}
	useLogsClock(t, evNow)
	v := NewLambdaInvocations(f, invParams("Text", ""))
	v.SetContext(t.Context())
	lookup := v.Init() // not run yet
	for _, k := range []rune{'r', 'l'} {
		_, cmd := v.Update(keyPress(k))
		assert.Nil(t, cmd, "%c waits for the lookup", k)
	}
	assert.Empty(t, f.queries, "no load before the lookup ends")
	pump(v, lookup, 5)
	require.NotEmpty(t, f.queries, "the load starts after the lookup")
	assert.True(t, f.queries[0].Start.Equal(evNow.Add(-time.Hour)))
}

// After the first invocation makes the group, r looks it up again and
// loads the rows.
func TestLambdaInvocations_RefreshAfterAMissingGroup(t *testing.T) {
	f := &invFake{groupErr: aws.ErrLogGroupNotFound, answer: func(int, aws.EventQuery) (*aws.EventPage, error) {
		return &aws.EventPage{End: true, Events: []aws.LogEvent{reportEvent(time.Minute, textReport("first", 9, 200, ""))}}, nil
	}}
	v := newInvocations(t, f, invParams("Text", ""), 3)
	require.True(t, v.missing)
	_, cmd := v.Update(keyPress('l'))
	assert.Nil(t, cmd, "only r acts on a missing group")

	f.groupErr, f.group = nil, &aws.LogGroup{}
	_, cmd = v.Update(keyPress('r'))
	require.NotNil(t, cmd)
	pump(v, cmd, 5)
	assert.False(t, v.missing)
	assert.Equal(t, []string{"first"}, invocationIDs(v))
	assert.Contains(t, v.Footer(), "1/1 invocations")
}

// Lambda retries an async invocation with the same request ID, and each
// try writes a REPORT line. Each line is a row, and the failed tries count.
func TestLambdaInvocations_RetriesStayRows(t *testing.T) {
	f := &invFake{group: &aws.LogGroup{}, answer: func(int, aws.EventQuery) (*aws.EventPage, error) {
		a := reportEvent(3*time.Minute, textReport("same", 3, 0, "Status: error\tError Type: Runtime.Unknown\t"))
		b := reportEvent(2*time.Minute, textReport("same", 3, 0, "Status: error\tError Type: Runtime.Unknown\t"))
		c := reportEvent(time.Minute, textReport("same", 3, 0, ""))
		a.ID, b.ID, c.ID = "e1", "e2", "e3"
		return &aws.EventPage{End: true, Events: []aws.LogEvent{a, b, c}}, nil
	}}
	v := newInvocations(t, f, invParams("Text", ""), 3)
	assert.Equal(t, []string{"same", "same", "same"}, invocationIDs(v))
	assert.Contains(t, v.Footer(), "3/3 invocations · 0 cold · 2 errors")
}

// The time sorts by the whole time, so rows from two days keep their order.
func TestLambdaInvocations_TimeSortsAcrossDays(t *testing.T) {
	late := lambdaInvocationRow(lambdaInvocation{Time: time.Date(2026, 11, 19, 23, 40, 0, 0, time.Local), Parsed: true}, ui.TierWide)
	early := lambdaInvocationRow(lambdaInvocation{Time: time.Date(2026, 11, 20, 0, 5, 0, 0, time.Local), Parsed: true}, ui.TierWide)
	assert.Less(t, late.Sort[0], early.Sort[0], "23:40 of the day before sorts first")
	bad := lambdaInvocationRow(lambdaInvocation{Time: time.Date(2026, 11, 20, 0, 5, 0, 0, time.Local), Raw: "x"}, ui.TierNarrow)
	assert.Equal(t, early.Sort[0], bad.Sort[0])
}

// Large memory sizes fit their column, and Managed Instances rows, which
// report only the duration, leave the memory and the billed time empty.
func TestLambdaInvocations_MemoryCells(t *testing.T) {
	mi := `{"type":"platform.report","record":{"requestId":"mi","status":"success","metrics":{"durationMs":4}}}`
	f := &invFake{group: &aws.LogGroup{}, answer: func(int, aws.EventQuery) (*aws.EventPage, error) {
		big := "REPORT RequestId: big\tDuration: 5 ms\tBilled Duration: 5 ms\tMemory Size: 10240 MB\tMax Memory Used: 10240 MB\t\n"
		return &aws.EventPage{End: true, Events: []aws.LogEvent{reportEvent(2*time.Minute, big), reportEvent(time.Minute, mi)}}, nil
	}}
	v := newInvocations(t, f, invParams("Text", ""), 3)
	assert.Contains(t, plain(v.View().Content), "10240/10240 MB")
	row := lambdaInvocationRow(parseReport(reportEvent(0, mi)), ui.TierWide)
	assert.Equal(t, "4 ms", row.Cells[1])
	assert.Empty(t, row.Cells[2], "no billed time")
	assert.Empty(t, row.Cells[3], "no memory")
}

// A custom log group can hold the lines of other functions. The list keeps
// only the streams of this function; the default group needs no check.
func TestLambdaInvocations_SharedGroup(t *testing.T) {
	mine := reportEvent(2*time.Minute, textReport("mine", 1, 0, ""))
	mine.Stream = "2026/11/20/orders-api[$LATEST]abc"
	other := reportEvent(time.Minute, textReport("other", 1, 0, ""))
	other.Stream = "2026/11/20/billing[$LATEST]def"
	answer := func(int, aws.EventQuery) (*aws.EventPage, error) {
		return &aws.EventPage{End: true, Events: []aws.LogEvent{mine, other}}, nil
	}
	p := invParams("Text", "")
	p["log_group"] = "/custom/shared"
	v := newInvocations(t, &invFake{group: &aws.LogGroup{}, answer: answer}, p, 3)
	assert.Equal(t, []string{"mine"}, invocationIDs(v))

	w := newInvocations(t, &invFake{group: &aws.LogGroup{}, answer: answer}, invParams("Text", ""), 3)
	assert.Len(t, invocationIDs(w), 2, "the default group belongs to the function")
}

// A line needs the REPORT prefix, also when its fields look right.
func TestParseReportNeedsThePrefix(t *testing.T) {
	assert.False(t, parseReport(reportEvent(0, "RequestId: r1\tDuration: 1 ms\tBilled Duration: 1 ms\t")).Parsed)
}

// A floor newer than an hour ago clamps the first window too.
func TestLambdaInvocations_FirstWindowStartsAtTheFloor(t *testing.T) {
	f := &invFake{group: &aws.LogGroup{Created: evNow.Add(-30 * time.Minute).Add(logsBackfill)}, answer: func(int, aws.EventQuery) (*aws.EventPage, error) {
		return &aws.EventPage{End: true}, nil
	}}
	v := newInvocations(t, f, invParams("Text", ""), 5)
	require.Len(t, f.queries, 1)
	assert.True(t, f.queries[0].Start.Equal(evNow.Add(-30*time.Minute)), "start %v", f.queries[0].Start)
	assert.Nil(t, v.more)
}
