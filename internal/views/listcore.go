package views

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The shared core of the resource lists. A list view embeds listCore[T] by
// value, sets its policy in a listConfig[T], and keeps its own type,
// constructor and Update. See docs/design/phase8-resource-list.md.

// fetchFunc loads one page. The core passes nil for the first page and then
// the next token of the page before; a nil next token ends the load. It runs
// in a tea.Cmd goroutine, so it must not read view state that changes.
type fetchFunc[T any] func(ctx context.Context, token *string) (items []T, next *string, err error)

// refreshFunc reloads the items with the given keys. It returns what it
// read, also when some keys failed. It sets its own timeouts (see
// refreshBy). Like a fetchFunc, it runs in a tea.Cmd goroutine.
type refreshFunc[T any] func(ctx context.Context, keys []string) ([]T, error)

// loadSpec is one full load: what to call and how to report it. The core
// keeps the spec of the current load. Stale replies are dropped by gen, so
// the load that finishes is always the kept one.
type loadSpec[T any] struct {
	fetch      fetchFunc[T]
	timeout    time.Duration // per page; 0 = none
	errContext string        // msg.ErrorMsg.Context of a failure
	label      string        // spinner text
	tag        string        // passed to OnLoaded; "" for the default load
}

// completePhase is a second load phase (the SQS attributes). With it, the
// pages collect in a buffer, Run turns the buffer into the items, and the
// rows change only when Run returns.
type completePhase[T any] struct {
	Run     func(ctx context.Context, items []T) ([]T, error)
	Timeout time.Duration // for the one Run call; 0 = none
	Label   string        // spinner text, with %d for the count: "Loading attributes for %d queues..."
	Context string        // msg.ErrorMsg.Context of a failed Run: "fetching SQS queue attributes"
}

// listConfig is the policy of one list. The first block is required:
// newListCore panics if a field of it is missing (FetchTimeout may be 0).
//
// OpenDetail, OnLoaded and FocusMissing run inside the core's handle. They
// must not call update again.
type listConfig[T any] struct {
	Noun         string // footer and log noun: "security groups"
	LoadingLabel string // spinner text of the default load: "Loading security groups..."
	ErrContext   string // msg.ErrorMsg.Context of a failed default load: "listing security groups"
	Key          func(T) string
	Columns      func(ui.WidthTier) []ui.Column
	Row          func(T, ui.WidthTier) ui.Row // Row.Key must equal Key(t)
	Fetch        fetchFunc[T]                 // default load; build with fetchAll or fetchPages
	FetchTimeout time.Duration                // per page: callTimeout for a page, listTimeout for a whole-list call; 0 = none

	Complete *completePhase[T]                   // second load phase; nil for every view but sqs
	Derive   func(items []T)                     // sets fields that depend on other rows, after each data change (sqs IsDLQ)
	OnLoaded func(items []T, tag string) tea.Cmd // after a full load ends, with the tag of that load
	Refresh  refreshFunc[T]                      // r with a selection refreshes the targets; nil: r always reloads

	FocusKey     func(T) string          // what a focus ID matches; nil: Key (sqs focuses by name)
	OpenDetail   func(T) tea.Cmd         // enter/d and focus; nil: the keys go to the table
	FocusMissing func(id string) tea.Cmd // focus on an ID that is not in the list; nil: do nothing

	MultiSelect bool   // space selects, esc deselects first, footer "(N selected)"
	Copy        bool   // y copies. MultiSelect: the targets. Single: the cursor key, "Copied: <key>".
	CopyNoun    string // toast noun with MultiSelect: "Copied N <CopyNoun>(s)"
}

