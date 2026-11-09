package views

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// Characterization tests for sqs_queues (Phase 8, PR 0). They record what
// the view does today, through its public surface, so the port to the
// shared list core (PR 8) shows each change of behavior as a diff here.
// A line that records a bug says which row of the divergence table in
// docs/design/phase8-resource-list.md changes it.

// ─── Helpers ────────────────────────────────────────────────────────────────

const charSQSPrefix = "https://sqs.us-east-1.amazonaws.com/123456789012/"

func charSQSURL(name string) string { return charSQSPrefix + name }

// charSQSQueue returns a readable standard queue.
func charSQSQueue(name string, messages int) aws.Queue {
	return aws.Queue{
		URL: charSQSURL(name), Name: name, Type: "Standard",
		ARN: "arn:aws:sqs:us-east-1:123456789012:" + name, ApproximateMessageCount: messages,
	}
}

// charSQSDenied is an AWS error with the code AccessDenied.
var charSQSDenied = &smithy.GenericAPIError{Code: "AccessDenied", Message: "not allowed"}

func charSQSNew(width int) (*SQSQueues, *awstest.MockSQSService) {
	svc := new(awstest.MockSQSService)
	v := NewSQSQueues(svc)
	v.Update(tea.WindowSizeMsg{Width: width, Height: 20})
	return v, svc
}

func charSQSURLs(queues []aws.Queue) []string {
	urls := make([]string, len(queues))
	for i, q := range queues {
		urls[i] = q.URL
	}
	return urls
}

// charSQSExpectLoad sets up the mock for one full load: one page with the
// queue URLs, then the attributes of all queues.
func charSQSExpectLoad(svc *awstest.MockSQSService, queues ...aws.Queue) {
	urls := charSQSURLs(queues)
	svc.On("ListQueuesPage", mock.Anything, (*string)(nil)).
		Return(&aws.QueuePage{QueueURLs: urls}, nil).Once()
	if len(urls) > 0 {
		svc.On("FetchAllQueueAttributes", mock.Anything, urls).
			Return(slices.Clone(queues), nil).Once()
	}
}

// charSQSLoaded returns a view that finished its first load with queues.
func charSQSLoaded(t *testing.T, width int, queues ...aws.Queue) (*SQSQueues, *awstest.MockSQSService) {
	t.Helper()
	v, svc := charSQSNew(width)
	charSQSExpectLoad(svc, queues...)
	pump(v, v.Init(), 6)
	require.True(t, strings.HasPrefix(v.Footer(), fmt.Sprintf("%d/%d queues", len(queues), len(queues))), v.Footer())
	return v, svc
}

// charSQSThree loads qa (8 messages), qb (10) and qc (3) at width 100.
func charSQSThree(t *testing.T) (*SQSQueues, *awstest.MockSQSService) {
	t.Helper()
	return charSQSLoaded(t, 100, charSQSQueue("qa", 8), charSQSQueue("qb", 10), charSQSQueue("qc", 3))
}

// charSQSNoTicks drops spinner ticks.
func charSQSNoTicks(msgs []tea.Msg) []tea.Msg {
	return slices.DeleteFunc(slices.Clone(msgs), func(m tea.Msg) bool {
		_, tick := m.(spinner.TickMsg)
		return tick
	})
}

// charSQSRun runs cmd and returns the produced messages without spinner
// ticks. It does not deliver them.
func charSQSRun(cmd tea.Cmd) []tea.Msg { return charSQSNoTicks(collectMsgs(cmd)) }

// charSQSDeliver delivers msgs to v and returns the produced messages
// without spinner ticks.
func charSQSDeliver(v tea.Model, msgs ...tea.Msg) []tea.Msg {
	return charSQSNoTicks(deliverAll(v, msgs...))
}

// charSQSHeader returns the table header of the view, words joined by one
// space.
func charSQSHeader(v *SQSQueues) string {
	lines := strings.Split(plain(v.View().Content), "\n")
	return strings.Join(strings.Fields(lines[0]), " ")
}

// charSQSRows returns the table rows of the view, words joined by one space.
func charSQSRows(v *SQSQueues) []string {
	lines := strings.Split(plain(v.View().Content), "\n")
	var rows []string
	for _, l := range lines[2:] {
		if f := strings.Fields(l); len(f) > 0 {
			rows = append(rows, strings.Join(f, " "))
		}
	}
	return rows
}

// charSQSNames returns the first word of each row (the queue name).
func charSQSNames(v *SQSQueues) []string {
	var names []string
	for _, r := range charSQSRows(v) {
		names = append(names, strings.Fields(strings.TrimPrefix(r, "✓ "))[0])
	}
	return names
}

// charSQSCursorURL presses p and returns the queue URL of the peek request,
// that is the queue under the cursor. It returns "" when p does nothing.
func charSQSCursorURL(t *testing.T, v *SQSQueues) string {
	t.Helper()
	_, cmd := v.Update(keyPress('p'))
	if cmd == nil {
		return ""
	}
	nav, ok := cmd().(msg.NavigateMsg)
	require.True(t, ok)
	return nav.Params["queue_url"]
}

// charSQSClipboard returns the text of a tea.SetClipboard message.
func charSQSClipboard(msgs []tea.Msg) (string, bool) {
	for _, m := range msgs {
		if fmt.Sprintf("%T", m) == "tea.setClipboardMsg" {
			return reflect.ValueOf(m).String(), true
		}
	}
	return "", false
}

// charSQSTargets presses y and returns the copied URLs: the selected
// queues, or the cursor queue.
func charSQSTargets(t *testing.T, v *SQSQueues) []string {
	t.Helper()
	_, cmd := v.Update(keyPress('y'))
	if cmd == nil {
		return nil
	}
	text, ok := charSQSClipboard(collectMsgs(cmd))
	require.True(t, ok)
	return strings.Split(text, "\n")
}

func charSQSDown(v *SQSQueues) { v.Update(tea.KeyPressMsg{Code: tea.KeyDown}) }

func charSQSSpace(v *SQSQueues) { v.Update(keyPress(' ')) }

func charSQSReadWrite(t *testing.T) {
	t.Helper()
	prev := ui.IsReadOnly()
	t.Cleanup(func() { ui.SetReadOnly(prev) })
	ui.SetReadOnly(false)
}

// charSQSSortCol returns the index of the column title in the sort picker.
func charSQSSortCol(t *testing.T, v *SQSQueues, title string) int {
	t.Helper()
	_, cmd := v.Update(keyPress('s'))
	req, ok := cmd().(msg.RequestSortPickerMsg)
	require.True(t, ok)
	i := slices.Index(req.Columns, title)
	require.GreaterOrEqual(t, i, 0)
	return i
}

// ─── All views ──────────────────────────────────────────────────────────────

func TestCharSQS_IDAndTitle(t *testing.T) {
	v := NewSQSQueues(new(awstest.MockSQSService))
	assert.Equal(t, "sqs_queues", v.ID())
	assert.Equal(t, "SQS Queues", v.Title())
}

func TestCharSQS_KeyMap(t *testing.T) {
	prev := ui.IsReadOnly()
	t.Cleanup(func() { ui.SetReadOnly(prev) })

	type hint struct {
		Keys    []string
		Key     string
		Desc    string
		Mode    ui.HintMode
		Enabled bool
	}
	snapshot := func() []hint {
		var out []hint
		for _, h := range NewSQSQueues(new(awstest.MockSQSService)).KeyMap() {
			out = append(out, hint{h.Keys(), h.Help().Key, h.Help().Desc, h.Mode, h.Enabled()})
		}
		return out
	}

	ui.SetReadOnly(true)
	assert.Equal(t, []hint{
		{[]string{"enter"}, "enter/d", "details", ui.ModeAny, true},
		{[]string{"p"}, "p", "peek messages", ui.ModeAny, true},
		{[]string{"m"}, "m", "manage", ui.ModeReadWrite, false},
		{[]string{"y"}, "y", "copy URL", ui.ModeAny, true},
		{[]string{"space"}, "space", "select", ui.ModeAny, true},
		{[]string{"s"}, "s/S", "sort", ui.ModeAny, true},
		{[]string{"/"}, "/", "filter", ui.ModeAny, true},
		{[]string{"r"}, "r", "refresh", ui.ModeAny, true},
	}, snapshot())

	ui.SetReadOnly(false)
	assert.Equal(t, []hint{
		{[]string{"enter"}, "enter/d", "details", ui.ModeAny, true},
		{[]string{"p"}, "p", "peek messages", ui.ModeAny, true},
		{[]string{"m"}, "m", "manage", ui.ModeReadWrite, true},
		{[]string{"y"}, "y", "copy URL", ui.ModeAny, true},
		{[]string{"space"}, "space", "select", ui.ModeAny, true},
		{[]string{"s"}, "s/S", "sort", ui.ModeAny, true},
		{[]string{"/"}, "/", "filter", ui.ModeAny, true},
		{[]string{"r"}, "r", "refresh", ui.ModeAny, true},
	}, snapshot())
	// esc, d and S work but have no hint of their own.
}

