package views

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/aws/smithy-go"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ops"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// Characterization tests for s3_list (Phase 8, PR 0). They record today's
// behavior through the public surface of the view, so the s3 port (PR 9)
// shows each change it makes as a diff of this file. A line that records a
// bug the design changes names its row in the divergence table of
// docs/design/phase8-resource-list.md.

var charS3Date = time.Date(2024, 3, 5, 7, 8, 9, 0, time.UTC)

func charS3Buckets(names ...string) []aws.Bucket {
	out := make([]aws.Bucket, len(names))
	for i, n := range names {
		out[i] = aws.Bucket{Name: n, CreationDate: charS3Date}
	}
	return out
}

// charS3Fresh returns a view that has a window size and has not loaded yet.
func charS3Fresh(t *testing.T) (*S3List, *awstest.MockS3Service) {
	t.Helper()
	svc := new(awstest.MockS3Service)
	v := NewS3List(svc, "us-east-1")
	v.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	return v, svc
}

// charS3Loaded returns a view that ran Init and loaded the buckets through
// the mock. Later ListBuckets calls return the same buckets unless the test
// queues other replies first with charS3NextList.
func charS3Loaded(t *testing.T, names ...string) (*S3List, *awstest.MockS3Service) {
	t.Helper()
	v, svc := charS3Fresh(t)
	svc.On("ListBuckets", mock.Anything).Return(charS3Buckets(names...), nil).Once()
	pump(v, v.Init(), 3)
	return v, svc
}

// charS3NextList makes the next ListBuckets call return names.
func charS3NextList(svc *awstest.MockS3Service, names ...string) {
	svc.On("ListBuckets", mock.Anything).Return(charS3Buckets(names...), nil).Once()
}

// charS3Replies runs cmd and returns its messages without spinner ticks.
func charS3Replies(cmd tea.Cmd) []tea.Msg {
	var out []tea.Msg
	for _, m := range collectMsgs(cmd) {
		if _, tick := m.(spinner.TickMsg); tick {
			continue
		}
		out = append(out, m)
	}
	return out
}

// charS3Reply runs cmd and returns its only message that is not a spinner
// tick.
func charS3Reply(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	got := charS3Replies(cmd)
	require.Len(t, got, 1)
	return got[0]
}

// charS3Text returns the ANSI-stripped view, one entry per non-empty line,
// with runs of spaces collapsed and the table rule shortened to "---".
func charS3Text(v *S3List) []string {
	var out []string
	for line := range strings.SplitSeq(plain(v.View().Content), "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		if strings.Trim(line, "─") == "" {
			line = "---"
		}
		out = append(out, line)
	}
	return out
}

// charS3Header returns the column titles of the table on screen.
func charS3Header(v *S3List) []string {
	return strings.Fields(strings.Split(plain(v.View().Content), "\n")[0])
}

// charS3Keys describes each hint as "keys|help key|help desc|mode|enabled".
func charS3Keys(v *S3List) []string {
	modes := map[ui.HintMode]string{ui.ModeAny: "any", ui.ModeReadOnly: "ro", ui.ModeReadWrite: "rw"}
	var out []string
	for _, h := range v.KeyMap() {
		on := "off"
		if h.Enabled() {
			on = "on"
		}
		out = append(out, fmt.Sprintf("%s|%s|%s|%s|%s",
			strings.Join(h.Keys(), ","), h.Help().Key, h.Help().Desc, modes[h.Mode], on))
	}
	return out
}

func charS3SetReadOnly(t *testing.T, ro bool) {
	t.Helper()
	prev := ui.IsReadOnly()
	t.Cleanup(func() { ui.SetReadOnly(prev) })
	ui.SetReadOnly(ro)
}

// charS3Type sends text to the view key by key and drops the commands. The
// create input ignores its own FilterChangedMsg while it is open.
func charS3Type(v *S3List, text string) {
	for _, r := range text {
		v.Update(keyPress(r))
	}
}

// charS3TypeMsgs is charS3Type, but returns the messages the keys produce
// at once (FilterChangedMsg). Commands that wait, such as the text input's
// cursor blink, are skipped.
func charS3TypeMsgs(v *S3List, text string) []tea.Msg {
	var out []tea.Msg
	for _, r := range text {
		_, cmd := v.Update(keyPress(r))
		out = append(out, collectMsgsTimed(cmd, 200*time.Millisecond)...)
	}
	return out
}

func charS3FgSGR(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)
}

var (
	errCharS3Denied = errors.New("AccessDenied")
	charS3Enter     = tea.KeyPressMsg{Code: tea.KeyEnter}
	charS3Esc       = tea.KeyPressMsg{Code: tea.KeyEscape}
	charS3Down      = tea.KeyPressMsg{Code: tea.KeyDown}
	charS3Space     = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
)

// charS3Browsed presses enter and returns the bucket it browses to, or ""
// when enter gives no NavigateMsg.
func charS3Browsed(t *testing.T, v *S3List) string {
	t.Helper()
	_, cmd := v.Update(charS3Enter)
	nav, ok := firstOfType[msg.NavigateMsg](collectMsgs(cmd))
	if !ok {
		return ""
	}
	require.Equal(t, "s3_objects", nav.ViewID)
	return nav.Params["bucket"]
}

// --- All views ---

func TestCharS3_IDAndTitle(t *testing.T) {
	v, _ := charS3Fresh(t)
	assert.Equal(t, "s3_list", v.ID())
	assert.Equal(t, "S3 Buckets", v.Title())
}

func TestCharS3_KeyMapSnapshot(t *testing.T) {
	v, _ := charS3Fresh(t)

	charS3SetReadOnly(t, true)
	assert.Equal(t, []string{
		"enter|enter|browse|any|on",
		"d|d|properties|any|on",
		"n|n|new bucket|rw|off",
		"x|x|delete bucket|rw|off",
		"space|space|select|any|on",
		"s|s/S|sort|any|on",
		"/|/|filter|any|on",
		"r|r|refresh|any|on",
	}, charS3Keys(v))

	ui.SetReadOnly(false)
	assert.Equal(t, []string{
		"enter|enter|browse|any|on",
		"d|d|properties|any|on",
		"n|n|new bucket|rw|on",
		"x|x|delete bucket|rw|on",
		"space|space|select|any|on",
		"s|s/S|sort|any|on",
		"/|/|filter|any|on",
		"r|r|refresh|any|on",
	}, charS3Keys(v))
}

