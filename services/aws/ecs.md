# ECS

Browse ECS clusters, their services, their tasks and the containers of each task, with the stopped tasks and their stop reasons. enter on a container opens its log stream. Browse the task definitions by family and revision. Open a shell in a container with ECS Exec. Force a new deployment of a service, set its desired count, and stop tasks. ReadOnly mode allows the reads, and blocks exec and the write actions. The design is in `docs/design/ecs.md`.

## Views

### Clusters

Open the list from the ECS tile on the home screen, or with `:ecs` (alias `:clusters`). `:ecs NAME` opens the list with the cursor on that cluster, and opens its detail. For a name that is not in the list, the status bar says that the loaded clusters have no cluster with that name.

| Column | Description |
|--------|-------------|
| Name | The name of the cluster |
| Status | `ACTIVE`, `PROVISIONING`, `DEPROVISIONING`, `FAILED` or `INACTIVE` |
| Services | The active services |
| Running | The running tasks |
| Pending | The pending tasks, in the warning color when not 0 |
| Instances | The registered container instances (wide terminals) |
| Capacity providers | The capacity providers of the cluster (wide terminals) |

A terminal of 80 to 119 columns leaves out Instances and Capacity providers. A narrow terminal shows only the name, the status and the running tasks. The counts sort as numbers.

The list loads every page, because an account has few clusters. Each page is one `ListClusters` call and one `DescribeClusters` call. When AWS cannot describe a cluster, its row shows `?`, and a message names the cluster and the reason.

| Key | Action |
|-----|--------|
| `enter` | Open the services of the cluster |
| `t` | Open every task of the cluster |
| `d` | Open the detail of the cluster |
| `y` | Copy the ARN of the cluster |
| `/` | Filter the clusters |
| `s`, `S` | Sort |
| `r` | Load the list again |

### Cluster Detail (side panel)

`d` opens a panel from the data of the row, with no call:

1. Info: the name, the ARN, the status, the counts, the Container Insights setting, the exec logging with its log group or S3 bucket, the KMS key of exec, the capacity providers and the default capacity provider strategy.
2. Tags: the tags of the cluster, when it has tags.

### Services

`enter` on a cluster opens its services.

| Column | Description |
|--------|-------------|
| Name | The name of the service |
| Status | `ACTIVE`, `DRAINING` or `INACTIVE` |
| Tasks | The running and the desired tasks, for example `2/3`. Green when they are equal, yellow when not |
| Pending | The pending tasks (wide terminals) |
| Launch | The launch type, or the capacity providers when the service uses a strategy |
| Task definition | The family and the revision, for example `web:7` |
| Rollout | The rollout state of the primary deployment: `COMPLETED` in green, `IN_PROGRESS` in yellow, `FAILED` in red |
| Strategy | `ROLLING`, `BLUE_GREEN`, `LINEAR` or `CANARY` (wide terminals) |
| Created | The date the service was made (wide terminals) |

A terminal of 80 to 119 columns leaves out Pending, Strategy and Created: Tasks already shows the gap between the running and the desired tasks. A narrow terminal shows only the name, the tasks and the rollout. Tasks sorts by the running count.

Each page is one `ListServices` call of up to 100 services and up to 10 `DescribeServices` calls, because one call describes at most 10 services. The list loads at most 10 pages (1,000 services). When more services exist, the footer shows `l for more`, and `l` loads the next 1,000. When AWS cannot describe a service, its row shows `?`.

