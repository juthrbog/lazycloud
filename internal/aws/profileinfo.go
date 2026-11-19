package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/logincreds"
	"github.com/aws/aws-sdk-go-v2/credentials/ssocreds"
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

// TokenState is the state of the cached sign-in token of a profile, read
// from the token cache of the SDK. It is a hint: the SDK checks the expiry
// with no grace time, and a refresh can still fail on the network.
type TokenState int

const (
	TokenNA      TokenState = iota // the profile has no sign-in token
	TokenValid                     // the token has not expired
	TokenRefresh                   // the token has expired, and the SDK can refresh it
	TokenExpired                   // the token has expired, and the user must sign in
	TokenMissing                   // no token: the user did not sign in
	TokenUnknown                   // the cache file does not parse, or the SDK does not accept it
)

// ProfileInfo describes how a profile gets its credentials. It comes from
// the shared AWS files and the token caches only, with no network call.
type ProfileInfo struct {
	Name     string
	Kind     CredKind // the kind of the root credential
	Role     bool     // role_arn on this profile or on a profile in its chain
	MFA      bool     // mfa_serial in the chain: the app cannot answer the prompt
	Root     string   // the profile that signs in, at the end of the source_profile chain
	TokenKey string   // the sso-session name, the start URL, or the login session: profiles with one key share a sign-in
	Account  string   // the account from the config, "" when the config does not name it
	Token    TokenState
	Expires  time.Time // the expiry of the token, zero when unknown
	Err      error     // the load error when Kind is KindInvalid
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
// the kind of its root credential, the account from the config, and the
// state of its sign-in token at now. An empty name means AWS_PROFILE, then
// "default".
func ClassifyProfile(name string, now time.Time) ProfileInfo {
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
	info.Account = roleAccount(&sc)
	switch info.Kind {
	case KindSSO:
		info.TokenKey = root.SSOSessionName
		info.Token, info.Expires = ssoTokenState(info.TokenKey, true, now)
	case KindSSOLegacy:
		info.TokenKey = root.SSOStartURL
		info.Token, info.Expires = ssoTokenState(info.TokenKey, false, now)
	case KindLogin:
		info.TokenKey = root.LoginSession
		var account string
		info.Token, info.Expires, account = loginTokenState(info.TokenKey, now)
		if info.Account == "" {
			info.Account = account
		}
	default:
	}
	if info.Account == "" && (info.Kind == KindSSO || info.Kind == KindSSOLegacy) {
		info.Account = root.SSOAccountID
	}
	return info
}

// DescribeProfiles returns the info of each profile in the shared AWS
// files, in the order of ListProfiles. A profile that does not load is in
// the list with KindInvalid.
func DescribeProfiles(now time.Time) []ProfileInfo {
	names := ListProfiles()
	out := make([]ProfileInfo, 0, len(names))
	for _, n := range names {
		out = append(out, ClassifyProfile(n, now))
	}
	return out
}

// roleAccount returns the account of the role of sc, the role that the
// session ends in, or "" without a role. The SDK refuses source_profile
// without role_arn, so a profile with a chain always has its own role.
func roleAccount(sc *config.SharedConfig) string {
	if a, err := arn.Parse(sc.RoleARN); err == nil {
		return a.AccountID
	}
	return ""
}

// ssoToken holds the fields of the SSO token cache that the app reads.
type ssoToken struct {
	AccessToken  string `json:"accessToken"`
	ExpiresAt    string `json:"expiresAt"`
	RefreshToken string `json:"refreshToken"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

// ssoTokenState reads the cached SSO token of key: an sso-session name, or
// the start URL of a legacy profile. Only an sso-session token can be
// refreshed.
func ssoTokenState(key string, session bool, now time.Time) (TokenState, time.Time) {
	path, err := ssocreds.StandardCachedTokenFilepath(key)
	if err != nil {
		return TokenUnknown, time.Time{}
	}
	var t ssoToken
	if state, ok := readToken(path, &t); !ok {
		return state, time.Time{}
	}
	expires, err := time.Parse(time.RFC3339, t.ExpiresAt)
	// The SDK does not accept a token without these fields.
	if t.AccessToken == "" || err != nil || expires.IsZero() {
		return TokenUnknown, time.Time{}
	}
	switch {
	case now.Before(expires):
		return TokenValid, expires
	case session && t.RefreshToken != "" && t.ClientID != "" && t.ClientSecret != "":
		return TokenRefresh, expires
	}
	return TokenExpired, expires
}

// loginToken holds the fields of the login token cache that the app reads.
type loginToken struct {
	AccessToken *struct {
		AccessKeyID     string    `json:"accessKeyId"`
		SecretAccessKey string    `json:"secretAccessKey"`
		SessionToken    string    `json:"sessionToken"`
		AccountID       string    `json:"accountId"`
		ExpiresAt       time.Time `json:"expiresAt"`
	} `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ClientID     string `json:"clientId"`
	DPOPKey      string `json:"dpopKey"`
}

// loginTokenState reads the cached token of an aws login session from
// AWS_LOGIN_CACHE_DIRECTORY or ~/.aws/login/cache. The SDK refreshes an
// expired token with its refresh token. It also returns the account of the
// token.
func loginTokenState(session string, now time.Time) (TokenState, time.Time, string) {
	path, err := logincreds.StandardCachedTokenFilepath(session, os.Getenv("AWS_LOGIN_CACHE_DIRECTORY"))
	if err != nil {
		return TokenUnknown, time.Time{}, ""
	}
	var t loginToken
	if state, ok := readToken(path, &t); !ok {
		return state, time.Time{}, ""
	}
	// The SDK does not accept a token without one of these fields.
	a := t.AccessToken
	if a == nil || a.AccessKeyID == "" || a.SecretAccessKey == "" || a.SessionToken == "" ||
		a.AccountID == "" || a.ExpiresAt.IsZero() || t.ClientID == "" || t.RefreshToken == "" || t.DPOPKey == "" {
		return TokenUnknown, time.Time{}, ""
	}
	if now.Before(a.ExpiresAt) {
		return TokenValid, a.ExpiresAt, a.AccountID
	}
	return TokenRefresh, a.ExpiresAt, a.AccountID
}

// readToken reads the JSON file at path into v. It returns false with the
// state for a file that is missing or does not parse.
func readToken(path string, v any) (TokenState, bool) {
	data, err := os.ReadFile(path) //nolint:gosec // the path comes from the SDK helper for the token cache
	if errors.Is(err, fs.ErrNotExist) {
		return TokenMissing, false
	}
	if err != nil || json.Unmarshal(data, v) != nil {
		return TokenUnknown, false
	}
	return TokenNA, true
}

// String names the state for the picker, with the time left for a valid
// token, for example "valid 3h12m".
func (p ProfileInfo) TokenText(now time.Time) string {
	switch p.Token {
	case TokenValid:
		left := p.Expires.Sub(now).Round(time.Minute)
		h, m := int(left.Hours()), int(left.Minutes())%60
		if h > 0 {
			return fmt.Sprintf("valid %dh%02dm", h, m)
		}
		return fmt.Sprintf("valid %dm", m)
	case TokenRefresh:
		return "refresh"
	case TokenExpired:
		return "expired"
	case TokenMissing:
		return "not signed in"
	case TokenUnknown:
		return "unknown"
	case TokenNA:
	}
	return ""
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