func TestCharS3_CapturingInput(t *testing.T) {
	charS3SetReadOnly(t, false)
	v, _ := charS3Loaded(t, "a-logs")
	assert.False(t, v.CapturingInput())

	v.Update(keyPress('/'))
	assert.True(t, v.CapturingInput(), "the filter is open")
	v.Update(charS3Esc)
	assert.False(t, v.CapturingInput())

	v.Update(keyPress('n'))
	assert.True(t, v.CapturingInput(), "the create input is open")
	v.Update(charS3Esc)
	assert.False(t, v.CapturingInput())
}

func TestCharS3_InitLoadsOnlyWhileLoading(t *testing.T) {
	v, svc := charS3Fresh(t)
	svc.On("ListBuckets", mock.Anything).Return(charS3Buckets("b-tmp", "a-logs"), nil).Once()

	cmd := v.Init()
	require.NotNil(t, cmd)
	pump(v, cmd, 3) // the first reply of a fresh view is accepted
	svc.AssertNumberOfCalls(t, "ListBuckets", 1)
	assert.Equal(t, "2/2 buckets", v.Footer())

	// A cached view (coming back from s3_objects) does not reload, but Init
	// still deselects.
	v.Update(charS3Space)
	require.Equal(t, "2/2 buckets  (1 selected)", v.Footer())
	assert.Nil(t, v.Init())
	assert.Equal(t, "2/2 buckets", v.Footer())
	svc.AssertNumberOfCalls(t, "ListBuckets", 1)
}

// s3 has no load generation today: the reply that arrives last wins, also
// when it belongs to the older load. The gen guard comes with the core in
// the s3 port (PR 9).
func TestCharS3_StaleReplyLastWins(t *testing.T) {
	v, svc := charS3Loaded(t, "a-logs")
	charS3NextList(svc, "old-1", "old-2")
	charS3NextList(svc, "new-1")

	_, first := v.Update(keyPress('r'))
	older := charS3Reply(t, first)
	_, second := v.Update(keyPress('r'))
	newer := charS3Reply(t, second)

	deliverAll(v, newer, older)
	assert.Equal(t, "2/2 buckets", v.Footer())
	assert.Contains(t, charS3Text(v), "old-1 2024-03-05 07:08:09")
}

func TestCharS3_StaleErrorIsApplied(t *testing.T) {
	v, svc := charS3Loaded(t, "a-logs")
	svc.On("ListBuckets", mock.Anything).Return([]aws.Bucket(nil), errCharS3Denied).Once()
	charS3NextList(svc, "new-1")

	_, first := v.Update(keyPress('r'))
	older := charS3Reply(t, first)
	_, second := v.Update(keyPress('r'))
	newer := charS3Reply(t, second)

	deliverAll(v, newer, older)
	// Divergence #9: changes in PR 9 (the core gen drops the stale failure).
	assert.Equal(t, []string{"Error: AccessDenied"}, charS3Text(v))
}

func TestCharS3_ErrorContexts(t *testing.T) {
	v, svc := charS3Fresh(t)
	svc.On("ListBuckets", mock.Anything).Return([]aws.Bucket(nil), errCharS3Denied).Once()
	em, ok := firstOfType[msg.ErrorMsg](charS3Replies(v.Init()))
	require.True(t, ok)
	assert.Equal(t, "listing S3 buckets", em.Context)
	require.ErrorIs(t, em.Err, errCharS3Denied)

	nilView := NewS3List(nil, "us-east-1")
	em, ok = firstOfType[msg.ErrorMsg](charS3Replies(nilView.Init()))
	require.True(t, ok)
	assert.Equal(t, "S3", em.Context)
	require.EqualError(t, em.Err, "AWS client not initialized")
}

func TestCharS3_ErrorViewIsSanitizedAndRed(t *testing.T) {
	v, _ := charS3Fresh(t)
	v.Update(msg.ErrorMsg{Err: errors.New("boom \x1b[31m"), Context: "listing S3 buckets"})

	out := v.View().Content
	assert.Equal(t, "\n  Error: boom ␛[31m", plain(out))
	assert.Contains(t, out, charS3FgSGR(ui.ActiveTheme.Error))
	assert.Equal(t, "0/0 buckets", v.Footer())

	// The text is aws.ErrorText, not err.Error().
	v.Update(msg.ErrorMsg{
		Err:     &smithy.GenericAPIError{Code: "AccessDenied", Message: "no"},
		Context: "listing S3 buckets",
	})
	assert.Equal(t, "\n  Error: access denied (AccessDenied). The credentials of this profile do not allow this call",
		plain(v.View().Content))
}

func TestCharS3_ViewSnapshots(t *testing.T) {
	charS3SetReadOnly(t, false)
	v, svc := charS3Fresh(t)
	svc.On("ListBuckets", mock.Anything).Return(charS3Buckets("b-tmp", "a-logs"), nil).Once()

	// First load: no tick was delivered, so the spinner shows its first frame.
	assert.Equal(t, "\n  ⣾  Loading S3 buckets...", plain(v.View().Content))
	pump(v, v.Init(), 3)
	assert.Equal(t, []string{
		"Bucket Created",
		"---",
		"b-tmp 2024-03-05 07:08:09",
		"a-logs 2024-03-05 07:08:09",
	}, charS3Text(v))

	// Reload: the rows are kept (footer) but the spinner replaces the table.
	charS3NextList(svc, "b-tmp", "a-logs")
	_, reload := v.Update(keyPress('r'))
	// Divergence #16: changes in PR 9 (spinner only while loading with no rows).
	assert.Equal(t, "\n  ⣾  Loading S3 buckets...", plain(v.View().Content))
	assert.Equal(t, "2/2 buckets", v.Footer())
	pump(v, reload, 3)

	// The create input is drawn under the table.
	v.Update(keyPress('n'))
	assert.Equal(t, []string{
		"Bucket Created",
		"---",
		"b-tmp 2024-03-05 07:08:09",
		"a-logs 2024-03-05 07:08:09",
		"┃ new bucket bucket-name",
	}, charS3Text(v))
	v.Update(charS3Esc)

	// Error screen, then n on it (divergence #54: kept).
	v.Update(msg.ErrorMsg{Err: errors.New("boom"), Context: "listing S3 buckets"})
	assert.Equal(t, "\n  Error: boom", plain(v.View().Content))
	v.Update(keyPress('n'))
	assert.Equal(t, []string{"Error: boom", "┃ new bucket bucket-name"}, charS3Text(v))
	charS3Type(v, "ab")
	assert.Equal(t, []string{"Error: boom", "┃ new bucket ab"}, charS3Text(v))
}

