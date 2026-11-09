package views

import (
	"fmt"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// listKeys are the keys of the shared list core. A view lists the ones it
// shows in its KeyMap, through hints.
var listKeys = struct {
	Esc         ui.HintBinding
	Details     ui.HintBinding
	Describe    ui.HintBinding
	CopyID      ui.HintBinding
	Sort        ui.HintBinding
	SortReverse ui.HintBinding
	Filter      ui.HintBinding
	Refresh     ui.HintBinding
}{
	Esc:         ui.NewHintBinding([]string{"esc"}, "esc", "back"),
	Details:     ui.NewHintBinding([]string{"enter"}, "enter/d", "details"),
	Describe:    ui.NewHintBinding([]string{"d"}, "d", "details"),
	CopyID:      ui.NewHintBinding([]string{"y"}, "y", "copy ID"),
	Sort:        ui.NewHintBinding([]string{"s"}, "s/S", "sort"),
	SortReverse: ui.NewHintBinding([]string{"S"}, "S", "reverse sort"),
	Filter:      ui.NewHintBinding([]string{"/"}, "/", "filter"),
	Refresh:     ui.NewHintBinding([]string{"r"}, "r", "refresh"),
}

// CapturingInput implements nav.InputCapturer: true while the filter is open.
func (c *listCore[T]) CapturingInput() bool { return c.filter.Active() }

// hints returns a copy of bs with the ReadOnly mode applied, for KeyMap.
func (c *listCore[T]) hints(bs ...ui.HintBinding) []ui.HintBinding {
	out := append([]ui.HintBinding(nil), bs...)
	ui.ApplyModeAll(out)
	return out
}

// footer returns "<shown>/<total> <noun>", plus the spinner while it shows.
func (c *listCore[T]) footer() string {
	shown, total := c.table.RowCount()
	s := fmt.Sprintf("%d/%d %s", shown, total, c.cfg.Noun)
	if c.spinner.Visible() {
		s += "  " + c.spinner.View()
	}
	return s
}

// render draws the spinner on the first load (no rows yet), else the error
// of the last load, else the table with the filter above it when open.
func (c *listCore[T]) render() string {
	switch {
	case c.loading && len(c.items) == 0:
		return "\n  " + c.spinner.View()
	case c.err != nil:
		return "\n  " + ui.S.Error.Render("Error: "+ui.Sanitize(aws.ErrorText(c.err)))
	}
	content := c.table.View()
	if c.filter.Active() {
		content = c.filter.View() + "\n" + content
	}
	return content
}

// handleKey handles the core keys. It returns false for a key that is not
// the core's, so the view or the table can handle it.
func (c *listCore[T]) handleKey(m tea.KeyPressMsg) (tea.Cmd, bool) {
	if c.filter.Active() {
		var cmd tea.Cmd
		c.filter, cmd = c.filter.Update(m)
		return cmd, true
	}
	switch {
	case key.Matches(m, listKeys.Esc.Binding):
		return func() tea.Msg { return msg.NavigateBackMsg{} }, true
	case key.Matches(m, listKeys.Sort.Binding):
		columns, current := c.table.SortColumnNames()
		return func() tea.Msg {
			return msg.RequestSortPickerMsg{Columns: columns, CurrentCol: current}
		}, true
	case key.Matches(m, listKeys.SortReverse.Binding):
		c.table.SortReverse()
		return nil, true
	case key.Matches(m, listKeys.Filter.Binding):
		c.filter.Activate()
		return nil, true
	case key.Matches(m, listKeys.Refresh.Binding):
		return c.reload(), true
	case c.cfg.Copy && key.Matches(m, listKeys.CopyID.Binding):
		id := c.table.CursorKey()
		if id == "" {
			return nil, false
		}
		return tea.Batch(
			tea.SetClipboard(id),
			func() tea.Msg { return msg.ToastSuccess("Copied: " + id) },
		), true
	case c.cfg.OpenDetail != nil && key.Matches(m, listKeys.Details.Binding, listKeys.Describe.Binding):
		it := c.cursorItem()
		if it == nil {
			return nil, false
		}
		return c.cfg.OpenDetail(*it), true
	}
	return nil, false
}
