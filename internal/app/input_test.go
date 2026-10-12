package app

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// These tests cover input capture (H2) and the central ReadOnly guard (M27)
// from docs/reviews/2026-10-11.

var (
	keyTab       = tea.KeyPressMsg{Code: tea.KeyTab}
	keyBackspace = tea.KeyPressMsg{Code: tea.KeyBackspace}
	keyEnter     = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyCtrlC     = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
)

func typeText(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, r := range text {
		m, _ = update(t, m, press(string(r)))
	}
	return m
}

// screen returns the rendered view on top, without ANSI codes.
func screen(m Model) string {
	return ansi.Strip(m.nav.Current().View().Content)
}

// H2: in a filter, letters that are app-wide keys are typed text.
func TestFilterTakesAppWideKeys(t *testing.T) {
	ec2m := new(awstest.MockEC2Service)
	ec2m.On("ListInstancesPage", mock.Anything, (*string)(nil)).
		Return(&aws.InstancePage{Instances: []aws.Instance{{ID: "i-1", Name: "web"}}}, nil)
	m := newTestModel(140, 40)
	m.ec2 = ec2m
	m, cmd := update(t, m, msg.NavigateMsg{ViewID: "ec2_list"})
	m, _ = update(t, m, findMsg(collectMsgs(cmd), "views.ec2PageLoadedMsg"))
	m.openPanel("detail", "{}", "json")
	m.panelFocused = false
	depth := m.nav.Depth()

	m, _ = update(t, m, press("/"))
	require.True(t, m.capturingInput())
	m = typeText(t, m, "qWTPRL:?sx")
	m, _ = update(t, m, keyTab)
	m, _ = update(t, m, keyBackspace)

	assert.Equal(t, depth, m.nav.Depth(), "q must not close the view")
	assert.Equal(t, "ec2_list", m.nav.Current().ID())
	assert.False(t, m.picker.Visible(), "W, T, P, and R must not open a picker")
	assert.False(t, m.help.Visible(), "? must not open help")
	assert.False(t, m.commandBar.Visible(), ": must not open the command bar")
	assert.False(t, m.panelFocused, "tab must not move the focus to the panel")
	assert.Contains(t, screen(m), "qWTPRL:?s", "the letters are in the filter")
	assert.NotContains(t, screen(m), "qWTPRL:?sx", "backspace deletes a character")

	// After esc, the same keys work again: q closes the panel, then the view.
	m, _ = update(t, m, press("esc"))
	require.False(t, m.capturingInput())
	m, _ = update(t, m, press("q"))
	assert.False(t, m.panelOpen)
	m, _ = update(t, m, press("q"))
	assert.Equal(t, depth-1, m.nav.Depth())
}

func TestCtrlCQuitsWhileTyping(t *testing.T) {
	m := newTestModel(140, 40)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "ec2_list"})
	m, _ = update(t, m, press("/"))
	_, cmd := update(t, m, keyCtrlC)
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
}

// s3ObjectsModel opens the object list of bucket "b" with one object.
func s3ObjectsModel(t *testing.T, s3m *awstest.MockS3Service) Model {
	t.Helper()
	s3m.On("ListObjectsPage", mock.Anything, "b", "", (*string)(nil)).
		Return(&aws.ObjectPage{Objects: []aws.S3Object{{Key: "a.txt"}}}, nil)
	m := newTestModel(140, 40)
	m.s3 = aws.GuardS3(s3m, ui.IsReadOnly)
	m, cmd := update(t, m, msg.NavigateMsg{ViewID: "s3_objects", Params: map[string]string{"bucket": "b"}})
	m, _ = update(t, m, findMsg(collectMsgs(cmd), "views.s3PageLoadedMsg"))
	return m
}

