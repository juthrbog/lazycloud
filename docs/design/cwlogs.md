# CloudWatch Logs: log groups, streams, events and live tail

Status: Approved on 2026-11-20 with all proposed answers. PR 0 is the design and its pin tests. The facts below were checked at `62bf329`.

## Summary

Market research puts CloudWatch Logs first on the roadmap (`docs/reviews/2026-10-11/market-research.md:54`, `:66`). Every competitor shows logs. Lambda and ECS link to logs, so logs come before them. A check of the code before this work found no blocker. PR #310 freed the command words `logs` and `log` and made the tests fail when a new service is missing from them.

Log events are not like the resources that lazycloud shows today. A resource list is small enough to load all of its pages. A log stream or a log group can hold millions of events, and new events keep coming. So this design adds two things to the base of the app, each inside the service that needs it first:

1. The shared list core can stop after a page budget, stop when a token repeats, and load more pages on a key. Log groups and log streams need this.
2. A new log viewer component shows a growing list of lines with a cap, follow, wrap and a cursor. The event views and live tail use it. Lambda and ECS can use it later.

This design makes five changes:

1. A list of log groups. Enter opens the newest events of the whole group. A key opens the streams of the group.
2. A list of the streams of a group, with the stream that has the newest event first.
3. An event viewer for a group or for one stream. It loads the newest events of a time range first and loads older events on a key. A filter pattern goes to the server, and a local filter acts on the loaded lines.
4. Live tail with `StartLiveTail`. The user starts it with a key, because AWS bills each minute of a session. The viewer shows that it is live, how long it ran, and when AWS samples the events. A session stops when the viewer leaves the screen.
5. A seed for LocalStack, smoke tests, and the service doc.

The phase has no write actions. Retention, delete and Logs Insights come later (decision D8).

## Facts that the design depends on

These facts were checked at `62bf329`. SDK paths are under `~/go/pkg/mod/github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs@v1.92.2/` unless they say otherwise. The quotas come from the AWS quotas page for CloudWatch Logs. The LocalStack facts come from a probe against LocalStack 2026.10.4 Pro.

### The module

- `cloudwatchlogs` v1.92.2 needs core v1.48.0, smithy-go v1.28.9, eventstream v1.7.27, configsources v1.5.12 and endpoints v2.8.12. `go.mod` already has these versions (`go.mod:12`, `:19`, `:31`, `:33-34`). So `go get` adds only the `cloudwatchlogs` module.
- All timestamps are `*int64` milliseconds since the epoch. `parseEpochMillis` already exists in `internal/aws/sqs.go:628-633`, but it is not exported. It gives local time, as the rest of the app shows (`internal/app/identity.go:252`).
- Each page token of the four read calls expires after 24 hours (`api_op_DescribeLogGroups.go:173`, `api_op_DescribeLogStreams.go:128`, `api_op_GetLogEvents.go:166-171`, `api_op_FilterLogEvents.go:219`).
- One log event can be up to 1 MB (`api_op_PutLogEvents.go:41`).

### Log groups

- `DescribeLogGroups` sorts by name and returns up to 50 groups for each page (`api_op_DescribeLogGroups.go:14-16`, `:62-64`). It takes a prefix or a substring pattern, not both. With a pattern, the answer holds only the ARN, the creation time and the name (`:98-115`). With a prefix, it holds the full record. The quota is 10 calls each second.
- `types.LogGroup` has two ARNs. `Arn` ends with `:*` and is for IAM policies. `LogGroupArn` has no `:*`, and the tag calls and `StartLiveTail` need it (`types/types.go`, the `LogGroup` struct). It also has `LogGroupClass`, `RetentionInDays` (nil means "never expire"), `StoredBytes`, `CreationTime`, `MetricFilterCount`, `KmsKeyId`, `DataProtectionStatus` and `DeletionProtectionEnabled`.
- `ListTagsForResource` needs `LogGroupArn`. With `Arn` (the `:*` form), LocalStack returns no tags and no error. Many read-only roles allow `logs:Describe*` but not `logs:ListTagsForResource`.

### Log streams

- `DescribeLogStreams` returns up to 50 streams for each page. `OrderBy: LastEventTime` with `Descending: true` gives the stream with the newest event first. A name prefix cannot be used with that order (`api_op_DescribeLogStreams.go:47-88`). The quota is 25 calls each second.
- `LastEventTimestamp` is eventually consistent. AWS says that it "typically updates in less than an hour" (`types/types.go`, the `LogStream` struct). `StoredBytes` of a stream is deprecated and always 0.

