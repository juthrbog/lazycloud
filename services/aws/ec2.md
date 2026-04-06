# EC2

Browse EC2 instances, security groups, and AMIs.

## Views

### Instance List

Lists EC2 instances with key details.

| Column | Description |
|--------|-------------|
| Instance ID | Instance ID |
| Name | Instance name tag |
| State | Running state (color-coded) |
| Type | Instance type |
| Private IP | Private IPv4 address |
| Public IP | Public IPv4 address |
| AZ | Availability zone |
| Launched | Launch date |

### Instance Detail (side panel)

Pressing `enter` or `d` fetches full instance metadata via `DescribeInstances` and displays it as formatted JSON in the side panel. Fields include:

- Instance ID, name, state, state reason
- Instance type, platform, architecture
- Network: private/public IP, private/public DNS, VPC, subnet, AZ
- Security groups (ID + name)
- Key pair, AMI, IAM role
- Root device type/name
- All tags

## Keybindings

| Key | Action |
|-----|--------|
| `enter` / `d` | View instance details as JSON |
| `o` | Start SSM session (connect to instance) |
| `m` | Manage instance (start/stop/reboot/terminate picker) |
| `y` | Copy instance ID to clipboard |
| `/` | Filter instances |
| `r` | Refresh |

## State Colors

Instance states are color-coded:

- **Green**: running, available, active
- **Red**: stopped, terminated, deleted
- **Yellow**: pending, starting, stopping

## Instance Operations

Press `m` on an instance to open an action picker showing only the valid operations for the instance's current state:

| Instance state | Available actions |
|---------------|-------------------|
| running | Stop, Reboot, Terminate |
| stopped | Start |
| pending, stopping, etc. | No actions available |

- **Start** executes immediately (safe, reversible)
- **Stop**, **Reboot**, and **Terminate** require typing "confirm" before proceeding
- All operations are gated behind ReadWrite mode — press `W` to switch from ReadOnly

After any operation, the instance list auto-refreshes to show the updated state.

## SSM Session

Press `o` on a running instance to start an SSM Session Manager shell. This suspends the TUI, opens an interactive terminal session, and restores the TUI when you exit.

**Prerequisites:**
- AWS CLI installed (`aws` command available)
- [Session Manager plugin](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html) installed
- Instance must be running with SSM agent and an appropriate IAM instance profile

If the instance is not running or the plugin is not installed, a toast error is shown.

## Security Group List

Lists EC2 security groups with inbound/outbound rule counts.

| Column | Description |
|--------|-------------|
| Group ID | Security group identifier |
| Name | Security group name |
| VPC | Associated VPC ID |
| Description | Group description (medium+ width) |
| In | Number of inbound rules |
| Out | Number of outbound rules |

### Security Group Detail (side panel)

Pressing `enter` or `d` opens a tabbed detail panel:

- **Info**: Group ID, Name, ARN, Description, VPC, Owner ID
- **Inbound**: Rules displayed in aligned columns (protocol, ports, source CIDR/SG, description)
- **Outbound**: Same format as inbound
- **JSON**: Full security group as formatted JSON
- **Tags**: Key-value pairs (shown only if tags exist)

### Keybindings

| Key | Action |
|-----|--------|
| `enter` / `d` | View security group details |
| `y` | Copy group ID to clipboard |
| `s` / `S` | Sort / reverse sort |
| `/` | Filter |
| `r` | Refresh |

### Cross-Navigation

Security groups are linked from EC2 instance details. Clicking a security group in an instance's Security Groups tab navigates to the SG list with the group auto-selected and its detail panel open.

## AMI List

Lists AMIs owned by the current account by default. Public AMIs can be searched by name.

| Column | Description |
|--------|-------------|
| AMI ID | Image identifier |
| Name | Image name |
| Owner | Owner alias or account ID |
| Architecture | x86_64, arm64 |
| State | available, pending, etc. (color-coded) |
| Created | Creation date |

### Keybindings

| Key | Action |
|-----|--------|
| `enter` / `d` | View AMI details as JSON |
| `y` | Copy AMI ID to clipboard |
| `?` | Search public AMIs by name |
| `/` | Filter current list |
| `s` / `S` | Sort / reverse sort |
| `r` | Refresh (returns to owned AMIs) |

### Public AMI Search

Press `?` to open an inline search prompt. Type a name fragment (e.g. `amazon-linux`, `ubuntu`) and press `enter` to query the AWS API. Results are capped at 100. Press `r` to return to the owned AMI list.

## Service Layer

`internal/aws/ec2.go` implements the `EC2Service` interface:

```go
type EC2Service interface {
    ListInstances(ctx context.Context) ([]Instance, error)
    GetInstanceDetail(ctx context.Context, instanceID string) (*InstanceDetail, error)
    StartInstance(ctx context.Context, instanceID string) error
    StopInstance(ctx context.Context, instanceID string) error
    RebootInstance(ctx context.Context, instanceID string) error
    TerminateInstance(ctx context.Context, instanceID string) error
    ListOwnedAMIs(ctx context.Context) ([]AMI, error)
    SearchAMIs(ctx context.Context, query string) ([]AMI, error)
    ListSecurityGroups(ctx context.Context) ([]SecurityGroup, error)
    GetSecurityGroup(ctx context.Context, groupID string) (*SecurityGroup, error)
}
```

Pagination is handled automatically in `ListInstances` and `ListSecurityGroups`. Each mutation method wraps the corresponding EC2 SDK call (`StartInstances`, `StopInstances`, `RebootInstances`, `TerminateInstances`). `ListOwnedAMIs` uses `DescribeImages` with `Owners: ["self"]`. `SearchAMIs` filters by name with a 100-result cap. `ListSecurityGroups` and `GetSecurityGroup` use `DescribeSecurityGroups` with automatic pagination. A shared `MockEC2Service` in `internal/aws/awstest/` enables testing views without AWS credentials.
