package app

import (
	"fmt"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/ops"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// opsTickInterval is the time between two redraws of the operations line.
// The line shows progress, which changes without a message to the app.
const opsTickInterval = 500 * time.Millisecond

// opsTickMsg redraws the operations line while operations run.
type opsTickMsg struct{}

func opsTick() tea.Cmd {
	return tea.Tick(opsTickInterval, func(time.Time) tea.Msg { return opsTickMsg{} })
}

// cancelPickerID is the picker that asks which operation to cancel.
const cancelPickerID = "cancel_op"

// maxOpTextWidth limits the text of one operation in the cancel picker.
const maxOpTextWidth = 70

// opText describes a running operation, for example
// "Download a/b.zip 4.0 MB / 10 MB (40%)". It names the session of the
// operation when that is not the current session.
func opText(op ops.Op, session string, now time.Time) string {
	text := op.Label + " " + progressText(op, now)
	if op.Session != session {
		text += " [" + op.Session + "]"
	}
	return text
}

// progressText is the progress part of opText. Without progress, it shows
// the time since the operation started.
func progressText(op ops.Op, now time.Time) string {
	p := op.Progress
	count := func(n int64) string {
		if p.Bytes {
			return aws.FormatBytes(n)
		}
		return strconv.FormatInt(n, 10)
	}
	switch {
	case op.Canceling:
		return "cancelling..."
	case p.Total > 0:
		return fmt.Sprintf("%s / %s (%d%%)", count(p.Done), count(p.Total), p.Done*100/p.Total)
	case p.Done > 0:
		return count(p.Done)
	}
	return now.Sub(op.Started).Round(time.Second).String()
}

// renderOpsLine renders the line above the status bar that shows the
// running operations: the oldest one, how many more run, and the key that
// cancels them. It returns "" when no operation runs.
func renderOpsLine(running []ops.Op, session string, width int, now time.Time) string {
	if len(running) == 0 {
		return ""
	}
	s := ui.S
	text := " " + ui.Sanitize(opText(running[0], session, now))
	if n := len(running) - 1; n > 0 {
		text += fmt.Sprintf(" · +%d more", n)
	}
	hint := s.StatusKey.Render("ctrl+x") + " " + s.StatusDesc.Render("cancel") + " "
	room := max(width-lipgloss.Width(hint)-1, 0)
	left := s.StatusDesc.Render(ansi.Truncate(text, room, "…"))
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(hint), 1)
	return left + s.StatusBarBase.Width(gap).Render("") + hint
}

// syncOpsLine shows the operations line when operations run and hides it
// when none runs. The line takes one row from the views, so it resizes them
// when it comes or goes.
func (m *Model) syncOpsLine() {
	show := m.ops.Count() > 0
	if show == m.opsShown {
		return
	}
	m.opsShown = show
	m.resizeViews()
}

// showCancelPicker asks which running operation to cancel.
func (m Model) showCancelPicker() (Model, tea.Cmd) {
	running := m.ops.Running()
	if len(running) == 0 {
		_, cmd := m.toasts.Add("No operation is running", ui.ToastInfo, 0)
		return m, cmd
	}
	now := time.Now()
	session := m.sessionLabel()
	options := make([]ui.PickerOption, len(running))
	for i, op := range running {
		options[i] = ui.PickerOption{
			Label: ansi.Truncate(opText(op, session, now), maxOpTextWidth, "…"),
			Value: strconv.Itoa(op.ID),
		}
	}
	m.picker.Show(cancelPickerID, "Cancel which operation?", options, 0)
	m.picker.SetHint("↑↓ navigate  enter cancel it  esc keep running")
	return m, nil
}

// cancelOp cancels the operation that the user picked. The operation ends
// on its own and reports its result.
func (m Model) cancelOp(r ui.PickerResultMsg) (Model, tea.Cmd) {
	if r.Selected < 0 {
		return m, nil
	}
	id, err := strconv.Atoi(r.Value)
	if err != nil {
		return m, nil
	}
	label := ""
	for _, op := range m.ops.Running() {
		if op.ID == id {
			label = op.Label
		}
	}
	if !m.ops.Cancel(id) {
		_, cmd := m.toasts.Add("The operation ended before the cancel", ui.ToastInfo, 0)
		return m, cmd
	}
	eventlog.Infof(eventlog.CatApp, "Cancelling: %s", label)
	_, cmd := m.toasts.Add("Cancelling: "+label, ui.ToastInfo, 0)
	return m, cmd
}
