package views

import (
	"context"
	"errors"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The shared core of the resource lists. A list view embeds listCore[T] by
// value, sets its policy in a listConfig[T], and keeps its own type,
// constructor and Update. See docs/design/phase8-resource-list.md.

// fetchFunc loads the list. It runs in a tea.Cmd goroutine, so it must not
// read view state that changes. The token and the next token are for paged
// lists; the core does not page yet, so it passes nil and ignores next.
type fetchFunc[T any] func(ctx context.Context, token *string) (items []T, next *string, err error)

// listConfig is the policy of one list. The first block is required:
// newListCore panics if a field of it is missing (FetchTimeout may be 0).
//
// OpenDetail runs inside the core's handle. It must not call update again.
type listConfig[T any] struct {
	Noun         string // footer and log noun: "security groups"
	LoadingLabel string // spinner text of a full load: "Loading security groups..."
	ErrContext   string // msg.ErrorMsg.Context of a failed load: "listing security groups"
	Key          func(T) string
	Columns      func(ui.WidthTier) []ui.Column
	Row          func(T, ui.WidthTier) ui.Row // Row.Key must equal Key(t)
	Fetch        fetchFunc[T]                 // build with fetchAll
	FetchTimeout time.Duration                // listTimeout for a whole-list call; 0 = none

	OpenDetail func(T) tea.Cmd // enter/d and focus; nil: the keys go to the table
	Copy       bool            // y copies the key of the cursor row: "Copied: <key>"
}

// listCore is the state and behavior the lists share. It has no Update,
// View, Init, ID, Title, KeyMap or Footer, so the core alone is never a
// nav.View, and each view declares its own Update that returns the view
// pointer. A view must not declare a field or a method with the name of one
// of the core's, and must not embed viewCtx itself
// (list_conformance_test.go checks this).
type listCore[T any] struct {
	viewCtx // the only viewCtx in the view; promotes SetContext

	cfg listConfig[T]

	table   ui.Table
	filter  ui.Filter
	spinner ui.Spinner

	items []T // what the table shows; kept during a reload

	loading      bool
	loadGen      int
	err          error // list-load error only; replaces the table in render
	width        int
	height       int
	widthTier    ui.WidthTier
	pendingFocus string // resource ID to open after the load ends
}

// newListCore builds the core in the loading state. It panics when a
// required config field is missing, because that is a programming error.
func newListCore[T any](cfg listConfig[T]) listCore[T] {
	for name, missing := range map[string]bool{
		"Noun":         cfg.Noun == "",
		"LoadingLabel": cfg.LoadingLabel == "",
		"ErrContext":   cfg.ErrContext == "",
		"Key":          cfg.Key == nil,
		"Columns":      cfg.Columns == nil,
		"Row":          cfg.Row == nil,
		"Fetch":        cfg.Fetch == nil,
	} {
		if missing {
			panic("views: listConfig." + name + " is required")
		}
	}
	return listCore[T]{
		cfg:       cfg,
		table:     ui.NewTable(cfg.Columns(ui.TierMedium)),
		filter:    ui.NewFilter(),
		spinner:   ui.NewSpinner(cfg.LoadingLabel),
		loading:   true,
		widthTier: ui.TierMedium,
	}
}

// --- lifecycle ---

// init deselects and starts the first load. A view that is shown again
// (loading is false) does not reload.
func (c *listCore[T]) init() tea.Cmd {
	c.table.DeselectAll()
	if !c.loading {
		return nil
	}
	return tea.Batch(c.spinner.Tick(), c.startLoad())
}

// startLoad starts a new load generation and returns its fetch cmd. The
// items stay, so the table stays on screen and usable during a reload.
func (c *listCore[T]) startLoad() tea.Cmd {
	c.loadGen++
	return c.fetchCmd()
}

// reload starts a full load with the loading label in the spinner.
func (c *listCore[T]) reload() tea.Cmd {
	c.loading = true
	c.err = nil
	c.spinner.Show(c.cfg.LoadingLabel)
	return tea.Batch(c.spinner.Tick(), c.startLoad())
}

// fetchCmd captures the generation, the config and the view context on the
// Update goroutine.
func (c *listCore[T]) fetchCmd() tea.Cmd {
	gen := c.loadGen
	fetch, timeout := c.cfg.Fetch, c.cfg.FetchTimeout
	noun, errContext := c.cfg.Noun, c.cfg.ErrContext
	ctx := c.viewContext()
	return func() tea.Msg {
		eventlog.Debugf(eventlog.CatAWS, "Fetching %s", noun)
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		items, _, err := fetch(ctx, nil)
		if err != nil {
			return listFailedMsg{gen: gen, err: err, context: errContext}
		}
		return listPageMsg[T]{gen: gen, items: items}
	}
}

// finishLoad ends a successful load and opens a focus that waited for it.
func (c *listCore[T]) finishLoad() tea.Cmd {
	c.loading = false
	c.err = nil
	c.spinner.Hide()
	eventlog.Infof(eventlog.CatAWS, "Loaded %d %s", len(c.items), c.cfg.Noun)
	if c.pendingFocus == "" {
		return nil
	}
	id := c.pendingFocus
	c.pendingFocus = ""
	return c.focus(id)
}

// --- entry points for the view's Update ---

// update handles m, or hands it to passThrough.
func (c *listCore[T]) update(m tea.Msg) tea.Cmd {
	if cmd, ok := c.handle(m); ok {
		return cmd
	}
	return c.passThrough(m)
}

// handle handles the core messages and keys. It returns false for a
// message that is not the core's, so the view can handle it.
func (c *listCore[T]) handle(m tea.Msg) (tea.Cmd, bool) {
	switch m := m.(type) {
	case listPageMsg[T]:
		if m.gen != c.loadGen {
			return nil, true // superseded by a newer load
		}
		c.setItems(m.items)
		return c.finishLoad(), true
	case listFailedMsg:
		return c.onFailed(m), true
	case msg.ErrorMsg:
		if _, ok := errors.AsType[*loadError](m.Err); ok {
			return nil, true // our own echo; listFailedMsg already applied it
		}
		// A bare ErrorMsg (tests) is applied as before.
		c.loading, c.err = false, m.Err
		c.spinner.Hide()
		return nil, true
	case msg.FocusResourceMsg:
		if c.loading {
			c.pendingFocus = m.ResourceID
			return nil, true
		}
		return c.focus(m.ResourceID), true
	case tea.WindowSizeMsg:
		c.resize(m)
		return nil, true
	case ui.FilterChangedMsg:
		if m.Source != "" {
			return nil, true // a side input, not the list filter
		}
		c.table.Filter(m.Text)
		return nil, true
	case ui.PickerResultMsg:
		if m.ID != "sort" {
			return nil, false
		}
		if m.Value == "_clear" {
			c.table.ClearSort()
		} else if m.Selected >= 0 {
			c.table.Sort(m.Selected)
		}
		return nil, true
	case tea.KeyPressMsg:
		return c.handleKey(m)
	}
	return nil, false
}

// onFailed applies a failure of the current load and turns it into the
// msg.ErrorMsg for the app. A failure of a superseded load is dropped, so it
// reaches neither the view nor the status bar. The pending focus is kept for
// the next good load.
func (c *listCore[T]) onFailed(m listFailedMsg) tea.Cmd {
	if m.gen != c.loadGen {
		eventlog.Debugf(eventlog.CatAWS, "Dropped stale %s error", m.context)
		return nil
	}
	c.loading, c.err = false, m.err
	c.spinner.Hide()
	e := msg.ErrorMsg{Err: &loadError{err: m.err}, Context: m.context}
	return func() tea.Msg { return e }
}

// resize fits the columns to the width. It rebuilds the rows when the tier
// or the column count changes; the table keeps cursor, selection and sort.
func (c *listCore[T]) resize(m tea.WindowSizeMsg) {
	c.width, c.height = m.Width, m.Height
	cols, tier := ui.BestFitTier(m.Width, c.cfg.Columns)
	if tier != c.widthTier || len(cols) != len(c.table.Columns()) {
		c.widthTier = tier
		c.table.SetColumns(cols)
		c.table.SetRows(c.rows(c.items))
	}
	c.table.SetSize(m.Width, m.Height-3)
	c.filter.SetWidth(m.Width)
}

// passThrough gives a spinner tick to the spinner, so it animates whenever
// it is visible, and every other message to the table, so the table stays
// usable during a load.
func (c *listCore[T]) passThrough(m tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	if _, ok := m.(spinner.TickMsg); ok {
		c.spinner, cmd = c.spinner.Update(m)
		return cmd
	}
	c.table, cmd = c.table.Update(m)
	return cmd
}

// --- data ---

// find returns the item with the given key, or nil.
func (c *listCore[T]) find(key string) *T {
	for i := range c.items {
		if c.cfg.Key(c.items[i]) == key {
			return &c.items[i]
		}
	}
	return nil
}

// cursorItem returns the item under the cursor, or nil.
func (c *listCore[T]) cursorItem() *T {
	key := c.table.CursorKey()
	if key == "" {
		return nil
	}
	return c.find(key)
}

// setItems replaces the items and the table rows. The table keeps cursor,
// selection and sort by key.
func (c *listCore[T]) setItems(items []T) {
	c.items = items
	c.table.SetRows(c.rows(items))
}

func (c *listCore[T]) rows(items []T) []ui.Row {
	rows := make([]ui.Row, 0, len(items))
	for _, it := range items {
		rows = append(rows, c.cfg.Row(it, c.widthTier))
	}
	return rows
}

// focus moves the cursor to the row with the given ID and opens its detail.
// A row the filter hides keeps the cursor where it is, but its detail still
// opens. An ID that is not in the list does nothing.
func (c *listCore[T]) focus(id string) tea.Cmd {
	c.table.SetCursorByKey(id)
	it := c.find(id)
	if it == nil || c.cfg.OpenDetail == nil {
		return nil
	}
	return c.cfg.OpenDetail(*it)
}
