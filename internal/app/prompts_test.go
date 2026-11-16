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
	assert.Equal(t, []msg.ConfirmResultMsg{{Confirmed: true, Action: "app_quit"}}, v.confirms)
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
		msg.ConfirmResultMsg{Confirmed: true, Action: "app_quit"},
		msg.ConfirmResultMsg{Confirmed: true, Action: "app_session_switch"},
		msg.ConfirmResultMsg{Confirmed: true, Action: "theme_switch"},
		msg.PickerResultMsg{ID: "region", Value: "eu-west-1"},
		msg.PickerResultMsg{ID: "theme", Value: "latte"},
		msg.PickerResultMsg{ID: "cancel_op", Value: "1"},
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
	// Only one prompt can be open (D3), so each case opens one.
	t.Run("confirm", func(t *testing.T) {
		m := newTestModel(140, 40)
		var replies []string
		m.askQuick("Sure?",
			func(m Model) (Model, tea.Cmd) { replies = append(replies, "yes"); return m, nil },
			func(m Model) (Model, tea.Cmd) { replies = append(replies, "no"); return m, nil })
		require.True(t, m.confirm.Visible())
		require.Empty(t, replies)

		m, cmd := m.setTheme(ui.ThemeOrder[0])
		assert.False(t, m.confirm.Visible())
		assert.Empty(t, m.overlays)
		pump(t, m, cmd)
		assert.Equal(t, []string{"no"}, replies)
	})
	t.Run("picker", func(t *testing.T) {
		m := newTestModel(140, 40)
		var replies []string
		m.pick("Pick", []ui.PickerOption{{Label: "a", Value: "a"}}, 0, func(m Model, r msg.PickerResultMsg) (Model, tea.Cmd) {
			replies = append(replies, map[bool]string{true: "pick", false: "cancel"}[r.Picked()])
			return m, nil
		})
		require.True(t, m.picker.Visible())
		require.Empty(t, replies)

		m, cmd := m.setTheme(ui.ThemeOrder[0])
		assert.False(t, m.picker.Visible())
		assert.Empty(t, m.overlays)
		pump(t, m, cmd)
		assert.Equal(t, []string{"cancel"}, replies)
	})
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