func TestCharS3_CreateInputOnSpinnerScreen(t *testing.T) {
	charS3SetReadOnly(t, false)
	v, _ := charS3Fresh(t)
	v.Update(keyPress('n'))
	assert.Equal(t, []string{"⣾ Loading S3 buckets...", "┃ new bucket bucket-name"}, charS3Text(v))
}

func TestCharS3_ColumnsPerWidth(t *testing.T) {
	v, _ := charS3Loaded(t, "b-tmp")
	cases := []struct {
		width   int
		cols    []string
		row     string
		created int // byte offset where "Created" starts, -1 when absent
	}{
		{60, []string{"Bucket"}, "b-tmp", -1},
		{79, []string{"Bucket"}, "b-tmp", -1},
		{80, []string{"Bucket", "Created"}, "b-tmp 2024-03-05 07:08:09", 52},
		{100, []string{"Bucket", "Created"}, "b-tmp 2024-03-05 07:08:09", 69},
		{160, []string{"Bucket", "Created"}, "b-tmp 2024-03-05 07:08:09", 83},
		{200, []string{"Bucket", "Created"}, "b-tmp 2024-03-05 07:08:09", 83},
	}
	for _, c := range cases {
		v.Update(tea.WindowSizeMsg{Width: c.width, Height: 12})
		assert.Equal(t, c.cols, charS3Header(v), "width %d", c.width)
		assert.Equal(t, c.row, charS3Text(v)[2], "width %d", c.width)
		header, _, _ := strings.Cut(plain(v.View().Content), "\n")
		assert.Equal(t, c.created, strings.Index(header, "Created"), "width %d", c.width)
	}
}

func TestCharS3_FooterSnapshots(t *testing.T) {
	v, svc := charS3Fresh(t)
	svc.On("ListBuckets", mock.Anything).Return(charS3Buckets("b-tmp", "a-logs"), nil)
	assert.Equal(t, "0/0 buckets", v.Footer(), "first load")
	pump(v, v.Init(), 3)
	assert.Equal(t, "2/2 buckets", v.Footer(), "after load")

	v.Update(charS3Space)
	assert.Equal(t, "2/2 buckets  (1 selected)", v.Footer(), "one selected")

	_, cmd := v.Update(keyPress('r'))
	// Divergence #15: changes in PR 9 (the footer shows loading text).
	assert.Equal(t, "2/2 buckets  (1 selected)", v.Footer(), "while reloading")
	pump(v, cmd, 3)
	assert.Equal(t, "2/2 buckets  (1 selected)", v.Footer(), "reload keeps the selection")
}

func TestCharS3_EscDeselectsThenGoesBack(t *testing.T) {
	v, _ := charS3Loaded(t, "a-logs", "b-tmp")
	v.Update(charS3Space)

	_, cmd := v.Update(charS3Esc)
	assert.Nil(t, cmd, "the first esc only deselects")
	assert.Equal(t, "2/2 buckets", v.Footer())

	_, cmd = v.Update(charS3Esc)
	assert.Equal(t, []tea.Msg{msg.NavigateBackMsg{}}, collectMsgs(cmd))
}

// Divergence #3: s3 has no y key today (open question 7).
func TestCharS3_NoCopyKey(t *testing.T) {
	v, _ := charS3Loaded(t, "a-logs")
	_, cmd := v.Update(keyPress('y'))
	assert.Empty(t, charS3Replies(cmd))
}

