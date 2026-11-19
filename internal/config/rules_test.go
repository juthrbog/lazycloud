package config

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchSession(t *testing.T) {
	c := Config{Rules: []Rule{
		{Name: "prod", Profiles: []string{"prod-*", "*-production"}, ReadOnly: true, Label: "PROD"},
		{Name: "acct", Accounts: []string{"123456789012"}, ReadOnly: true},
		{Name: "stage", Profiles: []string{"stage"}, Label: "STG"},
		{Name: "stage-lock", Profiles: []string{"stage"}, ReadOnly: true, Label: "LATER"},
		{Name: "default", Profiles: []string{"default"}, Label: "DEF"},
		{Name: "all-prod", Profiles: []string{"prod-*"}, ReadOnly: true},
	}}

	for _, tc := range []struct {
		profile, account string
		want             Match
	}{
		// all-prod also locks prod-admin, but the first locking rule names
		// the lock.
		{"prod-admin", "", Match{Lock: "prod", Label: "PROD", LabelLocks: true}},
		{"app-production", "", Match{Lock: "prod", Label: "PROD", LabelLocks: true}},
		{"production", "", Match{}},
		{"dev", "123456789012", Match{Lock: "acct"}},
		{"dev", "", Match{}},
		// The first label wins, and any lock wins.
		{"stage", "", Match{Lock: "stage-lock", Label: "STG"}},
		// The empty profile is the profile "default".
		{"", "", Match{Label: "DEF"}},
		{"default", "", Match{Label: "DEF"}},
	} {
		assert.Equal(t, tc.want, c.MatchSession(tc.profile, tc.account), "%s/%s", tc.profile, tc.account)
	}
}

func TestHasAccountRules(t *testing.T) {
	assert.False(t, Config{Rules: []Rule{{Profiles: []string{"a"}}}}.HasAccountRules())
	assert.True(t, Config{Rules: []Rule{{Profiles: []string{"a"}}, {Accounts: []string{"123456789012"}}}}.HasAccountRules())
}

// Load checks the rules. A bad entry is dropped with a warning, and the
// rest of the rule stays.
func TestLoadChecksTheRules(t *testing.T) {
	path := writeConfig(t, `
[[rules]]
name = "prod"
profiles = ["prod-*", "[bad"]
accounts = ["123456789012", "12345", "12345678901x", "1234567890123"]
read_only = true
label = "PRODUCTION"

[[rules]]
read_only = true

[[rules]]
name = "dev"
profiles = ["dev"]
lable = "DEV"

[[rules]]
name = "acct"
accounts = ["210987654321"]
read_only = true
`)

	cfg, warnings, err := Load(path)

	require.NoError(t, err)
	require.Len(t, cfg.Rules, 4)
	prod := cfg.Rules[0]
	assert.Equal(t, []string{"prod-*"}, prod.Profiles)
	assert.Equal(t, []string{"123456789012"}, prod.Accounts)
	assert.Empty(t, prod.Label)
	assert.True(t, prod.ReadOnly)
	assert.Equal(t, "#2", cfg.Rules[1].Name)
	assert.Equal(t, []string{
		`unknown key "rules.lable" in ` + path,
		`rule "prod" in ` + path + `: bad profile pattern "[bad"`,
		`rule "prod" in ` + path + `: account "12345" is not a 12-digit account ID`,
		`rule "prod" in ` + path + `: account "12345678901x" is not a 12-digit account ID`,
		`rule "prod" in ` + path + `: account "1234567890123" is not a 12-digit account ID`,
		`rule "prod" in ` + path + `: label "PRODUCTION" is longer than 8 characters, so it is not shown`,
		`rule "#2" in ` + path + `: the rule has no profiles and no accounts, so it matches nothing`,
	}, warnings)
	assert.Equal(t, "prod", cfg.MatchSession("prod-admin", "").Lock, "the good glob still locks")
}

// A label of exactly 8 characters stays.
func TestLoadKeepsALabelOf8Characters(t *testing.T) {
	path := writeConfig(t, "[[rules]]\nprofiles = [\"a\"]\nlabel = \"ABCDEFGH\"\n")

	cfg, warnings, err := Load(path)

	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Equal(t, "ABCDEFGH", cfg.Rules[0].Label)
}

// The commented example in the default file loads with no warnings when
// the user takes away the comment marks.
func TestDefaultFileRuleExampleLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	_, err := WriteDefault(path)
	require.NoError(t, err)
	data, err := os.ReadFile(path) //nolint:gosec // test file
	require.NoError(t, err)

	start := regexp.MustCompile(`(?m)^# \[\[rules\]\]$`).FindIndex(data)
	require.NotNil(t, start)
	example := regexp.MustCompile(`(?m)^# ?`).ReplaceAllString(string(data[start[0]:]), "")
	require.NoError(t, os.WriteFile(path, []byte(example), 0o600)) //nolint:gosec // path is a temp file of the test

	cfg, warnings, err := Load(path)
	require.NoError(t, err)
	assert.Empty(t, warnings)
	require.Len(t, cfg.Rules, 1)
	assert.Equal(t, Match{Lock: "prod", Label: "PROD", LabelLocks: true}, cfg.MatchSession("prod-admin", ""))
}
