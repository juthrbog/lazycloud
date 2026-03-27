# Charm Ecosystem Layout Reference

Deep research into BubbleTea v2, LipGloss v2, and Bubbles v2 layout patterns.
Based on reading actual source code from the installed modules, Charm blog posts,
community discussions, and real-world usage in this codebase.

**Source versions studied:**
- `charm.land/lipgloss/v2@v2.0.2`
- `charm.land/bubbletea/v2@v2.0.2`
- `charm.land/bubbles/v2@v2.0.0`

---

## 1. Layout Primitives (LipGloss v2)

### 1.1 Measurement Functions

```go
lipgloss.Width(str string) int     // Max line width (ANSI-aware, handles wide chars)
lipgloss.Height(str string) int    // Count of '\n' + 1
lipgloss.Size(str string) (w, h)   // Both at once
```

**Critical detail:** `Height()` counts newlines. An empty string has height 1.
A string with no trailing newline "foo\nbar" has height 2. Always use these
instead of `len()` or `len([]rune())`.

### 1.2 Style Dimension Properties

| Property | Behavior | When to Use |
|----------|----------|-------------|
| `Width(n)` | Sets **total** width including borders/padding. Pads short lines, wraps long text. Content area = `n - GetHorizontalFrameSize()`. | When you need a block to be exactly N cells wide total. |
| `Height(n)` | Sets **minimum total** height including borders/padding. Pads with blank lines if content is shorter. Does NOT truncate. Content area = `n - GetVerticalFrameSize()`. | When you need a block to be **at least** N cells tall total. |
| `MaxWidth(n)` | Truncates lines exceeding N cells. Applied **after** all other rendering (borders, margins). | Hard width cap on final output. |
| `MaxHeight(n)` | Truncates lines exceeding N. Applied **after** all other rendering. | Hard height cap on final output. |

**The Height() trap:** `Height(n)` is a minimum, not an exact size. If your
content is taller than `n`, the output will exceed `n` lines. To get exact
height, you must **either**:
1. Use `Height(n)` + `MaxHeight(n)` together, or
2. Pre-truncate your content before rendering, or
3. Use a viewport (which handles scrolling for overflow).

**How the render pipeline works** (from reading `style.go` Render method):
1. Tab conversion
2. Word wrap (if Width set, wraps at `width - padding - borders`)
3. Apply text styling (bold, color, etc.)
4. Apply padding (left, right, top, bottom)
5. Apply Height (vertical alignment/padding to minimum height)
6. Apply horizontal alignment (also pads lines to equal width)
7. Apply borders
8. Apply margins
9. Apply MaxWidth (truncate each line)
10. Apply MaxHeight (truncate line count)

**Frame size helpers:**
```go
style.GetHorizontalFrameSize() // margins + padding + borders (horizontal)
style.GetVerticalFrameSize()   // margins + padding + borders (vertical)
style.GetHorizontalBorderSize()
style.GetVerticalBorderSize()
style.GetFrameSize() (x, y int)
```

### 1.3 JoinVertical / JoinHorizontal

```go
lipgloss.JoinVertical(pos Position, strs ...string) string
lipgloss.JoinHorizontal(pos Position, strs ...string) string
```

**JoinVertical** stacks strings top-to-bottom. The `pos` controls horizontal
alignment of blocks with different widths:
- `Left` (0.0): left-align, pad right with spaces
- `Center` (0.5): center, pad both sides
- `Right` (1.0): right-align, pad left with spaces

All lines in the result are padded to the width of the widest line across
all blocks.

**JoinHorizontal** places strings side-by-side. The `pos` controls vertical
alignment when blocks have different heights:
- `Top` (0.0): align tops, pad bottom
- `Center` (0.5): center vertically
- `Bottom` (1.0): align bottoms, pad top

All blocks are padded to the height of the tallest block.

**Key behavior from source code (`join.go`):**
- JoinHorizontal pads each block's lines to that block's max width with spaces
- It adds blank lines to shorter blocks based on position
- The result has NO trailing newline unless the inputs did