### Events of one stream

- `GetLogEvents` needs a stream name. A page holds up to 1 MB or 10,000 events, and a page can be empty without being the end (`api_op_GetLogEvents.go:17-21`). Without `Limit`, a page holds as many events as fit in 1 MB, up to 10,000 (`:78-81`).
- The tokens are never nil. At the end of the stream, `NextBackwardToken` and `NextForwardToken` return the same token that the call got (`:23-27`, `:165-173`). The LocalStack probe confirmed this: the end page is empty, and its token repeats.
- `StartFromHead` false, the default, starts at the newest events. A page is always in ascending order. To go back in time, the reader sends `NextBackwardToken`.
- `EndTime` is exclusive: "Events with a timestamp equal to or later than this time are not included" (`:73-76`).
- The generated paginator follows only the forward token, and it never stops unless `StopOnDuplicateToken` is set (`:274-327`). So the app calls `GetLogEvents` itself and stops when the token repeats.
- The quota is 25 calls each second in most regions and 10 in some, for example `eu-west-1`. It cannot be raised.

### Events of a group

- `FilterLogEvents` reads all streams of a group, or the streams that a list or a prefix names. It takes a filter pattern and a time range (`api_op_FilterLogEvents.go:80-156`). Without `Limit`, a page holds up to 10,000 events (`:106-107`).
- `StartTime` and `EndTime` are both inclusive: "Events with a timestamp later than this time are not returned" (`:80-83`, `:152-156`).
- A page can be empty and still carry a token. The end is a nil token (`:36-40`).
- `StartFromHead` false gives the newest events first, but only with a start time on or after 2024-01-01 UTC. An earlier start time returns `InvalidParameterException` (`:138-150`). After the first page, the token sets the direction.
- Each event has its stream name and an event ID (`types.FilteredLogEvent`).
- The quota is 25 calls each second in `us-east-1`, 10 in most regions and 5 in some, for example `eu-central-1`. It cannot be raised, and all users of the account share it.

### Live tail

- `StartLiveTail` returns an event stream. The stream sends one `SessionStart`, then one `SessionUpdate` each second, also when no event matched. An update holds up to 500 events, and `SessionMetadata.Sampled` is true when more events matched (`api_op_StartLiveTail.go:17-49`, `types/types.go` `LiveTailSessionMetadata`).
- A session ends after 3 hours with `SessionTimeoutException`. A slow reader gets up to 10 updates buffered, and then the server drops the oldest.
- The input names up to 10 log groups by their ARN, without `*`. A stream list or a stream prefix works only with exactly one group (`:87-131`).
- In Go, `out.GetStream()` gives `Events()`, a channel, plus `Close()` and `Err()`. `Close` must be called, or resources leak, and it is safe to call more than once. Stream errors come from `Err()` after the channel closes, not as channel items (`:290-350`).
- The stream reads the body of the HTTP response of the call. The SDK does not watch a context for the stream (`waitStreamClose` at `:333-358`), but `net/http` ends the body read when the context of the request ends. So the context of the call must live as long as the session, and it must have no timeout. The 60 second read timeout of the HTTP client (`internal/aws/client.go:30`) does not fire, because an update comes each second.
- The request goes to the host `stream-logs.<region>.amazonaws.com`. The SDK adds `stream-` to the host also when `BaseEndpoint` is set. It skips this only when `smithyhttp.IsEndpointHostPrefixDisabled(ctx)` is true (`api_op_StartLiveTail.go:244-253`). With the LocalStack endpoint the host becomes `stream-localhost`, which does not resolve.
- `LimitExceededException` comes when the account already runs 15 sessions, which it shares with console users (`types/errors.go:416`).
- Quotas: 5 `StartLiveTail` calls each second, 15 sessions at the same time for each account, and 10 groups for each session. AWS bills each minute of a session, and live tail works only on the Standard log class.

### LocalStack

- LocalStack 2026.10.4 does not implement `StartLiveTail`. With the host prefix turned off, the call returns "The start_live_tail action has not been implemented" (501).
- `DescribeLogGroups` ignores `Limit`, so a smoke test cannot page log groups.
- `GetLogEvents` with the group ARN in `LogGroupIdentifier` fails with `ResourceNotFoundException`. It works with `LogGroupName`.
- `EndTime` of `GetLogEvents` is inclusive on LocalStack, and exclusive on AWS.
- `FilterLogEvents` returns the events one stream after the other, not merged by time. It does not support `?term` patterns, and it accepts an invalid pattern with no error. It never returned an empty page with a token.
- `CreateLogGroup` with the Infrequent Access class still shows `STANDARD`.

