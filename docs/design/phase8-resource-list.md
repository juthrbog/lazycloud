# Phase 8 (A10): a shared core for the resource lists

Status: approved on 2026-11-08. The owner accepted the proposed answer to each open question (see "Decisions" below). PR 0 starts the work.

## Summary

Seven list views (ec2, sqs_queues, ami, s3, subnet, vpc, sg) copy the same list code. Together they are 4,395 lines. About 2,400 of those lines do the same thing in each view: load pages, guard against stale replies, filter, sort, resize, select, copy, focus a linked row, draw the footer and the error screen. The copies have drifted apart, so each view has its own bugs (frozen spinners, stale replies that wipe the table, an AMI search that filters the table without showing it).

This proposal adds one unexported generic struct, `listCore[T]`, in package `views`. Each view embeds it and keeps its own type, its own constructor and its own `Update`. The core owns the shared state and behavior. The view sets its policy in a `listConfig[T]` made of plain func fields, and keeps its domain code (columns, rows, detail tabs, actions). The app, the navigator and the `msg` package do not change.

Result: about 4,395 → about 3,080 lines (about 2,320 in the views plus about 760 shared). Every load, refresh and load error has one generation guard. A stale load error reaches neither the view nor the status bar. A new simple list costs about 100-150 lines plus its detail tabs.

## Scores of the three drafts

Each score is from 1 (poor) to 10 (excellent).

| Draft | Coverage of the inventory | Migration safety | API simplicity and readability | Fit with app contracts | Total |
|---|---|---|---|---|---|
| spec (`ResourceList[T, X]` + data-driven `ListSpec`) | 9 | 5 | 6 | 8 | 28 |
| compose (`listCore[T]` embedded, view owns `Update`) | 8 | 6 | 8 | 9 | 31 |
| risk (`*resourceList[T]` + `owner`, knobs, characterize first) | 8 | 9 | 5 | 8 | 30 |

Why these scores:

- **spec** covers the most behavior and has the smallest view files. But it moves actions, prompts, forms and background ops into one large generic config with two type parameters. It also changes a lot of visible behavior inside the port PRs.
- **compose** has the clearest code. A view reads like the current views, minus the copies. Identity is safe by construction: `listCore` has no `Update`, so a view that forgets its own `Update` does not compile as a `nav.View` (`resolveView` returns `nav.View`). It is weaker on stale `ErrorMsg` and on migration safety.
- **risk** has the safest plan: characterization tests first, a parity checklist per PR, deliberate changes listed. All three drafts need a base config of about the same size. But risk adds extra knobs on top of it (`ClearOnReload`, `Progressive`, `HideFooterSpinner`, `Input`, `Inputs`, `Resize`, `Count`, ...). Most of them exist only to keep old quirks, and later PRs remove them again.

**Base: compose.** It gives the smallest and clearest API, and it fits the identity contract with no back-reference. I graft these ideas from the others:

- From **risk**: PR 0 with characterization tests, the parity checklist and the "Deliberate changes" section in each PR, the `nextToken` guard for a nil token, `-race` on the conformance suite, a conformance case with a nil service, a panic on a bad config in the constructor.
- From **spec**: a generation guard for load errors (compose kept stale errors), names in every confirm prompt with a `Busy` spinner label per action, `Derive` after each refresh merge, the AMI footer based on the data on screen (`shown`), and the port order (AMI before EC2).

### Claims I checked against the code where the drafts disagree

- **Stale `ErrorMsg` can be guarded without changing `msg`, but not by a wrapper alone.** The app checks `errors.Is(e.Err, context.Canceled)` (`internal/app/app.go:249`) and builds the status text with `aws.ErrorText` (`app.go:264-266`). `ErrorText` only uses `errors.Is` and `errors.AsType` (`internal/aws/errtext.go:72-94`), so a wrapper with `Unwrap()` keeps those behaviors. But the app writes every routed `ErrorMsg` to the status bar (`app.go:256-257`) before it hands it to the view. A view-side gen check would hide the error in the view and still show it in the status bar. So a failed load first comes back as a private core message (`listFailedMsg`). The core drops a stale one, and only a current one becomes a `msg.ErrorMsg` (see the API).
- **App tests match type names as strings.** `findMsg` compares `fmt.Sprintf("%T", ...)` (`internal/app/routing_test.go:69-77`). The page lookups are `input_test.go:52`, `ops_test.go:116`, `routing_test.go:168, 189, 217, 253, 301, 302`. The lookups for `views.ec2InstanceMutatedMsg` (`ops_test.go:126`, `routing_test.go:227`) stay valid, because that type stays. Spec listed `routing_test.go:227` as a page lookup; that is wrong.
- **sqs `attrsLoading` is redundant.** `loading` stays true from `startLoad` until `sqsAllAttrsLoadedMsg` (`sqs_queues.go:501, 510`). Every read is `loading || attrsLoading` (`:537, :688`). One flag is enough.
- **`ui.Filter` emits `FilterChangedMsg` on every edit and on esc** (`internal/ui/filter.go:93-118`). The message has only `Text` (`filter.go:10-12`). This is the root of the AMI search leak and of the s3 `table.Filter("")` hack.
- **Methods cannot be declared on an alias of an instantiated generic type.** So the spec approach (`type VPCList = ResourceList[aws.VPC, noExt]`) must move all view state into `X` and all view methods into free funcs or closures. That is a large part of why spec needs two type parameters.
- **A method value on a nil interface panics; a method expression does not.** `ec2.ListSecurityGroups` with a nil `ec2` panics when the value is made. `aws.EC2Service.ListSecurityGroups` is a plain func that takes the service as its first argument, so it is safe to build. The fetch adapters below use this form.
- **SQS selective refresh runs one cmd per queue today, each with its own 30s `callTimeout`** (`sqs_queues.go:268-277, 436-438, 611-616`; `context.go:13`). One failed queue does not stop the others (`:440-456`). The new refresh keeps both properties.

## API (Go code)

All new code is in package `views`.

### Files

| File | Contents | ~Lines |
|---|---|---|
| `internal/views/listcore.go` | `fetchFunc`, `loadSpec`, `listConfig[T]`, `completePhase[T]`, `listCore[T]`, `newListCore`, load, page, complete, refresh, focus, `update`, `handle`, `passThrough` | 360 |
| `internal/views/listcore_ui.go` | `listKeys`, `hints`, `footer`, `footerWith`, `render`, `handleKey` | 120 |
| `internal/views/listmsgs.go` | `listPageMsg[T]`, `listCompleteMsg[T]`, `listRefreshedMsg[T]`, `listFailedMsg`, `loadError`, `errNoClient`, `nextToken`, `fetchAll`, `fetchPages`, `refreshBy`, `refreshEach` | 110 |
| `internal/views/detail.go` | `buildTagsContent` (moved from `ec2_list.go:866`), `infoField`, `buildInfo`, `detailTitle`, `tabbedCmd`, `toastCmd`, `yesNo` | 80 |
| `internal/views/actions.go` | `pendingConfirm`, `confirmMessage`, `runEach` | 90 |
| `internal/views/listcore_test.go` | core unit tests with a fake `T` | 320 |
| `internal/views/list_conformance_test.go` | one suite over all 7 views | 360 |

### Messages and small helpers (`listmsgs.go`)

```go
package views

// errNoClient is returned by a fetch adapter when its service is nil.
var errNoClient = errors.New("AWS client not initialized")

// listPageMsg is one page of a load. next == nil means the last page.
// page 0 or 1 replaces the rows. Tests send page 0 with gen 0 to a view
// whose Init never ran, so the constructor must leave loadGen at 0.
type listPageMsg[T any] struct {
	gen   int
	items []T
	next  *string
	page  int
}

// listCompleteMsg carries the result of the second load phase (cfg.Complete).
type listCompleteMsg[T any] struct {
	gen   int
	items []T
}

// listRefreshedMsg carries a selective refresh. items holds every item that
// was read, also when err is set (partial success). A full load started
// since the request makes it stale.
type listRefreshedMsg[T any] struct {
	gen   int
	items []T
	err   error
}

// listFailedMsg is a failed page or complete call. It goes to the core
// first, not to the app, so a stale failure never reaches the status bar.
type listFailedMsg struct {
	gen     int
	err     error
	context string // the errContext of the load that failed
}

// loadError marks the msg.ErrorMsg that the core emits for a current
// failure. It wraps the AWS error, so errors.Is(context.Canceled) in app.go,
// aws.ErrorText and the status bar work as today. The core uses the mark
// only to know that this ErrorMsg is its own echo, already applied.
type loadError struct {
	gen int
	err error
}

func (e *loadError) Error() string { return e.err.Error() }
func (e *loadError) Unwrap() error { return e.err }

// nextToken adapts the {HasMorePages, Token} page structs. It also stops
// when HasMorePages is set but Token is nil (today that re-fetches page 1).
func nextToken(more bool, tok *string) *string {
	if more && tok != nil {
		return tok
	}
	return nil
}
```

The fetch and refresh adapters take the service as a value and the call as a method expression (or a func literal that takes the service). They check for nil inside the cmd. The unsafe form, a method value on a nil interface, cannot be passed to them, because their `call` parameter takes the service as its first argument.

