package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// A prompt of a view can never run code of the app, also when its action
// is the one that the quit confirm used before PR 6. The reply goes to the
// view.
func TestViewConfirmNeverRunsAnAppPrompt(t *testing.T) {
	m := newTestModel(140, 40)
	m = startTestOp(t, m)
	v := newReplyView()
	m.pushView(v)
	m, _ = update(t, m, nav.Routed{Origin: v, Msg: msg.RequestConfirmMsg{Message: "Quit?", Action: "app_quit", Quick: true}})
	require.True(t, m.confirm.Visible())
	assert.False(t, m.quitAsked())

	m, cmd := update(t, m, press("y"))
	require.NotNil(t, cmd)
	reply := cmd()
	assert.IsType(t, overlayReplyMsg{}, reply)
	m, cmd = update(t, m, reply)
	assert.Equal(t, []ui.ConfirmResultMsg{{Confirmed: true, Action: "app_quit"}}, v.confirms)
	if cmd != nil {
		assert.NotEqual(t, tea.QuitMsg{}, cmd(), "the app does not quit")
	}
	assert.Equal(t, 1, m.ops.Count())
	m.ops.CancelAll()
}

// A reply that no prompt of the app asked for runs no code of the app,
// whatever its action or ID says.
func TestStrayRepliesRunNoAppCode(t *testing.T) {
	m := newTestModel(140, 40)
	m = startTestOp(t, m)
	region := m.activeRegion()
	for _, stray := range []tea.Msg{
		ui.ConfirmResultMsg{Confirmed: true, Action: "app_quit"},
		ui.ConfirmResultMsg{Confirmed: true, Action: "app_session_switch"},
		ui.ConfirmResultMsg{Confirmed: true, Action: "theme_switch"},
		ui.PickerResultMsg{ID: "region", Value: "eu-west-1"},
		ui.PickerResultMsg{ID: "theme", Value: "latte"},
		ui.PickerResultMsg{ID: "cancel_op", Value: "1"},
	} {
		var cmd tea.Cmd
		m, cmd = update(t, m, stray)
		if cmd != nil {
			assert.NotEqual(t, tea.QuitMsg{}, cmd(), "%v", stray)
		}
	}
	assert.Equal(t, region, m.activeRegion())
	assert.Equal(t, 1, m.ops.Count())
	require.NoError(t, m.ops.Running()[0].Ctx.Err(), "no operation was cancelled")
	m.ops.CancelAll()
}

// A theme switch throws the prompt components away. Each open prompt gets
// exactly one decline first, so no handler waits for an answer.
func TestThemeSwitchDeclinesOpenPrompts(t *testing.T) {
	m := newTestModel(140, 40)
	var confirms, picks []string
	m.pick("Pick", []ui.PickerOption{{Label: "a", Value: "a"}}, 0, func(m Model, r msg.PickerResultMsg) (Model, tea.Cmd) {
		picks = append(picks, map[bool]string{true: "pick", false: "cancel"}[r.Picked()])
		return m, nil
	})
	m.askQuick("Sure?",
		func(m Model) (Model, tea.Cmd) { confirms = append(confirms, "yes"); return m, nil },
		func(m Model) (Model, tea.Cmd) { confirms = append(confirms, "no"); return m, nil })

	m, cmd := m.setTheme(ui.ThemeOrder[0])
	assert.False(t, m.confirm.Visible())
	assert.False(t, m.picker.Visible())
	assert.Empty(t, m.overlays)
	pump(t, m, cmd)
	assert.Equal(t, []string{"no"}, confirms)
	assert.Equal(t, []string{"cancel"}, picks)
}

// The decline of closePrompts for a view's prompt finds no view after the
// reset, so it is dropped.
func TestThemeSwitchDropsTheDeclineForAViewPrompt(t *testing.T) {
	m := newTestModel(140, 40)
	v := newReplyView()
	m.pushView(v)
	m, _ = update(t, m, nav.Routed{Origin: v, Msg: msg.RequestActionPickerMsg{Title: "Actions", Options: []string{"Stop"}}})
	require.True(t, m.picker.Visible())

	m, cmd := m.setTheme(ui.ThemeOrder[0])
	pump(t, m, cmd)
	assert.Empty(t, v.picks, "the view is gone, so it gets nothing")
}

