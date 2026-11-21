package views

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/ui"
)

// Pin tests for the Lambda design (docs/design/lambda.md). They pin the
// hazards as they are today (TestLambdaHazard_*). The PR that fixes a
// hazard flips its test (TestLambdaH4_*). H1 to H3 and H11 are facts of the AWS API; PR 1
// tests them against httptest fakes. The REPORT parser tests of PR 5 cover
// H9. H7, H8 and H10 are facts of AWS and LocalStack that no unit test can
// pin; the design handles them with missing_hint, the seed rules, and no
// GetFunction for each row.

// H4, fixed in PR 2: StateColor colored only lowercase states, and Lambda
// sends "Active", "Failed" and "Pending". Mappings send "Enabled". It now
// compares without case, keeps the case of the text, and knows the new
// values.
func TestLambdaH4_StateColorIgnoresCase(t *testing.T) {
	for state, like := range map[string]string{
		"Active": "active", "Failed": "failed", "Pending": "pending",
		"Enabled": "running", "Disabled": "stopped", "InProgress": "pending",
		"Successful": "running", "Enabling": "pending", "Updating": "pending", "Disabling": "pending",
	} {
		got := ui.StateColor(state)
		require.NotEqual(t, state, got, "%s gets a color", state)
		assert.Equal(t, strings.Replace(ui.StateColor(like), like, state, 1), got, "%s looks like %s", state, like)
	}
	assert.NotEqual(t, "Inactive", ui.StateColor("Inactive"), "Inactive is muted")
	assert.Equal(t, "Unknown", ui.StateColor("Unknown"))
}

// H5, stays: the app binds L to the event log, and the conformance test
// forbids E. Issue #124 asked for L to jump to the logs, so the function
// list uses enter for that (decision D4).
func TestLambdaHazard_LAndEAreTaken(t *testing.T) {
	assert.Equal(t, []string{"L"}, ui.DefaultAppKeyMap().EventLog.Keys(), "the app owns L")
	assert.True(t, slices.Contains(globalLetters, "L"))
	assert.True(t, slices.Contains(globalLetters, "E"), "the conformance test forbids E")
}

// H6, fixed in PR 4: the event viewer has no route parameter for a time
// range, and its text for a missing group gives no reason.
func TestLambdaHazard_EventViewerIgnoresARange(t *testing.T) {
	f := &fakeEvents{}
	start, end := evNow.Add(-10*time.Minute), evNow.Add(-9*time.Minute)
	v := newEvents(t, f, map[string]string{"log_group": "/app", "log_group_arn": evGroup.ARN,
		"log_stream": "s1",
		"log_start":  strconv.FormatInt(start.UnixMilli(), 10), "log_end": strconv.FormatInt(end.UnixMilli(), 10)})
	require.NotEmpty(t, f.calls)
	assert.True(t, f.calls[0].q.Start.Equal(evNow.Add(-time.Hour)), "the range parameters are ignored: start %v", f.calls[0].q.Start)
	assert.Contains(t, plain(v.View().Content), "last 1h")

	f2 := &fakeEvents{} // no group: GetLogGroup says not found
	v2 := newEvents(t, f2, map[string]string{"log_group": "/aws/lambda/never-ran",
		"missing_hint": "Lambda makes it at the first invocation of the function."})
	assert.Contains(t, plain(v2.View().Content), "No log group named /aws/lambda/never-ran")
	assert.NotContains(t, plain(v2.View().Content), "first invocation")
}