func TestCharSQS_CapturingInputWhileFilterOpen(t *testing.T) {
	v, svc := charSQSThree(t)
	assert.False(t, v.CapturingInput())

	v.Update(keyPress('/'))
	assert.True(t, v.CapturingInput())
	assert.Contains(t, strings.Split(plain(v.View().Content), "\n")[0], "/ filter...")

	// Typed letters go to the filter, not to the keys: r does not reload.
	_, cmd := v.Update(keyPress('r'))
	changed, ok := firstOfType[ui.FilterChangedMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "r", changed.Text)
	svc.AssertNumberOfCalls(t, "ListQueuesPage", 1)

	_, cmd = v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.False(t, v.CapturingInput())
	changed, ok = firstOfType[ui.FilterChangedMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Empty(t, changed.Text)
}

func TestCharSQS_InitDeselectsAndLoadsOnlyWhileLoading(t *testing.T) {
	v, svc := charSQSThree(t)
	charSQSSpace(v)
	require.Equal(t, "3/3 queues  (1 selected)", v.Footer())

	// The app calls Init again when the user comes back from sqs_messages.
	assert.Nil(t, v.Init(), "a loaded view does not reload")
	assert.Equal(t, "3/3 queues", v.Footer(), "Init deselects")
	svc.AssertNumberOfCalls(t, "ListQueuesPage", 1)
	svc.AssertNumberOfCalls(t, "FetchAllQueueAttributes", 1)
}

// A fresh view has load generation 0, so a gen-0 message is accepted.
func TestCharSQS_FreshViewAcceptsGenZero(t *testing.T) {
	v, svc := charSQSNew(100)
	v.Update(sqsAllAttrsLoadedMsg{queues: []aws.Queue{charSQSQueue("qa", 1)}}) // private
	assert.Equal(t, []string{"qa Standard 1 0 0"}, charSQSRows(v))

	v2, svc2 := charSQSNew(100)
	svc2.On("FetchAllQueueAttributes", mock.Anything, []string{charSQSURL("qa")}).
		Return([]aws.Queue{charSQSQueue("qa", 2)}, nil).Once()
	_, cmd := v2.Update(sqsQueuesPageMsg{urls: []string{charSQSURL("qa")}, pageNum: 1}) // private
	pump(v2, cmd, 3)
	assert.Equal(t, []string{"qa Standard 2 0 0"}, charSQSRows(v2))
	svc.AssertExpectations(t)
	svc2.AssertExpectations(t)
}

func TestCharSQS_FirstLoadPhasesAndSnapshots(t *testing.T) {
	v, svc := charSQSNew(100)
	charSQSExpectLoad(svc, charSQSQueue("qa", 3), charSQSQueue("qb", 0))

	assert.Equal(t, "\n  ⣾  Loading SQS queues...", plain(v.View().Content))
	assert.Equal(t, "0/0 queues  ⣾  Loading SQS queues...", plain(v.Footer()))

	pages := charSQSRun(v.Init())
	require.Len(t, pages, 1)
	attrs := charSQSDeliver(v, pages...)
	require.Len(t, attrs, 1)

	// Phase 1 done: the URLs are known, but no row shows before the
	// attributes arrive.
	assert.Equal(t, "\n  ⣾  Loading attributes for 2 queues...", plain(v.View().Content))
	assert.Equal(t, "0/0 queues  ⣾  Loading attributes for 2 queues...", plain(v.Footer()))
	svc.AssertNumberOfCalls(t, "FetchAllQueueAttributes", 1)

	assert.Empty(t, charSQSDeliver(v, attrs...))
	assert.Equal(t, "Queue Name Type Messages In-Flight Delayed", charSQSHeader(v))
	assert.Equal(t, []string{"qa Standard 3 0 0", "qb Standard 0 0 0"}, charSQSRows(v))
	assert.Equal(t, "2/2 queues", plain(v.Footer()))
	assert.Nil(t, v.Init(), "the load ended")
}

func TestCharSQS_MultiPageLoad(t *testing.T) {
	v, svc := charSQSNew(100)
	token := "t1"
	svc.On("ListQueuesPage", mock.Anything, (*string)(nil)).
		Return(&aws.QueuePage{QueueURLs: []string{charSQSURL("qa")}, HasMorePages: true, Token: &token}, nil).Once()
	svc.On("ListQueuesPage", mock.Anything, &token).
		Return(&aws.QueuePage{QueueURLs: []string{charSQSURL("qb")}}, nil).Once()
	svc.On("FetchAllQueueAttributes", mock.Anything, []string{charSQSURL("qa"), charSQSURL("qb")}).
		Return([]aws.Queue{charSQSQueue("qa", 1), charSQSQueue("qb", 2)}, nil).Once()

	page2 := charSQSDeliver(v, charSQSRun(v.Init())...)
	require.Len(t, page2, 1)
	assert.Equal(t, "\n  ⣾  Loading queues... 1 so far", plain(v.View().Content))
	assert.Equal(t, "0/0 queues  ⣾  Loading queues... 1 so far", plain(v.Footer()))

	pump(v, func() tea.Msg { return page2[0] }, 4)
	assert.Equal(t, []string{"qa Standard 1 0 0", "qb Standard 2 0 0"}, charSQSRows(v))
	svc.AssertExpectations(t)
}

func TestCharSQS_ReloadSnapshot(t *testing.T) {
	v, svc := charSQSLoaded(t, 100, charSQSQueue("qa", 3), charSQSQueue("qb", 0))
	charSQSExpectLoad(svc, charSQSQueue("qb", 5), charSQSQueue("qa", 4))

	_, cmd := v.Update(keyPress('r'))
	// Divergence #7: changes in PR 8. The reload clears the items, so the
	// full-screen spinner hides the rows that the table still holds.
	assert.Equal(t, "\n  ⣾  Loading SQS queues...", plain(v.View().Content))
	assert.Equal(t, "2/2 queues  (loading... 2 so far)  ⣾  Loading SQS queues...", plain(v.Footer()))

	pump(v, cmd, 6)
	assert.Equal(t, []string{"qb Standard 5 0 0", "qa Standard 4 0 0"}, charSQSRows(v))
	assert.Equal(t, "2/2 queues", plain(v.Footer()))
	svc.AssertNumberOfCalls(t, "ListQueuesPage", 2)
	svc.AssertNumberOfCalls(t, "FetchAllQueueAttributes", 2)
}

func TestCharSQS_StalePageDropped(t *testing.T) {
	v, svc := charSQSNew(100)
	svc.On("ListQueuesPage", mock.Anything, (*string)(nil)).
		Return(&aws.QueuePage{QueueURLs: []string{charSQSURL("qa"), charSQSURL("qb")}}, nil)
	svc.On("FetchAllQueueAttributes", mock.Anything, []string{charSQSURL("qa"), charSQSURL("qb")}).
		Return([]aws.Queue{charSQSQueue("qa", 1), charSQSQueue("qb", 2)}, nil)

	old := charSQSRun(v.Init())
	_, cmd := v.Update(keyPress('r'))
	cur := charSQSRun(cmd)
	require.Len(t, old, 1)
	require.Len(t, cur, 1)

	assert.Empty(t, charSQSDeliver(v, old...), "a page of the older load starts no attribute fetch")
	attrs := charSQSDeliver(v, cur...)
	require.Len(t, attrs, 1)
	charSQSDeliver(v, attrs...)

	assert.Equal(t, []string{"qa Standard 1 0 0", "qb Standard 2 0 0"}, charSQSRows(v))
	svc.AssertNumberOfCalls(t, "FetchAllQueueAttributes", 1)
}

// Two presses of r while the first load runs: only the last load fetches
// attributes, once.
func TestCharSQS_DoubleRefreshFetchesAttributesOnce(t *testing.T) {
	v, svc := charSQSNew(100)
	urls := []string{charSQSURL("qa")}
	svc.On("ListQueuesPage", mock.Anything, (*string)(nil)).Return(&aws.QueuePage{QueueURLs: urls}, nil)
	svc.On("FetchAllQueueAttributes", mock.Anything, urls).Return([]aws.Queue{charSQSQueue("qa", 1)}, nil)

	first := charSQSRun(v.Init())
	_, cmd := v.Update(keyPress('r'))
	second := charSQSRun(cmd)
	_, cmd = v.Update(keyPress('r'))
	third := charSQSRun(cmd)

	seen := charSQSDeliver(v, slices.Concat(first, second, third)...)
	require.Len(t, seen, 1, "only the page of the last load starts an attribute fetch")
	charSQSDeliver(v, seen...)
	assert.Equal(t, []string{"qa Standard 1 0 0"}, charSQSRows(v))
	svc.AssertNumberOfCalls(t, "ListQueuesPage", 3)
	svc.AssertNumberOfCalls(t, "FetchAllQueueAttributes", 1)
}

func TestCharSQS_StaleAttributesDropped(t *testing.T) {
	v, svc := charSQSNew(100)
	urls := []string{charSQSURL("qa")}
	svc.On("ListQueuesPage", mock.Anything, (*string)(nil)).Return(&aws.QueuePage{QueueURLs: urls}, nil)
	svc.On("FetchAllQueueAttributes", mock.Anything, urls).
		Return([]aws.Queue{charSQSQueue("qa", 1)}, nil).Once()
	svc.On("FetchAllQueueAttributes", mock.Anything, urls).
		Return([]aws.Queue{charSQSQueue("qa", 2)}, nil).Once()

	oldAttrs := charSQSDeliver(v, charSQSRun(v.Init())...)
	_, cmd := v.Update(keyPress('r'))
	newAttrs := charSQSDeliver(v, charSQSRun(cmd)...)
	require.Len(t, oldAttrs, 1)
	require.Len(t, newAttrs, 1)

	charSQSDeliver(v, newAttrs...)
	charSQSDeliver(v, oldAttrs...) // arrives last, from the older load
	assert.Equal(t, []string{"qa Standard 2 0 0"}, charSQSRows(v))
}

func TestCharSQS_ErrorContexts(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		v, svc := charSQSNew(100)
		svc.On("ListQueuesPage", mock.Anything, (*string)(nil)).Return(nil, errors.New("boom"))
		got, ok := firstOfType[msg.ErrorMsg](charSQSRun(v.Init()))
		require.True(t, ok)
		assert.Equal(t, "listing SQS queues", got.Context)
	})
	t.Run("attributes", func(t *testing.T) {
		v, svc := charSQSNew(100)
		svc.On("ListQueuesPage", mock.Anything, (*string)(nil)).
			Return(&aws.QueuePage{QueueURLs: []string{charSQSURL("qa")}}, nil)
		svc.On("FetchAllQueueAttributes", mock.Anything, mock.Anything).Return(nil, errors.New("boom"))
		got, ok := firstOfType[msg.ErrorMsg](charSQSDeliver(v, charSQSRun(v.Init())...))
		require.True(t, ok)
		assert.Equal(t, "fetching SQS queue attributes", got.Context)
	})
}

