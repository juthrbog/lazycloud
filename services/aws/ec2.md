# EC2

Browse EC2 instances, AMIs, security groups, VPCs, and subnets.

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

Pressing `enter` or `d` fetches the instance with `DescribeInstances` and opens a tabbed side panel: Info, JSON, Security Groups, and Tags. The Info tab links to the VPC, the subnet, and the AMI of the instance. The fields include:

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
| `enter` / `d` | View instance details |
| `o` | Start SSM session (connect to instance). ReadWrite mode only. |
| `m` | Manage instance (start/stop/reboot/terminate picker). ReadWrite mode only. |
| `y` | Copy instance ID to clipboard |
| `space` | Select several instances for `m` |
| `s` / `S` | Sort / reverse sort |
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

- **Start** asks first with a y/n prompt. Press `y` to start the instances. `n`, `enter`, or `esc` cancels. A start destroys nothing, but a running instance costs money.
- **Stop**, **Reboot**, and **Terminate** require typing "confirm" before proceeding
- All operations are gated behind ReadWrite mode — press `W` to switch from ReadOnly

After any operation, the instance list auto-refreshes to show the updated state. An operation on several instances can partly succeed. For example, a terminate stops at a protected instance in one Availability Zone but goes on in another. When an operation fails, the toast shows the AWS error code, and the list refreshes the instances of the operation, so it shows their real state.

## SSM Session

Press `o` on a running instance to start an SSM Session Manager shell. This suspends the TUI, opens an interactive terminal session, and restores the TUI when you exit.

An SSM session is an interactive shell on the instance, so ReadOnly mode blocks it. Press `W` to switch to ReadWrite mode first.

LazyCloud runs the `aws` CLI directly, without a shell. The instance name is shown in a banner as plain text, so a Name tag cannot inject commands or terminal escape sequences.

**Prerequisites:**
- AWS CLI installed (`aws` command available)
- [Session Manager plugin](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html) installed
- Instance must be running with SSM agent and an appropriate IAM instance profile

If the app is in ReadOnly mode, the instance is not running, or the `aws` CLI or the plugin is not installed, a toast error is shown.

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

## VPC List

Lists the VPCs of the region. It loads them page by page.

| Column | Description |
|--------|-------------|
| VPC ID | VPC identifier |
| Name | Name tag |
| CIDR Block | Primary IPv4 CIDR block |
| State | available, pending (color-coded) |
| Default | Whether it is the default VPC (wide terminals) |
| Tenancy | Instance tenancy (wide terminals) |

### VPC Detail (side panel)

Pressing `enter` or `d` opens a tabbed detail panel:

- **Info**: VPC ID, Name, State, CIDR Block, Default, Tenancy, DHCP Options, Owner, and a link to the subnets of the VPC
- **CIDR Blocks**: The IPv4 blocks, and the IPv6 blocks if the VPC has them
- **JSON**: Full VPC as formatted JSON
- **Tags**: Key-value pairs (shown only if tags exist)

### Keybindings

| Key | Action |
|-----|--------|
| `enter` / `d` | View VPC details |
| `n` | Open the subnets of the VPC |
| `y` | Copy VPC ID to clipboard |
| `s` / `S` | Sort / reverse sort |
| `/` | Filter |
| `r` | Refresh |

## Subnet List

Lists the subnets of the region. It loads them page by page. When it opens from a VPC, it lists only the subnets of that VPC.

| Column | Description |
|--------|-------------|
| Subnet ID | Subnet identifier |
| Name | Name tag |
| VPC ID | VPC of the subnet (wide terminals) |
| AZ | Availability zone |
| CIDR Block | IPv4 CIDR block |
| Avail IPs | Free IPv4 addresses (wide terminals) |
| State | available, pending (color-coded) |
| Public | Whether instances get a public IP at launch (wide terminals) |
| Default | Whether it is a default subnet (wide terminals) |

### Subnet Detail (side panel)

Pressing `enter` or `d` opens a tabbed detail panel:

