# Architecture

LazyCloud follows the [Elm Architecture](https://guide.elm-lang.org/architecture/) (Model-View-Update) via Bubble Tea.

## Package Layout

```
cmd/seed/               Seed program for populating LocalStack with test data. See docs/DEVELOPMENT.md.
internal/aws/           Service interfaces (S3Service, EC2Service) + SDK implementations. No UI imports.
internal/aws/awstest/   Shared testify mocks for service interfaces (used by view and app tests).
internal/views/         Bubble Tea models. Calls AWS layer via tea.Cmd. Handles input and rendering.
internal/ui/            Reusable components (table, picker, toast, etc.). Not tied to any AWS service.
internal/app/           Root model — message router, layout compositor, view factory, side panel.
internal/nav/           Stack-based navigator with view caching.
internal/msg/           Shared message types for the event loop.
internal/config/        TOML config with layered precedence (file < env < flags).
internal/eventlog/      Thread-safe ring buffer for in-app event logging. See docs/LOGGING.md for guidelines.
```

## Runtime Patterns

### Navigator (View Stack)

Views are pushed onto a stack when drilling into resources and popped on `esc`. Each view implements the `nav.View` interface (`ID()`, `Title()`, `KeyMap()`). Views are cached by ID so navigating back preserves scroll position and filter state.

### Message Flow

All side effects (AWS API calls, clipboard, file I/O) happen in `tea.Cmd` goroutines that return messages. Views never mutate state directly — they emit messages like `NavigateMsg`, `ToastMsg`, or `RequestConfirmMsg` that the app routes.

### Message Routing

The navigator tags each command that a view returns (`nav.Tag`). The result of the command reaches the app as `nav.Routed{Origin, Msg}`. The app delivers it by these rules:

- A view message, for example a page of results, goes to the origin view. This is also true when the user has navigated away from the view. The view keeps loading in the background and shows its results when the user comes back.
- If the origin view no longer exists, the app drops the message. A profile, region, theme, or `:home` switch builds a new navigator, so results from the old session never reach the new views.
- Navigation, overlays, and the side panel (`NavigateMsg`, `RequestConfirmMsg`, `TabbedContentMsg`, and similar) take effect only when the origin view is on top. A late result cannot move the user or open a dialog over another view.
- Toasts always show.
- An `ErrorMsg` goes to the origin view. The status bar shows the error only while that view is on top.

Bubble Tea runtime messages (batches, exec, clipboard, quit) pass through `nav.Tag` unwrapped, because the runtime must handle them. For a batch, each inner command is tagged.

Follow these rules when you write a view:

- Do not assume that the view on top receives the result of your command. The result goes to the view that started the command.
- For a paginated list, give each full load a generation. Put the generation in each page message, and drop pages from an older load. See `startLoad` in `internal/views/ec2_list.go`. Without a generation, a refresh during a load duplicates rows.
- The navigator calls `Init` each time it shows a cached view. If a load is already running, do not start a second one.

### Table Rows and Selection

`ui.Table` identifies each row by a key: the ID of the resource in the row. A view passes `[]ui.Row` values with a `Key`, the `Cells`, and optional `Sort` values. The cursor and the selection follow the key. They stay on the same resource when the rows are reloaded, sorted, or filtered.

Rules for view code:

- Use a key that is unique in the list, for example an instance ID, a bucket name, an S3 object key, a queue URL, or a message ID.
- Read the row under the cursor with `CursorKey()`, and find the resource by its key. Do not read the ID from the cell text.
- For an action on several rows, use `TargetKeys()`. It returns the selected keys, or the cursor key when no row is selected.
- To move the cursor to a resource, for example for a focus link, use `SetCursorByKey`.
- For the first page of a load, call `SetRows`. For each later page, call `AppendRows`, which sorts only the new rows.
- When the width tier changes the column set, call `SetColumns`, then `SetRows` with the cells in the new column order. The sort stays on the column with the same title. Give each column a unique title, or keep a shared title, such as an empty icon column, at the same index in every tier.
- The filter deselects the rows that it hides, so an action never applies to a row that the user cannot see.
- A selected key whose row is not loaded stays selected, so a reload keeps the selection. After an action removes resources, call `DeselectAll()`.

### Progressive Loading

Large S3 listings use command chaining: each page fetch returns a message, the handler appends data and returns a command for the next page. The table updates after each page so users see results within ~200ms.

### Overlay Compositing

Pickers, confirm dialogs, and toasts render on top of existing content using Lipgloss's Canvas/Layer system. The background view stays visible around the overlay.

### Contextual Keybindings

Each view declares its own `KeyMap()` using `key.Binding` structs. The status bar merges view-specific hints with global hints, so available actions update automatically as you navigate. See [KEYBINDINGS.md](KEYBINDINGS.md) for the full dispatch system.

### Toast Notifications

Transient feedback (copy, download, delete) uses auto-dismissing toasts rendered in the bottom-right via Compositor overlay. Each toast gets a `time.Sleep` goroutine that sends a dismiss message after 4 seconds.

### Access Mode

LazyCloud starts in **ReadOnly** mode by default. In ReadOnly mode, LazyCloud blocks all mutating operations (create, delete, copy, move), whatever your AWS IAM permissions allow. Press `W` to switch to ReadWrite mode. Then only IAM limits what you can do. The header shows the mode as an `RO` or `RW` badge. `ui.IsReadOnly()` reads the mode, and `ui.SetReadOnly()` changes it.

Two guards enforce ReadOnly mode:

1. When the user presses a key, the app looks for it in the `KeyMap()` of the view on top. If a binding with `ui.ModeReadWrite` has that key, the app shows the toast "ReadOnly mode — press W to switch", and the view does not get the key.
2. When the action runs, the AWS service refuses it. The app wraps each service with `aws.GuardS3`, `aws.GuardEC2`, or `aws.GuardSQS`. In ReadOnly mode, each mutating method returns `aws.ErrReadOnly` before it sends a request. This guard also covers a prompt, a picker, or a form that the user opened in ReadWrite mode.

Rules for view code:

- Give each key that changes AWS resources `Mode: ui.ModeReadWrite` in `KeyMap()`. Do not test `ui.IsReadOnly()` for these keys in the view.
- Call AWS only through the services that the app gives the view.
- When you add a mutating method to a service interface, add a guard for it in `internal/aws/readonly.go`. A test in `readonly_test.go` fails until each interface method is listed as a read or as a mutation.

### AWS Sessions

A session is one profile and one region. `aws.NewServices` builds its client and its guarded services into one `aws.Services` value, and the app keeps it in `m.svc`. The app builds every session through this function: at startup and in `switchSession()`, which handles both a profile switch and a region switch.

- If the client cannot be built at startup, for example because the profile does not exist, the app starts without a session. Home shows the error, and the app opens no AWS view until a switch succeeds. The event log still opens.
- If a switch fails, nothing changes. The old session, the views, and the configuration stay, and a toast shows the error.
- If a switch succeeds, `resetNav()` builds a new navigator with a new home view. It closes the panel and clears the stashed panel, the cross-navigation history, and the error. Then the app opens the service view that was on top again. `topLevelViewID()` finds it: the nearest view under the top of the stack that is a service view in the registry.

A theme switch and `:home` also call `resetNav()`. Each time Home is shown again, the app calls its `Init()`. This starts a new chain of animation ticks with a new generation, and Home ignores the frames of older chains.

### Input Capture

A view that takes text input implements `nav.InputCapturer`. Its `CapturingInput()` returns true while a filter, a form, or another input is active. While it returns true, the app sends every key except `ctrl+c` to the view. Typed letters such as `q`, `W`, `:`, or `tab` do not run app-wide keys, and the status bar shows only the hints of the view. `FormView` always returns true.

When a view gets a filter or another text input, add the input to `CapturingInput()`.

### Side Detail Panel

When the terminal is at least 120 columns wide, pressing `d` (describe) or `enter` (preview) opens a side panel alongside the main view. The panel supports **tabbed views** — EC2 instance detail shows Info, JSON, Security Groups, and Tags tabs (press `1-4` to switch). Lines marked with `→` are navigable: press `enter` to follow a cross-resource link (e.g., from an EC2 instance to its security group). Press `tab` to toggle focus between the main view and panel. On narrow terminals, content opens full-screen.

When the panel is open, the header breadcrumbs append the panel title (e.g., `Services › EC2 Instances › web-server-1 (i-abc123)`) so it's always clear which resource is being viewed.

### Selective Refresh

Views that support multi-select (`space`) implement selective refresh on `r`. When resources are selected, `r` re-fetches only those items instead of reloading the entire list.

**Branch on selection count** in the `r` key handler:

```go
case key.Matches(m, v.keys.Refresh):
    if ids := v.selectedResourceIDs(); len(ids) > 0 && v.table.SelectionCount() > 0 {
        eventlog.Infof(eventlog.CatAWS, "Selective refresh: %d resources", len(ids))
        v.spinner.Show(fmt.Sprintf("Refreshing %d resources...", len(ids)))
        return v, tea.Batch(v.spinner.Tick(), v.refreshResources(ids))
    }
    // Fall through to full refresh
    v.loading = true
    v.spinner.Show("Loading resources...")
    return v, tea.Batch(v.spinner.Tick(), v.fetchAll())
```

**Add a batch-by-ID service method** (e.g., `RefreshInstances(ctx, ids) ([]Instance, error)`) that calls the AWS Describe API with an ID filter. Reuse the existing `map*()` functions to return the same type as the list view uses.

**Handle the response with an in-place update** — find each returned item by ID and replace it in the slice, then rebuild rows:

```go
case resourcesRefreshedMsg:
    v.spinner.Hide()
    if m.err != nil {
        return v, func() tea.Msg { return msg.ToastError("Refresh failed: " + m.err.Error()) }
    }
    for _, updated := range m.resources {
        for i := range v.resources {
            if v.resources[i].ID == updated.ID {
                v.resources[i] = updated
                break
            }
        }
    }
    v.rebuildRows()
    return v, nil
```

**Rules:**
- Do NOT clear the items slice or call `DeselectAll()` — the in-place update preserves selection automatically
- Do NOT set `v.loading = true` — selective refresh is non-destructive, the existing table stays visible
- Hide the spinner in the response handler on both success and error
- Log the selective refresh to the event log with the count

Reference implementations: `sqs_queues.go` (per-resource), `ec2_list.go` (batch API), `ami_list.go` (batch API).

### Cross-Resource Navigation

Cross-resource links use the `FocusResourceMsg` pattern. When a link includes a `focus` parameter, the target view auto-selects the matching resource and opens its detail panel. If the view is still loading data, the focus is deferred until the data arrives. This lets users follow links seamlessly — e.g., clicking a security group in an EC2 instance detail navigates to the SG list with that group's detail panel already open.

**Back traversal:** Before navigating forward, the app saves the current panel state (title, tabs, links) and the stack depth to a bounded history stack (max 10 entries). Press `backspace` to pop the history. The app goes back to the view where the user followed the link, also when the user opened more views after the link, and restores its panel. This preserves the user's context through chains of cross-resource links. The history is automatically invalidated when the user manually navigates back via `esc`/`q`. A profile, region, or theme switch and `:home` clear it.

### Forms

Multi-field input forms use `charm.land/huh/v2` wrapped in a `FormView` (`internal/ui/form.go`). Unlike overlays (confirm, picker), forms are pushed onto the nav stack as full views. Views declare fields via `RequestFormMsg` with declarative `FormField` specs — no `huh` import needed. The `FormView` translates specs to huh fields, applies the LazyCloud theme, and emits `FormResultMsg` on submit/cancel.

**Key handling:** A `FormView` always captures input (see Input Capture), so the app gives it every key except `ctrl+c`. The status bar shows only the hints of the form.

**Field types:** `input` (single-line), `text` (multi-line textarea), `select` (single choice), `confirm` (yes/no). Custom validation via `FormField.Validate`.

**Dirty tracking:** `FormView` snapshots initial field values at construction and compares on Esc. If any field changed, it shows a "Discard unsaved changes?" confirm overlay before closing. Two huh details matter here:
- `form.GetString(key)` only returns values committed on field transition (Tab/Enter). The focused field's live value must be read via `form.GetFocusedField().GetValue()`.
- While the confirm overlay is visible, the overlay priority system intercepts all keys before they reach the form, so form data stays intact without extra state management.

---

# Architecture Guidelines

## Service Sub-Navigation Tree

As LazyCloud grows beyond EC2 and S3, the navigation hierarchy should follow this structure. Each service defines its own sub-resources, accessible via the home screen feature picker and command mode (`:service/subresource`).

```
Home (service grid)
├── EC2
│   ├── Instances        (list → detail with tabs: Info / SGs / Volumes / Tags)
│   ├── AMIs             (list → detail)
│   ├── Security Groups  (list → detail with rules)
│   ├── Volumes          (list → detail)
│   └── Key Pairs        (list)
├── S3
│   └── Buckets          (list → objects → versions)
├── SQS
│   └── Queues           (list → detail with tabs / message peek)
├── Lambda
│   ├── Functions        (list → detail with tabs: Config / Env / Triggers / Logs)
│   └── Layers           (list → detail)
├── ECS
│   ├── Clusters         (list → services → tasks)
│   └── Task Definitions (list → detail)
├── IAM
│   ├── Users            (list → detail with policies)
│   ├── Roles            (list → detail with trust policy)
│   └── Groups           (list → detail)
├── RDS
│   └── Instances        (list → detail)
└── CloudWatch
    ├── Log Groups       (list → streams → log viewer)
    └── Alarms           (list → detail)
```

### Adding a New Service

1. **AWS layer**: Create `internal/aws/<service>.go` with a service interface and SDK implementation
2. **Session**: Add the service to `aws.Services` and `aws.NewServices` in `internal/aws/services.go`. Add a ReadOnly guard for its mutating methods in `internal/aws/readonly.go`. Profile and region switches then rebuild it with no other change.
3. **Mock**: Create `internal/aws/awstest/mock_<service>.go` for testing
4. **Views**: Create one or more view files in `internal/views/`
5. **Registry**: Add a `Service` entry (with `Feature` sub-resources) and `Command` entries in `internal/registry/registry.go`. The home view and the command bar then show the service.
6. **View factory**: Add a case in `app.go`'s `resolveView()` for each new ViewID, with the service from `m.svc` (the sync test will catch any missing entries)
7. **Service doc**: Add `services/aws/<service>.md` describing supported features and API calls used

## Context-Aware Action System

Rather than scattering keybinding definitions across view files, views should declare their actions as structured data. This powers both the status bar and the `?` help overlay automatically.

### Action Definition

```go
type Action struct {
    Key         string   // e.g., "m", "ctrl+d"
    Label       string   // e.g., "Manage", "Delete"
    Description string   // e.g., "Open actions menu for selected instance"
    Mode        Mode     // ReadOnly, ReadWrite, or Any
    Category    string   // "Navigation", "Actions", "View" — for help overlay grouping
    Handler     func() tea.Cmd
}
```

### View Interface Extension

Each view exposes its actions:

```go
type ActionProvider interface {
    Actions() []Action
}
```

The root model collects actions from: global bindings + current view + focused UI component (table, detail panel). The status bar renders a subset (highest priority actions that fit). The `?` overlay renders all of them grouped by category.

### Benefits

- **Status bar and help overlay never drift out of sync** — both derive from the same action list
- **Keybinding conflicts are detectable** at registration time
- **New services get status bar hints for free** just by declaring actions
- **ReadWrite-only actions can be visually distinguished** (dimmed or badged in ReadOnly mode)

## Scaling Strategy

### Performance

- **Lazy loading**: Don't fetch resources for services the user hasn't visited. Only call AWS APIs when a view is first pushed onto the nav stack (via `Init()`)
- **Pagination**: Continue the existing pattern (S3 fetches 1000/page with progressive UI updates). Apply the same to EC2 DescribeInstances, Lambda ListFunctions, etc.
- **Cache invalidation**: A profile, region, or theme switch and `:home` build a new navigator in `resetNav()`, so no cached view survives them. Add TTL-based staleness detection: if a cached view is older than 60s, show a subtle "stale" indicator and auto-refresh on focus
- **Background refresh**: When a list view regains focus, trigger a non-blocking refresh. Show the cached data immediately, then update when fresh data arrives (optimistic UI)

### Terminal Size Adaptation

The layout should adapt to three terminal width tiers:

| Width       | Layout                                          |
| ----------- | ----------------------------------------------- |
| < 80 cols   | Single column: table only, no detail panel      |
| 80-119 cols | Table with abbreviated columns, no detail panel |
| >= 120 cols | Table + side detail panel (current behavior)    |

For height, the table should always show at minimum 5 rows. If the terminal is too short, hide the header or status bar before shrinking the table.

### Responsive Table Columns

Table columns use a **weight-based flex layout** (`ui.Column`) so they fill available terminal width instead of leaving empty space on wide terminals. Each column declares:

- `Width` — base/minimum width (used when terminal is narrow)
- `Weight` — flex factor for distributing extra space (0 = fixed width)
- `MaxWidth` — upper bound to prevent over-stretching (0 = no limit)

`ui.DistributeWidths(cols, totalWidth)` converts flex definitions to fixed-width `table.Column` values. The `Table` wrapper calls this automatically in `SetSize`, so views only need to declare flex columns.

**Weight guidelines for new views:**

| Column type                          | Weight | MaxWidth | Examples                     |
| ------------------------------------ | ------ | -------- | ---------------------------- |
| Text/name (variable, unbounded)      | 2      | 60-80    | Name, Bucket, Body, Desc     |
| IDs (fixed format, ~20-36 chars)     | 1      | 35       | Instance ID, VPC ID, ARN     |
| Dates/timestamps                     | 1      | 25       | Created, Launched, Modified  |
| Status/enum (short, fixed set)       | 0      | —        | State, Type, AZ, Protocol    |
| Numeric counts                       | 0      | —        | Messages, In, Out, Avail IPs |
| Boolean flags                        | 0      | —        | Default, Public, Latest      |

**Important:** Do not pre-truncate row data before putting it in the table. The bubbles table already truncates cell content with "…" at the actual column width. Pre-truncation prevents wider columns from showing more content.

### Command Registry Pattern

**Implemented in `internal/registry/registry.go`.** Services and commands are defined once and consumed by both the home view and the command bar.

```go
// Service is a top-level AWS service shown in the home grid.
type Service struct {
    Name     string
    Icon     ui.ServiceIcon
    Features []Feature        // sub-resources (e.g., Instances, AMIs under EC2)
}

// Command is a palette entry — either a navigation target or an app action.
type Command struct {
    Name        string
    Aliases     []string       // e.g., ["q", "qa", "qall"] for quit
    Description string
    ViewID      string         // empty for non-navigation commands (quit, mode, theme, etc.)
}
```

- `registry.Services` drives the home view's service grid
- `registry.Commands` drives the command bar via `registry.CommandBarEntries()`
- `registry.LookupCommand(input)` resolves names and aliases in `executeCommand()`
- A sync test (`TestRegistryNavCommandsCoveredByResolveView`) ensures every nav command has a corresponding view factory entry in `app.go`'s `resolveView()`

The view factory (`resolveView()`) remains in `app.go` because it needs the services of the current session (`m.svc`), which change on profile and region switches. The registry is the source of truth for *what exists*, and `resolveView()` handles *how to create it*.