func TestCharSQS_ErrorViewSnapshot(t *testing.T) {
	t.Cleanup(func() { ui.ActiveTheme = ui.DefaultTheme; ui.RebuildStyles() })
	ui.ActiveTheme = ui.CatppuccinLatte
	ui.RebuildStyles()

	v, svc := charSQSNew(100)
	svc.On("ListQueuesPage", mock.Anything, (*string)(nil)).Return(nil, errors.New("bad\x1b[31mname"))
	pump(v, v.Init(), 3)

	out := v.View().Content
	assert.Equal(t, "\n  Error: bad␛[31mname", plain(out), "sanitized")
	assert.Contains(t, out, fgSGR(ui.CatppuccinLatte.Error), "theme red")
	assert.Equal(t, "0/0 queues", plain(v.Footer()))
	assert.Nil(t, v.Init(), "an error ends the load")
}

func TestCharSQS_ErrorDuringReload(t *testing.T) {
	v, svc := charSQSLoaded(t, 100, charSQSQueue("qa", 1), charSQSQueue("qb", 2))
	svc.On("ListQueuesPage", mock.Anything, (*string)(nil)).Return(nil, errors.New("boom")).Once()
	_, cmd := v.Update(keyPress('r'))
	pump(v, cmd, 3)
	assert.Equal(t, "\n  Error: boom", plain(v.View().Content))
	assert.Equal(t, "2/2 queues", plain(v.Footer()), "the table keeps the old rows")

	// Divergence #8: changes in PR 8. Only r clears the error.
	charSQSExpectLoad(svc, charSQSQueue("qa", 1))
	_, cmd = v.Update(keyPress('r'))
	pump(v, cmd, 6)
	assert.Equal(t, []string{"qa Standard 1 0 0"}, charSQSRows(v))
}

// Divergence #9: changes in PR 8. A failure of a superseded load still
// replaces the list with the error.
func TestCharSQS_StaleErrorApplied(t *testing.T) {
	v, svc := charSQSNew(100)
	svc.On("ListQueuesPage", mock.Anything, (*string)(nil)).Return(nil, errors.New("old failure")).Once()
	charSQSExpectLoad(svc, charSQSQueue("qa", 1))

	stale := charSQSRun(v.Init())
	_, cmd := v.Update(keyPress('r'))
	pump(v, cmd, 6)
	require.Equal(t, []string{"qa Standard 1 0 0"}, charSQSRows(v))

	charSQSDeliver(v, stale...)
	assert.Equal(t, "\n  Error: old failure", plain(v.View().Content))
}

func TestCharSQS_ColumnsPerWidth(t *testing.T) {
	q := charSQSQueue("qa", 3)
	q.CreatedTimestamp = time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	cases := []struct {
		width  int
		header string
		row    string
	}{
		{60, "Queue Name Messages", "qa 3"},
		{100, "Queue Name Type Messages In-Flight Delayed", "qa Standard 3 0 0"},
		{160, "Queue Name Type Messages In-Flight Delayed DLQ Created", "qa Standard 3 0 0 2026-01-02"},
		{200, "Queue Name Type Messages In-Flight Delayed DLQ Created", "qa Standard 3 0 0 2026-01-02"},
	}
	for _, c := range cases {
		t.Run(fmt.Sprint(c.width), func(t *testing.T) {
			v, _ := charSQSLoaded(t, c.width, q)
			assert.Equal(t, c.header, charSQSHeader(v))
			assert.Equal(t, []string{c.row}, charSQSRows(v))
		})
	}

	// A resize that changes the tier rebuilds the rows.
	v, _ := charSQSLoaded(t, 160, q)
	v.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	assert.Equal(t, "Queue Name Messages", charSQSHeader(v))
	assert.Equal(t, []string{"qa 3"}, charSQSRows(v))
}

func TestCharSQS_SortPicker(t *testing.T) {
	v, _ := charSQSThree(t)

	_, cmd := v.Update(keyPress('s'))
	req, ok := cmd().(msg.RequestSortPickerMsg)
	require.True(t, ok)
	assert.Equal(t, msg.RequestSortPickerMsg{
		Columns:    []string{"Queue Name", "Type", "Messages", "In-Flight", "Delayed"},
		CurrentCol: -1,
	}, req)

	// Counts sort as numbers: 3, 8, 10.
	v.Update(ui.PickerResultMsg{ID: "sort", Selected: 2, Value: "Messages"})
	assert.Equal(t, []string{"qc", "qa", "qb"}, charSQSNames(v))
	assert.Equal(t, 2, charSQSSortColCurrent(t, v))

	v.Update(keyPress('S'))
	assert.Equal(t, []string{"qb", "qa", "qc"}, charSQSNames(v))

	v.Update(ui.PickerResultMsg{ID: "sort", Selected: -1})
	assert.Equal(t, []string{"qb", "qa", "qc"}, charSQSNames(v), "a cancelled picker keeps the sort")

	v.Update(ui.PickerResultMsg{ID: "sort", Selected: 0, Value: "_clear"})
	assert.Equal(t, []string{"qa", "qb", "qc"}, charSQSNames(v))
	assert.Equal(t, -1, charSQSSortColCurrent(t, v))
}

func charSQSSortColCurrent(t *testing.T, v *SQSQueues) int {
	t.Helper()
	_, cmd := v.Update(keyPress('s'))
	req, ok := cmd().(msg.RequestSortPickerMsg)
	require.True(t, ok)
	return req.CurrentCol
}

func TestCharSQS_SortSurvivesTierChangeAndReload(t *testing.T) {
	v, svc := charSQSLoaded(t, 160, charSQSQueue("qa", 3), charSQSQueue("qb", 1), charSQSQueue("qc", 2))
	v.Update(ui.PickerResultMsg{ID: "sort", Selected: charSQSSortCol(t, v, "Messages"), Value: "Messages"})
	require.Equal(t, []string{"qb", "qc", "qa"}, charSQSNames(v))

	v.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	assert.Equal(t, []string{"qb 1", "qc 2", "qa 3"}, charSQSRows(v))
	assert.Equal(t, 1, charSQSSortColCurrent(t, v), "Messages in the narrow tier")

	charSQSExpectLoad(svc, charSQSQueue("qa", 0), charSQSQueue("qb", 9), charSQSQueue("qc", 5))
	_, cmd := v.Update(keyPress('r'))
	pump(v, cmd, 6)
	assert.Equal(t, []string{"qa 0", "qc 5", "qb 9"}, charSQSRows(v))
}

func TestCharSQS_Footer(t *testing.T) {
	v, _ := charSQSNew(100)
	assert.Equal(t, "0/0 queues  ⣾  Loading SQS queues...", plain(v.Footer()))

	v, _ = charSQSThree(t)
	assert.Equal(t, "3/3 queues", v.Footer())
	charSQSSpace(v)
	assert.Equal(t, "3/3 queues  (1 selected)", v.Footer())
	v.Update(ui.FilterChangedMsg{Text: "qb"})
	assert.Equal(t, "1/3 queues", v.Footer(), "the filter drops the hidden selection")

	// An empty list.
	v, _ = charSQSLoaded(t, 100)
	assert.Equal(t, "0/0 queues", v.Footer())
	assert.Equal(t, "Queue Name Type Messages In-Flight Delayed", charSQSHeader(v))
	assert.Empty(t, charSQSRows(v))
}

func TestCharSQS_CountCellsArePlainAndColumnColored(t *testing.T) {
	v, _ := charSQSLoaded(t, 160, charSQSQueue("qa", 8))
	out := v.View().Content
	assert.Contains(t, out, ui.CountColor("8"), "the column style colors the count")
	assert.Equal(t, []string{"qa", "Standard", "8", "0", "0", "", ""},
		[]string(v.table.SelectedRow()), "the cells hold plain text") // private
}

func TestCharSQS_EscWithAndWithoutSelection(t *testing.T) {
	v, _ := charSQSThree(t)
	charSQSSpace(v)
	// Divergence #1: sqs keeps MultiSelect, so esc still deselects first
	// after PR 8.
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Nil(t, cmd, "the first esc deselects")
	assert.Equal(t, "3/3 queues", v.Footer())

	_, cmd = v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.NotNil(t, cmd)
	assert.Equal(t, msg.NavigateBackMsg{}, cmd())
}

func TestCharSQS_CopyToast(t *testing.T) {
	v, _ := charSQSThree(t)

	_, cmd := v.Update(keyPress('y'))
	msgs := collectMsgs(cmd)
	text, ok := charSQSClipboard(msgs)
	require.True(t, ok)
	assert.Equal(t, charSQSURL("qa"), text, "the cursor queue")
	assert.Equal(t, []msg.ToastMsg{msg.ToastSuccess("Copied 1 queue URL(s)")}, toasts(msgs))

	charSQSSpace(v)
	charSQSDown(v)
	charSQSDown(v)
	charSQSSpace(v)
	_, cmd = v.Update(keyPress('y'))
	msgs = collectMsgs(cmd)
	text, ok = charSQSClipboard(msgs)
	require.True(t, ok)
	assert.Equal(t, charSQSURL("qa")+"\n"+charSQSURL("qc"), text)
	assert.Equal(t, []msg.ToastMsg{msg.ToastSuccess("Copied 2 queue URL(s)")}, toasts(msgs))

	empty, _ := charSQSLoaded(t, 100)
	_, cmd = empty.Update(keyPress('y'))
	assert.Nil(t, cmd, "nothing to copy")
}

// C3: the table keeps cursor, selection and sort by key when its rows are
// set again.
func TestCharSQS_CursorSelectionSortKeptAcrossSetRows(t *testing.T) {
	v, svc := charSQSThree(t)
	v.Update(ui.PickerResultMsg{ID: "sort", Selected: 2, Value: "Messages"}) // qc 3, qa 8, qb 10
	require.Equal(t, charSQSURL("qa"), charSQSCursorURL(t, v), "the cursor follows qa")
	charSQSSpace(v)
	require.Equal(t, []string{charSQSURL("qa")}, charSQSTargets(t, v))

	// The selective refresh of qa moves it to the end by its new count.
	svc.On("GetQueueAttributes", mock.Anything, charSQSURL("qa")).
		Return(&aws.Queue{URL: charSQSURL("qa"), Name: "qa", Type: "Standard", ApproximateMessageCount: 20}, nil)
	_, cmd := v.Update(keyPress('r'))
	pump(v, cmd, 3)

	assert.Equal(t, []string{"qc", "qb", "qa"}, charSQSNames(v))
	assert.Equal(t, "3/3 queues  (1 selected)", v.Footer())
	assert.Equal(t, []string{charSQSURL("qa")}, charSQSTargets(t, v))
	assert.Equal(t, charSQSURL("qa"), charSQSCursorURL(t, v))
}

