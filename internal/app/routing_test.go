package app

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// These tests cover message routing: each async result goes back to the view
// that started it (see nav.Routed). They replay the repro tests from
// docs/reviews/2026-10-11 with the fixed behavior.

// collectMsgs runs cmd, flattens batches, and returns the messages produced.
// Timer-based messages (spinner ticks, animation frames, toast dismissals)
// are skipped, because tests deliver results by hand.
func collectMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var m tea.Msg
	select {
	case m = <-ch:
	case <-time.After(300 * time.Millisecond):
		return nil
	}
	if b, ok := m.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, collectMsgs(c)...)
		}
		return out
	}
	if m == nil {
		return nil
	}
	name := fmt.Sprintf("%T", unwrapRouted(m))
	for _, skip := range []string{"TickMsg", "AnimationFrameMsg", "ToastDismissMsg"} {
		if strings.Contains(name, skip) {
			return nil
		}
	}
	return []tea.Msg{m}
}

func unwrapRouted(m tea.Msg) tea.Msg {
	if r, ok := m.(nav.Routed); ok {
		return r.Msg
	}
	return m
}

// findMsg returns the first message whose (unwrapped) type name matches.
// It returns the message as produced, so a nav.Routed is fed back routed.
func findMsg(msgs []tea.Msg, typeName string) tea.Msg {
	for _, m := range msgs {
		if fmt.Sprintf("%T", unwrapRouted(m)) == typeName {
			return m
		}
	}
	return nil
}

// findMsgPrefix is findMsg for a type name that starts with prefix and ends
// with suffix. Generic message types print with their full type arguments,
// for example "views.listPageMsg[github.com/.../aws.Subnet]".
func findMsgPrefix(msgs []tea.Msg, prefix, suffix string) tea.Msg {
	for _, m := range msgs {
		name := fmt.Sprintf("%T", unwrapRouted(m))
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix) {
			return m
		}
	}
	return nil
}

func update(t *testing.T, m Model, in tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	require.NotNil(t, in, "message to deliver is nil")
	r, c := m.Update(in)
	return r.(Model), c
}

// runOp delivers the StartOpMsg that cmd produces, as the runtime would. It
// returns the model, with the operation registered, and the command that
// runs the operation.
func runOp(t *testing.T, m Model, cmd tea.Cmd) (Model, tea.Cmd) {
	t.Helper()
	start := findMsg(collectMsgs(cmd), "msg.StartOpMsg")
	require.NotNil(t, start, "the command starts a background operation")
	return update(t, m, start)
}

