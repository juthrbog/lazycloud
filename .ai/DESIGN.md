# LazyCloud

## Project Overview

LazyCloud is a terminal user interface (TUI) for interacting with AWS services, inspired by [lazygit](https://github.com/jesseduffield/lazygit) and [k9s](https://github.com/derailed/k9s). It is AWS-only — no other cloud providers will be supported.

- **Language:** Go
- **TUI Framework:** [Bubble Tea v2](https://github.com/charmbracelet/bubbletea) — import as `charm.land/bubbletea/v2`
- **Styling:** [Lipgloss v2](https://github.com/charmbracelet/lipgloss) — import as `charm.land/lipgloss/v2`
- **Reusable Components:** [Bubbles v2](https://github.com/charmbracelet/bubbles) — import as `charm.land/bubbles/v2`
- **License:** Apache 2.0 (same as k9s)
- **Repository Name:** `lazycloud`

## Architecture

### The Elm Architecture (Model-View-Update)

Bubble Tea uses The Elm Architecture. Every component (model) implements three methods:

- **Init() → tea.Cmd** — returns an initial command to run (e.g., fetch data)
- **Update(tea.Msg) → (tea.Model, tea.Cmd)** — handles messages (keypresses, API responses, etc.), returns updated model and optional command
- **View() → tea.View** — renders the current state as a `tea.View` struct for the terminal

In v2, `View()` returns a `tea.View` struct instead of a string. Use `tea.NewView(s)` to create one from a string. The `tea.View` struct also controls terminal settings like alt screen mode and mouse mode:

```go
func (m model) View() tea.View {
    v := tea.NewView("Hello, world!")
    v.AltScreen = true
    return v
}
```

**Critical rules:**

- Never do expensive work (API calls, I/O) inside `Update()` or `View()`. Offload to `tea.Cmd` functions that run in goroutines and return messages.
- Never use goroutines directly to modify model state. Always send messages through the Bubble Tea event loop.
- All state changes happen in `Update()` and are returned as the new model.
- Only the root model's `View()` should set `AltScreen = true`. Child views' AltScreen settings are ignored since only the top-level return matters to Bubble Tea.
- Child UI components (bubbles table, spinner, etc.) still have `View() string` methods — compose their output into the parent's `tea.NewView()` call.

### Tree of Models

The app uses a tree of models pattern. The root model acts as a message router and screen compositor. Child models represent individual views (e.g., EC2 list, S3 bucket browser). Each child model implements `Init()`, `Update()`, and `View()`.

The root model is responsible for:

1. Handling global keybindings (quit, help, profile/region switching)
2. Routing messages to the currently active child model
3. Broadcasting messages that all children need (e.g., `tea.WindowSizeMsg`). The root extracts child content via the `Content` field on `tea.View` for layout composition.
4. Composing the final layout (header + active view + status bar)

### Navigator (Model Stack)

Navigation uses a stack-based approach via the `nav.Navigator` struct:

- Navigating into a resource pushes a new model onto the stack
- Pressing `Esc` or backspace pops the current model, returning to the previous view
- The top of the stack is the "current" model that receives input and is rendered
- Models are created dynamically on demand and cached by `ID()` so that going back and forward doesn't re-fetch data unnecessarily
- `ClearCache()` invalidates cached views when profile or region changes

All navigable views implement the `nav.View` interface:

```go
type View interface {
    tea.Model
    ID() string    // unique identifier for caching, e.g. "ec2_list"
    Title() string // human-readable title for breadcrumb display
}
```

### Layer Separation

The codebase has strict separation between three layers:

1. **AWS Layer** (`internal/aws/`) — Pure AWS SDK calls. No Bubble Tea imports. Returns Go structs. Knows nothing about the UI.
2. **View Layer** (`internal/views/`) — Bubble Tea models for each screen. Calls into the AWS layer via `tea.Cmd`. Handles user input and rendering.
3. **UI Component Layer** (`internal/ui/`) — Reusable TUI components (tables, detail panes, filter inputs, status bar). Not tied to any specific AWS service.

## Project Structure

```
lazycloud/
├── main.go                        # Entry point, creates tea.Program
├── internal/
│   ├── app/
│   │   └── app.go                 # Root model — message router + layout compositor
│   │
│   ├── nav/
│   │   └── navigator.go           # Model stack with push/pop/cache
│   │
│   ├── aws/                       # AWS service layer (NO Bubble Tea imports)
│   │   ├── client.go              # Shared AWS config/session setup
│   │   ├── ec2.go                 # EC2 API calls (instances, security groups, etc.)
│   │   ├── s3.go                  # S3 API calls (buckets, objects)
│   │   ├── lambda.go              # Lambda API calls
│   │   ├── ecs.go                 # ECS API calls (clusters, services, tasks)
│   │   ├── iam.go                 # IAM API calls (users, roles, policies)
│   │   ├── cloudwatch.go          # CloudWatch logs/metrics
│   │   └── rds.go                 # RDS instances
│   │
│   ├── ui/                        # Reusable TUI components
│   │   ├── table.go               # Generic resource table (sortable, filterable)
│   │   ├── detail.go              # Detail/preview pane (key-value display)
│   │   ├── header.go              # Top bar: profile, region, breadcrumb
│   │   ├── statusbar.go           # Bottom bar: contextual keybindings, errors
│   │   ├── filter.go              # Fuzzy filter/search input
│   │   ├── confirm.go             # Confirmation dialog for destructive actions
│   │   ├── spinner.go             # Loading indicator
│   │   └── styles.go              # Lipgloss style definitions
│   │
│   ├── views/                     # Service-specific views (each is a Bubble Tea model)
│   │   ├── home.go                # Service selector / dashboard
│   │   ├── ec2_list.go            # EC2 instances list view
│   │   ├── ec2_detail.go          # Single EC2 instance detail view
│   │   ├── s3_list.go             # S3 buckets list view
│   │   ├── s3_objects.go          # Objects within a bucket
│   │   ├── lambda_list.go         # Lambda functions list view
│   │   ├── lambda_detail.go       # Single Lambda function detail
│   │   ├── ecs_clusters.go        # ECS clusters list
│   │   ├── ecs_services.go        # ECS services within a cluster
│   │   ├── ecs_tasks.go           # ECS tasks within a service
│   │   ├── iam_users.go           # IAM users list
│   │   ├── iam_roles.go           # IAM roles list
│   │   ├── rds_list.go            # RDS instances list
│   │   └── cloudwatch_logs.go     # CloudWatch log groups/streams
│   │
│   ├── msg/
│   │   └── messages.go            # Shared message types
│   │
│   └── config/
│       └── config.go              # AWS profile/region selection, app preferences
│
├── go.mod
├── go.sum
├── LICENSE                        # Apache 2.0
└── README.md
```

## Shared Message Types (`internal/msg/messages.go`)

Define common messages used across views:

```go
package msg

// ResourcesLoadedMsg is sent when an AWS API call completes successfully.
type ResourcesLoadedMsg[T any] struct {
    Resources []T
}

// ErrorMsg is sent when an AWS API call fails.
type ErrorMsg struct {
    Err     error
    Context string // e.g., "fetching EC2 instances"
}

// LoadingMsg indicates a resource is being fetched.
type LoadingMsg struct {
    Resource string // e.g., "EC2 Instances"
}

// RefreshMsg triggers a reload of the current view's data.
type RefreshMsg struct{}

// NavigateMsg tells the root model to push a new view.
type NavigateMsg struct {
    ViewID string // view identifier
    Params map[string]string // e.g., {"instanceId": "i-abc123"}
}

// NavigateBackMsg tells the root model to pop the current view.
type NavigateBackMsg struct{}

// ProfileChangedMsg is broadcast when the AWS profile changes.
type ProfileChangedMsg struct {
    Profile string
}

// RegionChangedMsg is broadcast when the AWS region changes.
type RegionChangedMsg struct {
    Region string
}
```

## AWS Client Pattern (`internal/aws/client.go`)

The Client struct includes LocalStack support via an optional endpoint override. See the "AWS Client with LocalStack Support" section below for the full implementation.

Each service file (e.g., `ec2.go`, `s3.go`) accepts a `*Client` and returns plain Go structs. The `ServiceEndpoint()` helper centralizes endpoint override logic:

```go
package aws

import (
    "context"
    "github.com/aws/aws-sdk-go-v2/service/ec2"
)

type Instance struct {
    ID         string
    Name       string
    State      string
    Type       string
    PrivateIP  string
    PublicIP   string
    LaunchTime string
}

func ListInstances(ctx context.Context, client *Client) ([]Instance, error) {
    svc := ec2.NewFromConfig(client.Config)
    output, err := svc.DescribeInstances(ctx, &ec2.DescribeInstancesInput{})
    if err != nil {
        return nil, err
    }
    // Transform SDK response into []Instance
    // ...
    return instances, nil
}
```

## View Pattern

Each view implements both `tea.Model` and the `nav.View` interface (which adds `ID()` and `Title()` for navigation/caching). `Update()` must return `(tea.Model, tea.Cmd)` — not the concrete type — and `View()` returns `tea.View`.

Example for EC2 instances list:

```go
package views

import (
    "github.com/juthrbog/lazycloud/internal/aws"
    "github.com/juthrbog/lazycloud/internal/msg"
    "github.com/juthrbog/lazycloud/internal/ui"
    tea "charm.land/bubbletea/v2"
)

type EC2ListView struct {
    client    *aws.Client
    table     ui.Table
    instances []aws.Instance
    loading   bool
    err       error
    width     int
    height    int
}

// nav.View interface
func (v *EC2ListView) ID() string    { return "ec2_list" }
func (v *EC2ListView) Title() string { return "EC2 Instances" }

func NewEC2ListView(client *aws.Client) *EC2ListView {
    return &EC2ListView{
        client:  client,
        loading: true,
    }
}

// Init fetches EC2 instances via a tea.Cmd (non-blocking).
func (v *EC2ListView) Init() tea.Cmd {
    return v.fetchInstances()
}

// fetchInstances returns a tea.Cmd that calls the AWS API in a goroutine.
func (v *EC2ListView) fetchInstances() tea.Cmd {
    return func() tea.Msg {
        instances, err := aws.ListInstances(context.Background(), v.client)
        if err != nil {
            return msg.ErrorMsg{Err: err, Context: "fetching EC2 instances"}
        }
        return msg.ResourcesLoadedMsg[aws.Instance]{Resources: instances}
    }
}

// Update must return (tea.Model, tea.Cmd) to satisfy the tea.Model interface.
func (v *EC2ListView) Update(m tea.Msg) (tea.Model, tea.Cmd) {
    switch m := m.(type) {
    case msg.ResourcesLoadedMsg[aws.Instance]:
        v.instances = m.Resources
        v.loading = false
        // populate table...
        return v, nil

    case msg.ErrorMsg:
        v.err = m.Err
        v.loading = false
        return v, nil

    case msg.RefreshMsg:
        v.loading = true
        return v, v.fetchInstances()

    // v2: tea.KeyMsg is now an interface. Use tea.KeyPressMsg for key presses.
    case tea.KeyPressMsg:
        switch m.String() {
        case "enter":
            selected := v.table.SelectedRow()
            return v, func() tea.Msg {
                return msg.NavigateMsg{
                    ViewID: "ec2_detail",
                    Params: map[string]string{"instanceId": selected.ID},
                }
            }
        case "r":
            v.loading = true
            return v, v.fetchInstances()
        }
    }
    // Pass remaining messages to the table component
    // ...
    return v, nil
}

// View returns tea.View (not string). Use tea.NewView() to wrap a string.
// Only the root model should set AltScreen on the returned View.
func (v *EC2ListView) View() tea.View {
    if v.loading {
        return tea.NewView("Loading EC2 instances...")
    }
    if v.err != nil {
        return tea.NewView("Error: " + v.err.Error())
    }
    return tea.NewView(v.table.View())
}
```

## UI Layout

The screen layout follows the k9s/lazygit pattern:

```
┌─────────────────────────────────────────────┐
│  Header: [Profile] [Region] > EC2 Instances │  ← persistent, shows context
├─────────────────────────────────────────────┤
│                                             │
│  Main Content Area                          │  ← table, detail view, etc.
│  (filterable, sortable table or detail)     │
│                                             │
│                                             │
├─────────────────────────────────────────────┤
│  Status Bar: <enter> view  <r> refresh ...  │  ← contextual keybindings
└─────────────────────────────────────────────┘
```

Use `lipgloss.Height()` and `lipgloss.Width()` to calculate sizes dynamically rather than hardcoding pixel values. The content area height should be:

```go
contentHeight = totalHeight - lipgloss.Height(header) - lipgloss.Height(statusBar)
```

The root model extracts child view content via the `Content` field on `tea.View`, composes the layout with lipgloss, then wraps the result in its own `tea.NewView()`:

```go
childView := m.nav.Current().View()
contentStr := childView.Content  // tea.View.Content is a public string field

body := lipgloss.JoinVertical(lipgloss.Left, header, contentStr, statusBar)

v := tea.NewView(body)
v.AltScreen = true
return v
```

## Keybinding Conventions

Follow vim-style keybindings consistent with lazygit/k9s:

| Key                    | Action                             |
| ---------------------- | ---------------------------------- |
| `j` / `k` or `↑` / `↓` | Navigate up/down in lists          |
| `enter`                | Drill into selected resource       |
| `esc`                  | Go back to previous view           |
| `/`                    | Open filter/search                 |
| `r`                    | Refresh current view               |
| `q`                    | Quit (with confirmation if needed) |
| `?`                    | Show help / keybindings            |
| `p`                    | Switch AWS profile                 |
| `R`                    | Switch AWS region                  |
| `y`                    | Copy resource ID/ARN to clipboard  |
| `d`                    | Describe / show detail pane        |

## Dependencies

In v2, the Charm libraries moved from `github.com/charmbracelet/` to the `charm.land/` vanity domain:

```
go get charm.land/bubbletea/v2
go get charm.land/lipgloss/v2
go get charm.land/bubbles/v2
go get github.com/aws/aws-sdk-go-v2
go get github.com/aws/aws-sdk-go-v2/config
go get github.com/aws/aws-sdk-go-v2/credentials
go get github.com/aws/aws-sdk-go-v2/service/ec2
go get github.com/aws/aws-sdk-go-v2/service/s3
go get github.com/aws/aws-sdk-go-v2/service/lambda
go get github.com/aws/aws-sdk-go-v2/service/ecs
go get github.com/aws/aws-sdk-go-v2/service/iam
go get github.com/aws/aws-sdk-go-v2/service/rds
go get github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs
```

Import examples:

```go
import (
    tea "charm.land/bubbletea/v2"
    "charm.land/lipgloss/v2"
    "charm.land/bubbles/v2/table"
    "charm.land/bubbles/v2/spinner"
    "charm.land/bubbles/v2/textinput"
    "charm.land/bubbles/v2/viewport"
)
```

## Development Tips

### Keep the event loop fast

Every AWS API call MUST happen inside a `tea.Cmd`. Never call AWS APIs directly in `Update()` or `View()`.

### Debugging

Bubble Tea occupies stdout, so you can't use `fmt.Println` for debugging. Instead, dump messages to a log file:

```go
if m.dump != nil {
    spew.Fdump(m.dump, msg)
}
```

Then `tail -f messages.log` in another terminal. Use `github.com/davecgh/go-spew` for pretty-printing.

### Pointer vs Value Receivers

You may use pointer receivers on your models. This is fine and common in larger Bubble Tea apps. Just ensure all state mutations happen inside `Update()`, never in goroutines.

### Message Ordering

Messages from concurrent `tea.Cmd` calls are NOT guaranteed to arrive in order. If ordering matters, use `tea.Sequence()` to chain commands.

### Layout

Always calculate sizes dynamically using `lipgloss.Height()` and `lipgloss.Width()` on rendered strings. Never hardcode heights — this breaks when borders, padding, or content changes.

### Bubble Tea v2 Specifics

Key differences from v1 that affect all code in this project:

- **Import paths:** Use `charm.land/` vanity domain, not `github.com/charmbracelet/`.
- **`View()` returns `tea.View`:** Use `tea.NewView(s)` to wrap a string. The `tea.View` struct has a public `Content string` field.
- **`tea.KeyMsg` is now an interface:** Use `tea.KeyPressMsg` for key press events, `tea.KeyReleaseMsg` for releases. `tea.KeyMsg` requires a nested type switch.
- **Program options moved to View:** `tea.WithAltScreen()` → `v.AltScreen = true` on the `tea.View` return. `tea.WithMouseCellMotion()` → `v.MouseMode = tea.MouseModeCellMotion`. `tea.NewProgram(model)` takes no options.
- **`Update()` returns `(tea.Model, tea.Cmd)`:** Not the concrete type. The concrete `*MyView` satisfies `tea.Model` when using pointer receivers.
- **Bubbles v2 uses getter/setter methods:** `viewport.SetWidth(w)` / `viewport.Width()` instead of `viewport.Width = w`. `textinput` still uses public fields (`Prompt`, `Placeholder`). Check each component.
- **Import alias to avoid shadowing:** When using `switch msg := msg.(type)` in `Update()`, any package named `msg` gets shadowed. Alias the import: `appmsg "...internal/msg"`.

### Testing

Use [teatest](https://github.com/charmbracelet/x/tree/main/exp/teatest) for end-to-end TUI testing. It emulates user input and checks rendered output.

### Demo Recording

Use [VHS](https://github.com/charmbracelet/vhs) from Charm to record terminal demos as animated GIFs for the README.

## Prior Art / Reference Projects

- **[lazygit](https://github.com/jesseduffield/lazygit)** — Git TUI, MIT license. Excellent UX patterns for multi-pane layout and navigation.
- **[k9s](https://github.com/derailed/k9s)** — Kubernetes TUI, Apache 2.0. Best reference for resource-browser UX with drill-down.
- **[PUG](https://github.com/leg100/pug)** — Terraform TUI built with Bubble Tea. Great reference for tree-of-models architecture, table component, and navigator pattern.
- **[clawscli/claws](https://github.com/clawscli/claws)** — Another k9s-inspired AWS TUI. Check for differentiation.

## AWS Services — Priority Order

Start with the most commonly used services and expand:

1. **EC2** — instances, security groups, key pairs
2. **S3** — buckets, objects (browse, download info)
3. **ECS** — clusters, services, tasks, task definitions
4. **Lambda** — functions, invocations, logs
5. **IAM** — users, roles, policies
6. **RDS** — instances, clusters
7. **CloudWatch** — log groups, log streams, log tailing
8. **CloudFormation** — stacks, events, resources
9. **Route53** — hosted zones, records
10. **DynamoDB** — tables, items

## Local Development with Taskfile + LocalStack

### Taskfile

[Taskfile](https://taskfile.dev) is a Go-based task runner that replaces Makefiles with readable YAML. It's a single binary with no dependencies, cross-platform, and has built-in support for watch mode, dotenv files, and task dependencies.

Install: `brew install go-task` (macOS) or see [taskfile.dev/installation](https://taskfile.dev/installation).

### LocalStack

[LocalStack](https://github.com/localstack/localstack) is a cloud service emulator that runs AWS services locally in a Docker container. It supports S3, Lambda, DynamoDB, SQS, SNS, EC2, IAM, and many more — letting you develop and test without hitting real AWS.

**Important (as of March 2026):** LocalStack has consolidated into a single image that requires a free account and auth token. The open-source Community Edition is no longer receiving updates. A free tier remains available for non-commercial use, and open-source projects can apply for free access to paid tiers.

Install: `brew install localstack/tap/localstack-cli` or `pip install localstack`

### AWS Client with LocalStack Support

The `internal/aws/client.go` should accept an optional endpoint override so the same code works against both real AWS and LocalStack:

```go
package aws

import (
    "context"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/credentials"
)

type Client struct {
    Config   aws.Config
    Profile  string
    Region   string
    Endpoint string // empty = real AWS, set = LocalStack
}

func NewClient(profile, region, endpoint string) (*Client, error) {
    opts := []func(*config.LoadOptions) error{}

    if profile != "" {
        opts = append(opts, config.WithSharedConfigProfile(profile))
    }
    if region != "" {
        opts = append(opts, config.WithRegion(region))
    }

    // For LocalStack, use dummy credentials
    if endpoint != "" {
        opts = append(opts, config.WithCredentialsProvider(
            credentials.NewStaticCredentialsProvider("test", "test", ""),
        ))
        if region == "" {
            opts = append(opts, config.WithRegion("us-east-1"))
        }
    }

    cfg, err := config.LoadDefaultConfig(context.Background(), opts...)
    if err != nil {
        return nil, err
    }

    return &Client{
        Config:   cfg,
        Profile:  profile,
        Region:   region,
        Endpoint: endpoint,
    }, nil
}

// ServiceEndpoint returns the endpoint override for service client constructors.
// Returns nil when targeting real AWS.
func (c *Client) ServiceEndpoint() *string {
    if c.Endpoint == "" {
        return nil
    }
    return aws.String(c.Endpoint)
}
```

When creating service clients, apply the endpoint override per-service using `BaseEndpoint`:

```go
func (c *Client) S3Client() *s3.Client {
    return s3.NewFromConfig(c.Config, func(o *s3.Options) {
        if c.Endpoint != "" {
            o.BaseEndpoint = aws.String(c.Endpoint)
            o.UsePathStyle = true // required for LocalStack S3
        }
    })
}

func (c *Client) EC2Client() *ec2.Client {
    return ec2.NewFromConfig(c.Config, func(o *ec2.Options) {
        if c.Endpoint != "" {
            o.BaseEndpoint = aws.String(c.Endpoint)
        }
    })
}
```

The app reads the endpoint from a CLI flag with env var fallback:

```go
// main.go
endpoint := flag.String("endpoint", os.Getenv("AWS_ENDPOINT_URL"), "AWS endpoint override (for LocalStack)")
```

### Docker Compose for LocalStack

Create a `docker-compose.yml` in the project root:

```yaml
services:
  localstack:
    image: localstack/localstack
    ports:
      - "4566:4566" # LocalStack gateway
    environment:
      - SERVICES=s3,ec2,lambda,ecs,iam,rds,logs,cloudformation,route53,dynamodb
      - DEBUG=0
      - PERSISTENCE=1 # persist state across restarts
    volumes:
      - "./scripts/localstack-init:/etc/localstack/init/ready.d" # seed scripts
      - "localstack-data:/var/lib/localstack"

volumes:
  localstack-data:
```

### Seed Scripts

Create `scripts/localstack-init/seed.sh` to populate LocalStack with test data:

```bash
#!/bin/bash
echo "Seeding LocalStack with test data..."

# Create S3 buckets
awslocal s3 mb s3://test-bucket-1
awslocal s3 mb s3://test-bucket-2
echo "hello world" | awslocal s3 cp - s3://test-bucket-1/test-file.txt

# Create DynamoDB table
awslocal dynamodb create-table \
    --table-name test-table \
    --key-schema AttributeName=id,KeyType=HASH \
    --attribute-definitions AttributeName=id,AttributeType=S \
    --billing-mode PAY_PER_REQUEST

# Create Lambda function (dummy)
awslocal lambda create-function \
    --function-name test-function \
    --runtime python3.12 \
    --handler lambda.handler \
    --role arn:aws:iam::000000000000:role/lambda-role \
    --zip-file fileb://scripts/localstack-init/dummy-lambda.zip

# Create IAM role
awslocal iam create-role \
    --role-name test-role \
    --assume-role-policy-document '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}'

echo "Seeding complete."
```

### Taskfile.yml

```yaml
version: "3"

dotenv: [".env"]

vars:
  BINARY: lazycloud
  LOCALSTACK_ENDPOINT: http://localhost:4566

tasks:
  default:
    desc: Show available tasks
    cmds:
      - task --list

  # ─── Build ──────────────────────────────────────────────

  build:
    desc: Build the binary
    cmds:
      - go build -o {{.BINARY}} .
    sources:
      - ./**/*.go
      - go.mod
      - go.sum
    generates:
      - "{{.BINARY}}"

  # ─── Development ────────────────────────────────────────

  dev:
    desc: Run the app against LocalStack
    deps: [localstack:up]
    cmds:
      - go run . --endpoint {{.LOCALSTACK_ENDPOINT}}
    env:
      AWS_ENDPOINT_URL: "{{.LOCALSTACK_ENDPOINT}}"

  dev:watch:
    desc: Rebuild and run on file changes
    deps: [localstack:up]
    cmds:
      - task: build
      - ./{{.BINARY}} --endpoint {{.LOCALSTACK_ENDPOINT}}
    watch: true
    sources:
      - ./**/*.go
    env:
      AWS_ENDPOINT_URL: "{{.LOCALSTACK_ENDPOINT}}"

  run:
    desc: Run the app against real AWS (uses default profile)
    deps: [build]
    cmds:
      - ./{{.BINARY}}

  # ─── Testing ────────────────────────────────────────────

  test:
    desc: Run all tests
    cmds:
      - go test ./... -v

  test:integration:
    desc: Run integration tests against LocalStack
    deps: [localstack:up, localstack:seed]
    cmds:
      - go test ./... -v -tags=integration
    env:
      AWS_ENDPOINT_URL: "{{.LOCALSTACK_ENDPOINT}}"

  # ─── Linting ────────────────────────────────────────────

  lint:
    desc: Run linter
    cmds:
      - golangci-lint run ./...

  fmt:
    desc: Format code
    cmds:
      - gofmt -s -w .
      - goimports -w .

  # ─── LocalStack ─────────────────────────────────────────

  localstack:up:
    desc: Start LocalStack
    cmds:
      - docker compose up -d
      - echo "Waiting for LocalStack to be ready..."
      - |
        timeout 30 bash -c 'until curl -s http://localhost:4566/_localstack/health | grep -q "running"; do sleep 1; done'
      - echo "LocalStack is ready."
    status:
      - curl -s http://localhost:4566/_localstack/health | grep -q "running"

  localstack:down:
    desc: Stop LocalStack
    cmds:
      - docker compose down

  localstack:reset:
    desc: Stop LocalStack and wipe all data
    cmds:
      - docker compose down -v

  localstack:seed:
    desc: Seed LocalStack with test data
    cmds:
      - bash scripts/localstack-init/seed.sh
    env:
      AWS_ENDPOINT_URL: "{{.LOCALSTACK_ENDPOINT}}"

  localstack:logs:
    desc: Tail LocalStack logs
    cmds:
      - docker compose logs -f localstack

  # ─── Housekeeping ───────────────────────────────────────

  clean:
    desc: Remove build artifacts
    cmds:
      - rm -f {{.BINARY}}
      - rm -rf .task/

  deps:
    desc: Download and tidy dependencies
    cmds:
      - go mod download
      - go mod tidy

  # ─── Release ────────────────────────────────────────────

  snapshot:
    desc: Build cross-platform binaries (requires goreleaser)
    cmds:
      - goreleaser build --snapshot --clean
```

### Developer Workflow

```bash
# First time setup
task deps                  # download Go dependencies
task localstack:up         # start LocalStack container
task localstack:seed       # populate with test data

# Daily development
task dev                   # run app against LocalStack
task dev:watch             # auto-rebuild on file changes

# Testing
task test                  # unit tests
task test:integration      # integration tests against LocalStack

# Against real AWS
task run                   # run against your default AWS profile

# Cleanup
task localstack:down       # stop container (data persists)
task localstack:reset      # stop container and wipe data
task clean                 # remove build artifacts
```

### Project Structure (updated)

The following files are added to support local development:

```
lazycloud/
├── Taskfile.yml                   # Task runner config
├── docker-compose.yml             # LocalStack container
├── .env                           # Local env vars (in .gitignore)
├── .env.example                   # Template for .env
├── scripts/
│   └── localstack-init/
│       ├── seed.sh                # Seed script for test data
│       └── dummy-lambda.zip       # Dummy Lambda for testing
├── ...
```

Add to `.gitignore`:

```
.task/
.env
lazycloud
localstack-data/
```

Add `.env.example` to the repo:

```bash
# AWS_ENDPOINT_URL=http://localhost:4566  # uncomment for LocalStack
# AWS_PROFILE=default
# AWS_REGION=us-east-1
```

## Build & Run

```bash
# Via Taskfile (recommended)
task build                         # build binary
task dev                           # run against LocalStack
task run                           # run against real AWS

# Manual
go build -o lazycloud .
./lazycloud                        # uses default profile/region
./lazycloud --profile staging      # specify AWS profile
./lazycloud --region us-west-2     # specify AWS region
./lazycloud --endpoint http://localhost:4566  # use LocalStack
```