func TestCharSQS_CursorKeptAcrossFullReload(t *testing.T) {
	v, svc := charSQSThree(t)
	charSQSDown(v) // qb
	charSQSExpectLoad(svc, charSQSQueue("qc", 3), charSQSQueue("qb", 10), charSQSQueue("qa", 8))
	_, cmd := v.Update(keyPress('r'))
	pump(v, cmd, 6)
	assert.Equal(t, []string{"qc", "qb", "qa"}, charSQSNames(v))
	assert.Equal(t, charSQSURL("qb"), charSQSCursorURL(t, v))
}

// C4: the filter deselects the rows it hides. M8: the selection marker never
// reaches the row key.
func TestCharSQS_FilterDeselectsHiddenRowsAndMarkerNeverLeaks(t *testing.T) {
	v, _ := charSQSThree(t)
	charSQSSpace(v) // qa
	charSQSDown(v)
	charSQSSpace(v) // qb
	require.Equal(t, "3/3 queues  (2 selected)", v.Footer())
	assert.Equal(t, charSQSURL("qb"), charSQSCursorURL(t, v), "a selected row keeps its key")

	v.Update(ui.FilterChangedMsg{Text: "qb"})
	assert.Equal(t, "1/3 queues  (1 selected)", v.Footer())
	assert.Equal(t, []string{charSQSURL("qb")}, charSQSTargets(t, v))

	v.Update(ui.FilterChangedMsg{Text: ""})
	assert.Equal(t, "3/3 queues  (1 selected)", v.Footer(), "qa stays deselected")
}

func TestCharSQS_FocusWhileLoadingWaitsForBothPhases(t *testing.T) {
	v, svc := charSQSNew(100)
	charSQSExpectLoad(svc, charSQSQueue("qa", 1), charSQSQueue("qb", 2))
	svc.On("GetQueueAttributes", mock.Anything, charSQSURL("qb")).Return(&aws.Queue{URL: charSQSURL("qb"), Name: "qb"}, nil)
	svc.On("ListDeadLetterSourceQueues", mock.Anything, charSQSURL("qb")).Return([]string(nil), nil)

	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "qa"})
	assert.Nil(t, cmd, "phase 1: stored")
	attrs := charSQSDeliver(v, charSQSRun(v.Init())...)
	require.Len(t, attrs, 1)

	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: "qb"})
	assert.Nil(t, cmd, "phase 2: stored, the last focus wins")

	out := charSQSDeliver(v, charSQSDeliver(v, attrs...)...)
	panel, ok := firstOfType[msg.TabbedContentMsg](out)
	require.True(t, ok)
	assert.Equal(t, "qb", panel.PanelTitle)
	assert.Equal(t, charSQSURL("qb"), charSQSCursorURL(t, v), "focus by name, cursor by URL")
	svc.AssertNotCalled(t, "GetQueueAttributes", mock.Anything, charSQSURL("qa"))
}

func TestCharSQS_FocusLoaded(t *testing.T) {
	v, svc := charSQSThree(t)
	svc.On("GetQueueAttributes", mock.Anything, charSQSURL("qc")).Return(&aws.Queue{URL: charSQSURL("qc"), Name: "qc"}, nil)
	svc.On("ListDeadLetterSourceQueues", mock.Anything, charSQSURL("qc")).Return([]string(nil), nil)

	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "qc"})
	assert.Equal(t, charSQSURL("qc"), charSQSCursorURL(t, v))
	panel, ok := firstOfType[msg.TabbedContentMsg](charSQSDeliver(v, charSQSRun(cmd)...))
	require.True(t, ok)
	assert.Equal(t, "qc", panel.PanelTitle)

	// A name that is not in the list does nothing.
	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: "missing"})
	assert.Nil(t, cmd)
	assert.Equal(t, charSQSURL("qc"), charSQSCursorURL(t, v))
	// A URL is not a name.
	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: charSQSURL("qa")})
	assert.Nil(t, cmd)
}

// Divergence #22 (kept): a focus on a row that the filter hides opens the
// panel, and the cursor stays.
func TestCharSQS_FocusOnFilteredRow(t *testing.T) {
	v, svc := charSQSThree(t)
	svc.On("GetQueueAttributes", mock.Anything, charSQSURL("qb")).Return(&aws.Queue{URL: charSQSURL("qb"), Name: "qb"}, nil)
	svc.On("ListDeadLetterSourceQueues", mock.Anything, charSQSURL("qb")).Return([]string(nil), nil)
	v.Update(ui.FilterChangedMsg{Text: "qa"})

	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "qb"})
	panel, ok := firstOfType[msg.TabbedContentMsg](charSQSDeliver(v, charSQSRun(cmd)...))
	require.True(t, ok)
	assert.Equal(t, "qb", panel.PanelTitle)
	assert.Equal(t, charSQSURL("qa"), charSQSCursorURL(t, v))
}

// Divergence #51 (kept): a stored focus survives an ErrorMsg.
func TestCharSQS_PendingFocusSurvivesError(t *testing.T) {
	v, svc := charSQSNew(100)
	svc.On("ListQueuesPage", mock.Anything, (*string)(nil)).Return(nil, errors.New("boom")).Once()
	charSQSExpectLoad(svc, charSQSQueue("qa", 1))
	svc.On("GetQueueAttributes", mock.Anything, charSQSURL("qa")).Return(&aws.Queue{URL: charSQSURL("qa"), Name: "qa"}, nil)
	svc.On("ListDeadLetterSourceQueues", mock.Anything, charSQSURL("qa")).Return([]string(nil), nil)

	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "qa"})
	require.Nil(t, cmd)
	pump(v, v.Init(), 3)
	require.Contains(t, plain(v.View().Content), "Error: boom")

	_, cmd = v.Update(keyPress('r'))
	seen := pump(v, cmd, 6)
	panel, ok := firstOfType[msg.TabbedContentMsg](seen)
	require.True(t, ok, "the stored focus opens after the next load")
	assert.Equal(t, "qa", panel.PanelTitle)
}