```go
// fetchAll adapts a one-call list (sg, ami owned, s3) to a fetchFunc.
// Use: fetchAll(ec2, aws.EC2Service.ListSecurityGroups)
func fetchAll[S, T any](svc S, call func(S, context.Context) ([]T, error)) fetchFunc[T] {
	return func(ctx context.Context, _ *string) ([]T, *string, error) {
		if any(svc) == nil {
			return nil, nil, errNoClient
		}
		items, err := call(svc, ctx)
		return items, nil, err
	}
}

// fetchPages adapts a paged call. split reads the items and the next token.
// Use: fetchPages(ec2, aws.EC2Service.ListVPCsPage,
//         func(p *aws.VPCPage) ([]aws.VPC, *string) { return p.VPCs, nextToken(p.HasMorePages, p.Token) })
func fetchPages[S, P, T any](svc S, call func(S, context.Context, *string) (P, error),
	split func(P) ([]T, *string)) fetchFunc[T]

// refreshBy adapts a batch refresh (ec2 RefreshInstances, ami RefreshAMIs),
// with one timeout for the call.
func refreshBy[S, T any](svc S, call func(S, context.Context, []string) ([]T, error),
	timeout time.Duration) refreshFunc[T]

// refreshEach calls get once per key, each with its own timeout, at most
// 8 at a time (errgroup.SetLimit; golang.org/x/sync is already in go.mod).
// It returns every item it read. When some keys fail, it also returns an
// error "N of M failed: <first error>" that wraps the first failure.
// Use (sqs): refreshEach(sqs, aws.SQSService.GetQueueAttributes, callTimeout)
func refreshEach[S, T any](svc S, get func(S, context.Context, string) (*T, error),
	timeout time.Duration) refreshFunc[T]
```

### Config and core (`listcore.go`)

```go
// fetchFunc loads one page. A nil next token ends the load.
// It runs in a tea.Cmd goroutine, so it must not read view state that changes.
type fetchFunc[T any] func(ctx context.Context, token *string) (items []T, next *string, err error)

// refreshFunc reloads the given keys. It returns what it read, also on a
// partial failure. It sets its own timeouts (see refreshBy, refreshEach).
type refreshFunc[T any] func(ctx context.Context, keys []string) ([]T, error)

// loadSpec is one full load: what to call and how to report it. The core
// stores the spec of the current load. Because stale replies are dropped by
// gen, the load that finishes is always the stored one.
type loadSpec[T any] struct {
	fetch      fetchFunc[T]
	timeout    time.Duration // per page; 0 = none
	errContext string        // msg.ErrorMsg.Context of a failure
	label      string        // spinner text
	tag        string        // passed to OnLoaded; "" for the default load
}

// completePhase is the second load phase (sqs_queues). With it, pages
// collect in a buffer and the rows change only when Run returns.
type completePhase[T any] struct {
	Run     func(ctx context.Context, items []T) ([]T, error)
	Timeout time.Duration
	Label   string // with %d: "Loading attributes for %d queues..."
	Context string // ErrorMsg.Context: "fetching SQS queue attributes"
}

// listConfig is the policy of one list. The first block is required;
// newListCore panics if a required field is missing. 18 fields, 8 required.
type listConfig[T any] struct {
	Noun         string // footer, progress and log noun: "VPCs", "instances"
	LoadingLabel string // first load and full refresh: "Loading VPCs..."
	ErrContext   string // ErrorMsg.Context of the default load: "listing VPCs"
	Key          func(T) string
	Columns      func(ui.WidthTier) []ui.Column
	Row          func(T, ui.WidthTier) ui.Row // Row.Key must equal Key(t)
	Fetch        fetchFunc[T]                  // default load; build with fetchAll or fetchPages
	FetchTimeout time.Duration                 // callTimeout per page; listTimeout for a whole-list call; 0 = none

	Complete *completePhase[T]                // nil for every view but sqs
	Derive   func(items []T)                  // cross-row fields after any data change (sqs IsDLQ)
	OnLoaded func(items []T, tag string) tea.Cmd // after a full load ends; tag of that load
	Refresh  refreshFunc[T]                   // nil: 'r' is always a full reload

	FocusKey     func(T) string          // default Key; sqs focuses by Name
	OpenDetail   func(T) tea.Cmd         // enter/d and focus; nil: the view owns enter/d (s3)
	FocusMissing func(id string) tea.Cmd // focus on an ID not in the list; nil: do nothing

	MultiSelect bool   // space, esc deselects first, footer "(N selected)", selective refresh
	Copy        bool   // 'y' copies. MultiSelect: all targets. Single: the cursor key.
	CopyNoun    string // toast noun, used only with MultiSelect: "Copied N <CopyNoun>(s)"
}

// listCore is the state and behavior the lists share. Views embed it by
// value. It has no Update, View, Init, ID, Title, KeyMap or Footer, so the
// core alone is never a nav.View, and a view must declare its own Update
// that returns the view pointer (navigator.go:166-181).
type listCore[T any] struct {
	viewCtx // the only viewCtx in the view; promotes SetContext and viewContext

	cfg  listConfig[T]
	load loadSpec[T] // spec of the current load; reload resets it from cfg

	table   ui.Table
	filter  ui.Filter
	spinner ui.Spinner

	items     []T // what the table shows; kept during a reload
	buf       []T // pages of a load that has Complete
	loadCount int
	pages     int

	loading      bool
	loadGen      int
	err          error // list-load error only; replaces the table in render
	width        int
	height       int
	widthTier    ui.WidthTier
	pendingFocus string
}

func newListCore[T any](cfg listConfig[T]) listCore[T]
```

Method set of `*listCore[T]`. Go methods cannot have their own type parameters, so every method uses only `T`.

```go
// lifecycle
func (c *listCore[T]) init() tea.Cmd                     // DeselectAll; startLoad only while loading (no reload on re-show)
func (c *listCore[T]) startLoad() tea.Cmd                // loadGen++, buf=nil, counts=0; items are KEPT; uses c.load
func (c *listCore[T]) reload(label string) tea.Cmd       // c.load = default spec from cfg; loading=true, err=nil, busy(label), startLoad
func (c *listCore[T]) backgroundReload(label string) tea.Cmd // default spec; busy(label), startLoad; loading and err unchanged (ec2 delayed reload)
func (c *listCore[T]) loadWith(s loadSpec[T]) tea.Cmd    // c.load = s; loading=true, err=nil, busy(s.label), startLoad (ami search)

// entry points for the view's Update
func (c *listCore[T]) update(m tea.Msg) tea.Cmd         // handle, else passThrough
func (c *listCore[T]) handle(m tea.Msg) (tea.Cmd, bool) // core messages and keys
func (c *listCore[T]) passThrough(m tea.Msg) tea.Cmd    // spinner.TickMsg -> spinner; the rest -> table

// data
func (c *listCore[T]) find(key string) *T
func (c *listCore[T]) cursorItem() *T
func (c *listCore[T]) targets() []string                       // table.TargetKeys()
func (c *listCore[T]) setItems(items []T)                      // replace, Derive, SetRows
func (c *listCore[T]) mergeItems(fresh []T)                    // replace by key; keep missing; ignore unknown; Derive
func (c *listCore[T]) updateItems(keys []string, fn func(*T))  // one SetRows (ec2 optimistic state)
func (c *listCore[T]) removeKeys(keys []string)                // sqs delete

// async helpers
func (c *listCore[T]) busy(label string) tea.Cmd         // spinner.Show + Tick
func (c *listCore[T]) idle()                             // hides the spinner unless a full load runs
func (c *listCore[T]) refreshKeys(keys []string) tea.Cmd // busy("Refreshing N <noun>...") + Refresh, gen-tagged
func (c *listCore[T]) focus(id string) tea.Cmd

// presentation
func (c *listCore[T]) CapturingInput() bool                        // filter.Active(); promoted = nav.InputCapturer
func (c *listCore[T]) hints(bs ...ui.HintBinding) []ui.HintBinding // copies, then ApplyModeAll
func (c *listCore[T]) footer() string                              // footerWith("%d/%d <noun>")
func (c *listCore[T]) footerWith(base string) string               // + "(loading... N so far)" + "(N selected)" + spinner
func (c *listCore[T]) render(above, below string) string           // see "render" below
```

The decisions live in these core bodies:

```go
func (c *listCore[T]) handle(m tea.Msg) (tea.Cmd, bool) {
	switch m := m.(type) {
	case listPageMsg[T]:
		return c.onPage(m), true
	case listCompleteMsg[T]:
		if m.gen != c.loadGen {
			return nil, true // superseded
		}
		c.setItems(m.items)
		return c.finishLoad(), true
	case listRefreshedMsg[T]:
		return c.onRefreshed(m), true
	case listFailedMsg:
		if m.gen != c.loadGen {
			eventlog.Debugf(eventlog.CatAWS, "Dropped stale %s error", m.context)
			return nil, true // never reaches the status bar
		}
		c.loading, c.err = false, m.err
		c.spinner.Hide()
		e := &loadError{gen: m.gen, err: m.err}
		return func() tea.Msg { return msg.ErrorMsg{Err: e, Context: m.context} }, true
		// pendingFocus is kept (open question 4)
	case msg.ErrorMsg:
		if _, ok := errors.AsType[*loadError](m.Err); ok {
			return nil, true // our own echo; listFailedMsg already applied it
		}
		c.loading, c.err = false, m.Err // a bare ErrorMsg (tests) is applied as today
		c.spinner.Hide()
		return nil, true
	case msg.FocusResourceMsg:
		if c.loading {
			c.pendingFocus = m.ResourceID
			return nil, true
		}
		return c.focus(m.ResourceID), true
	case tea.WindowSizeMsg:
		c.width, c.height = m.Width, m.Height
		cols, tier := ui.BestFitTier(m.Width, c.cfg.Columns)
		if tier != c.widthTier || len(cols) != len(c.table.Columns()) {
			c.widthTier = tier
			c.table.SetColumns(cols) // keeps cursor, selection and sort by title
			c.table.SetRows(c.rows(c.items))
		}
		c.table.SetSize(m.Width, m.Height-3)
		c.filter.SetWidth(m.Width)
		return nil, true
	case ui.FilterChangedMsg:
		if m.Source != "" {
			return nil, true // a side input (ami search, s3 create)
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

func (c *listCore[T]) onPage(m listPageMsg[T]) tea.Cmd {
	if m.gen != c.loadGen {
		return nil
	}
	c.pages++
	c.loadCount += len(m.items)
	switch {
	case c.cfg.Complete != nil:
		c.buf = append(c.buf, m.items...)
	case m.page <= 1:
		c.items = dedupe(nil, m.items, c.cfg.Key)
		c.table.SetRows(c.rows(c.items)) // replaces old rows; cursor and selection kept by key
	default:
		fresh := dedupe(c.items, m.items, c.cfg.Key) // items stay equal to the table rows
		c.items = append(c.items, fresh...)
		c.table.AppendRows(c.rows(fresh))
	}
	if m.next != nil {
		c.spinner.Show(fmt.Sprintf("Loading %s... %d so far", c.cfg.Noun, c.loadCount))
		return tea.Batch(c.spinner.Tick(), c.fetchCmd(m.next, m.page+1))
	}
	if c.cfg.Complete != nil {
		if len(c.buf) == 0 {
			c.setItems(nil) // zero results clear stale rows
			return c.finishLoad()
		}
		c.spinner.Show(fmt.Sprintf(c.cfg.Complete.Label, len(c.buf)))
		return tea.Batch(c.spinner.Tick(), c.completeCmd(c.buf))
	}
	if c.cfg.Derive != nil {
		c.setItems(c.items)
	}
	return c.finishLoad()
}

// fetchCmd and completeCmd capture gen, c.load (fetch, timeout, errContext)
// and viewContext on the Update goroutine. A failure returns
// listFailedMsg{gen, err, c.load.errContext} (or Complete.Context).

// finishLoad: loading=false, err=nil, spinner.Hide, one Infof
// "Loaded N <noun> (P pages)", OnLoaded(items, c.load.tag), then consume pendingFocus.

func (c *listCore[T]) onRefreshed(m listRefreshedMsg[T]) tea.Cmd {
	c.idle() // keeps a full-load spinner
	if m.gen != c.loadGen {
		return nil // a full load started since; it brings newer data
	}
	c.mergeItems(m.items) // partial success still updates the rows; runs Derive
	if m.err == nil || canceled(m.err) {
		return nil
	}
	eventlog.Errorf(eventlog.CatAWS, "%s refresh failed: %v", c.cfg.Noun, m.err)
	return toastCmd(msg.ToastError("Refresh failed: " + aws.ErrorText(m.err)))
}

func (c *listCore[T]) passThrough(m tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	if _, ok := m.(spinner.TickMsg); ok { // animate whenever visible
		c.spinner, cmd = c.spinner.Update(m)
		return cmd
	}
	c.table, cmd = c.table.Update(m) // the table always gets nav keys
	return cmd
}
```

A nil service gives `errNoClient` through the adapter, with the `errContext` of the load (today it is `"EC2"`, `"S3"`; no test asserts that).

**`render(above, below)`** draws, in order: the main part, then `below`. The main part is the spinner (loading with no rows), or the error, or `above` + the filter + the table. `below` is added in every branch, as s3 does today with its create input (`s3_list.go:497-513`), so a side input stays visible on the spinner and error screens too.

`handleKey` (in `listcore_ui.go`) runs in this order:

1. If the filter is active, the filter gets the key.
2. `esc`: deselect if there is a selection, else `NavigateBackMsg`.
3. `s`: sort picker. `S`: reverse sort. `/`: open the filter.
4. `r`: `refreshKeys(targets)` if `Refresh != nil && SelectionCount() > 0`, else `reload(LoadingLabel)`. `reload` always goes back to the default spec, so after an AMI search `r` loads owned AMIs with `listTimeout` and the context "listing owned AMIs".
5. `y`: copy (only if `Copy`). `space`: toggle the selection (only if `MultiSelect`).
6. `enter`/`d`: `OpenDetail(*cursorItem())` (only if `OpenDetail != nil`). With no cursor item it returns `handled=false`, as today.
7. Any other key returns `handled=false` and goes to `passThrough`, so to the table.

Shared key bindings are `ui.HintBinding` values in one `listKeys` var. `hints()` clones the slice before `ApplyModeAll`, so shared bindings never change.

### Detail helpers (`detail.go`)

```go
type infoField struct {
	K, V   string
	ViewID string            // "" = no link
	Params map[string]string // link params
}

// buildInfo prints "%-16s %s" lines, skips empty values, and counts link
// lines among printed lines only (the rule of ec2, vpc, subnet and sg).
func buildInfo(fields []infoField) (string, []msg.TabLink)

func detailTitle(name, id string) string // "name (id)" or id
func tabbedCmd(title string, tabs ...msg.TabContent) tea.Cmd
func buildTagsContent(tags map[string]string) string // moved from ec2_list.go:866
```

EC2 builds the Root Device value as `""` when both parts are empty, so `buildInfo` needs no `" ()"` special case. This is a small code change, not a move (`ec2_list.go:831, 837`). PR 1 tests it.

### Action helpers (`actions.go`)

Actions stay in the views (EC2, SQS, S3). These non-generic helpers remove the repeated parts. PR 2 adds them on today's views, before any port.

```go
// pendingConfirm holds action targets between key press, picker and confirm.
type pendingConfirm struct {
	action string   // confirm Action id, e.g. "ec2_stop"
	keys   []string // frozen at key press
}

func (p *pendingConfirm) hold(keys []string)                    // 'm' pressed; picker open
func (p *pendingConfirm) ask(req msg.RequestConfirmMsg) tea.Cmd // records req.Action; emits req
func (p *pendingConfirm) clear()
// take returns the keys only for a confirmed reply to the recorded action.
// Every other reply (decline, the app's fake decline, another action) clears it.
func (p *pendingConfirm) take(m ui.ConfirmResultMsg) ([]string, bool)

// confirmMessage: "Stop 2 instance(s)?\n" + promptNameList(names) + warning.
func confirmMessage(verb string, n int, noun string, names []string, warning string) string

// runEach calls fn per key in order, each with its own detached context,
// and goes on after a failure. names is a snapshot from the Update goroutine.
func runEach(ctx context.Context, keys []string, names map[string]string,
	fn func(ctx context.Context, key string) error) (done []string, failed []itemFailure)
```

`s3_list` keeps its own loop, because it needs `ops.Report` and a cancel count.

### Small `ui` change

```go
// ui/filter.go
type FilterChangedMsg struct {
	Text   string
	Source string // "" for the list filter; set by WithSource for side inputs
}

func (f Filter) WithSource(id string) Filter
```

AMI search uses `WithSource("ami-search")`. S3 create uses `WithSource("s3-create")`. The core ignores a message with a `Source`. This fixes both leaks and removes the s3 `creating` gate and the `table.Filter("")` hack.

### Full sketch: `vpc_list.go`

```go
var vpcSubnetsKey = ui.NewHintBinding([]string{"n"}, "n", "subnets")

// VPCList displays EC2 VPCs.
type VPCList struct {
	listCore[aws.VPC]
}

// NewVPCList creates the VPC list view.
func NewVPCList(ec2 aws.EC2Service) *VPCList {
	v := &VPCList{}
	v.listCore = newListCore(listConfig[aws.VPC]{
		Noun:         "VPCs",
		LoadingLabel: "Loading VPCs...",
		ErrContext:   "listing VPCs",
		Key:          func(x aws.VPC) string { return x.ID },
		Columns:      vpcColumns,
		Row:          vpcRow,
		FetchTimeout: callTimeout,
		Fetch: fetchPages(ec2, aws.EC2Service.ListVPCsPage,
			func(p *aws.VPCPage) ([]aws.VPC, *string) { return p.VPCs, nextToken(p.HasMorePages, p.Token) }),
		OpenDetail: vpcDetail,
		Copy:       true, // single select: "Copied: <id>"
	})
	return v
}

func (v *VPCList) ID() string     { return "vpc_list" }
func (v *VPCList) Title() string  { return "VPCs" }
func (v *VPCList) Init() tea.Cmd  { return v.init() }
func (v *VPCList) Footer() string { return v.footer() }
func (v *VPCList) View() tea.View { return tea.NewView(v.render("", "")) }

func (v *VPCList) KeyMap() []ui.HintBinding {
	return v.hints(listKeys.Details, listKeys.CopyID, vpcSubnetsKey,
		listKeys.Sort, listKeys.Filter, listKeys.Refresh)
}

func (v *VPCList) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := m.(tea.KeyPressMsg); ok && !v.CapturingInput() && key.Matches(k, vpcSubnetsKey.Binding) {
		id := v.table.CursorKey()
		if id == "" {
			return v, nil
		}
		return v, func() tea.Msg {
			return msg.NavigateMsg{ViewID: "subnet_list", Params: map[string]string{"vpc_id": id}}
		}
	}
	return v, v.update(m)
}

// vpcColumns (unchanged), vpcRow (one func per row, from buildVPCRows),
// vpcDetail (buildInfo + CIDR + JSON + Tags, then tabbedCmd),
// buildVPCCIDRContent (unchanged).
```

Subnet passes its `vpcID` through a func literal that takes the service, so it stays nil-safe: `fetchPages(ec2, func(s aws.EC2Service, ctx context.Context, tok *string) (*aws.SubnetPage, error) { return s.ListSubnetsPage(ctx, tok, vpcID) }, ...)`.

### Sketch: `ec2_list.go` (structure)

