package views

import (
	"context"
	"time"
)

// Time limits of AWS calls made by views. The HTTP client also fails a call
// that gets no data for a minute (see internal/aws), so these limits catch
// calls that are slow but not silent, for example a retry loop.
const (
	// callTimeout limits one request or one page.
	callTimeout = 30 * time.Second
	// listTimeout limits a call that reads all pages of a list.
	listTimeout = 2 * time.Minute
	// mutateTimeout limits one short change, such as starting instances.
	mutateTimeout = time.Minute
)

// viewCtx holds the context that the navigator gives a view (see
// nav.ContextSetter). The navigator cancels it when the view leaves the
// stack, so the view's loads stop when the user leaves it.
//
// Embed it in each view that calls AWS, and use it by policy:
//   - a load or describe call: context.WithTimeout(ctx, callTimeout)
//   - a short change that must finish when the user leaves: detached(ctx)
//   - a long operation: appmsg.StartOpMsg, which the app runs in the
//     background with its own context
type viewCtx struct {
	ctx context.Context
}

// SetContext implements nav.ContextSetter.
func (v *viewCtx) SetContext(ctx context.Context) { v.ctx = ctx }

// viewContext returns the view's context. A view that was never pushed
// through a navigator, as in many tests, gets context.Background().
func (v *viewCtx) viewContext() context.Context {
	if v.ctx == nil {
		return context.Background()
	}
	return v.ctx
}

// detached returns a context for a short change. Closing the view does not
// cancel it, so the change finishes and reports its result.
func detached(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), mutateTimeout)
}
