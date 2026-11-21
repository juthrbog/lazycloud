# Lambda

Browse Lambda functions with their aliases, versions, triggers and recent invocations, and open their logs. Every call reads, so ReadOnly mode allows all of them. The design is in `docs/design/lambda.md`.

## Views

### Functions

Open the list from the Lambda tile on the home screen, or with `:lambda` (aliases `:functions` and `:fn`). `:lambda NAME` opens the list with the cursor on that function, and opens its detail.

| Column | Description |
|--------|-------------|
| Name | The name of the function |
| Runtime | The runtime, or `image` for a function from a container image |
| Memory | The memory in MB |
| Timeout | The timeout in seconds |
| Size | The size of the code package |
| Arch | The architecture: `x86_64` or `arm64` (wide terminals) |
| Package | `zip` or `image` (wide terminals) |
| Modified | The date of the last change |

A terminal of 80 to 119 columns leaves out Arch and Package. A narrow terminal shows only the name, the runtime and the date. Memory, timeout and size sort as numbers.

The list has no state column. `ListFunctions` does not return the state, and one more call for each function would use the shared quota of the account. The detail shows the state.

The list loads at most 20 pages of 50 functions (1,000 functions). When more functions exist, the footer shows `l for more`, and `l` loads the next 1,000. If a later page fails, the loaded rows stay, and `l` asks for that page again. `:lambda NAME` finds only a loaded function. For another name, the status bar says that the loaded functions have no function with that name.

| Key | Action |
|-----|--------|
| `enter` | Open the log group of the function in the event viewer |
| `d` | Open the detail of the function |
| `$` | Open the detail with the values of the environment variables shown |
| `a` | Open the aliases of the function |
| `v` | Open the versions of the function |
| `t` | Open the triggers of the function |
| `i` | Open the recent invocations of the function |
| `y` | Copy the ARN of the function |
| `l` | Load more functions |
| `/` | Filter the loaded functions |
| `s`, `S` | Sort |
| `r` | Load the list again from the first page |

The log group is the group in the logging settings of the function, or `/aws/lambda/<name>`. Lambda makes the group at the first invocation, so a function that never ran has no group. The event viewer then says "No log group named NAME. Lambda makes it at the first invocation of the function."

### Function Detail (side panel)

`d` calls `GetFunction` and `GetFunctionUrlConfig`, and opens a panel with these tabs:

1. Info: the name, the ARN, the state with its reason, the last update status, the description, the runtime, the handler, the image, the package type, the architecture, the memory, the timeout, the ephemeral storage, the code size and SHA256, the reserved concurrency, the role, the log group with its format and levels, the function URL with its auth type, the tracing mode, SnapStart, the layers, the dead letter target, the KMS key and the time of the last change.
2. Environment: the names of the environment variables. The values show as `••••`, because they often hold secrets. `$` in the list opens the detail with the values shown, and with the Environment tab first, so a narrow terminal shows it.
3. Network: the VPC, the subnets and the security groups of the function. Each line is a link to its list. A function outside a VPC shows "No VPC".
4. Tags: the tags of the function, when it has tags. When the role may not read the tags (`lambda:ListTags`), the tab shows the reason.

If `GetFunctionUrlConfig` fails, for example because AWS limits the request rate, the URL line shows the error, and the panel still opens.

### Aliases

`a` on a function opens its aliases.

| Column | Description |
|--------|-------------|
| Name | The name of the alias |
| Version | The version that the alias points at |
| Weights | The other versions that get a part of the calls, for example `2: 10%` |
| Description | The description of the alias (not on a narrow terminal) |

`enter` opens the log group of the function, because Lambda writes the logs of every alias there. `d` opens the detail, with the routing of the calls, for example `1: 90%, 2: 10%`. `y` copies the name.

### Versions

`v` on a function opens its versions, with `$LATEST`. The columns are Version, Description, Runtime, Size and Modified. A narrow terminal shows only the version, the description and the date. The versions sort as numbers, and `$LATEST` sorts first.

`enter` opens the log group of the function. `d` opens the detail of that version with `GetFunction`. A version has no function URL of its own, so its detail leaves the URL out. The list loads at most 4 pages of 50 versions (200 versions), and `l` loads more. `y` copies the version.

### Triggers

`t` on a function opens its event source mappings: the queues and streams that Lambda reads and passes to the function.

