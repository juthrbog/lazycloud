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
internal/eventlog/      Thread-safe ring buffer for in-app event logging.
```

## Runtime Patterns

### Navigator (View Stack)

Views are pushed onto a stack when drilling into resources and popped on `esc`. Each view implements the `nav.View` interface (`ID()`, `Title()`, `KeyMap()`). Views are cached by ID so navigating back preserves scroll position and filter state.

### Message Flow

All side effects (AWS API calls, clipboard, file I/O) happen in `tea.Cmd` goroutines that return messages. Views never mutate state directly — they emit messages like `NavigateMsg`, `ToastMsg`, or `RequestConfirmMsg` that the app routes.

### Progressive Loading

Large S3 listings use command chaining: each page fetch returns a message, the handler appends data and returns a command for the next page. The table updates after each page so users see results within ~200ms.

### Overlay Compositing

Pickers, confirm dialogs, and toasts render on top of existing content using Lipgloss's Canvas/Layer system. The background view stays visible around the overlay.

### Contextual Keybindings

Each view declares its own `KeyMap()` using `key.Binding` structs. The status bar merges view-specific hints with global hints, so available actions update automatically as you navigate. See [KEYBINDINGS.md](KEYBINDINGS.md) for the full dispatch system.

### Toast Notifications

Transient feedback (copy, download, delete) uses auto-dismissing toasts rendered in the bottom-right via Compositor overlay. Each toast gets a `time.Sleep` goroutine that sends a dismiss message after 4 seconds.

### Access Mode

LazyCloud starts in **ReadOnly** mode by default. All mutating operations (create, delete, copy, move) are blocked at the UI level regardless of your AWS IAM permissions. Press `W` to switch to ReadWrite mode, which then falls back to normal IAM permission checks. The current mode is shown as an `RO`/`RW` badge in the header.

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

**Back traversal:** Before navigating forward, the app saves the current panel state (title, tabs, links) to a bounded history stack (max 10 entries). Press `backspace` to pop the history, navigate back, and restore the previous panel. This preserves the user's context through chains of cross-resource links. The history is automatically invalidated when the user manually navigates back via `esc`/`q`, and cleared on profile or region changes.

### Forms

Multi-field input forms use `charm.land/huh/v2` wrapped in a `FormView` (`internal/ui/form.go`). Unlike overlays (confirm, picker), forms are pushed onto the nav stack as full views. Views declare fields via `RequestFormMsg` with declarative `FormField` specs — no `huh` import needed. The `FormView` translates specs to huh fields, applies the LazyCloud theme, and emits `FormResultMsg` on submit/cancel.

**Key handling:** When a `FormView` is active, all global key handlers (`q`, `T`, `P`, etc.) are bypassed so text input works. Only `ctrl+c` remains global. The status bar shows only form-relevant hints.

**Field types:** `input` (single-line), `text` (multi-line textarea), `select` (single choice), `confirm` (yes/no). Custom validation via `FormField.Validate`.

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
2. **Mock**: Create `internal/aws/awstest/mock_<service>.go` for testing
3. **Views**: Create one or more view files in `internal/views/`
4. **Registry**: Add a `Service` entry (with `Feature` sub-resources) and `Command` entries in `internal/registry/registry.go` — this automatically populates the home view and command bar
5. **View factory**: Add a case in `app.go`'s `resolveView()` for each new ViewID (the sync test will catch any missing entries)
6. **Service doc**: Add `services/aws/<service>.md` describing supported features and API calls used

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
- **Cache invalidation**: Profile/region changes already call `ClearCache()`. Add TTL-based staleness detection — if a cached view is older than 60s, show a subtle "stale" indicator and auto-refresh on focus
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

The view factory (`resolveView()`) remains in `app.go` because it needs access to live service clients (`m.ec2`, `m.s3`, etc.) that change on profile/region switches. The registry is the source of truth for *what exists*; `resolveView()` handles *how to create it*.
