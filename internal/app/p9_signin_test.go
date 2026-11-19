package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/credentials/ssocreds"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// cliOnPath puts a fake aws CLI on PATH, so the app finds it. The app
// tests never run it.
func cliOnPath(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake aws CLI is a shell script")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "aws"), []byte("#!/bin/sh\nexit 0\n"), 0o700)) //nolint:gosec // a fake CLI for the test
	t.Setenv("PATH", dir)
}

// signedOutEnv is pickerEnv with no SSO token: the profiles dev and
// prod-admin are not signed in.
func signedOutEnv(t *testing.T) {
	t.Helper()
	pickerEnv(t)
	path, err := ssocreds.StandardCachedTokenFilepath("corp")
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))
}

// H4, fixed in PR 5: a pick of a profile that is not signed in offers a
// sign-in before the switch.
func TestP9_H4_PickOfASignedOutProfileOffersASignIn(t *testing.T) {
	signedOutEnv(t)
	cliOnPath(t)
	m := modelWithRules(t, "ci")

	m = openProfiles(t, m)
	m = typeInto(t, m, "prod-admin")
	m = answer(t, m, keyEnter)

	assert.Contains(t, rendered(m), "prod-admin needs aws sso login (not signed in). Sign in now?")
	assert.Equal(t, "ci", m.config.AWS.Profile, "no switch before the answer")

	t.Run("yes signs in", func(t *testing.T) {
		m, cmd := update(t, m, press("y"))
		require.NotNil(t, cmd, "the sign-in runs in the terminal")
		assert.Equal(t, "ci", m.config.AWS.Profile, "the switch waits for the sign-in")
	})
	t.Run("no switches anyway", func(t *testing.T) {
		m := answer(t, m, press("n"))
		assert.Equal(t, "prod-admin", m.config.AWS.Profile)
		assert.True(t, ui.IsReadOnly())
	})
}

// An expired token says so in the offer.
func TestP9_OfferNamesAnExpiredToken(t *testing.T) {
	signedOutEnv(t)
	path, err := ssocreds.StandardCachedTokenFilepath("corp")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(`{"accessToken":"a","expiresAt":"2020-01-01T00:00:00Z"}`), 0o600))
	m := modelWithRules(t, "ci")

	m, _ = m.pickProfile(aws.ClassifyProfile("dev", time.Now()))

	assert.Contains(t, rendered(m), "dev needs aws sso login (token expired). Sign in now?")
}

// The offer names aws login for an aws login profile.
func TestP9_OfferNamesAWSLogin(t *testing.T) {
	pickerEnv(t)
	m := modelWithRules(t, "ci")

	m, _ = m.pickProfile(aws.ClassifyProfile("web", time.Now()))

	assert.Contains(t, rendered(m), "web needs aws login (not signed in). Sign in now?")
}

// A sign-in to the default profile names it in the toast. The session
// keeps the empty profile.
func TestP9_SignInToTheDefaultProfile(t *testing.T) {
	pickerEnv(t)
	m := modelWithRules(t, "")

	m, _ = update(t, m, signInDoneMsg{target: "", info: aws.ProfileInfo{Name: "default", Kind: aws.KindSSO, Root: "default"}})

	assert.Empty(t, m.config.AWS.Profile)
	assert.Contains(t, rendered(m), "Profile: default (signed in)")
}

// PR 5: after a good sign-in, the app builds the session again. It starts
// in ReadOnly, also for the current profile.
func TestP9_SignInBuildsTheSessionAgain(t *testing.T) {
	pickerEnv(t)
	readWrite(t)
	m := modelWithRules(t, "dev")
	require.False(t, ui.IsReadOnly())
	before := m.svc.Client

	m, _ = update(t, m, signInDoneMsg{target: "dev", info: aws.ClassifyProfile("dev", time.Now())})

	assert.NotSame(t, before, m.svc.Client, "a new session")
	assert.Equal(t, "dev", m.config.AWS.Profile)
	assert.True(t, ui.IsReadOnly(), "a sign-in starts in ReadOnly")
	assert.Contains(t, rendered(m), "Profile: dev (signed in) · ReadOnly")
}

