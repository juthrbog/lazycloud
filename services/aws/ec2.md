# EC2

Browse EC2 instances.

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
| `y` | Copy instance ID to clipboard |
| `/` | Filter instances |
| `r` | Refresh |

## State Colors

Instance states are color-coded:

- **Green**: running, available, active
- **Red**: stopped, terminated, deleted
- **Yellow**: pending, starting, stopping

## SSM Session

Press `o` on a running instance to start an SSM Session Manager shell. This suspends the TUI, opens an interactive terminal session, and restores the TUI when you exit.

**Prerequisites:**
- AWS CLI installed (`aws` command available)
- [Session Manager plugin](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html) installed
- Instance must be running with SSM agent and an appropriate IAM instance profile

If the instance is not running or the plugin is not installed, a toast error is shown.

## Service Layer

`internal/aws/ec2.go` implements the `EC2Service` interface:

```go
type EC2Service interface {
    ListInstances(ctx context.Context) ([]Instance, error)
    GetInstanceDetail(ctx context.Context, instanceID string) (*InstanceDetail, error)
}
```

Pagination is handled automatically in `ListInstances`. The service uses `DescribeInstances` for both operations.
