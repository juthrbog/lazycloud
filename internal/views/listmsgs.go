package views

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/juthrbog/lazycloud/internal/aws"
)

// Messages and adapters of the shared list core (see listcore.go).

// listPageMsg is one page of a load. A page whose gen is not the current
// load generation is dropped. Page 0 or 1 replaces the rows; a later page
// is appended. next is the token of the next page; nil means the last page.
// Tests send gen 0 and page 0 to a view whose Init never ran, so the
// constructor leaves loadGen at 0.
type listPageMsg[T any] struct {
	gen   int
	items []T
	next  *string
	page  int
}

// listCompleteMsg carries the result of the second load phase
// (listConfig.Complete). Like a page, it is dropped when its gen is not the
// current load generation.
type listCompleteMsg[T any] struct {
	gen   int
	items []T
}

// listFailedMsg is a failed page or second-phase call. It goes to the core
// first, not to the app, so a failure of a superseded load reaches neither
// the view nor the status bar.
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

// fetchPages adapts a paged call to a fetchFunc. split reads the items and
// the next token of a page (use nextToken for the {HasMorePages, Token}
// page structs). Like fetchAll, it takes the service and a method
// expression (or a func literal that takes the service), and a nil service
// gives errNoClient.
//
// Use: fetchPages(ec2, aws.EC2Service.ListVPCsPage, splitVPCPage)
func fetchPages[S, P, T any](svc S, call func(S, context.Context, *string) (P, error),
	split func(P) ([]T, *string),
) fetchFunc[T] {
	return func(ctx context.Context, token *string) ([]T, *string, error) {
		if any(svc) == nil {
			return nil, nil, errNoClient
		}
		page, err := call(svc, ctx, token)
		if err != nil {
			return nil, nil, err
		}
		items, next := split(page)
		return items, next, nil
	}
}

// nextToken adapts the {HasMorePages, Token} page structs. It ends the load
// when there are no more pages, and also when HasMorePages is set but the
// token is nil or empty (asking again with no token would load page 1).
func nextToken(more bool, tok *string) *string {
	if more && tok != nil && *tok != "" {
		return tok
	}
	return nil
}

// listRefreshedMsg is the reply of a selective refresh. It carries what the
// refresh read, also when some keys failed. A reply whose gen is not the
// current load generation is dropped, because a full load started since
// and brings newer data.
type listRefreshedMsg[T any] struct {
	gen   int
	items []T
	err   error
}

// refreshBy adapts a batch refresh (for example aws.EC2Service.RefreshAMIs)
// to a refreshFunc, with one timeout for the call. Like fetchAll, it takes
// the service and a method expression, and a nil service gives errNoClient.
//
// Use: refreshBy(ec2, aws.EC2Service.RefreshAMIs, callTimeout)
func refreshBy[S, T any](svc S, call func(S, context.Context, []string) ([]T, error),
	timeout time.Duration,
) refreshFunc[T] {
	return func(ctx context.Context, keys []string) ([]T, error) {
		if any(svc) == nil {
			return nil, errNoClient
		}
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		return call(svc, ctx, keys)
	}
}

// refreshLimit is the most calls refreshEach runs at the same time.
const refreshLimit = 8

// refreshEach adapts a refresh of one item (for example
// aws.SQSService.GetQueueAttributes) to a refreshFunc. It calls get once per
// key, each call with its own timeout, at most refreshLimit at a time, so
// one slow or failed key does not stop the others. It returns every item it
// read. When some keys fail, it also returns a *refreshError. When ctx is
// canceled, it returns the cancel error, so the core stays silent. Like
// fetchAll, it takes the service and a method expression, and a nil service
// gives errNoClient.
//
// Use: refreshEach(sqs, aws.SQSService.GetQueueAttributes, callTimeout)
func refreshEach[S, T any](svc S, get func(S, context.Context, string) (*T, error),
	timeout time.Duration,
) refreshFunc[T] {
	return func(ctx context.Context, keys []string) ([]T, error) {
		if any(svc) == nil {
			return nil, errNoClient
		}
		got := make([]*T, len(keys))
		errs := make([]error, len(keys))
		var g errgroup.Group
		g.SetLimit(refreshLimit)
		for i, k := range keys {
			g.Go(func() error {
				kctx := ctx
				if timeout > 0 {
					var cancel context.CancelFunc
					kctx, cancel = context.WithTimeout(ctx, timeout)
					defer cancel()
				}
				got[i], errs[i] = get(svc, kctx, k)
				return nil
			})
		}
		_ = g.Wait() // the calls report through errs
		var items []T
		var first error
		failed := 0
		for i := range keys {
			if errs[i] != nil {
				failed++
				if first == nil {
					first = errs[i]
				}
				continue
			}
			if got[i] != nil {
				items = append(items, *got[i])
			}
		}
		if failed == 0 {
			return items, nil
		}
		if err := ctx.Err(); err != nil {
			return items, err
		}
		return items, &refreshError{failed: failed, total: len(keys), first: first}
	}
}

// refreshError reports the keys of a refreshEach that failed. Its text is
// for the toast: "1 of 3 failed: <the first error>". It has no Unwrap on
// purpose: aws.ErrorText reads an AWS error inside a chain and would then
// drop the count.
type refreshError struct {
	failed, total int
	first         error
}

func (e *refreshError) Error() string {
	return fmt.Sprintf("%d of %d failed: %s", e.failed, e.total, aws.ErrorText(e.first))
}
