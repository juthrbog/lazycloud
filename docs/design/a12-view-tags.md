# A12: typed tags for the prompts of the views

Status: done. Approved on 2026-11-15, with the proposed answer for each decision. PR 7 is accepted.

## Summary

A view that needs an answer sends a prompt request: a confirm (`msg.RequestConfirmMsg`), an action picker (`msg.RequestActionPickerMsg`) or a sort picker (`msg.RequestSortPickerMsg`). After A7, the reply goes only to the view that asked. But the view still finds out what the reply is about from strings, and it keeps the targets of the action in its own fields until the reply arrives:

- A confirm carries an `Action` string, for example `"ec2_stop"`, `"delete_bucket"` or `"s3_overwrite:3"`. `pendingConfirm` (`internal/views/actions.go`) holds the targets of EC2, SQS queues and S3 buckets. S3 objects keeps `pendingOverwrite`, `overwriteSeq` and `pendingDeleteEntries`.
- A picker reply carries an `ID`, `"action"` or `"sort"`. EC2, SQS queues and SQS messages find the picked action by its label (`ec2ActionByLabel`, `switch m.Value`).
- A sort pick applies the index of the picked column to the table at the time of the reply, also when the columns changed while the picker was open.

This proposal makes three changes:

- A prompt request of a view carries a tag (`msg.PromptTag`): a pointer to a small struct of the view. The tag holds everything that the answer acts on, frozen when the user pressed the key. The app copies the tag into every reply to that prompt, also into each decline. The view reads the reply with a type assertion on the tag, and it keeps no prompt state.
- An action picker reports the pick by its index into the options that the view froze in its tag. The labels live in one table per view.
- A sort pick finds the column by its title.

At the end, the `Action` and `ID` strings, `pendingConfirm`, the pending fields of S3 objects, the three label switches and the special path for the form discard confirm are gone. The changes that the user can see are listed in "Visible changes".

## Facts that the design depends on

These facts were checked at `ff47ebf`:

- The app shows one prompt at a time (A7 decision D3). A prompt request while a prompt is open gets a toast and one decline. A request of a view that is not on top gets the same. So a view has at most one prompt open, and each prompt gets exactly one reply.
- Every reply to a view goes through one of two functions in `internal/app`: `deliverReply` (a reply of the confirm or the picker, and the declines of `closePrompts`) and `refusePrompt` (the decline of a refused request). Both have the `replyRoute` of the prompt.
- `msg` imports no other package of the app, so a tag type in `msg` cannot name a view type. A tag is an interface in `msg`, and each view defines its own unexported tag types.
- The picker replies with the index of the option in the original list, also after a filter (`picker.go`, `origIdx := p.filtered[p.cursor]`).
- `ui.Table.SetColumns` keeps the current sort by its title (`columnIndex`). `Sort` on the sorted column flips the direction.
- The app's own prompts (quit, theme, session, service, region and the others) use handler functions and need no tag.

The design found four hazards in the code of today:

- H1. A fast `m m` on EC2 or SQS queues sends two action requests. The app refuses the second one, and its decline clears the targets of the first one. The first picker then asks "Stop 0 instance(s)?", and a yes does nothing.
- H2. SQS messages counts the selected messages when the user presses `x`, but it deletes the selection at the time of the reply. A page load while the confirm is open can change which messages are deleted.
- H3. The declines of `closePrompts` carry no `Action`. A decline of an overwrite question does not clear `pendingOverwrite` in S3 objects, so later conflicts are not asked. Today a theme switch replaces the view, so this cannot happen yet.
- H6. When the columns of S3 versions change while the sort picker is open, the picked index can name another column.

## Scores of the three drafts

Each score is from 1 (poor) to 10 (excellent). A judge checked the claims of each draft against the code.

| Draft | Score | Why |
|---|---|---|
| minimal (an opaque tag, the smallest change) | 7.5 | Correct and complete. It removes the most state, but `any` lets string tags come back. It has no list of invariants. |
| type-safety (generic requests and replies) | 5.5 | The best compile-time checks, but it rewrites the seven pickers of the app and the reply types. That is much more than A12. |
| risk (invariants first) | 8.5 | Every fact was correct. It has the best migration and the list of invariants. |

