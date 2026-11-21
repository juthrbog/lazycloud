# ECS

Browse ECS clusters, their services and their tasks, with the stopped tasks and their stop reasons. Every call reads, so ReadOnly mode allows all of them. The design is in `docs/design/ecs.md`. Containers, logs, task definitions, exec and the write actions come in later PRs of that design.

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
| `y` | Copy the ARN of the service |
| `l` | Load more services |
| `/` | Filter the loaded services |
| `s`, `S` | Sort |
| `r` | Load the list again from the first page |

### Service Detail (side panel)

`d` opens a panel with these tabs:

1. Info: the name, the ARN, the status, the task counts, the launch type or the capacity provider strategy, the platform version, the task definition, the scheduling strategy, the deployment controller, the deployment strategy with its percentages, the circuit breaker, exec on or off, Express Mode, the creation time and the creator.
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
| `enter`, `d` | Open the detail of the task. In a later PR, `enter` opens the containers |
| `x` | Switch between the running and the stopped tasks |
| `y` | Copy the ARN of the task |
| `l` | Load more tasks |
| `/` | Filter the loaded tasks |
| `s`, `S` | Sort |
| `r` | Load the list again from the first page |

### Task Detail (side panel)

`d` opens a panel from the data of the row, with no call:

1. Info: the ID, the ARN, the status (and the desired status when it differs), the health, the stop code and reason, the task definition, the launch type or capacity provider, the platform, CPU, memory, ephemeral storage, the zone, the group, the started-by, the connectivity, exec on or off, and the times of the task.
2. Containers: the image, the status, the health, the exit code and the reason of each container.
3. Network: the network interface, its private IP and its subnet, which is a link to the subnet list.
4. Tags: the tags of the task, when it has tags.

