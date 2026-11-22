package views

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// Pin tests for the ECS design (docs/design/ecs.md). They pin the hazards as
// they are today (TestECSHazard_*). The PR that fixes a hazard flips its test
// (TestECSH3_*, TestECSH6_*).
// H1, H2, H4, H5 and H13 are facts of the AWS API; PR 1, PR 4 and PR 5 test
// them against httptest fakes. H8 to H11 are facts of AWS and LocalStack that
// no unit test can pin. H12 is a fact of the list core that PR 3 changes with
// tests. The AWS layer half of H6, and H7, are pinned in internal/aws.

// H3, fixed in PR 2: StateColor gave no color to the ECS lifecycle states.
// They are now yellow, as the other states between two others. The health
// and rollout values get their own column styles (ui.RolloutColor, and the
// health style of PR 3), so they are not part of this test.
func TestECSH3_StateColorKnowsTheECSStates(t *testing.T) {
	for _, s := range []string{"PROVISIONING", "DEPROVISIONING", "DRAINING", "ACTIVATING", "DEACTIVATING"} {
		assert.Equal(t, strings.Replace(ui.StateColor("pending"), "pending", s, 1), ui.StateColor(s), "%s looks like pending", s)
	}
	// The states that it knows keep their colors.
	for _, s := range []string{"ACTIVE", "RUNNING", "PENDING", "STOPPING", "STOPPED", "FAILED", "INACTIVE"} {
		assert.NotEqual(t, s, ui.StateColor(s), "%s has a color", s)
	}
}

// H6, fixed in PR 4: the event viewer showed a plain error for a stream that
// does not exist. With aws.ErrLogStreamNotFound and a known group, it now
// names the stream and the group, and shows missing_stream_hint. r tries
// again, because the stream can come later.
func TestECSH6_MissingStreamHasItsOwnText(t *testing.T) {
	missing := fmt.Errorf("%w: ecs/app/0123456789abcdef in /app", aws.ErrLogStreamNotFound)
	f := &fakeEvents{get: func(aws.EventQuery) (*aws.EventPage, error) { return nil, missing }}
	p := streamParams()
	p["log_stream"] = "ecs/app/0123456789abcdef"
	p["missing_stream_hint"] = "The task writes the stream when its container starts."
	v := newEvents(t, f, p)
	require.NotEmpty(t, f.calls)
	text := plain(v.View().Content)
	assert.Contains(t, text, "No log stream named ecs/app/0123456789abcdef in /app. The task writes the stream when its container starts.")
	assert.Equal(t, "No log stream named ecs/app/0123456789abcdef · r to try again", v.Footer())

	// r tries again, and the stream is there now.
	f.get = nil
	f.events = []aws.LogEvent{{Time: evNow.Add(-time.Minute), Message: "msg-late", ID: "1"}}
	msgs := pressEv(v, keyPress('r'))
	assert.Contains(t, plain(v.View().Content), "msg-late")
	assert.NotContains(t, v.Footer(), "No log stream")
	for _, m := range msgs {
		_, isErr := m.(msg.ErrorMsg)
		assert.False(t, isErr)
	}
}

// Without the hint, the text ends with the group. The missing stream sends
// no error to the status bar, because it is not a failure of AWS.
func TestECSH6_MissingStreamWithoutHint(t *testing.T) {
	f := &fakeEvents{get: func(aws.EventQuery) (*aws.EventPage, error) {
		return nil, fmt.Errorf("%w: s1 in /app", aws.ErrLogStreamNotFound)
	}}
	useLogsClock(t, evNow)
	v := NewLogEvents(f, streamParams())
	v.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	for _, m := range pump(v, v.Init(), 20) {
		_, isErr := m.(msg.ErrorMsg)
		assert.False(t, isErr, "no error for a missing stream")
	}
	text := plain(v.View().Content)
	assert.Contains(t, text, "No log stream named s1 in /app")
	assert.NotContains(t, text, "in /app.")
}

// AWS gives ResourceNotFoundException also for a missing group. So when the
// group lookup failed for another reason, the viewer cannot tell, and it
// shows the error of AWS, which says what is missing.
func TestECSH6_MissingStreamNeedsAKnownGroup(t *testing.T) {
	rnf := &cwltypes.ResourceNotFoundException{Message: new("The specified log group does not exist.")}
	f := &fakeEvents{
		groupErr: errors.New("AccessDeniedException: no DescribeLogGroups"),
		get: func(aws.EventQuery) (*aws.EventPage, error) {
			return nil, fmt.Errorf("%w: s1 in /app: %w", aws.ErrLogStreamNotFound, rnf)
		},
	}
	v := newEvents(t, f, map[string]string{"log_group": "/app", "log_stream": "s1", "missing_stream_hint": "hint"})
	text := plain(v.View().Content)
	assert.Contains(t, text, "Error: ")
	assert.Contains(t, text, "The specified log group does not exist.")
	assert.NotContains(t, text, "No log stream named")
	assert.NotContains(t, text, "hint")
	assert.Equal(t, "could not load events · r to try again", v.Footer())
}