### The app

- The list core loads every page until the next token is nil (`internal/views/listcore.go:23-26`, `:270-278`). It has no page budget, and it does not check that the token changed.
- A failed page sets `c.err` (`listcore.go:458-467`), and `render` then shows only the error, also when rows are loaded (`listcore_ui.go:84-87`). `TestListCore_FailureOnLaterPage` pins this for every list (`internal/views/listcore_test.go:473-488`).
- The core binds both `enter` and `d` to `OpenDetail`, and a focus link opens it too (`listcore_ui.go:29-30`, `:141-146`, `listcore.go:695-699`). S3 makes enter browse by leaving `OpenDetail` nil and handling both keys itself (`internal/views/s3_list.go:63-71`).
- A list with `Complete` puts its pages in a buffer and starts its second phase only on a nil next token (`listcore.go:255-275`). SQS queues use it (`internal/views/sqs_queues.go:111`).
- `ui.ContentView` replaces all of its text in `SetContent`, runs chroma over the whole text, and jumps to the top (`internal/ui/contentview.go:193-210`, `:693-716`). It has no append, no cap and no follow.
- The app event log view has follow, wrap and a filter, but it reads the global event log, and it rebuilds all lines on each change (`internal/views/eventlog.go:236-299`).
- A theme switch restyles each view on the stack that implements `ui.Restyler` (`internal/app/app.go:1386-1391`). Without colors, a cursor or a selection must use reverse video (`internal/ui/styles.go:210-222`).
- Each view gets a context from the navigator. A pop or a session switch cancels it (`internal/nav/navigator.go:54-63`, `:210-229`, `app.go:1281-1291`). After that, the app drops every result of that view, so its `Update` never runs again (`app.go:310-318`).
- A view that another view covers stays on the stack, and its results still reach it (`app.go:310-338`). A nav command pushes a new view and pops nothing (`app.go:806`, `:897-898`).
- The ops line and ctrl+x are for operations that the user starts and that outlive a view (`internal/ops/ops.go:1-9`). Live tail belongs to its view.
- The app runs its keys before the view, unless the view reports `CapturingInput` (`app.go:651-725`). These keys are `q T P R W L : ?`, backspace, ctrl+x, and tab while the panel is open. `s` opens the service picker only on the home screen (`app.go:709-713`). `e < > =` act only while the panel has focus (`app.go:728-746`). A tabbed panel opens with the focus unless a text input is open (`app.go:937-938`), and while it has the focus it gets every other key (`app.go:728-750`).
- The list core uses `enter d y space s S / r` (`listcore_ui.go:28-36`). The SQS message view uses `l` for "load more" (`internal/views/sqs_messages.go:123`). `ContentView` uses `h l` to scroll sideways (`internal/ui/keys.go:130-133`).
- `executeCommand` takes an argument only for `:login`, and its navigation branch sends no parameters (`app.go:840-855`).
- `TestServiceWordsAreFree` keeps `logs`, `log` and `events` free (`internal/registry/registry_test.go:41-47`).

## Hazards

These are true today, and PR 0 pins them.

- H1. A page whose next token equals the token of its request makes the list core load the same page again, without end. `GetLogEvents` does this at the end of each stream.
- H2. A list has no page budget. A group with 100,000 streams makes the stream list call `DescribeLogStreams` 2,000 times, at a quota of 25 each second.
- H3. A failure on a later page hides the rows that already loaded, and `r` starts again from page 1.
- H4. No component can append lines, cap them and follow the end. `ContentView` and the event log view rebuild everything on each change.
- H5. With an endpoint override, `StartLiveTail` goes to a host that does not resolve.
- H6. A stream that a view starts must stop when the view goes away or is covered, also on a profile or region switch. The view cannot do this itself, because a gone view gets no more messages. Live tail costs money each minute, and the account has only 15 sessions.
- H7. A log message can be up to 1 MB and can hold terminal control characters and newlines.

## Design

### Names

- Service `CloudWatch` on the home screen, with the feature `Log Groups`. More features (alarms, metrics) can come later.
- View IDs: `logs_groups`, `logs_streams` and `logs_events`. The route of each has the root `logs_groups`.
- Commands: `logs` (alias `log`) opens the log groups. `logs` also takes a group name: `:logs /aws/lambda/orders` opens the events of that group. `executeCommand` allows the argument for `logs` and sends `NavigateMsg{ViewID: "logs_events", Params: {"log_group": NAME}}`. The word `events` stays free, for example for EventBridge (decision D7).
- `TestServiceWordsAreFree` keeps only `events`.