func TestCharS3_RefreshKey(t *testing.T) {
	v, svc := charS3Loaded(t, "a-logs")
	v.Update(msg.ErrorMsg{Err: errCharS3Denied, Context: "listing S3 buckets"})
	require.Equal(t, []string{"Error: AccessDenied"}, charS3Text(v))

	charS3NextList(svc, "a-logs", "b-tmp")
	_, cmd := v.Update(keyPress('r'))
	assert.Equal(t, []string{"⣾ Loading S3 buckets..."}, charS3Text(v), "r clears the error and shows the spinner")
	pump(v, cmd, 3)
	svc.AssertNumberOfCalls(t, "ListBuckets", 2)
	svc.AssertNotCalled(t, "ListObjectsPage", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	assert.Equal(t, "2/2 buckets", v.Footer())
	assert.Equal(t, "Bucket Created", charS3Text(v)[0])
}

func TestCharS3_LoadDoesNotClearError(t *testing.T) {
	charS3SetReadOnly(t, false)
	v, svc := charS3Loaded(t, "a-logs")
	v.Update(msg.ErrorMsg{Err: errCharS3Denied, Context: "listing S3 buckets"})

	// A create reloads the list, but the reload does not clear the error.
	svc.On("CreateBucket", mock.Anything, "b-new", "us-east-1").Return(nil)
	charS3NextList(svc, "a-logs", "b-new")
	v.Update(keyPress('n'))
	charS3Type(v, "b-new")
	_, cmd := v.Update(charS3Enter)
	pump(v, cmd, 3)
	assert.Equal(t, "2/2 buckets", v.Footer())
	// Divergence #8: changes in PR 9 (a good full load clears the error).
	assert.Equal(t, []string{"Error: AccessDenied"}, charS3Text(v))
}

func TestCharS3_KeysDuringReload(t *testing.T) {
	v, svc := charS3Loaded(t, "a-logs", "b-tmp")
	charS3NextList(svc, "a-logs", "b-tmp")
	_, reload := v.Update(keyPress('r'))
	reply := charS3Reply(t, reload)

	// Navigation keys go to the spinner, not the table.
	v.Update(charS3Down)
	// The view's own keys still act on the kept rows (divergence #7: kept).
	assert.Equal(t, "a-logs", charS3Browsed(t, v), "enter during a reload browses")
	v.Update(charS3Space)
	assert.Equal(t, "2/2 buckets  (1 selected)", v.Footer(), "space during a reload selects")

	deliverAll(v, reply)
	// Divergence #6: changes in PR 9 (the table is usable during a load).
	assert.Equal(t, "a-logs", charS3Browsed(t, v), "down during the reload did not move the cursor")
}

func TestCharS3_TableKeepsCursorAndSelectionAcrossReload(t *testing.T) {
	v, svc := charS3Loaded(t, "b-tmp", "a-logs")
	v.Update(charS3Down)
	v.Update(charS3Space) // select a-logs, the cursor is on it

	charS3NextList(svc, "0new", "a-logs", "b-tmp")
	_, cmd := v.Update(keyPress('r'))
	pump(v, cmd, 3)
	assert.Equal(t, "3/3 buckets  (1 selected)", v.Footer())
	// M8: the marker never reaches the key.
	assert.Equal(t, "a-logs", charS3Browsed(t, v))
	assert.Contains(t, charS3Text(v), "✓ a-logs 2024-03-05 07:08:09")
}

// C3: the sort is kept by key across a reload that brings rows in another
// order.
func TestCharS3_SortSurvivesReload(t *testing.T) {
	v, svc := charS3Loaded(t, "b-tmp", "a-logs")
	v.Update(ui.PickerResultMsg{ID: "sort", Selected: 0, Value: "Bucket"})
	v.Update(charS3Down) // cursor on b-tmp

	charS3NextList(svc, "c-web", "b-tmp", "0new", "a-logs")
	_, cmd := v.Update(keyPress('r'))
	pump(v, cmd, 3)
	assert.Equal(t, []string{
		"Bucket ▲ Created",
		"---",
		"0new 2024-03-05 07:08:09",
		"a-logs 2024-03-05 07:08:09",
		"b-tmp 2024-03-05 07:08:09",
		"c-web 2024-03-05 07:08:09",
	}, charS3Text(v))
	assert.Equal(t, "b-tmp", charS3Browsed(t, v), "the cursor follows its key")
}

func TestCharS3_FilterDeselectsHiddenRows(t *testing.T) {
	v, _ := charS3Loaded(t, "a-logs", "b-tmp")
	v.Update(charS3Space) // select a-logs

	v.Update(keyPress('/'))
	msgs := charS3TypeMsgs(v, "b")
	_, ok := firstOfType[ui.FilterChangedMsg](msgs)
	require.True(t, ok)
	deliverAll(v, msgs...)
	assert.Equal(t, "1/2 buckets", v.Footer(), "the hidden selection is dropped")
	assert.Equal(t, []string{"┃ / b", "Bucket Created", "---", "b-tmp 2024-03-05 07:08:09"}, charS3Text(v))

	deliverAll(v, deliverAll(v, charS3Esc)...)
	assert.Equal(t, "2/2 buckets", v.Footer(), "the selection does not come back")
}

func TestCharS3_SortPicker(t *testing.T) {
	v, _ := charS3Loaded(t, "b-tmp", "a-logs", "c-web")
	rows := func() []string {
		var out []string
		for _, l := range charS3Text(v)[2:] {
			out = append(out, strings.Fields(l)[0])
		}
		return out
	}

	_, cmd := v.Update(keyPress('s'))
	assert.Equal(t, []tea.Msg{msg.RequestSortPickerMsg{Columns: []string{"Bucket", "Created"}, CurrentCol: -1}}, collectMsgs(cmd))

	v.Update(ui.PickerResultMsg{ID: "sort", Selected: 0, Value: "Bucket"})
	assert.Equal(t, []string{"a-logs", "b-tmp", "c-web"}, rows())
	assert.Equal(t, []string{"Bucket", "▲", "Created"}, charS3Header(v))
	_, cmd = v.Update(keyPress('s'))
	assert.Equal(t, []tea.Msg{msg.RequestSortPickerMsg{Columns: []string{"Bucket", "Created"}, CurrentCol: 0}}, collectMsgs(cmd))

	v.Update(keyPress('S'))
	assert.Equal(t, []string{"c-web", "b-tmp", "a-logs"}, rows())

	// The sort survives a tier change.
	v.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	assert.Equal(t, []string{"Bucket", "▼"}, charS3Header(v))
	assert.Equal(t, []string{"c-web", "b-tmp", "a-logs"}, rows())
	v.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	assert.Equal(t, []string{"c-web", "b-tmp", "a-logs"}, rows())

	// A cancelled picker and another picker change nothing.
	v.Update(ui.PickerResultMsg{ID: "sort", Selected: -1})
	v.Update(ui.PickerResultMsg{ID: "feature", Selected: 1, Value: "x"})
	assert.Equal(t, []string{"c-web", "b-tmp", "a-logs"}, rows())

	v.Update(ui.PickerResultMsg{ID: "sort", Selected: -2, Value: "_clear"})
	assert.Equal(t, []string{"b-tmp", "a-logs", "c-web"}, rows())
	assert.Equal(t, []string{"Bucket", "Created"}, charS3Header(v))
}

func TestCharS3_UpdateReturnsSamePointer(t *testing.T) {
	charS3SetReadOnly(t, false)
	v, svc := charS3Loaded(t, "a-logs", "b-tmp")
	svc.On("ListBuckets", mock.Anything).Return(charS3Buckets("a-logs", "b-tmp"), nil)
	msgs := []tea.Msg{
		tea.WindowSizeMsg{Width: 120, Height: 20},
		charS3Down, charS3Space, keyPress('s'), keyPress('S'), keyPress('d'), keyPress('x'), keyPress('y'),
		ui.ConfirmResultMsg{Confirmed: false, Action: "delete_bucket"},
		ui.PickerResultMsg{ID: "sort", Selected: 0},
		ui.FilterChangedMsg{Text: "a"},
		msg.FocusResourceMsg{ResourceID: "a-logs"},
		keyPress('/'), keyPress('a'), charS3Enter,
		keyPress('n'), keyPress('z'), charS3Esc,
		keyPress('r'),
		msg.ErrorMsg{Err: errCharS3Denied, Context: "listing S3 buckets"},
		charS3Esc, charS3Esc,
	}
	for _, m := range msgs {
		got, _ := v.Update(m)
		require.Same(t, v, got, "%T", m)
	}

	// The view's own replies, made by running its commands against the mock.
	svc.On("GetBucketProperties", mock.Anything, mock.Anything).Return(&aws.BucketProperties{Name: "a-logs"}, nil)
	svc.On("CreateBucket", mock.Anything, "c-new", "us-east-1").Return(nil)
	svc.On("EmptyAndDeleteBucket", mock.Anything, mock.Anything).Return(nil)
	_, reload := v.Update(keyPress('r'))
	var replies []tea.Msg
	for _, m := range collectMsgs(reload) { // a spinner tick and the bucket list
		replies = append(replies, m)
		got, _ := v.Update(m)
		require.Same(t, v, got, "%T", m)
	}
	require.Len(t, replies, 2)
	_, props := v.Update(keyPress('d'))
	replies = append(replies, charS3Reply(t, props))
	v.Update(keyPress('n'))
	charS3Type(v, "c-new")
	_, create := v.Update(charS3Enter)
	replies = append(replies, charS3Reply(t, create))
	op := charS3ConfirmDelete(t, v)
	replies = append(replies, op.Run(context.Background()), ui.ConfirmResultMsg{Confirmed: true, Action: "delete_bucket"})
	for _, m := range replies {
		got, _ := v.Update(m)
		require.Same(t, v, got, "%T", m)
	}
}

// Divergence #45: changes in PR 9 (focus comes free from the core).
func TestCharS3_FocusIsIgnored(t *testing.T) {
	v, svc := charS3Fresh(t)
	svc.On("ListBuckets", mock.Anything).Return(charS3Buckets("a-logs", "b-tmp"), nil).Once()
	init := v.Init()

	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "b-tmp"})
	assert.Nil(t, cmd, "focus while loading")
	pump(v, init, 3)
	assert.Equal(t, "a-logs", charS3Browsed(t, v), "focus is not stored")

	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: "b-tmp"})
	assert.Nil(t, cmd)
	assert.Equal(t, "a-logs", charS3Browsed(t, v), "focus does not move the cursor")

	// Focus on a bucket that is not listed does nothing and fetches nothing.
	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: "zz-missing"})
	assert.Nil(t, cmd)
	assert.Equal(t, "2/2 buckets", v.Footer())
	svc.AssertNumberOfCalls(t, "ListBuckets", 1)
	svc.AssertNotCalled(t, "GetBucketProperties", mock.Anything, mock.Anything)
}

