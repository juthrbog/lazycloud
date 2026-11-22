# Jump to a resource

Status: Approved 2026-11-22 with all proposed answers (#332). PR 0 is this design and its pin tests. PR 1 adds the parser. PR 2 makes the focus changes. PR 3 adds `:go` and the lookups. The facts below were checked at `685827c` with the SDK versions in `go.mod` and LocalStack 2026.10.4.

## Summary

The market research lists this idea as item 3 of "Ideas that are not on the roadmap": "The user pastes an ID, an ARN, or a name and goes to the resource. This is the main pitch of neboto. Add tag filters with Resource Explorer or the Resource Groups Tagging API." (`docs/reviews/2026-10-11/market-research.md:88`).

Today the user must know the service, open its list, wait for the load, and filter. An ARN from a log line, an alarm or a teammate gives the service, the Region, the account and the resource. LazyCloud can read all of that and go there in one step.

This design has four parts:

1. `:go TEXT` (alias `:jump`) takes an ID, an ARN, a URL or a name. A bare ARN, ID or URL after `:` also works (decision D2).
2. A parser reads the text with no AWS call. Then one cheap read confirms the resource and gets the route parameters. Then the app opens the view, with the cursor on the resource and its detail open.
3. A resource in another Region asks before it switches the Region. A resource in another account or partition is refused with a message (decision D4).
4. Later PRs add exact-name lookups across services and a tag search with the Resource Groups Tagging API. Resource Explorer stays out (decision D6).

## Facts that the design depends on

### Navigation and focus

- `NavigateMsg{ViewID, Params}` with `Params["focus"]` pushes the view and then sends `FocusResourceMsg` (`internal/app/app.go:804-815`). When the view on top has that exact ID, the app only sends the focus and pushes nothing (`app.go:795-799`). That shortcut works only for views whose `ID()` has no parameters (ec2, ami, sg, vpc, subnet without a VPC, s3, sqs, log groups, Lambda functions, ECS clusters, task families).
- The focus is not tagged. It goes to the view on top when it arrives (`app.go:778`).
- Only `listCore` handles `FocusResourceMsg` (`internal/views/listcore.go:517-522`). `s3_objects`, `s3_versions`, `sqs_messages` and `logs_events` drop it.
- While a list loads, the focus waits in `pendingFocus`, which holds one ID (`listcore.go:134`, `:517-521`). It runs when the load ends, also when the load stops at its page budget (`listcore.go:356-359`, `:477-481`). So on a list with a budget, a focus checks only the loaded pages.
- `findFocus` compares `FocusKey`, or `Key` when there is no `FocusKey` (`listcore.go:661-672`). It never falls back from `FocusKey` to `Key`. So a list whose `FocusKey` is a name or a short ID and whose `Key` is the ARN or URL does not match a pasted ARN or URL: SQS (name vs URL, `sqs_queues.go:106`, `:120`), Lambda functions (`lambda_functions.go:62-63`), ECS clusters and services by name (`ecs_clusters.go:48-49`, `ecs_services.go:46-47`), ECS tasks by task ID (`ecs_tasks.go:57-58`), and ECS containers by name (`ecs_containers.go:60-65`, `:85-86`).
- Tests pin this behavior today: `TestListCore_FocusKey` (a `Key` is not a focus ID when `FocusKey` is set, `internal/views/listcore_complete_test.go:315-316`), and the missing-ID cases of the security group, VPC, subnet and S3 characterization tests (`list_characterization_sg_test.go:510-511`, `list_characterization_vpc_test.go:527-528`, `list_characterization_subnet_test.go:514-515`, `list_characterization_s3_test.go:672-674`).
- On a miss, `FocusMissing` runs, or nothing happens (`listcore.go:813-818`):
  - EC2 fetches the detail of an unlisted instance (`ec2_list.go:133`, `:189-199`).
  - AMIs fetch the image and add a row (`ami_list.go:64`, `:179-209`).
  - Task definition revisions fetch the detail by ARN (`ecs_taskdefs.go:188-192`).
  - Lambda functions and ECS clusters show a toast (`lambda_functions.go:138-146`, `ecs_clusters.go:93-95`).
  - Security groups, VPCs, subnets, S3 buckets, SQS queues, log groups and streams, the Lambda child lists, ECS services, tasks, families and containers do nothing and show nothing.
- The S3 bucket list has no detail on focus: the focus only moves the cursor (`s3_list.go:100-113`).
- Every route builds from its parameters alone (`internal/app/routes.go:28-113`). `:logs NAME` already opens `logs_events` from any view (`app.go:850-855`). A child view opened this way has a flat breadcrumb, and Back goes to the view below it.

### The command bar

- `executeCommand` cuts the input at the first space (`app.go:842-843`). Only `login`, `logs`, `lambda` and `ecs` take an argument. Any other command with an argument, and any unknown word, gives "Unknown command: INPUT" (`app.go:844-848`, `:897-899`).
- When the input has no space and a suggestion matches, Enter replaces the input with the name of the top command (`internal/ui/commandbar.go:115-125`). A fuzzy match turns some bare words into commands: `api` runs `lambda`, `test` runs `ecs`, `foo` runs `logs` (checked with a copy of the match logic in `internal/ui/match.go:22-81`). IDs, ARNs and URLs match no command.
- The input keeps at most 256 runes and drops the rest of a paste with no sign (`internal/ui/query.go:14-15`, `:58-62`). It turns `\n` and `\t` into spaces and drops control characters (Cc), but keeps format characters (Cf) such as U+200B and U+FEFF, and keeps U+00A0 (`query.go:57-70`).
- Bracketed paste is on. It is the default of Bubble Tea v2.1.1, and `main.go:161` sets no option. The bar takes a `tea.PasteMsg` as typed text (`commandbar.go:215-228`).
- Command words must be unique (`internal/registry/registry_test.go:29-39`). `go`, `jump`, `open`, `find` and `goto` are free (`registry.go:64-85`).

### Session, Region and account

- `Client.Region` is the Region of the session and is never empty (`internal/aws/client.go:75-76`). `applyRegion` accepts any string and switches the session (`app.go:1380-1382`).
- A session switch drops the stack and reopens only the root list of the top view, with no parameters and no focus (`app.go:1290-1300`, `:1639-1650`). The `NavigateMsg` of that root goes out in a `tea.Batch`, so a jump `NavigateMsg` in the same batch runs in random order with it. While an operation runs, the switch first asks with a y/n prompt, and it can happen later or not at all (`app.go:1165-1181`).
- The account of the session comes from STS `GetCallerIdentity` (`internal/app/identity.go:19-25`, `:46-57`). Its state is checking, known, sign-in needed or unknown (`internal/ui/header.go:13-18`). Only "known" has an account. `DescribeProfiles` gives the account of each profile that names one (`internal/aws/profileinfo.go:76-87`, `:161`).
- `Client.Config` is exported, so a lookup client for another Region costs one line, as `S3ClientForRegion` does (`internal/aws/s3.go:73-79`). The views use `m.svc.*`, which is built for one Region (`routes.go:44-100`). So a lookup can cross Regions cheaply, but a view cannot.
- S3 needs no Region switch. `ListBuckets` returns the buckets of all Regions with their Region (`s3.go:125-142`), and object calls pick a client per bucket (`s3.go:155-159`).

### Formats

- The SDK `arn` package parses `arn:PARTITION:SERVICE:REGION:ACCOUNT:RESOURCE` with `SplitN(":", 6)`, so `Resource` keeps every later `:` (`aws-sdk-go-v2@v1.48.0/aws/arn/arn.go:61-76`). It rejects a partial ARN. It is already used in `internal/aws/identity.go:53` and `profileinfo.go:174`.
- EC2 IDs have a prefix: `i-`, `ami-`, `sg-`, `vpc-`, `subnet-` (`service/ec2@v1.345.0/types/types.go:797`, `:19669`; `service/ecs@v1.106.0/types/types.go:569`; `service/ec2@v1.345.0/types/types.go:13174`, `:7471`). A security group name cannot start with `sg-` (`api_op_CreateSecurityGroup.go:61`), but nothing stops a Lambda, ECS or SQS name such as `vpc-flow-logs`.
- An S3 ARN has no Region and no account: `arn:aws:s3:::bucket/key` (`arn.go:60`).
- SQS: every call uses the queue URL (`internal/aws/sqs.go:150-445`). An AWS URL is `https://sqs.REGION.amazonaws.com/ACCOUNT/NAME` (`internal/aws/sqs_test.go:118-119`). LocalStack 2026.10.4 returns `http://sqs.REGION.localhost.localstack.cloud:4566/000000000000/NAME` (checked with `ListQueues` against the seed). An older LocalStack form, `http://localhost:4566/ACCOUNT/NAME`, has no Region (`sqs_test.go:120`). `GetQueueUrl` takes a name and an optional owner account (`service/sqs@v1.53.0/api_op_GetQueueUrl.go:47-52`).
- A log group ARN is `arn:aws:logs:REGION:ACCOUNT:log-group:NAME`, with or without `:*` at the end (`service/cloudwatchlogs@v1.92.2/types/types.go:3491-3544`). A group name has no `:` (`api_op_CreateLogGroup.go:23-25`). A group name can start with `/`. Lambda, ECS and SQS names cannot hold `/`.
- Lambda `FunctionName` takes a name, a full ARN or a partial ARN, each with an optional `:version` or `:alias` (`service/lambda@v1.118.0/api_op_GetFunction.go:39-54`).
- ECS: a service or task ARN is long (`service/CLUSTER/NAME`, `task/CLUSTER/ID`) or short (`service/NAME`, `task/ID`), and one account can hold both (`service/ecs@v1.106.0/api_op_TagResource.go:38-47`, `api_op_PutAccountSetting.go:41-56`). Every cluster parameter takes a name or an ARN (`api_op_ListServices.go:33-36`, `api_op_DescribeTasks.go:46-49`). A missing service or task comes back in `Failures`, not as an error (`api_op_DescribeServices.go:77`, `api_op_DescribeTasks.go:80`).
- `web:3` is both an ECS `family:revision` and a Lambda `function:qualifier`.

### Lookup APIs

- Direct reads that confirm one resource exist for most services, and many are already in the AWS layer: `GetInstanceDetail` (`internal/aws/ec2.go:294-311`), `RefreshAMIs` (`ec2.go:470-473`), `GetSecurityGroup` (`ec2.go:527-531`), `GetLogGroup` (`internal/aws/logs.go:138-155`), `GetFunction` (`internal/aws/lambda.go:278-289`), `DescribeTask` and `DescribeTaskDefinition` (`internal/aws/ecs.go`). There is no read for one queue URL, one bucket, one ECS cluster or one ECS service. (`GetVPC` and `GetSubnet` exist too, in `internal/aws/ec2.go`: PR 3 found them.)
- `GetBucketRegion` (`internal/aws/s3_region.go:22-40`) does not confirm a bucket. With an endpoint it makes no call (`s3_region.go:24-30`). It answers from its cache (`:32-34`). On AWS it takes the Region from the header of a denied HeadBucket (`:43-57`), and S3 sends that header for a bucket of another account.
- The Resource Groups Tagging API (`service/resourcegroupstaggingapi@v1.42.0`) returns ARNs and tags of tagged resources in the calling Region only (`api_op_GetResources.go:14-15`, `:33`). It has no name, type or state. `TagFilters` AND the keys and OR the values of a key (`:132-170`). The module is not in `go.mod`.
- That module defaults to the Smithy RPC v2 CBOR protocol since v1.42.0 (`api_client.go:216`; `CHANGELOG.md:1-3`). LocalStack 2026.10.4 rejects CBOR with HTTP 500 and answers the same call over JSON 1.1. `Options.Protocol` is public and the option functions run after the default (`api_client.go:216-220`, `options.go:122`), so a client can set `awsjson.New11(...)`, as ECS and Logs use by default (`service/ecs@v1.106.0/api_client.go:220`). Dependabot bumps the AWS modules weekly as a group (`.github/dependabot.yml:1-20`), so pinning an older version does not last.
- Resource Explorer (`service/resourceexplorer2`) needs an index and a default view in the Region. Without a default view, `Search` fails with 401 (`api_op_Search.go:17-21`, `:83-87`). Cross-Region results need an aggregator index. Results stop at 1,000 (`:55-57`). LocalStack returns HTTP 501 for every call.

### LocalStack

- The direct reads behave as on AWS: a missing instance gives `InvalidInstanceID.NotFound`, a missing queue `QueueDoesNotExist`, a missing bucket a 404 with no Region header, a missing cluster `MISSING` in `failures`, and a missing function `ResourceNotFoundException`.
- The Tagging API over JSON 1.1 answers with the tagged EC2, ECS and Lambda resources of the seed, and matches `TagFilters` and `ResourceARNList`. It ignores typed `ResourceTypeFilters` (for example `ec2:instance` returns nothing), ignores `ResourcesPerPage`, and leaves out tagged log groups.
- All ARNs in LocalStack have the account `000000000000`.

### What other tools do

- neboto searches a local cache of loaded resources with fuzzy matching, and its CLI tries an exact ID or ARN, then an exact name (https://github.com/neboto/neboto-tui).
- k9s, taws and claws use `:` for a resource type and `/` for a filter. k9s switches its context from a command only with an explicit `@ctx` and a warning (https://k9scli.io/topics/commands/).
- No competitor documents a parsed ARN jump. The AWS console search uses Resource Explorer, which splits `i-0abc` and `us-east-1` into words unless quoted (https://docs.aws.amazon.com/resource-explorer/latest/userguide/using-search-query-syntax.html).

## Hazards

These are true today. PR 0 pins the ones that the app code shows.

- H1. `:` with an ARN, an ID or a URL, and `:go` with any argument, give "Unknown command".
- H2. A focus by ARN misses on a list that focuses by name, also when the row key is that ARN (SQS by URL, Lambda functions, ECS).
- H3. A focus on a missing ID does nothing and shows nothing on most lists (security groups, VPCs, subnets, S3, SQS, log groups, ECS services and tasks).
- H4. A focus on a list with a page budget checks only the loaded pages (log groups, Lambda functions, Lambda versions, ECS services and tasks, task families).
- H5. `s3_objects` and the other views without `listCore` drop a focus.
- H6. A session switch reopens only the root list of the top view. A jump after a Region switch must wait for the switch and replace that root `NavigateMsg`.
- H7. The command input keeps at most 256 runes with no sign, and it keeps format characters such as U+200B. A long S3 object ARN, or an ARN copied from a web page, can fail to parse.
- H8. A bare word can match a command by fuzzy match, so `:api` runs `:lambda`. A bare name after `:` can never be a jump.
- H9. The Tagging API module defaults to CBOR, which LocalStack rejects. A Dependabot bump of any AWS module can change the protocol of a client.
- H10. A view whose `ID()` has parameters never takes the same-view shortcut, so a second jump to it pushes a second copy.
- H11. `GetBucketRegion` does not confirm that a bucket exists or belongs to the account. With an endpoint it makes no call.

## Design

### Names

- Command `go`, with the alias `jump`. Its description: "Jump to a resource by ID, ARN, URL or name".
- A new package `internal/jump` holds the parser and the routing table. It has no UI and no AWS calls, so it has plain table tests.
- No new view in PR 1 to PR 5. The tag search of PR 6 adds one view, `jump_tags`, and the command `tags` (see "Tag search").
- No new global key (decision D2).

### What the parser accepts

`jump.Parse(text)` returns a `Target`:

```go
type Target struct {
	Kind      Kind   // Empty, Invalid (an ARN or URL it cannot read), Unsupported,
	                 // Instance, AMI, SecurityGroup, VPC, Subnet, Bucket, Object, Queue,
	                 // LogGroup, LogStream, Function, Cluster, Service, Task, TaskDefinition,
	                 // Name (a bare name), Tags
	Partition string // from an ARN, else ""
	Region    string // from an ARN or a URL, else ""
	Account   string // from an ARN or a URL, else ""
	Service   string // the ARN service, for Unsupported
	ID        string // the main ID or name: i-..., the bucket, the queue name, the group name, ...
	Parent    string // the bucket of an object, the group of a stream, the cluster of a service or task
	Qualifier string // a Lambda version or alias, an ECS revision, the SUFFIX of NAME:SUFFIX
	Tags      []Tag  // the tag:KEY=VALUE terms (PR 6)
	Raw       string // the cleaned input
}
```

Before it parses, the parser trims spaces, removes format characters (Cf, such as U+200B, U+FEFF and bidi marks), and turns U+00A0 into a space. The input turns a line break into a plain space (`internal/ui/query.go:65-66`), so the parser cannot tell it from a real space. The parser removes the spaces from an ARN, with two exceptions, because an S3 key and a log stream name can hold spaces: a bucket ARN keeps the spaces after `BUCKET/`, and a log stream ARN keeps the spaces after `:log-stream:`. An `s3://` URL keeps its spaces. An ARN with no partition, service or resource, or with an account that is not 12 digits, is Invalid.

`executeCommand` checks the whole trimmed input with `jump.Looks(input)` before it cuts the input at the first space (`app.go:842`). When the input starts with `arn:`, `s3://` or `http`, or is an EC2 ID, the whole input goes to the jump.

The order of the checks:

1. An ARN (`arn.Parse`):
   - `ec2`: `instance/`, `image/`, `security-group/`, `vpc/`, `subnet/`. Other EC2 types are Unsupported.
   - `s3`: a bucket, or a bucket and a key. Region and account are empty.
   - `sqs`: a queue name.
   - `logs`: `log-group:NAME`, with `:*` cut, or `log-group:NAME:log-stream:STREAM`.
   - `lambda`: `function:NAME`, with an optional qualifier.
   - `ecs`: `cluster/`, `service/`, `task/` (long or short), `task-definition/FAMILY:REV`. Container ARNs are Unsupported.
   - Any other service is Unsupported, with the service name kept for the message.
2. A partial Lambda ARN `ACCOUNT:function:NAME[:QUALIFIER]`.
3. A URL: an SQS queue URL (two path parts, the first is 12 digits), or `s3://BUCKET[/KEY]`. The Region of a queue URL comes from a host `sqs.REGION.amazonaws.com` or `sqs.REGION.localhost.localstack.cloud`. Any other host gives no Region, and the session Region is used.
4. An EC2 ID: `^(i|ami|sg|vpc|subnet)-[0-9a-f]{8}([0-9a-f]{9})?$`. LocalStack 2026.10.4 makes IDs of the same form: 17 hex digits for instances, groups, VPCs and subnets, and 8 for its AMIs (checked against the seed in PR 1).
5. A path that starts with `/`: a log group name.
6. `tag:KEY=VALUE`, one or more, separated by spaces: Tags (PR 6).
7. Anything else: a Name (PR 5). Before PR 5, a name gives a message that names the accepted forms. Before PR 6, tags do too.

An ARN of a known service with a resource that the parser cannot read (`function:` with no name, a `cluster/` with two names), an `http` URL that is not a queue URL, and a path with characters that a log group cannot hold are Invalid. Empty text is Empty (Q2). Both give a message that names the accepted forms.

### The checks before the jump

`jump.Check(target, session)` returns a refusal message, or nothing. An empty field in the target means the input does not name it, and the check of that field is skipped. S3 ARNs, EC2 image ARNs (`arn:aws:ec2:REGION::image/ami-...`), EC2 IDs, names, paths and partial Lambda ARNs have an empty Region, an empty account, or both.

- A partition other than the partition of the session Region: refused. "The resource is in the aws-cn partition. The session uses aws." A queue URL has no partition, so the check uses the partition of its Region.
- An account in the target other than the session account, when STS knows the account: refused. When a profile in the config names that account, the message says so: "The resource is in account 111122223333, the profile prod. Press P to switch." When no profile names it: "The resource is in account 111122223333. The session uses account 444455556666." The app does not switch the profile (decision D4).
- When the account is not known yet (checking, sign-in needed, unknown), the jump goes on. The lookup then fails with the error of AWS if the account is wrong.
- With an endpoint (LocalStack), the account check is skipped, because LocalStack ARNs have the account `000000000000`.
- A Region in the target other than the session Region, for every kind except S3, is not a refusal. `jump.OtherRegion(target, session)` returns that Region, and the app asks with a y/n prompt, "The resource is in eu-west-1. Switch to eu-west-1 and open it?" On yes, it switches the session and opens the resource after the switch (see "Another Region"). On no, nothing happens.
- Unsupported: "LazyCloud has no view for SERVICE resources: ARN", so the user can copy the ARN. A message shows at most 120 runes of the text.

### The lookup

After the checks, one read confirms the resource and gets the route parameters. It runs as an operation with a timeout of 30 seconds, so the operations line shows "Find RESOURCE" and ctrl+x cancels it. A result that comes after a session switch is dropped. A missing resource gives one toast that names it and the Region: "No Lambda function named orders in us-east-1." An access error gives the text of `aws.ErrorText`.

| Kind | Read | Route |
|---|---|---|
| Instance | `GetInstanceDetail` | `ec2_list` with focus ID |
| AMI | `RefreshAMIs` | `ami_list` with focus ID |
| SecurityGroup | `GetSecurityGroup` | `sg_list` with focus ID |
| VPC, Subnet | `GetVPC`, `GetSubnet` | `vpc_list`, `subnet_list` with focus ID |
| Bucket | new `HeadBucket` | `s3_objects{bucket}` |
| Object | new `HeadBucket` | `s3_objects{bucket, prefix: the folder of the key, focus: the key}` |
| Queue | new `GetQueueURL` | `sqs_queues` with focus URL |
| LogGroup | `GetLogGroup` | `logs_events{log_group, log_group_arn, ...}` (as `:logs`) |
| LogStream | `GetLogGroup` | `logs_events{log_group, log_stream}` |
| Function | `GetFunction` | `lambda_functions` with focus ARN. With a qualifier: `$LATEST` or a number goes to `lambda_versions{function, log_group}` with focus on the version, and any other qualifier goes to `lambda_aliases{function, log_group}` with focus on the alias. `log_group` comes from the `GetFunction` result, as `lambdaChildParams` builds it (`lambda_children.go:33-35`). |
| Cluster | new `DescribeCluster` | `ecs_clusters` with focus ARN |
| Service | new `DescribeService` | `ecs_services{cluster: ARN}` with focus ARN |
| Task | `DescribeTask` | `ecs_tasks{cluster: ARN}` with focus ARN |
| TaskDefinition | `DescribeTaskDefinition` | `ecs_task_definitions{family, focus: ARN}` |

A short ECS service or task ARN has no cluster. The lookup tries the `default` cluster, as AWS does when no cluster is given. When that fails, the message says that the ARN has no cluster and asks for the long ARN.

The new `HeadBucket` read sends HeadBucket on the client of the bucket Region, also with an endpoint, with `ExpectedBucketOwner` set to the session account when STS knows it. A 404 gives "No bucket named NAME." A 403 gives "Bucket NAME is not in this account, or access is denied." (H11).

In PR 3, the lookup uses the services of the session for the session Region. In PR 4, a target in another Region gets its lookup from clients for that Region, built from `Client.Config` as `S3ClientForRegion` does (`internal/aws/s3.go:73-79`). That lookup runs before the Region prompt.

### Focus changes in the list core and the views

- `findFocus` tries `FocusKey` first and then `Key` (H2). Then a focus by ARN works on every list whose row key is the ARN, and a focus by name or task ID keeps working. `TestListCore_FocusKey` changes with it.
- Each list that loads all of its rows and has no `FocusMissing` gets one that shows a toast: "No security group sg-123 in the list" (security groups, VPCs, subnets, S3, SQS) (H3). The text does not name the Region: these views do not know it, and the bucket list holds the buckets of every Region. The header shows the Region. The missing-ID cases of the characterization tests change with it.
- Lists with a page budget get a `FocusMissing` that fetches the one resource and opens its detail, as EC2 and the task definitions do (H4): Lambda functions with `GetFunction`, Lambda versions with `GetFunction(function, version)`, ECS services with `DescribeService`, ECS tasks with `DescribeTask` (also a stopped task), log groups with `GetLogGroup`. The row is not added, as for EC2. `TestLambdaFunctions_FocusMissing` changes from the toast to the fetch.
- `s3_objects` handles `FocusResourceMsg`, as the list core does: while it loads, the focus waits, and after the load it moves the cursor to that key. When the key is not on the loaded pages, a toast says so, and `l` loads more (H5). The `focus` route parameter reaches it through the same message.
- A jump never pushes a second copy of the view on top (H10). The app builds the view of the route and compares its `ID()` with the `ID()` of the view on top. `ID()` holds the route parameters and never the focus. When the IDs match, the app sends only the focus, and with no focus it does nothing. This runs for every `NavigateMsg`, also with no focus (a bucket, a log group). A panel link uses the same check, so a link to the view on top closes the panel and adds no back entry. Before PR 2, a link with no focus to the view on top pushed a copy and added an entry (A6 decision D3, `TestA6Keep_LinksToOtherViewsAddAnEntry`).

### Another Region

The lookup runs in the target Region before the prompt. A miss or an access error gives its toast, and the app asks nothing and does not switch. On yes at the Region prompt, the app switches the session with a follow-up: the `NavigateMsg` that the lookup built, in place of the root `NavigateMsg` of the switch (H6). `switchSession` takes an optional follow-up and sends it in place of the root. When the switch first asks because an operation runs, the follow-up waits with it. When the switch fails, the follow-up does not run.

The new session starts with the same mode rules as a Region switch from `R`: the same profile keeps ReadWrite (`app.go:1251-1268`).

### Names (PR 5)

A bare name runs exact-name reads in the session Region, all at once, each with `callTimeout`:

- Lambda `GetFunction(name)`, ECS `DescribeCluster(name)`, ECS `DescribeTaskDefinition(name)` (the family), SQS `GetQueueURL(name)`, S3 (the bucket is in `ListBuckets` of the session, `s3.go:125-142`, or `HeadBucket(name)` with the session account as the owner, when the name is a valid bucket name), Logs `GetLogGroup(name)`, EC2 security groups by the `group-name` filter, and EC2 instances by the `tag:Name` filter (both new reads).
- No match gives a toast. One match opens it. More than one opens a picker: "Several resources are named web", with one line for each, for example "ECS cluster", "Lambda function", "EC2 instance i-0abc (running)".
- A name can never come after a bare `:`, because the command bar turns some words into commands (H8). It needs `:go`.
- `NAME:SUFFIX` (for example `web:3`) asks both Lambda (function and qualifier) and ECS (family and revision).

### Tag search (PR 6)

- `:go tag:team=orders` opens `jump_tags`, a list of the tagged resources of the session Region from `GetResources` with `TagFilters`. Several `tag:` terms are ANDed.
- Columns: Service, Type, Name (the last part of the ARN), Region, and the tags. A page budget of 10 pages of 100, with `l` for more.
- enter runs the jump of that ARN. A resource with no view shows the message of Unsupported.
- The client sets `o.Protocol = awsjson.New11(...)`, and a pin test checks the `X-Amz-Target` header of a request, so a Dependabot bump that changes the protocol fails the test (H9).
- The registry gets a service "Resources" with the feature "Tag search", ViewID `jump_tags`, and a nav command `tags` ("Search the resources of the Region by tag"). `jump_tags` is its own root (`internal/app/app_test.go:566-601`). With no tag filter it opens a form for the filter, so the route builds with no parameters. `:go tag:team=orders` opens it with the filter.
- The text of the toast for a missing permission names the action: "tag:GetResources is not allowed for this identity."

### Seed and tests

- The seed already tags EC2 instances, ECS clusters and services, Lambda functions and log groups (`cmd/seed/ecs.go:120`, `:266`, `cmd/seed/lambda.go:141`, `cmd/seed/logs.go:64-65`, `cmd/seed/ec2.go:591-597`). No change is needed. SQS and S3 have no tags. The seed puts `team=orders` on the function `orders-api` and its log group (`cmd/seed/tiers.toml:207`, `:248`), so the tag search smoke test uses that tag. LocalStack leaves tagged log groups out of the Tagging API, so the test expects the function only.
- Unit tests in `internal/jump` cover every accepted form, the cleaning of the input, each refusal, and inputs that look alike: `vpc-flow-logs` (a name, not a VPC), `web:3`, `/aws/lambda/x`, a log group ARN with `:*`, short and long ECS ARNs, S3 ARNs with no Region, LocalStack URLs in both forms.
- Unit tests in `internal/app` cover the command, the bare `:` form, the routes of each kind, the Region prompt and the follow-up after the switch.
- LocalStack smoke tests: `:go` with an instance ID, a Lambda ARN, a queue URL, a log group name, an ECS service ARN and a task definition ARN of the seed (PR 3), a bare name (PR 5), and `tag:team=orders` (PR 6). The Region switch runs in a unit test, because the seed has one Region.

### What does not change

- `:logs NAME`, `:lambda NAME` and `:ecs NAME` keep their behavior.
- No view gets a new column. No AWS write is added.
- Resource Explorer is not used (decision D6).

## Visible changes

- `:go ID`, `:go ARN`, `:go URL` and `:ARN` open the resource, with its detail.
- A resource in another Region asks before the switch. Another account or partition gives a message.
- Lists that showed nothing for a missing ID now say that it is missing.
- `:go NAME` finds resources of that name across services (PR 5).
- `:go tag:KEY=VALUE` lists the tagged resources of the Region. Home has a "Resources" tile with "Tag search", and `:tags` opens it (PR 6).

## Plan

Each PR needs the one before it.

- PR 0. This design, and pin tests: `TestJumpHazard_*` pins H1 (`:go` and `:ARN` give "Unknown command"), H2 (a Lambda focus by ARN misses), H3 (a security group focus on a missing ID shows nothing), H5 (`s3_objects` does not move its cursor to a focus), H7 (the input stops at 256 runes), H10 (a second focus link to `ecs_task_definitions` pushes a second copy) and H11 (`GetBucketRegion` does not confirm a bucket). `TestJumpGuard_*` pins two facts that stay: H6 (a plain session switch reopens the root with no focus) and H8 (a bare word runs a command by fuzzy match). `TestLambdaFunctions_FocusMissing` already pins H4.
- PR 1. `internal/jump`: the parser, the cleaning of the input, and the checks, with table tests. The only UI change is the input limit in `internal/ui/query.go:15`, which goes from 256 to 2,048 runes for the command bar, the picker and the help filter. H7 flips. The input still keeps format characters, and the parser removes them.
- PR 2. The focus changes: `findFocus` falls back to `Key`, the toasts and fetches of `FocusMissing`, the focus of `s3_objects`, and the same-view check by `ID()`. The new read `DescribeService`. H2, H3, H4, H5 and H10 flip, and `TestListCore_FocusKey` and the missing-ID cases of the characterization tests change with them. Unit tests only.
- PR 3. `:go` and the bare `:` form for IDs, ARNs and URLs in the session Region: the lookup, the routes, and the messages. The new reads `GetQueueURL`, `HeadBucket` and `DescribeCluster`. Until PR 4, a resource in another Region gives "The resource is in eu-west-1. Press R to switch to eu-west-1, then jump again." H1 and H11 flip. Smoke tests for an instance, a Lambda ARN, a queue URL, a log group, an ECS service ARN, a task definition ARN and an S3 object.
- PR 4. Another Region: the lookup clients for the target Region, the lookup before the prompt, the prompt, the follow-up of `switchSession`, and S3 without a switch. The H6 guard stays. New tests check that the follow-up replaces the root `NavigateMsg`, waits when an operation runs, and does not run when the switch fails, and that a miss in the other Region keeps the session and the stack.
- PR 5. Exact names across services, with the picker, and the reads by name for security groups and instances.
- PR 6. Tag search: the Tagging API module with the JSON 1.1 protocol and its pin, the `jump_tags` view, the "Resources" service with the "Tag search" feature, the `:tags` command, and the smoke test. H9 is pinned in this PR, because the module comes with it.

## Decisions

- D1. The scope. Proposed: IDs, ARNs and URLs first (PR 1 to PR 4), then exact names (PR 5), then tags (PR 6). Other answers: (a) stop after PR 4, and plan names and tags in a later design. (b) Add fuzzy search over a cache of loaded resources, as neboto does.
- D2. How the user starts a jump. Proposed: `:go TEXT` (alias `:jump`), and a bare ARN, ID or URL after `:`. No new global key. Other answers: (a) a global key such as `@` that opens the command bar with `go ` filled in. (b) `:go` only, with no bare form.
- D3. Where a jump lands. Proposed: the list of the resource, with the cursor on it and its detail open, so Back goes to the view the user came from. A bucket and a log group open their content (`s3_objects`, `logs_events`), as enter on their row does today. Other answer: open only the detail panel over the current view.
- D4. Another account, Region or partition. Proposed: a y/n prompt and a switch for another Region, a refusal with a hint for another account (the profile name when the config knows it), and a refusal for another partition. Other answers: (a) refuse every Region other than the session Region, with "Press R". (b) Also switch the profile when one profile names the account.
- D5. A resource past the page budget of its list. Proposed: `FocusMissing` fetches the one resource and opens its detail, with no row, as EC2 does. Other answer: load more pages until the resource shows, which can be many calls.
- D6. The tag search engine. Proposed: the Resource Groups Tagging API, Region by Region, with the JSON 1.1 protocol. It needs no setup and LocalStack answers it. Other answers: (a) Resource Explorer, which also finds untagged resources and other Regions, but needs an index and a default view, stops at 1,000, and has no LocalStack support. (b) Both, with Resource Explorer when the account has a default view.
- D7. The lookup before the jump. Proposed: one read for each jump, so a missing resource gives a clear message before any view opens, and the route gets what it needs (the cluster of a task, the URL of a queue, the log group of a function). Other answer: open the view with the parsed values, and let the view show the miss.

## Open questions

- Q1. Does a jump to an S3 object open its folder with the cursor on it, or the versions of the object (`s3_versions`)? Proposed answer: the folder, with the cursor on the object.
- Q2. Does `:go` with no argument open anything? Proposed answer: no. It shows the accepted forms in a toast.
- Q3. Does the command history keep the jump text? Proposed answer: yes, as for every command today (`app.go:641-646`). It is in memory only.
- Q4. Does a jump to a Lambda function open the function list, or its log group as enter on a function does? Proposed answer: the list, with the cursor on the function and its detail open. enter then opens the logs.
- Q5. What does a tag search show for a resource of a service with no view (for example RDS)? Proposed answer: the row, with the service and the ARN. enter shows the message of Unsupported.
