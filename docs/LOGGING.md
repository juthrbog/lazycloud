# Event Logging Guidelines

LazyCloud uses an in-app event log (`internal/eventlog/`) for operational visibility. The log is a thread-safe ring buffer (500 entries) viewable via the Event Log panel.

## Levels

| Level | When to use |
|-------|-------------|
| `Debug` | Fetch started, internal state transitions useful for troubleshooting |
| `Info` | Operation completed successfully, data loaded, mutation finished |
| `Warn` | Degraded state, unexpected but recoverable conditions |
| `Error` | Operation failed, AWS errors, mutation errors |

## Categories

| Category | Constant | When to use |
|----------|----------|-------------|
| `aws` | `CatAWS` | All AWS operations: loads, mutations, refreshes |
| `nav` | `CatNav` | View navigation, back/forward |
| `cfg` | `CatConfig` | Profile, region, theme, mode changes |
| `ui` | `CatUI` | Panel open/close, clipboard, editor launch |
| `app` | `CatApp` | App lifecycle, startup, centralized error handling |

Use `CatAWS` for all AWS service operations regardless of the specific service (EC2, S3, SQS, etc.). Do not create per-service categories.

## What to log

| Event | Level | Message pattern | Example |
|-------|-------|-----------------|---------|
| Fetch started | Debug | `"Fetching %s (page %d)"` | `"Fetching EC2 instances (page 1)"` |
| Data loaded | Info | `"Loaded %d %s"` | `"Loaded 42 S3 buckets"` |
| Mutation initiated | Info | `"<Action> %s: %s"` | `"Starting 3 instances"` |
| Mutation completed | Info | `"<Action> completed: %s"` | `"Instance start completed: i-abc123"` |
| Mutation failed | Error | `"<Action> failed: %v"` | `"Instance start failed for i-abc123: ..."` |
| Refresh completed | Info | `"Refreshed %d %s"` | `"Refreshed 5 EC2 instances"` |
| Refresh failed | Error | `"%s refresh failed: %v"` | `"EC2 refresh failed: ..."` |

## What NOT to log

- **Filter/sort changes** -- too noisy for a 500-entry buffer.
- **UI-only state** -- window resize, spinner ticks, table scroll position.
- **Duplicate errors** -- if `app.go` already logs an `ErrorMsg`, views should not log it again (see below).

## ErrorMsg handling

`app.go` intercepts all `ErrorMsg` messages, logs them centrally at Error level with `CatApp`, then forwards them to the active view. Views handle `ErrorMsg` to reset UI state (hide spinners, set error display) but should **not** add their own `eventlog.Error` call -- the app-level handler already covers it.

View-internal completion messages (e.g., `s3DeleteCompleteMsg`, `ec2InstanceMutatedMsg`) are different -- they never pass through `app.go`, so views **must** log errors from these messages themselves.

## AWS service layer

`internal/aws/` is intentionally pure -- it returns data and errors without side effects. All event logging happens in views, which have the context to produce meaningful messages (resource names, counts, bucket paths).

## Message format conventions

- Use present tense for initiation: `"Starting..."`, `"Fetching..."`, `"Purging..."`
- Use past tense for completion: `"Loaded..."`, `"Deleted..."`, `"Copied..."`
- Include counts where available: `"Loaded 42 EC2 instances (2 pages)"`
- Include resource identifiers: `"Instance start completed: i-abc123"`
- For S3, use `s3://bucket/prefix` paths: `"Fetching objects in s3://my-bucket/data/"`
- Keep messages under ~120 characters for readability in the event log panel
- Use `%v` for error formatting: `"Copy failed: %v"`

## File logging

Enable persistent logging to disk with the `--log` flag or `[log] file` config option:

```bash
lazycloud --log /tmp/lazycloud.log
```

```toml
[log]
file = "/tmp/lazycloud.log"
```

Output is JSON (one line per event), using [Charm Log v2](https://github.com/charmbracelet/log) with `JSONFormatter`:

```json
{"time":"2026-04-09T14:23:01-05:00","level":"info","msg":"Loaded 42 EC2 instances (2 pages)","category":"aws"}
{"time":"2026-04-09T14:23:02-05:00","level":"error","msg":"listing S3 buckets: AccessDenied","category":"app"}
```

All levels are captured (Debug through Error) — level filtering is only applied in the in-app EventLog view, not the file output. The file is truncated on each startup.