- **Info**: Subnet ID, Name, ARN, VPC (a link to the VPC), AZ, AZ ID, CIDR Block, Available IPs, State, Owner
- **Config**: Public IP on launch, IPv6 assignment, default for the AZ, DNS64, and IPv6-only
- **IPv6**: The IPv6 blocks (shown only if the subnet has them)
- **JSON**: Full subnet as formatted JSON
- **Tags**: Key-value pairs (shown only if tags exist)

### Keybindings

| Key | Action |
|-----|--------|
| `enter` / `d` | View subnet details |
| `y` | Copy subnet ID to clipboard |
| `s` / `S` | Sort / reverse sort |
| `/` | Filter |
| `r` | Refresh |

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
| `p` | Search public AMIs by name |
| `/` | Filter current list |
| `s` / `S` | Sort / reverse sort |
| `r` | Refresh (returns to owned AMIs) |

The owned list shows every AMI of the account, over as many pages as there are.

The AMI link of an instance usually points to an AMI of another account, which is not in the owned list. The view then fetches that AMI by ID, adds a row for it until the next refresh, and opens its panel. The footer counts these rows as linked. If the AMI is not found, a toast says so.

### Public AMI Search

Press `p` to open an inline search prompt. Type a name fragment (e.g. `amazon-linux`, `ubuntu`) and press `enter` to query the AWS API. Results are capped at 100. A page of a filtered EC2 listing can hold few matches while more pages follow, so the search reads up to 20 pages of 1,000 images to find them. Press `r` to return to the owned AMI list.

## Service Layer

`internal/aws/ec2.go` implements the `EC2Service` interface:

```go
type EC2Service interface {
    ListInstances(ctx context.Context) ([]Instance, error)
    ListInstancesPage(ctx context.Context, token *string) (*InstancePage, error)
    GetInstanceDetail(ctx context.Context, instanceID string) (*InstanceDetail, error)
    RefreshInstances(ctx context.Context, ids []string) ([]Instance, error)
    StartInstance(ctx context.Context, instanceID string) error
    StopInstance(ctx context.Context, instanceID string) error
    RebootInstance(ctx context.Context, instanceID string) error
    TerminateInstance(ctx context.Context, instanceID string) error
    StartInstances(ctx context.Context, ids []string) error
    StopInstances(ctx context.Context, ids []string) error
    RebootInstances(ctx context.Context, ids []string) error
    TerminateInstances(ctx context.Context, ids []string) error
    ListOwnedAMIs(ctx context.Context) ([]AMI, error)
    SearchAMIs(ctx context.Context, query string) ([]AMI, error)
    RefreshAMIs(ctx context.Context, ids []string) ([]AMI, error)
    ListSecurityGroups(ctx context.Context) ([]SecurityGroup, error)
    GetSecurityGroup(ctx context.Context, groupID string) (*SecurityGroup, error)
    ListVPCsPage(ctx context.Context, token *string) (*VPCPage, error)
    GetVPC(ctx context.Context, vpcID string) (*VPC, error)
    ListSubnetsPage(ctx context.Context, token *string, vpcID string) (*SubnetPage, error)
    GetSubnet(ctx context.Context, subnetID string) (*Subnet, error)
}
```

`ListInstances` and `ListSecurityGroups` read every page. The instance, VPC, and subnet lists load page by page with the `...Page` methods. An instance page holds up to 1,000 instances. `ListSubnetsPage` filters by VPC when `vpcID` is not empty. Each mutation method wraps the corresponding EC2 SDK call (`StartInstances`, `StopInstances`, `RebootInstances`, `TerminateInstances`). The single-instance methods call the same SDK calls with one ID. `RefreshInstances` and `RefreshAMIs` fetch a set of resources by ID. `ListOwnedAMIs` uses `DescribeImages` with `Owners: ["self"]` and reads every page. `SearchAMIs` filters by name and reads pages until it has 100 results or read 20 pages. `ListSecurityGroups` and `GetSecurityGroup` use `DescribeSecurityGroups` with automatic pagination. A shared `MockEC2Service` in `internal/aws/awstest/` enables testing views without AWS credentials.
