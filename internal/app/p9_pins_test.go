package app

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

// H1, fixed in PR 2: a switch to another profile starts in ReadOnly, and
// the toast says so.
func TestP9_H1_ProfileSwitchStartsReadOnly(t *testing.T) {
	awsConfigEnv(t, "prod")
	readWrite(t)
	m := modelWithProfile("")
	require.False(t, ui.IsReadOnly())

	m, _ = m.applyProfile("prod")
	require.NoError(t, m.svcErr)
	require.Equal(t, "prod", m.config.AWS.Profile)

	assert.True(t, ui.IsReadOnly(), "the new profile is in ReadOnly")
	assert.Contains(t, rendered(m), "Profile: prod · ReadOnly")
	assert.ErrorIs(t, m.svc.S3.DeleteBucket(t.Context(), "b"), aws.ErrReadOnly)
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

// H7, fixed in PR 2: each session has its own mode. After a switch to a
// profile in ReadOnly, the old session can still finish a batch that it
// runs in the background.
func TestP9_H7_EachSessionHasItsOwnMode(t *testing.T) {
	awsConfigEnv(t, "prod", "prod2")
	readWrite(t)
	var deletes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletes.Add(1)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	cfg := config.DefaultConfig()
	cfg.AWS.Endpoint = srv.URL
	m := New(cfg)
	require.NoError(t, m.svcErr)
	old, oldMode := m.svc, ui.CurrentMode()

	m, _ = m.applyProfile("prod")
	require.NotSame(t, old.Client, m.svc.Client)
	require.NotSame(t, oldMode, ui.CurrentMode())

	require.ErrorIs(t, m.svc.S3.DeleteBucket(t.Context(), "b"), aws.ErrReadOnly, "the new session is in ReadOnly")
	require.NoError(t, old.S3.DeleteBucket(t.Context(), "b"), "the old session keeps ReadWrite")
	assert.Equal(t, int32(1), deletes.Load())
	assert.False(t, oldMode.ReadOnly())

	// The second session also keeps its own mode after the next switch.
	ui.SetReadOnly(false)
	second := m.svc
	m, _ = m.applyProfile("prod2")
	require.Equal(t, "prod2", m.config.AWS.Profile)
	require.NoError(t, second.S3.DeleteBucket(t.Context(), "b"), "the second session keeps ReadWrite")
	require.ErrorIs(t, m.svc.S3.DeleteBucket(t.Context(), "b"), aws.ErrReadOnly)
	assert.Equal(t, int32(2), deletes.Load())
}

// PR 2: a switch to another region of the same profile keeps the mode,
// because the account is the same.
func TestP9_RegionSwitchKeepsTheMode(t *testing.T) {
	for name, rw := range map[string]bool{"ReadWrite": true, "ReadOnly": false} {
		t.Run(name, func(t *testing.T) {
			awsConfigEnv(t, "prod")
			t.Cleanup(func() { ui.SetReadOnly(true) })
			ui.SetReadOnly(!rw)
			m := modelWithProfile("prod")
			oldMode := ui.CurrentMode()

			m, _ = m.applyRegion("eu-west-1")
			require.Equal(t, "eu-west-1", m.activeRegion())

			assert.NotSame(t, oldMode, ui.CurrentMode(), "the new session has its own mode")
			assert.Equal(t, !rw, ui.IsReadOnly())
			assert.Contains(t, rendered(m), "Region: eu-west-1")
			assert.NotContains(t, rendered(m), "· ReadOnly")
		})
	}
}

// PR 2: a failed switch keeps the session and its mode.
func TestP9_FailedSwitchKeepsTheMode(t *testing.T) {
	awsConfigEnv(t)
	readWrite(t)
	m := modelWithProfile("")
	oldMode := ui.CurrentMode()

	m, _ = m.applyProfile("typo")
	require.Contains(t, rendered(m), "Cannot switch profile to typo")

	assert.Same(t, oldMode, ui.CurrentMode())
	assert.False(t, ui.IsReadOnly())
}

// PR 2: --read-write asks for ReadWrite before the first session exists.
// The first session gets its own mode in ReadWrite.
func TestP9_StartRequestGoesToTheFirstSession(t *testing.T) {
	awsConfigEnv(t)
	ui.SetCurrentMode(ui.NewSessionMode())
	readWrite(t)
	before := ui.CurrentMode()

	m := modelWithProfile("")
	require.NoError(t, m.svcErr)

	assert.NotSame(t, before, ui.CurrentMode())
	assert.False(t, ui.IsReadOnly())
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
