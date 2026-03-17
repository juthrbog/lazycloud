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
type StatusMsg struct {
	Text string
}