### Time

- The viewer shows local time, as the rest of the app does. The header names the zone, for example "last 1h · UTC-05:00" (decision D10).
- A custom time is read in local time, for example `2026-11-20 14:00`. A time with `Z` or an offset in RFC 3339 form is read as written.
- A relative range, such as "last 1h", sends only `StartTime`. It sends no `EndTime`, so a slow local clock does not hide the newest events. Only a custom range sends `EndTime`.

### The list core

`listConfig[T]` gets three optional settings:

- `PageBudget int`: the core stops after this many pages. Zero keeps today's behavior.
- `MoreKey bool`: when the core stopped with a next token, `l` loads the next `PageBudget` pages. The footer adds the hint after its counts: "1000/1000 log groups · l for more". `l` does nothing while a load runs.
- `FetchDetail func(ctx context.Context, item T) (msg.TabbedContentMsg, error)`: an async detail. It returns the whole panel, because the panel needs a title too. When it is set, `d` and a focus link open the detail, and enter goes to the view. The core keeps the block of row keys on the error screen.

These rules change for every list:

- A page whose next token equals its request token ends the load, as a nil token does today. This fixes H1 for any service.

These rules change only for a list with `MoreKey`:

- A failure on a later page keeps the rows. The core shows the error in the status bar, and the footer still offers `l`, which tries the same token again. A failure of the first page shows the error screen, as today. This fixes H3 for these lists. Other lists keep the error screen, and `TestListCore_FailureOnLaterPage` stays as it is.
- The core keeps the time of the pending token. When the token is more than 23 hours old, `l` does not send it. The status bar says "The page token expired. Press r to load again."

A load more keeps the load generation, so its pages append, and `r` still starts a new generation. `newListCore` panics when `PageBudget` or `MoreKey` is set together with `Complete`, because the second phase needs all pages.

The core does not get a second type parameter for the detail, so the type of each list stays the same. EC2 and SQS keep their own detail code in this phase (decision D9).

### The log group list

- A `listCore[aws.LogGroup]` with `PageBudget: 20` (1,000 groups) and `MoreKey`.
- Columns: name, class, retention, stored size, created. A narrow terminal shows the name and the retention. `RetentionInDays` nil shows as "never".
- `/` filters the loaded rows. A server-side prefix search can come later.
- Enter opens the events of the whole group (`logs_events` with `log_group`). This matches `aws logs tail GROUP` (decision D1). The view keeps the group record, so the viewer gets the ARN, the class, the creation time and the retention with no extra call.
- `v` opens the streams of the group (`logs_streams`).
- `d` opens the detail through `FetchDetail`. The Info tab comes from the row: name, ARN, class, retention, size, created, metric filters, KMS key, data protection, deletion protection. The Tags tab comes from `ListTagsForResource` with `LogGroupArn`. When that call fails, for example with `AccessDenied`, the Tags tab shows the error, and the panel still opens.
- `y` copies the group name.

### The log stream list

- A `listCore[aws.LogStream]` with `OrderBy: LastEventTime`, `Descending: true`, `PageBudget: 4` (200 streams) and `MoreKey`.
- Columns: name, last event, first event, created. The "last event" time can lag by up to an hour, so the help text says so.
- `/` filters the loaded rows only. AWS does not take a prefix with this order. While more pages exist, the footer says "filter acts on the loaded streams".
- Enter opens the events of the stream (`logs_events` with `log_group` and `log_stream`). `y` copies the stream name.

### The log viewer component

A new `ui.LogView` in `internal/ui/logview.go`. It knows nothing about AWS.

- It holds raw lines in a ring with two caps: 10,000 lines and 32 MB of text (decision D4). When the ring drops lines from the top, the footer counts them: "2000 older lines dropped".
- It styles only the rows on screen, each time it draws. So a resize and a theme switch need no rebuild, and `Restyle` only clears the style cache. `LogView` and the event viewer implement `ui.Restyler`.
- `Append(lines)` adds lines at the end. `Prepend(lines)` adds older lines at the top and keeps the cursor on the same line. Neither touches the other lines. Each line is sanitized once, when it comes in.
- Follow: when follow is on and the cursor is on the last line, new lines move the view to the end. A move up turns follow off. `G` goes to the end and turns follow on again.
- Keys: `j k`, `ctrl+d ctrl+u`, `g G`, `w` wrap, `/` a local filter that hides lines that do not match, `y` copy the line, `V` visual select with `y` to copy many lines, and `enter` to open the line.
- Without colors, the cursor and the visual selection use reverse video, and the words for errors and warnings use bold.
- Each line has a time column, a source column (the stream name, for a group) and the message. The time shows as `15:04:05.000`, or with the date when the range is longer than one day. A narrow terminal hides the source column.
- The row shows the first line of a message, cut to the width. A message with newlines shows a `↵` mark. Enter shows all of it. A message longer than 64 KB shows cut in the detail too, with a note and a key to copy all of it.
- Words such as `ERROR`, `FATAL`, `WARN` and `Exception` get the error and warning colors of the theme.

