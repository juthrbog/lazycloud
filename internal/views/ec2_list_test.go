package views

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

func newTestEC2List() (*EC2List, *awstest.MockEC2Service) {
	m := new(awstest.MockEC2Service)
	view := NewEC2List(m, nil) // nil awsClient — SSM not tested here
	view.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
	return view, m
}

func loadInstances(view *EC2List, instances []aws.Instance) {
	deliverItems(view, &view.listCore, instances)
}

var testRunningInstance = aws.Instance{ID: "i-running", Name: "web-1", State: "running"}
var testStoppedInstance = aws.Instance{ID: "i-stopped", Name: "batch-1", State: "stopped"}
var testTerminatedInstance = aws.Instance{ID: "i-terminated", Name: "old-1", State: "terminated"}

// --- ReadOnly guard ---

// Without a client (failed profile load), Connect must report an error
// instead of dereferencing nil.
func TestEC2List_ConnectWithoutClient(t *testing.T) {
	ui.SetReadOnly(false)
	defer func() { ui.SetReadOnly(true) }()

	view, _ := newTestEC2List()
	loadInstances(view, []aws.Instance{testRunningInstance})

	var cmd tea.Cmd
	require.NotPanics(t, func() { _, cmd = view.Update(keyPress('o')) })
	require.NotNil(t, cmd)
	toast, ok := cmd().(msg.ToastMsg)
	require.True(t, ok)
	assert.Equal(t, 2, toast.Level)
}

func TestEC2List_ConnectHintIsReadWrite(t *testing.T) {
	view, _ := newTestEC2List()
	for _, h := range view.KeyMap() {
		if h.Binding.Help().Key == "o" {
			assert.Equal(t, ui.ModeReadWrite, h.Mode)
			return
		}
	}
	t.Fatal("connect hint not found")
}

// --- Action picker ---

func TestEC2List_ManageRunningInstance(t *testing.T) {
	ui.SetReadOnly(false)
	defer func() { ui.SetReadOnly(true) }()

	view, _ := newTestEC2List()
	loadInstances(view, []aws.Instance{testRunningInstance})

	_, cmd := view.Update(keyPress('m'))
	require.NotNil(t, cmd)

	result := cmd()
	picker, ok := result.(msg.RequestActionPickerMsg)
	require.True(t, ok, "expected RequestActionPickerMsg, got %T", result)
	assert.Equal(t, []string{"Stop", "Reboot", "Terminate"}, picker.Options)
	assert.Equal(t, []string{"i-running"}, view.pending.keys)
}

func TestEC2List_ManageStoppedInstance(t *testing.T) {
	ui.SetReadOnly(false)
	defer func() { ui.SetReadOnly(true) }()

	view, _ := newTestEC2List()
	loadInstances(view, []aws.Instance{testStoppedInstance})

	_, cmd := view.Update(keyPress('m'))
	require.NotNil(t, cmd)

	result := cmd()
	picker, ok := result.(msg.RequestActionPickerMsg)
	require.True(t, ok, "expected RequestActionPickerMsg, got %T", result)
	assert.Equal(t, []string{"Start"}, picker.Options)
}

func TestEC2List_ManageTerminatedInstance(t *testing.T) {
	ui.SetReadOnly(false)
	defer func() { ui.SetReadOnly(true) }()

	view, _ := newTestEC2List()
	loadInstances(view, []aws.Instance{testTerminatedInstance})

	_, cmd := view.Update(keyPress('m'))
	require.NotNil(t, cmd)

	result := cmd()
	toast, ok := result.(msg.ToastMsg)
	require.True(t, ok, "expected ToastMsg, got %T", result)
	assert.Contains(t, toast.Text, "No common actions")
}

// --- Picker result handling ---