The plan uses the risk draft as its base. It takes the smaller set of deletions and the removal of the overwrite guard from the minimal draft. It takes the enums with one label table and `SortByTitle` from the type-safety draft. One change is new: the app puts the tag on the reply from the route, so the ui components never hold a tag (decision D2).

## Design

### Types

In `internal/msg`:

```go
// PromptTag is data that a view attaches to a prompt request. The app never
// reads it. It copies it into every reply to that prompt, declines
// included. Implement PromptTag on a pointer receiver, and never change a
// tag after the request goes out.
type PromptTag interface{ PromptTag() }

type RequestConfirmMsg struct {
	Message string
	Phrase  string // the text to type; "" means "confirm"
	Quick   bool
	Tag     PromptTag
}

type RequestActionPickerMsg struct {
	Title   string
	Options []string // labels; the reply's Selected is an index into them
	Tag     PromptTag
}

type RequestSortPickerMsg struct {
	Columns    []string
	CurrentCol int
	Tag        PromptTag
}

type ConfirmResultMsg struct {
	Confirmed bool
	Tag       PromptTag // nil for the prompts of the app
}

type PickerResultMsg struct {
	Tag      PromptTag
	Outcome  PickOutcome
	Selected int    // the original index of the option; only for Picked
	Value    string // read only by the pickers of the app
}
```

A pointer tag is always comparable, so a tag never makes `==` panic. The marker method stops a string from being used as a tag.

### The app

- `replyRoute` gets a field `tag msg.PromptTag`. The handlers of `RequestConfirmMsg`, `RequestActionPickerMsg` and `RequestSortPickerMsg` put the tag of the request on the route.
- One helper, `route.stamp(reply tea.Msg) tea.Msg`, sets `Tag` on a `ConfirmResultMsg` or a `PickerResultMsg`. `deliverReply` and `refusePrompt` call it before they send a reply to a view. So every reply carries the tag: y, n, quick enter, typed enter, esc, ctrl+x, both refusals and the declines of `closePrompts`. This also fixes H3.
- The app never reads a tag.
- The ui components do not change in PR 1. In the cleanup PR, `Confirm.Show` and `ShowQuick` lose the action parameter, and `Picker.Show` loses the ID parameter.

### Views

A helper in `internal/views/actions.go`:

```go
// pickedOption returns the option that r picked from opts, the options that
// the view froze in its tag. A reply that is not a pick, or an index out of
// range, returns false.
func pickedOption[T any](opts []T, r msg.PickerResultMsg) (T, bool)
```

The rule for a handler: `t, ok := r.Tag.(*someTag); if !ok || t == nil`, the reply is not for this handler.

- Sort (listCore, S3 objects, S3 versions, SQS messages): `sortTag{columns []string}` holds the column titles when the picker opens. One function, `askSort(t *ui.Table) tea.Cmd`, builds the request for the four views. `applySortPick` returns false for any other tag. A pick calls the new `Table.SortByTitle(title, hint)`, which finds the column with `columnIndex`. A title that is gone is ignored (D5).
- EC2: an enum `ec2Op` indexes the action table, and `actionsForState` returns ops, not labels. The tags are `ec2ManageTag{ids, ops}` and `ec2ConfirmTag{op, ids}`. `ec2ActionByLabel`, `ec2ActionByConfirmID`, `confirmID` and `pending` are deleted. The ReadOnly check at the reply stays.
- SQS queues: an enum `sqsOp` (send, purge, delete) with one label table. The tags are `sqsManageTag{urls, ops}` and `sqsConfirmTag{op, urls}`. `pending` and the label switch are deleted. `sendTarget` is state of the form, not of a prompt, so it stays (D9).
- S3 buckets: `s3BucketDeleteTag{names}`. `pendingDelete` is deleted.
- S3 objects: `s3OverwriteTag{mode, srcKey, dstKey}` and `s3DeleteTag{entries}`. `pendingOverwrite`, `overwriteSeq`, `overwriteAction`, `s3Transfer` and `pendingDeleteEntries` are deleted. A second conflict while a prompt is open gets the refusal of the app: a toast with the question, and a decline that copies nothing (D3).
- SQS messages: `sqsDeleteMsgsTag{ids}` freezes the message IDs at the key press. A yes deletes those messages with their newest receipt handles and skips the ones that are gone (D4). The redrive picker uses `sqsDLQPickTag{ops}`, and the redrive confirm uses `sqsRedriveTag{}`.
- The form: `FormView` sends a quick `RequestConfirmMsg` with its own `discardTag`. The app then needs no special path for the discard confirm (D6). `RequestFormDiscardConfirmMsg`, `FormDiscardAction` and `askDiscard` are deleted.

