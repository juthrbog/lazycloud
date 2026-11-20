package views

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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

// Tests for the log group and log stream lists (docs/design/cwlogs.md,
// PR 4).

// fakeLogs is an aws.LogsService with numbered pages: n pages of one item
// each, with the tokens "t1" to "t<n-1>". A token in fail fails once.
type fakeLogs struct {
	aws.LogsService // the event calls are not used by the lists

	pages  int
	fail   map[string]bool
	tokens []string // the tokens the list calls got, "" for nil
	group  string   // the group of the last stream call
	tags   map[string]string
	tagErr error
	tagARN string // the ARN of the last tags call
}

// page returns the index of tok and records it. A token in fail fails once.
func (f *fakeLogs) page(tok *string) (int, *string, error) {
	i, k := 0, ""
	if tok != nil {
		k = *tok
		n, err := strconv.Atoi(k[1:])
		if err != nil {
			return 0, nil, err
		}
		i = n
	}
	if f.fail[k] {
		delete(f.fail, k)
		f.tokens = append(f.tokens, k+"!")
		return 0, nil, errors.New("throttled")
	}
	f.tokens = append(f.tokens, k)
	if i == f.pages-1 {
		return i, nil, nil
	}
	next := fmt.Sprintf("t%d", i+1)
	return i, &next, nil
}

func (f *fakeLogs) ListLogGroupsPage(_ context.Context, tok *string) (*aws.LogGroupPage, error) {
	i, next, err := f.page(tok)
	if err != nil {
		return nil, err
	}
	name := fmt.Sprintf("/g/%03d", i)
	return &aws.LogGroupPage{Groups: []aws.LogGroup{{Name: name, ARN: "arn:aws:logs:r:1:log-group:" + name}}, Next: next}, nil
}

func (f *fakeLogs) ListLogStreamsPage(_ context.Context, group string, tok *string) (*aws.LogStreamPage, error) {
	f.group = group
	i, next, err := f.page(tok)
	if err != nil {
		return nil, err
	}
	return &aws.LogStreamPage{Streams: []aws.LogStream{{Name: fmt.Sprintf("s%03d", i)}}, Next: next}, nil
}

func (f *fakeLogs) ListLogGroupTags(_ context.Context, arn string) (map[string]string, error) {
	f.tagARN = arn
	return f.tags, f.tagErr
}

// logList is what the tests need of both log lists.
type logList interface {
	tea.Model
	Init() tea.Cmd
	Footer() string
	count() int
}

func (g *LogGroups) count() int  { return len(g.items) }
func (s *LogStreams) count() int { return len(s.items) }

// logLists builds each log list on f, sized for the tests.
func logLists(f *fakeLogs) map[string]func() logList {
	size := func(v logList) logList {
		v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
		return v
	}
	return map[string]func() logList{
		"logs_groups":  func() logList { return size(NewLogGroups(f)) },
		"logs_streams": func() logList { return size(NewLogStreams(f, "/aws/lambda/orders")) },
	}
}

// loadAll runs the load of v until it stops and returns every message.
func loadAll(v logList, cmd tea.Cmd) []tea.Msg { return pump(v, cmd, 100) }

// H2, fixed for the log lists: a load stops after the page budget, and l
// loads the next budget. The groups load 20 pages, the streams 4.
func TestLogsH2_LogListsStopAtTheBudget(t *testing.T) {
	budgets := map[string]int{"logs_groups": logGroupPageBudget, "logs_streams": logStreamPageBudget}
	assert.Equal(t, 20, logGroupPageBudget, "decision D6: 1,000 groups")
	assert.Equal(t, 4, logStreamPageBudget, "decision D6: 200 streams")
	for name := range logLists(nil) {
		t.Run(name, func(t *testing.T) {
			budget := budgets[name]
			f := &fakeLogs{pages: 2*budget + 1}
			v := logLists(f)[name]()
			loadAll(v, v.Init())
			assert.Equal(t, budget, v.count(), "the first load stops at the budget")
			assert.Contains(t, v.Footer(), fmt.Sprintf("%d/%d log", budget, budget))
			assert.Contains(t, v.Footer(), "· l for more")

			_, cmd := v.Update(keyPress('l'))
			loadAll(v, cmd)
			assert.Equal(t, 2*budget, v.count(), "l loads one more budget")
			assert.Contains(t, v.Footer(), "· l for more")

			_, cmd = v.Update(keyPress('l'))
			loadAll(v, cmd)
			assert.Equal(t, 2*budget+1, v.count(), "the last l reaches the end")
			assert.NotContains(t, v.Footer(), "l for more")
			assert.Len(t, f.tokens, 2*budget+1, "each page is asked for once")
		})
	}
}

