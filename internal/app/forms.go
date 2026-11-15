package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/eventlog"
	appmsg "github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// formRoute records an open form and the view that asked for it. A form is
// a view on the navigation stack, not an overlay.
type formRoute struct {
	form  *ui.FormView
	asker nav.View // nil for a request without an origin
}

// openForm pushes the form that req asks for and records asker. The app
// keeps only the routes of forms that are still on the stack. It makes a
// new slice, so two copies of Model never share one.
func (m Model) openForm(req appmsg.RequestFormMsg, asker nav.View) (Model, tea.Cmd) {
	fv := ui.NewFormView(req)
	m.dismissPanel()
	cmd := m.pushView(fv)
	forms := make([]formRoute, 0, len(m.forms)+1)
	for _, f := range m.forms {
		if m.nav.Contains(f.form) {
			forms = append(forms, f)
		}
	}
	forms = append(forms, formRoute{form: fv, asker: asker})
	m.forms = forms
	return m, cmd
}

// finishForm pops form and sends its result to the view that asked for it.
// The caller made sure that form is on top. If the asker is gone, the
// result is dropped. A form without a recorded asker sends its result to
// the view below it, as before.
func (m Model) finishForm(form *ui.FormView, res appmsg.FormResultMsg) (Model, tea.Cmd) {
	var asker nav.View
	forms := make([]formRoute, 0, len(m.forms))
	for _, f := range m.forms {
		if f.form == form {
			asker = f.asker
			continue
		}
		forms = append(forms, f)
	}
	m.forms = forms
	m.nav.Pop()
	m.resizeViews()
	if asker == nil {
		return m, m.nav.UpdateCurrent(res)
	}
	if !m.nav.Contains(asker) {
		eventlog.Debugf(eventlog.CatNav, "Dropped %T for %s: view no longer exists", res, asker.ID())
		return m, nil
	}
	return m, m.nav.UpdateView(asker, res)
}

// askDiscard asks whether to discard the changes of a form. The reply goes
// to form, which closes itself on a yes. A nil form means a request
// without an origin, and its reply goes the old way.
func (m Model) askDiscard(form nav.View) Model {
	m.confirm.ShowQuick("Discard unsaved changes?", ui.FormDiscardAction)
	m.openOverlayFor(ovConfirm, form)
	return m
}