func TestCharSQS_UpdateReturnsSamePointer(t *testing.T) {
	charSQSReadWrite(t)
	v, svc := charSQSNew(100)
	charSQSExpectLoad(svc, charSQSQueue("qa", 1), charSQSQueue("qb", 2))
	svc.On("GetQueueAttributes", mock.Anything, mock.Anything).Return(&aws.Queue{URL: charSQSURL("qa"), Name: "qa"}, nil)
	svc.On("ListDeadLetterSourceQueues", mock.Anything, mock.Anything).Return([]string(nil), nil)
	svc.On("PurgeQueue", mock.Anything, mock.Anything).Return(nil)
	svc.On("DeleteQueue", mock.Anything, mock.Anything).Return(nil)
	svc.On("SendMessage", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	var all []tea.Msg
	check := func(m tea.Msg) {
		t.Helper()
		got, cmd := v.Update(m)
		require.Same(t, v, got, "%T", m)
		all = append(all, charSQSRun(cmd)...)
	}
	check(tea.WindowSizeMsg{Width: 160, Height: 20})
	check(msg.FocusResourceMsg{ResourceID: "qa"})
	for _, m := range charSQSRun(v.Init()) {
		check(m)
	}
	for len(all) > 0 {
		batch := all
		all = nil
		for _, m := range batch {
			check(m)
		}
	}
	for _, m := range []tea.Msg{
		keyPress('d'), tea.KeyPressMsg{Code: tea.KeyEnter}, keyPress('p'), keyPress('y'),
		keyPress('s'), keyPress('S'), keyPress(' '), keyPress('r'), tea.KeyPressMsg{Code: tea.KeyEscape},
		tea.KeyPressMsg{Code: tea.KeyDown},
		ui.PickerResultMsg{ID: "sort", Selected: 0, Value: "Queue Name"},
		ui.FilterChangedMsg{Text: "q"}, ui.FilterChangedMsg{},
		keyPress('m'),
		ui.PickerResultMsg{ID: "action", Selected: 0, Value: "Send Message"},
		msg.FormResultMsg{ID: "sqs_send_message", Values: map[string]string{"body": "x"}},
		keyPress('m'),
		ui.PickerResultMsg{ID: "action", Selected: 1, Value: "Purge Queue"},
		ui.ConfirmResultMsg{Confirmed: true, Action: "sqs_purge"},
		keyPress('m'),
		ui.PickerResultMsg{ID: "action", Selected: 2, Value: "Delete Queue"},
		ui.ConfirmResultMsg{Confirmed: false, Action: "sqs_delete"},
		msg.ErrorMsg{Err: errors.New("x"), Context: "listing SQS queues"},
		keyPress('/'), keyPress('a'), tea.KeyPressMsg{Code: tea.KeyEscape},
	} {
		check(m)
	}
	for len(all) > 0 {
		batch := all
		all = nil
		for _, m := range batch {
			check(m)
		}
	}
}

// ─── sqs_queues ─────────────────────────────────────────────────────────────

func TestCharSQS_UnreadableQueue(t *testing.T) {
	v, svc := charSQSNew(100)
	queues := []aws.Queue{
		charSQSQueue("qa", 4),
		{URL: charSQSURL("qb"), Name: "qb", Err: charSQSDenied},
		{URL: charSQSURL("qc"), Name: "qc", Err: errors.New("boom")},
	}
	charSQSExpectLoad(svc, queues...)
	seen := pump(v, v.Init(), 6)

	assert.Equal(t, []string{"qa Standard 4 0 0", "qb unknown ? ? ?", "qc unknown ? ? ?"}, charSQSRows(v))
	assert.Equal(t, []msg.ToastMsg{
		msg.ToastError("Could not read 2 queue(s): qb (AccessDenied), qc (boom). Their counts show ?."),
	}, toasts(seen))

	// Unknown counts sort before every number.
	v.Update(ui.PickerResultMsg{ID: "sort", Selected: 2, Value: "Messages"})
	assert.Equal(t, []string{"qb", "qc", "qa"}, charSQSNames(v))
}

func TestCharSQS_NoToastWhenAllReadable(t *testing.T) {
	v, svc := charSQSNew(100)
	charSQSExpectLoad(svc, charSQSQueue("qa", 1))
	assert.Empty(t, toasts(pump(v, v.Init(), 6)))
}

func TestCharSQS_DLQMarking(t *testing.T) {
	src := charSQSQueue("src", 0)
	src.RedrivePolicy = &aws.RedrivePolicy{DeadLetterTargetArn: "arn:aws:sqs:us-east-1:123456789012:dlq", MaxReceiveCount: 3}
	dlq := charSQSQueue("dlq", 0)
	allow := charSQSQueue("allow", 0)
	allow.RedriveAllowPolicy = &aws.RedriveAllowPolicy{RedrivePermission: "allowAll"}
	plainQ := charSQSQueue("plain", 0)

	v, _ := charSQSLoaded(t, 160, src, dlq, allow, plainQ)
	assert.Equal(t, []string{
		"src Standard 0 0 0",
		"dlq Standard 0 0 0 Yes",
		"allow Standard 0 0 0 Yes",
		"plain Standard 0 0 0",
	}, charSQSRows(v))
}

func TestCharSQS_ManagePicker(t *testing.T) {
	v, _ := charSQSThree(t)

	_, cmd := v.Update(keyPress('m'))
	require.NotNil(t, cmd)
	assert.Equal(t, msg.RequestActionPickerMsg{
		Title:   "Manage Queue",
		Options: []string{"Send Message", "Purge Queue", "Delete Queue"},
	}, cmd())

	charSQSSpace(v)
	charSQSDown(v)
	charSQSSpace(v)
	_, cmd = v.Update(keyPress('m'))
	require.NotNil(t, cmd)
	assert.Equal(t, msg.RequestActionPickerMsg{
		Title:   "Manage 2 Queues",
		Options: []string{"Purge Queue", "Delete Queue"},
	}, cmd())

	// A cancelled picker does nothing.
	_, cmd = v.Update(ui.PickerResultMsg{ID: "action", Selected: -1})
	assert.Nil(t, cmd)

	empty, _ := charSQSLoaded(t, 100)
	_, cmd = empty.Update(keyPress('m'))
	assert.Nil(t, cmd, "no queue, no picker")
}

func charSQSSendForm(t *testing.T, v *SQSQueues) msg.RequestFormMsg {
	t.Helper()
	_, cmd := v.Update(keyPress('m'))
	require.NotNil(t, cmd)
	_, cmd = v.Update(ui.PickerResultMsg{ID: "action", Selected: 0, Value: "Send Message"})
	require.NotNil(t, cmd)
	form, ok := cmd().(msg.RequestFormMsg)
	require.True(t, ok)
	return form
}

type charSQSField struct {
	Key, Type, Title, Description, Placeholder string
	Required, HasValidate                      bool
}

func charSQSFields(form msg.RequestFormMsg) []charSQSField {
	var out []charSQSField
	for _, f := range form.Fields {
		out = append(out, charSQSField{f.Key, f.Type, f.Title, f.Description, f.Placeholder, f.Required, f.Validate != nil})
	}
	return out
}

func charSQSField0(form msg.RequestFormMsg, key string) msg.FormField {
	for _, f := range form.Fields {
		if f.Key == key {
			return f
		}
	}
	return msg.FormField{}
}

var charSQSBodyField = charSQSField{"body", "text", "Message Body", "", "Enter message body (JSON or plain text)", true, false}

func TestCharSQS_SendStandard(t *testing.T) {
	v, svc := charSQSThree(t)
	form := charSQSSendForm(t, v)
	assert.Equal(t, "sqs_send_message", form.ID)
	assert.Equal(t, "Send Message", form.Title)
	assert.Equal(t, []charSQSField{
		charSQSBodyField,
		{"delay", "input", "Delay Seconds", "Optional. Seconds to delay delivery (0-900).", "0", false, true},
	}, charSQSFields(form))

	validate := charSQSField0(form, "delay").Validate
	require.NoError(t, validate(""))
	require.NoError(t, validate("0"))
	require.NoError(t, validate("900"))
	require.EqualError(t, validate("901"), "must be between 0 and 900")
	require.EqualError(t, validate("-1"), "must be between 0 and 900")
	require.EqualError(t, validate("abc"), "must be a number")

	svc.On("SendMessage", mock.Anything, charSQSURL("qa"), "hi", int32(30), "", "").Return(nil).Once()
	_, cmd := v.Update(msg.FormResultMsg{ID: "sqs_send_message", Values: map[string]string{"body": "hi", "delay": "30"}})
	sent := charSQSDeliver(v, charSQSRun(cmd)...)
	assert.Equal(t, []msg.ToastMsg{msg.ToastSuccess("Message sent")}, toasts(sent))

	// The target is used once.
	_, cmd = v.Update(msg.FormResultMsg{ID: "sqs_send_message", Values: map[string]string{"body": "again"}})
	assert.Nil(t, cmd)

	// A delay out of range is sent as 0.
	charSQSSendForm(t, v)
	svc.On("SendMessage", mock.Anything, charSQSURL("qa"), "late", int32(0), "", "").Return(charSQSDenied).Once()
	_, cmd = v.Update(msg.FormResultMsg{ID: "sqs_send_message", Values: map[string]string{"body": "late", "delay": "901"}})
	sent = charSQSDeliver(v, charSQSRun(cmd)...)
	assert.Equal(t, []msg.ToastMsg{
		msg.ToastError("Send failed: access denied (AccessDenied). The credentials of this profile do not allow this call"),
	}, toasts(sent))
	svc.AssertExpectations(t)
}

func TestCharSQS_SendFIFO(t *testing.T) {
	fifo := aws.Queue{URL: charSQSURL("f.fifo"), Name: "f.fifo", Type: "FIFO", FifoQueue: true}
	cbd := aws.Queue{URL: charSQSURL("c.fifo"), Name: "c.fifo", Type: "FIFO", FifoQueue: true, ContentBasedDeduplication: true}
	v, svc := charSQSLoaded(t, 100, fifo, cbd)

	form := charSQSSendForm(t, v)
	assert.Equal(t, []charSQSField{
		charSQSBodyField,
		{"group_id", "input", "Message Group ID", "Required. Messages in the same group are processed in order.", "Enter group ID", true, true},
		{"dedup_id", "input", "Deduplication ID", "Required (content-based dedup is disabled on this queue).", "Enter deduplication ID", true, true},
	}, charSQSFields(form))
	require.NoError(t, charSQSField0(form, "group_id").Validate(strings.Repeat("g", 128)))
	require.Error(t, charSQSField0(form, "group_id").Validate(strings.Repeat("g", 129)))
	require.Error(t, charSQSField0(form, "dedup_id").Validate(strings.Repeat("d", 129)))

	svc.On("SendMessage", mock.Anything, charSQSURL("f.fifo"), "hi", int32(0), "g1", "d1").Return(nil).Once()
	_, cmd := v.Update(msg.FormResultMsg{ID: "sqs_send_message", Values: map[string]string{"body": "hi", "group_id": "g1", "dedup_id": "d1"}})
	charSQSRun(cmd)

	charSQSDown(v)
	form = charSQSSendForm(t, v)
	assert.Equal(t, []charSQSField{
		charSQSBodyField,
		{"group_id", "input", "Message Group ID", "Required. Messages in the same group are processed in order.", "Enter group ID", true, true},
		{"dedup_id", "input", "Deduplication ID", "Optional. Auto-generated from message body if empty.", "Leave empty for content-based dedup", false, true},
	}, charSQSFields(form))
	svc.AssertExpectations(t)
}

func TestCharSQS_SendTargetsSelectionAndAbortClears(t *testing.T) {
	v, svc := charSQSThree(t)
	charSQSDown(v)
	charSQSSpace(v) // qb
	v.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	require.Equal(t, charSQSURL("qa"), charSQSCursorURL(t, v))

	charSQSSendForm(t, v)
	svc.On("SendMessage", mock.Anything, charSQSURL("qb"), "hi", int32(0), "", "").Return(nil).Once()
	_, cmd := v.Update(msg.FormResultMsg{ID: "sqs_send_message", Values: map[string]string{"body": "hi"}})
	charSQSRun(cmd)
	svc.AssertExpectations(t)

	charSQSSendForm(t, v)
	_, cmd = v.Update(msg.FormResultMsg{ID: "sqs_send_message", Aborted: true})
	assert.Nil(t, cmd)
	_, cmd = v.Update(msg.FormResultMsg{ID: "sqs_send_message", Values: map[string]string{"body": "late"}})
	assert.Nil(t, cmd, "the aborted form cleared the target")
}

func TestCharSQS_SendNeedsOneQueue(t *testing.T) {
	v, _ := charSQSThree(t)
	charSQSSpace(v)
	charSQSDown(v)
	charSQSSpace(v)
	_, cmd := v.Update(keyPress('m'))
	require.NotNil(t, cmd)
	_, cmd = v.Update(ui.PickerResultMsg{ID: "action", Selected: 0, Value: "Send Message"})
	assert.Equal(t, []msg.ToastMsg{msg.ToastError("Select one queue to send a message")}, toasts(collectMsgs(cmd)))
}

func charSQSConfirm(t *testing.T, v *SQSQueues, action string) msg.RequestConfirmMsg {
	t.Helper()
	_, cmd := v.Update(keyPress('m'))
	require.NotNil(t, cmd)
	_, cmd = v.Update(ui.PickerResultMsg{ID: "action", Selected: 1, Value: action})
	require.NotNil(t, cmd)
	req, ok := cmd().(msg.RequestConfirmMsg)
	require.True(t, ok)
	return req
}

func TestCharSQS_PurgeFlow(t *testing.T) {
	charSQSReadWrite(t)
	v, svc := charSQSThree(t)
	charSQSSpace(v)
	charSQSDown(v)
	charSQSSpace(v)

	// Divergence #24 (fixed in PR 2). The prompt lists the queue names.
	assert.Equal(t, msg.RequestConfirmMsg{
		Message: "Purge 2 queue(s)?\n  qa\n  qb\nThis removes all messages and cannot be undone.",
		Action:  "sqs_purge",
	}, charSQSConfirm(t, v, "Purge Queue"))

	svc.On("PurgeQueue", mock.Anything, charSQSURL("qa")).Return(charSQSDenied).Once()
	svc.On("PurgeQueue", mock.Anything, charSQSURL("qb")).Return(nil).Once()
	svc.On("GetQueueAttributes", mock.Anything, charSQSURL("qb")).
		Return(&aws.Queue{URL: charSQSURL("qb"), Name: "qb", Type: "Standard"}, nil).Once()

	_, cmd := v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "sqs_purge"})
	// Divergence #27 (fixed in PR 2). The spinner shows after the confirm.
	assert.Equal(t, "3/3 queues  (2 selected)  ⣾  Purging 2 queue(s)...", plain(v.Footer()))
	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.NotEmpty(t, batch)
	assert.IsType(t, spinner.TickMsg{}, batch[0](), "the spinner ticks")

	seen := pump(v, cmd, 4)
	assert.Equal(t, []msg.ToastMsg{msg.ToastError("Purged 1 of 2 queue(s). Failed: qa (AccessDenied)")}, toasts(seen))
	assert.Equal(t, []string{"✓ qa Standard 8 0 0", "✓ qb Standard 0 0 0", "qc Standard 3 0 0"}, charSQSRows(v),
		"the failed purge went on; only the purged queue is read again")
	assert.Equal(t, "3/3 queues  (2 selected)", plain(v.Footer()), "purge keeps the selection")
	svc.AssertExpectations(t)
	svc.AssertNotCalled(t, "GetQueueAttributes", mock.Anything, charSQSURL("qa"))

	// Full success has the cooldown text.
	charSQSConfirm(t, v, "Purge Queue")
	svc.On("PurgeQueue", mock.Anything, mock.Anything).Return(nil)
	svc.On("GetQueueAttributes", mock.Anything, mock.Anything).
		Return(&aws.Queue{URL: charSQSURL("qa"), Name: "qa", Type: "Standard"}, nil)
	_, cmd = v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "sqs_purge"})
	seen = pump(v, cmd, 4)
	assert.Equal(t, []msg.ToastMsg{msg.ToastSuccess("Purged 2 queue(s) (60s cooldown before the next purge)")}, toasts(seen))
}

