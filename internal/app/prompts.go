package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/eventlog"
	appmsg "github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The prompts of the app itself use handlers, not action strings or picker
// IDs, so a reply to a prompt of a view can never run code of the app.

// One rule covers every prompt request while a prompt is open: the app
// does not show the new prompt, the user gets a toast, and the route of the
// new prompt gets one decline (decision D3). A prompt that a view asks for
// while the view is not on top gets the same treatment. The open prompt
// keeps the keys, so a key meant for it, such as y to quit, never answers
// another question.

// promptOpen reports whether a confirm or a picker is open. Help and the
// command bar are not prompts, so a prompt may open over them.
func (m Model) promptOpen() bool {
	return m.confirm.Visible() || m.picker.Visible()
}

// refuseOpen refuses a prompt because another prompt is open.
func (m *Model) refuseOpen(text string, route replyRoute, decline tea.Msg) tea.Cmd {
	return m.refusePrompt("Not asked, another prompt is open: "+text, route, decline)
}

// refuseBelowTop refuses a prompt of a view that is not on top. A prompt
// over another view would be confusing, but the user must learn that the
// view needs an answer.
func (m *Model) refuseBelowTop(asker nav.View, text string, decline tea.Msg) tea.Cmd {
	return m.refusePrompt(asker.Title()+" needs an answer, but it is not on top: "+text, replyRoute{replyTo: asker}, decline)
}

// refusePrompt shows why as a toast and sends decline on route. A question
// that was not asked counts as a no, so the asker forgets the action that
// waits for the answer.
func (m *Model) refusePrompt(why string, route replyRoute, decline tea.Msg) tea.Cmd {
	eventlog.Warnf(eventlog.CatUI, "Prompt skipped: %s", why)
	_, toast := m.toasts.Add(why, ui.ToastInfo, 0)
	var reply tea.Cmd
	switch {
	case route.onReply != nil:
		*m, reply = route.onReply(*m, decline)
	case route.replyTo != nil && m.nav.Contains(route.replyTo):
		reply = m.nav.UpdateView(route.replyTo, decline)
	}
	return tea.Batch(toast, reply)
}

// askQuick shows a y/n confirm of the app. onYes runs on a yes. onNo runs
// on any other reply, also a decline, and may be nil.
func (m *Model) askQuick(message string, onYes, onNo func(Model) (Model, tea.Cmd)) tea.Cmd {
	route := replyRoute{onReply: confirmHandler(onYes, onNo)}
	if m.promptOpen() {
		return m.refuseOpen(message, route, appmsg.ConfirmResultMsg{})
	}
	m.confirm.ShowQuick(message, "")
	m.openOverlayRoute(ovConfirm, route)
	return nil
}

// askQuit shows the quit confirm. A second ctrl+c quits at once.
func (m *Model) askQuit(message string) tea.Cmd {
	route := replyRoute{
		onReply: confirmHandler(func(m Model) (Model, tea.Cmd) { return m.quitNow() }, nil),
		quit:    true,
	}
	if m.promptOpen() {
		return m.refuseOpen(message, route, appmsg.ConfirmResultMsg{})
	}
	m.confirm.ShowQuick(message, "")
	m.openOverlayRoute(ovConfirm, route)
	return nil
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
// a pick, a cancel, and a decline.
func (m *Model) pick(title string, options []ui.PickerOption, initialIdx int, onPick func(Model, appmsg.PickerResultMsg) (Model, tea.Cmd)) tea.Cmd {
	route := replyRoute{onReply: func(m Model, reply tea.Msg) (Model, tea.Cmd) {
		if r, ok := reply.(appmsg.PickerResultMsg); ok {
			return onPick(m, r)
		}
		return m, nil
	}}
	if m.promptOpen() {
		return m.refuseOpen(title, route, pickDecline(""))
	}
	m.picker.Show("", title, options, initialIdx)
	m.openOverlayRoute(ovPicker, route)
	return nil
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
