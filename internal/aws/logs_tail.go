package aws

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// Live tail of CloudWatch Logs. See docs/design/cwlogs.md, "Live tail".

// TailQuery selects the events of a live tail session.
type TailQuery struct {
	GroupARN string // the ARN without ":*"
	Stream   string // "": all streams of the group
	Pattern  string // "": all events
}

// TailUpdate is one update of a session. AWS sends one each second, also
// with no events.
type TailUpdate struct {
	Events []LogEvent // in ascending time order; ID is empty
	// Sampled is true when more events matched than AWS sends in one
	// update (500).
	Sampled bool
}

// LiveTail is a running live tail session. The context of the
// StartLiveTail call owns the session: when it ends, the stream closes.
type LiveTail interface {
	// Next waits for the next update. It returns an error when the session
	// ends: ctx.Err() when ctx ends, the stream error when AWS ends it
	// (for example SessionTimeoutException after 3 hours), or
	// ErrLiveTailEnded when the stream closes with no error.
	Next(ctx context.Context) (*TailUpdate, error)
}

// ErrLiveTailEnded is the error of Next when the stream closes with no
// error.
var ErrLiveTailEnded = errors.New("the live tail session ended")

// ErrLiveTailUnavailable is the error of StartLiveTail when the client
// has an endpoint override, and the endpoint does not implement live tail
// or does not answer. LocalStack does not implement it. The caller can
// poll FilterEvents instead.
var ErrLiveTailUnavailable = errors.New("the endpoint has no live tail")

// tailStream is the part of the event stream of the SDK that a session
// uses. Tests replace it.
type tailStream interface {
	Events() <-chan cwltypes.StartLiveTailResponseStream
	Close() error
	Err() error
}

// StartLiveTail starts a live tail session. ctx must have no timeout,
// because it lives as long as the session. When ctx ends, the stream
// closes, also when nothing reads it any more.
//
// With an endpoint override, the call turns off the "stream-" host prefix
// of the SDK, so the call goes to the endpoint host (H5).
func (svc *LogsServiceImpl) StartLiveTail(ctx context.Context, q TailQuery) (LiveTail, error) {
	in := &cloudwatchlogs.StartLiveTailInput{LogGroupIdentifiers: []string{q.GroupARN}}
	if q.Stream != "" {
		in.LogStreamNames = []string{q.Stream}
	}
	if q.Pattern != "" {
		in.LogEventFilterPattern = aws.String(q.Pattern)
	}
	var opts []func(*cloudwatchlogs.Options)
	if svc.client.Endpoint != "" {
		opts = append(opts, noHostPrefix)
	}
	out, err := svc.client.LogsClient().StartLiveTail(ctx, in, opts...)
	if err != nil {
		if svc.client.Endpoint != "" && liveTailMissing(err) {
			return nil, fmt.Errorf("%w: %w", ErrLiveTailUnavailable, err)
		}
		return nil, fmt.Errorf("StartLiveTail: %w", err)
	}
	return newLiveTail(ctx, out.GetStream()), nil
}

// noHostPrefix turns off the host prefix for one call. The flag is a stack
// value, and the SDK clears the stack values when a call starts, so a
// middleware of the call sets it.
func noHostPrefix(o *cloudwatchlogs.Options) {
	o.APIOptions = append(o.APIOptions, func(s *middleware.Stack) error {
		return s.Initialize.Add(middleware.InitializeMiddlewareFunc("LazyCloudNoHostPrefix",
			func(ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler) (
				middleware.InitializeOutput, middleware.Metadata, error) {
				return next.HandleInitialize(smithyhttp.DisableEndpointHostPrefix(ctx, true), in)
			}), middleware.After)
	})
}

// liveTailMissing reports whether err means that the endpoint does not
// implement live tail (HTTP 501), or that it cannot be reached.
func liveTailMissing(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if re, ok := errors.AsType[interface {
		error
		HTTPStatusCode() int
	}](err); ok && re.HTTPStatusCode() == http.StatusNotImplemented {
		return true
	}
	// A failed connect also comes as a response error, with status 0.
	if _, ok := errors.AsType[*net.OpError](err); ok {
		return true
	}
	_, ok := errors.AsType[*net.DNSError](err)
	return ok
}

// session is a LiveTail over the event stream of the SDK.
type session struct {
	s tailStream
}

// newLiveTail binds the stream to ctx: when ctx ends, the stream closes.
// When ctx has already ended, it closes at once. Close runs once.
func newLiveTail(ctx context.Context, s tailStream) *session {
	context.AfterFunc(ctx, func() { _ = s.Close() })
	return &session{s: s}
}

func (t *session) Next(ctx context.Context) (*TailUpdate, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case ev, ok := <-t.s.Events():
			if !ok {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if err := t.s.Err(); err != nil {
					return nil, fmt.Errorf("StartLiveTail: %w", err)
				}
				return nil, ErrLiveTailEnded
			}
			if u, ok := ev.(*cwltypes.StartLiveTailResponseStreamMemberSessionUpdate); ok {
				return tailUpdateFrom(&u.Value), nil
			}
			// SessionStart, or a member that this SDK does not know.
		}
	}
}

func tailUpdateFrom(u *cwltypes.LiveTailSessionUpdate) *TailUpdate {
	out := &TailUpdate{Events: make([]LogEvent, 0, len(u.SessionResults))}
	if u.SessionMetadata != nil {
		out.Sampled = u.SessionMetadata.Sampled
	}
	for _, e := range u.SessionResults {
		out.Events = append(out.Events, LogEvent{
			Time:     parseEpochMillis(aws.ToInt64(e.Timestamp)),
			Ingested: parseEpochMillis(aws.ToInt64(e.IngestionTime)),
			Message:  aws.ToString(e.Message),
			Stream:   aws.ToString(e.LogStreamName),
		})
	}
	slices.SortStableFunc(out.Events, func(a, b LogEvent) int { return a.Time.Compare(b.Time) })
	return out
}
