# Lambda: functions, aliases, versions, triggers and invocations

Status: Proposed. PR 0 is this design and its pin tests. The facts below were checked at `10ab49b` with `service/lambda` v1.118.0 and LocalStack 2026.10.4.

## Summary

Market research puts Lambda second on the roadmap, after CloudWatch Logs (`docs/reviews/2026-10-11/market-research.md:67`). It is the most used serverless service, and it links to logs. Closed issues #122 and #124 describe the feature. This design follows them, with changes where the facts below disagree with them.

The main use of a Lambda view is to answer two questions fast: how is this function set up, and what did it do lately. So this design has three parts:

1. A function list with a detail panel, and child lists for the aliases, the versions and the triggers (event source mappings) of a function.
2. A jump from a function to its log group in the event viewer of CloudWatch Logs, which already exists.
3. A list of recent invocations, read from the `REPORT` lines in the log group. Each row shows the duration, the memory and a cold start. Enter on a row opens the log lines around that one invocation. No competitor that the research read does this.

Every call in this phase reads. Invoke and write actions come in a later phase (decision D8).

## Facts that the design depends on

### The module

- `go get github.com/aws/aws-sdk-go-v2/service/lambda` gives v1.118.0 (2026-11-19). It adds only that module and its two `go.sum` lines. The repo already has every module that it needs.
- AWS renamed functions to "Lambda Event Functions" in the docs. The API names do not change (`lambda@v1.118.0/doc.go:18-21`).
- `LambdaClient()` copies the pattern of `LogsClient()` (`internal/aws/client.go:155-161`).

### Functions

