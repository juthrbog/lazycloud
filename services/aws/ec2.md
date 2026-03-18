# EC2

Browse EC2 instances.

> **Note:** EC2 currently uses mock data. Real AWS API integration is planned.

## Views

### Instance List

Lists EC2 instances with key details.

| Column | Description |
|--------|-------------|
| ID | Instance ID |
| Name | Instance name tag |
| State | Running state (color-coded) |
| Type | Instance type |
| Private IP | Private IPv4 address |
| Public IP | Public IPv4 address |
| Launched | Launch date |

## Keybindings

| Key | Action |
|-----|--------|
| `enter` | View instance details as JSON |
| `d` | View instance details as JSON |
| `/` | Filter instances |
| `r` | Refresh |

## State Colors

Instance states are color-coded:

- **Green**: running, available, active
- **Red**: stopped, terminated, deleted
- **Yellow**: pending, starting, stopping