func TestEC2List_StopTriggersConfirm(t *testing.T) {
	view, _ := newTestEC2List()
	loadInstances(view, []aws.Instance{testRunningInstance})
	view.pending.hold([]string{"i-running"})

	_, cmd := view.Update(ui.PickerResultMsg{ID: "action", Selected: 0, Value: "Stop"})
	require.NotNil(t, cmd)

	result := cmd()
	confirm, ok := result.(msg.RequestConfirmMsg)
	require.True(t, ok, "expected RequestConfirmMsg, got %T", result)
	assert.Equal(t, "ec2_stop", confirm.Action)
	assert.Contains(t, confirm.Message, "1 instance(s)")
}

func TestEC2List_TerminateTriggersConfirm(t *testing.T) {
	view, _ := newTestEC2List()
	loadInstances(view, []aws.Instance{testRunningInstance})
	view.pending.hold([]string{"i-running"})

	_, cmd := view.Update(ui.PickerResultMsg{ID: "action", Selected: 2, Value: "Terminate"})
	require.NotNil(t, cmd)

	result := cmd()
	confirm, ok := result.(msg.RequestConfirmMsg)
	require.True(t, ok, "expected RequestConfirmMsg, got %T", result)
	assert.Equal(t, "ec2_terminate", confirm.Action)
}

// M24: Start ran without a prompt. It now asks with a y/n prompt first.
func TestEC2List_StartAsksThenExecutesWithOptimisticUpdate(t *testing.T) {
	setReadWrite(t)
	view, mockSvc := newTestEC2List()
	loadInstances(view, []aws.Instance{testStoppedInstance})
	view.pending.hold([]string{"i-stopped"})

	mockSvc.On("StartInstances", mock.Anything, []string{"i-stopped"}).Return(nil)

	_, cmd := view.Update(ui.PickerResultMsg{ID: "action", Selected: 0, Value: "Start"})
	require.NotNil(t, cmd)
	confirm, ok := cmd().(msg.RequestConfirmMsg)
	require.True(t, ok, "Start asks first")
	assert.True(t, confirm.Quick, "a y/n prompt, because a start destroys nothing")
	assert.Equal(t, "ec2_start", confirm.Action)
	assert.Equal(t, "stopped", view.find("i-stopped").State, "nothing changes before the answer")
	mockSvc.AssertNotCalled(t, "StartInstances", mock.Anything, mock.Anything)

	_, cmd = view.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "ec2_start"})
	require.NotNil(t, cmd)

	// Verify optimistic state update happened immediately
	inst := view.find("i-stopped")
	require.NotNil(t, inst)
	assert.Equal(t, "pending", inst.State, "instance state should be optimistically set to pending")

	// Verify spinner is showing
	assert.True(t, view.spinner.Visible())

	// Execute the batch — one of the cmds calls StartInstance
	// (tea.Batch returns a BatchMsg containing the cmds)
	result := cmd()
	batchMsgs, ok := result.(tea.BatchMsg)
	require.True(t, ok, "expected tea.BatchMsg, got %T", result)

	// Execute each cmd in the batch to trigger the mock
	for _, batchCmd := range batchMsgs {
		if batchCmd != nil {
			batchCmd()
		}
	}
	mockSvc.AssertCalled(t, "StartInstances", mock.Anything, []string{"i-stopped"})
}

// setReadWrite turns ReadOnly mode off for the test.
func setReadWrite(t *testing.T) {
	t.Helper()
	ro := ui.IsReadOnly()
	t.Cleanup(func() { ui.SetReadOnly(ro) })
	ui.SetReadOnly(false)
}