| Key | Action |
|-----|--------|
| `enter` | Open the tasks of the service |
| `d` | Open the detail of the service |
| `f` | Force a new deployment of the service (see [Write actions](#write-actions)). ReadWrite mode only |
| `c` | Set the desired count of the service (see [Write actions](#write-actions)). ReadWrite mode only |
| `y` | Copy the ARN of the service |
| `l` | Load more services |
| `/` | Filter the loaded services |
| `s`, `S` | Sort |
| `r` | Load the list again from the first page |

### Service Detail (side panel)

`d` opens a panel with these tabs:

1. Info: the name, the ARN, the status, the task counts, the launch type or the capacity provider strategy, the platform version, the task definition (a link to its revision), the scheduling strategy, the deployment controller, the deployment strategy with its percentages, the circuit breaker, exec on or off, Express Mode, the creation time and the creator.
2. Deployments: the current service deployment, with its status, lifecycle stage, target revision, rollback, circuit breaker and alarms. Then each deployment of the service, with its task definition, counts, failed tasks, rollout state and times.
3. Events: the events of the service, newest first. AWS keeps the 100 latest events.
4. Network: the subnets and the security groups, each a link to its list, the public IP setting, the load balancers and the service registries.
5. Tags: the tags of the service, when it has tags.

The current service deployment needs one `DescribeServiceDeployments` call. When it fails, its line shows the error, and the panel still opens. LocalStack does not support this call.

### Tasks

`enter` on a service opens its tasks. `t` on a cluster opens every task of the cluster. A task that `RunTask` started is not part of a service, so only the list of the cluster shows it.

| Column | Description |
|--------|-------------|
| Task | The ID of the task, from its ARN |
| Status | The last status, for example `PROVISIONING`, `PENDING`, `RUNNING` or `STOPPING` |
| Health | `HEALTHY` in green, `UNHEALTHY` in red, `UNKNOWN` in gray. A task without a health check is `UNKNOWN` |
| Task def | The family and the revision |
| Started | How long ago the task started, for example `3h12m` |
| Launch | The capacity provider, or the launch type |
| CPU | The CPU units (wide terminals) |
| Memory | The memory in MiB (wide terminals) |
| Zone | The Availability Zone (wide terminals) |
| Group | The task group, for example `service:web` (wide terminals) |

A narrow terminal shows only the task, the status and the start. Started sorts by the time. An empty value, for example the start of a task that did not start, shows as an empty cell.

Each page is one `ListTasks` call of up to 100 tasks and one `DescribeTasks` call. The list loads at most 10 pages (1,000 tasks), with `l` for more.

`x` switches the list to the stopped tasks, and `x` again goes back to the running tasks. AWS keeps a stopped task for about an hour, and the footer says so. The stopped list has its own columns, so the reason shows without the detail:

| Column | Description |
|--------|-------------|
| Task | The ID of the task |
| Stop code | For example `EssentialContainerExited`, `TaskFailedToStart` or `UserInitiated` |
| Reason | The stop reason, for example `CannotPullContainerError: ...` |
| Stopped | How long ago the task stopped |
| Task def | The family and the revision |
| Started | How long ago the task started (wide terminals) |

A terminal of 80 to 119 columns leaves out Started. A narrow terminal shows only the task, the reason and the stop time. Stopped sorts by the time.

| Key | Action |
|-----|--------|
| `enter` | Open the containers of the task |
| `d` | Open the detail of the task |
| `x` | Switch between the running and the stopped tasks |
| `o` | Open a shell in a container of the task (see [Exec](#exec)). ReadWrite mode only |
| `X` | Stop the selected tasks, or the task under the cursor (see [Write actions](#write-actions)). ReadWrite mode only |
| `space` | Select the task, for `X` and `y` |
| `y` | Copy the ARN of the task, or the ARNs of the selected tasks |
| `l` | Load more tasks |
| `/` | Filter the loaded tasks |
| `s`, `S` | Sort |
| `r` | Load the list again from the first page |

### Task Detail (side panel)

`d` opens a panel from the data of the row, with no call:

1. Info: the ID, the ARN, the status (and the desired status when it differs), the health, the stop code and reason, the task definition (a link to its revision), the launch type or capacity provider, the platform, CPU, memory, ephemeral storage, the zone, the group, the started-by, the connectivity, exec on or off, and the times of the task.
2. Containers: the image, the status, the health, the exit code and the reason of each container.
3. Network: the network interface, its private IP and its subnet, which is a link to the subnet list.
4. Tags: the tags of the task, when it has tags.

### Containers

`enter` on a task opens its containers. The list reads the task with `DescribeTasks`, and then its task definition with `DescribeTaskDefinition`, because the log configuration is in the task definition. `r` repeats both calls.

| Column | Description |
|--------|-------------|
| Name | The name of the container |
| Image | The image |
| Status | `PENDING`, `RUNNING` or `STOPPED` |
| Exit | The exit code, when the container exited |
| Health | `HEALTHY` in green, `UNHEALTHY` in red, `UNKNOWN` in gray |
| Reason | Why the container stopped (wide terminals) |
| Logs | The log group, with its Region when the group is in another Region. The log driver when it is not `awslogs` |

A terminal of 80 to 119 columns leaves out Reason. A narrow terminal shows only the name, the status and the logs.

When AWS no longer knows the task, the list says "The task is gone. AWS keeps stopped tasks for about an hour." When the task definition call fails, the list still shows the containers. The Logs column then shows `?`, and a message shows the error.

| Key | Action |
|-----|--------|
| `enter` | Open the log stream of the container |
| `d` | Open the detail of the container |
| `o` | Open a shell in the container (see [Exec](#exec)). ReadWrite mode only |
| `y` | Copy the ARN of the container |
| `/` | Filter the containers |
| `s`, `S` | Sort |
| `r` | Load the list again |

### The log stream of a container

`enter` on a container opens its stream in the log event viewer. The stream is `PREFIX/CONTAINER/TASK-ID`, from the `awslogs` options of the task definition. The time range starts one minute before the task was made or pulled its image:

- For a stopped task, the range ends one minute after the task stopped.
- For a task that has not stopped, the viewer shows the last hour, and `l` walks back to the start of the task.

When the group does not exist, the viewer says that the task definition sends the logs to a group that does not exist. When the stream does not exist yet, the viewer says that the task writes the stream when its container starts.

In these cases, `enter` shows a message and does not open the viewer:

- The container sends its logs with another driver, for example `awsfirelens` or `splunk`.
- The log group is in another Region. Press `R` to switch the Region.
- The container has no log configuration, or the configuration names no group.
- The task definition call failed.

Without a stream prefix, which only the EC2 launch type allows, each stream has the Docker container ID as its name. `enter` then opens the whole group, with the same time range, and a message says why.

### Container Detail (side panel)

`d` opens a panel from the data of the row, with no call:

1. Info: the name, the ARN, the image and its digest, the runtime ID, the status, the health, the exit code, the reason, CPU and memory.
2. Network: the private IPs and the port bindings.
3. Agents: the managed agents, for example the `ExecuteCommandAgent` of ECS Exec, when the container has agents.
4. Logs: the log driver, the stream and the options of the log configuration.

### Exec

`o` on a container, or on a task, opens `/bin/sh` in the container with ECS Exec. This suspends the TUI, opens an interactive terminal session, and restores the TUI when you exit. On a task with more than one container, `o` asks for the container first.

A shell can change the container, so ReadOnly mode blocks `o`. Press `W` to switch to ReadWrite mode first.

LazyCloud runs `aws ecs execute-command --cluster=CLUSTER --task=TASK --container=NAME --interactive --command=/bin/sh` directly, without a shell. It adds `--region` and `--profile` of the session, and `--endpoint-url` when the app uses an endpoint. Each name is in the argument of its flag, so a name that starts with `-` is not read as a flag. The banner shows the names as plain text.

Before the session, the app checks these, and shows a message when one fails:

- The task is `RUNNING`, and the container is `RUNNING`.
- Exec is on for the task. Exec cannot be turned on for a task that runs already: turn it on for the service, then start new tasks.
- The `ExecuteCommandAgent` of the container is `RUNNING`, when the task reports its agents.
- The AWS CLI and the [Session Manager plugin](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html) are installed.

Type `exit` or press Ctrl+D to end the session and go back to LazyCloud. In the session, Ctrl+C goes to the shell in the container. Ctrl+C before the session starts cancels it without an error. When the CLI fails, its error stays on the screen until you press Enter, and then a message shows the exit code. LocalStack does not support ECS Exec.

### Write actions

Each action needs ReadWrite mode. Press `W` to switch. When ReadOnly mode is on at the time you confirm, the action does not run. After an action, the list loads again.

- `f` on a service forces a new deployment after a y/n prompt. ECS replaces every task of the service with a new one, from the same task definition. A service with the `CODE_DEPLOY` or `EXTERNAL` deployment controller does not take a forced deployment from ECS, so the app refuses it with a message.
- `c` on a service opens a form for the desired count. The form shows the running and the desired counts. The value must be a whole number from 0 up. A new desired count starts no deployment. For a service that Express Mode manages, the form says that Express Mode can change the count.
- `X` on a task, or on the selected tasks, stops them after you type `stop N tasks` (`stop 1 task` for one). The stop reason is always "Stopped by lazycloud". A service starts new tasks in place of the stopped ones. When some tasks fail, the message says how many stopped and names the failed ones, for example "Stopped 2 of 3 task(s). Failed: bbb (AccessDeniedException)". The stopped list has no `X`.

### Task Definition Families

Open the list from the Task definitions tile of ECS on the home screen, or with `:taskdefs` (alias `:td`). The list shows the active families, one column with the name. It loads at most 10 pages (1,000 families), with `l` for more.

| Key | Action |
|-----|--------|
| `enter` | Open the revisions of the family |
| `d` | Open the detail of the latest active revision |
| `$` | Open the detail of the latest active revision, with the environment values shown |
| `y` | Copy the name of the family |
| `l` | Load more families |
| `/` | Filter the loaded families |
| `s`, `S` | Sort |
| `r` | Load the list again from the first page |

### Revisions

`enter` on a family opens its active revisions, newest first, with the revision number and the ARN. `ListTaskDefinitions` returns only the ARNs, so the list has no other columns. The Revision column sorts as a number. The list loads at most 4 pages (400 revisions), with `l` for more.

The Task def line in the detail of a service or a task is a link. It opens the revisions of the family, with the cursor on that revision, and opens its detail. When the revision is not in the list, because it is inactive or past the loaded pages, its detail still opens. When AWS cannot read it, a message says why.

| Key | Action |
|-----|--------|
| `enter`, `d` | Open the detail of the revision |
| `$` | Open the detail with the environment values shown |
| `y` | Copy the ARN of the revision |
| `l` | Load more revisions |
| `/` | Filter the loaded revisions |
| `s`, `S` | Sort |
| `r` | Load the list again from the first page |

### Task Definition Detail (side panel)

`d` opens a panel with one `DescribeTaskDefinition` call:

1. Info: the family, the revision, the ARN, the status, the registration and deregistration times, the registrant, CPU, memory, the network mode, the required and compatible launch types, the platform, the ephemeral storage, the task role, the execution role and the volumes.
2. Containers: for each container, the image, essential or not, CPU and memory, the entry point and the command, the environment files, the ports (or port ranges), the log configuration, and the secrets as names and sources. The panel never shows the value of a secret.
3. Environment: the environment variables of each container. The values show as `••••`. `$` opens the panel with the values shown, and with this tab first.
4. Tags: the tags of the revision, when it has tags.
