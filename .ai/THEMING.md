# Theming Guidelines

## Semantic Color Palette

All UI elements should use semantic color roles, never hardcoded color values. Each theme defines concrete colors for these roles, ensuring every theme looks intentional rather than accidental.

### Status Colors

| Role         | Usage                                                    |
| ------------ | -------------------------------------------------------- |
| `Success`    | Running instances, healthy tasks, completed operations   |
| `Warning`    | Pending states, stopping instances, approaching limits   |
| `Error`      | Failed tasks, terminated instances, API errors           |
| `Info`       | Informational badges, neutral highlights, links          |

All four themes (Catppuccin, Dracula, Nord, Tokyo Night) must map these roles to their palette's green, yellow, red, and blue equivalents respectively. New themes must define all four.

### Log Level Colors

Log viewers use a subset of status colors for log level keywords:

| Level   | Maps to   |
| ------- | --------- |
| `ERROR` | `Error`   |
| `WARN`  | `Warning` |
| `INFO`  | `Success` |
| `DEBUG` | `Info`    |

### Resource State Colors

EC2 and ECS resource states should map to semantic roles:

| State                           | Maps to   |
| ------------------------------- | --------- |
| `running`, `active`, `RUNNING`  | `Success` |
| `pending`, `stopping`           | `Warning` |
| `stopped`, `terminated`         | `Error`   |

## Focus and Dimming

- **Focused panel**: Full-brightness colors, highlighted border using theme accent color
- **Unfocused panel**: Slightly dimmed text (use lipgloss `Faint(true)` or reduce foreground alpha). Borders use the theme's muted/surface color
- **Active breadcrumb segment**: Bold text; inactive segments use muted foreground
- **Active tab**: Theme accent color background or underline; inactive tabs use muted foreground

This creates clear visual hierarchy without being distracting. The user should always know which panel has focus at a glance.

## Nerd Font Icons

### Consistency Rules

- Every AWS service in the home view must have a distinct icon
- Icons should be semantically meaningful (server icon for EC2, bucket for S3, lambda symbol for Lambda, etc.)
- All icons must have a Unicode fallback defined in `ui/icons.go`
- Fallbacks should be recognizable single characters, not emoji (emoji render inconsistently across terminals)

### Service Icon Conventions

Follow this pattern for new services:

| Service    | Nerd Font             | Unicode Fallback |
| ---------- | --------------------- | ---------------- |
| EC2        | Server/compute icon   | `⊞`              |
| S3         | Bucket/database icon  | `◉`              |
| Lambda     | Lambda symbol         | `λ`              |
| ECS        | Container icon        | `⊟`              |
| IAM        | Shield/lock icon      | `⛨`              |
| RDS        | Database icon         | `⊡`              |
| CloudWatch | Chart/graph icon      | `◈`              |

## Color Accessibility

- Never rely on color alone to convey information — always pair with text labels or symbols (e.g., state badges show both color and text like `● running`)
- Maintain sufficient contrast between foreground text and background in all four themes
- Test new theme colors against both dark terminal backgrounds and the theme's own surface colors