// ReadOnly mode turned on while the prompt is open: the confirm clears the
// targets, shows the app's ReadOnly toast, and makes no API call. This holds
// for each Manage action.
func TestEC2List_ConfirmInReadOnlyRunsNothing(t *testing.T) {
	cases := []struct {
		action, method string
		inst           aws.Instance
	}{
		{"Start", "StartInstances", testStoppedInstance},
		{"Stop", "StopInstances", testRunningInstance},
		{"Reboot", "RebootInstances", testRunningInstance},
		{"Terminate", "TerminateInstances", testRunningInstance},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			setReadWrite(t)
			view, mockSvc := newTestEC2List()
			loadInstances(view, []aws.Instance{tc.inst})

			_, cmd := view.Update(keyPress('m'))
			require.NotNil(t, cmd)
			_, cmd = view.Update(ui.PickerResultMsg{ID: "action", Selected: 0, Value: tc.action})
			require.NotNil(t, cmd)
			confirm, ok := cmd().(msg.RequestConfirmMsg)
			require.True(t, ok)

			ui.SetReadOnly(true)
			_, cmd = view.Update(ui.ConfirmResultMsg{Confirmed: true, Action: confirm.Action})
			require.NotNil(t, cmd)
			assert.Equal(t, msg.ToastError("ReadOnly mode — press W to switch"), cmd())
			assert.Empty(t, view.pending.keys)
			assert.Equal(t, tc.inst.State, view.find(tc.inst.ID).State, "no optimistic state")
			assert.False(t, view.spinner.Visible())
			mockSvc.AssertNotCalled(t, tc.method, mock.Anything, mock.Anything)

			// The targets are gone: a second confirm in ReadWrite mode does nothing.
			ui.SetReadOnly(false)
			_, cmd = view.Update(ui.ConfirmResultMsg{Confirmed: true, Action: confirm.Action})
			assert.Nil(t, cmd)
			mockSvc.AssertNotCalled(t, tc.method, mock.Anything, mock.Anything)
		})
	}
}

// A canceled picker clears the targets, so no later reply can use them.
func TestEC2List_PickerCancelClearsTargets(t *testing.T) {
	view, _ := newTestEC2List()
	loadInstances(view, []aws.Instance{testRunningInstance})
	view.pending.hold([]string{"i-running"})

	_, cmd := view.Update(ui.PickerResultMsg{ID: "action", Selected: -1})
	assert.Nil(t, cmd)
	assert.Empty(t, view.pending.keys)
}

// The names in the prompt go through promptNameList: they are made safe and
// capped, in the order of the targets.
func TestEC2List_ConfirmNamesSanitizedAndCapped(t *testing.T) {
	view, _ := newTestEC2List()
	n := maxPromptNames + 2
	insts := make([]aws.Instance, 0, n)
	ids := make([]string, 0, n)
	for i := range n {
		id := fmt.Sprintf("i-%02d", i)
		insts = append(insts, aws.Instance{ID: id, Name: fmt.Sprintf("n%02d", i), State: "running"})
		ids = append(ids, id)
	}
	insts[0].Name = "evil\x1b[31m"
	loadInstances(view, insts)
	view.pending.hold(ids)

	_, cmd := view.Update(ui.PickerResultMsg{ID: "action", Selected: 0, Value: "Stop"})
	require.NotNil(t, cmd)
	confirm, ok := cmd().(msg.RequestConfirmMsg)
	require.True(t, ok)
	assert.NotContains(t, confirm.Message, "\x1b")
	lines := strings.Split(strings.TrimSuffix(confirm.Message, "\n"), "\n")
	require.Len(t, lines, 1+maxPromptNames+1, "title, the capped names, the rest line")
	assert.Equal(t, fmt.Sprintf("Stop %d instance(s)?", n), lines[0])
	assert.Equal(t, "  n01 (i-01)", lines[2])
	assert.Equal(t, fmt.Sprintf("  n%02d (i-%02d)", maxPromptNames-1, maxPromptNames-1), lines[maxPromptNames])
	assert.Equal(t, "  and 2 more", lines[maxPromptNames+1])
}

// The prompt names each target: "<Name> (<id>)", or the ID alone.
func TestEC2List_ConfirmListsNames(t *testing.T) {
	view, _ := newTestEC2List()
	loadInstances(view, []aws.Instance{testRunningInstance, {ID: "i-noname", State: "running"}})
	view.pending.hold([]string{"i-running", "i-noname", "i-gone"})

	_, cmd := view.Update(ui.PickerResultMsg{ID: "action", Selected: 1, Value: "Reboot"})
	require.NotNil(t, cmd)
	confirm, ok := cmd().(msg.RequestConfirmMsg)
	require.True(t, ok)
	assert.Equal(t, msg.RequestConfirmMsg{
		Message: "Reboot 3 instance(s)?\n  web-1 (i-running)\n  i-noname\n  i-gone\n",
		Action:  "ec2_reboot",
	}, confirm)
}