// listCore is the state and behavior the lists share. It has no Update,
// View, Init, ID, Title, KeyMap or Footer, so the core alone is never a
// nav.View, and each view declares its own Update that returns the view
// pointer. A view must not declare a field or a method with the name of one
// of the core's, and must not embed viewCtx itself
// (list_conformance_test.go checks this).
type listCore[T any] struct {
	viewCtx // the only viewCtx in the view; promotes SetContext

	cfg  listConfig[T]
	load loadSpec[T] // spec of the current load; reload goes back to the default

	table   ui.Table
	filter  ui.Filter
	spinner ui.Spinner

	items []T // what the table shows; kept during a reload
	pages int // pages of the current load received so far
	buf   []T // with Complete: the pages of the current load

	completing bool // with Complete: the second phase of the current load runs

	// refreshed holds the items that a selective refresh read while the
	// current full load ran. That load may have read them before the
	// refresh did (before a purge, for example), so its result does not
	// replace them.
	refreshed map[string]T

	tasks     int    // side tasks of the view that have not ended (sqs purge and delete)
	taskLabel string // spinner text of the last task started

	loading      bool // the view shows a load: progress in the footer, a focus waits
	running      bool // a full load is in flight, also a background reload
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
	if cfg.Complete != nil && (cfg.Complete.Run == nil || cfg.Complete.Label == "" || cfg.Complete.Context == "") {
		panic("views: listConfig.Complete needs Run, Label and Context")
	}
	c := listCore[T]{
		cfg:       cfg,
		table:     ui.NewTable(cfg.Columns(ui.TierMedium)),
		filter:    ui.NewFilter(),
		spinner:   ui.NewSpinner(cfg.LoadingLabel),
		loading:   true,
		widthTier: ui.TierMedium,
	}
	c.load = c.defaultSpec()
	return c
}

// defaultSpec is the load of the config: the one Init and r start.
func (c *listCore[T]) defaultSpec() loadSpec[T] {
	return loadSpec[T]{
		fetch:      c.cfg.Fetch,
		timeout:    c.cfg.FetchTimeout,
		errContext: c.cfg.ErrContext,
		label:      c.cfg.LoadingLabel,
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

// startLoad starts a new load generation of the current spec and returns
// the fetch cmd of its first page. The items stay, so the table stays on
// screen and usable during a reload; page 1 replaces them.
func (c *listCore[T]) startLoad() tea.Cmd {
	c.loadGen++
	c.pages = 0
	c.buf = nil
	c.completing = false
	c.refreshed = nil
	c.running = true
	return c.fetchCmd(nil, 1)
}

// reload starts the default load with label in the spinner. After another
// load (an AMI search), it goes back to the default.
func (c *listCore[T]) reload(label string) tea.Cmd {
	s := c.defaultSpec()
	s.label = label
	return c.loadWith(s)
}

// backgroundReload starts the default load with label in the spinner, but
// leaves loading and the error as they are: the footer shows no progress,
// and a focus opens at once instead of waiting (the EC2 reload after a
// state change).
func (c *listCore[T]) backgroundReload(label string) tea.Cmd {
	s := c.defaultSpec()
	s.label = label
	c.load = s
	return tea.Batch(c.busy(label), c.startLoad())
}

// loadWith starts a full load of s, with its label in the spinner. It
// replaces the current load, so a reply of the load before is dropped.
func (c *listCore[T]) loadWith(s loadSpec[T]) tea.Cmd {
	c.load = s
	c.loading = true
	c.err = nil
	return tea.Batch(c.busy(s.label), c.startLoad())
}

// fetchCmd fetches one page of the current load. It captures the
// generation, the spec and the view context on the Update goroutine. Each
// page has its own timeout.
func (c *listCore[T]) fetchCmd(token *string, page int) tea.Cmd {
	gen := c.loadGen
	spec := c.load
	noun := c.cfg.Noun
	ctx := c.viewContext()
	return func() tea.Msg {
		eventlog.Debugf(eventlog.CatAWS, "Fetching %s (page %d)", noun, page)
		if spec.timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, spec.timeout)
			defer cancel()
		}
		items, next, err := spec.fetch(ctx, token)
		if err != nil {
			return listFailedMsg{gen: gen, err: err, context: spec.errContext}
		}
		return listPageMsg[T]{gen: gen, items: items, next: next, page: page}
	}
}

