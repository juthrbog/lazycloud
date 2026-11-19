package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/config"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// These tests pin the behavior that docs/design/phase9-profiles.md changes
// (TestP9Hazard_*) and the behavior that must stay (TestP9Keep_*). The PR
// that fixes a hazard changes its test. The AWS side is in
// internal/aws/p9_pins_test.go.

// readWrite asks for ReadWrite on a fresh mode for one test, as main does
// for --read-write. The cleanup leaves a fresh ReadOnly mode, so no lock
// of a test stays for the next one.
func readWrite(t *testing.T) {
	t.Helper()
	ui.SetCurrentMode(ui.NewSessionMode())
	ui.SetReadOnly(false)
	t.Cleanup(func() { ui.SetCurrentMode(ui.NewSessionMode()) })
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

// prodRule locks the profiles prod-* and gives them the label PROD.
var prodRule = config.Rule{Name: "prod", Profiles: []string{"prod-*"}, ReadOnly: true, Label: "PROD"}

// modelWithRules starts the app with profile and rules, against a closed
// local port. The cleanup leaves a fresh mode, so a lock does not stay for
// the next test.
func modelWithRules(t *testing.T, profile string, rules ...config.Rule) Model {
	t.Helper()
	t.Cleanup(func() { ui.SetCurrentMode(ui.NewSessionMode()) })
	cfg := config.DefaultConfig()
	cfg.AWS.Profile = profile
	cfg.AWS.Endpoint = "http://127.0.0.1:1"
	cfg.Rules = rules
	m := New(cfg)
	m.width, m.height = 140, 40
	return m
}

// H2, fixed in PR 3: the mode picker refuses ReadWrite for a profile that a
// rule locks, and says why. Other profiles still switch.
func TestP9_H2_LockedProfileRefusesReadWrite(t *testing.T) {
	awsConfigEnv(t, "prod-admin", "dev")
	t.Cleanup(func() { ui.SetReadOnly(true) })
	m := modelWithRules(t, "prod-admin", prodRule)
	require.NoError(t, m.svcErr)

	m, _ = m.applyMode("readwrite")

	assert.True(t, ui.IsReadOnly())
	assert.Contains(t, rendered(m), "prod-admin is locked to ReadOnly by rule prod")
	require.ErrorIs(t, m.svc.S3.DeleteBucket(t.Context(), "b"), aws.ErrReadOnly)

	m, _ = m.applyProfile("dev")
	m, _ = m.applyMode("readwrite")
	assert.False(t, ui.IsReadOnly(), "a profile that no rule locks switches")
}

// H3, fixed in PR 3: the mode is on screen below 60 columns, and in the
// status bar when the app hides the header on a short terminal.
func TestP9_H3_ModeOnSmallTerminals(t *testing.T) {
	narrow := ansi.Strip(ui.RenderHeader(ui.HeaderData{Profile: "prod", Region: "us-east-1", Mode: "RO", Width: 50}))
	assert.Contains(t, narrow, " RO ")

	awsConfigEnv(t, "prod-admin")
	m := modelWithRules(t, "prod-admin", prodRule)
	m.width, m.height = 140, 10
	out := rendered(m)
	assert.NotContains(t, out, "LazyCloud", "the header is hidden")
	last := out[strings.LastIndex(out, "\n")+1:]
	assert.True(t, strings.HasPrefix(last, " PROD "), last)
	assert.Contains(t, last, " RO")

	m.height = 40
	last = rendered(m)
	last = last[strings.LastIndex(last, "\n")+1:]
	assert.NotContains(t, last, "PROD", "with the header on screen, the status bar has no badge")
}

// PR 3: --read-write on a locked profile starts in ReadOnly. main prints
// the warning, and the app shows it.
func TestP9_ReadWriteFlagOnALockedProfile(t *testing.T) {
	awsConfigEnv(t, "prod-admin")
	readWrite(t)

	m := modelWithRules(t, "prod-admin", prodRule)

	assert.True(t, ui.IsReadOnly())
	want := "--read-write ignored: profile prod-admin is locked to ReadOnly by rule prod"
	assert.Equal(t, []string{want}, m.StartWarnings())
	assert.Contains(t, rendered(m), want)
}

// PR 3: --read-write on a profile that no rule locks starts in ReadWrite,
// with no warning.
func TestP9_ReadWriteFlagOnAnUnlockedProfile(t *testing.T) {
	awsConfigEnv(t, "dev")
	readWrite(t)

	m := modelWithRules(t, "dev", prodRule)

	assert.False(t, ui.IsReadOnly())
	assert.Empty(t, m.StartWarnings())
}

// PR 3: a switch to a locked profile says so, and the header shows the
// label and the lock. A region switch of a locked profile stays locked.
func TestP9_SwitchToALockedProfile(t *testing.T) {
	awsConfigEnv(t, "prod-admin")
	readWrite(t)
	m := modelWithRules(t, "", prodRule)
	require.False(t, ui.IsReadOnly())

	m, _ = m.applyProfile("prod-admin")

	assert.True(t, ui.IsReadOnly())
	out := rendered(m)
	assert.Contains(t, out, "Profile: prod-admin · ReadOnly (locked by rule prod)")
	assert.Contains(t, out, " PROD ")
	assert.Equal(t, prodRule.Label, m.match.Label)

	m, _ = m.applyRegion("eu-west-1")
	locked, rule := ui.CurrentMode().Locked()
	assert.True(t, locked)
	assert.Equal(t, "prod", rule)
	assert.Contains(t, rendered(m), "Region: eu-west-1 · ReadOnly (locked by rule prod)")
}

// PR 3: on a locked session, the key guard and the mode picker name the
// rule.
func TestP9_LockedSessionNamesTheRule(t *testing.T) {
	awsConfigEnv(t, "prod-admin")
	m := modelWithRules(t, "prod-admin", prodRule)

	m.showModePicker()
	assert.Contains(t, rendered(m), "ReadWrite   (locked by rule prod)")

	m = modelWithRules(t, "prod-admin", prodRule)
	m.svc.S3 = new(awstest.MockS3Service)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "s3_list"})
	require.Equal(t, "s3_list", m.nav.Current().ID())
	m, _ = update(t, m, press("n"))
	assert.Contains(t, rendered(m), "ReadOnly mode (locked by rule prod)")
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