### 1.4 Place / PlaceHorizontal / PlaceVertical

```go
lipgloss.Place(width, height int, hPos, vPos Position, str string, opts ...WhitespaceOption) string
lipgloss.PlaceHorizontal(width int, pos Position, str string, opts ...WhitespaceOption) string
lipgloss.PlaceVertical(height int, pos Position, str string, opts ...WhitespaceOption) string
```

These place content within a fixed-size box, padding with whitespace.
**They are noops if the content already exceeds the specified dimension.**

`Place` is just `PlaceVertical(height, vPos, PlaceHorizontal(width, hPos, str))`.

Use `WhitespaceOption` to style the padding (e.g., background color on the
whitespace fill).

### 1.5 Compositor / Layer / Canvas (New in v2)

The compositor system provides cell-level rendering with z-ordering and
absolute positioning. It is fundamentally different from the string-joining
approach.

```go
// Layer: content + position
layer := lipgloss.NewLayer(renderedString).X(col).Y(row).Z(depth)
layer.ID("my-layer")              // for hit testing
layer.AddLayers(childLayers...)   // nesting

// Compositor: flattens layer tree, sorts by Z, renders
comp := lipgloss.NewCompositor(layer1, layer2, layer3)
output := comp.Render()           // creates canvas internally, draws all layers

// Canvas: low-level cell buffer
canvas := lipgloss.NewCanvas(width, height)
canvas.Compose(layer)             // draws layer onto canvas
result := canvas.Render()         // outputs string

// Hit testing (for mouse support)
hit := comp.Hit(mouseX, mouseY)   // returns LayerHit with ID and bounds
```