`LogView` has no sideways scroll. With wrap off, the row shows the start of the message, and `w` or enter shows the rest. `l` stays free for the viewer, where it loads older events, as it loads more in the SQS message view.

`ContentView` and the event log view do not change in this phase. The event log view can move to `LogView` later.

### The event viewer

`logs_events` shows the events of a group, or of one stream when the route has `log_stream`.

When the route has only a group name, from `:logs NAME`, the viewer first calls `DescribeLogGroups` with that name as the prefix and keeps only the exact match. This gives the ARN, the class, the creation time and the retention. When no group matches, the viewer says "No log group named NAME" and loads nothing.

Each load sets `Limit` to the space left in its budget, at most 1,000 events. Each load stops after 1,000 events or 10 pages. Each call has the time limit of a list page and uses the view context.

For a group:

- It calls `FilterLogEvents` with the range, newest first (`StartFromHead` false). The default range is the last hour (decision D3). The header shows the range, the zone and the filter pattern, for example "last 1h · UTC-05:00 · filter: ERROR".
- `l` loads older events with the saved token. When the token is nil, `l` moves the range back by the same length. The new range ends 1 ms before the old start, because both ends of the range are inclusive. The viewer also drops an event ID that it already has.
- The walk back stops at the oldest time that can hold events: the latest of the creation time of the group, now minus its retention, and 2024-01-01 UTC. AWS rejects a newest-first query with an earlier start. The footer then says "start of group". A custom range that starts before 2024-01-01 is moved to that date, and the status bar says so.
- The footer shows the count and the range, for example "1000 events · 14:02 to 15:02 · l for older". When a range has no events, the footer says "no events from 14:02 to 15:02 · l for older".
- The viewer sorts each page by time, and then by event ID, before it adds the page at the top. So a page in another order, for example from LocalStack, still shows in order.

For a stream:

- It calls `GetLogEvents` with `StartFromHead` false. `l` sends `NextBackwardToken`. A token that equals the request token is the start of the stream, and the footer then says "start of stream".
- With a filter pattern, the viewer calls `FilterLogEvents` with `LogStreamNames`, because `GetLogEvents` has no filter.

For both:

- `f` opens a one-line input for a filter pattern and loads again with it. An empty pattern clears it. A bad pattern shows the error of AWS in the status bar and keeps the old lines.
- `t` opens a picker for the range: 5m, 15m, 1h, 3h, 12h, 1d, 3d, 7d, and "custom", which asks for a start and an end time.
- While the `/`, `f` or custom time input is open, the view reports `CapturingInput`, so the global keys do not act on the typed text.
- `/` filters the loaded lines, with no AWS call. `f` filters on the server. The help groups the two keys and says which is which.
- Enter on a line opens the whole event in the side panel: the time, the ingestion time, the stream, the event ID and the message. A message that is valid JSON shows formatted, with the JSON colors of `ContentView`. The panel opens without the focus, so `F`, `l` and `f` still reach the viewer. Tab moves the focus to the panel.
- `r` loads the range again from its end.
- When a saved token is more than 23 hours old, `l` starts a new query in place of the old token. For a group, the query ends 1 ms before the oldest loaded event. For a stream, it calls `FilterLogEvents` with `LogStreamNames`, because `GetLogEvents` has an exclusive end and no event ID. That query ends at the time of the oldest loaded event, and the viewer drops an event at that time whose message it already has.
- A throttling error shows the hint that `aws.ErrorText` already gives ("AWS limits the request rate"), and the lines stay.

### Live tail