func TestCharSQS_DeleteFlow(t *testing.T) {
	charSQSReadWrite(t)
	v, svc := charSQSThree(t)
	charSQSSpace(v)
	charSQSDown(v)
	charSQSSpace(v)
	charSQSDown(v)
	charSQSSpace(v)

	// Divergence #24 (fixed in PR 2). The prompt lists the queue names.
	assert.Equal(t, msg.RequestConfirmMsg{
		Message: "Delete 3 queue(s)?\n  qa\n  qb\n  qc\nAll messages will be lost.",
		Action:  "sqs_delete",
	}, charSQSConfirm(t, v, "Delete Queue"))

	svc.On("DeleteQueue", mock.Anything, charSQSURL("qa")).Return(nil).Once()
	svc.On("DeleteQueue", mock.Anything, charSQSURL("qb")).Return(charSQSDenied).Once()
	svc.On("DeleteQueue", mock.Anything, charSQSURL("qc")).Return(nil).Once()

	_, cmd := v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "sqs_delete"})
	// Divergence #27 (fixed in PR 2). The spinner shows after the confirm.
	assert.Equal(t, "3/3 queues  (3 selected)  ⣾  Deleting 3 queue(s)...", plain(v.Footer()))
	seen := pump(v, cmd, 3)
	assert.Equal(t, []msg.ToastMsg{msg.ToastError("Deleted 2 of 3 queue(s). Failed: qb (AccessDenied)")}, toasts(seen))
	assert.Equal(t, []string{"qb Standard 10 0 0"}, charSQSRows(v), "only the deleted rows go")
	assert.Equal(t, "1/1 queues", v.Footer(), "delete deselects")
	svc.AssertExpectations(t)

	charSQSConfirm(t, v, "Delete Queue")
	svc.On("DeleteQueue", mock.Anything, charSQSURL("qb")).Return(nil).Once()
	_, cmd = v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "sqs_delete"})
	seen = pump(v, cmd, 3)
	assert.Equal(t, []msg.ToastMsg{msg.ToastSuccess("Deleted 1 queue(s)")}, toasts(seen))
	assert.Equal(t, "0/0 queues", v.Footer())
}

func TestCharSQS_ConfirmDeclineClearsTargets(t *testing.T) {
	charSQSReadWrite(t)
	v, svc := charSQSThree(t)
	charSQSConfirm(t, v, "Purge Queue")
	_, cmd := v.Update(ui.ConfirmResultMsg{Confirmed: false, Action: "sqs_purge"})
	assert.Nil(t, cmd)

	// A late confirm has no targets and calls nothing.
	_, cmd = v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "sqs_purge"})
	assert.Nil(t, cmd)
	svc.AssertNotCalled(t, "PurgeQueue", mock.Anything, mock.Anything)
}

// Divergence #25 (fixed in PR 2). The confirm Action is checked against the
// pending action: a purge prompt answered as sqs_delete does nothing and
// clears the targets.
func TestCharSQS_ConfirmActionChecked(t *testing.T) {
	charSQSReadWrite(t)
	v, svc := charSQSThree(t)
	charSQSConfirm(t, v, "Purge Queue")
	_, cmd := v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "sqs_delete"})
	assert.Nil(t, cmd)
	_, cmd = v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "sqs_purge"})
	assert.Nil(t, cmd, "the targets were cleared")

	// An unknown Action clears the targets and does nothing.
	charSQSConfirm(t, v, "Purge Queue")
	_, cmd = v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "other"})
	assert.Nil(t, cmd)
	_, cmd = v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "sqs_purge"})
	assert.Nil(t, cmd, "the targets were cleared")
	svc.AssertNotCalled(t, "PurgeQueue", mock.Anything, mock.Anything)
	svc.AssertNotCalled(t, "DeleteQueue", mock.Anything, mock.Anything)
}

// Divergence #25 (fixed in PR 2). After a cancelled action picker no confirm
// was asked, so a confirm reply does nothing.
func TestCharSQS_CancelledPickerIgnoresConfirm(t *testing.T) {
	charSQSReadWrite(t)
	v, svc := charSQSThree(t)
	_, cmd := v.Update(keyPress('m'))
	require.NotNil(t, cmd)
	_, cmd = v.Update(ui.PickerResultMsg{ID: "action", Selected: -1})
	require.Nil(t, cmd)

	_, cmd = v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "sqs_purge"})
	assert.Nil(t, cmd)
	svc.AssertNotCalled(t, "PurgeQueue", mock.Anything, mock.Anything)
}

// The app blocks RW keys in ReadOnly mode, so m and the picker still work at
// view level. When ReadOnly is on at the confirm reply (the mode changed
// after the picker), the view clears the targets and calls nothing.
func TestCharSQS_ReadOnlyAtConfirmCallsNothing(t *testing.T) {
	prev := ui.IsReadOnly()
	t.Cleanup(func() { ui.SetReadOnly(prev) })
	ui.SetReadOnly(true)

	v, svc := charSQSThree(t)
	_, cmd := v.Update(keyPress('m'))
	require.NotNil(t, cmd)
	assert.Equal(t, "Manage Queue", cmd().(msg.RequestActionPickerMsg).Title)
	charSQSConfirm(t, v, "Delete Queue")
	_, cmd = v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "sqs_delete"})
	assert.Equal(t, []msg.ToastMsg{msg.ToastError("ReadOnly mode — press W to switch")}, toasts(pump(v, cmd, 3)))
	assert.Equal(t, "3/3 queues", plain(v.Footer()), "no spinner")

	// The targets are gone, also after a switch to ReadWrite.
	ui.SetReadOnly(false)
	_, cmd = v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "sqs_delete"})
	assert.Nil(t, cmd)
	svc.AssertNotCalled(t, "DeleteQueue", mock.Anything, mock.Anything)
}

// Mutations use a detached context: they finish when the user leaves.
func TestCharSQS_MutationsSurviveLeavingTheView(t *testing.T) {
	charSQSReadWrite(t)
	v, svc := charSQSThree(t)
	ctx, cancel := context.WithCancel(context.Background())
	v.SetContext(ctx)
	cancel()

	var mu sync.Mutex
	var errs []error
	record := func(args mock.Arguments) {
		mu.Lock()
		defer mu.Unlock()
		errs = append(errs, args.Get(0).(context.Context).Err())
	}
	svc.On("PurgeQueue", mock.Anything, charSQSURL("qa")).Run(record).Return(nil).Once()
	svc.On("GetQueueAttributes", mock.Anything, charSQSURL("qa")).Return(nil, context.Canceled).Once()
	svc.On("DeleteQueue", mock.Anything, charSQSURL("qa")).Run(record).Return(nil).Once()
	svc.On("SendMessage", mock.Anything, charSQSURL("qa"), "hi", int32(0), "", "").Run(record).Return(nil).Once()

	charSQSConfirm(t, v, "Purge Queue")
	_, cmd := v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "sqs_purge"})
	pump(v, cmd, 3)
	charSQSSendForm(t, v)
	_, cmd = v.Update(msg.FormResultMsg{ID: "sqs_send_message", Values: map[string]string{"body": "hi"}})
	pump(v, cmd, 2)
	charSQSConfirm(t, v, "Delete Queue")
	_, cmd = v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "sqs_delete"})
	pump(v, cmd, 2)

	svc.AssertExpectations(t)
	assert.Equal(t, []error{nil, nil, nil}, errs)
}

