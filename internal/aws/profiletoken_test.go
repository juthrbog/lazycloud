package aws

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/credentials/logincreds"
	"github.com/aws/aws-sdk-go-v2/credentials/ssocreds"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// p9Now is the fixed time of the token tests.
var p9Now = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

// writeSSOToken writes the cached SSO token of key at the path of the SDK.
func writeSSOToken(t *testing.T, key, data string) {
	t.Helper()
	path, err := ssocreds.StandardCachedTokenFilepath(key)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	writeFile(t, path, data)
}

// writeLoginToken writes the cached login token of session at the path of
// the SDK.
func writeLoginToken(t *testing.T, cache, session, data string) {
	t.Helper()
	path, err := logincreds.StandardCachedTokenFilepath(session, cache)
	require.NoError(t, err)
	writeFile(t, path, data)
}

const loginSession = "arn:aws:iam::222222222222:user/me"

func loginTokenJSON(expires string) string {
	return `{"accessToken":{"accessKeyId":"AKIA","secretAccessKey":"s","sessionToken":"t","accountId":"222222222222","expiresAt":"` +
		expires + `"},"refreshToken":"r","clientId":"c","dpopKey":"k"}`
}

func TestTokenStateOfAnSSOSession(t *testing.T) {
	for _, tc := range []struct {
		name, token string
		want        TokenState
		text        string
	}{
		{"valid", `{"accessToken":"a","expiresAt":"2026-11-18T14:05:00Z"}`, TokenValid, "valid 2h05m"},
		{"valid minutes", `{"accessToken":"a","expiresAt":"2026-11-18T12:07:00Z"}`, TokenValid, "valid 7m"},
		{"valid an hour", `{"accessToken":"a","expiresAt":"2026-11-18T13:05:00Z"}`, TokenValid, "valid 1h05m"},
		{"refresh", `{"accessToken":"a","expiresAt":"2026-11-18T11:00:00Z","refreshToken":"r","clientId":"c","clientSecret":"s"}`, TokenRefresh, "refresh"},
		{"no client secret", `{"accessToken":"a","expiresAt":"2026-11-18T11:00:00Z","refreshToken":"r","clientId":"c"}`, TokenExpired, "expired"},
		{"no refresh token", `{"accessToken":"a","expiresAt":"2026-11-18T11:00:00Z","clientId":"c","clientSecret":"s"}`, TokenExpired, "expired"},
		{"no client id", `{"accessToken":"a","expiresAt":"2026-11-18T11:00:00Z","refreshToken":"r","clientSecret":"s"}`, TokenExpired, "expired"},
		{"expires now", `{"accessToken":"a","expiresAt":"2026-11-18T12:00:00Z"}`, TokenExpired, "expired"},
		{"garbage", `not json`, TokenUnknown, "unknown"},
		{"no access token", `{"expiresAt":"2026-11-18T14:00:00Z"}`, TokenUnknown, "unknown"},
		{"no expiry", `{"accessToken":"a"}`, TokenUnknown, "unknown"},
		{"bad expiry", `{"accessToken":"a","expiresAt":"tomorrow"}`, TokenUnknown, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p9Env(t, p9Profiles)
			writeSSOToken(t, "corp", tc.token)

			info := ClassifyProfile("sso", p9Now)

			assert.Equal(t, tc.want, info.Token)
			assert.Equal(t, tc.text, info.TokenText(p9Now))
			assert.Equal(t, "corp", info.TokenKey)
		})
	}
}

func TestTokenStateWithoutAFile(t *testing.T) {
	p9Env(t, p9Profiles)

	for _, name := range []string{"sso", "sso-old", "login", "role-sso"} {
		info := ClassifyProfile(name, p9Now)
		assert.Equal(t, TokenMissing, info.Token, name)
		assert.Equal(t, "not signed in", info.TokenText(p9Now), name)
	}
	for _, name := range []string{"keys", "process", "mfa-role", "empty", "nope"} {
		info := ClassifyProfile(name, p9Now)
		assert.Equal(t, TokenNA, info.Token, name)
		assert.Empty(t, info.TokenText(p9Now), name)
	}
}

// A legacy SSO token is keyed by the start URL, and the SDK never refreshes
// it.
func TestTokenStateOfALegacyProfile(t *testing.T) {
	p9Env(t, p9Profiles)
	writeSSOToken(t, "https://old.example.com/start",
		`{"accessToken":"a","expiresAt":"2026-11-18T11:00:00Z","refreshToken":"r","clientId":"c","clientSecret":"s"}`)

	info := ClassifyProfile("sso-old", p9Now)
	assert.Equal(t, TokenExpired, info.Token)
	assert.Equal(t, "https://old.example.com/start", info.TokenKey)
	assert.Equal(t, time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC), info.Expires)

	writeSSOToken(t, "https://old.example.com/start", `{"accessToken":"a","expiresAt":"2026-11-18T13:00:00Z"}`)
	assert.Equal(t, TokenValid, ClassifyProfile("sso-old", p9Now).Token)
}

