package views

import (
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
	view.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	return view, m
}

func loadInstances(view *EC2List, instances []aws.Instance) {
	view.Update(ec2InstancesLoadedMsg{instances: instances})
}

var testRunningInstance = aws.Instance{ID: "i-running", Name: "web-1", State: "running"}
var testStoppedInstance = aws.Instance{ID: "i-stopped", Name: "batch-1", State: "stopped"}
var testTerminatedInstance = aws.Instance{ID: "i-terminated", Name: "old-1", State: "terminated"}

// --- ReadOnly guard ---

func TestEC2List_ReadOnlyBlocksManage(t *testing.T) {
	ui.ReadOnly = true
	defer func() { ui.ReadOnly = true }()

	view, _ := newTestEC2List()
	loadInstances(view, []aws.Instance{testRunningInstance})

	_, cmd := view.Update(keyPress('m'))
	require.NotNil(t, cmd)

	result := cmd()
	toast, ok := result.(msg.ToastMsg)
	require.True(t, ok, "expected ToastMsg, got %T", result)
	assert.Equal(t, 2, toast.Level) // error
	assert.Contains(t, toast.Text, "ReadOnly")
}

// --- Action picker ---

func TestEC2List_ManageRunningInstance(t *testing.T) {
	ui.ReadOnly = false
	defer func() { ui.ReadOnly = true }()

	view, _ := newTestEC2List()
	loadInstances(view, []aws.Instance{testRunningInstance})

	_, cmd := view.Update(keyPress('m'))
	require.NotNil(t, cmd)

	result := cmd()
	picker, ok := result.(msg.RequestActionPickerMsg)
	require.True(t, ok, "expected RequestActionPickerMsg, got %T", result)
	assert.Equal(t, []string{"Stop", "Reboot", "Terminate"}, picker.Options)
	assert.Equal(t, "i-running", view.pendingInstanceID)
}

func TestEC2List_ManageStoppedInstance(t *testing.T) {
	ui.ReadOnly = false
	defer func() { ui.ReadOnly = true }()

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
	ui.ReadOnly = false
	defer func() { ui.ReadOnly = true }()

	view, _ := newTestEC2List()
	loadInstances(view, []aws.Instance{testTerminatedInstance})

	_, cmd := view.Update(keyPress('m'))
	require.NotNil(t, cmd)

	result := cmd()
	toast, ok := result.(msg.ToastMsg)
	require.True(t, ok, "expected ToastMsg, got %T", result)
	assert.Contains(t, toast.Text, "No actions available")
}

// --- Picker result handling ---

func TestEC2List_StopTriggersConfirm(t *testing.T) {
	view, _ := newTestEC2List()
	loadInstances(view, []aws.Instance{testRunningInstance})
	view.pendingInstanceID = "i-running"

	_, cmd := view.Update(ui.PickerResultMsg{ID: "action", Selected: 0, Value: "Stop"})
	require.NotNil(t, cmd)

	result := cmd()
	confirm, ok := result.(msg.RequestConfirmMsg)
	require.True(t, ok, "expected RequestConfirmMsg, got %T", result)
	assert.Equal(t, "ec2_stop", confirm.Action)
	assert.Contains(t, confirm.Message, "i-running")
}

func TestEC2List_TerminateTriggersConfirm(t *testing.T) {
	view, _ := newTestEC2List()
	loadInstances(view, []aws.Instance{testRunningInstance})
	view.pendingInstanceID = "i-running"

	_, cmd := view.Update(ui.PickerResultMsg{ID: "action", Selected: 2, Value: "Terminate"})
	require.NotNil(t, cmd)

	result := cmd()
	confirm, ok := result.(msg.RequestConfirmMsg)
	require.True(t, ok, "expected RequestConfirmMsg, got %T", result)
	assert.Equal(t, "ec2_terminate", confirm.Action)
}

func TestEC2List_StartExecutesImmediately(t *testing.T) {
	view, mockSvc := newTestEC2List()
	loadInstances(view, []aws.Instance{testStoppedInstance})
	view.pendingInstanceID = "i-stopped"

	mockSvc.On("StartInstance", mock.Anything, "i-stopped").Return(nil)

	_, cmd := view.Update(ui.PickerResultMsg{ID: "action", Selected: 0, Value: "Start"})
	require.NotNil(t, cmd)

	// Execute the command — it should call StartInstance
	result := cmd()
	mutated, ok := result.(ec2InstanceMutatedMsg)
	require.True(t, ok, "expected ec2InstanceMutatedMsg, got %T", result)
	assert.Equal(t, "started", mutated.action)
	assert.Equal(t, "i-stopped", mutated.instanceID)
	assert.NoError(t, mutated.err)
	mockSvc.AssertCalled(t, "StartInstance", mock.Anything, "i-stopped")
}

// --- KeyMap ---

func TestEC2List_KeyMapHidesManageInReadOnly(t *testing.T) {
	ui.ReadOnly = true
	defer func() { ui.ReadOnly = true }()

	view, _ := newTestEC2List()
	hints := view.KeyMap()

	descs := make([]string, len(hints))
	for i, h := range hints {
		descs[i] = h.Desc
	}
	assert.NotContains(t, descs, "manage")
	assert.Contains(t, descs, "details")
}

func TestEC2List_KeyMapShowsManageInReadWrite(t *testing.T) {
	ui.ReadOnly = false
	defer func() { ui.ReadOnly = true }()

	view, _ := newTestEC2List()
	hints := view.KeyMap()

	descs := make([]string, len(hints))
	for i, h := range hints {
		descs[i] = h.Desc
	}
	assert.Contains(t, descs, "manage")
}

// --- actionsForState ---

func TestEC2List_ActionsForState(t *testing.T) {
	view, _ := newTestEC2List()

	assert.Equal(t, []string{"Start"}, view.actionsForState("stopped"))
	assert.Equal(t, []string{"Stop", "Reboot", "Terminate"}, view.actionsForState("running"))
	assert.Nil(t, view.actionsForState("pending"))
	assert.Nil(t, view.actionsForState("stopping"))
	assert.Nil(t, view.actionsForState("terminated"))
}
