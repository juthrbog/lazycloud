# LazyCloud Visual Design System

Guidelines for consistent visual styling across all LazyCloud UI components.
All styling uses lipgloss v2 and derives from the `Theme` struct via the
shared `Styles` (S) or direct theme color access.

---

## 1. Color Roles

Every color in the Theme struct has a defined role. Use the role, not the
specific hex value, to ensure consistency across themes.

| Role | Theme Field | Usage |
|------|-------------|-------|
| **Primary** | `Primary` | Headings, table headers, dialog borders, key column in help |
| **Secondary** | `Secondary` | Unfocused borders, format badge background, selection background |
| **Accent** | `Accent` | Active/focused borders, key hints, cursor, filter prompt, active tab, interactive indicators |
| **Error** | `Error` | Error messages, destructive dialog borders, error toasts |
| **Warning** | `Warning` | RW mode badge, visual mode indicator, destructive confirmations |
| **Success** | `Success` | Success toasts |
| **Info** | `Info` | Informational badges (nowrap), link indicators, info toasts |
| **Base** | `Base` | Background fill, header padding |
| **Surface** | `Surface` | Status bar background, table header background |
| **Overlay** | `Overlay` | Cursor line background, selected row background |
| **Muted** | `Muted` | Inactive tabs, separators, line numbers, position info, hint descriptions, deemphasized text |
| **Text** | `Text` | Default body text, normal suggestion text |
| **SubText** | `SubText` | Status bar descriptions, breadcrumb ancestors, secondary text |
| **BrightText** | `BrightText` | Emphasized text, selected suggestion name, active breadcrumb, format badge text |
| **StateRunning** | `StateRunning` | Running/available/active resource states (green) |
| **StateStopped** | `StateStopped` | Stopped/terminated/deleted resource states (red) |
| **StatePending** | `StatePending` | Pending/starting/stopping resource states (yellow) |
| **GradientFrom/To** | `GradientFrom`, `GradientTo` | Header gradient line, gradient text |

### When to use which color

- **Interactive element focused?** → Accent
- **Interactive element unfocused?** → Secondary
- **User needs to read this?** → Text
- **Metadata the user can ignore?** → Muted
- **Something important happened?** → Error / Warning / Success / Info
- **Background of a container?** → Base (page) / Surface (bar) / Overlay (selection)

---

## 2. Typography

| Style | When to Use | Example |
|-------|-------------|---------|
| **Bold** | Emphasis, headings, active states, selected items | Active tab, dialog title, selected suggestion |
| **Faint** | Deemphasized content, unfocused panel | Unfocused side of split view |
| **Muted color** | Metadata, secondary info | Line numbers, position info, hint descriptions |
| **BrightText** | Maximum emphasis within a region | Current breadcrumb, badge text |

### Rules

- Never use bold + faint together
- Bold for interactive/active, normal weight for passive/inactive
- Muted for information that supports but isn't the focus
- BrightText only where the eye should be drawn

---

## 3. Badges / Pills

Inline metadata indicators. Use a consistent pattern:

```go
// Standard badge: colored text on themed background
lipgloss.NewStyle().
    Foreground(t.BrightText).
    Background(t.Secondary).
    Padding(0, 1).
    Render("json")
```

| Badge Type | Foreground | Background | Examples |
|------------|------------|------------|----------|
| **Format** | BrightText | Secondary | `json`, `yaml`, `markdown` |
| **Mode (RO)** | BrightText | Primary | `RO` |
| **Mode (RW)** | Base | Warning | `RW` (stands out as caution) |
| **Profile** | BrightText | Primary | `default`, `prod` |
| **Region** | Base | Accent | `us-east-1` |
| **State** | (via StateColor) | None | `✓ running`, `○ stopped` |
| **Info** | Info | None (text only) | `nowrap`, `Col 5` |

### Rules

- Badges with backgrounds get `Padding(0, 1)` for breathing room
- State badges use icons (nerd font or fallback) + colored text, no background
- Info badges are text-only with Info color (low visual weight)

---

## 4. Borders

| Component | Border Style | Border Color | When |
|-----------|-------------|-------------|------|
| **Content area** | `RoundedBorder()` | Secondary (unfocused) / Accent (focused) | Always visible |
| **Side panel** | `RoundedBorder()` | Secondary (unfocused) / Accent (focused) | When panel is open |
| **Dialogs** (picker, help) | `RoundedBorder()` | Primary | Modal overlays |
| **Confirm dialog** | `RoundedBorder()` | Error | Destructive action confirmation |
| **Suggestions** | `RoundedBorder()` | Secondary | Command bar dropdown |
| **Toasts** | `RoundedBorder()` | Secondary | Notification popups |

### Rules

- **RoundedBorder** everywhere — it's the app's visual signature
- Focused interactive regions get **Accent** borders
- Modal dialogs get **Primary** borders (they demand attention)
- Destructive dialogs get **Error** borders
- Non-interactive containers get **Secondary** borders
- Border padding: `Padding(0, 1)` for dialogs/suggestions, none for content borders

---

## 5. Spacing

### Padding standards

| Context | Padding | Rationale |
|---------|---------|-----------|
| **Inside dialog borders** | `Padding(0, 1)` | 1 char horizontal breathing room |
| **Badge text** | `Padding(0, 1)` | Consistent pill shape |
| **Status bar hints** | No padding, `"  "` separator between hints | Compact, many hints per line |
| **Table cells** | `Padding(0, 1)` (bubbles default) | Standard table readability |

