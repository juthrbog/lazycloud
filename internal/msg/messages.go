package msg

// NavigateMsg tells the root model to push a new view.
type NavigateMsg struct {
	ViewID string
	Params map[string]string
}

// NavigateBackMsg tells the root model to pop the current view.
type NavigateBackMsg struct{}

// ResourcesLoadedMsg is sent when an AWS API call completes successfully.
type ResourcesLoadedMsg[T any] struct {
	Resources []T
}

// ErrorMsg is sent when an AWS API call fails.
type ErrorMsg struct {
	Err     error
	Context string
}

// LoadingMsg indicates a resource is being fetched.
type LoadingMsg struct {
	Resource string
}

// RefreshMsg triggers a reload of the current view's data.
type RefreshMsg struct{}

// ProfileChangedMsg is broadcast when the AWS profile changes.
type ProfileChangedMsg struct {
	Profile string
}

// RegionChangedMsg is broadcast when the AWS region changes.
type RegionChangedMsg struct {
	Region string
}

// StatusMsg displays a transient status message in the status bar.
// Deprecated: Use ToastMsg for auto-dismissing notifications.
type StatusMsg struct {
	Text string
}

// ToastMsg displays an auto-dismissing toast notification.
type ToastMsg struct {
	Text  string
	Level int // 0=info, 1=success, 2=error (matches ui.ToastLevel)
}

// Convenience constructors for ToastMsg.
func ToastInfo(text string) ToastMsg    { return ToastMsg{Text: text, Level: 0} }
func ToastSuccess(text string) ToastMsg { return ToastMsg{Text: text, Level: 1} }
func ToastError(text string) ToastMsg   { return ToastMsg{Text: text, Level: 2} }

// RequestConfirmMsg asks the app to show a type-to-confirm dialog.
// The ConfirmResultMsg is routed back to the current view.
type RequestConfirmMsg struct {
	Message string
	Action  string
}

// RequestActionPickerMsg asks the app to show an action picker.
// The PickerResultMsg (ID="action") is routed back to the current view.
type RequestActionPickerMsg struct {
	Title   string   // picker title
	Options []string // action labels
}

// RequestSortPickerMsg asks the app to show a column sort picker.
// The PickerResultMsg (ID="sort") is routed back to the current view.
type RequestSortPickerMsg struct {
	Columns    []string // column titles
	CurrentCol int      // currently sorted column index (-1 if unsorted)
}