// H3, fixed for the log lists: a failure on a later page keeps the rows,
// sends the error to the status bar, and l asks for that page again.
func TestLogsH3_LogListsKeepRowsOnLaterFailure(t *testing.T) {
	for name := range logLists(nil) {
		t.Run(name, func(t *testing.T) {
			f := &fakeLogs{pages: 3, fail: map[string]bool{"t2": true}}
			v := logLists(f)[name]()
			seen := loadAll(v, v.Init())
			em, ok := firstOfType[msg.ErrorMsg](seen)
			require.True(t, ok, "the error goes to the status bar")
			assert.Contains(t, aws.ErrorText(em.Err), "throttled")

			out := plain(v.View().Content)
			assert.NotContains(t, out, "Error:", "no error screen")
			assert.Equal(t, 2, v.count(), "the rows of pages 1 and 2 stay")
			assert.Contains(t, v.Footer(), "· l for more")

			_, cmd := v.Update(keyPress('l'))
			loadAll(v, cmd)
			assert.Equal(t, 3, v.count())
			assert.Equal(t, []string{"", "t1", "t2!", "t2"}, f.tokens, "l asks for the failed page again")
		})
	}
}

// A failure of the first page still shows the error screen.
func TestLogLists_FirstPageFailureShowsErrorScreen(t *testing.T) {
	for name := range logLists(nil) {
		t.Run(name, func(t *testing.T) {
			f := &fakeLogs{pages: 2, fail: map[string]bool{"": true}}
			v := logLists(f)[name]()
			loadAll(v, v.Init())
			assert.Contains(t, plain(v.View().Content), "Error: throttled")
		})
	}
}

func TestLogStreams_AsksForItsGroup(t *testing.T) {
	f := &fakeLogs{pages: 1}
	v := NewLogStreams(f, "/aws/lambda/orders")
	loadAll(v, v.Init())
	assert.Equal(t, "/aws/lambda/orders", f.group)
	assert.Equal(t, "logs_streams:/aws/lambda/orders", v.ID())
	assert.Equal(t, "/aws/lambda/orders", v.Title())
}

func testLogGroups(t *testing.T, f *fakeLogs, groups ...aws.LogGroup) *LogGroups {
	t.Helper()
	v := NewLogGroups(f)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	deliverItems(v, &v.listCore, groups)
	return v
}

var (
	testGroupOrders = aws.LogGroup{
		Name: "/aws/lambda/orders", ARN: "arn:aws:logs:us-east-1:1:log-group:/aws/lambda/orders",
		Class: "STANDARD", RetentionDays: 14, StoredBytes: 3 * 1024 * 1024,
		Created: time.Date(2026, 3, 4, 10, 0, 0, 0, time.Local), MetricFilters: 2,
		DataProtection: "ACTIVATED", DeletionProtection: true,
	}
	testGroupWeb = aws.LogGroup{Name: "/ecs/web", Class: "INFREQUENT_ACCESS"}
)

func TestLogGroups_Rows(t *testing.T) {
	v := testLogGroups(t, nil, testGroupOrders, testGroupWeb)
	out := plain(v.View().Content)
	for _, s := range []string{"Name", "Class", "Retention", "Stored", "Created",
		"/aws/lambda/orders", "Standard", "14 days", "3.0 MB", "2026-03-04",
		"/ecs/web", "Infrequent", "never"} {
		assert.Contains(t, out, s)
	}
	assert.NotContains(t, out, "0001-01-01", "a zero creation time shows empty")

	v.Update(tea.WindowSizeMsg{Width: 50, Height: 30})
	out = plain(v.View().Content)
	assert.Contains(t, out, "Retention", "a narrow terminal keeps the retention")
	assert.NotContains(t, out, "Class")
	assert.NotContains(t, out, "Stored")
}