- `F` starts live tail for the group or the stream of the viewer, with the same filter pattern. `F` again stops it. Follow turns on at the start (decision D2).
- The app calls `StartLiveTail` with the `LogGroupArn` of the group, plus `LogStreamNames` for one stream.
- Each session gets its own child context of the view context, with no timeout. The 3 hour limit comes from the server.
- The command that starts the session calls `context.AfterFunc(sessionCtx, stream.Close)` as soon as the call returns. When the context has already ended by then, it calls `Close` at once. So a pop, a profile or region switch, or `F` closes the stream, also when the view gets no more messages. This fixes H6.
- A read command waits on `Events()` and on the session context. It returns one update as a message, and the view returns the same command again. When the session context ends, the read ends.
- `F` to stop cancels the session context. The view keeps the session number, and an update or a start reply of an older session is dropped. Its stream is already closed by its context.
- When another view covers the viewer, live tail stops (decision D11). The navigator tells the covered view through a small optional interface, `nav.Coverable`, and the viewer cancels the session. When the user comes back, the status bar says "Live tail stopped. F starts it again." The side panel, the help and the pickers do not cover the viewer, so they do not stop it.
- The header shows `LIVE 12:34`, the time since the start. When an update has `Sampled` true, it shows `sampled` too, and the footer says that AWS shows a sample above 500 events each second.
- At the 3 hour timeout, or on a stream error, the viewer stops live tail and keeps the lines. The status bar says why, and `F` starts a new session. `LimitExceededException` gets its own text: "The account already runs 15 live tail sessions."
- Before the first start in each run of the app, the status bar says "Live tail is billed for each minute. F stops it." It does not ask for a confirm (decision D2).
- The lines of live tail append at the end, so the history above them stays. When the user moves up, follow turns off, and new lines still append below.
- On a group of another class than Standard, `F` does not start a session. The status bar says that live tail needs the Standard class.

With an endpoint override (decision D5):

- The viewer calls `StartLiveTail` with the host prefix turned off for that call (`smithyhttp.DisableEndpointHostPrefix`).
- When that call fails with "not implemented" (501, as LocalStack does) or cannot connect, the viewer polls `FilterLogEvents` every 2 seconds. The header shows `POLL` in place of `LIVE`, and the status bar says once that the poll uses the `FilterLogEvents` quota of the account.
- Each poll asks from 30 seconds before the newest event time that it has, so an event that arrives late still shows. The viewer drops an event ID that it already has. It keeps the IDs of the last 2 minutes for this.
- A poll reads at most 2 pages.
- The smoke tests use this path, because LocalStack has no live tail.

### Seed and tests

- `cmd/seed/logs.go` makes log groups such as `/aws/lambda/orders-api` and `/ecs/web`, each with streams. One stream gets 2,500 events, so the viewer must page. Some events are JSON, and some have `ERROR`. One group is empty. The seeder is idempotent, as the others are.
- `TestListsLoad` gets a `logs` case. A new smoke test opens a stream, loads older events with `l` until the footer says "start of stream", sets a filter pattern on a group, and starts `F` in its polling form.
- Unit tests use fakes for the AWS calls. They cover what LocalStack cannot: an empty page with a token, a repeated token, a sampled update, a session timeout, a stream error, and a token that expired.

### What does not change

- No write action. The `Logs` service guard passes every call, because every call reads.
- `ContentView` and the app event log view.
- EC2 and SQS keep their own detail code.
- Lists without `MoreKey` keep the error screen on a later failure.
- The app still starts in ReadOnly. Live tail is a read, so ReadOnly allows it.

## Visible changes

- The home screen shows CloudWatch with Log Groups. `:logs` opens the groups, and `:logs NAME` opens the events of a group.
- Long lists of groups and streams stop after a budget, and `l` loads more. When a later page fails, the rows stay.
- The event viewer shows the newest hour of a group or a stream in local time, pages back with `l`, filters with `f` (server) and `/` (local), and changes the range with `t`.
- `F` starts live tail. The header shows `LIVE` and the time, or `POLL` with an endpoint override where live tail is not available. A session stops when the viewer is covered or closed.

## Plan

Each PR keeps `go test ./...` green and runs golangci-lint with gosec.

