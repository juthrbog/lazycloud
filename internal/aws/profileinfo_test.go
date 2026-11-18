package aws

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// p9Profiles has one profile for each case of ClassifyProfile.
const p9Profiles = `[profile keys]
aws_access_key_id = AKIAEXAMPLE
aws_secret_access_key = secret

[profile sso]
sso_session = corp
sso_account_id = 111111111111
sso_role_name = Dev

[profile sso-old]
sso_start_url = https://old.example.com/start
sso_region = us-east-1
sso_account_id = 333333333333
sso_role_name = Dev

[profile sso-url-only]
sso_start_url = https://old.example.com/start

[profile src-only]
credential_source = Environment

[profile role-sso]
role_arn = arn:aws:iam::555555555555:role/Admin
source_profile = sso

[profile login]
login_session = arn:aws:iam::222222222222:user/me

[profile role-login]
role_arn = arn:aws:iam::666666666666:role/Admin
login_session = arn:aws:iam::222222222222:user/me

[profile role-over-login]
role_arn = arn:aws:iam::666666666666:role/Admin
source_profile = login

[profile source]
role_arn = arn:aws:iam::777777777777:role/App
credential_source = Ec2InstanceMetadata

[profile web-id]
role_arn = arn:aws:iam::888888888888:role/Ci
web_identity_token_file = /tmp/token

[profile process]
credential_process = /bin/false

[profile mfa-role]
role_arn = arn:aws:iam::444444444444:role/Admin
source_profile = keys
mfa_serial = arn:aws:iam::444444444444:mfa/me

[profile no-session]
sso_session = missing

[profile no-source]
role_arn = arn:aws:iam::999999999999:role/X
source_profile = nope

[profile cycle-a]
role_arn = arn:aws:iam::111111111111:role/A
source_profile = cycle-b
aws_access_key_id = AKIACYCLE
aws_secret_access_key = secret

[profile cycle-b]
role_arn = arn:aws:iam::111111111111:role/B
source_profile = cycle-a

[profile empty]
region = eu-west-1

[sso-session corp]
sso_start_url = https://corp.example.com/start
sso_region = us-east-1
`

func TestClassifyProfile(t *testing.T) {
	p9Env(t, p9Profiles)

	for _, tc := range []struct {
		name string
		kind CredKind
		role bool
		mfa  bool
		root string
	}{
		{name: "keys", kind: KindKeys, root: "keys"},
		// sso_account_id and sso_role_name make the legacy test of the SDK
		// true, but the profile uses its sso_session.
		{name: "sso", kind: KindSSO, root: "sso"},
		{name: "sso-old", kind: KindSSOLegacy, root: "sso-old"},
		{name: "sso-url-only", kind: KindSSOLegacy, root: "sso-url-only"},
		{name: "role-sso", kind: KindSSO, role: true, root: "sso"},
		{name: "login", kind: KindLogin, root: "login"},
		{name: "role-login", kind: KindLogin, role: true, root: "role-login"},
		{name: "source", kind: KindCredSource, role: true, root: "source"},
		{name: "web-id", kind: KindWebIdentity, role: true, root: "web-id"},
		{name: "process", kind: KindProcess, root: "process"},
		{name: "mfa-role", kind: KindKeys, role: true, mfa: true, root: "keys"},
		// The SDK loads a cycle: the second copy of cycle-a has no source.
		{name: "cycle-a", kind: KindKeys, role: true, root: "cycle-a"},
		{name: "empty", kind: KindDefault, root: "empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := ClassifyProfile(tc.name)
			require.NoError(t, info.Err)
			assert.Equal(t, tc.name, info.Name)
			assert.Equal(t, tc.kind, info.Kind, info.Kind.String())
			assert.Equal(t, tc.role, info.Role, "role")
			assert.Equal(t, tc.mfa, info.MFA, "mfa")
			assert.Equal(t, tc.root, info.Root)
		})
	}
}

func TestClassifyProfileThatDoesNotLoad(t *testing.T) {
	p9Env(t, p9Profiles)

	for name, check := range map[string]func(t *testing.T, err error){
		// The SDK does not count login_session as credentials of a source
		// profile.
		"role-over-login": func(t *testing.T, err error) {
			assert.ErrorAs(t, err, new(config.SharedConfigAssumeRoleError))
		},
		"no-source": func(t *testing.T, err error) {
			assert.ErrorAs(t, err, new(config.SharedConfigAssumeRoleError))
		},
		// credential_source needs role_arn.
		"src-only":   func(t *testing.T, err error) { assert.ErrorContains(t, err, "requires role_arn") },
		"no-session": func(t *testing.T, err error) { assert.ErrorContains(t, err, "missing") },
		"nope": func(t *testing.T, err error) {
			assert.ErrorAs(t, err, new(config.SharedConfigProfileNotExistError))
		},
	} {
		t.Run(name, func(t *testing.T) {
			info := ClassifyProfile(name)
			assert.Equal(t, KindInvalid, info.Kind)
			assert.Equal(t, name, info.Root)
			check(t, info.Err)
		})
	}
}

