# A6: a controller for the side panel

Status: done on 2026-11-17. The design was approved on 2026-11-16, with all proposed answers. PR 0 (#284) adds the tests in `internal/app/a6_pins_test.go`. PR 1 (#288) fixes H7. PR 2 (#289) adds `panelController` with the same behavior. PR 3 (#290) fixes H1 and H3. PR 4 (#291) fixes H2, H4, H5 and H6.

The code differs from the sketch below in small points. `panelState` also keeps the title. The panel on screen is a `panelState` value, not a pointer. `Back` returns the entry, and the app pops the navigator and calls `Restore`. The focus argument comes last in `Open`, `Unhide` and `Restore`.

## Summary

The side panel shows the details of a resource next to the list. The app keeps its state in eight fields of `Model` in `internal/app/app.go`: `panel`, `panelOpen`, `panelFocused`, `panelTitle`, `panelWidthOverride`, `stashedPanelTitle`, `stashedPanelTabs` and `crossNavHistory`. About fifteen functions and handlers change these fields.

Two kinds of saved panels exist:

- The stash. When the terminal gets too narrow for the panel, the app closes the panel and saves it. When the terminal is wide again, the app opens the saved panel.
- The back history (cross-navigation history). When the user follows a link in the panel to another view, the app saves the panel with the depth of the view that showed it. `backspace` pops back to that depth and opens the saved panel.

M11 and M12 fixed several cases where a saved panel opened over the wrong view. The fixes work because every path that changes the top view calls `dismissPanel` or `trimCrossNavHistory`. Nothing in the types makes this so. A new path that forgets the call brings the bugs back.

This proposal makes four changes:

- A type `panelController` in `internal/app/panel.go` holds all panel state. The app calls its methods and does not touch the fields.
- The stash and each history entry name their owner: the view that showed the panel. A saved panel opens only in its owner, and only when the owner is on top.
- The stash and the history keep the panel itself, not a copy of its text. A restore then brings back the tab, the scroll position and the focus.
- The panel has the focus only when the view on top does not take the keys, and a message from the panel goes only to the panel that sent it.

The same work fixes L7 (hidden tabs keep their old size). The changes that the user can see are in "Visible changes".

## Facts that the design depends on

These facts were checked at `b24d6ed`:

- The navigator holds views as `nav.View` values. `nav.Navigator` has `Contains`, `IsCurrent`, `Depth`, `Views`, `Pop` and `PopTo`. Two views with the same ID can be on the stack at the same time, for example two `s3_objects` views at different depths.
- A message from a view arrives as `nav.Routed{Origin, Msg}`. The app drops a `NavigateMsg`, `NavigateBackMsg`, `TabbedContentMsg` or `ContentLinkActivatedMsg` from a view that is not on top (`app.go` near line 233). `nav.Tag` wraps a command so that its result arrives routed.
- The panel belongs to the app, not to a view. A link in the panel returns a plain `ui.ContentLinkActivatedMsg` from a command (`internal/ui/contentview.go`). It is not routed, so the guard does not check it. The handler then sends a plain `NavigateMsg` from a second command.
- `openTabbedPanel` always gives the panel the focus. The key handler gives every key to a view that takes input (`capturingInput`) before it looks at the panel. The border, the dimming and the hints follow `panelFocused`.
- `savePanelState` copies the title and the text, format and links of each tab. `ui.NewTabbedPanel` starts on tab 1. So the stash and the history lose the active tab, the scroll position, the cursor, the wrap setting and the focus.
- `TabbedPanel.SetSize` resizes only the viewer of the active tab (`tabbedpanel.go` line 129). A viewer gets its size only when it is made.
- `trimCrossNavHistory` keeps the entries with `navDepth < Depth()`. `finishForm` (`forms.go`) pops a form without a trim.
- A link to a row of the view on top only moves the cursor (`FocusResourceMsg`). It pushes no view.

## Hazards

A search with adversarial checks found these hazards. Each was traced in the code, and most were shown with a test.

- H1. A narrow and then wide resize opens the panel on tab 1, with the focus, and at the top of the text. Back traversal does the same. The tab that held the link is lost.
- H2. A panel that opens while the view takes input looks focused, but the keys go to the view. This happens after a restore from the stash, and when a detail result arrives while the filter is open. `tab` cannot move the focus, and the status bar shows panel keys that do nothing. When the filter closes, the keys suddenly go to the panel.
- H3. A link to a row of the view on top leaves a history entry at the current depth. This is the "DLQ Source Queues" link of SQS queues. Then:
  - `backspace` opens the old panel next to a cursor on another row.
  - A child view that closes with `q` or `esc` removes the entry, but a form that closes keeps it.
  - A detail fetch of the new row that ends after `backspace` replaces the restored panel, and the history is empty, so that panel is gone.
- H4. A link in the panel and the narrow fallback of `TabbedContentMsg` send their `NavigateMsg` through a command, without a route. A key that the app handles in between, for example `q`, changes the stack first. Then the link pushes its view onto the wrong view, and a history entry points at a depth that is gone.
- H5. Help builds its list of keys when it opens. A resize that stashes or restores the panel does not update the list, so help shows panel keys without a panel, or the reverse.
- H6. The header shows "← bksp" and the title of the last history entry while a form is on top. The form takes `backspace`, so the key does not do what the header says. The status bar already hides the hint in this case.
- H7 (L7). A tab that was shown before keeps its old width after a resize or a change of the panel width. With `>` pressed four times on a 200-column terminal, the screen is 217 columns wide.

## Design

### Types

```go
// panelController holds the side panel and the panels that the app saved.
type panelController struct {
	open    *panelState   // the panel on screen; nil when none
	stash   *panelState   // the panel that a narrow terminal hides
	history []historyEntry // back traversal, oldest first, at most 10
	widthOverride int     // 0 = 40% of the window
	gen     uint64        // counts the panels that opened
}

// panelState is one panel and the view that showed it.
type panelState struct {
	panel   *ui.TabbedPanel
	owner   nav.View
	focused bool
	gen     uint64
}

// historyEntry is a panel that a link left. depth is the depth of owner.
type historyEntry struct {
	state panelState
	depth int
}
```

The methods take the navigator, or the facts they need from it, as arguments. `panelController` does not hold the navigator.

- `Open(title, tabs, owner, focus)` opens a new panel for owner. It drops the stash.
- `Close()` closes the panel on screen and keeps the stash and the history.
- `Dismiss()` closes the panel on screen and drops the stash. It is today's `dismissPanel`.
- `Hide()` moves the panel on screen to the stash, for a narrow terminal.
- `Unhide(top nav.View)` opens the stash again when its owner is `top`. Otherwise it drops the stash.
- `Push(depth)` adds the panel on screen to the history.
- `Back(nav)` pops the newest entry. It pops the navigator to the depth of the entry, when the view at that depth is still the owner, and opens the saved panel. An entry whose owner is gone is dropped, and `Back` tries the next one.
- `Trim(nav)` drops each entry whose owner is not at its depth in the navigator. It also drops each entry whose depth is not below the top, as `trimCrossNavHistory` does today. So `q` or `esc` in the view that a link opened drops the entry (D7).
- `Reset()` drops everything. `resetNav` calls it.

### Owners

The owner is the view instance, not the view ID. Two views with the same ID can be on the stack, so an ID and a depth together can still name the wrong view after a pop and a push. A view instance is exact.

A saved panel opens only in its owner. The stash opens only when its owner is on top. A history entry opens only when its owner is still at the depth of the entry. So a path that forgets `Dismiss` or `Trim` can no longer open a panel over the wrong view. The app still calls them, to free memory and to keep the back hint right.

### Saved panels keep the panel

The stash and each history entry keep the `*ui.TabbedPanel` itself and the focus. A restore calls `SetSize` with the current size. The tab, the scroll position, the cursor and the wrap setting come back as the user left them. `savePanelState` and the copy of the links go away.

### Focus

`Open` takes the focus as an argument. The app passes `!m.capturingInput()`. So a panel that opens while the filter is open does not take the focus, and `tab` moves the focus after the filter closes. A restore from the stash or from the history keeps the focus that the panel had, but only while the view does not take input. `Unhide` and `Back` take the same argument as `Open`.

### Messages from the panel

Each panel gets a number (`gen`) when it opens. The app wraps each command of the panel, as `nav.Tag` does for a view, so that its result arrives as `panelRouted{gen, msg}`. The app drops a result whose `gen` is not the number of the panel on screen. A link from a panel that closed, or from an older panel, then does nothing.

The handler of a link, and the narrow fallback of `TabbedContentMsg`, call the navigation code directly. They do not send a second `NavigateMsg` through a command. So no key can come in between.

### Links to the view on top

A link to a row of the view on top adds no history entry. The panel of the new row opens, as a detail result. `backspace` does nothing new in this case. This removes the three parts of H3.

### The rest of the app

- `contentSize`, `resizeViews` and `View` ask the controller for the open panel and its width.
- When the panel opens, closes, hides or unhides while help is open, the app builds the help keys again (H5).
- The header shows the back label only when the view on top does not take input (H6).
- `TabbedPanel.SetSize` resizes the viewer of every tab that exists (H7, L7).

## Invariants

Each PR keeps every invariant, except where the PR names a change.

- I1. The panel on screen belongs to the view on top. Each change of the top view closes it, except a link, which saves it to the history first.
- I2. The stash opens only in its owner, and only when the owner is on top. A change of the top view drops the stash.
- I3. A history entry opens only in its owner, at the depth of the entry. Back traversal pops to that depth. The history holds at most 10 entries.
- I4. `resetNav` (a session switch, a theme switch and `:home`) drops the panel, the stash and the history.
- I5. A late result from a view that is not on top is dropped (the `IsCurrent` guard).
- I6. With the panel open and focused, the panel gets the keys. `tab` moves the focus while the view does not take input. `q` closes an open panel before it pops a view.
- I7. `contentSize` counts the panel only while it is open. The width override stays when the panel closes and opens again.
- I8. In a narrow terminal, `TabbedContentMsg` pushes its first tab as a full-screen view.
- I9. The border, the dimming and the hints show the focus. From PR 4, the focus is on the panel only when the keys go to the panel.

## Visible changes

- After a narrow and then wide resize, the panel comes back on the same tab, at the same scroll position, with the same focus (PR 3).
- `backspace` after a link brings back the panel on the tab that held the link (PR 3).
- A link to a source queue in the DLQ panel of SQS queues adds no back entry (PR 3).
- A panel that opens while the filter is open does not take the focus (PR 4).
- A link from a panel that closed does nothing (PR 4).
- Help lists the panel keys only when a panel is open, also after a resize (PR 4).
- The header hides the back label while a form is on top (PR 4).
- Every tab of the panel has the width of the panel (PR 1).

## PR plan

Each PR runs the unit tests, `go vet`, golangci-lint with gosec and the LocalStack smoke tests.

- PR 0, tests only. Tests pin H1 to H7 as they are today, each marked with the PR that changes it. Tests pin the behavior that must stay: I1 to I9, a link to another view and back, a chain of two links, back after a narrow terminal, the depth cap, and `q` after a link. A test named `TestA6Hazard_...` pins a hazard. The PR that fixes it flips the test and renames it `TestA6_<hazard>_<new behavior>`.
- PR 1. `TabbedPanel.SetSize` resizes every viewer (H7, L7).
- PR 2. Add `panelController` and move the fields and the functions into it, with the same behavior. Only `internal/app` changes.
- PR 3. Owners and saved panels: the stash and the history keep the panel, its focus and its owner (H1). A link to the view on top adds no entry (H3).
- PR 4. Focus and messages: `Open` takes the focus (H2). Panel commands carry `gen`, and the link and the narrow fallback navigate directly (H4). Help and the header follow the panel (H5, H6). The docs describe the controller.

## Risks

- R1. A history entry keeps a view that was popped alive until `Trim` drops it. `Trim` runs after each pop, and the history holds at most 10 entries.
- R2. A saved panel keeps its old size. `Unhide` and `Back` call `SetSize` before the panel shows. `TestA6Keep_RestoredPanelHasTheCurrentWidth` checks it.
- R3. A panel command can return a batch or a sequence. The wrapper must wrap each result, as `nav.Tag` does.
- R4. Many tests in `internal/app` read the panel fields. PR 2 moves them to the controller, so it changes many test lines without a change of behavior.
- R5. Visual mode or a search in the panel stays on in a saved panel. This is the state that the user left, so the design keeps it.

## Decisions

D1. What is the owner of a saved panel?
Proposed: the view instance, with the depth for the history. The tracker says "owner view ID and depth", but an ID can repeat on the stack.

D2. Do the stash and the history keep the panel itself, or a copy of its text?
Proposed: the panel itself, with its focus. A restore then shows the panel as the user left it (H1).

D3. Does a link to a row of the view on top add a history entry?
Proposed: no (H3). The other choice is to keep the entry and also save the row, so that back moves the cursor back. That needs a resource ID in the panel, which no view gives today.

D4. What happens when a panel opens while the view takes input?
Proposed: it opens without the focus (H2). The other choice is to not open it, but then a detail result is lost.

D5. Do messages from the panel carry the number of the panel, and do the link and the narrow fallback navigate directly?
Proposed: yes to both (H4).

D6. With the panel open but not focused, `q` closes the panel and `esc` pops the view. Change it?
Proposed: no. `q` closing the panel first is I6, and `esc` belongs to the view.

D7. `q` or `esc` in the view that a link opened goes back without the panel and drops the entry. Change it?
Proposed: no. `backspace` is the key that goes back with the panel.

D8. The panel is a snapshot. It stays open with old content after the view deletes or refreshes the resource. Change it?
Proposed: no, not in A6. A fix needs a resource ID in the panel.

D9. Add L7 to A6?
Proposed: yes, as PR 1.

D10. A late reply of the terminal with its background color resets the views when the theme setting is auto. The search found this, but it is about the theme, not the panel. Where does it go?
Proposed: a new tracker item, L34, for A8.
