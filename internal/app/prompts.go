package app

import (
	tea "charm.land/bubbletea/v2"

	appmsg "github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The prompts of the app itself use handlers, not action strings or picker
// IDs, so a reply to a prompt of a view can never run code of the app.

// askQuick shows a y/n confirm of the app. onYes runs on a yes. onNo runs
// on any other reply, also the decline of closePrompts, and may be nil.
func (m *Model) askQuick(message string, onYes, onNo func(Model) (Model, tea.Cmd)) {
	m.confirm.ShowQuick(message, "")
	m.openOverlayRoute(ovConfirm, replyRoute{onReply: confirmHandler(onYes, onNo)})
}

// askQuit shows the quit confirm. A second ctrl+c quits at once.
func (m *Model) askQuit(message string) {
	m.confirm.ShowQuick(message, "")
	m.openOverlayRoute(ovConfirm, replyRoute{
		onReply: confirmHandler(func(m Model) (Model, tea.Cmd) { return m.quitNow() }, nil),
		quit:    true,
	})
}

func confirmHandler(onYes, onNo func(Model) (Model, tea.Cmd)) replyHandler {
	return func(m Model, reply tea.Msg) (Model, tea.Cmd) {
		if r, ok := reply.(appmsg.ConfirmResultMsg); ok && r.Confirmed {
			return onYes(m)
		}
		if onNo != nil {
			return onNo(m)
		}
		return m, nil
	}
}

// pick shows a picker of the app. onPick gets every reply of the picker:
// a pick, a cancel, and the decline of closePrompts.
func (m *Model) pick(title string, options []ui.PickerOption, initialIdx int, onPick func(Model, appmsg.PickerResultMsg) (Model, tea.Cmd)) {
	m.picker.Show("", title, options, initialIdx)
	m.openOverlayRoute(ovPicker, replyRoute{onReply: func(m Model, reply tea.Msg) (Model, tea.Cmd) {
		if r, ok := reply.(appmsg.PickerResultMsg); ok {
			return onPick(m, r)
		}
		return m, nil
	}})
}

// onPicked returns a pick handler that runs f with the picked value and
// does nothing on a cancel.
func onPicked(f func(m Model, value string) (Model, tea.Cmd)) func(Model, appmsg.PickerResultMsg) (Model, tea.Cmd) {
	return func(m Model, r appmsg.PickerResultMsg) (Model, tea.Cmd) {
		if !r.Picked() {
			return m, nil
		}
		return f(m, r.Value)
	}
}

// quitAsked reports whether the quit confirm is open.
func (m Model) quitAsked() bool {
	e, ok := m.overlays.find(ovConfirm)
	return ok && e.route.quit && m.confirm.Visible()
}

// closePrompts sends a decline on the route of each open prompt, so every
// prompt gets exactly one reply. Call it before the app throws the prompt
// components away. The declines arrive after the current update.
func (m Model) closePrompts() tea.Cmd {
	var cmds []tea.Cmd
	for _, e := range m.overlays {
		if !e.route.routed() || !m.overlay(e.kind).Visible() {
			continue
		}
		var decline tea.Msg
		switch e.kind {
		case ovConfirm:
			decline = appmsg.ConfirmResultMsg{Confirmed: false, Action: m.confirm.Action()}
		case ovPicker:
			decline = appmsg.PickerResultMsg{ID: m.picker.ID(), Outcome: appmsg.PickCancelled, Selected: -1}
		default:
			continue
		}
		reply := overlayReplyMsg{route: e.route, msg: decline}
		cmds = append(cmds, func() tea.Msg { return reply })
	}
	return tea.Batch(cmds...)
}
