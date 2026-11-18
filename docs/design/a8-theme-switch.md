# A8: switch the theme without a reset

Status: done on 2026-11-17. The design was approved on 2026-11-17, with all proposed answers. PR 0 (#292) adds the tests in `internal/app/a8_pins_test.go`. PR 1 (#293) adds `ui.Restyler` and `Restyle` on the table and the views. PR 2 (#294) changes the app and fixes H1 to H5.

The code differs from the design in two points. `ContentView` keeps its cache key, because its cached lines use no style that depends on NoColor. `FormView` also implements `Restyler`: a test of PR 2 showed that huh keeps the drawn fields of a group until the next update, so the fact about huh below was wrong. The app removes `closePrompts`, because no code calls it after the change.

## Summary

Tracker item A8 asks for a theme context: pass the theme, the styles, ReadOnly and the nerd font state into the components, and resolve the styles at render time. Two results are expected. A theme switch does not rebuild the views (L17), and the tests can run in parallel (T2).

The code changed after the tracker was written. Most components already read the styles when they draw. Only three places keep colors from an earlier theme: the table, the event log, and the help overlay. So a theme switch can keep the views if the app tells these places to draw again with the new colors. A context object that goes into every component is not necessary for L17.

This proposal makes four changes:

- A view that keeps colors implements a new interface `ui.Restyler` with one method, `Restyle()`. `Table.Restyle` takes the new table styles and styles its cells again.
- A theme switch calls `Restyle` on each view on the stack and refreshes help. It does not reset the navigation, and it does not close the prompts.
- The theme picker does not ask for a confirmation below the home view, because nothing is lost.
- A theme that the user picks ends the auto setting, so a late reply of the terminal cannot replace it (L34). A late reply that arrives before a pick switches the theme in place, without a reset.

The theme, the styles, ReadOnly and the nerd font setting stay package variables in `internal/ui` (decision D1). T2 leaves A8 (decision D3).

## Facts that the design depends on

These facts were checked at `521e75d`:

- `setTheme` (`internal/app/app.go`) sets `ui.ActiveTheme`, calls `ui.RebuildStyles`, declines each open prompt with `closePrompts`, calls `resetNav`, and makes a new confirm and a new picker. `resetNav` closes the panel, the stash and the back history, closes all views, and starts again from a new home view.
- The theme picker asks "Switching themes resets the current view. Continue?" when the stack is deeper than the home view.
- `tea.BackgroundColorMsg` sets the auto theme only while `autoTheme` is true, and clears it. `applyTheme` does not clear `autoTheme`. So when the reply comes after the user picked a theme, the reply replaces that theme, and it resets the views.
- These components read `ui.S` or `ui.ActiveTheme` each time they draw: the header, the status bar, the command bar, the toasts, the confirm, the picker, the filter, the spinner, the animations of the home view, the tab bar of the panel, `listcore_ui.go`, and the `View` functions of the event log and SQS messages.
- `huh` fields call the theme function of `ui.HuhTheme` each time they draw (`field_input.go`, `Input.View`). (Found wrong in PR 2: a group keeps the drawn fields in a viewport and draws them again only on an update, `group.go`, `buildView`.)
- `ContentView` caches its drawn lines with the theme name, and draws again when the name changes (`contentview.go`, `screen.theme`). The cache key does not hold the NoColor setting. It does not need it, because the cached lines use no style that depends on NoColor, and `paintHighlight` reads NoColor each time it draws.
- `NewTable` sets the table styles once, from `tableStyles(ActiveTheme)`. `Table.showCells` runs the `Style` function of each column once per row, when the row is added, and keeps the result in `t.cells`. The `Style` functions are `ui.StateColor` and `ui.CountColor`, which read `ActiveTheme`. The cells of a `Row` hold plain text.
- Four view types own a `ui.Table`: `listCore` (all seven resource lists), `S3Objects`, `S3Versions` and `SQSMessages`.
- `EventLog.refreshContent` writes colored lines into its viewport. It runs on a resize, a filter change and some keys, not on a theme switch.
- `HelpOverlay` writes its colored content into a viewport when it opens and in `SetHints`.
- `ui.SetNoColor` runs once, on `tea.ColorProfileMsg`. Its comment says that no table exists yet. Nothing checks this.
- ReadOnly is an atomic value that the views, the status bar and the guards of the AWS services read when they need it. A mode switch works without a rebuild today. The nerd font setting is set once in `main.go` and never changes.
- The tests take about 10 seconds (`internal/app` 10 s, `internal/views` 7 s, run in parallel by package). No test calls `t.Parallel()`. Apart from ReadOnly and the theme, the tests also share the global event log (`eventlog.Entries`).

## Hazards

These are the hazards of the theme switch today. PR 0 pins each one with a test.

- H1 (L17). A theme switch below the home view closes all views, the panel, the stash, the back history and the open forms. The loaded rows, the cursor, the filter, the selection and the scroll position are lost.
- H2. Below the home view, the theme picker asks for a confirmation, because of H1.
- H3. The switch declines each open prompt and throws away the confirm and the picker.
- H4 (L34). A reply of the terminal that comes after the user opened views does H1 and H3.
- H5 (L34). A reply of the terminal that comes after the user picked a theme replaces that theme.

The change also removes a weak point. When `tea.ColorProfileMsg` comes after a table was made, the cursor row of that table does not use reverse video. Today the order of the messages prevents this.

## Design

### The interface

```go
// Restyler is a view or a component that keeps text drawn with the styles
// of a theme. Restyle draws that text again with the active theme.
type Restyler interface {
	Restyle()
}
```

The interface is in `internal/ui`, next to the styles, because the components that implement it are in `ui` and in `views`.

### The table

`Table.Restyle` sets `tableStyles(ActiveTheme)` on the inner table, makes `t.cells` again from `t.rows` with `showCells`, and calls `refresh` with the cursor key. The rows, the order, the filter, the selection and the cursor stay. The search text stays, because it holds no colors.

### The views

These views implement `Restyler`:

- `listCore` calls `table.Restyle()`. The seven lists get it from the embedded core.
- `S3Objects`, `S3Versions` and `SQSMessages` call `table.Restyle()`.
- `EventLog` calls `refreshContent()`.

A conformance test builds the view of each route in `routes` and finds each `ui.Table` field with `reflect`. A view type that has one must implement `Restyler`. A new view with a table then cannot forget the method.

### The app

```go
// setTheme makes a theme active and draws the open views again with it.
func (m Model) setTheme(name string) Model {
	t, ok := ui.Themes[name]
	if !ok {
		return m
	}
	ui.ActiveTheme = t
	ui.RebuildStyles()
	m.restyle()
	return m
}

// restyle draws the views on the stack and help again with the active styles.
func (m *Model) restyle() {
	for _, v := range m.nav.Views() {
		if r, ok := v.(ui.Restyler); ok {
			r.Restyle()
		}
	}
	m.refreshHelp()
}
```

The views are pointers, so the call changes the view in the stack. The panel needs no call: its tab bar reads the styles when it draws, and each `ContentView` draws again when the theme name changes. The saved panels in the stash and the back history are the same panel values, so they draw with the new theme when they come back. `setTheme` no longer returns a command, because it no longer declines prompts or starts the home view.

The handler of `tea.ColorProfileMsg` calls `restyle` after `ui.SetNoColor`. Then the order of the start messages no longer matters.

The theme picker applies the theme at once at every depth. `applyTheme` sets `autoTheme` to false. The handler of `tea.BackgroundColorMsg` keeps its test of `autoTheme`, and calls the new `setTheme`.

### What does not change

- ReadOnly, the nerd font setting and NoColor stay package variables in `internal/ui`.
- The panel, the overlays and the navigator do not change.
- The animation of the home view keeps its tick. A switch on the home view does not stop it, and a switch below the home view does not start a second chain.

## Visible changes

- A theme switch keeps the open views, their rows, cursor, filter, selection and scroll position, the panel, and the back history. Only the colors change.
- The theme picker does not ask "Switching themes resets the current view. Continue?".
- An open prompt stays open over a theme switch.
- With the auto setting, a slow terminal no longer closes the views that the user opened, and no longer replaces a theme that the user picked.

## Plan

Each PR keeps the tests green.

- PR 0. This design, and the tests in `internal/app/a8_pins_test.go`. `TestA8Hazard_*` pins H1 to H5 as they are today. `TestA8Keep_*` pins what stays: the new theme reaches the header, a table, a panel and an open form, and ReadOnly survives a switch.
- PR 1. `ui.Restyler`, `Table.Restyle`, and `Restyle` on `listCore`, `S3Objects`, `S3Versions`, `SQSMessages` and `EventLog`, with unit tests and the conformance test. The app does not call it yet, so the behavior is the same.
- PR 2. The app: the new `setTheme` and `restyle`, the call after `SetNoColor`, no confirmation in the theme picker, and `autoTheme` cleared by a pick. The pins of H1 to H5 flip. The tests that expect a reset change: `TestA12Pin_ThemeSwitchWithEachViewPromptOpen`, `TestThemeSwitchDeclinesOpenPrompts`, `TestThemeSwitchDropsTheDeclineForAViewPrompt`, `TestOverlayPin_ThemeSwitchAsksBelowHome` and `TestThemeSwitchRestartsHomeAnimation`. `docs/ARCHITECTURE.md` describes the restyle. The tracker marks L17, L34 and A8 done.

## Decisions

- D1. Scope. Proposed: keep the theme, the styles, ReadOnly and the nerd font setting as package variables, and add the restyle. A context object would change about 84 places in 27 files, and L17 does not need it. ReadOnly and the nerd font setting already work without a rebuild. Other answer: a `ui.Env` value that the app owns and passes to each component and view.
- D2. How the views get the new theme. Proposed: the app calls `Restyle` directly on each view on the stack. Other answer: a `ThemeChangedMsg` that each view handles in `Update`. A message goes through the input and loading paths of each view, and the app cannot know that the view handled it.
- D3. T2. Proposed: remove T2 from A8, and keep it open as a test item with a low priority. The tests take about 10 seconds, and parallel tests also need a separate event log for each test. Other answer: close T2.
- D4. The confirmation in the theme picker. Proposed: remove it, because the switch loses nothing. Other answer: keep it.
- D5. Open prompts. Proposed: they stay open over a switch, and get no decline. Other answer: decline them as today.
- D6. A late reply of the terminal before any pick. Proposed: it switches the theme in place, because the user asked for the auto setting and the switch now loses nothing. Other answer: ignore a reply that comes after the first view opened.
