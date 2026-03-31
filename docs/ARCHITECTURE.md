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
