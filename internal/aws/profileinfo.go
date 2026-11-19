package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/config"
)

// CredKind is the kind of credential at the root of a profile: the
// profile that signs in or holds the keys.
type CredKind int

// The kinds follow the order in which the SDK picks the credential of a
// profile (resolveCredsFromProfile in aws-sdk-go-v2/config).
const (
	KindDefault     CredKind = iota // no credential setting: the SDK asks the environment
	KindKeys                        // static keys
	KindCredSource                  // credential_source, such as Ec2InstanceMetadata
	KindWebIdentity                 // web_identity_token_file
	KindSSO                         // sso_session
	KindSSOLegacy                   // sso_start_url on the profile
	KindLogin                       // login_session, from aws login
	KindProcess                     // credential_process
	KindInvalid                     // the profile does not load
)

// String returns a short name for the picker and the logs.
func (k CredKind) String() string {
	switch k {
	case KindKeys:
		return "keys"
	case KindCredSource:
		return "source"
	case KindWebIdentity:
		return "web-id"
	case KindSSO:
		return "sso"
	case KindSSOLegacy:
		return "sso-old"
	case KindLogin:
		return "login"
	case KindProcess:
		return "process"
	case KindInvalid:
		return "invalid"
	case KindDefault:
	}
	return "default"
}

// ProfileInfo describes how a profile gets its credentials. It comes from
// the shared AWS files only, with no network call.
type ProfileInfo struct {
	Name string
	Kind CredKind // the kind of the root credential
	Role bool     // role_arn on this profile or on a profile in its chain
	MFA  bool     // mfa_serial in the chain: the app cannot answer the prompt
	Root string   // the profile that signs in, at the end of the source_profile chain
	Err  error    // the load error when Kind is KindInvalid
}

// SignInCommand returns the AWS CLI command that signs in to the profile,
// or "" when the profile does not sign in (keys, a process, and others).
func (p ProfileInfo) SignInCommand() string {
	var cmd string
	switch p.Kind {
	case KindSSO, KindSSOLegacy:
		cmd = "aws sso login"
	case KindLogin:
		cmd = "aws login"
	default:
		return ""
	}
	if p.Root != "" && p.Root != "default" {
		cmd += " --profile " + p.Root
	}
	return cmd
}

// ClassifyProfile reads profile from the shared AWS files, with the file
// paths from AWS_CONFIG_FILE and AWS_SHARED_CREDENTIALS_FILE, and returns
// the kind of its root credential. An empty name means AWS_PROFILE, then
// "default".
func ClassifyProfile(name string) ProfileInfo {
	sc, resolved, err := loadSharedProfile(context.Background(), name)
	info := ProfileInfo{Name: resolved, Root: resolved}
	if err != nil {
		info.Kind, info.Err = KindInvalid, err
		return info
	}
	// The SDK puts the source profile in Source and clears the credential
	// settings of the profile that names it. The root is the end of the
	// chain. A cycle ends at a second copy of a profile with no Source.
	root := &sc
	for {
		info.Role = info.Role || root.RoleARN != ""
		info.MFA = info.MFA || root.MFASerial != ""
		if root.Source == nil {
			break
		}
		root = root.Source
	}
	if root.Profile != "" {
		info.Root = root.Profile
	}
	info.Kind = rootKind(root)
	return info
}

// rootKind returns the kind of a profile without a Source, in the order
// of the SDK.
func rootKind(sc *config.SharedConfig) CredKind {
	switch {
	case sc.Credentials.HasKeys():
		return KindKeys
	case sc.CredentialSource != "":
		return KindCredSource
	case sc.WebIdentityTokenFile != "":
		return KindWebIdentity
	// The SDK takes the SSO branch for an sso_session and for the legacy
	// keys. The legacy test is also true for most sso_session profiles,
	// because they have sso_account_id and sso_role_name too. So the
	// session comes first, as in resolveSSOCredentials.
	case sc.SSOSession != nil || sc.SSOSessionName != "":
		return KindSSO
	case sc.SSOStartURL != "" || sc.SSORegion != "" || sc.SSOAccountID != "" || sc.SSORoleName != "":
		return KindSSOLegacy
	case sc.LoginSession != "":
		return KindLogin
	case sc.CredentialProcess != "":
		return KindProcess
	}
	return KindDefault
}

// loadSharedProfile loads profile from the same files as the SDK. It
// returns the profile name that it used.
func loadSharedProfile(ctx context.Context, profile string) (config.SharedConfig, string, error) {
	env, err := config.NewEnvConfig()
	if err != nil {
		return config.SharedConfig{}, profile, err
	}
	if profile == "" {
		profile = env.SharedConfigProfile
	}
	if profile == "" {
		profile = "default"
	}
	sc, err := config.LoadSharedConfigProfile(ctx, profile, func(o *config.LoadSharedConfigOptions) {
		if env.SharedConfigFile != "" {
			o.ConfigFiles = []string{env.SharedConfigFile}
		}
		if env.SharedCredentialsFile != "" {
			o.CredentialsFiles = []string{env.SharedCredentialsFile}
		}
	})
	return sc, profile, err
}