// An empty name means AWS_PROFILE, then default.
func TestClassifyProfileWithoutAName(t *testing.T) {
	p9Env(t, p9Profiles+"\n[default]\nsso_session = corp\n")

	info := ClassifyProfile("")
	assert.Equal(t, "default", info.Name)
	assert.Equal(t, KindSSO, info.Kind)

	t.Setenv("AWS_PROFILE", "login")
	info = ClassifyProfile("")
	assert.Equal(t, "login", info.Name)
	assert.Equal(t, KindLogin, info.Kind)
}

func TestSignInCommand(t *testing.T) {
	for _, tc := range []struct {
		info ProfileInfo
		want string
	}{
		{ProfileInfo{Kind: KindSSO, Root: "dev"}, "aws sso login --profile dev"},
		{ProfileInfo{Kind: KindSSOLegacy, Root: "old"}, "aws sso login --profile old"},
		{ProfileInfo{Kind: KindLogin, Root: "web"}, "aws login --profile web"},
		{ProfileInfo{Kind: KindSSO, Root: "default"}, "aws sso login"},
		{ProfileInfo{Kind: KindLogin, Root: ""}, "aws login"},
		{ProfileInfo{Kind: KindKeys, Root: "ci"}, ""},
		{ProfileInfo{Kind: KindProcess, Root: "p"}, ""},
		{ProfileInfo{Kind: KindInvalid, Root: "x"}, ""},
	} {
		assert.Equal(t, tc.want, tc.info.SignInCommand(), tc.info.Kind.String())
	}
}

func TestCredKindString(t *testing.T) {
	names := map[CredKind]string{
		KindDefault: "default", KindKeys: "keys", KindCredSource: "source", KindWebIdentity: "web-id",
		KindSSO: "sso", KindSSOLegacy: "sso-old", KindLogin: "login", KindProcess: "process", KindInvalid: "invalid",
	}
	for k, want := range names {
		assert.Equal(t, want, k.String())
	}
}

// The hints name the profile that signs in, or ask for new credentials of
// a profile that does not sign in.
func TestLoginHintsByProfile(t *testing.T) {
	p9Env(t, p9Profiles)
	expired := func(profile string) error {
		return &ProfileError{Profile: profile, Err: &smithy.GenericAPIError{Code: "ExpiredToken", Message: "expired"}}
	}
	const prefix = "the AWS credentials have expired (ExpiredToken). "

	assert.Equal(t, prefix+"Get new ones, for example with: aws sso login --profile sso", ErrorText(expired("role-sso")),
		"a role names the profile at the end of its chain")
	assert.Equal(t, prefix+"Get new ones, for example with: aws login --profile role-login", ErrorText(expired("role-login")))
	assert.Equal(t, prefix+"Get new credentials for profile keys", ErrorText(expired("keys")))
	assert.Equal(t, prefix+"Get new ones, for example with: aws sso login --profile nope", ErrorText(expired("nope")),
		"a profile that does not load keeps the old hint")
	assert.Equal(t, prefix+"Get new ones, for example with: aws sso login", ErrorText(expired("")),
		"no default profile in the files: the old hint, with no --profile")
	assert.Equal(t, prefix+"Get new ones, for example with: aws sso login",
		ErrorText(&smithy.GenericAPIError{Code: "ExpiredToken", Message: "expired"}), "no profile in the error")
}

// The plain errors of the login provider are "login needed".
func TestLoginNeededForLoginErrors(t *testing.T) {
	for _, text := range []string{
		"failed to refresh cached credentials, load login token: token file not found, please reauthenticate",
		"create oauth2 token: login session has expired, please reauthenticate",
		"create oauth2 token: login session password has changed, please reauthenticate",
		"validate login token: missing refreshToken",
	} {
		kind, ok := loginNeeded(errorString(text))
		assert.True(t, ok, text)
		assert.Equal(t, loginAWS, kind, text)
	}
	_, ok := loginNeeded(errorString("create oauth2 token: insufficient permissions, you may be missing permissions for the CreateOAuth2Token action"))
	assert.False(t, ok, "a denied refresh is not a sign-in problem")
}

type errorString string

func (e errorString) Error() string { return string(e) }
