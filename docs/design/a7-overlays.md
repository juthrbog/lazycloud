# A7: an overlay stack and replies to the view that asked

Status: done. PR 0 to PR 8 (#265 to #273). Approved on 2026-11-15. The owner accepted the proposed answer to each question in "Decisions": A7 stops at PR 8, esc reaches the view, PR 7 adds one rule for all prompt requests, and the clear hint shows only on the sort picker.

## Summary

The app has four overlays: the confirm prompt, the picker, the help overlay and the command bar. Each overlay is a field of `app.Model`. The app checks them in a fixed chain of `if` statements, in three places: key routing (`app.go:324-349`), drawing (`app.go:1492-1519`) and resize (`app.go:379`). Only the picker gets a new size on resize.

A reply from an overlay uses string IDs. The app handles its own prompts by an action string (`"app_quit"`, `"theme_switch"`, `"form_discard"`) or a picker ID (`"theme"`, `"region"`, `"service"`). It sends every other reply to the view that is on top at the time of the reply, not to the view that asked. The picker reports esc as `Selected: -1` and ctrl+x as `Selected: -2, Value: "_clear"`. Four files check for `"_clear"`: `listcore.go`, which the seven resource lists share, `s3_objects.go`, `s3_versions.go` and `sqs_messages.go`.

This proposal makes four changes:

- An `ui.Overlay` interface and a stack of open overlays in the app. The top of the stack gets the keys. The app draws the stack from bottom to top, and every overlay gets a new size on resize.
- A reply goes to the view that asked, by its instance (`nav.View`), through `nav.UpdateView`. If that view is gone, the app drops the reply and writes a debug line.
- The app's own prompts use typed handler functions in place of action strings, picker IDs and the fields `pendingTheme` and `pendingSession`.
- The picker returns a typed outcome: `Picked`, `PickCancelled` or `PickCleared`. Only the sort picker can be cleared.

The user sees almost no change. The changes that the user can see are listed in "Visible changes".

## Facts that the design depends on

These facts were checked at `d07300a`:

- `nav` imports `ui`, and `ui` imports `msg`. `msg` imports no other package of the app. So a type in `msg` or `ui` cannot hold a `nav.View`. The stack and the reply route live in `internal/app`.
- `nav.UpdateView` tags each command that a view returns. So a request from a view arrives as `nav.Routed` with its origin, and the app knows which view asked.
- `setTheme` calls `resetNav`, which replaces every view. After a theme switch, a reply to an old view finds no view on the stack.
- The picker and the confirm prompt have value receivers on `Update`. `app.Model` is a value, and Bubble Tea copies it on each update.
- Every sequence of prompts that can happen today closes one prompt before it opens the next one: the service picker and then the feature picker, the theme picker and then the theme confirm, the region or profile picker and then the session confirm. So one instance of each overlay kind is enough.

## Scores of the three drafts

Each score is from 1 (poor) to 10 (excellent). A judge checked the claims of each draft against the code.

| Draft | Score | Why |
|---|---|---|
| minimal (small PRs, behavior stays the same) | 8 | It is the most accurate against the code. It keeps the overlay components as value fields, so copying `Model` stays safe. |
| type-safety (generic typed requests and replies) | 7 | It covers A7 most fully and removes every string ID. But some of its steps are not needed, and it rewrites about 150 test literals. |
| risk (invariants first) | 7 | Its list of invariants is the best, and it adds the `Clearable` flag and the record of who opened a form. Some of its claims about the code are wrong. |

The plan uses the minimal draft as its base. It takes the invariants, the `Clearable` flag, the move of the reply types to `msg` and the form record from the risk draft. It takes the test helpers from the type-safety draft. The generic typed tags of the type-safety draft are an optional last phase (decision D1).

## Design

### Typed picker outcome

```go
type PickOutcome uint8

const (
	Picked        PickOutcome = iota // the zero value, so PickerResultMsg{Selected: 2} is still a pick
	PickCancelled                    // esc, a skipped prompt, or a closed prompt
	PickCleared                      // ctrl+x on a picker that can be cleared (sort)
)

type PickerResultMsg struct {
	ID       string // for the pickers of the views; A12 removes it
	Outcome  PickOutcome
	Selected int    // the index of the option, only when Outcome is Picked
	Value    string
}

func (r PickerResultMsg) Picked() bool { return r.Outcome == Picked }
```

The picker gets a `Clearable` flag. It shows the "ctrl+x clear" hint only when the flag is set. The helper `views.applySortPick(t *ui.Table, r PickerResultMsg) bool` replaces four copies of the sort reply code (`listcore.go`, `s3_objects.go`, `s3_versions.go`, `sqs_messages.go`).

### The overlay interface

```go
// Overlay is an overlay that the app keeps in its stack.
type Overlay interface {
	Visible() bool
	Update(tea.Msg) tea.Cmd // changes the overlay in place
	View() string
	SetSize(width, height int)
}

// AsOverlay adapts a value component, such as Confirm, to Overlay.
func AsOverlay[T valueModel[T]](p *T) Overlay
```

The components stay value fields of `app.Model`. The adapter holds a pointer to the field. Each component gets a `SetSize` method. The help overlay lays out its text again when the size changes while it is open.

### The stack

```go
type overlayKind uint8

const (
	ovHelp overlayKind = iota
	ovCommandBar
	ovConfirm
	ovPicker
)

// replyHandler handles the reply to a prompt of the app. It must use the
// Model that it gets and never keep a Model of its own.
type replyHandler func(m Model, reply tea.Msg) (tea.Model, tea.Cmd)

type replyRoute struct {
	replyTo nav.View     // a view asked: the reply goes to this view
	onReply replyHandler // the app asked
	quit    bool         // the quit confirm: a second ctrl+c quits at once
}

type overlayEntry struct {
	kind  overlayKind
	route replyRoute
}

// overlayStack is bottom first. push and prune make a new slice, so two
// copies of Model never share it.
type overlayStack []overlayEntry

// overlayReplyMsg carries a reply with its route, because the app pops the
// entry when the overlay closes.
type overlayReplyMsg struct {
	route replyRoute
	msg   tea.Msg
}
```

Key routing: the overlay on top of the stack gets each key press. If the top is the quit confirm and the key is ctrl+c, the app quits. After the key, the app removes the entries whose overlay closed, and it wraps a reply in `overlayReplyMsg`.

Drawing: the app draws the views, then the overlays from bottom to top, then the toasts. The command bar input still replaces the status bar.

Resize: on each `tea.WindowSizeMsg` and after `setTheme`, the app calls `SetSize` on all four overlays.

### Reply delivery

- If the route has `replyTo`, the app sends the reply with `nav.UpdateView`. If the view is no longer on the stack, it drops the reply and writes a debug line.
- If the route has `onReply`, the app calls it.
- For a request without an origin, the app keeps today's path until the cleanup PR.

The app's own prompts use the helpers `askConfirm`, `askConfirmQuick` and `pick`, each with a handler. The handlers keep the data that the fields `pendingTheme` and `pendingSession` keep today, so these fields go away. Before `setTheme` rebuilds the components, `closePrompts` sends a decline on the route of each open prompt.

### Forms

A form stays a view on the navigation stack. When a view asks for a form, the app records the form and the view that asked. A `FormResultMsg` pops the form only when that form is on top, and the result goes to the view that asked. The discard confirm keeps the form in its handler, and it acts only when that form is still on top. Today it reads `nav.Current().(*ui.FormView)`.

### Package placement

`PickOutcome`, the confirm reply, the picker reply and `RequestFormDiscardConfirmMsg` move to `internal/msg`, so the views stop importing `ui` for replies. `ui` keeps type aliases until the cleanup PR, so about 145 test literals compile without change.

### Tests

PR 0 adds two typed helpers to the app tests. `findOf[T](msgs) tea.Msg` returns the first message of type T as it was produced, so a test can feed a `nav.Routed` back. `valueOf[T](msgs) (T, bool)` returns the unwrapped value. Both look inside `nav.Routed`, and PR 3 makes them also look inside `overlayReplyMsg`. They replace the 16 lookups of exported types by type name. `findMsg` and `findMsgPrefix` stay for the unexported messages of the views, such as `views.s3PageLoadedMsg` and `listPageMsg[T]`. Helpers in the views tests (`sortCancelled`, `sortCleared`, `actionCancelled`) build the replies that pick no option, so PR 1 changes only these helpers.

## Invariants

These rules describe the end state of A7. Each rule names the PR that makes it true when the rule is not true today. A PR must not break a rule that is already true.

1. The top overlay gets every key press before input capture, the global keys, the panel keys, the ReadOnly guard and the view. In each state that can happen, the top overlay is the one that the old chain chose: picker, then confirm, then help, then command bar.
2. Only key presses are captured. Ticks, loads, paste and routed messages keep running while an overlay is open.
3. While the quit confirm is on top, a second ctrl+c quits at once.
4. While a picker is open, ctrl+x goes to the picker and never to the global key that cancels an operation.
5. The discard confirm of a form shows over the form and gets keys before the form.
6. A prompt from a view that is not on top never shows. A confirm request that comes while a prompt is open never replaces it. Both get a toast and a decline to the view that asked. Today this is true for a confirm request. PR 7 makes it true for every prompt request.
7. Each prompt gets exactly one reply on its route: an answer, esc, a skip, or a close by `setTheme`. No app state stays pending. PR 3 (esc reaches the view, D2) and PR 6 (`closePrompts`) make this true.
8. A reply goes only to the view that asked, never to the view that happens to be on top. The app drops it with a debug line when that view is gone. PR 3 makes this true.
9. A prompt of a view can never be taken for a prompt of the app. The app's prompts use handlers, and the views' prompts use `replyTo`. PR 6 makes this true.
10. The drawing order is the stack order: the views, the overlays from bottom to top, the toasts, and then `StripUnsafe`. PR 2 makes this true.
11. Each overlay gets `SetSize` on each resize and after `setTheme`. PR 2 makes this true.
12. A `FormResultMsg` pops only the form that sent it, and only when that form is on top. The result goes to the view that opened the form. PR 4 makes this true.
13. Copying `Model` is safe. The components stay value fields, the stack slice is copied on write, and no handler keeps a `Model`.

## Visible changes

- The help overlay gets the new size when the window changes while it is open. Today it keeps the size it had when it opened.
- The "ctrl+x clear" hint shows only on the sort picker (decision D4).
- A confirm or a picker draws over the suggestions of the command bar. Today the suggestions draw over the dialog. This happens only when a view asks for a confirm while the command bar is open, and the dialog then gets the keys, so it must be visible.
- A reply goes to the view that asked. Today a reply goes to the view on top, which is a different view only in a race, for example when a load pushes a view under an open picker.
- Every prompt request follows one rule while a prompt is open (D3).

## PR plan

Each PR is small and keeps all tests green. Each PR keeps the behavior the same, except for the changes that it names.

- PR 0, tests only. Add `findOf[T]`, `valueOf[T]` and the reply helpers, and use them. Pin today's behavior in `internal/app/overlay_test.go`: a confirm over open help or an open command bar gets the keys, ctrl+x on an open picker keeps an operation, esc and ctrl+x on each picker kind (sort, action, theme, region, profile, mode, service, feature, cancel), the theme confirm below the home view, the discard confirm over a form, the help size after a resize, and a race test where a view is pushed under an open picker. The last two tests pin the current behavior, and PR 2 and PR 3 change them on purpose. Older tests already pin the second ctrl+x on the quit confirm, a late prompt that does not replace an open one, a skipped prompt, and the ReadOnly guard.
- PR 1, typed picker outcome. Add `PickOutcome`, `Picked()` and the `Clearable` flag, and remove `"_clear"`. `Selected` stays -1 for a cancel until PR 8, as a safety net. Add `applySortPick`. Visible change: the hint (D4).
- PR 2, overlay interface and stack. Add `ui/overlay.go` and `SetSize` on the four components. Add `app/overlays.go`. The top of the stack gets the keys, and the app draws the stack in order. Visible change: the help size.
- PR 3, replies go to the view that asked. Add `overlayReplyMsg`. A routed request records its origin, and the reply goes there. The race test from PR 0 changes. A new test: a reply to view A never reaches view B, also when B is on top.
- PR 4, form route. Record the form and the view that asked for it. Tests for a view that is gone and for a form that is not on top.
- PR 5, move the reply types to `msg`, with aliases in `ui`. No behavior change.
- PR 6, typed handlers for the app's own prompts. Remove `pendingTheme`, `pendingSession`, the action constants and the picker IDs of the app. Add `closePrompts`. No behavior change.
- PR 7: one rule for all prompt requests while a prompt is open (D3).
- PR 8, cleanup and docs. Remove the old reply cases, the untagged paths, `Selected: -1` for a cancel, `Confirm.Action()` and the aliases. Update `ARCHITECTURE.md`, `KEYBINDINGS.md`, the tracker and the A10 note.
- Not in A7 (D1): typed tags in the views. A new tracker item, A12, keeps them: typed confirm tags (removes `pendingConfirm` and `"s3_overwrite:N"`) and typed sort and action pickers (removes the label matching and `PickerResultMsg.ID`).

## Decisions

D1. Should A7 go as far as typed tags in the views (PR 9a and 9b)?
Accepted: No. Stop at PR 8. After PR 3, a view's action string cannot clash with another view or with the app, so typed tags only make the code tidier. Add PR 9a and 9b to the tracker as a new item.
Other answers: include PR 9a and 9b in A7, which rewrites about 150 test literals. Or do only 9b.

D2. Should esc on a sort or action picker reach the view that asked? Today the app drops it (`app.go:648`), so the cancel code in the EC2 list and six view tests never run.
Accepted: Yes, from PR 3. The SQS queue list then also clears its pending action on esc. The user sees no change, and the cancel code then runs.
Other answer: keep dropping it, and delete the cancel code and its tests.

D3. Should one rule apply to every prompt request while a prompt is open (PR 7)? Today only a confirm request is refused. A picker request, the discard confirm and the app's own prompts replace the open prompt without a decline.
Accepted: Yes, in PR 7. The app refuses the request with a toast and sends one decline to the asker. No path that a user can reach today replaces an open prompt, so a user should not notice it.
Other answers: keep A7 free of behavior changes and add the rule to the tracker as a new item. Or open the new prompt on top of the old one, which needs more than one instance of each kind.

D4. Should the "ctrl+x clear" hint show on pickers where clearing means nothing (theme, region, profile, mode, service, feature, cancel, action)?
Accepted: No. Show it only on the sort picker. ctrl+x still closes the other pickers as a cancel.
Other answers: keep the hint on every picker. Or make ctrl+x do nothing on those pickers.

Two more choices have a clear answer, and the plan uses them:

- `replyTo` is the view instance, not the view ID that the tracker names. A view ID can be on the stack twice after cross-navigation, and after a session switch a new view has the same ID as an old one. `nav.Routed` and `nav.Contains` also use the instance.
- While a prompt is open, a view can still push a view under it, for example when a load ends with a navigation. Replies to the view that asked make this safe.