// Each table entry has a unique label and confirm id, and only Start is
// Quick.
func TestEC2List_ActionTable(t *testing.T) {
	labels := map[string]bool{}
	ids := map[string]bool{}
	for _, a := range ec2Actions {
		assert.False(t, labels[a.label], a.label)
		assert.False(t, ids[a.confirmID], a.confirmID)
		labels[a.label], ids[a.confirmID] = true, true
		assert.Equal(t, a.label == "Start", a.quick, a.label)
		assert.Equal(t, a.label, actionName(a.past), "Outcome text uses the label")
	}
	assert.Len(t, ec2Actions, 4)
}

// --- Multi-select ---

func TestEC2List_SpaceTogglesSelect(t *testing.T) {
	view, _ := newTestEC2List()
	loadInstances(view, []aws.Instance{testRunningInstance, testStoppedInstance})

	view.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	assert.Equal(t, 1, view.table.SelectionCount())

	view.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	assert.Equal(t, 0, view.table.SelectionCount())
}

func TestEC2List_FooterShowsSelectionCount(t *testing.T) {
	view, _ := newTestEC2List()
	loadInstances(view, []aws.Instance{testRunningInstance, testStoppedInstance})

	view.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	assert.Contains(t, view.Footer(), "1 selected")
}

// --- KeyMap ---

func TestEC2List_KeyMapManageIsReadWriteOnly(t *testing.T) {
	view, _ := newTestEC2List()
	hints := view.KeyMap()

	for _, h := range hints {
		if h.Help().Desc == "manage" {
			assert.Equal(t, ui.ModeReadWrite, h.Mode, "manage hint should require ReadWrite mode")
			return
		}
	}
	t.Fatal("expected 'manage' hint in KeyMap")
}

func TestEC2List_KeyMapAlwaysContainsAllHints(t *testing.T) {
	view, _ := newTestEC2List()
	hints := view.KeyMap()

	descs := make([]string, len(hints))
	for i, h := range hints {
		descs[i] = h.Help().Desc
	}
	assert.Contains(t, descs, "details")
	assert.Contains(t, descs, "manage")
}

// --- Responsive columns ---

func TestEC2List_NarrowTierHidesColumns(t *testing.T) {
	cols := ec2Columns(ui.TierNarrow)
	assert.Len(t, cols, 4, "narrow tier should show 4 columns")
	assert.Equal(t, "Instance ID", cols[0].Title)
	assert.Equal(t, "Type", cols[3].Title)
}

func TestEC2List_MediumTierColumns(t *testing.T) {
	cols := ec2Columns(ui.TierMedium)
	assert.Len(t, cols, 5, "medium tier should show 5 columns")
	assert.Equal(t, "Instance ID", cols[0].Title)
	assert.Equal(t, "Private IP", cols[4].Title)
}

func TestEC2List_WideTierShowsAllColumns(t *testing.T) {
	cols := ec2Columns(ui.TierWide)
	assert.Len(t, cols, 8, "wide tier should show 8 columns")
	assert.Equal(t, "Launched", cols[7].Title)
}

func TestEC2List_TierChangeRebuildsRows(t *testing.T) {
	view, _ := newTestEC2List()
	loadInstances(view, []aws.Instance{testRunningInstance})

	// Resize to narrow
	view.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	assert.Equal(t, ui.TierNarrow, view.widthTier)

	// Resize back to wide (must be wide enough for medium columns + padding)
	view.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
	assert.Equal(t, ui.TierWide, view.widthTier)
}

// --- actionsForState ---

func TestEC2List_ActionsForState(t *testing.T) {
	assert.Equal(t, []string{"Start"}, actionsForState("stopped"))
	assert.Equal(t, []string{"Stop", "Reboot", "Terminate"}, actionsForState("running"))
	assert.Nil(t, actionsForState("pending"))
	assert.Nil(t, actionsForState("stopping"))
	assert.Nil(t, actionsForState("terminated"))
}