// Only the quit confirm takes a second ctrl+c. ctrl+c on another confirm of
// the app does not quit.
func TestCtrlCQuitsOnlyFromTheQuitConfirm(t *testing.T) {
	m := newTestModel(140, 40)
	m = startTestOp(t, m)
	m, _ = m.applyRegion("eu-west-1")
	require.True(t, m.confirm.Visible(), "the session switch asks")
	require.False(t, m.quitAsked())

	m, cmd := update(t, m, keyCtrlC)
	if cmd != nil {
		assert.NotEqual(t, tea.QuitMsg{}, cmd())
	}
	assert.True(t, m.confirm.Visible(), "the session switch still asks")
	m.ops.CancelAll()
}

// typeInto types text into the overlay on top, such as a picker filter.
func typeInto(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, r := range text {
		m, _ = update(t, m, press(string(r)))
	}
	return m
}

// closePrompts skips a prompt without a route. Its reply has no place to
// go, so a decline would only reach deliverReply with nothing to deliver.
func TestThemeSwitchSkipsAPromptWithoutRoute(t *testing.T) {
	m := newTestModel(140, 40)
	v := newReplyView()
	m.pushView(v)
	m, _ = update(t, m, msg.RequestConfirmMsg{Message: "Sure?", Action: "a", Quick: true})
	require.True(t, m.confirm.Visible())

	cmd := m.closePrompts()
	assert.Nil(t, cmd, "no decline for a prompt without a route")
	m, _ = update(t, m, overlayReplyMsg{msg: ui.ConfirmResultMsg{Action: "a"}})
	assert.Empty(t, v.confirms, "a reply without a route goes nowhere")
}

// A pick in the region, profile, and mode pickers runs its handler.
func TestAppPickersApplyThePick(t *testing.T) {
	t.Run("region", func(t *testing.T) {
		m := newTestModel(140, 40)
		require.NotEqual(t, "eu-north-1", m.activeRegion())
		m, _ = update(t, m, press("R"))
		m = typeInto(t, m, "eu-north-1")
		m = answer(t, m, keyEnter)
		assert.Equal(t, "eu-north-1", m.activeRegion())
	})
	t.Run("profile", func(t *testing.T) {
		awsConfigEnv(t, "dev", "prod")
		m := newTestModel(140, 40)
		m, _ = update(t, m, press("P"))
		m = typeInto(t, m, "prod")
		m = answer(t, m, keyEnter)
		assert.Equal(t, "prod", m.config.AWS.Profile)
	})
	t.Run("mode", func(t *testing.T) {
		ui.SetReadOnly(true)
		t.Cleanup(func() { ui.SetReadOnly(true) })
		m := newTestModel(140, 40)
		m, _ = update(t, m, press("W"))
		m = typeInto(t, m, "ReadWrite")
		m = answer(t, m, keyEnter)
		assert.False(t, m.picker.Visible())
		assert.False(t, ui.IsReadOnly())
	})
}

// The service picker opens a service with one feature at once, and asks
// for the feature of a service with several.
func TestServicePickerOpensTheService(t *testing.T) {
	m := newTestModel(140, 40)
	m, _ = update(t, m, press("s"))
	m = typeInto(t, m, "S3")
	m = answer(t, m, keyEnter)
	assert.False(t, m.picker.Visible(), "S3 has one feature")
	assert.Equal(t, "s3_list", m.nav.Current().ID())

	m = newTestModel(140, 40)
	m, _ = update(t, m, press("s"))
	m = typeInto(t, m, "EC2")
	m = answer(t, m, keyEnter)
	assert.True(t, m.picker.Visible(), "EC2 asks which feature")
	assert.Equal(t, 1, m.nav.Depth())
}