`pendingConfirm` and `actions_test.go` are deleted after S3 objects moves.

## Invariants

Each PR keeps every invariant, except where the PR names a change.

- I1. The targets are frozen when the user presses the key: EC2, SQS queues, S3 buckets and the delete of S3 objects. The targets of an overwrite are frozen at the conflict. The delete of SQS messages is frozen from PR 5.
- I2. A reply that is not a yes runs nothing.
- I3. A yes runs only the action and the targets in its own tag.
- I4. A nil, typed-nil or foreign tag does nothing in a view, and it never panics.
- I5. A reply to a view that is gone is dropped.
- I6. A theme switch declines each open prompt exactly once.
- I7. EC2, SQS queues and S3 buckets check ReadOnly again at the reply. From PR 7, S3 objects and SQS messages do too.
- I8. A refused prompt gives one toast and one decline. From PR 1, the decline carries the tag.
- I9. The app never reads a tag. The prompts of the app have a nil tag.
- I10. Each prompt gets exactly one reply, and that reply carries its tag on every path.
- I11. S3 objects asks one overwrite question at a time. A second conflict replaces nothing and shows a toast.
- I12. Sort: a pick sorts, and a pick of the sorted column flips the direction. ctrl+x clears the sort, and esc keeps it. Only the sort picker can be cleared.
- I13. SQS Send Message works only for one queue.
- I14. Form discard: a yes closes the form. A no or a refusal keeps it open.
- I15. A tag never changes after the request goes out. It holds keys, values and enums, never functions, views or row pointers.
- I16. `Selected` is the original index of the option, also after a filter.

## Visible changes

- H1: after a fast `m m`, the first picker asks about its own targets, and a yes acts on them (PR 3).
- H6: a sort pick sorts by the title that the user picked (PR 2).
- H2: SQS messages deletes the messages that the prompt counted (PR 5).
- A second overwrite conflict shows the info toast of the app, "Not asked, another prompt is open: s3://b/k exists. Replace it with x?", in place of an error toast (PR 4, D3).
- S3 objects and SQS messages show the ReadOnly toast in place of a service error, when the mode changes while their prompt is open (PR 7, D7).

## PR plan

Each PR runs the unit tests, `go vet`, golangci-lint with gosec and the LocalStack smoke tests.