func TestLogGroups_RetentionText(t *testing.T) {
	assert.Equal(t, "never", logRetentionText(0))
	assert.Equal(t, "1 day", logRetentionText(1))
	assert.Equal(t, "365 days", logRetentionText(365))
	assert.Equal(t, "Delivery", logClassText("DELIVERY"))
	assert.Equal(t, "NEW_CLASS", logClassText("NEW_CLASS"))
}

// "never" sorts after every number of days, and the days sort as numbers.
func TestLogGroups_RetentionSortsAsNumbers(t *testing.T) {
	key := func(days int) string { return logGroupRow(aws.LogGroup{RetentionDays: days}, ui.TierMedium).Sort[2] }
	assert.Less(t, key(7), key(30))
	assert.Less(t, key(30), key(3653))
	assert.Less(t, key(3653), key(0), "never is the longest")
	size := func(b int64) string { return logGroupRow(aws.LogGroup{StoredBytes: b}, ui.TierMedium).Sort[3] }
	assert.Less(t, size(900), size(2048))
}

// v opens the streams of the cursor group.
func TestLogGroups_VOpensStreams(t *testing.T) {
	want := msg.NavigateMsg{ViewID: "logs_streams", Params: map[string]string{"log_group": "/aws/lambda/orders"}}
	v := testLogGroups(t, nil, testGroupOrders, testGroupWeb)
	_, cmd := v.Update(keyPress('v'))
	assert.Equal(t, []tea.Msg{want}, collectMsgs(cmd))
}

// Enter opens the events of the whole group (decision D1). The viewer gets
// the group record, so it needs no lookup.
func TestLogGroups_EnterOpensEventsWithTheRecord(t *testing.T) {
	v := testLogGroups(t, nil, testGroupOrders, testGroupWeb)
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, []tea.Msg{msg.NavigateMsg{ViewID: "logs_events", Params: map[string]string{
		"log_group":     "/aws/lambda/orders",
		"log_group_arn": testGroupOrders.ARN,
		"log_class":     "STANDARD",
		"log_retention": "14",
		"log_created":   strconv.FormatInt(testGroupOrders.Created.UnixMilli(), 10),
	}}}, collectMsgs(cmd))

	ev := NewLogEvents(nil, logGroupParams(testGroupOrders))
	require.NotNil(t, ev.group, "the viewer builds the record from the parameters")
	assert.Equal(t, testGroupOrders.ARN, ev.group.ARN)
	assert.Equal(t, 14, ev.group.RetentionDays)
	assert.True(t, testGroupOrders.Created.Equal(ev.group.Created))

	// A zero creation time sends no parameter.
	assert.NotContains(t, logGroupParams(testGroupWeb), "log_created")
}

// Enter on a stream opens its events, with the time of its first event.
func TestLogStreams_EnterOpensEvents(t *testing.T) {
	s := NewLogStreams(nil, "g")
	s.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	deliverItems(s, &s.listCore, []aws.LogStream{testStream, {Name: "new"}})
	_, cmd := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, []tea.Msg{msg.NavigateMsg{ViewID: "logs_events", Params: map[string]string{
		"log_group": "g", "log_stream": "web/abc",
		"stream_first": strconv.FormatInt(testStream.FirstEvent.UnixMilli(), 10),
	}}}, collectMsgs(cmd))

	s.Update(keyPress('j'))
	_, cmd = s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, []tea.Msg{msg.NavigateMsg{ViewID: "logs_events", Params: map[string]string{
		"log_group": "g", "log_stream": "new",
	}}}, collectMsgs(cmd), "a stream with no events sends no first time")

	s.Update(msg.ErrorMsg{Err: errors.New("denied")})
	_, cmd = s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Empty(t, navigations(collectMsgs(cmd)), "the error screen hides the rows")
}