**How Compositor.Render() works** (from `layer.go`):
1. Flattens all nested layers recursively with absolute X,Y positions
2. Sorts by Z index (lowest drawn first = background)
3. Creates a Canvas sized to the bounding box of all layers
4. Draws each layer's content onto the canvas at its position
5. Later layers overwrite earlier ones (painter's algorithm)
6. Returns `canvas.Render()`

**Canvas** uses `ultraviolet.ScreenBuffer` internally -- a 2D cell grid where
each cell has a rune, style, and link. This means overlapping content is
handled correctly at the cell level, unlike string concatenation.

---

## 2. The Right Way to Build Fixed Layouts

### 2.1 The Height Budget Pattern

The core pattern for building a TUI with header + content + status bar:

```go
func (m Model) View() tea.View {
    // 1. Render fixed-height chrome first
    header := renderHeader(m.width)
    statusBar := renderStatusBar(m.width)

    // 2. Measure chrome (never hardcode heights!)
    headerH := lipgloss.Height(header)
    statusH := lipgloss.Height(statusBar)

    // 3. Compute remaining budget for content
    contentH := m.height - headerH - statusH
    if contentH < 0 {
        contentH = 0
    }

    // 4. Tell child its exact dimensions
    content := m.child.View()  // child already knows its size from Update

    // 5. Force content to exact height
    contentBlock := lipgloss.NewStyle().
        Width(m.width).
        Height(contentH).
        MaxHeight(contentH).  // CRITICAL: prevents overflow
        Render(content)

    // 6. Stack vertically
    body := lipgloss.JoinVertical(lipgloss.Left, header, contentBlock, statusBar)

    v := tea.NewView(body)
    v.AltScreen = true
    return v
}
```

**Why this works:**
- Header and status bar render at their natural height
- Content gets exactly the remaining space
- `Height(n)` pads if content is too short
- `MaxHeight(n)` truncates if content is too tall
- `JoinVertical` ensures consistent width across all sections

**Why `Height()` alone is not enough:**
From the lipgloss source (`style.go` line 481):
```go
// Height
if height > 0 {
    str = alignTextVertical(str, verticalAlign, height, nil)
}
```
And `alignTextVertical` (`align.go` line 61):
```go
func alignTextVertical(str string, pos Position, height int, _ *ansi.Style) string {
    strHeight := strings.Count(str, "\n") + 1
    if height < strHeight {
        return str  // NOOP if content is taller!
    }
    // ... padding logic
}
```

### 2.2 Graceful Degradation

When the terminal is very small, hide chrome progressively:

```go
contentH := m.height - headerH - statusH

// Hide header first if too cramped
minContent := 5
if contentH < minContent && headerH > 0 {
    header = ""
    headerH = 0
    contentH = m.height - statusH
}
// Hide status bar if still too cramped
if contentH < minContent && statusH > 0 {
    statusBar = ""
    statusH = 0
    contentH = m.height
}
```

This pattern is already used in lazycloud's `app.go` (line 758-771).

### 2.3 Early Return for Zero Dimensions

BubbleTea sends `WindowSizeMsg` asynchronously after startup. The first
`View()` call may have width=0, height=0. Always guard:

```go
func (m Model) View() tea.View {
    if m.width == 0 || m.height == 0 {
        return tea.NewView("")
    }
    // ... normal rendering
}
```

The viewport bubble does this too (`viewport.go` line 737):
```go
if w == 0 || h == 0 {
    return ""
}
```

---

## 3. Split Panel Patterns

### 3.1 Horizontal Split (Side Panel)

The pattern for a main view + side detail panel:

```go
func (m Model) renderSplitView(contentH int) string {
    panelW := m.panelWidth()
    mainW := m.width - panelW - 1  // 1 char gap or shared border

    // Borders consume 2 chars each (left + right)
    mainInnerW := mainW - 2
    mainInnerH := contentH - 2
    panelInnerW := panelW - 2
    panelInnerH := contentH - 2

    mainStyle := lipgloss.NewStyle().
        Border(lipgloss.RoundedBorder()).
        BorderForeground(mainBorderColor).
        Width(mainInnerW).        // inner width
        Height(mainInnerH)        // inner height (minimum)

    panelStyle := lipgloss.NewStyle().
        Border(lipgloss.RoundedBorder()).
        BorderForeground(panelBorderColor).
        Width(panelInnerW).
        Height(panelInnerH)

    return lipgloss.JoinHorizontal(lipgloss.Top,
        mainStyle.Render(mainContent),
        panelStyle.Render(panelContent),
    )
}
```

**Border accounting:** `Width(n)` and `Height(n)` set **total** dimensions
including borders. Lipgloss subtracts border sizes internally before processing
content. From the source (`style.go` line 408):
```go
width -= horizontalBorderSize  // content wraps within width minus borders
```

So if you want a box that occupies exactly 40 terminal columns with a rounded
border, set `Width(40)`. The content area will be 38 (40 - 2 border chars).
Do NOT subtract borders yourself -- that double-subtracts.

### 3.2 Propagating Dimensions to Children

When the panel state changes or the window resizes, recalculate all children:

```go
func (m *Model) recalcLayout() {
    innerH := m.height - m.chromeHeight()

    if m.panelOpen && m.panel != nil {
        pw := m.panelWidth()
        mainW := m.width - pw - 1  // gap
        // Tell main view its available space (minus borders)
        m.nav.UpdateCurrent(tea.WindowSizeMsg{
            Width:  mainW - 2,
            Height: innerH - 2,
        })
        // Tell panel its available space (minus borders)
        m.panel.SetSize(pw - 2, innerH - 2)
    } else {
        m.nav.UpdateCurrent(tea.WindowSizeMsg{
            Width:  m.width - 2,
            Height: innerH - 2,
        })
    }
}
```

### 3.3 Panel Width Calculation

```go
func (m Model) panelWidth() int {
    pw := m.width / 3
    if pw < panelMinWidth {
        pw = panelMinWidth
    }
    if pw > panelMaxWidth {
        pw = panelMaxWidth
    }
    return pw
}

func (m Model) canShowPanel() bool {
    pw := m.panelWidth()
    return m.width - pw > panelMinWidth  // main area must also be usable
}
```

---

## 4. Nested Model Pattern

### 4.1 Architecture

BubbleTea uses the Elm architecture: each component is a Model with
`Init()`, `Update()`, and `View()`. Parent models compose children.

**The tree structure:**
```
App (root model)
  |-- Header (pure function, no model)
  |-- Navigator
  |     |-- EC2List (table model)
  |     |-- S3Objects (table model)
  |     |-- ContentView (viewport model)
  |-- TabbedPanel
  |     |-- ContentView (viewport model)
  |-- StatusBar (pure function, no model)
  |-- HelpOverlay
  |-- Confirm dialog
  |-- Picker dialog
```

### 4.2 Dimension Flow

Dimensions flow **top-down** through the tree. The root model receives
`WindowSizeMsg` from BubbleTea, then propagates calculated sizes to children:

```
WindowSizeMsg{Width: 120, Height: 40}
  |
  App stores m.width=120, m.height=40
  |
  App.View() computes:
    headerH = 3, statusH = 1
    contentH = 40 - 3 - 1 = 36
    |
    sends to child: WindowSizeMsg{Width: 118, Height: 34}
                    (118 = 120-2 for borders, 34 = 36-2 for borders)
```

**Important:** Children should NOT handle `tea.WindowSizeMsg` from BubbleTea
directly for layout purposes. The parent should intercept it and send
adjusted dimensions. In this codebase, the app intercepts the real
`WindowSizeMsg` (line 126) and then sends adjusted messages to children
via `pushView` and `recalcLayout`.

### 4.3 The SetSize Pattern

For non-BubbleTea child models (like ContentView, TabbedPanel), use a
`SetSize(w, h)` method instead of routing WindowSizeMsg:

```go
type ContentView struct {
    viewport viewport.Model
    width    int
    height   int
}

func (cv *ContentView) SetSize(w, h int) {
    cv.width = w
    cv.height = h
    cv.viewport.SetWidth(w)
    cv.viewport.SetHeight(h)
}
```

For BubbleTea Model children (those with Update/View), send them a
`WindowSizeMsg` through their Update:

```go
func (parent *Model) resizeChild() {
    child.Update(tea.WindowSizeMsg{
        Width:  calculatedWidth,
        Height: calculatedHeight,
    })
}
```

### 4.4 View Composition

Child `View()` returns a string. Parent composes these strings:

```go
func (m Model) View() tea.View {
    header := renderHeader(m.width)
    content := m.currentChild.View()  // returns string
    status := renderStatusBar(m.width)

    body := lipgloss.JoinVertical(lipgloss.Left, header, content, status)
    return tea.NewView(body)
}
```

Only the root model returns `tea.View`. All child models return `string`.
The bubbles library components (viewport, table, etc.) all return `string`
from their `View()` methods.

---

## 5. Compositor vs Join -- When to Use Each

### 5.1 Use JoinVertical / JoinHorizontal For:

- **Sequential layouts**: header + content + footer stacked vertically
- **Side-by-side panels**: main view + detail panel joined horizontally
- **Any layout where components don't overlap**
- **The common case** -- this is what you use 95% of the time

**Advantages:**
- Simple string operations, easy to reason about
- No cell-buffer overhead
- Works with any styled string

**Limitations:**
- Cannot overlap content (no z-layering)
- JoinHorizontal pads all blocks to the tallest height with spaces
- No pixel-level positioning

### 5.2 Use Compositor / Layer / Canvas For:

- **Overlays**: dialogs, modals, toasts on top of content
- **Floating elements**: tooltips, dropdowns, context menus
- **Hit testing**: detecting which layer was clicked (mouse support)
- **Any layout where content must overlap**

**Example -- centered dialog overlay:**
```go
func composeOverlay(bg, dialog string, bgWidth, bgHeight int) string {
    dlgW := lipgloss.Width(dialog)
    dlgH := lipgloss.Height(dialog)
    x := (bgWidth - dlgW) / 2
    y := (bgHeight - dlgH) / 2

    comp := lipgloss.NewCompositor(
        lipgloss.NewLayer(bg).Z(0),
        lipgloss.NewLayer(dialog).X(x).Y(y).Z(1),
    )
    return comp.Render()
}
```

**Performance note:** The compositor creates a full cell buffer and iterates
every cell. For a 120x40 terminal that is 4800 cells. This is fine for
overlays but would be wasteful for simple sequential layouts where
JoinVertical does the same job with string concatenation.

### 5.3 Decision Matrix

| Scenario | Tool |
|----------|------|
| Header above content | `JoinVertical` |
| Two panels side by side | `JoinHorizontal` |
| Status bar at bottom | `JoinVertical` |
| Modal dialog over content | `Compositor` |
| Dropdown menu | `Compositor` |
| Toast notifications | `Compositor` |
| Command palette overlay | `Compositor` |
| Tab bar above viewport | `JoinVertical` or string concat |
| Context menu at mouse position | `Compositor` with hit testing |

---

## 6. Common Pitfalls

### 6.1 Height() Is a Minimum, Not Exact

```go
// WRONG: content taller than 10 lines will overflow
style := lipgloss.NewStyle().Height(10).Render(longContent)

// RIGHT: exact height
style := lipgloss.NewStyle().Height(10).MaxHeight(10).Render(longContent)

// ALSO RIGHT: use a viewport for scrollable content
vp := viewport.New(viewport.WithWidth(w), viewport.WithHeight(h))
vp.SetContent(longContent)
output := vp.View()
```

### 6.2 Width() Wraps, MaxWidth() Truncates

```go
// Width(40) will word-wrap text to fit in 40 columns (minus padding/borders)
// MaxWidth(40) will hard-truncate each line at 40 columns

// For a fixed-width box that wraps content:
lipgloss.NewStyle().Width(40).Render(text)

// For a fixed-width box that truncates overflow:
lipgloss.NewStyle().Width(40).MaxWidth(40).Render(text)
```

Note: `MaxWidth` is applied AFTER borders and margins, so it operates on the
total rendered width. `Width` is applied BEFORE borders -- it sets the content
area width.

### 6.3 JoinHorizontal Height Mismatches

When joining blocks of different heights horizontally, the shorter block gets
padded with blank lines. This can cause visual issues if one block has a
background color and the other doesn't:

```go
// Block A: 5 lines with blue background
// Block B: 3 lines with no background
// Result: Block B gets 2 blank lines added (no background) -- looks wrong

// Fix: ensure both blocks have the same height before joining
blockA := lipgloss.NewStyle().Height(5).Width(40).Background(blue).Render(a)
blockB := lipgloss.NewStyle().Height(5).Width(40).Background(green).Render(b)
result := lipgloss.JoinHorizontal(lipgloss.Top, blockA, blockB)
```

### 6.4 Border Size Accounting

Borders ARE included in `Width()` / `Height()` values. Lipgloss subtracts
border sizes internally. A `Width(10)` box with `RoundedBorder()` renders at
10 columns total (8 content + 1 left border + 1 right border).

```go
// To fill exactly `availableWidth` columns:
style.Width(availableWidth).Render(content)  // lipgloss handles border subtraction

// To calculate the inner content width available to children:
innerW := availableWidth - style.GetHorizontalFrameSize()
// GetHorizontalFrameSize = margins + padding + borders
```

### 6.5 String Concatenation vs JoinVertical

```go
// WRONG: simple concatenation doesn't align widths
result := header + "\n" + content + "\n" + footer

// RIGHT: JoinVertical pads all blocks to the same width
result := lipgloss.JoinVertical(lipgloss.Left, header, content, footer)

// EXCEPTION: if all blocks are already the exact same width (e.g., all
// rendered with the same Width(n)), concatenation with \n is fine and
// marginally faster. The TabbedPanel does this (line 183):
//   return tabBar + "\n" + content
// This works because both are rendered at tp.width.
```

### 6.6 View() Called Before WindowSizeMsg

BubbleTea's initial `WindowSizeMsg` arrives asynchronously. The first
`View()` may be called with zero dimensions. Always handle this:

```go
func (m Model) View() tea.View {
    if m.width == 0 || m.height == 0 {
        return tea.NewView("")  // or a loading indicator
    }
    // ...
}
```

### 6.7 Forgetting to Resize Children When Layout Changes

When opening/closing a panel or changing layout mode, you must recalculate
and propagate dimensions to all affected children immediately:

```go
func (m *Model) togglePanel() {
    m.panelOpen = !m.panelOpen
    m.recalcLayout()  // MUST call this
}
```

---

## 7. Viewport: The Scrollable Container

The `bubbles/viewport` is the primary tool for displaying content that may
exceed available space. Key patterns from the source:

### 7.1 How Viewport.View() Works

From `viewport.go` line 728:
```go
func (m Model) View() string {
    w, h := m.Width(), m.Height()
    // ...
    if w == 0 || h == 0 {
        return ""
    }

    contentWidth := w - m.Style.GetHorizontalFrameSize()
    contentHeight := h - m.Style.GetVerticalFrameSize()
    contents := lipgloss.NewStyle().
        Width(contentWidth).     // pad to width
        Height(contentHeight).   // pad to height (minimum)
        Render(strings.Join(m.visibleLines(), "\n"))
    return m.Style.
        UnsetWidth().UnsetHeight().  // already applied above
        Render(contents)
}
```

Key insight: the viewport renders only the visible lines (a slice of the
full content), then uses `Width` + `Height` on a fresh style to ensure the
output fills the allocated space. The outer `Style` handles borders/padding
without double-applying dimensions.

### 7.2 Setting Up a Viewport

```go
vp := viewport.New(
    viewport.WithWidth(contentWidth),
    viewport.WithHeight(contentHeight),
)
vp.Style = lipgloss.NewStyle().
    Border(lipgloss.RoundedBorder()).
    BorderForeground(borderColor)

vp.SetContent(myContent)

// On resize:
vp.SetWidth(newWidth)
vp.SetHeight(newHeight)
```

### 7.3 Viewport + Outer Border (Nested Frame)

When the viewport has its own border style, you need to account for it
when setting dimensions:

```go
borderStyle := lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
frameX, frameY := borderStyle.GetFrameSize()

vp := viewport.New(
    viewport.WithWidth(availableWidth),    // total width including borders
    viewport.WithHeight(availableHeight),  // total height including borders
)
vp.Style = borderStyle  // viewport subtracts frame internally
```

The viewport's `View()` subtracts `Style.GetHorizontalFrameSize()` and
`Style.GetVerticalFrameSize()` internally, so you pass the **total**
available space, not the inner content space.

---

## 8. Reference: How lazycloud Handles Layout

### 8.1 Root Layout (app.go View)

```
+--------------------------------------------------+
| Header (profile, region, breadcrumbs)             |  <- headerH lines
+--------------------------------------------------+
| +----------------------------------------------+ |
| | Content area (table, detail view, etc.)      | |  <- contentH lines
| |                                              | |     = height - headerH - statusH
| +----------------------------------------------+ |
+--------------------------------------------------+
| Status bar (key hints or command input)           |  <- statusH lines
+--------------------------------------------------+
```

The content area has a border (RoundedBorder), so the child view gets
`width-2` and `contentH-2` as its available space.

### 8.2 Split Panel Layout (app.go View with panel)

```
+----------------------------+  +------------------+
| Main content               |  | Side panel       |
| (fainted if panel focused) |  | (tabbed content) |
|                            |  |                  |
+----------------------------+  +------------------+
```

Each side gets its own border. The main width = `width - panelWidth - 1`.
Children are resized via `recalcLayout()`.

### 8.3 Overlay Composition

Dialogs (confirm, picker, help) use the compositor to layer over the
background content:

```go
comp := lipgloss.NewCompositor(
    lipgloss.NewLayer(bg).Z(0),
    lipgloss.NewLayer(dialog).X(centerX).Y(centerY).Z(1),
)
```

---

## 9. Styling Capabilities (LipGloss v2)

### Border Types

| Function | Style | Characters |
|----------|-------|------------|
| `RoundedBorder()` | Rounded corners | `╭╮╰╯─│` |
| `NormalBorder()` | Square corners | `┌┐└┘─│` |
| `ThickBorder()` | Heavy weight | `┏┓┗┛━┃` |
| `DoubleBorder()` | Double lines | `╔╗╚╝═║` |
| `HiddenBorder()` | Invisible (preserves layout) | spaces |
| `BlockBorder()` | Solid blocks | `█` |
| `OuterHalfBlockBorder()` | Half blocks (outer) | `▀▄▌▐▛▜▙▟` |
| `InnerHalfBlockBorder()` | Half blocks (inner) | `▄▀▐▌▗▖▝▘` |

Per-side control: `BorderTop(bool)`, `BorderRight(bool)`, etc.
Per-side colors: `BorderTopForeground(color)`, `BorderForegroundBlend(colors...)`.

### Text Decoration

All return `Style` for chaining:
- `Bold(bool)`, `Italic(bool)`, `Faint(bool)`, `Reverse(bool)`, `Blink(bool)`
- `Underline(bool)`, `UnderlineStyle(UnderlineSingle|Double|Curly|Dotted|Dashed)`
- `Strikethrough(bool)`
- `Transform(func(string) string)` — custom text transformation at render time
- `Hyperlink(url string)` — terminal-supported clickable links

### Color System

```go
lipgloss.Color("#ff00ff")      // Hex color
lipgloss.Color("5")            // ANSI 16-color
lipgloss.Color("134")          // ANSI 256-color
lipgloss.RGBColor{R: 255}     // Direct RGB
lipgloss.NoColor{}             // Transparent/default
```

**Adaptive colors:**
```go
hasDark := lipgloss.HasDarkBackground(stdin, stdout)
ld := lipgloss.LightDark(hasDark)
c := ld(lightColor, darkColor)  // Picks based on terminal background
```

**Terminal capability detection:**
```go
cp := lipgloss.Complete(colorprofile.TrueColor)
c := cp(ansi4Color, ansi256Color, trueColor)
```

**Color manipulation:**
- `Darken(c, 0.2)`, `Lighten(c, 0.2)`, `Alpha(c, 0.5)`, `Complementary(c)`

**Gradients:**
- `Blend1D(steps, color1, color2, ...)` — linear gradient (CIELAB color space)
- `Blend2D(w, h, angle, color1, color2, ...)` — 2D gradient with rotation

### Inline Mode

`Inline(true)` renders as a single line, disabling margins, padding, and
borders. Useful for styling inline text without changing dimensions:

```go
badge := lipgloss.NewStyle().Inline(true).
    Background(accentColor).Padding(0, 1).
    MaxWidth(20).Render("badge text")
```

---

## 10. Patterns Summary

### The Golden Rules

1. **Height budget flows top-down.** Root gets terminal size, computes chrome,
   gives remainder to children.

2. **Always measure rendered chrome.** Use `lipgloss.Height(rendered)`, never
   hardcode `3` for a header. Chrome height changes with borders, wrapping, etc.

3. **Height() + MaxHeight() for exact sizing.** `Height()` alone only sets a
   minimum. Add `MaxHeight()` to prevent overflow.

4. **Use GetFrameSize() for border accounting.** Never hardcode `2` for
   borders. Use `style.GetHorizontalFrameSize()` / `style.GetVerticalFrameSize()`.

5. **Recalculate on every layout change.** Window resize, panel open/close,
   tab switch -- any change that affects dimensions must trigger recalculation
   for all affected children.

6. **Guard View() against zero dimensions.** Return empty string when
   width or height is 0.

7. **Use JoinVertical/JoinHorizontal for sequential layouts.** Only use the
   Compositor for overlapping content (modals, toasts, menus).

8. **Use viewports for scrollable content.** Do not try to implement
   scrolling manually. The viewport handles visible-line slicing, scroll
   position, padding to height, and keyboard navigation.

9. **Children return strings, root returns tea.View.** Only the top-level
   model creates a `tea.View`. Everything else composes strings.

10. **Propagate dimensions immediately.** When a child is pushed onto the
    navigator or a panel is opened, send it the calculated dimensions before
    the next View() call.