func TestCharS3_SpinnerTicks(t *testing.T) {
	v, svc := charS3Fresh(t)
	svc.On("ListBuckets", mock.Anything).Return(charS3Buckets("a-logs"), nil).Once()
	msgs := collectMsgs(v.Init())
	tick, ok := firstOfType[spinner.TickMsg](msgs)
	require.True(t, ok)

	assert.Equal(t, "\n  ⣾  Loading S3 buckets...", plain(v.View().Content))
	_, cmd := v.Update(tick)
	next, ok := firstOfType[spinner.TickMsg](collectMsgs(cmd))
	require.True(t, ok, "the spinner ticks while loading")
	assert.Equal(t, "\n  ⣽  Loading S3 buckets...", plain(v.View().Content), "the frame moved")
	v.Update(next)
	assert.Equal(t, "\n  ⣻  Loading S3 buckets...", plain(v.View().Content))

	deliverAll(v, msgs...)
	_, cmd = v.Update(tick)
	assert.Nil(t, cmd, "no tick after the load")
}

// --- s3_list ---

func TestCharS3_EnterBrowses(t *testing.T) {
	v, _ := charS3Loaded(t, "a-logs")
	_, cmd := v.Update(charS3Enter)
	assert.Equal(t, []tea.Msg{msg.NavigateMsg{
		ViewID: "s3_objects",
		Params: map[string]string{"bucket": "a-logs", "prefix": ""},
	}}, collectMsgs(cmd))

	empty, _ := charS3Loaded(t)
	assert.Empty(t, charS3Browsed(t, empty), "no bucket, no browse")
}

func TestCharS3_Properties(t *testing.T) {
	v, svc := charS3Loaded(t, "a-logs")
	props := &aws.BucketProperties{Name: "a-logs", Region: "eu-west-1", Versioning: "Enabled", Encryption: "AES256", PublicAccess: true}
	var deadline time.Duration
	svc.On("GetBucketProperties", mock.Anything, "a-logs").Run(func(args mock.Arguments) {
		d, _ := args.Get(0).(context.Context).Deadline()
		deadline = time.Until(d)
	}).Return(props, nil)

	_, cmd := v.Update(keyPress('d'))
	got := pump(v, cmd, 2)
	assert.InDelta(t, 30.0, deadline.Seconds(), 5, "props use the 30s call timeout")
	// Divergence #39: changes in PR 9 (a tabbed panel with one JSON tab).
	nav, ok := firstOfType[msg.NavigateMsg](got)
	require.True(t, ok)
	assert.Equal(t, msg.NavigateMsg{ViewID: "content", Params: map[string]string{
		"title": "a-logs (properties)",
		"content": "{\n" +
			"  \"Name\": \"a-logs\",\n" +
			"  \"Region\": \"eu-west-1\",\n" +
			"  \"Versioning\": \"Enabled\",\n" +
			"  \"Encryption\": \"AES256\",\n" +
			"  \"PublicAccess\": true\n" +
			"}",
		"format": "json",
	}}, nav)

	empty, _ := charS3Loaded(t)
	_, cmd = empty.Update(keyPress('d'))
	assert.Nil(t, cmd, "d with no cursor does nothing")
}

func TestCharS3_PropertiesErrorReplacesList(t *testing.T) {
	v, svc := charS3Loaded(t, "a-logs")
	svc.On("GetBucketProperties", mock.Anything, "a-logs").Return((*aws.BucketProperties)(nil), errCharS3Denied)

	_, cmd := v.Update(keyPress('d'))
	got := pump(v, cmd, 2)
	assert.Empty(t, toasts(got))
	// Divergence #19: changes in PR 3 (a toast, the list stays).
	assert.Equal(t, []string{"Error: AccessDenied"}, charS3Text(v))
}

// Divergence #43: changes in PR 3 (guards in the fetch closure).
func TestCharS3_PropertiesPanics(t *testing.T) {
	v, svc := charS3Loaded(t, "a-logs")
	svc.On("GetBucketProperties", mock.Anything, "a-logs").Return((*aws.BucketProperties)(nil), nil)
	_, cmd := v.Update(keyPress('d'))
	reply := cmd()
	_, open := v.Update(reply)
	require.NotNil(t, open)
	assert.Panics(t, func() { open() }, "nil props panic")

	// A view with a nil service gets its rows from the reply of another
	// view's load, so d has a cursor.
	other, otherSvc := charS3Loaded(t, "a-logs")
	otherSvc.On("ListBuckets", mock.Anything).Return(charS3Buckets("a-logs"), nil)
	_, loaded := other.Update(keyPress('r'))
	nilView := NewS3List(nil, "us-east-1")
	nilView.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	nilView.Update(charS3Reply(t, loaded))
	require.Equal(t, "1/1 buckets", nilView.Footer())
	_, cmd = nilView.Update(keyPress('d'))
	require.NotNil(t, cmd)
	assert.Panics(t, func() { cmd() }, "nil service panics")
}