// The keys do nothing while the filter is open, on the error screen, or
// in an empty list.
func TestLogGroups_StreamsKeyNeedsARow(t *testing.T) {
	v := testLogGroups(t, nil)
	_, cmd := v.Update(keyPress('v'))
	assert.Empty(t, navigations(collectMsgs(cmd)), "an empty list")

	v = testLogGroups(t, nil, testGroupOrders)
	v.Update(keyPress('/'))
	_, cmd = v.Update(keyPress('v'))
	assert.Empty(t, navigations(collectMsgs(cmd)), "v types into the filter")
	assert.Equal(t, "v", v.filter.Value())

	v = testLogGroups(t, nil, testGroupOrders)
	v.Update(msg.ErrorMsg{Err: errors.New("denied")}) // a bare error shows the error screen
	require.Error(t, v.err)
	_, cmd = v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Empty(t, navigations(collectMsgs(cmd)), "the error screen hides the rows")
}

func navigations(msgs []tea.Msg) []msg.NavigateMsg {
	var out []msg.NavigateMsg
	for _, m := range msgs {
		if n, ok := m.(msg.NavigateMsg); ok {
			out = append(out, n)
		}
	}
	return out
}

// d opens the detail with the Info tab from the row and the Tags tab from
// ListTagsForResource, which gets the ARN without ":*".
func TestLogGroups_DetailHasInfoAndTags(t *testing.T) {
	f := &fakeLogs{tags: map[string]string{"team": "orders"}}
	v := testLogGroups(t, f, testGroupOrders)
	_, cmd := v.Update(keyPress('d'))
	panel := detailPanel(t, v, cmd)

	assert.Equal(t, testGroupOrders.ARN, f.tagARN)
	assert.Equal(t, "/aws/lambda/orders", panel.PanelTitle)
	require.Len(t, panel.Tabs, 2)
	info := panel.Tabs[0].Content
	for _, s := range []string{"/aws/lambda/orders", testGroupOrders.ARN, "Standard", "14 days",
		"3.0 MB", "2026-03-04 10:00:00", "Metric filters   2", "none (AWS owned key)",
		"activated", "Delete protected Yes"} {
		assert.Contains(t, info, s)
	}
	assert.Equal(t, "Tags", panel.Tabs[1].Title)
	assert.Contains(t, panel.Tabs[1].Content, "team")
	assert.Contains(t, panel.Tabs[1].Content, "orders")
}

// Many read-only roles may not call ListTagsForResource. The panel still
// opens, and the Tags tab shows the error.
func TestLogGroups_DetailShowsTagError(t *testing.T) {
	f := &fakeLogs{tagErr: errors.New("AccessDeniedException: not allowed")}
	v := testLogGroups(t, f, testGroupWeb)
	_, cmd := v.Update(keyPress('d'))
	panel := detailPanel(t, v, cmd)
	require.Len(t, panel.Tabs, 2)
	assert.Contains(t, panel.Tabs[1].Content, "Tags failed:")
	assert.Contains(t, panel.Tabs[1].Content, "not allowed")

	f = &fakeLogs{}
	v = testLogGroups(t, f, testGroupWeb)
	_, cmd = v.Update(keyPress('d'))
	assert.Contains(t, detailPanel(t, v, cmd).Tabs[1].Content, "No tags")
}

func TestLogGroups_DetailWithoutClient(t *testing.T) {
	_, err := logGroupDetail(nil)(context.Background(), testGroupWeb)
	assert.ErrorIs(t, err, errNoClient)
}

// detailPanel runs the detail fetch of cmd through v and returns the panel.
func detailPanel(t *testing.T, v tea.Model, cmd tea.Cmd) msg.TabbedContentMsg {
	t.Helper()
	for _, m := range pump(v, cmd, 3) {
		if p, ok := m.(msg.TabbedContentMsg); ok {
			return p
		}
	}
	t.Fatal("no detail panel")
	return msg.TabbedContentMsg{}
}

func TestLogLists_YCopiesTheName(t *testing.T) {
	v := testLogGroups(t, nil, testGroupOrders)
	_, cmd := v.Update(keyPress('y'))
	ts := toasts(collectMsgs(cmd))
	require.Len(t, ts, 1)
	assert.Contains(t, ts[0].Text, "/aws/lambda/orders")

	s := NewLogStreams(nil, "g")
	s.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	deliverItems(s, &s.listCore, []aws.LogStream{{Name: "2026/11/20/[$LATEST]abc"}})
	_, cmd = s.Update(keyPress('y'))
	ts = toasts(collectMsgs(cmd))
	require.Len(t, ts, 1)
	assert.Contains(t, ts[0].Text, "2026/11/20/[$LATEST]abc")
}