### Separator conventions

| Separator | Character | Usage |
|-----------|-----------|-------|
| **Breadcrumb** | ` › ` | Navigation path |
| **Status hint** | `  ` (2 spaces) | Between key+desc pairs |
| **Tab bar** | `  ` (2 spaces) | Between tab labels |
| **Key+description** | ` ` (1 space) | `<key> desc` in status bar |

---

## 6. Focus Indicators

| State | Visual Treatment |
|-------|-----------------|
| **Focused panel** | Accent border, normal text |
| **Unfocused panel** | Secondary border, faint text |
| **Selected table row** | Overlay background, BrightText |
| **Cursor line** (content view) | Overlay background |
| **Visual selection** | Secondary background |
| **Selected suggestion** | `▸ ` indicator, bold BrightText |
| **Active tab** | Accent color, bold |
| **Inactive tab** | Muted color, normal weight |

---

## 7. Icons

Defined in `internal/ui/icons.go`. All icons have nerd font and plain fallback:

| Icon | Nerd Font | Fallback | Usage |
|------|-----------|----------|-------|
| Cloud | `\U000f015f` | `☁` | App title |
| S3 | `\U000f01bc` | `◇` | S3 service |
| EC2 | `\U000f01c4` | `◈` | EC2 service |
| Running | `\U000f012c` | `●` | State: running/available |
| Stopped | `\U000f0156` | `○` | State: stopped/terminated |
| Pending | `\U000f0e4e` | `◌` | State: pending/starting |

### Rules

- Always use `Icon()` method to respect the `UseNerdFonts` flag
- State icons pair with `StateColor()` for consistent color + icon

---

## 8. Component Specifications

### Header
- Gradient text title + profile/region/mode badges + breadcrumbs
- Full-width Surface background padding
- Gradient line (`▀` characters with `Blend1D`) below
- At < 80 cols: drop region badge. At < 60 cols: title + breadcrumb only

### Status Bar
- Surface background, full width
- Key hints: `<key>` in Accent+Bold + `desc` in SubText
- Progressive hiding: drop hints from right when they don't fit
- Replaced by command bar input when `:` is active

### Content View (Side Panel)
- Tab bar: `1:Name` format, Accent+Bold for active, Muted for inactive
- Header line: format badge + position info + mode indicators
- Viewport: syntax-highlighted content with line numbers
- Cursor/selection: Overlay/Secondary backgrounds

### Dialogs (Picker, Help, Confirm)
- Centered overlay via Compositor
- RoundedBorder with Primary (or Error for destructive)
- Padding(0, 1) inside border
- Title in Bold+Primary

### Toasts
- Bottom-right overlay via Compositor
- RoundedBorder with Secondary
- Level-based icon + color (Success/Error/Primary)

### Command Bar
- Input: `:` in Accent+Bold + text + `█` cursor + muted hints
- Suggestions: left-aligned above input, RoundedBorder+Secondary
- Top suggestion auto-highlighted with `▸` indicator

---

## 9. Styling Architecture

### Shared Styles (S)

The `Styles` struct in `styles.go` provides pre-built styles derived from
the active theme. Use these for repeated patterns:

- `S.Title` — Bold + Primary
- `S.StatusKey` / `S.StatusDesc` / `S.StatusBarBase` — Status bar
- `S.DetailKey` / `S.DetailValue` — Key-value pairs
- `S.FilterPrompt` — Filter `/` character
- `S.Error` / `S.Warning` / `S.Success` — Message styling
- `S.HeaderStyle` / `S.HeaderAccent` — Header badges
- `S.HeaderGradient` — Pre-computed gradient colors

### When to use S vs inline

- **Use S** when the same style appears in multiple components or is part of
  a standardized pattern (status bar, titles, badges)
- **Use inline** for one-off or context-dependent styling (cursor highlights,
  dynamic badges, per-line coloring)
- **Never hardcode colors** — always reference `ActiveTheme` fields

### Adding new shared styles

1. Add the field to the `Styles` struct in `styles.go`
2. Initialize it in `NewStyles(t Theme)` using theme colors
3. Use `S.FieldName` in components
4. Styles auto-rebuild on theme change via `RebuildStyles()`

---

## 10. Theme Compatibility

All 4 themes (Catppuccin, Dracula, Nord, Tokyo Night) follow the same
color role structure. When adding styling:

- Test with at least 2 themes (one warm like Catppuccin, one cool like Nord)
- Avoid assuming specific luminance — use the role names, not the values
- State colors (Running/Stopped/Pending) are semantically consistent across
  all themes (green/red/yellow)
- Gradient generation via `Blend1D` uses CIELAB color space for perceptually
  uniform transitions across all theme palettes

---

## 11. Current Audit: Components Needing Updates

Based on auditing all components against this design system:

| Component | Issue | Priority |
|-----------|-------|----------|
| **Status bar** | Flat text styling, no visual hierarchy | High (#65) |
| **Spinner** | No theming, uses bubbles defaults | Low |
| **Toast** | Could use level-specific border colors | Low |
| **Command bar input** | No background, visually disconnected from suggestions | Medium |

These are tracked as follow-up issues where applicable.