func TestTokenStateOfALoginSession(t *testing.T) {
	for _, tc := range []struct {
		name, token string
		want        TokenState
		account     string
	}{
		{"valid", loginTokenJSON("2026-11-18T12:30:00Z"), TokenValid, "222222222222"},
		// The SDK refreshes an expired login token with its refresh token.
		{"expired", loginTokenJSON("2026-11-18T11:30:00Z"), TokenRefresh, "222222222222"},
		{"garbage", `{`, TokenUnknown, ""},
		{"no access token", `{"refreshToken":"r","clientId":"c","dpopKey":"k"}`, TokenUnknown, ""},
		{"no dpop key", `{"accessToken":{"accessKeyId":"AKIA","secretAccessKey":"s","sessionToken":"t","accountId":"222222222222","expiresAt":"2026-11-18T12:30:00Z"},"refreshToken":"r","clientId":"c"}`, TokenUnknown, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, cache := p9Env(t, p9Profiles)
			writeLoginToken(t, cache, loginSession, tc.token)

			info := ClassifyProfile("login", p9Now)

			assert.Equal(t, tc.want, info.Token)
			assert.Equal(t, tc.account, info.Account, "the account comes from the token")
			assert.Equal(t, loginSession, info.TokenKey)
		})
	}
}

// Each field that the SDK requires in a login token makes the state
// unknown when it is missing.
func TestLoginTokenNeedsEachField(t *testing.T) {
	full := map[string]string{
		"accessKeyId": `"AKIA"`, "secretAccessKey": `"s"`, "sessionToken": `"t"`,
		"accountId": `"222222222222"`, "expiresAt": `"2026-11-18T12:30:00Z"`,
	}
	top := map[string]string{"refreshToken": `"r"`, "clientId": `"c"`, "dpopKey": `"k"`}
	build := func(skip string) string {
		inner := ""
		for k, v := range full {
			if k != skip {
				inner += `,"` + k + `":` + v
			}
		}
		out := `{"accessToken":{` + inner[1:] + `}`
		for k, v := range top {
			if k != skip {
				out += `,"` + k + `":` + v
			}
		}
		return out + "}"
	}
	for field := range mergeKeys(full, top) {
		t.Run(field, func(t *testing.T) {
			_, cache := p9Env(t, p9Profiles)
			writeLoginToken(t, cache, loginSession, build(field))
			assert.Equal(t, TokenUnknown, ClassifyProfile("login", p9Now).Token)
		})
	}
}

func mergeKeys(a, b map[string]string) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

// The account from the config: the account of the role that the session
// ends in, else the SSO account, else the account of the login token.
func TestAccountFromTheConfig(t *testing.T) {
	_, cache := p9Env(t, p9Profiles+`
[profile role-sso-acct]
role_arn = arn:aws:iam::555555555555:role/Admin
source_profile = sso-acct

[profile sso-acct]
sso_session = corp
sso_account_id = 999999999999
sso_role_name = Dev

[profile bad-arn]
role_arn = not-an-arn
source_profile = keys
`)
	writeLoginToken(t, cache, loginSession, loginTokenJSON("2026-11-18T12:30:00Z"))

	for name, want := range map[string]string{
		"sso":           "111111111111",
		"sso-old":       "333333333333",
		"role-sso":      "555555555555",
		"role-sso-acct": "555555555555",
		"role-login":    "666666666666",
		"login":         "222222222222",
		"mfa-role":      "444444444444",
		"cycle-a":       "111111111111",
		"keys":          "",
		"process":       "",
		"bad-arn":       "",
	} {
		assert.Equal(t, want, ClassifyProfile(name, p9Now).Account, name)
	}
}

func TestDescribeProfiles(t *testing.T) {
	p9Env(t, "[profile b]\nsso_session = corp\n\n[profile a]\naws_access_key_id = AKIA\naws_secret_access_key = s\n\n[profile broken]\nsso_session = nope\n\n[sso-session corp]\nsso_start_url = https://corp.example.com/start\nsso_region = us-east-1\n")

	infos := DescribeProfiles(p9Now)

	require.Len(t, infos, 3)
	assert.Equal(t, []string{"a", "b", "broken"}, []string{infos[0].Name, infos[1].Name, infos[2].Name})
	assert.Equal(t, []CredKind{KindKeys, KindSSO, KindInvalid}, []CredKind{infos[0].Kind, infos[1].Kind, infos[2].Kind})
	assert.Error(t, infos[2].Err)
}

// A role profile with mfa_serial fails to build, and ErrorText says why.
func TestErrorTextForAnMFAProfile(t *testing.T) {
	p9Env(t, p9Profiles)

	_, err := NewClient("mfa-role", "", "")
	require.Error(t, err)

	assert.Equal(t, MFANotSupported, ErrorText(err))
}