func press(s string) tea.KeyPressMsg {
	if s == "esc" {
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

// C2: keys resolved for a folder delete in bucket A must never be deleted in
// bucket B, even when the user opens bucket B before the delete ends. The
// delete runs in the background and reports after its view is gone.
func TestRouting_S3DeleteStaysInOriginBucket(t *testing.T) {
	ui.SetReadOnly(false)
	defer func() { ui.SetReadOnly(true) }()

	s3m := new(awstest.MockS3Service)
	s3m.On("ListObjectsPage", mock.Anything, "bucket-a", "", (*string)(nil)).
		Return(&aws.ObjectPage{Prefixes: []string{"tmp/"}}, nil)
	s3m.On("ListObjectsPage", mock.Anything, "bucket-b", "", (*string)(nil)).
		Return(&aws.ObjectPage{Objects: []aws.S3Object{{Key: "tmp/1"}}, Prefixes: []string{"tmp/"}}, nil)
	s3m.On("ListAllKeys", mock.Anything, "bucket-a", "tmp/").Return([]string{"tmp/1", "tmp/2"}, nil)
	s3m.On("DeleteObjects", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	m := newTestModel(140, 40)
	m.svc.S3 = s3m

	m, cmd := update(t, m, msg.NavigateMsg{ViewID: "s3_objects", Params: map[string]string{"bucket": "bucket-a"}})
	m, _ = update(t, m, findMsg(collectMsgs(cmd), "views.s3PageLoadedMsg"))
	m, cmd = update(t, m, press("x"))
	m, _ = update(t, m, findMsg(collectMsgs(cmd), "msg.RequestConfirmMsg"))
	require.True(t, m.confirm.Visible())
	m.confirm = ui.NewConfirm()
	m, cmd = update(t, m, ui.ConfirmResultMsg{Confirmed: true, Action: "delete_objects"})
	m, opCmd := runOp(t, m, cmd)
	require.Equal(t, 1, m.ops.Count(), "the delete is registered before it runs")

	// While the delete runs, the user goes back and opens bucket B.
	m, cmd = update(t, m, press("esc"))
	m, _ = update(t, m, findMsg(collectMsgs(cmd), "msg.NavigateBackMsg"))
	m, cmd = update(t, m, msg.NavigateMsg{ViewID: "s3_objects", Params: map[string]string{"bucket": "bucket-b"}})
	m, _ = update(t, m, findMsg(collectMsgs(cmd), "views.s3PageLoadedMsg"))
	require.Equal(t, "s3_objects:bucket-b:", m.nav.Current().ID())

	done := findMsg(collectMsgs(opCmd), "views.s3DeleteCompleteMsg")
	require.IsType(t, nav.Routed{}, done, "results must carry their origin view")
	assert.Equal(t, 0, m.ops.Count(), "the operation ends with its command")
	m, toastCmd := update(t, m, done)
	toast := findMsg(collectMsgs(toastCmd), "msg.ToastMsg")
	require.NotNil(t, toast, "the result shows although its view is gone")
	assert.Contains(t, toast.(msg.ToastMsg).Text, "Deleted 2")
	m, _ = update(t, m, toast)
	assert.True(t, m.toasts.HasActive())

	s3m.AssertNotCalled(t, "DeleteObjects", mock.Anything, "bucket-b", mock.Anything)
	s3m.AssertCalled(t, "DeleteObjects", mock.Anything, "bucket-a", []string{"tmp/1", "tmp/2"})
}

// H3: a page from the old region must not appear in the new region's view.
func TestRouting_StaleRegionPageDropped(t *testing.T) {
	old := new(awstest.MockEC2Service)
	old.On("ListInstancesPage", mock.Anything, (*string)(nil)).Return(&aws.InstancePage{
		Instances: []aws.Instance{{ID: "i-old1", State: "running"}, {ID: "i-old2", State: "running"}},
	}, nil)
	m := newTestModel(140, 40)
	m.svc.EC2 = old
	m, oldCmd := update(t, m, msg.NavigateMsg{ViewID: "ec2_list"})

	m, rc := m.applyRegion("eu-west-1")
	m, _ = update(t, m, findMsg(collectMsgs(rc), "msg.NavigateMsg"))
	require.Equal(t, "ec2_list", m.nav.Current().ID())

	m, _ = update(t, m, findMsg(collectMsgs(oldCmd), "views.ec2PageLoadedMsg"))
	assert.NotContains(t, m.nav.Current().Footer(), "2/2", "old-region instances shown in the new region")
}

// H4: reopening a view while the first one still loads builds a new view.
// The first view is gone, so its late page is dropped and cannot add rows
// twice.
func TestRouting_ReenterWhileLoadingNoDuplicates(t *testing.T) {
	e := new(awstest.MockEC2Service)
	e.On("ListInstancesPage", mock.Anything, (*string)(nil)).Return(&aws.InstancePage{
		Instances: []aws.Instance{{ID: "i-1", State: "running"}, {ID: "i-2", State: "running"}},
	}, nil)
	m := newTestModel(140, 40)
	m.svc.EC2 = e
	m, first := update(t, m, msg.NavigateMsg{ViewID: "ec2_list"})
	firstView := m.nav.Current()
	m, _ = update(t, m, press("q")) // back to home before the load finishes
	m, second := update(t, m, msg.NavigateMsg{ViewID: "ec2_list"})
	require.NotSame(t, firstView, m.nav.Current(), "the reopened view is new")

	for _, c := range []tea.Cmd{first, second} {
		p := findMsg(collectMsgs(c), "views.ec2PageLoadedMsg")
		require.NotNil(t, p)
		m, _ = update(t, m, p)
	}
	assert.Contains(t, m.nav.Current().Footer(), "2/2 instances")
	assert.Contains(t, firstView.Footer(), "0/0", "the page of the gone view is dropped")
}

// H4: the result of a mutation shows after the user leaves its view: the
// view is gone, so the app logs and shows the result's Outcome.
func TestRouting_MutationResultReachesHiddenView(t *testing.T) {
	ui.SetReadOnly(false)
	defer func() { ui.SetReadOnly(true) }()
	e := new(awstest.MockEC2Service)
	e.On("ListInstancesPage", mock.Anything, (*string)(nil)).Return(&aws.InstancePage{
		Instances: []aws.Instance{{ID: "i-abc", State: "running"}},
	}, nil)
	// The event log keeps only the last 500 entries, so the test finds the
	// failure by a text that is unique to this run, not by position.
	failure := fmt.Sprintf("UnauthorizedOperation %d", time.Now().UnixNano())
	e.On("StopInstances", mock.Anything, []string{"i-abc"}).Return(errors.New(failure))
	// After a failed action, the view refreshes the instances (M7).
	e.On("RefreshInstances", mock.Anything, []string{"i-abc"}).
		Return([]aws.Instance{{ID: "i-abc", State: "running"}}, nil)

	m := newTestModel(140, 40)
	m.svc.EC2 = e
	m, c := update(t, m, msg.NavigateMsg{ViewID: "ec2_list"})
	m, _ = update(t, m, findMsg(collectMsgs(c), "views.ec2PageLoadedMsg"))
	m, c = update(t, m, press("m"))
	m, _ = update(t, m, findMsg(collectMsgs(c), "msg.RequestActionPickerMsg"))
	m.picker = ui.NewPicker()
	m, c = update(t, m, ui.PickerResultMsg{ID: "action", Selected: 0, Value: "Stop"})
	m, _ = update(t, m, findMsg(collectMsgs(c), "msg.RequestConfirmMsg"))
	m.confirm = ui.NewConfirm()
	m, stopCmd := update(t, m, ui.ConfirmResultMsg{Confirmed: true, Action: "ec2_stop"})

	m, _ = update(t, m, press("q")) // leave the view
	m, toastCmd := update(t, m, findMsg(collectMsgs(stopCmd), "views.ec2InstanceMutatedMsg"))

	errs := 0
	for _, en := range eventlog.Entries() {
		if en.Level == eventlog.LevelError && strings.Contains(en.Message, failure) {
			errs++
		}
	}
	assert.Equal(t, 1, errs, "the failure is logged")
	toast := findMsg(collectMsgs(toastCmd), "msg.ToastMsg")
	require.NotNil(t, toast, "the failure toast is produced")
	m, _ = update(t, m, toast)
	assert.True(t, m.toasts.HasActive(), "the failure toast is shown on the home view")
}

// M13: an error belongs to the view that caused it.
func TestRouting_ErrorStaysWithOriginView(t *testing.T) {
	e := new(awstest.MockEC2Service)
	e.On("ListInstancesPage", mock.Anything, (*string)(nil)).Return(&aws.InstancePage{
		Instances: []aws.Instance{{ID: "i-abc", State: "running"}},
	}, nil)
	m := newTestModel(140, 40)
	m.svc.EC2 = e
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
	hidden := m.nav.Current()
	m, c := update(t, m, msg.NavigateMsg{ViewID: "ec2_list"})
	m, _ = update(t, m, findMsg(collectMsgs(c), "views.ec2PageLoadedMsg"))

	// An error from the hidden view must not replace the EC2 table.
	m, _ = update(t, m, nav.Routed{Origin: hidden, Msg: msg.ErrorMsg{Err: errors.New("AccessDenied"), Context: "head s3://b/k"}})
	v := ansi.Strip(m.nav.Current().View().Content)
	assert.Contains(t, v, "i-abc")
	assert.Empty(t, m.visibleErr(), "status bar shows only errors of the current view")

	// An error of the current view shows until the user leaves the view.
	m, _ = update(t, m, msg.ErrorMsg{Err: errors.New("Throttled"), Context: "listing EC2 instances"})
	assert.Contains(t, m.visibleErr(), "Throttled")
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
	assert.Empty(t, m.visibleErr(), "error stays with the view that caused it")
}

// M12: a late panel result from a hidden view must not open over another view.
func TestRouting_LatePanelFromHiddenViewDropped(t *testing.T) {
	panel := msg.TabbedContentMsg{PanelTitle: "report.csv", Tabs: []msg.TabContent{{Title: "Info", Content: "x"}}}

	m := newTestModel(140, 40)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
	hidden := m.nav.Current()
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "ec2_list"})

	m, _ = update(t, m, nav.Routed{Origin: hidden, Msg: panel})
	assert.False(t, m.panelOpen, "panel from a hidden view opened over the current view")

	m, _ = update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: panel})
	assert.True(t, m.panelOpen, "panel from the current view still opens")
}