```go
// EC2List displays all EC2 instances.
type EC2List struct {
	listCore[aws.Instance]
	ec2       aws.EC2Service
	awsClient *aws.Client
	pending   pendingConfirm
}

// ec2Action describes one Manage action (added in PR 2, on today's view).
type ec2Action struct {
	label, busy, past       string // "Stop", "Stopping", "stopped"
	confirmID, transitional string // "ec2_stop", "stopping"
	quick                   bool   // Start: y/n (M24)
	run                     func(aws.EC2Service, context.Context, []string) error
}

var ec2Actions = []ec2Action{
	{"Start", "Starting", "started", "ec2_start", "pending", true, aws.EC2Service.StartInstances},
	{"Stop", "Stopping", "stopped", "ec2_stop", "stopping", false, aws.EC2Service.StopInstances},
	{"Reboot", "Rebooting", "rebooted", "ec2_reboot", "pending", false, aws.EC2Service.RebootInstances},
	{"Terminate", "Terminating", "terminated", "ec2_terminate", "shutting-down", false, aws.EC2Service.TerminateInstances},
}

func NewEC2List(ec2 aws.EC2Service, client *aws.Client) *EC2List {
	e := &EC2List{ec2: ec2, awsClient: client}
	e.listCore = newListCore(listConfig[aws.Instance]{
		Noun: "instances", LoadingLabel: "Loading EC2 instances...",
		ErrContext:   "listing EC2 instances", // routing_test.go:262
		Key:          func(i aws.Instance) string { return i.ID },
		Columns:      ec2Columns,
		Row:          ec2Row,
		FetchTimeout: callTimeout,
		Fetch: fetchPages(ec2, aws.EC2Service.ListInstancesPage, splitInstancePage),
		Refresh:      refreshBy(ec2, aws.EC2Service.RefreshInstances, callTimeout),
		OpenDetail:   func(i aws.Instance) tea.Cmd { return e.fetchDetail(i.ID) },
		FocusMissing: e.fetchDetail, // keep: a link opens the detail even for an unlisted ID
		MultiSelect:  true,
		Copy:         true,
		CopyNoun:     "instance ID",
	})
	return e
}

func (e *EC2List) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case ui.PickerResultMsg:
		if m.ID == "action" {
			return e, e.onActionPicked(m) // confirmMessage with names; pending.ask
		}
	case ui.ConfirmResultMsg:
		return e, e.onConfirm(m) // pending.take; updateItems(optimistic); busy(act.busy + " N instance(s)..."); mutate
	case ec2InstanceMutatedMsg:
		return e, e.onMutated(m) // deselect; error: refreshKeys + M7 toast; ok: busy + 2s tick + Outcome toast
	case ec2DelayedRefreshMsg:
		return e, e.backgroundReload("Refreshing instances...") // as today: loading stays false
	case ec2SSMSessionFinishedMsg:
		return e, e.onSSMFinished(m) // toast; ok: reload("Refreshing instances...") (today sets loading=true)
	case ec2InstanceDetailMsg:
		return e, e.onDetail(m) // error: toast, list stays; ok: tabbedCmd
	case tea.KeyPressMsg:
		if !e.CapturingInput() {
			switch {
			case key.Matches(m, ec2Keys.Connect.Binding):
				return e, e.connect() // unchanged checks; cursor instance
			case key.Matches(m, ec2Keys.Manage.Binding):
				return e, e.manage() // targets; commonActions; pending.hold; picker
			}
		}
	}
	return e, e.update(m)
}
```

The old `e.fetchPage(token, page)` method goes away. EC2 uses the adapter, so there is no view method named like a core method (`fetchCmd`, `focus`, `find`, `refreshKeys`, ...). The reflection test enforces this for every view.

`ec2InstanceMutatedMsg`, its `Outcome()` and its text stay as they are.

### Sketch: AMI search (the only `loadWith` user)

```go
func (a *AMIList) search(q string) tea.Cmd {
	svc := a.ec2
	return a.loadWith(loadSpec[aws.AMI]{
		fetch: fetchAll(svc, func(s aws.EC2Service, ctx context.Context) ([]aws.AMI, error) {
			return s.SearchAMIs(ctx, q) // q is captured on the Update goroutine
		}),
		timeout:    0, // search reads up to 20 pages
		errContext: "searching AMIs",
		label:      "Searching AMIs...",
		tag:        q,
	})
}

// OnLoaded: a.linked = 0; a.shown = tag ("" = owned). The footer reads shown.
```

## Package layout and fit with the app

**Package.** Everything stays in `views`. The core needs `viewCtx`, `detached`, `itemFailure`, `resultText`, `canceled` and `promptNameList`, and the white-box tests need private access. A sub-package would mean exporting about ten helpers. No import edge changes. `views` still does not import `nav`. The test file may import `nav`, because `nav` imports only `ui`.

| Contract | How it holds |
|---|---|
| `nav.View` (Init, Update, View, ID, Title, KeyMap, Footer) | Each view declares all seven on its own pointer type. `listCore` declares none, so a view without `Update` fails to compile in `resolveView`. |
| Identity (`navigator.go:166-181`, `Routed.Origin`) | The view's `Update` returns `v`. The conformance suite asserts `require.Same(t, v, got)` for about 12 message kinds per view. |
| `nav.InputCapturer` | `listCore.CapturingInput` is promoted (filter only). AMI and S3 declare their own (filter or side input); the outer method wins. `app/input_test.go:213-217` keeps passing. |
| `nav.ContextSetter` | `viewCtx` is embedded once, in `listCore`, so `SetContext` is promoted. A view must not embed `viewCtx` itself, or the shallower copy wins and fetches run on `Background()`. A reflection test blocks this. |
| `msg.Outcome` | It stays on the view result messages (`ec2InstanceMutatedMsg`, `sqsQueue*Msg`, `s3Bucket*Msg`). Names and texts do not change. |
| `msg.ErrorMsg` and the status bar | Same type and same `Context` strings, per load (owned AMIs and AMI search keep their own). A failure first comes back as `listFailedMsg`. Only a current one becomes an `ErrorMsg`, so the app status bar (`app.go:248-260`) never shows a superseded error. The `Err` is a `*loadError` that unwraps to the AWS error, so the app's Canceled check and status text work unchanged. A bare `ErrorMsg` from tests is still applied. Known limit, same as today: a good reload clears the view error but not the status bar text, which the app clears only on a session switch (`app.go:1260`). |
| Routed and unrouted messages | Page, complete, refresh and failed messages are cmd results, so the app routes them by origin. The `ErrorMsg` the core emits is also a cmd result of the view, so it is routed to the same origin. `ConfirmResultMsg`, `PickerResultMsg`, `FormResultMsg`, focus after a push and `tea.Exec` callbacks arrive through `UpdateCurrent`. The view or core handles them as today. |
| Panel | `OpenDetail` returns `msg.TabbedContentMsg`, as today. Views never see panel state. Narrow terminals still push the first tab full screen (`app.go:434-445`). |
| ReadOnly | Views mark RW hints `.WithMode(ui.ModeReadWrite)`. `hints()` always calls `ApplyModeAll`. The central block (`app.go:795`) needs the real keys in `KeyMap()`, and they stay there. EC2 and SQS also check `ui.IsReadOnly()` again in `onConfirm`, in case the user pressed `W` while the prompt was open. The aws guards stay as the last layer. |
| Key hints | Each view lists its hints in the current order and with the current help strings ("browse", "new bucket", "delete bucket", "enter/d details", "s/S sort"). `input_test.go:161-166` (`[o,m]`, `[m]`, `[n,x]`, none) keeps passing. |
| NO_COLOR and theme | No change. The table is still built in the constructor, after `ColorProfileMsg`. A theme change still rebuilds the navigator (L17 stays open). |
| Timeouts (M3) | Per load: `callTimeout` for pages, `listTimeout` for whole-list calls (sg, ami owned, s3 ListBuckets, see open question 6), none for AMI search. `Complete.Timeout: listTimeout` for sqs attributes. Refresh sets its timeout in the adapter: `refreshBy` with `callTimeout` for the one batch call (ec2, ami), `refreshEach` with `callTimeout` per queue (sqs, as today). Detail uses `callTimeout`. Mutations keep `detached`. |
| Resize | Only the current view gets `WindowSizeMsg`. The core rebuilds on a tier change or a count change. A view with a side input sets the input width first, then hands the message to the core. |
| Building views | `resolveView` keeps calling `views.NewVPCList(...)` and the others. Names and signatures do not change. `app.go` does not change. A5 stays out of scope. |

## How each view maps

| View | Generic part (core) | View-specific part | Lines before → after |
|---|---|---|---|
| sg_list | `fetchAll(ec2, aws.EC2Service.ListSecurityGroups)` with listTimeout, gen, keys, footer, render, focus, `OpenDetail`, copy "Copied: id" | columns, row (In/Out with `SortKeyInt`), Info/Inbound/Outbound/JSON/Tags, rule formatters | 424 → ~240 |
| vpc_list | `fetchPages` with callTimeout, gen, `OpenDetail`, copy | `n` key in `Update`, columns, row, Info/CIDR tabs | 432 → ~175 |
| subnet_list | as vpc | `vpcID` field, `ID()`/`Title()`, Fetch literal with vpcID, Config/IPv6 tabs, Avail IPs `SortKeyInt` | 466 → ~205 |
| ami_list | `fetchAll(ec2, aws.EC2Service.ListOwnedAMIs)`, `loadWith` for search with its own context and tag, gen for both, `MultiSelect`, `Copy`, `CopyNoun "AMI ID"`, `Refresh: refreshBy(RefreshAMIs)`, `FocusMissing`, `OnLoaded(items, tag)` | search input (Source "ami-search"), `shown` set from the tag, linked count, `amiFocusLoadedMsg`, footer text via `footerWith`, JSON detail | 558 → ~260 |
| ec2_list | `fetchPages(ListInstancesPage)` with progress, `MultiSelect`, `Copy`, `Refresh: refreshBy`, `OpenDetail`, `FocusMissing`, `backgroundReload` | SSM connect, Manage/confirm/mutate, optimistic state, delayed reload, async detail and its tabs | 909 → ~520 |
| sqs_queues | URL pages + `Complete` (FetchAllQueueAttributes), `Derive` (markDLQQueues), `OnLoaded` (unreadable toast), `Refresh: refreshEach(GetQueueAttributes, callTimeout)` (per queue, parallel, partial results), `FocusKey: Name`, `MultiSelect`, `Copy`, `CopyNoun "queue URL"` | Manage, send form, purge/delete via `runEach`, `p` peek, async detail and its six tabs | 1,091 → ~620 |
| s3_list | `fetchAll(s3, aws.S3Service.ListBuckets)`, gen, `MultiSelect`, `OpenDetail: nil`, `render(.., createInput)` in every branch | enter browse, `d` props, `n` create input (Source "s3-create"), `x` delete op, Outcome messages | 515 → ~300 |
| shared | | `listcore*.go`, `listmsgs.go`, `detail.go`, `actions.go` | 0 → ~760 |