func TestCharS3_DeletePromptOneBucket(t *testing.T) {
	charS3SetReadOnly(t, false)
	v, _ := charS3Loaded(t, "a-logs", "b-tmp")
	_, cmd := v.Update(keyPress('x'))
	assert.Equal(t, []tea.Msg{msg.RequestConfirmMsg{
		Message: "Delete bucket 'a-logs'?\n" +
			"Every object version and delete marker is deleted\n" +
			"permanently, then the bucket. You cannot undo this.",
		Action: "delete_bucket",
		Phrase: "a-logs",
	}}, collectMsgs(cmd))
}

func TestCharS3_DeletePromptSeveralBuckets(t *testing.T) {
	charS3SetReadOnly(t, false)
	v, _ := charS3Loaded(t, "a-logs", "b-tmp")
	v.Update(charS3Space)
	v.Update(charS3Down)
	v.Update(charS3Space)
	_, cmd := v.Update(keyPress('x'))
	assert.Equal(t, []tea.Msg{msg.RequestConfirmMsg{
		Message: "Delete 2 buckets?\n  a-logs\n  b-tmp\n" +
			"Every object version and delete marker is deleted\n" +
			"permanently, then the bucket. You cannot undo this.",
		Action: "delete_bucket",
		Phrase: "delete 2 buckets",
	}}, collectMsgs(cmd))
}

// Divergence #42: changes in PR 3 (the name is sanitized).
func TestCharS3_DeletePromptNotSanitized(t *testing.T) {
	charS3SetReadOnly(t, false)
	v, _ := charS3Loaded(t, "a\x1b[7mb")
	_, cmd := v.Update(keyPress('x'))
	req, ok := firstOfType[msg.RequestConfirmMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(req.Message, "Delete bucket 'a\x1b[7mb'?\n"))
	assert.Equal(t, "a\x1b[7mb", req.Phrase)
}

func TestCharS3_DeleteWithNoBucketDoesNothing(t *testing.T) {
	charS3SetReadOnly(t, false)
	v, _ := charS3Loaded(t)
	_, cmd := v.Update(keyPress('x'))
	assert.Nil(t, cmd)
}

func TestCharS3_OtherConfirmResultClearsPending(t *testing.T) {
	charS3SetReadOnly(t, false)
	cases := []ui.ConfirmResultMsg{
		{Confirmed: false, Action: "delete_bucket"},
		{Confirmed: true, Action: "other"},
	}
	for _, other := range cases {
		v, svc := charS3Loaded(t, "a-logs")
		v.Update(keyPress('x'))
		_, cmd := v.Update(other)
		assert.Nil(t, cmd, "%+v", other)
		_, cmd = v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "delete_bucket"})
		assert.Nil(t, cmd, "pending was cleared by %+v", other)
		svc.AssertNotCalled(t, "EmptyAndDeleteBucket", mock.Anything, mock.Anything)
	}

	v, _ := charS3Loaded(t, "a-logs")
	_, cmd := v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "delete_bucket"})
	assert.Nil(t, cmd, "no pending delete")
}

// The view does not check ReadOnly; the app gates the RW keys. A confirm
// that arrives in ReadOnly mode still deletes. PR 2 adds a ReadOnly recheck
// on confirm (a deliberate change with no divergence row).
func TestCharS3_ViewDoesNotCheckReadOnly(t *testing.T) {
	charS3SetReadOnly(t, true)
	v, _ := charS3Loaded(t, "a-logs")

	v.Update(keyPress('n'))
	assert.True(t, v.CapturingInput(), "n opens the create input in ReadOnly mode")
	v.Update(charS3Esc)

	_, cmd := v.Update(keyPress('x'))
	_, ok := firstOfType[msg.RequestConfirmMsg](collectMsgs(cmd))
	require.True(t, ok, "x asks in ReadOnly mode")
	_, cmd = v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "delete_bucket"})
	require.NotNil(t, cmd)
	_, ok = cmd().(msg.StartOpMsg)
	assert.True(t, ok)
}

// charS3ConfirmDelete presses x on the current targets, confirms, and
// returns the StartOpMsg that the confirm gives.
func charS3ConfirmDelete(t *testing.T, v *S3List) msg.StartOpMsg {
	t.Helper()
	_, cmd := v.Update(keyPress('x'))
	_, ok := firstOfType[msg.RequestConfirmMsg](collectMsgs(cmd))
	require.True(t, ok)
	_, cmd = v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "delete_bucket"})
	require.NotNil(t, cmd)
	op, ok := cmd().(msg.StartOpMsg)
	require.True(t, ok)
	return op
}

func TestCharS3_DeleteOneBucketLabel(t *testing.T) {
	charS3SetReadOnly(t, false)
	v, _ := charS3Loaded(t, "a-logs", "b-tmp")
	op := charS3ConfirmDelete(t, v)
	assert.Equal(t, "Delete bucket a-logs", op.Label)
}

func TestCharS3_DeleteRunsAsOperation(t *testing.T) {
	charS3SetReadOnly(t, false)
	v, svc := charS3Loaded(t, "test-bucket", "other-bucket")
	v.Update(charS3Space)
	v.Update(charS3Down)
	v.Update(charS3Space)
	op := charS3ConfirmDelete(t, v)
	assert.Equal(t, "Delete 2 buckets", op.Label)

	reg := ops.NewRegistry()
	running := reg.Begin(op.Label, "default/us-east-1")
	var progress []ops.Progress
	record := func(mock.Arguments) { progress = append(progress, reg.Running()[0].Progress) }
	svc.On("EmptyAndDeleteBucket", mock.Anything, "test-bucket").Run(record).Return(nil)
	svc.On("EmptyAndDeleteBucket", mock.Anything, "other-bucket").Run(record).Return(errCharS3Denied)
	res := op.Run(running.Ctx)
	running.Done()
	assert.Equal(t, []ops.Progress{{Done: 0, Total: 2}, {Done: 1, Total: 2}}, progress)

	out, ok := res.(msg.Outcome)
	require.True(t, ok)
	assert.Equal(t, msg.ToastError("Deleted 1 of 2 bucket(s). Failed: other-bucket (AccessDenied)"), out.Outcome())

	// The result deselects first, then always reloads, also after a failure.
	charS3NextList(svc, "other-bucket")
	got := pump(v, func() tea.Msg { return res }, 3)
	assert.Equal(t, []msg.ToastMsg{msg.ToastError("Deleted 1 of 2 bucket(s). Failed: other-bucket (AccessDenied)")}, toasts(got))
	svc.AssertNumberOfCalls(t, "ListBuckets", 2)
	assert.Equal(t, "1/1 buckets", v.Footer())
}