// PR 5: a sign-in to another profile switches to it.
func TestP9_SignInSwitchesToTheTarget(t *testing.T) {
	pickerEnv(t)
	m := modelWithRules(t, "ci")

	m, _ = update(t, m, signInDoneMsg{target: "prod-admin", info: aws.ClassifyProfile("prod-admin", time.Now())})

	assert.Equal(t, "prod-admin", m.config.AWS.Profile)
}

// PR 5: a cancelled or failed sign-in changes nothing.
func TestP9_SignInThatDoesNotWork(t *testing.T) {
	pickerEnv(t)
	login := aws.ProfileInfo{Name: "web", Kind: aws.KindLogin, Root: "web"}
	sso := aws.ProfileInfo{Name: "dev", Kind: aws.KindSSO, Root: "dev"}
	for name, tc := range map[string]struct {
		msg  signInDoneMsg
		want string
	}{
		"cancelled":    {signInDoneMsg{target: "dev", info: sso, err: exitErr(t, 130)}, "Sign-in cancelled"},
		"failed":       {signInDoneMsg{target: "dev", info: sso, err: exitErr(t, 1)}, "Sign-in failed for dev (exit status 1)"},
		"old CLI":      {signInDoneMsg{target: "web", info: login, err: exitErr(t, 2), output: "Invalid choice: invalid choice 'login'"}, "aws login needs AWS CLI 2.32 or later"},
		"old CLI, sso": {signInDoneMsg{target: "dev", info: sso, err: exitErr(t, 2), output: "invalid choice"}, "Sign-in failed for dev (exit status 2)"},
	} {
		t.Run(name, func(t *testing.T) {
			m := modelWithRules(t, "ci")
			before := m.svc.Client

			m, _ = update(t, m, tc.msg)

			assert.Same(t, before, m.svc.Client)
			assert.Equal(t, "ci", m.config.AWS.Profile)
			assert.Contains(t, rendered(m), tc.want)
		})
	}
}

// exitErr returns the error of a process that exits with code.
func exitErr(t *testing.T, code int) error {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the exit code comes from sh")
	}
	err := exec.CommandContext(t.Context(), "/bin/sh", "-c", "exit "+strconv.Itoa(code)).Run() //nolint:gosec // a fixed shell command with a number
	require.Error(t, err)
	return err
}

// PR 5: :login signs in to the current profile, or to the profile it
// names. It says why when it cannot.
func TestP9_LoginCommand(t *testing.T) {
	pickerEnv(t)
	cliOnPath(t)

	m := modelWithRules(t, "dev")
	_, cmd := m.executeCommand("login")
	assert.NotNil(t, cmd, "the sign-in runs in the terminal")

	_, cmd = m.executeCommand("login prod-admin")
	assert.NotNil(t, cmd)

	for input, want := range map[string]string{
		"login ci":     "Profile ci has no sign-in (keys)",
		"login broken": "Cannot use profile broken:",
		"theme nord":   "Unknown command: theme nord",
	} {
		m := modelWithRules(t, "dev")
		m, _ = m.executeCommand(input)
		assert.Contains(t, rendered(m), want, input)
	}
}

// Without the aws CLI, the app names the command to run in another
// terminal.
func TestP9_LoginWithoutTheCLI(t *testing.T) {
	pickerEnv(t)
	t.Setenv("PATH", t.TempDir())
	m := modelWithRules(t, "prod-admin")

	m, cmd := m.executeCommand("login")

	assert.NotNil(t, cmd, "the toast")
	assert.Contains(t, rendered(m), "aws CLI not found. Sign in from another terminal: aws sso login --profile dev")
}