Total: 4,395 → about 3,080. The rest of each view is resource content (columns, detail tabs, actions). The A10 goal of 100-150 lines for a new view holds for the spec, constructor, hints, fetch adapter, columns and row. Detail tabs come on top.

## Divergences between views

**Unify** means the core does it one way for all views. **Keep** means a config field or the view keeps the old behavior. **Fix** means it is a bug and goes away. The PR column says where the change lands; "pre" means a PR on today's code, before the ports.

| # | Divergence | Decision | Why | PR |
|---|---|---|---|---|
| 1 | Esc deselects first (ec2/sqs/ami/s3) vs always back | Unify | Without `MultiSelect` there is never a selection, so esc goes straight back. | port |
| 2 | Init calls `DeselectAll` only in multi-select views | Unify | A no-op without a selection. | port |
| 3 | Copy: all targets with "Copied N ...(s)" vs cursor with "Copied: id"; s3 has no `y` | Keep both, chosen by `MultiSelect`; `Copy` turns `y` on | Tests assert both texts. s3 `y` is open question 7. | port |
| 4 | Selective refresh: ec2/ami one call with toast; sqs N calls, silent, no gen; others none | Unify on `Refresh(ctx, keys)` with partial results | sqs keeps one call per queue with its own `callTimeout`, run in parallel (`refreshEach`). One reply, one spinner hide, a gen guard. Queues that were read are merged even when others fail; then one error toast (new for sqs). | 6, 8 |
| 5 | Progress label "N so far" and re-tick only in ec2; logs per page (vpc/subnet) vs once (ec2) | Unify | Every multi-page load shows progress. Debugf per page, one Infof at the end. Logs only. | 5 |
| 6 | Table frozen during a load (vpc/sg/ami/s3) vs usable once rows exist (ec2/sqs) | Unify (usable) | Items now stay equal to the rows, so keys act on real data. | port |
| 7 | Reload clears items (vpc/subnet/ec2/sqs) vs keeps them (sg/ami/s3) | Unify (keep) | Fixes ec2 "No common actions" and a no-op `o` during the delayed reload, the vpc "0/0" footer after a resize mid-reload, and details that do nothing during a reload. The full-screen spinner shows only on the first load. Open question 2. | port |
| 8 | `err` cleared only by `r` | Fix in the view | Any successful full load clears the view error. The status bar text stays until a session switch, as today (app change, out of scope). | port |
| 9 | Stale `ErrorMsg` from a superseded load is applied | Fix | `listFailedMsg` with gen. A stale failure reaches neither the view nor the status bar. | 4 |
| 10 | Refresh replies have no gen and can wipe the table or hide the load spinner | Fix | Gen-tagged, `idle()` hides only when no full load runs. | port |
| 11 | Spinner freezes when `loading` is false | Fix | Ticks go to the spinner by type. `busy()` always adds a tick. | port |
| 12 | Tier change detected by column count (subnet narrow/medium bug) | Unify on tier or count | Subnet widths then follow the tier, not the resize history. | 5 |
| 13 | Zero sqs queues keep stale rows | Fix | `setItems(nil)`. | 8 |
| 14 | `AppendRows` drops duplicate keys but items keep them | Fix | `dedupe` before append. | 5 |
| 15 | Footer: "(loading... N so far)" and "(N selected)" only in some views; s3 has no spinner | Unify | Shown when it applies. AMI keeps its mode text through `footerWith`. | port |
| 16 | s3 shows the spinner whenever loading | Unify | Spinner only when loading with no rows, then error, then table. | 9 |
| 17 | `ApplyModeAll` only in RW views | Unify | A no-op without RW keys. | port |
| 18 | Detail local (vpc/sg/subnet) vs remote (ec2/sqs/s3 props) | Keep | `OpenDetail` returns a cmd; remote views fetch and handle their own detail message. | - |
| 19 | Detail fetch error replaces the list (ec2, sqs, s3 props) | Unify to a toast | `err` means only "the list failed to load". Open question 1. | pre (3) |
| 20 | Focus on a missing ID: ec2 fetches detail, ami fetches the row, others nothing | Keep via `FocusMissing` | Real product differences (M10). | - |
| 21 | sqs focuses by Name, rows keyed by URL | Keep via `FocusKey` | | - |
| 22 | Focus on a row the filter hides: panel opens, cursor stays | Keep | Clearing the user's filter is worse. Recorded for A6. | - |
| 23 | Mutation model: ec2 one batch call + optimistic + delayed reload; sqs/s3 per item | Keep | The EC2 API is a batch API. M7 refresh covers partial success. | - |
| 24 | Confirm prompts list only a count (ec2, sqs) | Fix | `confirmMessage` lists names (A10 goal). Phrases stay: Start is Quick, others type "confirm", s3 types the bucket name. Open question 3. | pre (2) |
| 25 | Confirm `Action` not checked against the pending action (ec2 stuck spinner); sqs `pendingAction` never read | Fix | `pendingConfirm.take` checks it. | pre (2) |
| 26 | ec2 spinner "stoping", "terminateing" | Fix | `busy` field per action. | pre (2) |
| 27 | sqs: no spinner after confirm | Unify | `busy("Purging N queue(s)...")`. No optimistic state. | pre (2) |
| 28 | Delete deselects, purge keeps the selection, ec2 always deselects | Keep | Per-view result handlers. | - |
| 29 | sqs: panel title Name only, no JSON/Tags tab, FIFO tab only for FIFO | Keep | Domain content. | - |
| 30 | sqs refreshed queue keeps a stale `IsDLQ` | Fix | `Derive` runs after each merge. | 8 |
| 31 | sqs `attrsLoading` | Remove | Redundant (checked above). | 8 |
| 32 | AMI search live-filters the table and leaves a hidden filter; s3 create leak and `table.Filter("")` hack | Fix | `FilterChangedMsg.Source`. Open question 8. | pre (1) |
| 33 | AMI duplicate Refresh hint in search mode | Fix | Leftover bug. | pre (1) |
| 34 | AMI enter emits `NavigateMsg{content}`, focus emits a tabbed panel with one tab named like the panel | Unify | `TabbedContentMsg` with one "JSON" tab for both. Open question 5. | 6 |
| 35 | AMI has no gen; the last reply wins (r then p) | Fix | The core gen covers owned and search. The last started load wins. | 6 |
| 36 | AMI footer flips to "(owned)" before data arrives | Fix | Footer reads `shown`, set in `OnLoaded` from the tag of the load that finished. | 6 |
| 37 | AMI refresh toasts on Canceled | Fix | The core drops Canceled for every view. | 6 |
| 38 | AMI search: no timeout, no nil guard; its error context is "searching AMIs" | Keep the timeout rule and the context, add the nil guard | Search reads up to 20 pages (comment in the code). The context is per load (`loadSpec.errContext`). | pre (3), 6 |
| 39 | s3 enter = browse, `d` = props JSON content panel | Keep the keys; props becomes a tabbed panel with one "JSON" tab titled "<name> (properties)" | Same output path as every other detail. | 9 |
| 40 | s3 create error replaces the list; create toast is hand-written | Unify | Toast with `m.Outcome()` text; success uses `m.Outcome()` too. | pre (3) |
| 41 | s3 create input width not updated on resize | Fix | The view sets the width before it hands off. | 9 |
| 42 | s3 single-bucket prompt not sanitized | Fix | `ui.Sanitize(name)`. | pre (3) |
| 43 | s3 props: no nil-svc guard, nil props panics | Fix | Guards in the fetch closure. | pre (3) |
| 44 | s3 ListBuckets uses callTimeout | Change to listTimeout | It pages inside the aws layer (M4). Open question 6. | 9 |
| 45 | s3 has no focus handling | Unify | Free from the core. Nothing links there yet. | 9 |
| 46 | sg/subnet numeric cells sort as text | Fix | `SortKeyInt` in their rows. ec2 drops its redundant `Sort == Cells` copy. | pre (3) |
| 47 | sqs/ami search have no nil-service guard | Unify | The adapters check for nil. | pre (3) |
| 48 | Details with no cursor fall through to the table | Keep | `handled=false`, same as today. | - |
| 49 | ec2 Reboot optimistic state "pending" | Keep | The 2s reload corrects it. Open question 9. | - |
| 50 | Refresh merge keeps items AWS no longer returns; ignores new IDs | Keep | Open question 10. | - |
| 51 | `pendingFocus` survives an `ErrorMsg` | Keep | Open question 4. The zero-result path now consumes it in `finishLoad`. | - |
| 52 | sg description truncation splits UTF-8 | Out of scope | Follow-up (`ansi.Truncate`). | - |
| 53 | ec2 delayed reload after a mutation does not set `loading` (focus opens at once); the SSM-end reload does | Keep | `backgroundReload` for the delayed reload, `reload` for SSM end. A test checks that focus during the delayed reload opens at once. | 7 |
| 54 | s3 create input drawn under every screen (spinner, error, table) | Keep | `render` adds `below` in every branch. A snapshot covers `n` on the error screen. | 9 |

## Behavior parity checklist

Each line must still be true after the port, unless the divergence table says otherwise. PR 0 turns these into tests.