func TestCharS3_DeleteSuccessDeselectsAndReloads(t *testing.T) {
	charS3SetReadOnly(t, false)
	v, svc := charS3Loaded(t, "a-logs", "b-tmp")
	v.Update(charS3Down)
	v.Update(charS3Space) // select b-tmp
	op := charS3ConfirmDelete(t, v)
	svc.On("EmptyAndDeleteBucket", mock.Anything, "b-tmp").Return(nil)
	res := op.Run(context.Background())

	charS3NextList(svc, "a-logs", "b-tmp") // b-tmp is still listed
	_, cmd := v.Update(res)
	assert.Equal(t, "2/2 buckets", v.Footer(), "deselected at once")
	assert.Equal(t, []string{"⣾ Loading S3 buckets..."}, charS3Text(v))
	got := pump(v, cmd, 3)
	assert.Equal(t, []msg.ToastMsg{msg.ToastSuccess("Deleted 1 bucket(s)")}, toasts(got))
	assert.Equal(t, "2/2 buckets", v.Footer(), "the selection stays cleared after the reload")
}

func TestCharS3_DeleteCancelAccounting(t *testing.T) {
	charS3SetReadOnly(t, false)
	newOp := func(t *testing.T) (msg.StartOpMsg, *awstest.MockS3Service) {
		t.Helper()
		v, svc := charS3Loaded(t, "a-logs", "b-tmp")
		v.Update(charS3Space)
		v.Update(charS3Down)
		v.Update(charS3Space)
		return charS3ConfirmDelete(t, v), svc
	}

	t.Run("cancelled before start", func(t *testing.T) {
		op, svc := newOp(t)
		reg := ops.NewRegistry()
		running := reg.Begin(op.Label, "")
		reg.Cancel(running.ID)
		res := op.Run(running.Ctx)
		svc.AssertNotCalled(t, "EmptyAndDeleteBucket", mock.Anything, mock.Anything)
		assert.Equal(t, msg.ToastInfo("Bucket delete cancelled: deleted 0 of 2 bucket(s)"), res.(msg.Outcome).Outcome())
	})

	t.Run("cancelled after the first bucket", func(t *testing.T) {
		op, svc := newOp(t)
		reg := ops.NewRegistry()
		running := reg.Begin(op.Label, "")
		svc.On("EmptyAndDeleteBucket", mock.Anything, "a-logs").
			Run(func(mock.Arguments) { reg.Cancel(running.ID) }).Return(nil)
		res := op.Run(running.Ctx)
		svc.AssertNotCalled(t, "EmptyAndDeleteBucket", mock.Anything, "b-tmp")
		assert.Equal(t, msg.ToastInfo("Bucket delete cancelled: deleted 1 of 2 bucket(s)"), res.(msg.Outcome).Outcome())
	})

	t.Run("cancel error after a failure", func(t *testing.T) {
		op, svc := newOp(t)
		svc.On("EmptyAndDeleteBucket", mock.Anything, "a-logs").Return(errCharS3Denied)
		svc.On("EmptyAndDeleteBucket", mock.Anything, "b-tmp").Return(fmt.Errorf("emptying: %w", ops.ErrCanceledByUser))
		res := op.Run(context.Background())
		assert.Equal(t, msg.ToastError("Bucket delete cancelled: deleted 0 of 2 bucket(s). Failed: a-logs (AccessDenied)"),
			res.(msg.Outcome).Outcome())
	})
}

func TestCharS3_CreateBucket(t *testing.T) {
	charS3SetReadOnly(t, false)
	for _, region := range []string{"", "eu-west-1"} {
		svc := new(awstest.MockS3Service)
		// A closed view context: the create still runs (detached).
		type ctxKey struct{}
		ctx, cancel := context.WithCancel(context.WithValue(context.Background(), ctxKey{}, "view"))
		cancel()
		v2 := NewS3List(svc, region)
		v2.SetContext(ctx)
		v2.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
		want := region
		if want == "" {
			want = "us-east-1"
		}
		var ctxErr error
		var value any
		var deadline time.Duration
		svc.On("CreateBucket", mock.Anything, "b-new", want).Run(func(args mock.Arguments) {
			c := args.Get(0).(context.Context)
			ctxErr = c.Err()
			value = c.Value(ctxKey{})
			d, _ := c.Deadline()
			deadline = time.Until(d)
		}).Return(nil)

		v2.Update(keyPress('n'))
		charS3Type(v2, "b-new")
		_, cmd := v2.Update(charS3Enter)
		assert.False(t, v2.CapturingInput(), "enter closes the input")
		require.NotNil(t, cmd)
		reply := cmd()
		svc.AssertCalled(t, "CreateBucket", mock.Anything, "b-new", want)
		require.NoError(t, ctxErr, "detached from the view context")
		assert.Equal(t, "view", value, "but made from the view context")
		assert.InDelta(t, 60.0, deadline.Seconds(), 5, "the 1 min mutate timeout")

		out, ok := reply.(msg.Outcome)
		require.True(t, ok)
		assert.Equal(t, msg.ToastSuccess("Bucket created: b-new"), out.Outcome())
	}
}

func TestCharS3_CreateSuccessReloads(t *testing.T) {
	charS3SetReadOnly(t, false)
	v, svc := charS3Loaded(t, "a-logs")
	svc.On("CreateBucket", mock.Anything, "b-new", "us-east-1").Return(nil)
	charS3NextList(svc, "a-logs", "b-new")

	v.Update(keyPress('n'))
	charS3Type(v, "b-new")
	_, cmd := v.Update(charS3Enter)
	reply := cmd()
	_, cmd = v.Update(reply)
	assert.Equal(t, []string{"⣾ Loading S3 buckets..."}, charS3Text(v))
	got := pump(v, cmd, 3)
	// Divergence #40: changes in PR 3 (the toast uses m.Outcome()); the text stays.
	assert.Equal(t, []msg.ToastMsg{msg.ToastSuccess("Bucket created: b-new")}, toasts(got))
	assert.Equal(t, "2/2 buckets", v.Footer())
}

