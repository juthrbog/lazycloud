# ECS: clusters, services, tasks, containers, task definitions and exec

Status: Approved on 2026-11-21 with all proposed answers (#324 merged). PR 0 is this design and its pin tests. The facts below were checked at `c7a867e` with `service/ecs` v1.106.0 and LocalStack 2026.10.4.

## Summary

Market research puts ECS third on the roadmap, after CloudWatch Logs and Lambda (`docs/reviews/2026-10-11/market-research.md:68`). e1s, a TUI for ECS only, has 939 stars. Closed issues #129 (browsing) and #130 (exec and actions) describe the feature. This design follows them, with changes where the facts below disagree with them.

Users of an ECS view want answers to three questions: what runs now, why did a task stop, and what do its logs say. They also want a shell in a container. So this design has five parts:

1. A cluster list, a service list with a detail panel, and a task list with a toggle for stopped tasks.
2. A container list for each task, with a jump to the exact log stream of the container in the event viewer of CloudWatch Logs.
3. A task definition browser.
4. ECS Exec on `o`, in the same way as SSM on an EC2 instance.
5. Three write actions behind ReadWrite: force a new deployment, change the desired count, and stop tasks.

The reads come first. Exec and the write actions come in the last two PRs (decision D1).

## Facts that the design depends on

### The module

- `service/ecs` v1.106.0 (2026-11-16) needs `aws-sdk-go-v2` v1.48.0 and `smithy-go` v1.28.9, the versions that the repo pins (`go.mod:12`, `:21`). Its internal modules `configsources` v1.5.12 and `endpoints/v2` v2.8.12 also match (`go.mod:35-36`). Adding it changes only `go.mod` and `go.sum`.
- ECS uses the `awsJson1_1` protocol: each request is `POST /` with the header `X-Amz-Target: AmazonEC2ContainerServiceV20141113.<Op>`. The `httptest` fake of CloudWatch Logs keys on that header (`internal/aws/logs_test.go:61`), so the ECS fake can copy it.

### Lists and describes

- Each list call returns ARNs only. A second call describes them (`api_op_ListClusters.go:71`, `api_op_ListServices.go:105`, `api_op_ListTasks.go:150`).

| Call | Page size | Describe call | Batch size |
|---|---|---|---|
| `ListClusters` | 1 to 100, default 100 | `DescribeClusters` | 100 |
| `ListServices` | 1 to 100, default 10 | `DescribeServices` | 10 |
| `ListTasks` | 1 to 100, default 100 | `DescribeTasks` | 100 |
| `ListTaskDefinitionFamilies` | 1 to 100, default 100 | none (names) | |
| `ListTaskDefinitions` | 1 to 100, default 100 | `DescribeTaskDefinition` | 1 |

  (`api_op_ListServices.go:33-63`, `api_op_DescribeServices.go:36-40`, `api_op_DescribeTasks.go:41-44`, `api_op_DescribeClusters.go:35-37`.)
- A describe call returns the items that it found, and a `Failures` list with `Arn`, `Reason` and `Detail` for the others. A missing resource is a failure with the reason `MISSING`, not an error (`types/types.go:8122-8131`, AWS failure reasons page).
- `DescribeServices` and `DescribeTasks` need their list of keys. The client refuses a nil list (`validators.go:4426-4428`, `:4474-4476`). An empty list goes to the server, which answers 400 (e1s issue #116). `DescribeClusters` has no such check: with no clusters, it describes the cluster named `default` (`api_op_DescribeClusters.go:35-37`). So a describe call must never get zero keys.
- A call without `Cluster` acts on the cluster named `default` (`api_op_DescribeServices.go:42-46`).
- `DescribeClusters` returns the settings (Container Insights) only with `Include=SETTINGS`, and the configuration (the exec log settings) only with `Include=CONFIGURATIONS` (`api_op_DescribeClusters.go:39-53`).
- `ListTasks` takes one `DesiredStatus`: `RUNNING` (the default) or `STOPPED`. `PENDING` returns nothing (`api_op_ListTasks.go:50-60`). Stopped tasks stay listable "for at least one hour" (`api_op_DescribeTasks.go:19`).
- `ListTaskDefinitions` sorts by family and revision, oldest first. `Sort=DESC` gives the newest first. Its `FamilyPrefix` matches an exact family name only (`api_op_ListTaskDefinitions.go:34-71`, LocalStack).
- Tags come back only with `Include=TAGS`. `DescribeTaskDefinition` returns the tags on the output, not in `TaskDefinition` (`api_op_DescribeTaskDefinition.go:94`).

### Names and ARNs

- A cluster ARN is `arn:aws:ecs:<region>:<account>:cluster/<name>`. A service ARN is `.../service/<cluster>/<name>` in the long format, or `.../service/<name>` in the old short format. A task ARN is `.../task/<cluster>/<id>` or `.../task/<id>` (`api_op_TagResource.go:38-47`). One account can hold both formats, because the format follows the user or role that made the resource (`api_op_PutAccountSetting.go:41-62`).
- A name holds letters, numbers, `-` and `_` only. So the last `/` segment of an ARN is the name or the ID in both formats (`api_op_CreateCluster.go:66`, `types/types.go:15469-15472`). Service names are unique only in their cluster.
- `Cluster.ClusterName`, `Service.ServiceName` and `Service.ClusterArn` exist. A task has no ID field, so the ID comes from the ARN. The cluster of a task comes from `Task.ClusterArn`, because the short ARN has no cluster.
- A task definition ARN ends in `task-definition/<family>:<revision>` (`api_op_RunTask.go:102`). Services, deployments and tasks hold only this ARN.

### Status fields

- Most status fields are strings with values only in the doc comments:
  - Cluster: `ACTIVE`, `PROVISIONING`, `DEPROVISIONING`, `FAILED` and `INACTIVE` (`types/types.go:1189-1209`).
  - Service: `ACTIVE`, `DRAINING` and `INACTIVE` (`:15481-15482`).
  - Deployment: `PRIMARY`, `ACTIVE` and `INACTIVE` (`:5438-5446`).
  - Task: the lifecycle values `PROVISIONING`, `PENDING`, `ACTIVATING`, `RUNNING`, `DEACTIVATING`, `STOPPING`, `DEPROVISIONING` and `STOPPED` (AWS task lifecycle page).
- Typed enums:
  - `HealthStatus`: `HEALTHY`, `UNHEALTHY` and `UNKNOWN`. A container without a health check is `UNKNOWN` (`types/enums.go:1446-1448`, `types/types.go:1602-1603`).
  - `TaskStopCode`: `TaskFailedToStart`, `EssentialContainerExited`, `UserInitiated`, `ServiceSchedulerInitiated`, `SpotInterruption`, `TerminationNotice` and `InfrastructureHealth` (`enums.go:2568-2574`). `StoppedReason` is free text, for example `CannotPullContainerError: ...`.
  - `DeploymentRolloutState`: `COMPLETED`, `FAILED` and `IN_PROGRESS` (`enums.go:932-934`). Only the `ECS` deployment controller fills it (`types/types.go:5397-5409`).
  - `LaunchType`: `EC2`, `FARGATE`, `EXTERNAL` and `MANAGED_INSTANCES` (`enums.go:1590-1593`). A service with a capacity provider strategy has no launch type (`types/types.go:15328-15331`).
  - `DeploymentControllerType`: `ECS`, `CODE_DEPLOY` and `EXTERNAL` (`enums.go:813-815`). `DeploymentStrategy`: `ROLLING`, `BLUE_GREEN`, `LINEAR` and `CANARY` (`enums.go:953-956`).
- CPU and memory are strings on a task and a container, and numbers on a container definition (`types/types.go:18315`, `:1835`, `:2143`).

### Services and deployments

- `Service.Events` holds the 100 latest events, with no paging (`types/types.go:15378-15379`).
- `Service.Deployments` lists the deployments of the rolling controller, with counts, `RolloutState` and its reason. `FailedTasks` counts failures in a row and goes back to 0 when a task runs (`:5354-5361`).
- The newer deployment API gives more: `Service.CurrentServiceDeployment` is an ARN, and `DescribeServiceDeployments` (at most 20 ARNs) returns the status (`PENDING`, `IN_PROGRESS`, `SUCCESSFUL`, `ROLLBACK_*` and more), the lifecycle stage, the rollback, the circuit breaker and the alarms (`types/types.go:15343`, `:16565-16690`, `enums.go:2234-2242`).
- `ResourceManagementType=ECS` marks a service that Express Mode manages (`types/types.go:15431-15433`).
- `UpdateService` takes `ForceNewDeployment` (sent only when true) and `DesiredCount` (a pointer, so 0 is sent). A new desired count starts no deployment. A `CODE_DEPLOY` or `EXTERNAL` service accepts only some fields (`api_op_UpdateService.go:34-47`, `:224-256`).
- `StopTask` takes the full task ARN and an optional reason (`api_op_StopTask.go:48-65`). The task then has the stop code `UserInitiated` (`types/enums.go:2570`, LocalStack).

### Logs

- A container sends its logs to CloudWatch when its definition has `LogConfiguration.LogDriver=awslogs`. The options are `awslogs-group`, `awslogs-region` and `awslogs-stream-prefix` (`types/types.go:11845-11913`).
- The stream name is `<prefix>/<container name>/<task ID>`. Without a prefix, which is allowed on EC2 only, the stream has the Docker container ID as its name (`:11904-11913`, LocalStack).
- The log configuration is in the task definition, not in the task. So the jump to the logs needs `DescribeTaskDefinition`.
- Other drivers (`awsfirelens`, `splunk` and more) have no CloudWatch stream to open.

### Exec

- `ExecuteCommand` needs `Interactive=true`, the task, the command, and the container when the task has more than one (`api_op_ExecuteCommand.go:39-62`, `:100-103`).
- Exec works only when `Task.EnableExecuteCommand` is true and the container has a managed agent `ExecuteCommandAgent` with `LastStatus=RUNNING` (AWS ECS Exec page). Exec cannot be turned on for a task that runs already.
- The AWS CLI runs `aws ecs execute-command`, which calls the API and then starts `session-manager-plugin`. The plugin needs the runtime ID of the container in its target, so a direct call of the plugin needs more code (awscli `executecommand.py`).
- EC2 already starts SSM sessions through the AWS CLI (`internal/aws/cliexec.go:113-129`), with the checks `AWSCLIAvailable` and `SSMPluginAvailable` (`internal/views/ec2_list.go:236-274`).

### Quotas

Each bucket is per account and Region. A throttle is `ThrottlingException` (AWS request throttling page).

| Bucket | Calls | Burst | Refill each second |
|---|---|---|---|
| Cluster read | `ListClusters`, `DescribeClusters` | 50 | 20 |
| Service read | `ListServices`, `DescribeServices` | 100 | 20 |
| Cluster resource read | `ListTasks`, `DescribeTasks` | 100 | 20 |
| Task definition read | the three task definition calls | 50 | 20 |
| Service deployment | `DescribeServiceDeployments` | 50 | 20 |
| Cluster resource modify | `ExecuteCommand`, `StopTask` | 100 | 40 |
| Service modify | `UpdateService` | 50 | 5 |

One page of 100 services costs 11 calls: one list and ten describes.

### LocalStack

- `ecs` is available. Each task runs a real Docker container on the host, with a published port and no limits. An idle `nginx:alpine` uses about 13 MiB. A service with a desired count of 0 runs no container.
- The list calls ignore `MaxResults` and never return a token. The describe calls do not enforce the batch limits. So only unit tests can cover paging and batches.
- Cluster counts (`RunningTasksCount` and the others) are always 0, and `Statistics` is always null. `CapacityProviders` is null and Container Insights is `disabled`, whatever the cluster was made with.
- `Service.Events` is always empty. A force deploy or a new desired count changes the one `PRIMARY` deployment in place and replaces every task. A service with a desired count of 0 stays `IN_PROGRESS`.
- `ExecuteCommand`, `ListServiceDeployments` and `DescribeServiceDeployments` return `InternalFailure` "not currently supported by LocalStack". `RuntimeId` and `ManagedAgents` are always null.
- `StopCode` and `StoppedReason` are set only for `StopTask` and for an image pull failure. A container that exits gives an empty stop code.
- A running task has `StoppedAt` set already. Only `LastStatus` tells that a task stopped.
- `RunTask` and `CreateService` with `awsvpc` need a subnet that exists in the EC2 of LocalStack, for example a default VPC subnet.
- `awslogs` writes to the stream `<prefix>/<container>/<task ID>`. With `awslogs-create-group=true`, LocalStack makes the group.
- `DescribeTasks` with a bare task ID returns the task and also a `MISSING` failure for the same ID. Full ARNs do not.
- An unknown `ServiceName` in `ListTasks` gives an empty list, not `ServiceNotFoundException`. `DesiredCount=-1` is accepted.
- A task leaves its ENI in EC2 after it stops. `DeleteCluster` succeeds while services exist, and a deleted cluster stays `INACTIVE` in the list for a few minutes.
- Task definition families stay in `ListTaskDefinitionFamilies` after all revisions are deleted. The research left eight `zz-research-*` families in the LocalStack of this worktree. `go run ./cmd/seed --wipe` clears them.

### The app

- The list core can do the list call and the describe calls in one fetch function, so one page holds both. This keeps `PageBudget` and `MoreKey`, which `Complete` does not allow (`internal/views/listcore.go:166-172`).
- The SQS list shows a queue that it cannot read as a row with `?` counts, and one toast for all of them (`internal/views/sqs_queues.go:232-247`, `:620-635`).
- `ui.StateColor` knows `active`, `running`, `pending`, `stopping`, `stopped`, `failed` and `inactive`. It gives no color to `PROVISIONING`, `DEPROVISIONING`, `DRAINING`, `ACTIVATING`, `DEACTIVATING`, `HEALTHY`, `UNHEALTHY`, `UNKNOWN`, `IN_PROGRESS` and `COMPLETED` (`internal/ui/icons.go:73-87`).
- A column can have its own style function, but the function gets only the plain text of its own cell (`internal/ui/layout.go:38-41`, `internal/ui/table.go:262-266`). So a color that depends on another field must come from the text of the cell.
- The list core reloads with `cfg.Fetch` on `r`. Its columns and its row function are fixed in its configuration, and it changes the columns only on a resize (`internal/views/listcore.go:191-197`, `:241-245`, `:602-612`).
- A focus in the list core moves the cursor and opens the detail. A focus on a row that is not loaded calls `FocusMissing`. The AMI list uses it to fetch the linked image (`internal/views/listcore.go:786-806`, `internal/views/ami_list.go:176-179`).
- `executeCommand` allows an argument only for `login`, `logs` and `lambda` (`internal/app/app.go:845`).
- The event viewer can open one stream with a range (`log_stream`, `log_start`, `log_end`, `stream_first` and `missing_hint`, `internal/views/logs_events.go:222-252`). Only a missing group has its own text, and `missing_hint` follows that text (`:330-342`). The Lambda invocation list also sends `missing_hint` with a stream. A missing stream is a plain error, and the logs AWS layer has no sentinel for it (`internal/aws/logs.go:39-40`).
- `SSMSessionCmd` passes no `--endpoint-url`, so an SSM session started against LocalStack goes to AWS (`internal/aws/cliexec.go:116-129`).
- The app owns `q T P R W L : ?` and backspace, and `TestListConformance_NoGlobalLetters` forbids `E` (`internal/app/app.go:666-724`, `internal/views/list_conformance_test.go:769-785`). The list core uses `esc enter d y space s S / r l`. EC2 uses `o` for SSM with `ModeReadWrite`, and the app blocks that key in ReadOnly (`internal/app/app.go:766-775`).
- A form reply has no tag, so the view keeps its target in a field, as the SQS send form does (`internal/views/sqs_queues.go:380`, `:448-456`). A confirm prompt has a message, a phrase and a tag, and no input field (`internal/msg/messages.go:51-61`).
- `TestListsLoad` needs a case for each feature of the registry, and it presses `d` on the seeded row (`internal/smoke/lists_test.go:14-43`). `TestRoutes` needs a route for each view of the registry (`internal/app/app_test.go:567-590`).
- The seeders run in parallel (`cmd/seed/main.go:99-105`).

## Hazards

These are true today. PR 0 pins the ones that the app code shows.

- H1. A describe call takes at most 10 services, or 100 tasks or clusters. An empty batch of services or tasks fails, and an empty batch of clusters describes the cluster `default`. LocalStack enforces none of these rules.
- H2. A describe call drops a resource that it cannot read into `Failures` with no error.
- H3. `ui.StateColor` gives no color to the ECS lifecycle states `PROVISIONING`, `DEPROVISIONING`, `DRAINING`, `ACTIVATING` and `DEACTIVATING`.
- H4. A task in a short-format ARN has no cluster in its ARN.
- H5. The log stream of a container comes from its task definition. Without `awslogs`, there is no stream. Without a prefix, the stream name is not known, but the group can still open. The log group can be in another Region, or missing. The task definition call can fail while the task is readable.
- H6. The event viewer shows a plain error for a missing stream, and the logs AWS layer has no sentinel for it. `missing_hint` is a hint for a missing group.
- H7. `SSMSessionCmd` passes no endpoint. An exec built the same way leaves LocalStack for AWS.
- H8. Stopped tasks disappear after about an hour. `StopCode`, `StoppedReason`, `StartedAt` and `ExitCode` can be empty.
- H9. `CODE_DEPLOY` and `EXTERNAL` services reject most `UpdateService` fields. Express Mode manages its own services.
- H10. The LocalStack quirks above: no paging, no batch limits, zero cluster counts, empty events, no exec and no deployment API.
- H11. The service read bucket refills at 20 calls each second, and one page of 100 services costs 11 calls.
- H12. The list core has one fetch, one set of columns and one row function. A list that switches between two kinds of rows needs a change in the core.
- H13. `ListTaskDefinitions` lists only active revisions by default, and the revision list stops at its page budget. A service or a task can use an inactive revision, so a link can name a revision that is not in the list. `ListTaskDefinitions` returns ARNs only, with no CPU, memory or time.

## Design

### Names

- Service `ECS` on the home screen, with the features `Clusters` and `Task definitions`.
- View IDs: `ecs_clusters`, `ecs_services`, `ecs_tasks`, `ecs_containers`, `ecs_task_families` and `ecs_task_definitions`. The root of the first four is `ecs_clusters`. The root of the last two is `ecs_task_families`.
- Commands: `ecs` with the alias `clusters`, and `taskdefs` with the alias `td`. `:ecs NAME` moves the cursor to that cluster and opens its detail, because a focus in the list core opens the detail (decision D9). When the cluster is not loaded, the status bar says "No cluster named NAME in the loaded clusters".
- An icon `IconECS` (nf-md-docker, with a plain fallback character).

### The AWS layer

- `ECSService` with these reads:
  - `ListClustersPage(ctx, token)`: one `ListClusters` page and one `DescribeClusters` call with `Include=TAGS,SETTINGS,CONFIGURATIONS`.
  - `ListServicesPage(ctx, cluster, token)`: one `ListServices` page with `MaxResults=100`, then `DescribeServices` in batches of 10, one after the other, with `Include=TAGS`.
  - `ListTasksPage(ctx, cluster, service, stopped, token)`: one `ListTasks` page with `MaxResults=100`, then one `DescribeTasks` call with `Include=TAGS`. `service` is the service name, not its ARN. When it is empty, the call sends no `ServiceName`.
  - `DescribeTask(ctx, cluster, taskARN)`. It returns `aws.ErrTaskNotFound` when the task comes back as a `MISSING` failure.
  - `ListTaskFamiliesPage(ctx, token)` with `Status=ACTIVE`, and `ListTaskDefinitionsPage(ctx, family, token)` with `Sort=DESC`. Both return names or ARNs only, with no describe call.
  - `DescribeTaskDefinition(ctx, arn)` with `Include=TAGS`. It also takes a family name, for the latest active revision.
  - `DescribeServiceDeployment(ctx, arn)`: one ARN, for the detail.
- Each page method skips the describe call when the list page is empty (H1). It keys the results by the returned ARN, not by their order.
- A failure with the reason `MISSING` means that the resource went away between the two calls, and the row is dropped. Any other failure becomes a row with only the ARN, the name from the ARN, and the reason in an `Err` field, as for SQS (H2).
- Each call passes the cluster ARN, never an empty cluster. Task methods take the full task ARN, which also avoids the LocalStack quirk with bare IDs.
- Domain types `aws.Cluster`, `aws.ClusterService` (the interface has the name `ECSService`), `aws.Task`, `aws.Container`, `aws.TaskDefinition` and `aws.ServiceDeployment` keep the fields that the views show, with times as `time.Time`. `ParseTaskDefinition` and `TaskDefinitionLabel` read the family and the revision from an ARN. `Task.ID` is the last `/` segment of its ARN (`ARNName`), and `Task.ClusterARN` comes from `ClusterArn` (H4).
- `GuardECS` passes every read. PR 7 adds the three writes, and the guard refuses them in ReadOnly.

### The cluster list

- Columns: Name, Status, Services, Running, Pending, Instances. On a wide terminal, also Capacity providers. The counts sort as numbers.
- The list loads all pages, because an account has few clusters (decision D3).
- The row key is the ARN, and `FocusKey` is the name.
- Keys: enter opens the services, `t` opens all tasks of the cluster (from PR 3), `d` opens the detail, and `y` copies the ARN.
- The detail uses the data of the row, with no call. Info shows the name, the ARN, the status, the counts, Container Insights, the exec log configuration, the capacity providers and the default strategy. Tags is the second tab.

### The service list and detail

- `ecs_services` has the route parameter `cluster` (the ARN). Its view ID holds the cluster.
- Columns: Name, Status, Tasks (running/desired), Pending, Launch (the launch type, or the capacity provider names), Task definition (`family:revision`), Rollout (the rollout state of the `PRIMARY` deployment). On a wide terminal, also Created and Strategy.
  - The Tasks cell is `running/desired`. Its column style reads the two numbers: green when they are equal, yellow when not, and no color when the text does not parse.
  - The Rollout column has its own style: `COMPLETED` green, `IN_PROGRESS` yellow and `FAILED` red. A failed rollout shows only here, because a style sees only its own cell.
- A page budget of 10 pages (1,000 services), with `l` for more (decision D3). The fetch of a page has the list timeout of 2 minutes, because a throttle can slow its 11 calls (H11).
- Keys: enter opens the tasks of the service, `d` opens the detail, and `y` copies the ARN. In PR 2, before the task list exists, enter opens the detail.
- The detail calls `DescribeServiceDeployment` for `CurrentServiceDeployment` when it is set. A failure of that call shows on its line, and the panel still opens. The tabs:
  - Info: the name, the ARN, the status, the launch type or the capacity provider strategy, the platform version, the task definition (a link to its revision list), the desired, running and pending counts, the scheduling strategy, the deployment controller and strategy, the circuit breaker, exec on or off, Express Mode, the creation time and the creator.
  - Deployments: the current service deployment (status, lifecycle stage, reason, rollback, alarms), then each entry of `Service.Deployments` with its status, task definition, counts, failed tasks, rollout state and reason, and times (decision D5).
  - Events: the events, newest first, with the time and the message. The tab says that AWS keeps the 100 latest.
  - Network: the subnets and the security groups as links to their lists, public IP on or off, the load balancers (target group, container and port) and the service registries.
  - Tags.

### The task list and detail

- `ecs_tasks` has the route parameters `cluster` (the ARN) and `service` (`Service.ServiceName`, not the ARN that is the row key). A service name is unique only in its cluster, so both go together. An empty `service` lists the tasks of the whole cluster. Its view ID holds both.
- Columns: Task (the ID), Status (`LastStatus`), Health, Task definition, Started (the age), Launch (or the capacity provider), CPU, Memory. On a wide terminal, also Availability zone and Group.
- `x` switches between running and stopped tasks, and loads again (decision D4). In stopped mode:
  - The columns are Task, Stop code, Reason, Stopped, Task definition and Started.
  - The footer adds "AWS keeps stopped tasks for about an hour".
- The toggle needs a small change in the list core (H12). A new method takes a fetch, columns and a row function. It sets them, sets the columns for the width, clears the rows, the selection and the filter, and reloads. After that, `r` and `l` stay in the current mode with no other code. PR 3 tests that `x` changes the columns and empties the rows, that `r` in stopped mode reads stopped tasks, and that `x` again goes back.
- The Health column has its own style: `HEALTHY` green, `UNHEALTHY` red and `UNKNOWN` gray.
- A page budget of 10 pages (1,000 tasks), with `l` for more.
- An empty value shows as an empty cell, never as `0` or a zero time (H8).
- Keys: enter opens the containers, `d` opens the detail, and `y` copies the ARN. In PR 3, before the container list exists, enter opens the detail.
- The detail uses the data of the row, with no call. The tabs: Info (the ARN, the statuses, the stop code and reason, the times, the started-by, the group, the task definition as a link, the platform, the capacity provider, CPU, memory and ephemeral storage, exec on or off), Containers (one block for each container), Network (the ENI, its private IP and its subnet) and Tags.

### The container list and the jump to the logs

- `ecs_containers` has the route parameters `cluster` and `task` (the ARN). Its fetch calls `DescribeTask` and then `DescribeTaskDefinition` for the task definition of the task, and joins each container with its log configuration. Each `r` repeats both calls.
- When the task is gone (`aws.ErrTaskNotFound`), the list says "The task is gone. AWS keeps stopped tasks for about an hour."
- When the task definition call fails, the list still shows the containers of the task. The Logs column shows `?`, one toast shows the error, and enter shows the same error in place of the logs (H5).
- Columns: Name, Image, Status, Exit code, Health, Reason, Logs (the group, or the driver when it is not `awslogs`).
- Keys: enter opens the logs, `d` opens the detail (the image and digest, the runtime ID, the network bindings and interfaces, the managed agents, the log configuration), and `y` copies the container ARN.
- enter opens the event viewer with:
  - `log_group` from `awslogs-group`, and `log_stream` `<prefix>/<container>/<task ID>`.
  - The start is the earliest of `PullStartedAt`, `CreatedAt` and `StartedAt`, minus one minute.
  - When `LastStatus` is `STOPPED`, `log_start` is the start, and `log_end` is the first of `StoppedAt`, `ExecutionStoppedAt` and `StoppingAt`, plus one minute. When none is set, only `stream_first` is sent.
  - For any other status, `stream_first` is the start, so the viewer shows the last hour and can walk back to the start. The branch reads `LastStatus` only, because LocalStack sets `StoppedAt` on a running task.
  - `missing_hint`: "The task definition sends the logs to this group, but the group does not exist."
  - `missing_stream_hint`, a new parameter: "The task writes the stream when its container starts."
- When the container cannot have a stream, enter shows a toast and does not open the viewer (H5):
  - Not `awslogs`: "The container sends its logs with DRIVER, not to CloudWatch Logs."
  - Another Region: "The logs are in REGION. Press R to switch the Region."
- No prefix (EC2 only): enter opens the whole group, with `log_group` and the same range but no `log_stream`, as the Lambda jump does. A toast says that each stream has the Docker container ID as its name.
- The logs AWS layer maps `ResourceNotFoundException` of `GetLogEvents` to a new `aws.ErrLogStreamNotFound`. AWS gives the same exception for a missing group, so the viewer checks it only after its group lookup passed. The viewer then shows "No log stream named NAME in GROUP" and `missing_stream_hint`, in place of a plain error (H6). `missing_hint` stays a hint for a missing group, so the Lambda jump keeps its text.

### Task definitions

- `ecs_task_families` lists the active families, with a page budget of 10 pages. enter opens `ecs_task_definitions` for that family. `d` opens the detail of the latest active revision, because `DescribeTaskDefinition` takes a family name.
- `ecs_task_definitions` lists the revisions, newest first, with the columns Revision and ARN. The list call returns ARNs only, and every listed revision is active (H13, decision D7). A page budget of 4 pages, with `l` for more. `d` opens the detail with `DescribeTaskDefinition`, which shows the status, the time, CPU and memory.
- The detail tabs: Info (the family and revision, the roles, the network mode, CPU and memory, the platform, the volumes), Containers (the image, CPU and memory, the ports, the command, the log configuration, the secrets as names and sources), Environment (the values hidden as `••••`, with `$` to show them, as for Lambda) and Tags.
- A link from a service or a task opens `ecs_task_definitions` for that family, moves the cursor to that revision and opens its detail.
- When the linked revision is not in the list, because it is inactive or past the page budget, `FocusMissing` calls `DescribeTaskDefinition` for the ARN and opens its detail, as the AMI list does (H13). A failed call shows a toast.
- No diff of two revisions in this phase (decision D7).

### Exec

- `o` on a container, or on a task, starts `aws ecs execute-command --cluster C --task T --container N --interactive --command /bin/sh`, with `--region`, `--profile` and `--endpoint-url` when they are set (decision D6). It runs through `CLIExec` and `tea.Exec`, as SSM does. The command and the names are separate arguments, with no shell.
- On a task with more than one container, `o` asks for the container with a picker.
- Checks before the session, each with its own toast:
  - The task is `RUNNING`.
  - `Task.EnableExecuteCommand` is true. Else: "Exec is off for this task. Turn it on for the service, then start new tasks."
  - The `ExecuteCommandAgent` agent of the container is `RUNNING`, when the task reports agents. Else the toast shows its status and reason.
  - The AWS CLI and `session-manager-plugin` are installed, with the texts of EC2.
- `o` has `ModeReadWrite`, as for EC2, so the app blocks it in ReadOnly.
- `SSMSessionCmd` of EC2 also gets `--endpoint-url` (H7).

### Write actions

All three need ReadWrite. Each view checks ReadOnly again when the reply comes, as the other views do.

- `f` on a service forces a new deployment, after a y/n prompt that says that every task is replaced. It is refused for a `CODE_DEPLOY` or `EXTERNAL` service (H9).
- `c` on a service opens a form for the desired count, with the running and desired counts in the description. The value must be a whole number from 0 up. For an Express Mode service, the form says that Express Mode manages the service.
- `X` on a task, or on the selected tasks, stops them after the typed phrase `stop N tasks` (decision D8). The confirm prompt has no input field, so the reason is fixed: "Stopped by lazycloud". The tag of the prompt holds the task ARNs. The result has the form of the other bulk actions: "Stopped 2 of 3 tasks. Failed: ...".
- After an action, the row loads again: the service after `f` and `c`, the tasks after `X`.

### Seed and tests

- `cmd/seed/ecs.go` makes the cluster `prod-cluster` with tags, and these services, with `awsvpc` on a default VPC subnet that it looks up:
  - `web`: `nginx:alpine` (multi-arch), desired count 1, exec on, `awslogs` to its own group `/ecs/web-app` with `awslogs-create-group=true` and the prefix `ecs`.
  - `worker` and `scheduler`: desired count 0, with two revisions of their task definitions. `worker` logs to `/ecs/worker`, and `scheduler` has no log configuration.
- One task of `web`'s task definition runs with `RunTask` and stops with `StopTask` and a reason, so the stopped list of the cluster has a row with a stop code. A task from `RunTask` is not part of a service, so `ListTasks` with the name of the service does not return it (found in PR 3).
- The tiers add services with a desired count of 0 in their own cluster, `bulk-cluster`: 30 (medium), 120 (large) and 1,100 (enterprise). They share one task definition family and start no container.
- The seeder never seeds a service that crashes, because LocalStack starts a new container every few seconds for it. It is idempotent, as the other seeders are. It makes its own log group, because the seeders run in parallel.
- Unit tests in `internal/aws` use an `httptest` fake keyed by `X-Amz-Target`: paging, the batches of 10 and 100, no describe call for an empty page (also for clusters), results out of order, `MISSING` and other failures, both ARN formats, the parse of a task definition ARN, the `Include` values, and the `ServiceName` of `ListTasks` (the bare name, or no field). LocalStack cannot catch a wrong `ServiceName`.
- Unit tests in `internal/views` cover each list, its keys, the detail tabs, the style of the Tasks, Rollout and Health columns, the stopped toggle, the empty values, the log jump in each case (running, stopping, stopped with and without a stop time, no prefix, another driver, another Region, a failed task definition call), the link to a revision that is not loaded, the exec checks and argv, and the write actions with ReadOnly at the reply.
- The views join the conformance and render safety tables, the ReadOnly key table, the filter test and `TestRoutes`, each in the PR that adds the view.
- LocalStack smoke tests: `TestListsLoad` with `prod-cluster` (PR 2) and with the family of `web` for `taskdefs` (PR 5), the services and the detail of `web` (PR 2), a stopped task in the task list of the cluster (PR 3, with a task that the test stops itself, because LocalStack forgets a stopped task after a while), and enter on the container of a task of `web` to its log stream (PR 4, with a task that the test runs itself, because the lines of the seeded task can be older than the first hour of the viewer). Exec, the deployment API and paging cannot run on LocalStack (H10).

### What does not change

- No metrics, no auto scaling, no container instances, no daemons and no port forwarding in this phase (decision D2).
- The event viewer keeps its behavior when no stream is missing.
- The app still starts in ReadOnly.

## Visible changes

- The home screen shows ECS with Clusters and Task definitions. `:ecs` opens the clusters, and `:ecs NAME` puts the cursor on a cluster and opens its detail.
- enter goes from a cluster to its services, to their tasks, to their containers, and to the log stream of a container. `x` shows the stopped tasks with their stop code and reason.
- `o` opens a shell in a container. `f`, `c` and `X` force a deployment, change the desired count and stop tasks in ReadWrite.
- The ECS states, the health values and the rollout states get colors. The states of other lists keep their colors.
- An SSM session of EC2 against LocalStack stays on LocalStack.

## Plan

Each PR keeps `go test ./...` green and runs golangci-lint with gosec.

- PR 0. This design, and pin tests: `TestECSHazard_*` pins H3 (no color for the five lifecycle states), H6 (the event viewer shows a plain error for a missing stream, and `GetStreamEvents` has no sentinel for it), and H7 (`SSMSessionCmd` has no `--endpoint-url`).
- PR 1. The AWS layer and the seed: the `ecs` module, `ECSService` with its reads, the batches and failures, the ARN helpers, `GuardECS`, `ECSClient`, the mock, the `httptest` fake, and the ECS seeder.
- PR 2. The cluster list and the service list with its detail, the registry entry with the Clusters feature only, the `ecs` command with `:ecs NAME`, the routes of both views, the icon, the new state colors, the Tasks and Rollout styles, the smoke cases, `services/aws/ecs.md` and the docs. H3 flips. `t` on a cluster is not bound yet, and enter on a service opens its detail.
- PR 3. The task list with the stopped toggle and its change in the list core, the task detail, the Health style, the route, and the smoke case for stopped tasks. `t` on a cluster and enter on a service open the task list. enter on a task opens its detail.
- PR 4. The container list, the jump to the logs, `aws.ErrLogStreamNotFound`, `missing_stream_hint` and the missing stream text of the event viewer, the route, and the smoke case for the logs. enter on a task opens the container list. H6 flips.
- PR 5. The task definition lists and detail, the links from the service and task details, the Task definitions feature, the `taskdefs` command, the routes, and the `taskdefs` smoke case.
- PR 6. Exec on `o`, and the endpoint for SSM. H7 flips.
- PR 7. The write actions. The tracker marks item 3 of the roadmap priority done.

Each PR needs the one before it.

## Decisions

- D1. The scope. Proposed: issues #129 and #130 in one plan, with the reads first and exec and the write actions in PR 6 and PR 7. Other answer: only #129 now, and #130 in a later design.
- D2. What stays out. Proposed: metrics, auto scaling, container instances, managed daemons, Service Connect details, port forwarding and the file transfer of e1s. Other answer: add container instances, because #129 shows their count.
- D3. The list budgets. Proposed: all clusters, then 10 pages each for services, tasks and families, and 4 pages for revisions, with `l` for more. A page of services costs 11 calls, so 10 pages cost 110 calls: the burst of 100, and half a second of refill. A throttle only slows the load, because the SDK retries it. Other answer: 20 pages of services, as for Lambda functions.
- D4. Stopped tasks. Proposed: `x` switches the list between running and stopped tasks in place, because `ListTasks` takes one status a call. This needs a small change in the list core (H12). Other answers: (a) a `stopped` route parameter, and `x` opens a new view, so each view keeps one configuration. (b) One list with both, which needs two walks in one token.
- D5. Deployments. Proposed: the Deployments tab shows `Service.Deployments` and the current service deployment, with one `DescribeServiceDeployment` call. Other answers: (a) `Service.Deployments` only, with no extra call. (b) A list view of all service deployments, as e1s has on `p`.
- D6. How exec runs. Proposed: through the AWS CLI, as SSM on EC2 does, so it needs the AWS CLI and the plugin. Other answer: call `ExecuteCommand` with the SDK and start `session-manager-plugin` directly, which needs no AWS CLI but needs the runtime ID and more code to test.
- D7. Task definitions. Proposed: families, revisions and the detail, with hidden environment values. The revision list shows the revision and the ARN only, with no describe call for each row. No diff and no register. Other answers: (a) describe each revision of a page (20 a page, about 5 at a time), for CPU, memory and time columns, at 21 calls a page in a bucket with a burst of 50. (b) Add a diff of two revisions.
- D8. The write actions. Proposed: `f` force deploy (y/n), `c` desired count (form), `X` stop tasks (typed phrase, with the fixed reason "Stopped by lazycloud"). Other answers: (a) a form with a phrase field and an optional reason field, with the ARNs kept in a field of the view. (b) One `m` manage picker, as EC2 has.
- D9. The commands. Proposed: `ecs` (alias `clusters`) and `taskdefs` (alias `td`). `:ecs NAME` puts the cursor on a cluster and opens its detail. Other answer: `:ecs CLUSTER/SERVICE` opens the tasks of a service at once.

## Open questions

- Q1. Does the service list show the services of all clusters? Proposed answer: no. A cluster is the first level, as in the console and in e1s.
- Q2. Does the task list refresh by itself during a deployment? Proposed answer: no. `r` loads again, as in the other lists.
- Q3. Does a session switch from a service list go back to the services? Proposed answer: no. It goes to Clusters, the root, as the Lambda child lists go to Functions.
- Q4. Does the container list open the logs when a task has one container, with no list? Proposed answer: no. The list shows the status and the exit code, which matter for a stopped task, and enter on it is one more key.
- Q5. Does exec take a command other than `/bin/sh`? Proposed answer: not in this phase. A form for the command can come later.