| Column | Description |
|--------|-------------|
| Type | SQS, Kinesis, DynamoDB, Kafka, MQ or DocumentDB, from the ARN of the source |
| Source | The short name of the source, for example the queue name |
| State | Enabled, Disabled, Creating and so on, in color |
| Batch | The batch size (not on a narrow terminal) |
| Modified | The time of the last change (not on a narrow terminal) |

`enter` and `d` open the detail: the UUID, the source ARN, the state with its reason, the last result, the batch size and window, the start position and the filters. For an SQS source, the source line links to the queue in the SQS list.

The list shows only poll-based triggers. API Gateway, S3, SNS and EventBridge call the function directly and are set up on their side. An empty list says so.

### Recent invocations

`i` on a function opens its recent invocations. The list reads the `REPORT` lines of the log group of the function: Lambda writes one line at the end of each invocation, with its duration and its memory.

| Column | Description |
|--------|-------------|
| Time | The time of the end of the invocation |
| Duration | How long the invocation ran |
| Billed | The billed duration (not on a narrow terminal) |
| Memory | The memory used, of the memory size (not on a narrow terminal) |
| Init | The init duration of a cold start. Empty for a warm start. |
| Status | `ok`, `error` or `timeout`, with the error type |
| Stream | The log stream (wide terminals) |

The list reads the last hour first, newest first. One load shows at most 1,000 invocations, and `l` loads more: first the rest of the hour, then the hour before. The walk stops at the oldest time that the group can hold: 14 days before it was made, and now minus its retention.

The footer counts the invocations, the cold starts and the errors, and shows the loaded span, for example `214/214 invocations · 12 cold · 3 errors · 14:02 to 15:02`.

| Key | Action |
|-----|--------|
| `enter` | Open the log lines of the invocation: its stream, from its start to its `REPORT` line |
| `d` | Open the detail with every field and the raw line |
| `l` | Load older invocations |
| `/` | Filter the loaded invocations |
| `s`, `S` | Sort. The durations sort as numbers. |
| `r` | Load again from the last hour |

Some things to know:

- **Log format:** the list reads the format in the logging settings of the function: text `REPORT` lines, or JSON `platform.report` records. After a change of the format, the older lines in the other format do not show.
- **System log level `WARN`:** Lambda writes a report only for an invocation that failed. The footer then says "only failed invocations are logged".
- **Max memory used:** AWS gives the maximum of the execution environment, so the value can be higher than one invocation used.
- **No log group:** a function that never ran has none. The list then says so, in place of an error. After the first invocation, `r` looks the group up again.
- **Shared log group:** a custom log group can hold the lines of more than one function. Lambda then names each stream `YYYY/MM/DD/<function>[<version>]<id>`, and the list keeps only the streams of this function.
- **Retries:** Lambda retries a failed async invocation with the same request ID. Each try writes its own `REPORT` line and is its own row.
- **Lines that do not parse:** a line that the list cannot read shows with its time and its raw text.

## API calls

| Call | Used for |
|------|----------|
| `ListFunctions` | The function list, 50 functions for each page |
| `GetFunction` | The detail of a function: the state, the configuration, the reserved concurrency and the tags |
| `GetFunctionUrlConfig` | The function URL in the detail |
| `ListAliases` | The aliases of a function |
| `ListVersionsByFunction` | The versions of a function, 50 for each page |
| `ListEventSourceMappings` | The triggers of a function, 100 for each page |
| `DescribeLogGroups` (CloudWatch Logs) | The log group of the invocation list: its retention, its creation, or that it does not exist |
| `FilterLogEvents` (CloudWatch Logs) | The `REPORT` lines of the invocation list, 100 for each call |

`GetFunction` has its own quota of 100 calls each second. The other calls share a quota of 15 calls each second with all users and tools of the account.

## IAM permissions

- `lambda:ListFunctions`
- `lambda:GetFunction`
- `lambda:GetFunctionUrlConfig` (optional: without it, the URL line shows the error)
- `lambda:ListAliases`, `lambda:ListVersionsByFunction` and `lambda:ListEventSourceMappings` (for the three lists of a function)
- `lambda:ListTags` (optional: without it, the Tags tab shows the reason)
- `logs:DescribeLogGroups` and `logs:FilterLogEvents` (for the logs and the invocation list)

`ListFunctions`, `ListVersionsByFunction` and `GetFunction` return the values of the environment variables to any role that may call them.