- PR 0. This design, and pin tests in `internal/views/cwlogs_pins_test.go`: `TestLogsHazard_*` pins H1 (a repeated token loads the same page again), H2 (no budget) and H3 (a later failure hides the rows). `TestLogsKeep_*` pins what stays: a nil token ends a load, `r` starts from page 1, a first-page failure shows the error screen, and enter and `d` open `OpenDetail`.
- PR 1. The list core: the repeated-token stop, `PageBudget`, `MoreKey` with `l` and its footer hint, rows kept on a later failure for `MoreKey` lists, the token age, the panic with `Complete`, and `FetchDetail` on `d`. Conformance tests for each. H1 flips for all lists. The list view guide in `docs/ARCHITECTURE.md` changes.
- PR 2. The AWS layer: the `cloudwatchlogs` module and the pin of H5 (the host prefix with an endpoint, against the SDK), `LogsService` (groups, streams, events of a stream, events of a group, one group by name, tags), the repeated-token rule in the events of a stream, `GuardLogs`, `LogsClient`, the mock, and `httptest` fakes. The seeder for logs.
- PR 3. `ui.LogView` with its tests: the ring with both caps, `Append` and `Prepend`, follow, wrap, the local filter, visual select, styles drawn on screen only, `Restyle`, and the look without colors. H4 flips.
- PR 4. The group and stream lists, the registry entry and the `logs` command, the routes, the icon, the smoke case, `services/aws/logs.md`, and the docs. `TestServiceWordsAreFree` keeps only `events`. H2 and H3 flip for these two lists.
- PR 5. The event viewer: the group and stream modes, `l` with the walk-back limits, `f`, `t` with the time rules, `/`, the event detail, and `:logs NAME` with the change in `executeCommand`. A smoke test pages a stream and sets a filter.
- PR 6. Live tail: the session context and `AfterFunc` close, the read loop, `nav.Coverable`, `LIVE` and `sampled`, the timeout and errors, the Standard class check, and the polling form with an endpoint override. H5 and H6 are covered. The smoke test starts `F` in its polling form. The tracker marks market research item 3 done.

PR 1, PR 2 and PR 3 do not depend on each other. PR 4 needs PR 1 and PR 2. PR 5 needs PR 3 and PR 4. PR 6 needs PR 5.

## Test plan

Unit tests in `internal/views` for the list core:

- A fake fetch that returns its request token as the next token ends the load after one page.
- `PageBudget` 2 with 5 pages: 2 pages load, the footer offers `l`, `l` loads 2 more with the same generation, and the last `l` ends the load. A second `l` while pages load does nothing.
- With `MoreKey`, a failure on page 3 keeps the rows of pages 1 and 2, sends the error, and `l` asks for page 3 again. A failure on page 1 shows the error screen. Without `MoreKey`, a failure on page 3 shows the error screen, as today.
- A pending token older than 23 hours: `l` sends nothing and shows the expiry text.
- `r` after a load more starts from page 1 and drops late pages of the old load.
- `newListCore` panics with `Complete` and `PageBudget`.
- `FetchDetail`: `d` opens the panel with the tabs, enter goes to the view, a focus link opens the detail, an error shows the toast, a result after a pop is dropped, and the error screen blocks `d`.

Unit tests in `internal/aws`:

- `httptest` fakes, in the style of `internal/aws/ec2_pages_test.go:31`, for each call: the fields that map to `LogGroup`, `LogStream` and an event, retention nil, the ARN without `*` for tags, `Limit` sent on each events call, an empty page with a token, the repeated token of `GetLogEvents`, a throttling error, and the exact match of one group by name.
- Live tail with a fake event stream behind a small interface: a start, updates, a sampled update, a timeout error, a stream error, `LimitExceededException`, and a cancel of the session context that ends the read and calls `Close` once. A start reply that comes after the context ended calls `Close` at once.
- The host prefix is turned off for the call when an endpoint is set.

Unit tests in `internal/ui`:

- `LogView` with 10 and with 20,000 lines: the line cap, the byte cap with 100 lines of 1 MB, the count of dropped lines, `Prepend` keeps the cursor line, follow on and off, `G`, wrap, the local filter, control characters removed, a 1 MB line cut in its row, and the `↵` mark.
- The width of every line at 40, 80 and 160 columns, also with long stream names, and a resize while lines append.
- A theme switch and `NO_COLOR` with the cursor and a visual selection.

Unit tests in `internal/views` for the viewer:

- The walk back: the range moves, the edge event shows once, and the walk stops at the creation time, at the retention and at 2024-01-01.
- A relative range sends no `EndTime`, and a custom range sends both times. A custom time with and without a zone.
- The poll: the 30 second overlap, the dedupe by event ID, and at most 2 pages.

App tests in `internal/app`:

