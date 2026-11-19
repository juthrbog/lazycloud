package aws

import (
	"crypto/sha1" //nolint:gosec // the SDK names the SSO cache file by a SHA-1 hash; the test checks that name
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/logincreds"
	"github.com/aws/aws-sdk-go-v2/credentials/ssocreds"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin the behavior that docs/design/phase9-profiles.md changes
// (TestP9Hazard_*) and the facts that the design depends on. The PR that
// fixes a hazard changes its test.

// p9Env isolates the AWS files, the home folder and the login cache, and
// writes config as the AWS config file. An empty HOME is not enough: the
// SDK then reads the home folder of the user from the system.
func p9Env(t *testing.T, config string) (home, loginCache string) {
	t.Helper()
	cfgFile, _ := isolatedAWSFiles(t)
	writeFile(t, cfgFile, config)
	home = t.TempDir()
	loginCache = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AWS_LOGIN_CACHE_DIRECTORY", loginCache)
	return home, loginCache
}

const p9SSOConfig = `[profile sso-dev]
sso_session = corp
sso_account_id = 111111111111
sso_role_name = Dev
region = us-east-1

[sso-session corp]
sso_start_url = https://corp.example.com/start
sso_region = us-east-1
`

const p9LoginConfig = `[profile web]
login_session = arn:aws:iam::222222222222:user/me
region = us-east-1
`

// H4: a client for a profile without an SSO token builds without an error.
// The error comes only when a call needs the credentials. This stays true.
// Since PR 5, the app offers a sign-in before such a switch
// (TestP9_H4_PickOfASignedOutProfileOffersASignIn).
func TestP9Fact_H4_SignedOutSSOProfileBuilds(t *testing.T) {
	p9Env(t, p9SSOConfig)

	c, err := NewClient("sso-dev", "", "")
	require.NoError(t, err, "the session builds")

	_, err = c.Config.Credentials.Retrieve(t.Context())
	require.Error(t, err, "the first call fails")
	assert.Contains(t, ErrorText(err), "aws sso login")
}

// H5, fixed in PR 1: a missing login token is "login needed", and the
// hint names aws login. The error of a real call names the profile.
func TestP9_H5_LoginTokenErrorNamesAWSLogin(t *testing.T) {
	p9Env(t, p9LoginConfig)

	c, err := NewClient("web", "", "")
	require.NoError(t, err)
	_, err = c.Config.Credentials.Retrieve(t.Context())
	require.Error(t, err)
	assert.Equal(t, "the aws login session has expired or is not signed in. Sign in with: aws login (or :login)", ErrorText(err))

	_, err = NewS3Service(c).ListBuckets(t.Context())
	require.Error(t, err)
	assert.Equal(t, "the aws login session has expired or is not signed in. Sign in with: aws login --profile web (or :login)", ErrorText(err))
}

// H5, fixed in PR 1: an expired-token error from AWS on a login profile
// names aws login.
func TestP9_H5_ExpiredLoginProfileGetsTheLoginHint(t *testing.T) {
	p9Env(t, p9LoginConfig)
	err := &ProfileError{Profile: "web", Err: &smithy.GenericAPIError{Code: "ExpiredToken", Message: "expired"}}

	assert.Equal(t, "the AWS credentials have expired (ExpiredToken). Get new ones, for example with: aws login --profile web (or :login)", ErrorText(err))
}

// H6, fixed in PR 1: with an endpoint, a profile that has only
// login_session has credentials, so it does not get the dummy keys.
func TestP9_H6_LoginProfileKeepsItsCredentials(t *testing.T) {
	p9Env(t, p9LoginConfig)

	assert.True(t, hasCredentials(t.Context(), "web"))

	c, err := NewClient("web", "", "http://127.0.0.1:1")
	require.NoError(t, err)
	_, err = c.Config.Credentials.Retrieve(t.Context())
	require.Error(t, err, "the login provider runs, and it has no token")
	assert.Contains(t, err.Error(), "please reauthenticate")
}

// The design reads the token caches at the paths of the SDK helpers. This
// test pins the paths that the helpers give.
func TestP9Fact_TokenCachePaths(t *testing.T) {
	home, loginCache := p9Env(t, "")

	sum1 := sha1.Sum([]byte("corp")) //nolint:gosec // see the import
	got, err := ssocreds.StandardCachedTokenFilepath("corp")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".aws", "sso", "cache", hex.EncodeToString(sum1[:])+".json"), got)

	sum256 := sha256.Sum256([]byte("arn:aws:iam::222222222222:user/me"))
	got, err = logincreds.StandardCachedTokenFilepath(" arn:aws:iam::222222222222:user/me ", loginCache)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(loginCache, hex.EncodeToString(sum256[:])+".json"), got, "the session is trimmed")

	got, err = logincreds.StandardCachedTokenFilepath("arn:aws:iam::222222222222:user/me", "")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".aws", "login", "cache", hex.EncodeToString(sum256[:])+".json"), got)
}

// A role profile with mfa_serial fails while the config loads, because the
// app sets no token provider.
func TestP9Fact_MFARoleFailsAtBuild(t *testing.T) {
	p9Env(t, `[profile base]
aws_access_key_id = AKIAEXAMPLE
aws_secret_access_key = secret

[profile mfa-role]
role_arn = arn:aws:iam::444444444444:role/Admin
source_profile = base
mfa_serial = arn:aws:iam::444444444444:mfa/me
region = us-east-1
`)

	_, err := NewClient("mfa-role", "", "")
	require.ErrorAs(t, err, new(config.AssumeRoleTokenProviderNotSetError))
}