func TestCharS3_CreateErrorReplacesList(t *testing.T) {
	charS3SetReadOnly(t, false)
	v, svc := charS3Loaded(t, "a-logs")
	svc.On("CreateBucket", mock.Anything, "Bad", "us-east-1").Return(errors.New("InvalidBucketName"))

	v.Update(keyPress('n'))
	charS3Type(v, "Bad")
	_, cmd := v.Update(charS3Enter)
	got := pump(v, cmd, 2)
	assert.Empty(t, toasts(got))
	// Divergence #40: changes in PR 3 (a toast "Bucket Bad not created: ...").
	assert.Equal(t, []string{"Error: InvalidBucketName"}, charS3Text(v))
	svc.AssertNumberOfCalls(t, "ListBuckets", 1)
}

func TestCharS3_CreateInputEmptyAndEsc(t *testing.T) {
	charS3SetReadOnly(t, false)
	v, svc := charS3Loaded(t, "a-logs")

	v.Update(keyPress('n'))
	_, cmd := v.Update(charS3Enter)
	assert.Nil(t, cmd, "an empty name creates nothing")
	assert.False(t, v.CapturingInput())

	v.Update(keyPress('n'))
	charS3Type(v, "abc")
	_, cmd = v.Update(charS3Esc)
	assert.Nil(t, cmd)
	assert.False(t, v.CapturingInput())
	assert.Equal(t, []string{"Bucket Created", "---", "a-logs 2024-03-05 07:08:09"}, charS3Text(v))

	// The input starts empty each time.
	v.Update(keyPress('n'))
	assert.Contains(t, charS3Text(v), "┃ new bucket bucket-name")
	svc.AssertNotCalled(t, "CreateBucket", mock.Anything, mock.Anything, mock.Anything)
}

// Divergence #32: changes in PR 1 (FilterChangedMsg.Source).
func TestCharS3_CreateInputFilterLeak(t *testing.T) {
	charS3SetReadOnly(t, false)
	v, svc := charS3Loaded(t, "a-logs", "b-tmp")

	v.Update(keyPress('n'))
	typed := charS3TypeMsgs(v, "b")
	fc, ok := firstOfType[ui.FilterChangedMsg](typed)
	require.True(t, ok, "the create input sends FilterChangedMsg")
	deliverAll(v, fc)
	assert.Equal(t, "2/2 buckets", v.Footer(), "ignored while the input is open")

	v.Update(charS3Esc)
	deliverAll(v, fc) // the same message, arriving after the input closed
	assert.Equal(t, "1/2 buckets", v.Footer(), "a late message filters the table")

	// A create success clears any table filter with table.Filter("").
	svc.On("CreateBucket", mock.Anything, "c-new", "us-east-1").Return(nil)
	charS3NextList(svc, "a-logs", "b-tmp", "c-new")
	v.Update(keyPress('n'))
	charS3Type(v, "c-new")
	_, cmd := v.Update(charS3Enter)
	pump(v, cmd, 3)
	assert.Equal(t, "3/3 buckets", v.Footer())
}

func TestCharS3_CreateClearsUserFilter(t *testing.T) {
	charS3SetReadOnly(t, false)
	v, svc := charS3Loaded(t, "a-logs", "b-tmp")
	v.Update(keyPress('/'))
	deliverAll(v, charS3TypeMsgs(v, "b")...)
	v.Update(charS3Enter) // keep the filter applied
	require.Equal(t, "1/2 buckets", v.Footer())

	svc.On("CreateBucket", mock.Anything, "c-new", "us-east-1").Return(nil)
	charS3NextList(svc, "a-logs", "b-tmp", "c-new")
	v.Update(keyPress('n'))
	charS3Type(v, "c-new")
	_, cmd := v.Update(charS3Enter)
	pump(v, cmd, 3)
	// Divergence #32: changes in PR 1 (the table.Filter("") hack goes away).
	assert.Equal(t, "3/3 buckets", v.Footer())
}

// Divergence #41: changes in PR 9 (the input width follows a resize).
func TestCharS3_CreateInputWidthNotUpdatedOnResize(t *testing.T) {
	charS3SetReadOnly(t, false)
	v, _ := charS3Loaded(t, "a-logs")
	inputWidth := func() int {
		lines := strings.Split(plain(v.View().Content), "\n")
		return ansi.StringWidth(lines[len(lines)-1])
	}
	v.Update(keyPress('n'))
	assert.Equal(t, 100, inputWidth())
	v.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	assert.Equal(t, 100, inputWidth())
}

func TestCharS3_SelectionSurvivesCreateReload(t *testing.T) {
	charS3SetReadOnly(t, false)
	v, svc := charS3Loaded(t, "a-prod", "b-tmp")
	v.Update(charS3Down)
	v.Update(charS3Space) // select b-tmp

	svc.On("CreateBucket", mock.Anything, "0new", "us-east-1").Return(nil)
	charS3NextList(svc, "0new", "a-prod", "b-tmp")
	v.Update(keyPress('n'))
	charS3Type(v, "0new")
	_, cmd := v.Update(charS3Enter)
	pump(v, cmd, 3)
	assert.Equal(t, "3/3 buckets  (1 selected)", v.Footer())

	_, cmd = v.Update(keyPress('x'))
	req, ok := firstOfType[msg.RequestConfirmMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "b-tmp", req.Phrase)
}

// Divergence #44: changes in PR 9 (ListBuckets uses the 2 min list timeout).
func TestCharS3_SetContextReachesFetch(t *testing.T) {
	type ctxKey struct{}
	v, svc := charS3Fresh(t)
	v.SetContext(context.WithValue(context.Background(), ctxKey{}, "view"))
	var value any
	var deadline time.Duration
	svc.On("ListBuckets", mock.Anything).Run(func(args mock.Arguments) {
		c := args.Get(0).(context.Context)
		value = c.Value(ctxKey{})
		d, _ := c.Deadline()
		deadline = time.Until(d)
	}).Return(charS3Buckets("a-logs"), nil)

	pump(v, v.Init(), 3)
	assert.Equal(t, "view", value)
	assert.InDelta(t, 30.0, deadline.Seconds(), 5, "ListBuckets uses the 30s call timeout")
}
