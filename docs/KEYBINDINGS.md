# Keybinding System

## Overview

Keybindings use the `key.Binding` pattern from `charm.land/bubbles/v2/key`. Each binding is defined once and consumed by both dispatch (`key.Matches`) and hint rendering (status bar, help overlay). This replaces the earlier string-based `msg.String()` dispatch.

## Core Types

### `HintBinding` (`internal/ui/keys.go`)

Wraps `key.Binding` with metadata the help overlay and status bar need:

```go
type HintBinding struct {
    key.Binding
    Mode     HintMode // ModeAny | ModeReadOnly | ModeReadWrite
    Category string   // help overlay grouping; empty = "Current View"
}
```

- `Mode` controls visibility in status bar/help and dispatch eligibility via `ApplyModeAll()`
- `Category` groups bindings in the help overlay (Current View, Navigation, Panel, Global)
- Builder methods: `.WithMode()`, `.WithCategory()`

### `AppKeyMap` (`internal/ui/keys.go`)

Global/panel keybindings owned by the app model. Includes quit, pickers (theme/profile/region/mode), command bar, help, tab toggle, and panel controls (close/editor/resize).

### `ContentViewKeyMap` (`internal/ui/keys.go`)

Panel content viewer bindings (j/k scroll, g/G jump, V visual, y yank, etc.). Used by `ContentView` and referenced by the app when building panel hints.

### Per-View KeyMaps (`internal/views/*.go`)

Each view defines a private `<view>KeyMap` struct and `default<View>KeyMap` variable in its own file. The struct fields are used for `key.Matches` dispatch in `Update()`, and the `KeyMap()` method returns `[]HintBinding` for the status bar and help overlay.

## Dispatch Pattern

```go
case tea.KeyPressMsg:
    switch {
    case key.Matches(msg, v.keys.Filter):
        // handle filter
    case key.Matches(msg, v.keys.Details, v.keys.Describe):
        // multiple keys, same action
    }
```

- `key.Matches` checks both the key string AND the binding's enabled state
- Disabled bindings (e.g. ReadWrite keys in ReadOnly mode) never match
- Modal guards (filter active, search active) remain above the switch as before

## ReadOnly / ReadWrite Mode

Bindings tagged with `ModeReadWrite` are disabled when `ui.ReadOnly == true`:

- Views call `ui.ApplyModeAll(hints)` in their `KeyMap()` method before returning
- `key.Matches` returns false for disabled bindings, so dispatch naturally blocks them
- The status bar and help overlay filter by mode to hide irrelevant hints
- The `[RW]` badge in the help overlay is driven by `hint.Mode == ModeReadWrite`

## Hint Flow

```
View.KeyMap() []HintBinding
        |
        v
currentKeyHints()          --> status bar (bottom)
collectAllKeyHints()       --> help overlay (? key)
        |
        +-- view hints     (Category: "")
        +-- navigation     (Category: "Navigation")
        +-- panel          (Category: "Panel")
        +-- global         (Category: "Global")
```

`currentKeyHints()` is context-sensitive (panel focused vs main view). `collectAllKeyHints()` returns everything with categories for the help overlay.

## What Uses String-Based Dispatch

Modal UI components that intercept all input when visible and don't expose hints:

- `filter.go` -- text input for table filtering
- `commandbar.go` -- command palette with fuzzy search
- `picker.go` -- selection popup (theme, profile, region, sort)
- `confirm.go` -- destructive action confirmation
- `tabbedpanel.go` -- numeric tab switching (delegates content keys to ContentView)
- `help.go` -- the help overlay itself (j/k scroll, filter input, esc/? close)

These stay string-based because they're self-contained, don't participate in the hint system, and don't benefit from `key.Binding`.

## Adding a New Keybinding

1. Add a `key.Binding` field to the relevant KeyMap struct
2. Initialize it in the default constructor with `key.NewBinding(key.WithKeys(...), key.WithHelp(...))`
3. Use `key.Matches(msg, v.keys.NewKey)` in the dispatch switch
4. Add the binding to the view's `KeyMap()` return slice (with `.WithMode()` if mode-sensitive)
5. That's it -- status bar and help overlay pick it up automatically

## Adding a New View

1. Define a private `<view>KeyMap` struct in the view file
2. Create a `default<View>KeyMap` package-level variable
3. Add `keys <view>KeyMap` as a field on the view struct, initialize in the constructor
4. Implement `KeyMap() []ui.HintBinding` on the view
5. Use `key.Matches` in the `Update()` method