// H4: a page for one subnet list must not be appended to another one.
func TestRouting_SubnetPagesStayInTheirView(t *testing.T) {
	e := new(awstest.MockEC2Service)
	e.On("ListSubnetsPage", mock.Anything, (*string)(nil), "vpc-A").
		Return(&aws.SubnetPage{Subnets: []aws.Subnet{{ID: "subnet-A1", VpcID: "vpc-A"}}}, nil)
	e.On("ListSubnetsPage", mock.Anything, (*string)(nil), "vpc-B").
		Return(&aws.SubnetPage{}, nil)
	m := newTestModel(140, 40)
	m.svc.EC2 = e

	m, cmdA := update(t, m, msg.NavigateMsg{ViewID: "subnet_list", Params: map[string]string{"vpc_id": "vpc-A"}})
	viewA := m.nav.Current()
	m, _ = update(t, m, press("q")) // leave before vpc-A's page arrives
	m, cmdB := update(t, m, msg.NavigateMsg{ViewID: "subnet_list", Params: map[string]string{"vpc_id": "vpc-B"}})
	viewB := m.nav.Current()
	require.NotEqual(t, viewA.ID(), viewB.ID())

	m, _ = update(t, m, findMsgPrefix(collectMsgs(cmdB), "views.listPageMsg[", "aws.Subnet]"))
	_, _ = update(t, m, findMsgPrefix(collectMsgs(cmdA), "views.listPageMsg[", "aws.Subnet]"))
	assert.NotContains(t, viewB.Footer(), "subnet-A1")
	assert.Contains(t, viewB.Footer(), "0/0", "vpc-A subnets appended to the vpc-B list")
	assert.False(t, m.nav.Contains(viewA), "a popped view is gone")
}

func TestRouting_ToastFromStaleSessionStillShows(t *testing.T) {
	m := newTestModel(140, 40)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
	stale := m.nav.Current()
	m, _ = m.applyRegion("eu-west-1")
	require.False(t, m.nav.Contains(stale))

	m, _ = update(t, m, nav.Routed{Origin: stale, Msg: msg.ToastSuccess("Instance stopped: i-1")})
	assert.True(t, m.toasts.HasActive())
}