- PR 0, tests only. Test helpers that run the command of a view, check the type of the request and build the reply from it. Today they copy `Action`, `ID` and `Value`. Later PRs make them copy `Tag`. The flow tests move to the helpers. Tests pin H1, H2, H3 and H6 as they are today, each marked with the PR that changes it. Tests pin the behavior that must stay: each decline path runs nothing, a theme switch with each kind of prompt open, a pick after a filter, ReadOnly at the reply, and form discard. The pins are in `internal/views/a12_pins_test.go` and `internal/app/a12_pins_test.go`. The test helpers are `requestOf`, `confirmReply`, `answerYes`, `pickLabel`, `pickColumn`, `cancelPick`, `clearSort` and `sortRequest` in `internal/views/helpers_test.go`. A test named `TestA12Hazard_...` pins a hazard, and the PR that fixes it flips the test.
- PR 1, the tag on the route. `PromptTag`, the `Tag` fields, `replyRoute.tag` and `stamp`. The `Action` and `ID` strings still work, so no view changes. A table test sends a test tag through every reply path and checks it with `assert.Same`. A prompt of the app after a prompt of a view replies with a nil tag.
- PR 2, sort. `sortTag`, `askSort`, `applySortPick` and `Table.SortByTitle`. The H6 pin changes.
- PR 3, EC2 and SQS queues. The enums, the tags and `pickedOption`. The H1 pins change.
- PR 4, S3 buckets and S3 objects. `pendingConfirm` and the pending fields of S3 objects are deleted. The H3 pin changes.
- PR 5, SQS messages and the form discard. The H2 pin changes.
- PR 6, cleanup and docs. Remove `Action`, `ID`, the action and ID parameters of `Confirm.Show`, `ShowQuick` and `Picker.Show`, `Picker.ID()` and the `"action"` and `"sort"` literals. Update `ARCHITECTURE.md`, `UI_COMPONENTS.md`, the doc comments in `messages.go`, the tracker and the note in `a7-overlays.md`. No change in behavior.
- PR 7, optional (D7). A ReadOnly check at the reply for S3 objects (delete and overwrite) and SQS messages (delete and redrive).

PR 6 is done. Prompts are now told apart only by their tag.

## Risks

- R1. A path that loses the tag turns a yes into a silent no. The table test of PR 1 covers every path. The PR 0 helper `answerYes` fails when a yes runs nothing, and the flow tests check the service call of each yes.
- R2. A missing case in a type switch is silent. Flow tests answer the real request of each view.
- R3. `assert.Equal` compares pointer tags by value. Use `assert.Same` where the identity counts.
- R4. A typed-nil tag can panic in a handler. Each view gets one typed-nil test.
- R5. Without `pendingConfirm.take`, the views rely on one reply per prompt (I10) and on one prompt at a time (A7 D3). PR 0 and PR 1 pin both. Some older tests send a second yes after a no or after a foreign reply, and expect nothing to run. That holds today only because `take` clears the targets. PR 3 and PR 4 remove that second yes from those tests, because a real prompt never gets two replies. The pins of PR 0 answer each prompt once.
- R6. A frozen target can name a row that left the list. That is the same as today: the service reports "not found" for that key, and SQS messages skips it.

## Decisions

D1. Which type is the tag?
Proposed: a marker interface, `msg.PromptTag`, on pointer receivers. It stops string tags, and a pointer is always comparable. `any` is simpler but lets strings back in. Generic request and reply types would also rewrite the seven pickers of the app.

D2. Where does the tag live while the prompt is open?
Proposed: on the reply route, and the app stamps it on the reply in `deliverReply` and `refusePrompt`. The ui components never hold a tag. The other choice is a tag field on `ui.Confirm` and `ui.Picker`, which needs a setter, a reset in `Show` and more changes to `ui`.

D3. What happens to a second overwrite conflict while a prompt is open?
Proposed: S3 objects drops its own guard and uses the refusal of the app. That removes the last prompt state of the view. The cost is that the toast is info, not error, and its text changes.

D4. Which messages does the delete of SQS messages act on?
Proposed: the IDs frozen at the key press, with the newest receipt handle of each at the reply. An ID that is gone is skipped.

D5. A sort pick whose title is gone after the columns changed?
Proposed: ignore it and keep the current sort.

D6. How does the form ask about discarding changes?
Proposed: the form sends a tagged quick `RequestConfirmMsg`, and the special path in the app is deleted.

D7. Add a ReadOnly check at the reply for S3 objects and SQS messages?
Proposed: yes, as the optional PR 7. PR 0 to PR 6 then change no behavior other than H1, H2, H6 and D3.

D8. Keep the old strings until the cleanup PR, or rename in one step?
Proposed: keep them until PR 6, as A7 did. Each view moves in one PR.

D9. Also tag the forms (`sendTarget`, `FormResultMsg.ID`)?
Proposed: no. Make it a separate tracker item if needed.
