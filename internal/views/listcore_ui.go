package views

import (
	"fmt"
	"strings"

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
	Select      ui.HintBinding
	Sort        ui.HintBinding
	SortReverse ui.HintBinding
	Filter      ui.HintBinding
	Refresh     ui.HintBinding
}{
	Esc:         ui.NewHintBinding([]string{"esc"}, "esc", "back"),
	Details:     ui.NewHintBinding([]string{"enter"}, "enter/d", "details"),
	Describe:    ui.NewHintBinding([]string{"d"}, "d", "details"),
	CopyID:      ui.NewHintBinding([]string{"y"}, "y", "copy ID"),
	Select:      ui.NewHintBinding([]string{"space"}, "space", "select"),
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

// footer returns footerWith("<shown>/<total> <noun>").
func (c *listCore[T]) footer() string {
	shown, total := c.table.RowCount()
	return c.footerWith(fmt.Sprintf("%d/%d %s", shown, total, c.cfg.Noun))
}

// footerWith returns base, plus "(loading... N so far)" while more pages
// of a load come, plus "(N selected)", plus the spinner while it shows.
// With Complete, the rows change only at the end of the load, so the
// progress is in the spinner text only.
func (c *listCore[T]) footerWith(base string) string {
	s := base
	if c.loading && c.pages > 0 && c.cfg.Complete == nil {
		s += fmt.Sprintf("  (loading... %d so far)", len(c.items))
	}
	if n := c.table.SelectionCount(); n > 0 {
		s += fmt.Sprintf("  (%d selected)", n)
	}
	if c.spinner.Visible() {
		s += "  " + c.spinner.View()
	}
	return s
}

// render draws the spinner on the first load (no rows yet), else the error
// of the last load, else above, the filter when it is open, and the table.
// above is a side input of the view (the AMI search), or "".
func (c *listCore[T]) render(above string) string {
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
	return above + content
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
		if c.cfg.MultiSelect && c.table.SelectionCount() > 0 {
			c.table.DeselectAll()
			return nil, true
		}
		return func() tea.Msg { return msg.NavigateBackMsg{} }, true
	case c.cfg.MultiSelect && key.Matches(m, listKeys.Select.Binding):
		c.table.ToggleSelect()
		return nil, true
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
		if c.cfg.Refresh != nil && c.table.SelectionCount() > 0 {
			return c.refreshKeys(c.targets()), true
		}
		return c.reload(c.cfg.LoadingLabel), true
	case c.cfg.Copy && key.Matches(m, listKeys.CopyID.Binding):
		return c.copyKeys()
	case c.cfg.OpenDetail != nil && key.Matches(m, listKeys.Details.Binding, listKeys.Describe.Binding):
		it := c.cursorItem()
		if it == nil {
			return nil, false
		}
		return c.cfg.OpenDetail(*it), true
	}
	return nil, false
}

// copyKeys copies the targets (MultiSelect) or the cursor key to the
// clipboard, with a toast.
func (c *listCore[T]) copyKeys() (tea.Cmd, bool) {
	if c.cfg.MultiSelect {
		keys := c.targets()
		if len(keys) == 0 {
			return nil, true
		}
		text := fmt.Sprintf("Copied %d %s(s)", len(keys), c.cfg.CopyNoun)
		return tea.Batch(
			tea.SetClipboard(strings.Join(keys, "\n")),
			func() tea.Msg { return msg.ToastSuccess(text) },
		), true
	}
	id := c.table.CursorKey()
	if id == "" {
		return nil, false
	}
	return tea.Batch(
		tea.SetClipboard(id),
		func() tea.Msg { return msg.ToastSuccess("Copied: " + id) },
	), true
}
