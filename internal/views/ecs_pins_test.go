package views

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// Pin tests for the ECS design (docs/design/ecs.md). They pin the hazards as
// they are today (TestECSHazard_*). The PR that fixes a hazard flips its test
// (TestECSH3_*).
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

// H6, fixed in PR 4: the event viewer shows a plain error for a stream that
// does not exist. Only a missing group has its own text, and missing_hint is
// a hint for a missing group. PR 4 adds missing_stream_hint, and its fake
// returns aws.ErrLogStreamNotFound.
func TestECSHazard_MissingStreamIsAPlainError(t *testing.T) {
	f := &fakeEvents{get: func(aws.EventQuery) (*aws.EventPage, error) {
		return nil, errors.New("ResourceNotFoundException: The specified log stream does not exist.")
	}}
	p := streamParams()
	p["log_stream"] = "ecs/app/0123456789abcdef"
	p["missing_stream_hint"] = "The task writes the stream when its container starts."
	v := newEvents(t, f, p)
	require.NotEmpty(t, f.calls)
	text := plain(v.View().Content)
	assert.Contains(t, text, "does not exist")
	assert.NotContains(t, text, "No log stream named")
	assert.NotContains(t, text, "The task writes the stream", "the hint shows only for a missing group")
}
