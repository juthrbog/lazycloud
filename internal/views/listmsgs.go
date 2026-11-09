package views

import "context"

// Messages and adapters of the shared list core (see listcore.go).

// listPageMsg is the result of a load. A page whose gen is not the current
// load generation is dropped. Tests send gen 0 to a view whose Init never
// ran, so the constructor leaves loadGen at 0.
type listPageMsg[T any] struct {
	gen   int
	items []T
}

// listFailedMsg is a failed load. It goes to the core first, not to the
// app, so a failure of a superseded load reaches neither the view nor the
// status bar.
type listFailedMsg struct {
	gen     int
	err     error
	context string // msg.ErrorMsg.Context of the load that failed
}

// loadError marks the msg.ErrorMsg that the core emits for a failure of
// the current load. It wraps the AWS error, so errors.Is(context.Canceled)
// in the app, aws.ErrorText and the status bar work as before. The core uses
// the mark only to know that this ErrorMsg is its own echo, already applied.
type loadError struct {
	err error
}

func (e *loadError) Error() string { return e.err.Error() }
func (e *loadError) Unwrap() error { return e.err }

// fetchAll adapts a call that returns the whole list in one go (for example
// aws.EC2Service.ListSecurityGroups) to a fetchFunc. It takes the service
// and a method expression, not a method value, because a method value on a
// nil interface panics when it is made. A nil service gives errNoClient.
//
// Use: fetchAll(ec2, aws.EC2Service.ListSecurityGroups)
func fetchAll[S, T any](svc S, call func(S, context.Context) ([]T, error)) fetchFunc[T] {
	return func(ctx context.Context, _ *string) ([]T, *string, error) {
		if any(svc) == nil {
			return nil, nil, errNoClient
		}
		items, err := call(svc, ctx)
		return items, nil, err
	}
}