- `ListFunctions` returns up to 50 functions on each page. `MaxItems` above 50 still gives 50 on AWS. Paging uses `Marker` in and `NextMarker` out (`api_op_ListFunctions.go:14-15`, `:46`, `:54-57`, `:90`). AWS does not document the order. LocalStack returns the creation order.
- The list result has no `State`, `StateReason`, `StateReasonCode`, `LastUpdateStatus` and `RuntimeVersionConfig`. The SDK says: "To get the additional fields ... use GetFunction" (`api_op_ListFunctions.go:20-23`). LocalStack also leaves them empty.
- The list result has `FunctionName`, `FunctionArn`, `Runtime`, `Handler`, `MemorySize` (MB), `Timeout` (seconds), `CodeSize` (bytes), `Architectures`, `PackageType`, `LastModified`, `Description`, `LoggingConfig` and `Environment.Variables` (LocalStack, and the Datadog security note in the market research).
- `LastModified` is a string, not a time: `"2019-09-26T20:28:40.438+0000"`. The zone has no colon, so `time.RFC3339` fails. The layout is `2006-01-02T15:04:05.000-0700` (`types/types.go:4237-4241`, the AWS CLI example, LocalStack). The repo has no parser for it.
- `GetFunction` returns `Configuration` (all fields, with `State` and `LastUpdateStatus`), `Code`, `Concurrency{ReservedConcurrentExecutions}`, `Tags` and `TagsError` (`api_op_GetFunction.go:78-105`). `Code.Location` is a presigned URL that is valid for 10 minutes. `Tags` is empty without `lambda:ListTags`, and `TagsError` then says why.
- `State` values: `Pending`, `Active`, `Inactive`, `Failed`, `Deactivating`, `Deactivated`, `ActiveNonInvocable` and `Deleting`. `LastUpdateStatus` values: `Successful`, `Failed` and `InProgress` (`types/enums.go:1123-1130`, `:547-549`). Issue #122 asks for "Updating" in yellow. That is not a state. It is `LastUpdateStatus=InProgress`.
- An image function has an empty `Runtime` and `Handler` (`types/types.go:4277-4280`). A layer has only an ARN, with no name or version field (`:5627-5642`).
- A function outside a VPC returns `VpcConfig{SubnetIds:[], VpcId:"", SecurityGroupIds:[]}`, not nil (AWS CLI example).
- The SDK has no data on deprecated runtimes. The runtime page of AWS lists them with dates (https://docs.aws.amazon.com/lambda/latest/dg/lambda-runtimes.html).

### Logging

- `LoggingConfig` has `LogGroup`, `LogFormat` (`Text` or `JSON`), `ApplicationLogLevel` and `SystemLogLevel`. The default group is `/aws/lambda/<function name>` (`types/types.go:5989-6012`). LocalStack always fills in `LogGroup`. On AWS this is not checked.
- Lambda makes the log group at the first invocation. A function that never ran has no group (LocalStack). The group stays after the function is deleted (AWS docs).
- Each invocation writes `START`, the lines of the function, `END` and `REPORT`. In text format a `REPORT` line has tab-separated fields: `REPORT RequestId: <id>\tDuration: 0.86 ms\tBilled Duration: 1 ms\tMemory Size: 256 MB\tMax Memory Used: 75 MB\tInit Duration: 28.24 ms\t\n`.
  - `Init Duration` shows only on a cold start.
  - `Status: error\tError Type: <type>` or `Status: timeout` can follow. A separate `INIT_REPORT` line can come before.
  - An `XRAY TraceId: ...` line can follow.
  - The spacing differs between AWS examples, so a parser must split on tabs.
- In JSON format, the platform lines are objects: `{"time": ..., "type": "platform.report", "record": {"requestId", "status", "metrics": {"durationMs", "billedDurationMs", "memorySizeMB", "maxMemoryUsedMB", "initDurationMs"?}}}`. `status` is `success`, `failure`, `error` or `timeout` (AWS telemetry schema). A group can hold both formats after a change of the format.
- With `SystemLogLevel=WARN`, Lambda writes a `REPORT` only for an invocation that did not succeed (AWS docs, "System log level event mapping").
- `Max Memory Used` is the maximum of an execution environment, not of one invocation, so it can be too high (AWS docs).
- The stream name is `2026/11/21/[$LATEST]<hex>`, or `[1]` for version 1. Each stream is one execution environment.

### Aliases, versions and triggers

- `ListAliases(FunctionName)` gives `AliasConfiguration{Name, FunctionVersion, Description, RoutingConfig.AdditionalVersionWeights map[string]float64}`. `ListVersionsByFunction` gives `FunctionConfiguration` rows, 50 on each page, without `State` (`api_op_ListAliases.go`, `api_op_ListVersionsByFunction.go:14-15`). LocalStack lists `$LATEST` first, then 1, 2 and so on. AWS does not document the order.
- `ListEventSourceMappings(FunctionName)` gives up to 100 on each page (`api_op_ListEventSourceMappings.go:31-77`). On LocalStack its last page has `NextMarker` `""`, not nil. The list core ends a load only on a nil token (`internal/views/listcore.go:23-24`), and the Logs layer maps `""` to nil with `nonEmpty` (`internal/aws/logs.go:297-302`). Each mapping has `UUID`, `EventSourceArn`, `State`, `StateTransitionReason`, `LastProcessingResult`, `LastModified` (a real `time.Time`), `BatchSize`, `MaximumBatchingWindowInSeconds` and `FilterCriteria`. `State` is a plain string: `Creating`, `Enabling`, `Enabled`, `Disabling`, `Disabled`, `Updating` or `Deleting` (`types/types.go:2949-2951`).
- A mapping lists only poll-based sources: SQS, Kinesis, DynamoDB Streams, Kafka, MQ and DocumentDB. API Gateway, S3, SNS and EventBridge call the function themselves, and their setup is on their own side.
- `GetFunctionUrlConfig` returns `FunctionUrl`, `AuthType` (`NONE` or `AWS_IAM`), `InvokeMode`, `Cors` and two times as strings. A function with no URL gives `ResourceNotFoundException` (LocalStack).

### Quotas and errors

- `GetFunction` has its own quota of 100 calls each second, and `GetPolicy` has 15. All other control-plane calls of an account share 15 calls each second. These include `ListFunctions`, `ListAliases`, `ListVersionsByFunction`, `ListEventSourceMappings`, `GetFunctionUrlConfig` and `ListTags`. None of these quotas can be raised (https://docs.aws.amazon.com/lambda/latest/dg/gettingstarted-limits.html).
- A throttle is `TooManyRequestsException`, which `aws.ErrorText` already explains (`internal/aws/errtext.go:52-53`). The SDK retries it, but it does not read the `Retry-After` header of Lambda.
- A missing function, alias, policy or URL gives `ResourceNotFoundException`.

### LocalStack

- The container mounts the Docker socket (`docker-compose.yml:6-11`), and CI uses the same file. `CreateFunction` takes a role ARN that does not exist, as long as it matches the pattern. A function is `Active` within about 2 seconds. Invoke starts a sibling container and works.
- LocalStack ignores a custom `LoggingConfig.LogGroup`: the logs still go to `/aws/lambda/<name>`.
- `ListVersionsByFunction` paging is broken: with more versions than `MaxItems`, LocalStack returns `NextMarker` as an object, and the SDK fails with "expected string, got {".
- Deleting a disabled event source mapping leaves a ghost: the list still shows it with `State=Deleting`, and every other call says that it does not exist. The LocalStack of this worktree has two such ghosts from the research (`42a0b2e2-...` and `7b87f3a9-...`). `go run ./cmd/seed --wipe` resets the state. CI starts from a fresh volume each time.
- An arm64 function fails at invoke time on an x86_64 host with `Runtime.InvalidEntrypoint`. The CI runners are x86_64.
- `Max Memory Used` always equals `Memory Size` on LocalStack.

### The app

- A new service touches `aws.Services`, `NewServices`, a guard, a mock, `mockAllServices`, the routes, the registry, an icon, the conformance and render safety tables, the ReadOnly key test, the filter test, the smoke list test, the seeder and the docs (the CloudWatch Logs PRs #311 to #317). `TestRoutes` keeps a map from each child view to its root (`internal/app/app_test.go:582-587`).
- The list core opens an async detail on `d` with `FetchDetail`, and enter then belongs to the view (`internal/views/listcore_ui.go:152-158`). The log group list uses this: enter opens the events, `v` the streams and `d` the detail (`internal/views/logs_groups.go:77-88`).
- `PageBudget` with `MoreKey` stops a list after a number of pages, and `l` loads more (`listcore.go:59-94`). `Complete`, a second load phase with one call for each row, cannot be used with them (`listcore.go:166-177`).
- The app owns the keys `q T P R W L : ?` and backspace before a view gets them (`internal/app/app.go:666-724`). `TestListConformance_NoGlobalLetters` also forbids `E` (`internal/views/list_conformance_test.go:575`). Issue #124 asks for `L` to jump to the logs, so it cannot have it.
- `ui.StateColor` matches lowercase values only (`internal/ui/icons.go:66-79`). Lambda sends `Active`, `Failed` and `Pending`, and mappings send `Enabled` and `Disabled`. None of these gets a color today.
- Below 120 columns, a detail opens as a full screen view with only its first tab and without its links (`app.go:570-587`).
- The event viewer takes `log_group`, `log_stream` and the group record as route parameters. It has no parameter for a filter pattern or a time range (`internal/views/logs_events.go:213-231`). When the group does not exist, it shows "No log group named NAME" in place of the lines.
- No code masks a value. AWS text shows in full after `ui.Sanitize`. In the side panel, `y` and `V` copy and `e` writes the tab to a temp file for `$EDITOR` (`internal/ui/contentview.go:267-291`, `:331`).
- The seeders run in parallel (`cmd/seed/main.go:100-106`), so a Lambda seeder cannot use a queue that the SQS seeder makes.

## Hazards

These are true today. PR 0 pins the ones that the app code shows.

- H1. `ListFunctions` has no state. A state column needs one `GetFunction` call for each function.
- H2. `ListFunctions`, `ListVersionsByFunction` and `GetFunction` return the values of the environment variables, which often hold secrets. The side panel shows any text in full, and `y`, `V` and `e` copy it to the clipboard or a temp file.
- H3. `LastModified` of a function does not parse with `time.RFC3339` (AWS CLI example, LocalStack). The times of a function URL have the same form on LocalStack. On AWS this is not checked.
- H4. `ui.StateColor` gives no color to the capitalized states of Lambda and of event source mappings.
- H5. `L` belongs to the app (the event log, `internal/ui/keys.go:86`), and the conformance test forbids `E`.
- H6. The event viewer cannot open with a time range, and its text for a missing group does not say why a function can have no group.
- H7. The log group of a function does not exist until the first invocation.
- H8. LocalStack: a custom log group is ignored, `ListVersionsByFunction` paging fails above `MaxItems`, a disabled mapping leaves a ghost when deleted, and arm64 functions fail at invoke on x86_64.
- H9. `REPORT` lines come in two formats, with optional fields, and `SystemLogLevel=WARN` drops the lines of successful invocations.
- H11. An empty `NextMarker` (`""`) from a page call makes the list core ask for page 1 again without end, unless the AWS layer maps it to nil.
- H10. The control-plane quota of 15 calls each second is shared by all users and tools of the account.

## Design

### Names

- Service `Lambda` on the home screen, with the feature `Functions`.
- View IDs: `lambda_functions`, `lambda_aliases`, `lambda_versions`, `lambda_triggers` and `lambda_invocations`. The root of each is `lambda_functions`.
- Commands: `lambda` with the aliases `functions` and `fn`. `:lambda NAME` opens the list with the cursor on that function (decision D7).
- An icon `IconLambda` (nf-md-lambda, fallback `λ`).

### The AWS layer

- `LambdaService` with these methods, each a read:
  - `ListFunctionsPage(ctx, token)`.
  - `GetFunction(ctx, name)`: the configuration, the reserved concurrency and the tags, without `Code.Location`.
  - `GetFunctionURL(ctx, name)`: nil and no error when the function has none.
  - `ListAliasesPage(ctx, name, token)`, `ListVersionsPage(ctx, name, token)` and `ListTriggersPage(ctx, name, token)`.
- Each page method maps an empty `NextMarker` to nil (H11).
- The domain type `aws.Function` keeps the fields of the list, with `LastModified` as a `time.Time`, and `LogGroup`, `LogFormat` and `SystemLogLevel` from `LoggingConfig`. It does not keep the environment values of the list result (H2). Only `GetFunction` gives them, in `aws.FunctionDetail`.
- A parse helper reads both time forms, `-0700` and RFC 3339 (H3). PR 1 tests it with the times of a function and of a function URL.
- `GuardLambda` passes every call. `TestGuardListsEveryMethod` lists each method as a read.

### The function list

- Columns: Name, Runtime, Memory, Timeout, Code size, Last modified. On a wide terminal, also Architecture and Package type. Memory, timeout and size sort as numbers. An image function shows `image` as its runtime.
- No state column (decision D2). The list does not call `GetFunction` for each row (H1, H10).
- A page budget of 20 pages (1,000 functions), with `l` for more (decision D3).
- The row key is the ARN, so `y` copies the ARN. `FocusKey` is the name, so a focus link and `:lambda NAME` find a function by its name.
- Keys:
  - enter opens the log group of the function in the event viewer (decision D4).
  - `d` opens the detail.
  - `a` opens the aliases, `v` the versions, `t` the triggers and `i` the recent invocations.
  - `y` copies the ARN.
  - `$` opens the detail with the environment values shown, and with the Environment tab first, so that a narrow terminal shows it (decision D5).
- `:lambda NAME` moves the cursor to the function and opens its detail, because a focus in the list core opens the detail (`listcore.go:787-806`). When the function is not in the loaded pages, the status bar says "No function named NAME in the loaded functions", with "· l for more" when more pages are left. `executeCommand` allows the argument for `lambda` and its aliases (`internal/app/app.go:844`).
- The detail calls `GetFunction` and `GetFunctionURL`. A failure of `GetFunctionURL`, for example a throttle or a denied call, shows on the URL line of the Info tab, and the panel still opens. It has these tabs:
  - Info: the name, the ARN, the state with its reason, the last update status, the runtime, the handler, the package type, the architecture, the memory, the timeout, the ephemeral storage, the code size and SHA, the reserved concurrency, the role, the description, the log group with its format and levels, the function URL with its auth type, the tracing mode, SnapStart, the layers (name and version from the ARN), the dead letter target and the KMS key.
  - Environment: one line for each variable, with the value hidden as `••••` (decision D5). An environment error shows here.
  - Network: the VPC, the subnets and the security groups, each a link to its list. "No VPC" when `VpcId` is empty.
  - Tags: from `GetFunction`. When `TagsError` is set, the tab shows it, as the log group detail does for its tags.
- Info comes first, so a narrow terminal shows the most useful tab.
- `ui.StateColor` compares without case, and it learns the Lambda and mapping values (H4):
  - Green: `active`, `enabled` and `successful`.
  - Yellow: `pending`, `inprogress`, `enabling`, `updating` and `disabling`.
  - Red: `failed` and `disabled`.
  - Gray: `inactive`.

### Aliases, versions and triggers

- Aliases: Name, Version, Weights (for example `2: 10%`) and Description. enter opens the logs of the function, and `d` opens the detail.
- Versions: Version, Description, Runtime, Code size and Last modified, in the order of AWS. enter opens the logs of the function, and `d` opens the detail with `GetFunction` of that version.
- Triggers: Source type (`SQS`, `Kinesis`, `DynamoDB`, `Kafka`, `MQ` or `DocumentDB`, from the ARN), Source, State, Batch size and Last modified. enter and `d` open the detail with the filter criteria, the batching window and the last result, from the list data. For an SQS source, the detail links to the queue in the SQS list.
- With no trigger, the view says: "This list shows only poll-based triggers (SQS, Kinesis, DynamoDB, Kafka, MQ, DocumentDB). API Gateway, S3, SNS and EventBridge call the function directly and are set up on their side."
- The aliases list and the triggers list load all of their pages, because a function has few aliases and triggers. The versions list stops after 4 pages (200 versions), and `l` loads more.
- The footer of each child list has the form of the core, for example `3/3 aliases`.

### The jump to the logs

- enter on a function, on a version or on an alias opens the event viewer of the log group: `LoggingConfig.LogGroup`, or `/aws/lambda/<name>` when it is empty.
- The viewer looks the group up as for `:logs NAME`. When the group is missing, the text says: "No log group named NAME. Lambda makes it at the first invocation of the function." (H6, H7). A new route parameter `missing_hint` carries the second sentence.
- From a version or an alias, the viewer opens the whole group. A filter pattern matches messages, not stream names, so it cannot select the streams of one version. A later phase can filter streams by the version in their name.
- The event viewer gets three new optional route parameters: `missing_hint`, and `log_start` and `log_end` (a custom range, in milliseconds). The invocation list uses the range.

### Recent invocations

- `i` on a function opens `lambda_invocations`. Its route parameters are `function`, `log_group`, `log_format` and `system_log_level`, from the row.
- It reads the `REPORT` lines of the log group with `FilterLogEvents`, newest first, in windows of one hour. It uses the existing `LogsService`, so it needs no new AWS call. It is a list of the list core:
  - Each call asks for 100 lines, and `PageBudget` is 10, so one load shows at most 1,000 rows.
  - The fetch reverses each page, so the rows stay newest first.
  - When a window ends, the fetch returns a token of its own for the window before (`win:<start in ms>`), so `l` first loads the rest of the hour and then the hour before.
  - The walk stops at the start of the retention, as in the event viewer.
- The filter pattern is `"REPORT RequestId"` for the text format, and `{ $.type = "platform.report" }` for the JSON format, from `log_format` (H9). A group that changed its format shows only the current format. When the list finds no line, its text says which format it read.
- When the log group does not exist (H7), the list is empty and says: "No log group named NAME. Lambda makes it at the first invocation of the function." It does not show the error screen.
- Columns: Time, Duration, Billed, Memory (used of size), Init (the init duration, empty for a warm start), Status (`ok`, `error`, `timeout`, with the error type), and Stream on a wide terminal.
- The footer has the form of the core, with the cold starts and the errors: `214/214 invocations · 12 cold · 3 errors · 14:02 to 15:02`. With `system_log_level` `WARN`, it adds "only failed invocations are logged".
- enter on a row opens the event viewer on that stream, with a range from the start of the invocation (the `REPORT` time minus the duration and the init duration, minus one second) to the `REPORT` time. A stream is one execution environment, and it runs one invocation at a time, so the range holds that invocation and its init. A filter pattern on the request ID would miss lines that the function prints without it.
- `d` opens a detail with all fields of the line, and the raw line.
- The parser splits on tabs, takes the known fields by name, and keeps a line it cannot read as a row with only the time and the raw text.

### Seed and tests

- `cmd/seed/lambda.go` makes x86_64 Python functions with a fake role ARN: `orders-api` (with environment variables, two versions, an alias `live` with a 10% weight on version 2, and an SQS trigger on its own queue), `thumbnailer` (JSON log format, `SystemLogLevel=WARN`), `report-builder` (a custom log group), and one image function that is never invoked. It is idempotent, as the other seeders are.
- The steps for `orders-api`, because `PublishVersion` returns the last version when nothing changed (LocalStack), and a new function is `Pending` for a short time:
  1. Create the function, and wait for `Active` with the `FunctionActiveV2` waiter.
  2. Read the versions. Publish version 1 only when it is missing.
  3. Change the description, wait for `LastUpdateStatus=Successful`, and publish version 2 only when it is missing.
  4. Create the alias only when it is missing.
- The seeder does not invoke a function. Invoke pulls a runtime image in CI and starts containers.
- The logs seeder owns the log lines, because the seeders run in parallel: the Lambda seeder never creates `/aws/lambda/orders-api`. A new stream kind in `cmd/seed/tiers.toml` writes `START`, `END` and `REPORT` lines in a new stream of `/aws/lambda/orders-api` (text) and of a new group `/aws/lambda/thumbnailer` (JSON), with cold starts, an error and a timeout. The two seeded streams with 2,500 and 120 events stay as they are.
- The seeder never disables a mapping before it deletes it (H8).
- Unit tests in `internal/aws` use `httptest` fakes for each call: the fields of each type, the two time forms, paging, an empty `NextMarker`, a missing URL, `TagsError`, and no environment values in `aws.Function`.
- Unit tests in `internal/views` cover the lists, the keys, the detail tabs, the hidden values, `$` with the Environment tab first, a URL error on the Info line, the source type of a trigger, the window walk of the invocation list, a missing log group, and the `REPORT` parser with each form: text with and without `Init Duration`, `Status: error` and `Status: timeout`, JSON with each status, a line it cannot read, and `\t\n` at the end.
- The four child lists join the conformance and render safety tables (`internal/views/list_conformance_test.go`, `render_safety_test.go`).
- App tests: the routes, the ReadOnly key test and the filter test for each view, `:lambda NAME`, and the event viewer with `log_start`, `log_end` and `missing_hint`.
- LocalStack smoke tests: `TestListsLoad` with a seeded function, the jump to the logs of `orders-api`, the invocation list of `orders-api` with enter on a row, and the triggers of `orders-api`.

### What does not change

- No write action and no Invoke (decision D8).
- No metrics (decision D9).
- The event viewer keeps its behavior when the new parameters are not set.
- The app still starts in ReadOnly.

## Visible changes

- The home screen shows Lambda with Functions. `:lambda` opens the list, and `:lambda NAME` puts the cursor on a function and opens its detail.
- enter on a function opens its logs. `a`, `v`, `t` and `i` open its aliases, versions, triggers and recent invocations. `d` opens the detail, with hidden environment values. `$` shows them.
- The invocation list shows the duration, the memory and the cold starts of each invocation, and enter shows the log lines around it.
- The states of other lists keep their colors. Capitalized states now also get colors.

## Plan

Each PR keeps `go test ./...` green and runs golangci-lint with gosec.

- PR 0. This design, and pin tests: `TestLambdaHazard_*` pins H4 (no color for `Active`), H5 (the app binds `L`, and the conformance test forbids `E`), and H6 (the event viewer ignores a range parameter, and its missing-group text has no hint).
- PR 1. The AWS layer and the seed: the `lambda` module, `LambdaService`, the time parser, the empty marker rule, `GuardLambda`, `LambdaClient`, the mock, the `httptest` fakes, the Lambda seeder, and the new stream kind of the logs seeder.
- PR 2. The function list and its detail, the registry entry, the `lambda` command with `:lambda NAME` and the change in `executeCommand`, the routes, the icon, `ui.StateColor` without case, the smoke case, `services/aws/lambda.md` and the docs. H4 flips. Until PR 4, enter on a function says that a later PR adds the logs.
- PR 3. The aliases, versions and triggers lists. enter on an alias or a version gives the same note as on a function until PR 4.
- PR 4. The jump to the logs from functions, aliases and versions, and the new parameters of the event viewer. H6 flips.
- PR 5. The invocation list and the `REPORT` parser, with the smoke test. The tracker marks item 2 of the roadmap priority done.

PR 1 comes first. PR 2 needs PR 1. PR 3 needs PR 2. PR 4 needs PR 3. PR 5 needs PR 4.

## Decisions

- D1. The scope. Proposed: functions, the detail, aliases, versions, triggers (event source mappings), the jump to the logs and recent invocations, all read-only. Other answer: add layers as a second feature now (neboto has them).
- D2. The state column. Proposed: no state in the list. The detail shows the state, the reason and the last update status. Other answers: (a) one `GetFunction` for each function in a second load phase. This uses the quota of 100 calls each second and loads every environment value (claws shows a state). (b) A state for the visible rows only.
- D3. The list budget. Proposed: 20 pages (1,000 functions) with `l` for more, as for log groups. Other answer: load all pages, as EC2 and SQS do.
- D4. What enter does on a function. Proposed: it opens the log group in the event viewer, because finding the logs is the main pain point of the console (issue #124), and `d` opens the detail. This matches the log group list, where enter opens the events. Other answer: enter opens the detail, and a key opens the logs (claws uses `l`, but `l` loads more pages here).
- D5. Environment values. Proposed: hidden as `••••` in the Environment tab, and `$` opens the detail with the values shown. Nothing hides them from a user who presses `$`. The goal is to keep them off the screen during a screen share. Other answers: show them always (neboto), or show only the count and never the values (claws).
- D6. The triggers. Proposed: event source mappings only, with the note about push sources. Other answer: also the EventBridge rules that target the function, which needs the EventBridge module (neboto does this).
- D7. The commands. Proposed: `lambda` with the aliases `functions` and `fn`, and `:lambda NAME` puts the cursor on that function and opens its detail. Other answer: `:lambda NAME` opens the logs of the function at once.
- D8. Invoke and write actions. Proposed: none in this phase. A later phase adds Invoke behind ReadWrite, with a payload form, the 4 KB log tail and a longer read timeout, because a synchronous call can run 30 minutes and the HTTP client stops after 60 seconds with no bytes. Other answer: add Invoke now, with an empty `{}` payload and a confirm (taws and claws).
- D9. Metrics. Proposed: none in this phase. The invocation list covers the duration, the memory, the cold starts and the errors from the logs. CloudWatch metrics need a new SDK module. Other answer: an invocation count in the list from `GetMetricData`, as claws shows.
- D10. Recent invocations. Proposed: a list view of parsed `REPORT` lines, with enter for the lines of one invocation. Other answer: a detail tab with the last 20 invocations, as issue #124 says. A tab is text, so it cannot open the lines of one invocation.

## Open questions

- Q1. Does the list mark deprecated runtimes? Proposed answer: not in this phase. The SDK has no data for it, and a table in the code gets old. A later phase can use `ListRuntimes` of the new Lambda Core service.
- Q2. Does `:lambda NAME` take a prefix? Proposed answer: no, only a full name. With a name that is not loaded, the status bar says that the loaded functions have no function with that name.
- Q3. Does the invocation list follow new invocations? Proposed answer: not in this phase. `r` loads again. Live tail with the `REPORT` pattern can come later.
- Q4. Does a session switch from a log viewer that Lambda opened go back to Lambda? Proposed answer: no. It goes to Log Groups, the root of the viewer, as an EC2 to VPC link does today.
- Q5. Does the detail show the resource policy (`GetPolicy`)? Proposed answer: not in this phase. It has its own quota of 15 calls each second, and it is long JSON.