// The text shows only for the first query with no events. A stream that
// goes missing after a load gives the plain error, and the lines stay.
func TestECSH6_MissingStreamAfterALoad(t *testing.T) {
	missing := fmt.Errorf("%w: s1 in /app", aws.ErrLogStreamNotFound)
	f := &fakeEvents{events: []aws.LogEvent{{Time: evNow.Add(-time.Minute), Message: "msg-1", ID: "1"}}}
	v := newEvents(t, f, streamParams())
	require.Contains(t, plain(v.View().Content), "msg-1")
	f.get = func(aws.EventQuery) (*aws.EventPage, error) { return nil, missing }
	msgs := pressEv(v, keyPress('r'))
	var errs int
	for _, m := range msgs {
		if _, ok := m.(msg.ErrorMsg); ok {
			errs++
		}
	}
	assert.Equal(t, 1, errs, "the status bar shows the error")
	assert.False(t, v.noStream)
	assert.Contains(t, plain(v.View().Content), "msg-1", "the lines stay")
	assert.NotContains(t, v.Footer(), "No log stream")

	// A first load that brings events and then fails on a later page
	// keeps the events.
	calls := 0
	g := &fakeEvents{get: func(q aws.EventQuery) (*aws.EventPage, error) {
		calls++
		if calls == 1 {
			tok := "older"
			return &aws.EventPage{Events: []aws.LogEvent{{Time: evNow.Add(-time.Minute), Message: "msg-2", ID: "2"}}, Next: &tok}, nil
		}
		return nil, missing
	}}
	w := newEvents(t, g, streamParams())
	assert.False(t, w.noStream)
	assert.Contains(t, plain(w.View().Content), "msg-2")
}

// A new range that also finds no stream puts the query back to the one
// that the view shows.
func TestECSH6_MissingStreamKeepsTheQuery(t *testing.T) {
	f := &fakeEvents{get: func(aws.EventQuery) (*aws.EventPage, error) {
		return nil, fmt.Errorf("%w: s1 in /app", aws.ErrLogStreamNotFound)
	}}
	v := newEvents(t, f, streamParams())
	require.True(t, v.noStream)
	_, cmd := v.Update(msg.PickerResultMsg{Tag: &logsRangeTag{}, Value: "3h"})
	pump(v, cmd, 20)
	assert.True(t, v.noStream)
	assert.Equal(t, logQuery{rng: v.walk.rng, pattern: v.walk.pattern}, v.want)
	assert.Equal(t, time.Hour, v.want.rng.dur)
}

// Live tail can wait for a stream that did not exist. When its lines come,
// the footer counts them, and no longer says that the stream is missing.
func TestECSH6_LiveTailAfterAMissingStream(t *testing.T) {
	f := newTailFake()
	f.get = func(aws.EventQuery) (*aws.EventPage, error) {
		return nil, fmt.Errorf("%w: s1 in /app", aws.ErrLogStreamNotFound)
	}
	v := newTailView(t, f, streamParams())
	require.True(t, v.noStream)
	_, read := startTail(t, v)
	assert.Contains(t, v.Footer(), "No log stream", "no lines yet")
	f.push(&aws.TailUpdate{Events: []aws.LogEvent{tailEv(0, "msg-new", "s1")}}, nil)
	step(v, read)
	assert.Contains(t, plain(v.View().Content), "msg-new")
	assert.Equal(t, "1 events from live tail · r to load the range", v.Footer())
}

// A missing group keeps its own text and missing_hint, so the Lambda jump
// does not change.
func TestECSH6_MissingGroupKeepsItsText(t *testing.T) {
	f := &fakeEvents{}
	v := newEvents(t, f, map[string]string{"log_group": "/gone", "log_stream": "s1",
		"missing_hint": "group hint", "missing_stream_hint": "stream hint"})
	text := plain(v.View().Content)
	assert.Contains(t, text, "No log group named /gone. group hint")
	assert.NotContains(t, text, "stream hint")
}