// onPage applies a page of the current load. Page 0 or 1 replaces the rows
// (the table keeps cursor, selection and sort by key); a later page is
// appended. Items whose key is already in the list are dropped, so the
// items stay equal to the table rows. With Complete, the pages go to the
// buffer instead, and the last page starts the second phase. A page of a
// superseded load is dropped.
func (c *listCore[T]) onPage(m listPageMsg[T]) tea.Cmd {
	if m.gen != c.loadGen {
		return nil
	}
	c.pages++
	switch {
	case c.cfg.Complete != nil:
		if m.page <= 1 {
			c.buf = nil
		}
		c.buf = append(c.buf, dedupe(c.buf, m.items, c.cfg.Key)...)
	case m.page <= 1:
		c.setItems(c.keepRefreshed(dedupe(nil, m.items, c.cfg.Key)))
	case c.cfg.Derive != nil:
		// A new row can change the derived fields of the rows before it.
		c.setItems(append(c.items, c.keepRefreshed(dedupe(c.items, m.items, c.cfg.Key))...))
	default:
		fresh := c.keepRefreshed(dedupe(c.items, m.items, c.cfg.Key))
		c.items = append(c.items, fresh...)
		c.table.AppendRows(c.rows(fresh))
	}
	if m.next == nil {
		if c.cfg.Complete != nil {
			return c.startComplete()
		}
		return c.finishLoad()
	}
	eventlog.Debugf(eventlog.CatAWS, "Loaded page %d of %s (%d so far)", max(m.page, 1), c.cfg.Noun, c.loadCount())
	return tea.Batch(c.busy(c.loadLabel()), c.fetchCmd(m.next, max(m.page, 1)+1))
}

// startComplete starts the second phase after the last page. Zero items
// end the load at once and clear the rows of the load before.
func (c *listCore[T]) startComplete() tea.Cmd {
	if len(c.buf) == 0 {
		c.setItems(nil)
		return c.finishLoad()
	}
	c.completing = true
	return tea.Batch(c.busy(c.loadLabel()), c.completeCmd(c.buf))
}

// completeCmd runs the second phase on a copy of items. Like fetchCmd, it
// captures the generation and the view context on the Update goroutine.
func (c *listCore[T]) completeCmd(items []T) tea.Cmd {
	gen := c.loadGen
	phase := *c.cfg.Complete
	ctx := c.viewContext()
	in := slices.Clone(items)
	return func() tea.Msg {
		if phase.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, phase.Timeout)
			defer cancel()
		}
		out, err := phase.Run(ctx, in)
		if err != nil {
			return listFailedMsg{gen: gen, err: err, context: phase.Context}
		}
		return listCompleteMsg[T]{gen: gen, items: out}
	}
}

// onComplete applies the second phase: its items replace the rows, and the
// load ends. A reply of a superseded load is dropped.
func (c *listCore[T]) onComplete(m listCompleteMsg[T]) tea.Cmd {
	if m.gen != c.loadGen {
		return nil
	}
	c.completing = false
	c.buf = nil
	c.setItems(c.keepRefreshed(dedupe(nil, m.items, c.cfg.Key)))
	return c.finishLoad()
}

// keepRefreshed replaces each item of a load result with the item that a
// selective refresh read during that load. items is a new slice (dedupe),
// so it is changed in place.
func (c *listCore[T]) keepRefreshed(items []T) []T {
	if len(c.refreshed) == 0 {
		return items
	}
	for i, it := range items {
		if r, ok := c.refreshed[c.cfg.Key(it)]; ok {
			items[i] = r
		}
	}
	return items
}

// loadCount is the number of items the running load has received so far.
func (c *listCore[T]) loadCount() int {
	if c.cfg.Complete != nil {
		return len(c.buf)
	}
	return len(c.items)
}

// loadLabel is the spinner text of the running load: its label, the
// progress once a page arrived with more to come, or the text of the
// second phase.
func (c *listCore[T]) loadLabel() string {
	switch {
	case c.completing:
		return fmt.Sprintf(c.cfg.Complete.Label, len(c.buf))
	case c.pages == 0:
		return c.load.label
	}
	return fmt.Sprintf("Loading %s... %d so far", c.cfg.Noun, c.loadCount())
}