**All views**
- `ID()` and `Title()` strings are the same (subnet: `subnet_list:<vpc>` and `Subnets (<vpc>)`).
- `KeyMap()` keys, help strings, modes and order are the same (AMI loses only the duplicate Refresh hint).
- `CapturingInput()` is true while the filter is open (AMI search and S3 create too).
- Init deselects, and loads only while `loading` is true (a cached view does not reload).
- A gen-0, page-0 or page-1 message is accepted on a fresh view.
- A page from an older load is dropped.
- `ErrorMsg.Context` strings are the same ("listing EC2 instances", "listing VPCs", "listing subnets", "listing security groups", "listing owned AMIs", "searching AMIs", "listing S3 buckets", "listing SQS queues", "fetching SQS queue attributes").
- The error view shows `Error: ` + sanitized `aws.ErrorText` in the theme red.
- The table keeps cursor, selection and sort by key across SetRows (C3); the filter deselects hidden rows (C4); the marker never leaks into `CursorKey` (M8).
- Sort picker: `_clear` clears, `Selected >= 0` sorts; the sort survives a tier change.
- Footer starts with `N/M <noun>` ("2/2 instances", "0/0").
- State and count cells hold plain text; the column Style colors them.
- Focus while loading is stored and returns a nil cmd; it opens after the load ends.
- Focus on the view already on top only moves the cursor (app behavior).
- `Update` returns the same pointer for every message.

**ec2_list**
- Columns 4/5/8 per tier with the same titles and widths.
- `o` checks: nil client, not running, no aws CLI, no plugin; each gives the same toast text.
- `o` uses the cursor; `m`, `y`, `r` use targets.
- Manage picker: stopped → [Start]; running → [Stop, Reboot, Terminate]; mixed with no common action → toast "No common actions for selected instances".
- Picker title "Manage Instance" / "Manage N Instances".
- Confirm Action IDs `ec2_start`, `ec2_stop`, `ec2_reboot`, `ec2_terminate`; Start is Quick; message contains "N instance(s)".
- Targets are frozen at `m`; a decline clears them.
- Optimistic states: pending, stopping, pending, shutting-down.
- Mutations use `detached` and survive leaving the view; `Outcome()` text "Instance stopped: i-1".
- Every mutation result deselects.
- Failure: selective refresh + toast "<Action> failed: <Code>. The list shows the current state of N instance(s)."
- Success: 2s delayed full reload + Outcome toast. During that reload `loading` stays false, so a focus opens at once.
- SSM end: error toast, or reload + "Session ended: <name or id>"; selection kept across the reload.
- Detail tabs: Info (same fields, order and links), JSON, Security Groups (links to sg_list), Tags.
- Focus on an unlisted ID still fetches and opens the detail.
- Copy toast "Copied N instance ID(s)".

**sqs_queues**
- Columns 2/5/7 per tier; count columns sort as numbers; an unreadable queue shows `?` and Type "unknown".
- Two phases: no new rows until attributes arrive; FetchAllQueueAttributes is called once after a double refresh; stale attributes are dropped.
- Unreadable queues give one toast "Could not read N queue(s): ... Their counts show ?."
- DLQ marking rule (target ARN or RedriveAllowPolicy).
- Focus by queue name, cursor by URL, waits for both phases; an unknown name does nothing.
- Manage: one target → [Send Message, Purge Queue, Delete Queue]; more → [Purge Queue, Delete Queue], title "Manage N Queues".
- Send targets the selected queue; FIFO and dedup rules; delay 0..900; form ID `sqs_send_message`; an aborted form clears the target.
- Purge and delete go on after a failure; purge refreshes only purged queues; delete removes only deleted rows and deselects; purge keeps the selection.
- Selective refresh and the purge refresh: each queue has its own 30s timeout; one failed queue does not stop the others from updating.
- Exact texts: "Deleted 2 of 3 queue(s). Failed: qb (AccessDenied)", the purge cooldown text, "Message sent", "Send failed: ...".
- Confirm Action IDs `sqs_purge`, `sqs_delete`.
- `p` opens sqs_messages with `queue_url`, `queue_name`, `fifo` for the cursor queue.
- Detail tabs and link lines (Messages line 0, DLQ source queues focus by name); detail calls GetQueueAttributes and ListDeadLetterSourceQueues.
- Copy toast "Copied N queue URL(s)".
- Init deselects when coming back from sqs_messages.

**ami_list**
- Columns 3/4/6 per tier; owner alias first; 10-char date.
- Owned load uses listTimeout; search has no timeout.
- A failed owned load reports "listing owned AMIs"; a failed search reports "searching AMIs".
- `p` opens the search; esc closes it; enter with an empty query only closes it.
- `r` with no selection returns to owned mode: after a search, `r` calls ListOwnedAMIs (not SearchAMIs) and the footer shows "(owned)" once that load ends.
- Footer: "(owned)", "(owned + N linked)", "results for %q".
- Focus on a missing AMI fetches it by ID, adds a row, counts it as linked, opens the panel; errors and "not found" give the same toasts; Canceled is silent; the spinner is hidden afterwards.
- A reload drops the linked row.
- Copy toast "Copied N AMI ID(s)".
- No RW keys.

**s3_list**
- Columns: Bucket only when narrow; Bucket + Created otherwise; date format kept.
- enter browses to s3_objects with `bucket` and empty `prefix`.
- Help strings "browse", "properties", "new bucket" (RW), "delete bucket" (RW).
- Delete prompt: one bucket → phrase is the bucket name; several → "Delete 2 buckets?\n  a-logs\n  b-tmp\n" with the warning and phrase "delete 2 buckets"; Action `delete_bucket`.
- Any other confirm result clears pending.
- Delete runs as `StartOpMsg` with progress, cancel accounting, always reloads, deselects first.
- Exact texts: "Deleted 1 of 2 bucket(s). Failed: other-bucket (AccessDenied)", the cancel texts, "Bucket created: <name>".
- Create uses the default region or "us-east-1" and a detached context.
- The create input is visible on the spinner, error and table screens.
- Selection survives the reload after a create (C3).

**sg_list, vpc_list, subnet_list**
- Columns per tier (sg 5/6, vpc 4/6, subnet 5/5/9) with the same titles and widths.
- Detail tabs in the same order (sg: Info, Inbound, Outbound, JSON, Tags; vpc: Info, CIDR Blocks, JSON, Tags; subnet: Info, Config, [IPv6], JSON, Tags) with the same link lines.
- sg rule formatting (`formatProtocol`, `formatPorts`, `formatRuleSources`, `buildRulesContent`) is unchanged.
- vpc `n` opens subnet_list with `vpc_id`; an empty cursor does nothing.
- Copy toast "Copied: <id>"; space does nothing.
- Two subnet lists with different vpcIDs still route separately (H4).

## Migration plan

Rules for every PR:

- One view per port PR. Each PR adds only the core features its view uses (list below), so each feature lands with its first real user and its smoke test.
- `task test:race` (`go test -race -shuffle=on ./...`, the same as CI), then golangci-lint with gosec, modernize and testifylint. Plain `task test` has no race detector and no shuffle (`Taskfile.yml:66` vs `:71`, `ci.yml:43`).
- Tests that change globals (`ui.SetReadOnly`, the theme, the event log) restore them with `t.Cleanup`.
- Smoke test in the real app with the LocalStack pty setup for each view the PR touches. Each PR names its seed tier. When LocalStack cannot show a change, the PR says "unit tests only" for it.
- The PR body has a "Parity checklist" (the lines above for that view) and a "Deliberate changes" section. Each deliberate change has a test.

Core features per PR:

| PR | Adds to `listConfig` / `listCore` |
|---|---|
| 4 (sg) | required fields, `Copy`, `OpenDetail`; `init`, `startLoad`, `reload`, `update`/`handle`/`passThrough`, `listFailedMsg` + `loadError`, `render`, `handleKey`, `footer`, `focus`, `find`, `cursorItem`, `setItems`; `fetchAll` |
| 5 (vpc, subnet) | paging: `fetchPages`, `nextToken`, append, `dedupe`, progress label |
| 6 (ami) | `MultiSelect`, `CopyNoun`, `targets`, `Refresh` + `refreshBy` + `refreshKeys` + `mergeItems` + `idle` + `busy`, `FocusMissing`, `OnLoaded`, `loadSpec` + `loadWith`, `footerWith` |
| 7 (ec2) | `backgroundReload`, `updateItems` |
| 8 (sqs) | `Complete`, `Derive`, `FocusKey`, `refreshEach`, `removeKeys` |
| 9 (s3) | `OpenDetail: nil` path, `render` `below` used for real |

**PR 0: characterization tests (tests only).**
- Add `list_characterization_test.go`: one table for all 7 views. It names no private message type. It builds each view with a mock service, runs `Init()` or presses `r`, and feeds the produced messages back with `deliverAll` or `collectMsgs`, as `load_gen_test.go:31-55` does. For stale cases it captures two cmd results and delivers them out of order. sg, ami and s3 have no gen field today, so their stale cases record today's behavior (last reply wins).
- If a case still needs a private literal, it goes in a per-view file (`list_characterization_<view>_test.go`). The port PR of that view may edit only that file. The plan says so in each PR body.
- Record as literals: KeyMap snapshot; Footer after load, while loading, with one selected; View text (ANSI stripped) for first load, reload and error, and for s3 `n` on the error screen; column counts at widths 60/100/160/200; esc with and without selection; copy toast; `r` (loading flag, which mock method runs); stale page drop; pending focus; focus on a missing ID; nav keys during a load; the AMI search error context.
- Record current bugs too, each with a comment naming the divergence number that will change it.
- Move `keyPress`, `firstOfType`, `deliverAll`, `collectMsgs` into `helpers_test.go`.
- In `internal/app`, add `findMsgPrefix(msgs, prefix, suffix)` next to `findMsg`.
- Verify: `task test:race` only. No app change.

