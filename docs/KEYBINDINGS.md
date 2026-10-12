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

- `Mode` marks the keys that change AWS resources (`ModeReadWrite`). The app blocks these keys in ReadOnly mode. The status bar and the help overlay also read it.
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

- `key.Matches` checks the key string and the enabled state of the binding.
- The view matches its own `keys` struct. `ApplyModeAll` does not change this struct, so a ReadWrite key still matches in the view. The ReadOnly block is in the app (see below).
- Modal guards (filter active, search active) stay above the switch.

## ReadOnly / ReadWrite Mode

In ReadOnly mode, the app blocks each key that a binding with `ModeReadWrite` has in the `KeyMap()` of the view on top:

- The app checks the key before the view gets it. It shows the toast "ReadOnly mode — press W to switch".
- The check compares the keys and ignores the enabled state. `ApplyModeAll(hints)` in `KeyMap()` disables only the returned copies, for the status bar and the help overlay.
- The guarded AWS services check the mode again when the action runs. See Access Mode in `ARCHITECTURE.md`.
- The status bar hides ReadWrite hints in ReadOnly mode. The help overlay shows all hints and marks ReadWrite keys with an `[RW]` badge.

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

## Built-in Table Navigation

These keys move the cursor in every table-based view. They come from `ui.DefaultTableKeyMap()`, which starts from the bubbles `table.DefaultKeyMap()`. A view can replace them with `Table.SetKeyMap`.

| Key | Action |
|-----|--------|
| `j` / `↓` | Line down |
| `k` / `↑` | Line up |
| `f` / `pgdown` | Page down |
| `b` / `pgup` | Page up |
| `ctrl+d` | Half page down |
| `ctrl+u` | Half page up |
| `g` / `home` | Go to top |
| `G` / `end` | Go to bottom |

Note: `DefaultTableKeyMap()` leaves out three keys of the bubbles defaults, because views bind them to actions. `space` selects a row, `d` describes, and `u` presigns.

## What Uses String-Based Dispatch

Modal UI components that intercept all input when visible and don't expose hints:

- `filter.go` -- text input for table filtering
- `commandbar.go` -- command palette with fuzzy search
- `picker.go` -- selection popup (theme, profile, region, sort)
- `confirm.go` -- destructive action confirmation
- `tabbedpanel.go` -- numeric tab switching (delegates content keys to ContentView)
- `help.go` -- the help overlay itself (j/k scroll, filter input, esc/? close)
- `form.go` -- multi-field forms via `huh/v2` (pushed as nav view, not overlay)

These stay string-based because they're self-contained, don't participate in the hint system, and don't benefit from `key.Binding`.

## Input Capture

When the view on top takes text input, the app skips all global key handlers except `ctrl+c`. Single-letter global keys (`q`, `T`, `P`, `R`, `W`, `L`, `:`, `?`) and `tab` are typed text, and `backspace` deletes a character. The status bar hides the global hints.

A view reports text input with `nav.InputCapturer`. Its `CapturingInput()` returns true while an input is active:

- A list view captures while its filter is active. The S3 views also capture during bucket create and the copy or move destination. The AMI view also captures during the public search.
- A `FormView` always captures. Huh handles the field keys (`tab` and `shift+tab`, `enter` to advance, `esc` to cancel).

## Adding a New Keybinding

1. Add a `key.Binding` field to the relevant KeyMap struct
2. Initialize it in the default constructor with `key.NewBinding(key.WithKeys(...), key.WithHelp(...))`
3. Use `key.Matches(msg, v.keys.NewKey)` in the dispatch switch
4. Add the binding to the view's `KeyMap()` return slice. If the key changes AWS resources, set `Mode: ui.ModeReadWrite`, so the app blocks it in ReadOnly mode.
5. That's it -- status bar and help overlay pick it up automatically

## Adding a New View

1. Define a private `<view>KeyMap` struct in the view file
2. Create a `default<View>KeyMap` package-level variable
3. Add `keys <view>KeyMap` as a field on the view struct, initialize in the constructor
4. Implement `KeyMap() []ui.HintBinding` on the view
5. Use `key.Matches` in the `Update()` method