// dedupe returns the items of fresh whose key is neither in have nor
// earlier in fresh. It always returns a new slice.
func dedupe[T any](have, fresh []T, key func(T) string) []T {
	seen := make(map[string]bool, len(have)+len(fresh))
	for _, it := range have {
		seen[key(it)] = true
	}
	out := make([]T, 0, len(fresh))
	for _, it := range fresh {
		k := key(it)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, it)
	}
	return out
}

// finishLoad ends a successful load, runs OnLoaded with the tag of the
// load, and then opens a focus that waited for the load.
func (c *listCore[T]) finishLoad() tea.Cmd {
	c.loading = false
	c.running = false
	c.completing = false
	c.refreshed = nil
	c.err = nil
	c.spinner.Hide()
	eventlog.Infof(eventlog.CatAWS, "Loaded %d %s (%d pages)", len(c.items), c.cfg.Noun, c.pages)
	var cmds []tea.Cmd
	if c.cfg.OnLoaded != nil {
		cmds = append(cmds, c.cfg.OnLoaded(c.items, c.load.tag))
	}
	if c.pendingFocus != "" {
		id := c.pendingFocus
		c.pendingFocus = ""
		cmds = append(cmds, c.focus(id))
	}
	return tea.Batch(cmds...)
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
		return c.onPage(m), true
	case listCompleteMsg[T]:
		return c.onComplete(m), true
	case listRefreshedMsg[T]:
		return c.onRefreshed(m), true
	case listFailedMsg:
		return c.onFailed(m), true
	case msg.ErrorMsg:
		if _, ok := errors.AsType[*loadError](m.Err); ok {
			return nil, true // our own echo; listFailedMsg already applied it
		}
		// A bare ErrorMsg (tests) is applied as before.
		c.loading, c.running, c.completing, c.err = false, false, false, m.Err
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
	c.loading, c.running, c.completing, c.err = false, false, false, m.err
	c.spinner.Hide()
	e := msg.ErrorMsg{Err: &loadError{err: m.err}, Context: m.context}
	return func() tea.Msg { return e }
}

