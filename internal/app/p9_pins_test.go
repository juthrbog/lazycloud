package app

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/config"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// These tests pin the behavior that docs/design/phase9-profiles.md changes
// (TestP9Hazard_*) and the behavior that must stay (TestP9Keep_*). The PR
// that fixes a hazard changes its test. The AWS side is in
// internal/aws/p9_pins_test.go.

// readWrite turns ReadWrite on for one test.
func readWrite(t *testing.T) {
	t.Helper()
	ui.SetReadOnly(false)
	t.Cleanup(func() { ui.SetReadOnly(true) })
}

// H1: ReadWrite stays on when the user switches to another profile.
func TestP9Hazard_H1_ReadWriteStaysOnOverAProfileSwitch(t *testing.T) {
	awsConfigEnv(t, "prod")
	readWrite(t)
	m := modelWithProfile("")

	m, _ = m.applyProfile("prod")
	require.NoError(t, m.svcErr)
	require.Equal(t, "prod", m.config.AWS.Profile)

	assert.False(t, ui.IsReadOnly(), "the new profile is in ReadWrite")
}

// H2: the mode picker turns ReadWrite on for any profile.
func TestP9Hazard_H2_ReadWriteForAnyProfile(t *testing.T) {
	awsConfigEnv(t, "prod-admin")
	t.Cleanup(func() { ui.SetReadOnly(true) })
	m := modelWithProfile("prod-admin")
	require.NoError(t, m.svcErr)

	m.applyMode("readwrite")

	assert.False(t, ui.IsReadOnly())
}

// H3: below 60 columns, and when the app hides the header on a short
// terminal, the mode is not on the screen.
func TestP9Hazard_H3_NoModeBadgeOnSmallTerminals(t *testing.T) {
	narrow := ansi.Strip(ui.RenderHeader(ui.HeaderData{Profile: "prod", Region: "us-east-1", Mode: "RO", Width: 50}))
	assert.NotContains(t, narrow, " RO ")

	m := newTestModel(140, 10)
	assert.NotContains(t, rendered(m), " RO ", "the header is hidden")
	assert.NotContains(t, rendered(m), "LazyCloud")
}

// H7: every session reads the one global mode. A reset of the mode after a
// switch also changes the mode of the old session, so a batch that the old
// session runs in the background stops.
func TestP9Hazard_H7_SessionsShareOneMode(t *testing.T) {
	awsConfigEnv(t, "prod")
	readWrite(t)
	m := modelWithProfile("")
	require.NoError(t, m.svcErr)
	old := m.svc

	m, _ = m.applyProfile("prod")
	require.NotSame(t, old.Client, m.svc.Client)
	ui.SetReadOnly(true)

	assert.ErrorIs(t, old.S3.DeleteBucket(t.Context(), "b"), aws.ErrReadOnly, "the old session follows the new mode")
}

// The app starts in ReadOnly mode.
func TestP9Keep_StartsReadOnly(t *testing.T) {
	awsConfigEnv(t)
	m := New(config.DefaultConfig())
	m.width, m.height = 140, 40

	assert.True(t, ui.IsReadOnly())
	assert.Contains(t, rendered(m), " RO ")
}

// The header shows the mode at 80 columns.
func TestP9Keep_ModeBadgeAt80Columns(t *testing.T) {
	for mode, want := range map[string]string{"RO": " RO ", "RW": " RW "} {
		got := ansi.Strip(ui.RenderHeader(ui.HeaderData{Profile: "prod", Region: "us-east-1", Mode: mode, Width: 80}))
		assert.Contains(t, got, want)
	}
}

// TestFailedProfileSwitchKeepsSession and TestSwitchedSessionIsGuarded in
// session_test.go pin the other behavior that stays: a failed switch keeps
// the session, and a switched session is guarded.