func TestCharSQS_SetContextReachesFetch(t *testing.T) {
	type key struct{}
	v, svc := charSQSNew(100)
	v.SetContext(context.WithValue(context.Background(), key{}, "view"))

	var mu sync.Mutex
	var got []any
	record := func(args mock.Arguments) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, args.Get(0).(context.Context).Value(key{}))
	}
	svc.On("ListQueuesPage", mock.Anything, (*string)(nil)).Run(record).
		Return(&aws.QueuePage{QueueURLs: []string{charSQSURL("qa")}}, nil).Once()
	svc.On("FetchAllQueueAttributes", mock.Anything, mock.Anything).Run(record).
		Return([]aws.Queue{charSQSQueue("qa", 1)}, nil).Once()
	svc.On("GetQueueAttributes", mock.Anything, charSQSURL("qa")).Run(record).
		Return(&aws.Queue{URL: charSQSURL("qa"), Name: "qa"}, nil).Once()
	svc.On("ListDeadLetterSourceQueues", mock.Anything, charSQSURL("qa")).Run(record).
		Return([]string(nil), nil).Once()

	pump(v, v.Init(), 6)
	_, cmd := v.Update(keyPress('d'))
	charSQSRun(cmd)
	assert.Equal(t, []any{"view", "view", "view", "view"}, got)
}

// charSQSRecordDeadlines makes GetQueueAttributes record how long each call
// may take.
func charSQSRecordDeadlines(svc *awstest.MockSQSService, replies map[string]*aws.Queue, fail string) func() map[string]time.Duration {
	var mu sync.Mutex
	limits := map[string]time.Duration{}
	for url, q := range replies {
		svc.On("GetQueueAttributes", mock.Anything, url).Run(func(args mock.Arguments) {
			mu.Lock()
			defer mu.Unlock()
			if d, ok := args.Get(0).(context.Context).Deadline(); ok {
				limits[url] = time.Until(d)
			}
		}).Return(q, nil).Once()
	}
	if fail != "" {
		svc.On("GetQueueAttributes", mock.Anything, fail).Run(func(args mock.Arguments) {
			mu.Lock()
			defer mu.Unlock()
			if d, ok := args.Get(0).(context.Context).Deadline(); ok {
				limits[fail] = time.Until(d)
			}
		}).Return(nil, charSQSDenied).Once()
	}
	return func() map[string]time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return maps.Clone(limits)
	}
}

func TestCharSQS_SelectiveRefresh(t *testing.T) {
	v, svc := charSQSThree(t)
	charSQSSpace(v) // qa
	charSQSDown(v)
	charSQSSpace(v) // qb
	charSQSDown(v)
	charSQSSpace(v) // qc
	limits := charSQSRecordDeadlines(svc, map[string]*aws.Queue{
		charSQSURL("qa"): {URL: charSQSURL("qa"), Name: "qa", Type: "Standard", ApproximateMessageCount: 99},
		charSQSURL("qc"): {URL: charSQSURL("qc"), Name: "qc", Type: "FIFO", ApproximateDelayedCount: 7},
	}, charSQSURL("qb"))

	_, cmd := v.Update(keyPress('r'))
	assert.Equal(t, "3/3 queues  (3 selected)  ⣾  Refreshing 3 queues...", plain(v.Footer()))
	assert.Equal(t, []string{"✓ qa Standard 8 0 0", "✓ qb Standard 10 0 0", "✓ qc Standard 3 0 0"}, charSQSRows(v),
		"the table stays usable")

	seen := pump(v, cmd, 3)
	// Divergence #4: changes in PR 8. One call per queue, no toast for the
	// failed queue.
	assert.Empty(t, toasts(seen))
	assert.Equal(t, []string{"✓ qa Standard 99 0 0", "✓ qb Standard 10 0 0", "✓ qc FIFO 0 0 7"}, charSQSRows(v),
		"a failed queue does not stop the others")
	assert.Equal(t, "3/3 queues  (3 selected)", plain(v.Footer()))
	svc.AssertNumberOfCalls(t, "ListQueuesPage", 1)
	svc.AssertNumberOfCalls(t, "GetQueueAttributes", 3)

	got := limits()
	require.Len(t, got, 3, "each queue has its own call")
	for url, d := range got {
		assert.InDelta(t, 30, d.Seconds(), 2, url)
	}
}

func TestCharSQS_PurgeRefreshHasOwnTimeouts(t *testing.T) {
	charSQSReadWrite(t)
	v, svc := charSQSThree(t)
	charSQSSpace(v)
	charSQSDown(v)
	charSQSSpace(v)
	svc.On("PurgeQueue", mock.Anything, mock.Anything).Return(nil)
	limits := charSQSRecordDeadlines(svc, map[string]*aws.Queue{
		charSQSURL("qa"): {URL: charSQSURL("qa"), Name: "qa", Type: "Standard"},
	}, charSQSURL("qb"))

	charSQSConfirm(t, v, "Purge Queue")
	_, cmd := v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "sqs_purge"})
	pump(v, cmd, 4)
	assert.Equal(t, []string{"✓ qa Standard 0 0 0", "✓ qb Standard 10 0 0", "qc Standard 3 0 0"}, charSQSRows(v))
	got := limits()
	require.Len(t, got, 2)
	for url, d := range got {
		assert.InDelta(t, 30, d.Seconds(), 2, url)
	}
}

// Divergence #30: changes in PR 8. A refreshed queue keeps its old IsDLQ.
func TestCharSQS_RefreshKeepsStaleDLQMark(t *testing.T) {
	src := charSQSQueue("src", 0)
	src.RedrivePolicy = &aws.RedrivePolicy{DeadLetterTargetArn: "arn:aws:sqs:us-east-1:123456789012:dlq"}
	v, svc := charSQSLoaded(t, 160, src, charSQSQueue("dlq", 0))
	require.Equal(t, []string{"src Standard 0 0 0", "dlq Standard 0 0 0 Yes"}, charSQSRows(v))

	svc.On("GetQueueAttributes", mock.Anything, charSQSURL("src")).Return(&aws.Queue{URL: charSQSURL("src"), Name: "src", Type: "Standard"}, nil)
	charSQSSpace(v)
	_, cmd := v.Update(keyPress('r'))
	pump(v, cmd, 3)
	assert.Equal(t, []string{"✓ src Standard 0 0 0", "dlq Standard 0 0 0 Yes"}, charSQSRows(v),
		"src no longer names dlq, but dlq keeps its mark")
}

// Divergence #10: changes in PR 8. A refresh reply has no generation: during
// a full reload it sets the empty item list on the table and hides the load
// spinner.
func TestCharSQS_RefreshReplyDuringReload(t *testing.T) {
	v, svc := charSQSThree(t)
	charSQSSpace(v)
	svc.On("GetQueueAttributes", mock.Anything, charSQSURL("qa")).Return(&aws.Queue{URL: charSQSURL("qa"), Name: "qa"}, nil)
	_, cmd := v.Update(keyPress('r'))
	replies := charSQSRun(cmd)
	require.Len(t, replies, 1)

	v.Update(tea.KeyPressMsg{Code: tea.KeyEscape}) // deselect
	_, _ = v.Update(keyPress('r'))                 // full reload, not run
	require.Equal(t, "\n  ⣾  Loading SQS queues...", plain(v.View().Content))

	charSQSDeliver(v, replies...)
	assert.Equal(t, "\n  ", plain(v.View().Content), "the spinner is gone")
	assert.Equal(t, "0/0 queues", plain(v.Footer()), "the table is empty")
}

// Divergence #11: changes in PR 8. Spinner ticks reach the spinner only while
// loading, so the selective refresh spinner does not move.
func TestCharSQS_SpinnerTicks(t *testing.T) {
	v, svc := charSQSThree(t)
	charSQSSpace(v)
	svc.On("GetQueueAttributes", mock.Anything, charSQSURL("qa")).Return(&aws.Queue{URL: charSQSURL("qa"), Name: "qa"}, nil)
	_, cmd := v.Update(keyPress('r'))
	tick, ok := firstOfType[spinner.TickMsg](collectMsgs(cmd))
	require.True(t, ok)
	_, cmd = v.Update(tick)
	assert.Nil(t, cmd, "no next tick")

	// While loading, the same tick asks for the next tick.
	v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	v.Update(keyPress('r')) // full reload, not run
	_, cmd = v.Update(tick)
	assert.NotNil(t, cmd)
}

// Divergence #13: changes in PR 8. A reload that finds zero queues keeps the
// old rows.
func TestCharSQS_ZeroQueuesKeepStaleRows(t *testing.T) {
	v, svc := charSQSLoaded(t, 100, charSQSQueue("qa", 1), charSQSQueue("qb", 2))
	charSQSExpectLoad(svc)
	_, cmd := v.Update(keyPress('r'))
	pump(v, cmd, 4)
	assert.Equal(t, []string{"qa Standard 1 0 0", "qb Standard 2 0 0"}, charSQSRows(v))
	assert.Equal(t, "2/2 queues", plain(v.Footer()))
	svc.AssertNumberOfCalls(t, "FetchAllQueueAttributes", 1)

	// The rows have no items behind them: details and peek do nothing.
	assert.Empty(t, charSQSCursorURL(t, v))
}

// Divergence #6 and #7: changes in PR 8. During a reload the items are empty,
// so keys that read items do nothing, while keys that read table rows still
// act on the old rows. Navigation keys go to the spinner, not the table.
func TestCharSQS_KeysDuringReload(t *testing.T) {
	v, svc := charSQSThree(t)
	charSQSExpectLoad(svc, charSQSQueue("qa", 8), charSQSQueue("qb", 10), charSQSQueue("qc", 3))
	_, reload := v.Update(keyPress('r'))

	assert.Empty(t, charSQSCursorURL(t, v), "p does nothing")
	_, cmd := v.Update(keyPress('d'))
	assert.Nil(t, cmd, "d does nothing")
	assert.Equal(t, []string{charSQSURL("qa")}, charSQSTargets(t, v), "y copies the old cursor row")
	_, cmd = v.Update(keyPress('m'))
	require.NotNil(t, cmd, "m opens the picker for the old cursor row")
	assert.Equal(t, "Manage Queue", cmd().(msg.RequestActionPickerMsg).Title)
	v.Update(ui.PickerResultMsg{ID: "action", Selected: -1})
	charSQSDown(v)

	pump(v, reload, 6)
	assert.Equal(t, charSQSURL("qa"), charSQSCursorURL(t, v), "the down key did not move the cursor")
}