- The routes, the ReadOnly key test and the filter test cases for the three views (the checks from #310 ask for them). The filter test also presses `f`.
- `:logs NAME` opens the viewer for that group, and an unknown name shows the text.
- A profile switch while live tail runs closes the fake stream, and no update reaches the new session.
- A nav command that covers the viewer stops live tail, and the status bar says so when the user comes back.
- Enter on an event opens the panel without the focus, and `F` still reaches the viewer.

LocalStack smoke tests:

- `TestListsLoad` with a seeded group.
- Open the stream with 2,500 events, page back with `l` until the footer says "start of stream", set a filter pattern on a group, and start `F`, which polls with the endpoint override, then put an event and wait for it.

Manual checks with a real account:

- Live tail on a busy group: the `LIVE` timer, a sampled update, a stop with `F`, a stop by leaving the view, a stop by `:ec2` on top, and a stop on a profile switch. After each stop, a debug line in the event log shows that the stream closed.
- A group in `eu-central-1`, where `FilterLogEvents` allows 5 calls each second.

Mutation testing:

- Mutants of the stop rules in the list core and in the events of a stream, of the walk-back limits, of the dedupe in polling, and of both caps in `LogView`. Each mutant that survives gets a test or a note in the PR.

## Decisions

- D1. What Enter does on a log group. Proposed: it opens the events of the whole group, newest first, and `v` opens the streams. This matches `aws logs tail` and most uses. Other answer: Enter opens the streams, as the tree in `docs/ARCHITECTURE.md:322-324` says, and a key opens the group events.
- D2. How live tail starts. Proposed: the user presses `F`, the first start in each run of the app shows a note about the cost in the status bar, and there is no confirm. Other answers: start live tail at once when the viewer opens, or ask for a confirm each time.
- D3. The default range. Proposed: the last hour, shown in the header, with a picker on `t` from 5 minutes to 7 days and a custom range. Other answer: 10 minutes, as `aws logs tail` does. A short range made users think that logs were missing in other tools.
- D4. The caps of the viewer. Proposed: 10,000 lines and 32 MB, and the footer counts the dropped lines. Other answer: a setting in the config file.
- D5. Live tail with an endpoint override. Proposed: try `StartLiveTail` with the host prefix turned off, and poll `FilterLogEvents` every 2 seconds only when that call is not implemented or cannot connect. The header shows `POLL`. Other answer: turn off live tail when an endpoint is set, and test the follow path only with fakes.
- D6. The list budgets. Proposed: 20 pages (1,000) for groups and 4 pages (200) for streams, with `l` for more. Other answer: load all groups, as the other lists do, and budget only the streams.
- D7. The commands. Proposed: `logs` with the alias `log`, which also takes a group name. `events` stays free. Other answer: `cwlogs` as the name, so `logs` stays free for another service.
- D8. Write actions and Logs Insights. Proposed: none in this phase. Retention, delete group and Logs Insights come in a later phase, each with its own design. Other answer: add retention and delete now, behind ReadWrite.
- D9. The async detail. Proposed: add `FetchDetail` to the list core and use it only for log groups in this phase. Other answer: also move EC2 and SQS to it now, which removes their copies but makes PR 1 larger.
- D10. The time zone. Proposed: local time, with the zone in the header, and custom input in local time or RFC 3339. Other answer: UTC, as AWS shows in many places, or a key that switches between local time and UTC.
- D11. Live tail under another view. Proposed: stop the session when another view covers the viewer, and say so when the user comes back. Other answer: keep it running under the other view, which keeps the follow going but bills each minute while the user does not see it.

## Open questions

- Q1. Does the event viewer keep its lines when the user changes the filter pattern or the range? Proposed answer: no. A new pattern or range is a new query, so the viewer clears the lines and loads again. The local filter `/` keeps the lines.
- Q2. Does the viewer show the ingestion time? Proposed answer: only in the event detail. The row shows the event time.
- Q3. Can the viewer show more than one group at once? Proposed answer: not in this phase. Live tail and `FilterLogEvents` can take more groups, and a later phase can add a multi-select in the group list.
- Q4. Does `:logs NAME` take a prefix or a pattern? Proposed answer: only a full group name. The viewer checks the name with one `DescribeLogGroups` call and says when no group has it.
- Q5. Does live tail stop when the user opens the side panel, the help or a picker? Proposed answer: no. These do not cover the viewer. It stops on `F`, when another view covers the viewer, when the viewer closes, at the timeout, and on a session switch.
- Q6. Does the viewer save lines to a file? Proposed answer: not in this phase. `V` and `y` copy lines. An export can come later.
- Q7. Does the viewer stop live tail after a time with no key press, for example 30 minutes? Proposed answer: not in this phase. The header shows the time of the session, and covering the viewer stops it. An idle stop can come if users ask.