// onRefreshed merges the reply of a selective refresh. The items it read
// are merged also when the refresh failed for others, and then one error
// toast follows. A cancel is silent. A reply from before the current full
// load is dropped, because that load brings newer data. A reply during the
// current full load is kept for its result (keepRefreshed).
func (c *listCore[T]) onRefreshed(m listRefreshedMsg[T]) tea.Cmd {
	c.idle()
	if m.gen != c.loadGen {
		return nil
	}
	c.mergeItems(m.items)
	if c.running {
		if c.refreshed == nil {
			c.refreshed = make(map[string]T, len(m.items))
		}
		for _, it := range m.items {
			c.refreshed[c.cfg.Key(it)] = it
		}
	}
	if m.err == nil || errors.Is(m.err, context.Canceled) {
		eventlog.Infof(eventlog.CatAWS, "Refreshed %d %s", len(m.items), c.cfg.Noun)
		return nil
	}
	eventlog.Errorf(eventlog.CatAWS, "%s refresh failed: %v", c.cfg.Noun, m.err)
	text := "Refresh failed: " + aws.ErrorText(m.err)
	return func() tea.Msg { return msg.ToastError(text) }
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

// findFocus returns the item that a focus ID names, or nil.
func (c *listCore[T]) findFocus(id string) *T {
	if c.cfg.FocusKey == nil {
		return c.find(id)
	}
	for i := range c.items {
		if c.cfg.FocusKey(c.items[i]) == id {
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

// targets returns the keys an action works on: the visible selected rows,
// or the cursor row when nothing is selected.
func (c *listCore[T]) targets() []string {
	return c.table.TargetKeys()
}

// setItems replaces the items and the table rows, after Derive. The table
// keeps cursor, selection and sort by key.
func (c *listCore[T]) setItems(items []T) {
	if c.cfg.Derive != nil {
		c.cfg.Derive(items)
	}
	c.items = items
	c.table.SetRows(c.rows(items))
}

// updateItems runs fn on each item with one of the keys and rebuilds the
// rows once (the EC2 state guess after an action).
func (c *listCore[T]) updateItems(keys []string, fn func(*T)) {
	for _, k := range keys {
		if it := c.find(k); it != nil {
			fn(it)
		}
	}
	c.setItems(c.items)
}

// mergeItems replaces each item that has the key of an item in fresh. An
// item that fresh does not hold stays, and an item of fresh with an unknown
// key is ignored.
func (c *listCore[T]) mergeItems(fresh []T) {
	if len(fresh) == 0 {
		return
	}
	byKey := make(map[string]T, len(fresh))
	for _, it := range fresh {
		byKey[c.cfg.Key(it)] = it
	}
	items := make([]T, len(c.items))
	for i, it := range c.items {
		if f, ok := byKey[c.cfg.Key(it)]; ok {
			it = f
		}
		items[i] = it
	}
	c.setItems(items)
}

// removeKeys removes the items with the given keys (the SQS delete).
func (c *listCore[T]) removeKeys(keys []string) {
	if len(keys) == 0 {
		return
	}
	drop := make(map[string]bool, len(keys))
	for _, k := range keys {
		drop[k] = true
	}
	c.setItems(slices.DeleteFunc(slices.Clone(c.items), func(it T) bool {
		return drop[c.cfg.Key(it)]
	}))
}

func (c *listCore[T]) rows(items []T) []ui.Row {
	rows := make([]ui.Row, 0, len(items))
	for _, it := range items {
		rows = append(rows, c.cfg.Row(it, c.widthTier))
	}
	return rows
}

// --- async helpers ---

// busy shows the spinner with label and starts its ticks.
func (c *listCore[T]) busy(label string) tea.Cmd {
	c.spinner.Show(label)
	return c.spinner.Tick()
}

// idle ends the spinner of a side task (a refresh, a fetch by ID). While a
// task of the view runs (startTask), the spinner shows its text again; else
// while a full load runs (also a background reload), the text of that load.
func (c *listCore[T]) idle() {
	switch {
	case c.tasks > 0:
		c.spinner.Show(c.taskLabel)
	case c.running:
		c.spinner.Show(c.loadLabel())
	default:
		c.spinner.Hide()
	}
}

// startTask shows the spinner with label until endTask, also across a
// refresh reply (the sqs purge and delete).
func (c *listCore[T]) startTask(label string) tea.Cmd {
	c.tasks++
	c.taskLabel = label
	return c.busy(label)
}

// endTask ends one task. When it was the last one, idle decides what the
// spinner shows.
func (c *listCore[T]) endTask() {
	if c.tasks > 0 {
		c.tasks--
	}
	c.idle()
}

// refreshKeys refreshes the items with the given keys through cfg.Refresh.
// The reply carries the current load generation.
func (c *listCore[T]) refreshKeys(keys []string) tea.Cmd {
	gen := c.loadGen
	refresh := c.cfg.Refresh
	ctx := c.viewContext()
	eventlog.Infof(eventlog.CatAWS, "Selective refresh: %d %s", len(keys), c.cfg.Noun)
	return tea.Batch(
		c.busy(fmt.Sprintf("Refreshing %d %s...", len(keys), c.cfg.Noun)),
		func() tea.Msg {
			items, err := refresh(ctx, keys)
			return listRefreshedMsg[T]{gen: gen, items: items, err: err}
		},
	)
}

// focus moves the cursor to the row with the given ID and opens its detail.
// The ID matches FocusKey, or Key without it. A row the filter hides keeps
// the cursor where it is, but its detail still opens. An ID that is not in
// the list goes to FocusMissing.
func (c *listCore[T]) focus(id string) tea.Cmd {
	it := c.findFocus(id)
	if it == nil {
		if c.cfg.FocusMissing == nil {
			return nil
		}
		return c.cfg.FocusMissing(id)
	}
	c.table.SetCursorByKey(c.cfg.Key(*it))
	if c.cfg.OpenDetail == nil {
		return nil
	}
	return c.cfg.OpenDetail(*it)
}
