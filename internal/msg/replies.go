package msg

// The replies of the prompts. The app sends each reply to the view that
// asked for the prompt. They live here, so a view needs no ui import to
// read a reply.

// PromptTag is data that a view attaches to a prompt request. The app never
// reads it. It copies the tag into every reply to that prompt, declines
// included, so the view reads the reply from the tag and keeps no state
// while the prompt is open. Implement PromptTag on a pointer receiver, so a
// tag is always comparable, and never change a tag after the request goes
// out. Go can give all pointers to a struct with no fields the same
// address, so give a tag a field when the view must tell two of its tags
// apart.
type PromptTag interface{ PromptTag() }

// ConfirmResultMsg is the reply of a confirm dialog.
type ConfirmResultMsg struct {
	Confirmed bool
	Action    string // the Action of the RequestConfirmMsg
	// Tag is the Tag of the request. It is nil for the prompts of the app.
	Tag PromptTag
}

// PickOutcome says how a picker closed.
type PickOutcome uint8

const (
	// Picked: the user picked an option. It is the zero value, so a
	// PickerResultMsg with only Selected and Value set is a pick.
	Picked PickOutcome = iota
	// PickCancelled: the user closed the picker with esc, or with ctrl+x
	// on a picker that cannot be cleared.
	PickCancelled
	// PickCleared: the user pressed ctrl+x on a picker that can be
	// cleared, such as the sort picker. See ui.Picker.SetClearable.
	PickCleared
)

// PickerResultMsg is the reply of a picker.
type PickerResultMsg struct {
	ID       string      // picker identifier
	Outcome  PickOutcome // how the picker closed
	Selected int         // the index of the picked option; only set when Outcome is Picked
	Value    string      // the value of the picked option; empty when Outcome is not Picked
	// Tag is the Tag of the request. It is nil for the pickers of the app.
	Tag PromptTag
}

// Picked reports whether the user picked an option.
func (r PickerResultMsg) Picked() bool { return r.Outcome == Picked }
