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

All themes must map these roles to their palette's green, yellow, red, and blue equivalents respectively. New themes must define all four.

### Light Themes

Catppuccin Latte and Tokyo Night Day are light themes. They set `Light: true`. On a light theme, `Base` and the surfaces are light, and `Text`, `SubText`, and `BrightText` are dark. `BrightText` is the strongest text, so it is near black, not white. The forms use the light huh base for these themes.

The theme setting `auto` is the default. The app asks the terminal for its background color at start (`tea.RequestBackgroundColor`). On a light background it uses Catppuccin Latte, and on a dark one, or when the terminal does not answer, Catppuccin Mocha. It picks the theme only once, so a later answer cannot rebuild the views that the user opened. A theme set in the config, with `--theme`, or with the picker always wins.

`TestLightThemesHaveReadableText` checks that the text of a light theme has a contrast of at least 3:1 on its background and on its badges. The official yellow and green of Catppuccin Latte are lower, so the theme uses darker shades of both.

A terminal without truecolor gets the nearest of 256 colors, and that color can be a gray. The official red of Catppuccin Latte, `#d20f39`, becomes 241 (`#626262`), so the theme uses `#c8102f`, which becomes 160 (`#d70000`). `TestThemeColorsKeepTheirHueIn256Colors` checks that the status colors of every theme keep a hue with 256 colors. Check a new color with `colorprofile.ANSI256.Convert`.

### Log Level Colors

The event log colors the level keyword of each entry:

| Level | Maps to   |
| ----- | --------- |
| `ERR` | `Error`   |
| `WRN` | `Warning` |
| `INF` | `Accent`  |
| `DBG` | `Muted`   |

### Resource State Colors

EC2 and ECS resource states should map to semantic roles:

| State                           | Maps to   |
| ------------------------------- | --------- |
| `running`, `active`, `RUNNING`  | `Success` |
| `pending`, `stopping`           | `Warning` |
| `stopped`, `terminated`         | `Error`   |

## Focus and Dimming

- **Focused panel**: Full-brightness colors, highlighted border using theme accent color
- **Unfocused panel**: Slightly dimmed text with `ui.Dim`. Do not use lipgloss `Faint(true)` on highlighted text: chroma ends each token with a reset, which also ends the faint style, so only the first token would be dim. `ui.Dim` and `ui.ReapplyAfterResets` set the style again after each reset. The cursor line and the selection of the content view use the same helper for their background. Borders use the theme's muted/surface color
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

`UI_COMPONENTS.md` lists the icons that the app uses now. A new icon must not reuse one of their fallbacks. These are the suggestions for the planned services:

| Service    | Nerd Font             | Unicode Fallback |
| ---------- | --------------------- | ---------------- |
| Lambda     | Lambda symbol         | `λ`              |
| ECS        | Container icon        | `⊟`              |
| IAM        | Lock icon             | `⊠`              |
| RDS        | Database icon         | `⊡`              |
| CloudWatch | Chart/graph icon      | `◔`              |

## Color Accessibility

- Never rely on color alone to convey information — always pair with text labels or symbols (e.g., state badges show both color and text like `● running`)
- Maintain sufficient contrast between foreground text and background in all themes
- Test new theme colors against the terminal background that the theme is made for, and against the theme's own surface colors
