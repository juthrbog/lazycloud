package msg

// The replies of the prompts. The app sends each reply to the view that
// asked for the prompt. They live here, so a view needs no ui import to
// read a reply. ui keeps aliases until A7 PR 8.

// ConfirmResultMsg is the reply of a confirm dialog.
type ConfirmResultMsg struct {
	Confirmed bool
	Action    string // the Action of the RequestConfirmMsg
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
	Selected int         // the index of the picked option; -1 when Outcome is not Picked
	Value    string      // the value of the picked option; empty when Outcome is not Picked
}

// Picked reports whether the user picked an option.
func (r PickerResultMsg) Picked() bool { return r.Outcome == Picked }

// RequestFormDiscardConfirmMsg asks the app to show a "discard changes?"
// dialog when the user presses esc on a form with unsaved changes. The
// reply goes to the form.
type RequestFormDiscardConfirmMsg struct{}

// FormDiscardAction is the Action of the discard confirm.
const FormDiscardAction = "form_discard"