// During the first load nothing reacts before the rows arrive.
func TestCharSQS_KeysDuringFirstLoad(t *testing.T) {
	v, svc := charSQSNew(100)
	charSQSExpectLoad(svc, charSQSQueue("qa", 1), charSQSQueue("qb", 2))
	attrs := charSQSDeliver(v, charSQSRun(v.Init())...)
	for _, k := range []rune{'p', 'd', 'y', 'm', ' '} {
		_, cmd := v.Update(keyPress(k))
		assert.Nil(t, cmd, string(k))
	}
	// The down key goes to the spinner, not to the table.
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	assert.Nil(t, cmd)
	assert.Equal(t, "\n  ⣾  Loading attributes for 2 queues...", plain(v.View().Content))
	charSQSDeliver(v, attrs...)
	assert.Equal(t, "2/2 queues", v.Footer(), "space selected nothing")
	assert.Equal(t, charSQSURL("qa"), charSQSCursorURL(t, v), "the cursor did not move")
}

func TestCharSQS_Peek(t *testing.T) {
	fifo := aws.Queue{URL: charSQSURL("f.fifo"), Name: "f.fifo", Type: "FIFO", FifoQueue: true}
	v, _ := charSQSLoaded(t, 100, charSQSQueue("qa", 1), fifo)
	charSQSDown(v)
	charSQSSpace(v)
	v.Update(tea.KeyPressMsg{Code: tea.KeyUp})

	_, cmd := v.Update(keyPress('p'))
	require.NotNil(t, cmd)
	assert.Equal(t, msg.NavigateMsg{ViewID: "sqs_messages", Params: map[string]string{
		"queue_url": charSQSURL("qa"), "queue_name": "qa", "fifo": "false",
	}}, cmd(), "the cursor queue, not the selection")

	charSQSDown(v)
	_, cmd = v.Update(keyPress('p'))
	assert.Equal(t, msg.NavigateMsg{ViewID: "sqs_messages", Params: map[string]string{
		"queue_url": charSQSURL("f.fifo"), "queue_name": "f.fifo", "fifo": "true",
	}}, cmd())
}

func TestCharSQS_DetailTabs(t *testing.T) {
	v, svc := charSQSThree(t)
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	detail := &aws.Queue{
		URL: charSQSURL("qa"), Name: "qa", ARN: "arn:aws:sqs:us-east-1:123456789012:qa", Type: "Standard",
		ApproximateMessageCount: 8, ApproximateInFlightCount: 2, ApproximateDelayedCount: 1,
		CreatedTimestamp: created, LastModifiedTimestamp: created,
		VisibilityTimeout: 30, DelaySeconds: 0, MaximumMessageSize: 262144, MessageRetentionPeriod: 345600,
		ReceiveMessageWaitTime: 20, SqsManagedSseEnabled: true,
	}
	svc.On("GetQueueAttributes", mock.Anything, charSQSURL("qa")).Return(detail, nil).Twice()
	svc.On("ListDeadLetterSourceQueues", mock.Anything, charSQSURL("qa")).
		Return([]string{charSQSURL("src1"), charSQSURL("src2")}, nil).Twice()

	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, keyPress('d')} {
		_, cmd := v.Update(k)
		out := charSQSDeliver(v, charSQSRun(cmd)...)
		require.Len(t, out, 1)
		panel, ok := out[0].(msg.TabbedContentMsg)
		require.True(t, ok)
		assert.Equal(t, "qa", panel.PanelTitle)

		var titles []string
		for _, tab := range panel.Tabs {
			titles = append(titles, tab.Title)
			assert.Equal(t, "text", tab.Format)
		}
		assert.Equal(t, []string{"Info", "Configuration", "Messages", "DLQ Config", "Encryption"}, titles)

		assert.Equal(t, ""+
			"Name                   qa\n"+
			"ARN                    arn:aws:sqs:us-east-1:123456789012:qa\n"+
			"URL                    "+charSQSURL("qa")+"\n"+
			"Type                   Standard\n"+
			"Created                2026-01-02 03:04:05\n"+
			"Last Modified          2026-01-02 03:04:05\n", panel.Tabs[0].Content)
		assert.Equal(t, ""+
			"Visibility Timeout     30s\n"+
			"Delay Seconds          0s\n"+
			"Max Message Size       256 KB\n"+
			"Retention Period       4 days\n"+
			"Receive Wait Time      20s\n", panel.Tabs[1].Content)
		assert.Equal(t, ""+
			"Approximate Messages   8\n"+
			"In-Flight              2\n"+
			"Delayed                1\n", panel.Tabs[2].Content)
		assert.Equal(t, []msg.TabLink{{Line: 0, ViewID: "sqs_messages", Params: map[string]string{
			"queue_url": charSQSURL("qa"), "queue_name": "qa", "fifo": "false",
		}}}, panel.Tabs[2].Links)
		assert.Equal(t, ""+
			"Redrive Policy         None\n"+
			"\nDLQ Source Queues:\n"+
			"  src1\n"+
			"  src2\n", panel.Tabs[3].Content)
		assert.Equal(t, []msg.TabLink{
			{Line: 3, ViewID: "sqs_queues", Params: map[string]string{"focus": "src1"}},
			{Line: 4, ViewID: "sqs_queues", Params: map[string]string{"focus": "src2"}},
		}, panel.Tabs[3].Links)
		assert.Equal(t, "SSE                    SQS-managed (SSE-SQS)\n", panel.Tabs[4].Content)
	}
	svc.AssertExpectations(t)
}

func TestCharSQS_DetailFIFOWithRedrive(t *testing.T) {
	q := aws.Queue{URL: charSQSURL("f.fifo"), Name: "f.fifo", Type: "FIFO", FifoQueue: true}
	v, svc := charSQSLoaded(t, 100, q)
	detail := q
	detail.ContentBasedDeduplication = true
	detail.DeduplicationScope = "messageGroup"
	detail.FifoThroughputLimit = "perMessageGroupId"
	detail.KmsMasterKeyID = "alias/aws/sqs"
	detail.KmsDataKeyReusePeriod = 300
	detail.RedrivePolicy = &aws.RedrivePolicy{DeadLetterTargetArn: "arn:dlq", MaxReceiveCount: 5}
	detail.RedriveAllowPolicy = &aws.RedriveAllowPolicy{RedrivePermission: "allowAll"}
	svc.On("GetQueueAttributes", mock.Anything, q.URL).Return(&detail, nil).Once()
	// A failed source list is ignored.
	svc.On("ListDeadLetterSourceQueues", mock.Anything, q.URL).Return([]string{charSQSURL("src")}, errors.New("denied")).Once()

	_, cmd := v.Update(keyPress('d'))
	panel, ok := firstOfType[msg.TabbedContentMsg](charSQSDeliver(v, charSQSRun(cmd)...))
	require.True(t, ok)
	var titles []string
	for _, tab := range panel.Tabs {
		titles = append(titles, tab.Title)
	}
	assert.Equal(t, []string{"Info", "Configuration", "Messages", "DLQ Config", "Encryption", "FIFO Settings"}, titles)
	assert.Equal(t, "true", panel.Tabs[2].Links[0].Params["fifo"])

	dlqTab := panel.Tabs[3]
	assert.Equal(t, ""+
		"Target DLQ ARN         arn:dlq\n"+
		"Max Receive Count      5\n"+
		"\nRedrive Policy:\n"+
		"{\n  \"deadLetterTargetArn\": \"arn:dlq\",\n  \"maxReceiveCount\": 5\n}\n"+
		"\nRedrive Allow Policy:\n"+
		"{\n  \"redrivePermission\": \"allowAll\"\n}\n"+
		"\nDLQ Source Queues:\n"+
		"  src\n", dlqTab.Content)
	require.Len(t, dlqTab.Links, 1)
	assert.Equal(t, 15, dlqTab.Links[0].Line)
	assert.Equal(t, "  src", strings.Split(dlqTab.Content, "\n")[15], "the link line is the source name")
	assert.Equal(t, map[string]string{"focus": "src"}, dlqTab.Links[0].Params)

	assert.Equal(t, ""+
		"SSE                    KMS\n"+
		"KMS Key ID             alias/aws/sqs\n"+
		"KMS Reuse Period       300s\n", panel.Tabs[4].Content)
	assert.Equal(t, ""+
		"Content-Based Dedup    true\n"+
		"Dedup Scope            messageGroup\n"+
		"Throughput Limit       perMessageGroupId\n", panel.Tabs[5].Content)
}

// Divergence #19 (fixed in PR 3): a failed detail fetch shows a toast, and
// the list stays.
func TestCharSQS_DetailErrorToast(t *testing.T) {
	v, svc := charSQSThree(t)
	svc.On("GetQueueAttributes", mock.Anything, charSQSURL("qa")).Return(nil, errors.New("gone")).Once()
	_, cmd := v.Update(keyPress('d'))
	got := charSQSDeliver(v, charSQSRun(cmd)...)
	assert.Equal(t, []msg.ToastMsg{msg.ToastError("Details failed: gone")}, toasts(got))
	assert.NotContains(t, plain(v.View().Content), "Error:")
	assert.Equal(t, "3/3 queues", v.Footer())
	svc.AssertNotCalled(t, "ListDeadLetterSourceQueues", mock.Anything, mock.Anything)
}

// Divergence #48 (kept): details with no cursor row fall through to the
// table.
func TestCharSQS_DetailWithNoRow(t *testing.T) {
	v, svc := charSQSLoaded(t, 100)
	_, cmd := v.Update(keyPress('d'))
	assert.Empty(t, charSQSRun(cmd))
	svc.AssertNotCalled(t, "GetQueueAttributes", mock.Anything, mock.Anything)
}