**PR 1: shared detail helpers and the filter source, no generics.**
- Add `detail.go`. Move `buildTagsContent`. Switch the Info builders of vpc, subnet, sg and ec2 to `buildInfo`. EC2 builds Root Device as `""` when both parts are empty.
- Add `FilterChangedMsg.Source` and `Filter.WithSource`. AMI search and S3 create set a source and ignore sourced messages. Delete the S3 `creating` gate in the filter case and the `table.Filter("")` hack. Remove the AMI duplicate hint.
- Tests: `buildInfo` (skip empty, link lines); an EC2 instance with an empty root device prints no Root Device line, and the VPC, Subnet and AMI link `Line` values are unchanged; `TestFilter_SourceTagged`, `TestAMIList_SearchDoesNotFilterTable`, `TestS3List_CreateInputDoesNotFilterTable`. Existing Info and link tests stay unchanged. Update the two PR 0 snapshots.
- Verify in the app (small tier): AMI `p`, type "linux", esc: all rows still show. S3 `n`, type, esc: no filter. Open the EC2, VPC, subnet and SG detail panels and follow each link line.

**PR 2: action helpers on today's views (ec2, sqs, s3), no generics.**
- Add `actions.go` (`pendingConfirm`, `confirmMessage`, `runEach`). Wire it into today's ec2, sqs and s3 views. Add the `ec2Actions` table with a `busy` label per action.
- Deliberate changes: #24 names in confirm prompts, #25 the confirm Action is checked, #26 "Stopping"/"Terminating" labels, #27 sqs spinner after confirm, the ReadOnly recheck in `onConfirm`.
- Tests: `pendingConfirm` (decline clears, other Action clears); `confirmMessage` (names capped and sanitized); spinner "Stopping 1 instance(s)..."; confirm with a different Action does nothing; ReadOnly turned on between picker and confirm means no API call; purge prompt lists names.
- Verify in the app (small tier): stop and start one and two instances; purge a queue; delete a bucket.

**PR 3: small fixes on today's views, no generics.**
- #19 detail fetch error gives a toast and the list stays (ec2, sqs, s3 props). #40 s3 create error gives a toast. #42 single-bucket prompt sanitized. #43 s3 props nil guards. #46 `SortKeyInt` for sg In/Out and subnet Avail IPs. #47 nil guards for sqs and AMI search.
- Tests: one per fix.
- Verify in the app (small tier): s3 props, a bad bucket name, sort sg by In and subnets by Avail IPs.

