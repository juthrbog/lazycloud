package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/credentials/ssocreds"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/config"
	"github.com/juthrbog/lazycloud/internal/ui"
)

const pickerProfiles = `[profile dev]
sso_session = corp
sso_account_id = 111111111111
sso_role_name = Dev

[profile prod-admin]
role_arn = arn:aws:iam::555555555555:role/Admin
source_profile = dev

[profile ci]
aws_access_key_id = AKIAEXAMPLE
aws_secret_access_key = secret

[profile mfa-role]
role_arn = arn:aws:iam::444444444444:role/Admin
source_profile = ci
mfa_serial = arn:aws:iam::444444444444:mfa/me

[profile broken]
sso_session = missing

[profile a-profile-with-a-very-long-name-indeed]
aws_access_key_id = AKIAEXAMPLE
aws_secret_access_key = secret

[sso-session corp]
sso_start_url = https://corp.example.com/start
sso_region = us-east-1
`

// pickerEnv isolates the AWS files, the home folder and the token caches,
// and writes a valid SSO token for the sso-session corp.
func pickerEnv(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config")
	require.NoError(t, os.WriteFile(cfgFile, []byte(pickerProfiles), 0o600))
	t.Setenv("AWS_CONFIG_FILE", cfgFile)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("AWS_LOGIN_CACHE_DIRECTORY", filepath.Join(dir, "login"))
	for _, v := range []string{"AWS_PROFILE", "AWS_REGION", "AWS_DEFAULT_REGION", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
		t.Setenv(v, "")
	}
	path, err := ssocreds.StandardCachedTokenFilepath("corp")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	expires := time.Now().Add(3*time.Hour + 30*time.Second).UTC().Format(time.RFC3339)
	require.NoError(t, os.WriteFile(path, []byte(`{"accessToken":"a","expiresAt":"`+expires+`"}`), 0o600))
}

// pickerLabels returns the rows of the profile picker by value, built the
// way openProfilePicker builds them. It also checks that the picker is
// open and shows the first row.
func pickerLabels(t *testing.T, m Model) map[string]string {
	t.Helper()
	require.True(t, m.picker.Visible(), "the profile picker is open")
	now := time.Now()
	out := map[string]string{}
	for _, o := range m.profileRows(aws.DescribeProfiles(now), now) {
		out[o.Value] = o.Label
	}
	assert.Contains(t, rendered(m), "Select Profile")
	return out
}

var acctRule = config.Rule{Name: "acct", Accounts: []string{"555555555555"}, ReadOnly: true, Label: "PROD"}

// PR 4: each row shows the kind, the account from the config, the token
// state, and the label and lock of the rules.
func TestP9_ProfilePickerRows(t *testing.T) {
	pickerEnv(t)
	ui.UseNerdFonts = false
	t.Cleanup(func() { ui.UseNerdFonts = true })
	m := modelWithRules(t, "dev", acctRule)

	m = openProfiles(t, m)
	rows := pickerLabels(t, m)

	assert.Regexp(t, `^dev ●\s+sso\s+111111111111\s+valid (2h59|3h00)m$`, rows["dev"])
	assert.Regexp(t, `^prod-admin\s+role>sso\s+555555555555\s+valid (2h59|3h00)m\s+PROD RO\*$`, rows["prod-admin"], "a rule by account")
	assert.Regexp(t, `^ci\s+keys\s+-$`, rows["ci"])
	assert.Regexp(t, `^mfa-role\s+role>keys\s+444444444444\s+mfa \(not supported\)$`, rows["mfa-role"])
	assert.Regexp(t, `^broken\s+invalid: sso-session$`, rows["broken"])
	long := rows["a-profile-with-a-very-long-name-indeed"]
	assert.True(t, strings.HasPrefix(long, "a-profile-with-a-very-l… keys"), long)
}

// PR 4: below 80 columns, the account column goes.
func TestP9_ProfilePickerOnANarrowTerminal(t *testing.T) {
	pickerEnv(t)
	m := modelWithRules(t, "dev")
	m.width = 70

	rows := pickerLabels(t, openProfiles(t, m))

	assert.Regexp(t, `^dev ●\s+sso\s+valid (2h59|3h00)m$`, rows["dev"])
	assert.NotContains(t, rows["dev"], "111111111111")
}

// PR 4: a pick of a profile that does not load, or that needs an MFA code,
// keeps the session and says why.
func TestP9_PickOfAProfileThatCannotWork(t *testing.T) {
	pickerEnv(t)
	for name, want := range map[string]string{
		"broken":   "Cannot use profile broken:",
		"mfa-role": "Cannot use profile mfa-role: " + aws.MFANotSupported,
	} {
		t.Run(name, func(t *testing.T) {
			m := modelWithRules(t, "dev")
			before := m.svc.Client

			m = openProfiles(t, m)
			m = typeInto(t, m, name)
			m = answer(t, m, keyEnter)

			assert.Equal(t, "dev", m.config.AWS.Profile)
			assert.Same(t, before, m.svc.Client)
			assert.Contains(t, rendered(m), want)
		})
	}
}

// PR 4: a pick of a good profile switches to it.
func TestP9_PickOfAGoodProfile(t *testing.T) {
	pickerEnv(t)
	m := modelWithRules(t, "dev")

	m = openProfiles(t, m)
	m = typeInto(t, m, "ci")
	m = answer(t, m, keyEnter)

	assert.Equal(t, "ci", m.config.AWS.Profile)
}

// PR 4: a prompt that opened while the profiles loaded wins.
func TestP9_ProfilesThatComeLateDoNotCoverAPrompt(t *testing.T) {
	pickerEnv(t)
	m := modelWithRules(t, "dev")
	m, cmd := update(t, m, press("P"))
	got := findOf[profilesMsg](collectMsgs(cmd))
	require.NotNil(t, got)

	m.askQuit("Quit?")
	m, _ = update(t, m, got)

	assert.False(t, m.picker.Visible())
	assert.True(t, m.quitAsked())
	assert.False(t, m.toasts.HasActive(), "the late picker goes away with no refusal")
}

// PR 4: without profiles, the app says so.
func TestP9_NoProfiles(t *testing.T) {
	awsConfigEnv(t)
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "none"))
	m := newTestModel(140, 40)

	m, _ = update(t, m, profilesMsg{})

	assert.False(t, m.picker.Visible())
	assert.Contains(t, rendered(m), "No profiles found")
}

// PR 4: a rule by account locks a session from the account in the config,
// at start and on a switch.
func TestP9_RuleByAccountLocksFromTheConfig(t *testing.T) {
	pickerEnv(t)
	readWrite(t)

	m := modelWithRules(t, "prod-admin", acctRule)
	locked, rule := ui.CurrentMode().Locked()
	assert.True(t, locked, "locked at start")
	assert.Equal(t, "acct", rule)
	assert.Equal(t, []string{"--read-write ignored: profile prod-admin is locked to ReadOnly by rule acct"}, m.StartWarnings())

	m, _ = m.applyProfile("dev")
	locked, _ = ui.CurrentMode().Locked()
	assert.False(t, locked, "dev is in another account")

	m, _ = m.applyProfile("prod-admin")
	locked, _ = ui.CurrentMode().Locked()
	assert.True(t, locked, "locked on a switch")
	assert.Equal(t, "PROD", m.match.Label)
}