// H2, the ReadOnly bypass: with the copy input open, W used to open the mode
// picker, and the copy ran after a switch to ReadOnly. Now W is typed text,
// and the guarded service refuses the copy if the mode changes anyway.
func TestCopyInputTakesModeKeyAndGuardBlocksCopy(t *testing.T) {
	ui.SetReadOnly(false)
	defer ui.SetReadOnly(true)
	s3m := new(awstest.MockS3Service)
	m := s3ObjectsModel(t, s3m)

	m, _ = update(t, m, press("c"))
	require.True(t, m.capturingInput(), "c opens the destination input")
	m, _ = update(t, m, press("W"))
	assert.False(t, m.picker.Visible(), "W is typed, not the mode picker")
	assert.Contains(t, screen(m), "W")

	ui.SetReadOnly(true)
	m, cmd := update(t, m, keyEnter)
	assert.False(t, m.capturingInput())
	m, _ = update(t, m, findMsg(collectMsgs(cmd), "views.s3CopyCompleteMsg"))
	assert.Contains(t, screen(m), aws.ErrReadOnly.Error())
	s3m.AssertNotCalled(t, "CopyObject", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// T3: a confirmed prompt runs its action through the guarded service. If the
// mode is ReadOnly when the action runs, nothing is deleted.
func TestConfirmedDeleteIsBlockedAfterSwitchToReadOnly(t *testing.T) {
	ui.SetReadOnly(false)
	defer ui.SetReadOnly(true)
	s3m := new(awstest.MockS3Service)
	m := s3ObjectsModel(t, s3m)

	m, cmd := update(t, m, press("x"))
	m, _ = update(t, m, findMsg(collectMsgs(cmd), "msg.RequestConfirmMsg"))
	require.True(t, m.confirm.Visible())

	ui.SetReadOnly(true)
	m.confirm = ui.NewConfirm()
	m, cmd = update(t, m, ui.ConfirmResultMsg{Confirmed: true, Action: "delete_objects"})
	m, cmd = update(t, m, findMsg(collectMsgs(cmd), "views.s3DeleteResolvedMsg"))
	m, _ = update(t, m, findMsg(collectMsgs(cmd), "views.s3DeleteCompleteMsg"))
	assert.Contains(t, screen(m), aws.ErrReadOnly.Error())
	s3m.AssertNotCalled(t, "DeleteObjects", mock.Anything, mock.Anything, mock.Anything)
	s3m.AssertNotCalled(t, "DeleteObject", mock.Anything, mock.Anything, mock.Anything)
}

// T5: in ReadOnly mode, every ReadWrite key of every view shows the
// ReadOnly toast, and the view never gets the key. The expected keys are
// listed, so a binding that loses its ReadWrite mark fails this test.
func TestReadOnlyBlocksEveryReadWriteKey(t *testing.T) {
	ui.SetReadOnly(true)
	cases := []struct {
		viewID string
		params map[string]string
		keys   []string
	}{
		{"s3_list", nil, []string{"n", "x"}},
		{"s3_objects", map[string]string{"bucket": "b"}, []string{"c", "m", "x", "X"}},
		{"s3_versions", map[string]string{"bucket": "b", "key": "k"}, nil},
		{"ec2_list", nil, []string{"o", "m"}},
		{"ami_list", nil, nil},
		{"sg_list", nil, nil},
		{"vpc_list", nil, nil},
		{"subnet_list", nil, nil},
		{"sqs_queues", nil, []string{"m"}},
		{"sqs_messages", map[string]string{"queue_url": "u", "queue_name": "q"}, []string{"x", "m"}},
		{"eventlog", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.viewID, func(t *testing.T) {
			m := newTestModel(140, 40)
			m.s3, m.ec2, m.sqs = new(awstest.MockS3Service), new(awstest.MockEC2Service), new(awstest.MockSQSService)
			m, _ = update(t, m, msg.NavigateMsg{ViewID: tc.viewID, Params: tc.params})
			require.Equal(t, tc.viewID, strings.SplitN(m.nav.Current().ID(), ":", 2)[0])

			var keys []string
			for _, h := range m.nav.Current().KeyMap() {
				if h.Mode == ui.ModeReadWrite {
					keys = append(keys, h.Keys()...)
				}
			}
			assert.ElementsMatch(t, tc.keys, keys)

			for _, k := range keys {
				fresh := m
				fresh.toasts = ui.NewToastManager()
				fresh, _ = update(t, fresh, press(k))
				assert.True(t, fresh.toasts.HasActive(), "key %q shows a toast", k)
				assert.False(t, fresh.capturingInput(), "key %q opens no input", k)
				assert.False(t, fresh.picker.Visible() || fresh.confirm.Visible(), "key %q opens no prompt", k)
			}
		})
	}
}

// In ReadWrite mode, the same keys reach the view.
func TestReadWriteKeysReachTheView(t *testing.T) {
	ui.SetReadOnly(false)
	defer ui.SetReadOnly(true)
	m := s3ObjectsModel(t, new(awstest.MockS3Service))
	m, _ = update(t, m, press("c"))
	assert.True(t, m.capturingInput(), "c opens the copy input")
	assert.False(t, m.toasts.HasActive())
}

// Every view with a text input reports it, so the app can give it the keys.
func TestViewsWithFiltersCaptureInput(t *testing.T) {
	for viewID, params := range map[string]map[string]string{
		"s3_list":      nil,
		"s3_objects":   {"bucket": "b"},
		"ec2_list":     nil,
		"ami_list":     nil,
		"sg_list":      nil,
		"vpc_list":     nil,
		"subnet_list":  nil,
		"sqs_queues":   nil,
		"sqs_messages": {"queue_url": "u", "queue_name": "q"},
		"eventlog":     nil,
	} {
		t.Run(viewID, func(t *testing.T) {
			m := newTestModel(140, 40)
			m.s3, m.ec2, m.sqs = new(awstest.MockS3Service), new(awstest.MockEC2Service), new(awstest.MockSQSService)
			m, _ = update(t, m, msg.NavigateMsg{ViewID: viewID, Params: params})
			_, ok := m.nav.Current().(nav.InputCapturer)
			require.True(t, ok, "%s implements nav.InputCapturer", viewID)
			m, _ = update(t, m, press("/"))
			assert.True(t, m.capturingInput())
			m, _ = update(t, m, press("esc"))
			assert.False(t, m.capturingInput())
		})
	}
}

// The app builds guarded services at startup and after a region or profile
// switch. The guard answers before any request leaves the process.
func TestAppServicesAreGuarded(t *testing.T) {
	ui.SetReadOnly(true)
	check := func(t *testing.T, m Model) {
		t.Helper()
		ctx := context.Background()
		assert.ErrorIs(t, m.s3.DeleteBucket(ctx, "b"), aws.ErrReadOnly)
		assert.ErrorIs(t, m.ec2.TerminateInstances(ctx, []string{"i-1"}), aws.ErrReadOnly)
		assert.ErrorIs(t, m.sqs.PurgeQueue(ctx, "u"), aws.ErrReadOnly)
	}
	// newTestModel points the client at a closed local port, so a missing
	// guard fails with "connection refused" and never reaches AWS.
	m := newTestModel(140, 40)
	check(t, m)
	m, _ = m.applyRegion("eu-west-1")
	check(t, m)
	m, _ = m.applyProfile("default")
	check(t, m)
}

func TestReadWriteKeyIgnoresEnabledFlag(t *testing.T) {
	ui.SetReadOnly(true)
	hints := []ui.HintBinding{
		ui.NewHintBinding([]string{"x"}, "x", "delete").WithMode(ui.ModeReadWrite),
		ui.NewHintBinding([]string{"d"}, "d", "describe"),
	}
	ui.ApplyModeAll(hints)
	require.False(t, hints[0].Enabled(), "ApplyModeAll disables the copy")
	assert.True(t, readWriteKey(hints, press("x")))
	assert.False(t, readWriteKey(hints, press("d")), "d is not a ReadWrite key")
	assert.False(t, readWriteKey(hints, press("y")), "y is not bound")
}