**PR 4: core + sg_list.**
- Add `listcore.go`, `listcore_ui.go`, `listmsgs.go` with only the PR 4 features. Port sg.
- Tests: `listcore_test.go` with a fake item (gen drop, page-1 replace keeps the cursor, stale `listFailedMsg` dropped with no `ErrorMsg` emitted, current failure emits one `ErrorMsg` with the load's context, the echo is not applied twice, bare `ErrorMsg` applied, success clears err, tier rebuild, spinner ticks while visible and not loading, `handle` returns false for a non-sort picker, constructor panics on a missing field; `fetchAll` with a nil service returns `errNoClient`).
- Add `list_conformance_test.go` with the sg case: `require.Same` identity; `nav.View`, `nav.InputCapturer`, `nav.ContextSetter` assertions; `SetContext` reaches the fetch; no global letters bound (q P R T W E L ? :); RW keys are `ModeReadWrite`; a nil service gives an `ErrorMsg`, not a panic; reflection test that no view declares `table`, `filter`, `spinner`, `items`, `loading`, `err`, `loadGen`, `pendingFocus` or `widthTier`, embeds `viewCtx`, or declares a method with the name of a `*listCore[T]` method.
- sg test edits: `view.groups` → `view.items`, `findSG` → `find`, a `deliverItems[T]` helper that uses `gen: v.loadGen`.
- Deliberate changes: gen guard (view and status bar), table usable during refresh.
- Verify in the app: small tier: open Security Groups, scroll during a refresh, follow a VPC link and back. The stale-reply race cannot be seen here (LocalStack answers at once); unit tests only. Error screen: stop LocalStack (or set a wrong endpoint), open the view, see the error in the view and status bar, start LocalStack, press `r`, see the table.

**PR 5: vpc_list and subnet_list.**
- Port both. Change `routing_test.go:301-302` to `findMsgPrefix(..., "views.listPageMsg[", "aws.Subnet]")`.
- Tests: subnet resize wide → medium → narrow uses the narrow widths; append + dedupe across pages; progress label on page 2; vpc `n` test unchanged; H4 routing test unchanged.
- Deliberate changes: rows stay visible during refresh; subnet widths follow the tier; progress label.
- Verify in the app (small tier): VPCs, `n`, a subnet link back to its VPC, resize the terminal across 80 and 120 columns. Paging is unit tests only: `ListVPCsPage` and `ListSubnetsPage` send no `MaxResults` (`ec2.go:776, 818`), so LocalStack returns one page.

**PR 6: ami_list.**
- Port AMI. Remove `amiListLoadedMsg` and `amiRefreshedMsg`; keep `amiFocusLoadedMsg`.
- Tests: `ownedMode`/`lastQuery` → `shown`; `TestAMIList_SearchThenOwned_LastStartedWins`; search then `r` calls ListOwnedAMIs and shows "(owned)" after the load; a failed search has Context "searching AMIs"; refresh with Canceled shows no toast; enter gives a `TabbedContentMsg` with a "JSON" tab (changes `ami_list_test.go:97-110`); `ami_focus_test` flows keep their assertions.
- Verify in the app (small tier): EC2 detail → AMI link (owned), `p` search, `r` back to owned, select two and `r`. Use the slow search (DescribeImages takes about 8 s on LocalStack): start a search, press `r` at once, and see that the owned list stays (stale-reply guard). The other-account AMI link (`FocusMissing`) is unit tests only: every seeded instance uses a self-owned seeded AMI (`cmd/seed/tiers.toml:139-170`).

**PR 7: ec2_list.**
- Port EC2. Only core-driven changes remain.
- App tests: the six `"views.ec2PageLoadedMsg"` lookups (`input_test.go:52`, `ops_test.go:116`, `routing_test.go:168, 189, 217, 253`) use `findMsgPrefix`. `"views.ec2InstanceMutatedMsg"` lookups stay.
- Tests: stale refresh reply after a full load is dropped; `m` works during the delayed reload; focus during the delayed reload opens at once (`backgroundReload`); `pendingInstanceIDs` → `pending.keys`, `findInstance` → `find`, `instances` → `items`, `actionsForState` becomes a free func. `results_test` and `outcome_test` texts unchanged.
- Verify in the app (enterprise tier: 1,005 instances, 2 pages at 1000): progress "N so far", rows stay during the delayed refresh, start and stop one and two instances, SSM refusal paths, leave the view during a stop and see the Outcome toast.

**PR 8: sqs_queues.**
- Port SQS. Remove `queueURLs`, `attrsLoading`, `sqsQueuesPageMsg`, `sqsAllAttrsLoadedMsg`, `sqsQueueRefreshedMsg`.
- Tests: load_gen tests count Complete calls and use `listCompleteMsg`; the 9 `sqsAllAttrsLoadedMsg` literals become a `loadQueues(v, ...)` helper; zero queues clear stale rows; three queues refreshed, one fails: two rows update and one toast; `refreshEach` gives each key its own timeout; refresh reruns DLQ marking; N selected queues give one refresh reply; `purgeQueues`/`deleteQueues` direct calls keep working (they stay view methods). Exact toast texts unchanged.
- Verify in the app (enterprise tier: more than 200 queues, ListQueues pages at 100): progress over pages, send to a standard and a FIFO queue, purge, delete two with one failing, select many queues and `r`, DLQ link focus, peek and back.

**PR 9: s3_list.**
- Port S3. View-owned enter, `d`, `n`, `x`. Props becomes a tabbed panel. ListBuckets uses listTimeout (if approved). The create input is passed as `below`.
- Tests: `s3BucketsLoadedMsg` literals use `deliverItems` with `gen: v.loadGen` (important: create success now raises `loadGen`, so `row_identity_test.go:71-80` must use the helper); `creating` → `createInput.Active()`; help strings kept; `n` on the error screen shows the input; delete op test unchanged; the s3 teatest passes.
- Verify in the app (medium tier): create a bucket, create a bad name, delete one and two buckets, cancel a delete with ctrl+x, props on a narrow and a wide terminal, `n` on the error screen (LocalStack stopped).

**PR 10: cleanup and docs.**
- Delete the dead `msg` types (`ResourcesLoadedMsg`, `LoadingMsg`, `RefreshMsg`, `ProfileChangedMsg`, `RegionChangedMsg`).
- Remove PR 0 snapshots that the conformance suite now covers.
- Add "Adding a list view" to the architecture doc (simple English, VPC as the example): required config fields, the adapters, the no-shadowing rule, the identity rule, the conformance case.
- Tick A10 in the tracker. Record follow-ups: sg UTF-8 truncation, A5 routes, A7 typed replies (would remove the `%T` coupling), clearing the status bar error after a good reload.

The order differs from the tracker (AMI before EC2) on purpose. AMI adds selection, selective refresh, async focus and a side input without any mutation. PR 2 and PR 3 move the fixes that do not need the core out of the big EC2 and SQS ports, so those ports change only what the core changes.

## Test strategy

**Characterization (PR 0).** One table-driven test records today's behavior for all 7 views through `Update`, `View`, `Footer`, `KeyMap` and the cmds they return. It uses mocks and cmd results only, and names no private message type or field, so the shared file survives the ports unchanged. The few cases that need a private literal live in a per-view file that only that view's port PR may edit. When a PR changes behavior on purpose, the diff of the snapshot shows exactly what changed, and the PR lists it under "Deliberate changes". Because PR 2 and PR 3 land the non-core fixes first, the snapshot diffs of the EC2 and SQS ports hold only core-driven changes.

**Core unit tests (`listcore_test.go`).** A fake `type fakeItem struct{ ID, Name string }` with a fake fetch. They cover the generation rules (pages, complete, refresh, failures), page replace and append, dedupe, Complete, refresh merge with partial results, `loadWith` and the reset on `reload`, the `OnLoaded` tag, focus, error handling and the echo, tiers, spinner ticks, copy and keys. Adapter tests: nil service gives `errNoClient`; `refreshEach` returns partial items and a wrapped error.

**Conformance suite (`list_conformance_test.go`).** One case per view, added in the view's port PR. It checks identity (`require.Same` for about 12 message kinds), the three optional interfaces, `SetContext` reaching the fetch, no global letters, RW keys marked, nil service safe, the reflection shadowing check (fields and method names), sanitized error in theme red, footer format, state column color, and the multi-select checks (space, esc, "(1 selected)", copy many, selective `r`, C4) for multi-select views. Runs under `task test:race`. Each case restores ReadOnly and the theme with `t.Cleanup`.

**Existing tests that change** (mechanical renames; the assertions stay):

| File | Change |
|---|---|
| `vpc_list_test.go`, `subnet_list_test.go`, `sg_list_test.go` | `vpcs`/`subnets`/`groups` → `items`; `findX` → `find`; loaders use `deliverItems` |
| `ec2_list_test.go` | `instances` → `items`; `pendingInstanceIDs` → `pending.keys` (PR 2); `findInstance` → `find`; `actionsForState` is a free func; loader uses `deliverItems` |
| `ami_list_test.go`, `ami_focus_test.go` | `amis` → `items`; `ownedMode`/`lastQuery` → `shown`; enter test expects `TabbedContentMsg` |
| `s3_list_test.go` | `buckets` → `items`; `creating` → `createInput.Active()`; loader uses `deliverItems` |
| `load_gen_test.go` | `ec2PageLoadedMsg` → `listPageMsg[aws.Instance]`; sqs tests use `listCompleteMsg` and `v.buf` |
| `row_identity_test.go` | `selectedXIDs()` → `targets()`; message literals → helpers; `startLoad` and `loadGen` still compile through promotion |
| `results_test.go` | `threeQueues` uses `loadQueues`; texts unchanged |
| `render_safety_test.go`, `error_style_test.go` | constructors unchanged; loaders use helpers |
| `internal/app/input_test.go`, `ops_test.go`, `routing_test.go` | page lookups use `findMsgPrefix` (8 sites) |

Tests that go only through `Update` and cmds (most behavior tests, `outcome_test.go`, the app tests apart from lookups, the s3 teatest) stay as they are.

## Risks and open questions

### Risks

1. **Visible behavior changes come in the port PRs.** Rows stay during reloads, tables move during loads, AMI and S3 props open tabbed panels, subnet widths follow the tier. Detail-error toasts and named prompts come earlier, in PR 2 and PR 3. PR 0 snapshots make each one a visible test diff. The owner approved them as a set (decision 11).
2. **Field and method shadowing.** A view field named `table`, a second `viewCtx` embed, or a view method with a core method name would silently split the state or confuse readers. The reflection test catches all three in CI.
3. **`%T` coupling in app tests.** Generic names are long (`views.listPageMsg[github.com/.../aws.Instance]`). `findMsgPrefix` keeps the change to one line per site. A missed lookup fails loudly (`require.NotNil`), not silently.
4. **Goroutine misuse.** A fetch closure that reads changing view state would race. Fetch funcs read only constructor values (the service). AMI captures the search query on the Update goroutine in `search`. The fetch cmd captures `c.load` on the Update goroutine. `task test:race` runs the suites.
5. **Method value on a nil interface** (`ec2.RefreshInstances` with a nil `ec2`) panics in the constructor. The adapters take a method expression and the service separately, so the unsafe form does not type-check where they are used. The conformance suite still builds each view with a nil service.
6. **Re-entrancy.** `OpenDetail`, `OnLoaded` and `FocusMissing` run inside `handle`. They must not call `update` again. Documented in the config comments.
7. **Review size.** PR 7 and PR 8 are still the largest. PR 2 and PR 3 take the non-core fixes out of them, and behavior assertions stay unchanged so reviewers can diff them.
8. **One event-loop hop for load errors.** A current failure reaches the status bar one message later than today (`listFailedMsg`, then `ErrorMsg`). No user can see the difference. The echo is marked with `*loadError`, so a reload started in between is not overwritten by it.

### Decisions

The owner accepted the proposed answer to each question below on 2026-11-08.

1. **Detail fetch errors** (ec2, sqs, s3 props, s3 create): show a toast and keep the list (proposed), or keep the full error screen?
2. **Reload display:** keep the old rows visible with a footer spinner during every reload (proposed; sg and ami do this today), or keep the full-screen spinner for vpc, subnet, ec2, sqs and s3?
3. **Confirm prompts:** is `"Stop 2 instance(s)?\n  web (i-1)\n  db (i-2)\n"` fine? Should Start (Quick y/n) list names too (proposed yes)? Keep the "confirm" phrase for ec2 and sqs (proposed)?
4. **Pending focus after a load error:** keep it so it fires on the next good load (proposed, as today), or clear it?
5. **AMI enter and S3 props:** move both to the tabbed panel with one "JSON" tab (proposed)?
6. **S3 ListBuckets timeout:** listTimeout (proposed, it pages inside) or keep callTimeout?
7. **S3 `y`:** add "copy bucket name" (it is free from the core), or keep no `y` (proposed, to keep the port small)?
8. **Side inputs:** add `Source` to `ui.FilterChangedMsg` (proposed, small ui change), or wrap the side input's cmd in the views to drop its `FilterChangedMsg`?
9. **EC2 Reboot optimistic state:** keep "pending" (proposed), or show no optimistic state?
10. **Selective refresh:** keep rows that AWS no longer returns (proposed, as today), or remove them?
11. **Port policy:** unify behavior inside the port PRs, with each change listed and tested (proposed), or port with zero visible change behind temporary config knobs and unify in small follow-up PRs (the risk draft; about 5 extra knobs and 6 extra PRs)?
12. **Port order:** sg, vpc+subnet, ami, ec2, sqs, s3 (proposed) instead of the tracker order (ec2 and sqs before ami)?
13. **A5:** keep data-driven routes as a separate follow-up phase (proposed)? Without it, a new list view still edits `resolveView` and the registry.
14. **SQS refresh error toast:** today a failed queue refresh is silent (log only). Show one toast "Refresh failed: N of M failed: ..." (proposed, same as ec2 and ami), or keep it silent for sqs?
15. **Status bar after recovery:** leave the old error text in the status bar after a good reload (proposed for this phase, as today), or add a small app change that clears it when the same view loads again?

## Review notes

1. parity, AMI error context: accepted. `ami_list.go:174` vs `:192` differ and the app shows the Context in the status bar. The context is now per load (`loadSpec.errContext`), with a test.
2. parity, `r` after a search and the `shown` footer: accepted. Nothing reset `fetch`. `reload` now restores the default spec, and `OnLoaded(items, tag)` gets the tag of the load that finished. Test added.
3. parity, SQS refresh timeout and partial results: accepted. Today each queue has its own `callTimeout` cmd (`sqs_queues.go:268-277`). New `refreshEach` keeps per-key timeouts in parallel, and `onRefreshed` merges partial results before the toast.
4. parity, stale error still in the status bar: accepted. `app.go:256-257` sets the status bar before the view sees the message. Failures now return `listFailedMsg` and the core emits `ErrorMsg` only for a current load. #8 for the status bar is recorded as a known limit (same as today) and open question 15.
5. parity, s3 create input on the error and spinner screens: accepted. `s3_list.go:509-511` draws it after the switch. `render` adds `below` in every branch; PR 0 snapshot added.
6. parity, ec2 delayed reload sets `loading`: accepted. `ec2_list.go:335-337` does not set `loading`. New `backgroundReload` keeps that; SSM end keeps `reload` because `ec2_list.go:351` sets `loading` today. Divergence #53.
7. api, AMI search cannot be expressed: accepted. Same root as 1 and 2; fixed by `loadSpec` with context and tag, and the reset in `reload`.
8. api, `fetchAll` invites a method value on a nil interface: accepted. Adapters now take the service and a method expression and check nil inside.
9. api, SQS refresh with one shared timeout: accepted. Same fix as 3; the adapter owns the timeout, so no core `RefreshTimeout` is needed.
10. api, `listConfig` size and `CopyNoun` doing two jobs: accepted. `Complete` is one optional `*completePhase[T]`, `Copy bool` turns `y` on, and the scores text no longer counts the base config against risk.
11. api, `e.fetchPage` shadows the core method name: accepted. EC2 now uses `fetchPages` (no view method), the core method is `fetchCmd`, and the reflection test also checks method names.
12. migration, PR 0 uses private messages: accepted. The private types are removed in later ports, and sg, ami and s3 have no gen today. PR 0 now uses mocks and cmds only, with per-view files for the rare literal.
13. migration, non-core fixes bundled into the EC2 and SQS ports: accepted. New PR 2 (action helpers) and PR 3 (small fixes) land them on today's code.
14. migration, LocalStack steps cannot show the change: accepted. Seed defaults to small, VPC and subnet calls send no MaxResults, seeded AMIs are all self-owned. Each PR now names its tier, marks unit-test-only checks, uses the slow AMI search for the race, and stops LocalStack to show the error screen.
15. migration, PR 1 smoke test misses EC2/VPC/subnet/SG detail: accepted. The Root Device change is new code (`ec2_list.go:831, 837`). Added the detail-link smoke step and a unit test for an empty root device.
16. migration, `task test` has no `-race`: accepted. `Taskfile.yml:66` is plain; the rule now says `task test:race`, and tests restore globals with `t.Cleanup`.
17. migration, PR 2 ships unused core features: accepted. A table now lists which config fields and methods each PR adds.