// Help and the command bar have no route and send no reply that a decline
// could stand for, so closePrompts skips them.
func TestClosePromptsSkipsHelpAndTheCommandBar(t *testing.T) {
	m := newTestModel(140, 40)
	m, _ = update(t, m, press(":"))
	m.help.Show(nil, 140, 40)
	m.openOverlay(ovHelp)
	require.Len(t, m.overlays, 2)
	assert.Nil(t, m.closePrompts())

	// A reply without a route goes nowhere.
	v := newReplyView()
	m.pushView(v)
	m, cmd := update(t, m, overlayReplyMsg{msg: msg.ConfirmResultMsg{Action: "a"}})
	assert.Nil(t, cmd)
	assert.Empty(t, v.confirms)
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

// While a prompt is open, every prompt request gets the same answer: the
// app does not show it, the user gets a toast, and the asker gets one
// decline (D3, PR 7). The open prompt stays and keeps the keys.
func TestEveryPromptRequestIsRefusedWhileAPromptIsOpen(t *testing.T) {
	type tc struct {
		name string
		ask  func(t *testing.T, m Model, v *replyView) Model
		want func(t *testing.T, v *replyView)
	}
	ask := func(t *testing.T, m Model, v *replyView, req tea.Msg) Model {
		m, cmd := update(t, m, nav.Routed{Origin: v, Msg: req})
		v.lastCmd = cmd
		return m
	}
	// The form sends this request. The test sends it from v, so v gets
	// the decline.
	discard := newDiscardRequest(t)
	for _, c := range []tc{
		{"confirm", func(t *testing.T, m Model, v *replyView) Model {
			return ask(t, m, v, msg.RequestConfirmMsg{Message: "Go?", Action: "go"})
		}, func(t *testing.T, v *replyView) {
			assert.Equal(t, []msg.ConfirmResultMsg{{Confirmed: false, Action: "go"}}, v.confirms)
		}},
		{"action picker", func(t *testing.T, m Model, v *replyView) Model {
			return ask(t, m, v, msg.RequestActionPickerMsg{Options: []string{"Stop"}})
		}, func(t *testing.T, v *replyView) {
			assert.Equal(t, []msg.PickerResultMsg{{ID: "action", Outcome: msg.PickCancelled}}, v.picks)
		}},
		{"sort picker", func(t *testing.T, m Model, v *replyView) Model {
			return ask(t, m, v, msg.RequestSortPickerMsg{Columns: []string{"Name"}})
		}, func(t *testing.T, v *replyView) {
			assert.Equal(t, []msg.PickerResultMsg{{ID: "sort", Outcome: msg.PickCancelled}}, v.picks)
		}},
		{"discard confirm", func(t *testing.T, m Model, v *replyView) Model {
			return ask(t, m, v, discard)
		}, func(t *testing.T, v *replyView) {
			require.Len(t, v.confirms, 1)
			assert.False(t, v.confirms[0].Confirmed)
			assert.Same(t, discard.Tag, v.confirms[0].Tag, "the decline carries the tag of the request")
		}},
	} {
		for _, open := range []string{"confirm", "picker"} {
			t.Run(c.name+" over "+open, func(t *testing.T) {
				m := newTestModel(140, 40)
				v := newReplyView()
				m.pushView(v)
				if open == "confirm" {
					m.askQuick("Open?", func(m Model) (Model, tea.Cmd) { return m, nil }, nil)
				} else {
					m.pick("Open", []ui.PickerOption{{Label: "a", Value: "a"}}, 0, onPicked(func(m Model, _ string) (Model, tea.Cmd) { return m, nil }))
				}
				before := kindsOf(m.overlays)
				title := m.confirm.View() + m.picker.View()

				m = c.ask(t, m, v)
				assert.Equal(t, before, kindsOf(m.overlays), "the open prompt stays alone")
				assert.NotNil(t, v.lastCmd, "the update returned the refusal")
				assert.Equal(t, title, m.confirm.View()+m.picker.View(), "the open prompt does not change")
				assert.True(t, m.toasts.HasActive())
				c.want(t, v)
			})
		}
	}
}

// A prompt of the app that comes while a prompt is open does not open
// either. Its handler gets the decline.
func TestAppPromptIsRefusedWhileAPromptIsOpen(t *testing.T) {
	m := newTestModel(140, 40)
	m.askQuick("Open?", func(m Model) (Model, tea.Cmd) { return m, nil }, nil)

	var no, cancelled int
	cmd := m.askQuick("Second?", func(m Model) (Model, tea.Cmd) { return m, nil },
		func(m Model) (Model, tea.Cmd) { no++; return m, nil })
	assert.NotNil(t, cmd, "a toast")
	cmd = m.pick("Pick", []ui.PickerOption{{Label: "a", Value: "a"}}, 0, func(m Model, r msg.PickerResultMsg) (Model, tea.Cmd) {
		if !r.Picked() {
			cancelled++
		}
		return m, nil
	})
	assert.NotNil(t, cmd)
	assert.Equal(t, 1, no)
	assert.Equal(t, 1, cancelled)
	assert.False(t, m.picker.Visible())
	assert.Equal(t, []overlayKind{ovConfirm}, kindsOf(m.overlays))

	m = startTestOp(t, m)
	cmd = m.askQuit("Quit?")
	assert.False(t, m.quitAsked(), "the quit confirm does not replace the open one")
	for _, out := range collectMsgs(cmd) {
		assert.NotEqual(t, tea.QuitMsg{}, out, "a refused quit confirm is a no")
	}
	require.NoError(t, m.ops.Running()[0].Ctx.Err(), "the operation keeps running")
	m.ops.CancelAll()
}

// A prompt that a view asks for while it is not on top never shows. The
// user gets a toast, and the view gets a decline. Before PR 7, a picker or
// a discard request from below the top was dropped without a word.
func TestPromptFromBelowTheTopGetsAToastAndADecline(t *testing.T) {
	// The form sends this request. The test sends it from a view below
	// the top, so that view gets the decline.
	discard := newDiscardRequest(t)
	for _, c := range []struct {
		name string
		req  tea.Msg
		want func(t *testing.T, v *replyView)
	}{
		{"action picker", msg.RequestActionPickerMsg{Options: []string{"Stop"}}, func(t *testing.T, v *replyView) {
			assert.Equal(t, []msg.PickerResultMsg{{ID: "action", Outcome: msg.PickCancelled}}, v.picks)
		}},
		{"sort picker", msg.RequestSortPickerMsg{Columns: []string{"Name"}}, func(t *testing.T, v *replyView) {
			assert.Equal(t, []msg.PickerResultMsg{{ID: "sort", Outcome: msg.PickCancelled}}, v.picks)
		}},
		{"discard confirm", discard, func(t *testing.T, v *replyView) {
			require.Len(t, v.confirms, 1)
			assert.False(t, v.confirms[0].Confirmed)
			assert.Same(t, discard.Tag, v.confirms[0].Tag, "the decline carries the tag of the request")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := newTestModel(140, 40)
			below := newReplyView()
			m.pushView(below)
			m.pushView(newReplyView())

			m, cmd := update(t, m, nav.Routed{Origin: below, Msg: c.req})
			assert.False(t, m.confirm.Visible())
			assert.False(t, m.picker.Visible())
			assert.True(t, m.toasts.HasActive())
			assert.NotNil(t, cmd, "the update returned the refusal")
			c.want(t, below)
		})
	}
}

// A prompt request from a view that is gone gets nothing: no toast about a
// view that the user left, and no decline.
func TestPromptFromAGoneViewIsDropped(t *testing.T) {
	for _, req := range []tea.Msg{
		msg.RequestConfirmMsg{Message: "Go?", Action: "go"},
		msg.RequestActionPickerMsg{Options: []string{"Stop"}},
		newDiscardRequest(t),
	} {
		m := newTestModel(140, 40)
		gone := newReplyView()
		m, _ = update(t, m, nav.Routed{Origin: gone, Msg: req})
		assert.False(t, m.toasts.HasActive(), "%T", req)
		assert.Empty(t, gone.picks)
		assert.Empty(t, gone.confirms)
	}
}

// The app sends the feature picker request to itself, after a pick in the
// service picker, so it has no origin. It follows the same rule as every
// prompt request while a prompt is open.
func TestFeaturePickerRequestIsRefusedWhileAPromptIsOpen(t *testing.T) {
	m := newTestModel(140, 40)
	m.askQuit("Quit?")
	require.True(t, m.quitAsked())

	m, cmd := update(t, m, msg.RequestFeaturePickerMsg{Service: "EC2", Labels: []string{"A"}, ViewIDs: []string{"a"}})
	assert.True(t, m.quitAsked(), "the quit confirm stays")
	assert.False(t, m.picker.Visible())
	assert.Equal(t, []overlayKind{ovConfirm}, kindsOf(m.overlays))
	assert.True(t, m.toasts.HasActive())
	assert.NotNil(t, cmd)
}

// A show function of the app returns the refusal, so its caller can pass
// the toast on.
func TestAppPickerReturnsItsRefusal(t *testing.T) {
	m := newTestModel(140, 40)
	m.askQuit("Quit?")
	for name, show := range map[string]func(*Model) tea.Cmd{
		"theme": (*Model).showThemePicker, "region": (*Model).showRegionPicker,
		"mode": (*Model).showModePicker, "service": (*Model).showServicePicker,
	} {
		assert.NotNil(t, show(&m), name)
		assert.False(t, m.picker.Visible(), name)
	}
}