func TestLogLists_KeyMaps(t *testing.T) {
	keys := func(hs []ui.HintBinding) []string {
		var out []string
		for _, h := range hs {
			out = append(out, h.Binding.Help().Key)
		}
		return out
	}
	assert.Equal(t, []string{"enter", "v", "d", "y", "l", "s/S", "/", "r"}, keys(NewLogGroups(nil).KeyMap()))
	assert.Equal(t, []string{"enter", "d", "y", "l", "s/S", "/", "r"}, keys(NewLogStreams(nil, "g").KeyMap()))
}

var testStream = aws.LogStream{
	Name: "web/abc", ARN: "arn:aws:logs:r:1:log-group:g:log-stream:web/abc",
	Created:    time.Date(2026, 11, 1, 9, 0, 0, 0, time.Local),
	FirstEvent: time.Date(2026, 11, 1, 9, 0, 1, 0, time.Local),
	LastEvent:  time.Date(2026, 11, 20, 13, 5, 0, 0, time.Local),
}

func TestLogStreams_Rows(t *testing.T) {
	s := NewLogStreams(nil, "g")
	s.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	deliverItems(s, &s.listCore, []aws.LogStream{testStream, {Name: "empty"}})
	out := plain(s.View().Content)
	for _, str := range []string{"Last event", "First event", "Created", "web/abc",
		"2026-11-20 13:05:00", "2026-11-01 09:00:01", "2026-11-01 09:00:00", "empty"} {
		assert.Contains(t, out, str)
	}
	assert.NotContains(t, out, "0001-01-01", "a zero time shows empty")

	s.Update(tea.WindowSizeMsg{Width: 50, Height: 30})
	out = plain(s.View().Content)
	assert.Contains(t, out, "Last event")
	assert.Contains(t, out, "2026-11-20 13:05:00", "a narrow terminal keeps the last event time")
	assert.NotContains(t, out, "First event")
	assert.NotContains(t, out, "2026-11-01 09:00:01")
}

// d opens the detail of a stream, which says that the last event time can
// be late.
func TestLogStreams_DetailNotesTheLag(t *testing.T) {
	s := NewLogStreams(nil, "g")
	s.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	deliverItems(s, &s.listCore, []aws.LogStream{testStream})
	_, cmd := s.Update(keyPress('d'))
	panel := detailPanel(t, s, cmd)
	assert.Equal(t, "web/abc", panel.PanelTitle)
	info := panel.Tabs[0].Content
	assert.Contains(t, info, testStream.ARN)
	assert.Contains(t, info, "2026-11-20 13:05:00 ("+logStreamLagNote+")")
	assert.NotContains(t, info, "Last ingestion", "a zero time is left out")
}

// While more streams exist, the footer says that the filter sees only the
// loaded streams. AWS takes no prefix with the order by the last event.
func TestLogStreams_FooterNotesTheLoadedFilter(t *testing.T) {
	const note = "filter acts on the loaded streams"
	f := &fakeLogs{pages: logStreamPageBudget + 1}
	s := NewLogStreams(f, "g")
	s.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	loadAll(s, s.Init())
	assert.Contains(t, s.Footer(), "· l for more · "+note)
	s.Update(keyPress('/'))
	s.Update(keyPress('s'))
	s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Contains(t, s.Footer(), note, "the filter has text")

	_, cmd := s.Update(keyPress('l'))
	loadAll(s, cmd)
	assert.NotContains(t, s.Footer(), note, "all streams loaded")
}

// A log group or stream name can hold any character. The rows show
// control characters as pictures.
func TestLogLists_ShowNamesSafely(t *testing.T) {
	g := testLogGroups(t, nil, aws.LogGroup{Name: "/g-" + dcs + csi8})
	out := g.View().Content
	assertSafeOutput(t, out)
	assert.Contains(t, ansi.Strip(out), "/g-␛P$qm␛\\�7m")

	s := NewLogStreams(nil, "g")
	s.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	deliverItems(s, &s.listCore, []aws.LogStream{{Name: "s-" + dcs}})
	out = s.View().Content
	assertSafeOutput(t, out)
	assert.Contains(t, ansi.Strip(out), "s-␛P$qm␛\\")
}
