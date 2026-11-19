# P9.1: profiles that know their credentials, sign-in from the app, and ReadOnly locks

Status: Approved on 2026-11-18 with all proposed answers. The facts below were checked at `1377e9c`.

## Summary

Market research items 1 and 2 (`docs/reviews/2026-10-11/market-research.md:86-87`) ask for two things. The first is a profile picker that knows the credential type of each profile, finds expired tokens, and runs `aws sso login` or `aws login`. The second is an account banner and ReadOnly rules for each profile.

The main risk of this feature is a change to resources in the wrong account. Today four things make that possible:

- ReadWrite stays on when the user switches the profile. A user in ReadWrite on a test profile who picks the production profile is still in ReadWrite.
- The app never shows the account or the role. It shows only the profile name, and the name can be wrong or out of date.
- On a narrow or short terminal, the mode badge is not on screen at all.
- An expired SSO or `aws login` session does not stop a switch. The switch succeeds, and the error comes later in each view. For an `aws login` profile, the error has no sign-in hint, or the wrong hint `aws sso login`.

This design makes six changes:

1. The error hints name the right sign-in command for each profile.
2. The mode belongs to the session. Each session (profile and region) has its own mode value. A profile switch or a new sign-in starts in ReadOnly. A region switch keeps the mode. A background operation keeps the mode of its own session.
3. Rules in the config file can lock a profile to ReadOnly. A rule matches profile names (globs) or account IDs. A locked session cannot go to ReadWrite with `W`, with `:mode`, or with `--read-write`.
4. The profile picker shows the credential type, the account from the config, the token state and a lock mark for each profile. It reads only local files.
5. The app can run `aws sso login` or `aws login` in the terminal, like SSM connect today, and then build the session again.
6. The header shows the account and the role from STS `GetCallerIdentity`, and the mode badge stays visible on all but very short terminals.

The app still starts in ReadOnly. No rule and no config key turns ReadWrite on. The only module change is that `service/sts` moves from indirect to direct. There is no IAM module and no account alias in this phase (decision D8).

## Facts that the design depends on

These facts were checked at `1377e9c`. SDK paths are under `~/go/pkg/mod/github.com/aws/aws-sdk-go-v2/` (config v1.33.13, credentials v1.20.13, core v1.47.5, sts v1.51.6). Bubble Tea is `~/go/pkg/mod/charm.land/bubbletea/v2@v2.1.0/`.

### Profiles and credentials

- `ListProfiles` reads only section headers and returns sorted names (`internal/aws/profiles.go:13-32`, prefix check at `:80-89`). It uses `AWS_CONFIG_FILE` and `AWS_SHARED_CREDENTIALS_FILE` (`profiles.go:34-54`). The SDK exports no function that lists profile names, so this scan stays.
- `config.LoadSharedConfigProfile` reads one profile with no network call. It links `source_profile` and the `[sso-session]` section (`config@v1.33.13/shared_config.go:660-711`). Each call parses all files again (`shared_config.go:675-704`). `hasCredentials` already calls it with the env file overrides (`internal/aws/client.go:130-160`).
- `SharedConfig` has the fields a classifier needs: `Credentials`, `CredentialSource`, `CredentialProcess`, `WebIdentityTokenFile`, `SSOSessionName`, `SSOSession`, `SSOStartURL`, `SSOAccountID`, `RoleARN`, `MFASerial`, `SourceProfileName`, `Source` and `LoginSession` (`shared_config.go:203-240`, `:373-374`).
- For a `source_profile` profile, the SDK clears the profile's own credential fields and puts the source in `sc.Source`, recursively (`shared_config.go:1004-1031`). It does not clear `SSOSessionName` or `LoginSession` (`shared_config.go:1511-1520`).
- A missing `[sso-session]` section or a missing `source_profile` makes `LoadSharedConfigProfile` fail (`shared_config.go:1010-1047`).
- A source profile must have credentials by `SharedConfig.hasCredentials`, or the load fails with `SharedConfigAssumeRoleError` (`shared_config.go:1022-1027`). That check does not know `login_session` (`shared_config.go:1476-1489`). So `role_arn` with `source_profile` that names a profile with only `login_session` does not load. A role over a login works only when `role_arn` and `login_session` are on the same profile. Then the login branch runs (`resolve_credentials.go:184-187`) and the role is assumed on top (`:202-204`).
- The SDK does not fail on a `source_profile` cycle. When it sees a profile for the second time, it clears the assume-role options of that instance and stops (`shared_config.go:976-981`). So A to B to A loads, and the second A is the credential root.
- The SDK picks the base credential in this order: `Source`, static keys, `credential_source`, web identity, SSO, `login_session`, `credential_process`, then container and IMDS. A `role_arn` then assumes a role on top (`config@v1.33.13/resolve_credentials.go:139-206`).
- The SSO branch is taken when `hasSSOTokenProviderConfiguration()` or `hasLegacySSOConfiguration()` is true (`shared_config.go:1491-1501`). `hasLegacySSOConfiguration` is true when any of `sso_region`, `sso_account_id`, `sso_start_url` or `sso_role_name` is set, so it is also true for most sso-session profiles. The SDK picks the token path by `sharedConfig.SSOSession != nil` (`resolve_credentials.go:229-251`). A classifier must decide sso-session by `SSOSession != nil` first.
- `ssocreds.StandardCachedTokenFilepath(key)` returns `~/.aws/sso/cache/<sha1(key)>.json` (`credentials@v1.20.13/ssocreds/sso_cached_token.go:19-40`). The key is the sso-session name (`resolve_credentials.go:239`) or the start URL for legacy SSO (`ssocreds/sso_credentials_provider.go:101-103`). The JSON has `accessToken`, `expiresAt` (RFC3339), `refreshToken`, `clientId` and `clientSecret` (`sso_cached_token.go:42-49`). The token type is not exported.
- An expired sso-session token is refreshed over the network when `refreshToken`, `clientId` and `clientSecret` are all present (`ssocreds/sso_token_provider.go:95-128`). A legacy token is never refreshed (`sso_credentials_provider.go:110-116`).
- `logincreds.StandardCachedTokenFilepath(session, dir)` returns `<dir>/<sha256(TrimSpace(session))>.json`. The default dir is `~/.aws/login/cache` (`credentials@v1.20.13/logincreds/token.go:25-41`). Its doc comment says `~/.aws/sso/cache`, but the code uses `.aws/login/cache` (`token.go:22-33`). The config package passes `AWS_LOGIN_CACHE_DIRECTORY` as `dir` (`resolve_credentials.go:633-636`).
- The login token holds `accessToken.expiresAt` and `accessToken.accountId`, and it refreshes itself through signin (`logincreds/token.go:44-90`, `logincreds/provider.go:72-114`). A missing file gives "token file not found, please reauthenticate" (`provider.go:125-130`). Expiry errors are at `provider.go:175-185`.
- Role profiles with `mfa_serial` fail today while the config loads, because the app sets no `AssumeRoleTokenProvider`. `LoadDefaultConfig` returns `AssumeRoleTokenProviderNotSetError` (`resolve_credentials.go:569-579`), so `NewClient` fails (`client.go:106-109`). A switch to such a profile keeps the old session and shows a toast (`app.go:1144-1149`). At start, the app has no session (`app.go:105-110`).
- `credential_process` children inherit `os.Stdin` and `os.Stderr` (`credentials@v1.20.13/processcreds/provider.go:109-110`). A prompt from such a process draws over the TUI.

### Session and errors

- `NewClient` calls `config.LoadDefaultConfig` and never fetches credentials (`internal/aws/client.go:81-123`). A switch to a profile with an expired token succeeds. Errors in the configuration, such as `mfa_serial` without a token provider, still fail at build time.
- `NewClient` adds `profileMiddleware`, which wraps each call error in `*ProfileError{Profile}` (`client.go:113-114`, `internal/aws/errtext.go:16-41`).
- `hasCredentials` runs only when an endpoint is set. It does not check `LoginSession` or `CredentialSource` (`client.go:100-104`, `:158-159`). A profile with only `login_session` gets the dummy `test` keys.
- `ssoLoginNeeded` matches SSO errors only (`errtext.go:148-169`). `loginCommand` always returns `aws sso login [--profile X]` (`errtext.go:171-178`), also from the `expiredCodes` branch (`errtext.go:120-121`).
- `New(cfg config.Config)` builds the first session with `aws.NewServices(..., ui.IsReadOnly)` and starts without a session on error (`internal/app/app.go:81`, `:103-110`).
- `requestSessionSwitch` asks first when operations run, and says that they keep running in the background with the old session (`app.go:1063-1080`). `askQuick` is at `internal/app/prompts.go:59`.
- `switchSession` calls `NewServices`. On error nothing changes. On success it sets profile and region, `resetNav`, a toast, and reopens the top view. It never touches the mode (`app.go:1143-1165`).
- `showProfilePicker` lists plain names with ` ●` on the current one (`app.go:1038-1055`). `PickerOption` has only `Label` and `Value` (`internal/ui/picker.go:14-17`). The mode picker already uses padded columns in `Label` (`app.go:1184-1194`).
- Session tests that must stay green: `internal/app/session_test.go:60, 77, 93, 108`.

### ReadOnly

- ReadOnly is one global atomic, true in `init()`. `IsReadOnly` and `SetReadOnly` are the only API (`internal/ui/mode.go:1-17`).
- 17 call sites outside tests read `ui.IsReadOnly()`. Two more places pass `ui.IsReadOnly` as the guard function (`app.go:105`, `:1144`). These two are the ones this design changes. 28 files call `ui.SetReadOnly`, and 25 of them are tests.
- `--read-write` calls `ui.SetReadOnly(false)` in `main` before `app.New` (`main.go:138-140`, `:157`).
- `applyMode` calls `ui.SetReadOnly` with no check (`app.go:1196-1210`).
- `NewServices` passes one `readOnly func() bool` to `GuardS3`, `GuardEC2` and `GuardSQS` (`internal/aws/services.go:17-28`). The guards call it when the call runs (`internal/aws/readonly.go:9-23`).
- Three checks enforce ReadOnly: the key guard, the confirm reply (`refuseReadOnly`), and the service guard (`docs/ARCHITECTURE.md:181-196`). The key guard toast is "ReadOnly mode — press W to switch" (`app.go:682-688`).

### UI, config and exec

- `HeaderData` has `Profile`, `Region`, `Mode`, `Breadcrumbs`, `CrossNavBackLabel` and `Width` (`internal/ui/header.go:10-17`). Below 60 columns only the title and breadcrumbs show. From 60 to 79 the region is dropped (`header.go:75-94`).
- The app hides the whole header when the content is shorter than `MinTableRows+2` (`app.go:1334-1339`). `chromeHeight` assumes a 2-line header (`app.go:912-935`).
- The status bar shows the error or the key hints, dropped from the right until they fit (`internal/ui/statusbar.go:29-65`). With an error, it draws only the error text (`statusbar.go:32-41`). When the content is still too short after the header and the operations line are gone, the app also hides the status bar (`app.go:1345-1349`).
- `RenderHeader` truncates only the breadcrumbs, not the badges (`header.go:81-92`). The region does not go through `Sanitize` (`header.go:36`).
- The picker uses only the height in `SetSize` (`picker.go:89-93`) and draws each row at the full width of its label (`picker.go:279-305`). `icons.go` has no lock icon.
- Width tiers: Narrow below 80, Medium 80 to 119, Wide 120 and up (`internal/ui/layout.go:10-25`).
- Config has `[aws]`, `[display]` and `[log]` (`internal/config/config.go:16-40`). `Load` decodes leniently with `toml.Unmarshal` (`config.go:89`). A second, strict decode lists the unknown keys as warnings (`config.go:95-109`). `WriteDefault` holds a comment template that says the app always starts ReadOnly (`config.go:111-163`).
- `SSMSession` is a `tea.ExecCommand` that prints a sanitized banner and runs `aws` with argv only, with no shell (`internal/aws/ec2.go:71-121`, `stripControl` at `:124-132`). `AWSCLIAvailable` checks `PATH` (`ec2.go:140-144`). EC2 connect checks it before `tea.Exec` (`internal/views/ec2_list.go:252-272`).
- `tea.Exec` releases the terminal, runs the child, then restores the terminal and repaints at once (`exec.go:22-50`, `:102-129`, `tea.go:1388-1410`). The event loop blocks while the child runs (`tea.go:858-860`). Signals are ignored, so Ctrl+C reaches the child (`tea.go:1367-1373`). The child writes straight to the terminal (`exec.go:111-113`), so the app does not see its output unless the writer copies it.
- `GetCallerIdentityOutput` has `Account`, `Arn` and `UserId`. The call needs no permission (`service/sts@v1.51.6/api_op_GetCallerIdentity.go:14-17`, `:42-63`). sts is an indirect module (`go.mod:42`). IAM is not in `go.mod`.
- `arn.Parse` gives the account and the resource. An assumed role has the resource `assumed-role/ROLE/SESSION` (`core aws-sdk-go-v2@v1.47.5/aws/arn/arn.go:61-77`).
- `internal/aws` tests already use `httptest` fakes (`internal/aws/ec2_pages_test.go:31`).

## Hazards

PR 0 pins each hazard with a test that shows the behaviour today. The PR that fixes a hazard flips its test.

- H1. ReadWrite stays on over a profile switch (`app.go:1143-1165`).
- H2. `--read-write` and `W` turn ReadWrite on for any profile (`main.go:138-140`, `app.go:1197-1210`).
- H3. The mode badge is not on screen below 60 columns (`header.go:75-79`) or when the app hides the header (`app.go:1334-1339`).
- H4. A switch to a profile with no SSO token succeeds, and the error comes later (`client.go:81-123`). The test sets `HOME`, `AWS_CONFIG_FILE`, `AWS_SHARED_CREDENTIALS_FILE` and `AWS_LOGIN_CACHE_DIRECTORY` to temp paths with `t.Setenv`. An empty `HOME` is not enough, because the SDK then falls back to `user.Current().HomeDir` (`aws-sdk-go-v2@v1.47.5/internal/shareddefaults/shared_config.go:33-47`) and reads the real token cache.
- H5. For a `login_session` profile, the sign-in hint is missing or wrong (`errtext.go:148-178`). A missing or expired login token gives a plain error (`logincreds/provider.go:73-76`, `:98-104`, `:127-129`, `:175-185`). It is not an API error and does not have the text "SSO token", so `ErrorText` gives the raw text with no hint (`errtext.go:107`). An API error with an expired-token code gives the wrong hint `aws sso login` (`errtext.go:120-121`). PR 0 pins both cases.
- H6. With an endpoint, a profile that has only `login_session` gets the dummy keys (`client.go:158-159`).
- H7. All sessions share one mode (`services.go:17-28`). If a switch resets that mode, a background batch of the old session stops half done.

## Design

### Error hints

`ssoLoginNeeded` becomes `loginNeeded(err) (loginKind, bool)`. It keeps the SSO cases and also matches the logincreds errors by text ("please reauthenticate", `logincreds/provider.go:128`, `:175-185`), because logincreds returns plain errors.

`loginCommand(err)` reads the profile from `ProfileError` and classifies it with `ClassifyProfile` (see "Profile info"). It returns `aws login --profile ROOT` for a login root, `aws sso login --profile ROOT` for an SSO root, and "" for keys, a process, web identity or `credential_source`. ROOT is the profile at the end of the `source_profile` chain, because that profile signs in. With "", the text says "Get new credentials for profile X". When the profile does not load, it falls back to `aws sso login` as today.

`hasCredentials` also checks `sc.LoginSession != ""`. It needs no check for `credential_source`, because the SDK refuses a `credential_source` without `role_arn`, and the role check covers that (found in PR 1).

### The session mode

A new type in `internal/ui`. It stays in `ui`, because the views, the status bar and the key guard read the mode from there.

```go
// SessionMode is the access mode of one session. The guards of the AWS
// services read it from command goroutines, so it is safe for concurrent use.
type SessionMode struct {
	readOnly atomic.Bool
	lock     atomic.Pointer[string] // nil when not locked, else the reason: rule "prod"
	checked  atomic.Bool            // the account is confirmed, or no account rule exists
}

func NewSessionMode() *SessionMode // starts ReadOnly, unlocked, checked
func (m *SessionMode) ReadOnly() bool
func (m *SessionMode) Lock(reason string) // also sets ReadOnly
func (m *SessionMode) Locked() (bool, string)
func (m *SessionMode) SetChecked(bool)
// SetReadWrite changes nothing and returns an error when the session is
// locked or its account is not checked.
func (m *SessionMode) SetReadWrite() error
func (m *SessionMode) SetReadOnly()

func CurrentMode() *SessionMode
func SetCurrentMode(*SessionMode)
```

`ui` keeps an `atomic.Pointer[SessionMode]` that starts with `NewSessionMode()`. `ui.IsReadOnly()` keeps its name and reads the current mode, so the 17 call sites do not change. `ui.SetReadOnly(v)` also stays, as a thin wrapper on the current mode. `true` calls `SetReadOnly`. `false` calls `SetReadWrite` and logs a refusal, so the wrapper cannot pass a lock. The tests that call it keep working.

`aws.NewServices` keeps its `readOnly func() bool` argument, because `internal/aws` must not import `ui`. The app passes `mode.ReadOnly`, the method of that session's own mode. So the guards of a session read only that session's mode (fixes H7). A background operation of an old session keeps its old mode, which matches the promise of `requestSessionSwitch`.

The key guard and `refuseReadOnly` read `ui.IsReadOnly()`, which is the current session. That is correct, because they act on the views of the current session.

| Event | Mode of the new session |
| --- | --- |
| Start | ReadOnly. `--read-write` asks for ReadWrite through the same check as `W`. |
| Profile switch | ReadOnly (fixes H1). |
| Region switch, same profile | The new mode copies `readOnly` and `checked` from the old one, because the profile and the account are the same. Then the rules apply again, so a lock still wins. |
| Session built again after a sign-in | ReadOnly. |

`main` keeps calling `ui.SetReadOnly(false)` for `--read-write`. Before any session exists this only sets the start request on the initial mode. `New` reads it, builds the first session with a new mode, applies the rules, and then calls `SetReadWrite` when the start request was ReadWrite.

- When a rule locks the session by its name or by the account from the config, the session stays ReadOnly. `New` records a start warning. `main` reads it with a new accessor, `Model.StartWarnings()`, and prints "--read-write ignored: profile prod is locked by rule prod" on stderr before the TUI starts. The app also shows a warning toast. `app.New(cfg)` keeps its signature.
- When account rules exist, the first session is not checked yet, because `New` runs before any STS call. Then the ReadWrite request stays pending. The app applies it when the first `identityMsg` arrives, if that session is not locked. When STS locks the session or fails, the request is dropped and a toast says why. The app shows ReadOnly until then.

`applyMode("readwrite")` calls `SetReadWrite`. On error it shows a warning toast with the reason, for example "prod-admin is locked to ReadOnly by rule prod". The mode picker label reads "ReadWrite   (locked)" for a locked session. The key guard toast says "ReadOnly mode (locked by rule prod)" for a locked session.

### Rules in the config

A new array of tables. `Load` decodes it leniently like the rest of the file, and the strict decode gives the usual warnings for unknown keys in a rule. The rule checks run on the lenient result.

```toml
# A session is locked to ReadOnly when ANY matching rule has read_only = true.
# The first matching rule with a label sets the header label.
[[rules]]
name = "prod"                          # shown in messages: locked by rule "prod"
profiles = ["prod-*", "*-production"]  # globs, path.Match syntax
accounts = ["123456789012"]            # 12-digit account IDs
read_only = true
label = "PROD"                         # header badge, at most 8 characters
```

```go
type Rule struct {
	Name     string   `toml:"name"`
	Profiles []string `toml:"profiles"`
	Accounts []string `toml:"accounts"`
	ReadOnly bool     `toml:"read_only"`
	Label    string   `toml:"label"`
}
// Config gets Rules []Rule `toml:"rules"`.

type Match struct{ Lock, Label, Rule string }
func (c Config) MatchSession(profile, account string) Match
func (c Config) HasAccountRules() bool
```

`Load` checks each rule and adds warnings to the list of unknown keys. It warns for a bad glob (`path.Match(p, "")` returns `ErrBadPattern`), an account that is not 12 digits, a label longer than 8 characters, and a rule with no `profiles` and no `accounts`. A bad entry is dropped and the rest of the rule stays, so a typo in one glob does not unlock the other matches. The profile name `default` also matches the empty profile. Flags and env do not change rules. `WriteDefault` gets a commented example.

A label is drawn on the Error color when its rule locks, and on the Accent color otherwise. Rule colors are an open question (Q6).

A match by account is the strong rule, because one account can have many profile names. The docs recommend both.

### Account before STS

The app knows a likely account before any network call:

- sso-session or legacy SSO: `sso_account_id`.
- Role: the account in `role_arn`, with `arn.Parse`.
- login_session: `accessToken.accountId` in the login cache.
- Keys, `credential_process`, web identity without a role: unknown.

`New` and `switchSession` match the rules with this account at once. So a locked production account is locked from the first moment. When STS answers, its account is matched again. A match can only add a lock. STS never removes a lock that the name or the config account set.

The `checked` gate applies only when `HasAccountRules()` is true. Then a new session is not checked until STS answers, and `SetReadWrite` fails with "Checking the account, try again in a moment". When STS fails, `W` opens a confirm: "The app cannot check the account (reason). Rules by account cannot apply. Switch to ReadWrite?" A yes sets `checked` for this session. Without account rules, every session is checked at once, so users without rules see no change.

### Profile info

New file `internal/aws/profileinfo.go`:

```go
type CredKind int // KindKeys, KindSSO, KindSSOLegacy, KindLogin, KindProcess,
                  // KindWebIdentity, KindCredSource, KindDefault, KindInvalid

type TokenState int // TokenNA, TokenValid, TokenRefresh, TokenExpired,
                    // TokenMissing, TokenUnknown

type ProfileInfo struct {
	Name     string
	Kind     CredKind  // kind of the root credential
	Role     bool      // role_arn on this profile or in the chain
	MFA      bool      // mfa_serial in the chain: the app cannot answer the prompt
	Root     string    // profile that signs in, "" means Name
	TokenKey string    // sso-session name, start URL, or login session: a shared login
	Account  string    // account from the config, see "Account before STS"
	Region   string
	Token    TokenState
	Expires  time.Time
	Err      error     // LoadSharedConfigProfile error, Kind is KindInvalid
}

func ClassifyProfile(name string, now time.Time) ProfileInfo
func DescribeProfiles(now time.Time) []ProfileInfo
```

PR 1 adds `ClassifyProfile(name string)` with the kind, the role, the MFA mark and the root, and a `SignInCommand` method. PR 4 adds the `now` argument with the token state and the account.

`ClassifyProfile` calls `LoadSharedConfigProfile` with the same file overrides as `hasCredentials`. It walks `sc.Source` to the end of the chain and labels the root in the SDK order of `resolve_credentials.go:139-206`. In the SSO step it checks `sc.SSOSession != nil` first and returns `KindSSO`. It returns `KindSSOLegacy` only when `SSOSession` is nil and `SSOStartURL` is set. It never uses the legacy test of `shared_config.go:1499-1501` alone, because that test is true for most sso-session profiles. A profile with no credential settings is `KindDefault`.

The token state reads the cache file at the path from the SDK helpers: `ssocreds.StandardCachedTokenFilepath(key)` and `logincreds.StandardCachedTokenFilepath(session, os.Getenv("AWS_LOGIN_CACHE_DIRECTORY"))`. So the app does not copy the hash rules. It parses only `expiresAt`, the three refresh fields, and `accessToken.accountId` into small local structs.

- Valid: `now` is before `expiresAt`.
- Refresh: expired, and the refresh fields are present (sso-session with `refreshToken`, `clientId` and `clientSecret`, or login with `refreshToken`).
- Expired: expired, and no refresh is possible. This is always the case for legacy SSO when expired.
- Missing: no file.
- Unknown: the file does not parse, or it lacks a field that the SDK requires. For SSO, that is an empty `accessToken` or a missing or zero `expiresAt` (`ssocreds/sso_cached_token.go:157-160`). For login, that is any required field (`logincreds/token.go:61-89`). The classifier must not show such a file as valid or expired.

`DescribeProfiles` calls `ListProfiles` and then `ClassifyProfile` for each name. A profile that fails to load stays in the list as `KindInvalid`.

The token state is a hint, not a promise. The SDK checks expiry with no grace window, and a refresh can still fail on the network.

### The profile picker

`P` and `:profile` start `DescribeProfiles` in a `tea.Cmd`, and the picker opens when the result comes. Tens of profiles load in milliseconds (decision D5). Each row is one label with padded columns. The picker filters on the label, so the user can type `sso` or `prod`. All AWS text goes through `ui.Sanitize`. The app builds the labels to fit the width of the terminal: it truncates the profile name with an ellipsis, and below 80 columns it drops the account column. An invalid row shows only a short error class, such as `invalid: sso-session`. The full error goes to the toast when the user picks the row.

```
prod-admin     role>sso   123456789012  expired       PROD RO*
dev            sso        210987654321  valid 3h12m
legacy         sso-old    333333333333  not signed in
sandbox        login      555555555555  refresh
ci             keys       -
mfa-role       role>keys  444444444444  mfa (not supported)
broken         invalid: sso-session
```

The lock mark is a new `ServiceIcon` for a lock, with the fallback `RO*` without nerd fonts. The label and the lock come from `MatchSession` with the name and the config account. The current profile keeps its ` ●`. A pick of an invalid row shows the error in a toast.

### Pick, sign-in and switch

When the user picks a profile:

1. Token valid, refresh, or not applicable: switch as today with `requestSessionSwitch`.
2. Token expired or missing on an SSO, legacy SSO or login root: `askQuick` asks "prod-admin needs aws sso login (token expired). Sign in now?" A yes runs the sign-in and then switches. A no switches anyway. The new session starts in ReadOnly, and the banner then shows "login needed".
3. `MFA`: the app refuses the pick with a toast: "mfa-role needs an MFA code, which LazyCloud does not support yet". A switch cannot work, because `NewClient` fails with `AssumeRoleTokenProviderNotSetError`. `ErrorText` also maps that error to the same text. An MFA token provider is out of scope.
4. `KindProcess`: switch as today.

`:login` signs in the root of the current profile. `:login NAME` signs in the root of NAME. After a successful exit, the app switches to the target profile, with the current region, through `requestSessionSwitch`. So it asks first when operations run, as every switch does today (`app.go:1066-1080`). For `:login NAME`, the target is NAME, also when NAME is not the current profile. A new client has new credential caches, so an old failure does not stay. The `ErrorText` hints gain "(or :login)".

Sign-in is allowed in ReadOnly mode, because it changes no AWS resource.

`SSMSession` becomes a general `CLIExec` in `internal/aws/cliexec.go`: a banner, an argv, an env, and the `SetStdin`, `SetStdout`, `SetStderr` and `Run` methods. `SSMSessionCmd` returns a `*CLIExec` and keeps its behaviour. `LoginCmd(info ProfileInfo) *CLIExec` builds the sign-in:

- The argv has no shell: `aws sso login --profile=ROOT` or `aws login --profile=ROOT`. The profile name is one argument with `=`, so a name that starts with `-` cannot become a flag.
- When `SSH_CONNECTION` is set, it adds `--use-device-code` for SSO or `--remote` for login, because no browser can open (decision D7).
- The child env drops `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` and `AWS_PROFILE`. `AWS_CONFIG_FILE`, `AWS_SHARED_CREDENTIALS_FILE` and `AWS_LOGIN_CACHE_DIRECTORY` stay.
- The banner (control characters stripped) names the command, the profile, the account from the config, and says "A browser opens. Ctrl+C cancels."
- Exit 0: no pause, then the session is built again.
- Exit 130 or a signal: no pause, toast "Sign-in cancelled", the session does not change.
- Any other exit: the wrapper prints "Sign-in failed (exit N). Press Enter to return." and reads one line, so the CLI error stays on screen. Then a toast. The session does not change. When the output of `aws login` has "invalid choice", the toast says that `aws login` needs AWS CLI 2.32 or later. To see that text, `CLIExec.SetStderr` wraps the writer in an `io.MultiWriter` with a buffer that keeps the last 4 KiB. The text still shows on screen, and `Run` searches the buffer after the exit.
- No `aws` on `PATH`: no exec. A toast names the command to run in another terminal.

The exec blocks the event loop (`tea.go:858-860`). Operations go on in their goroutines and their messages queue. This is the same as SSM connect today.

### Identity

```go
// internal/aws/identity.go
type Identity struct {
	Account   string
	ARN       string
	Principal string // role name for assumed-role, else the user name or the ARN resource
	Session   string // session name for assumed-role
}
func (c *Client) CallerIdentity(ctx context.Context) (Identity, error)
```

The STS client gets the endpoint override like the S3, EC2 and SQS clients (`client.go:162-168`). After each successful session build (start, switch, sign-in), the app runs `CallerIdentity` in a `tea.Cmd` with a 10 second timeout. For a `credential_process` root, this call runs the process, and the process gets the terminal (`processcreds/provider.go:109-110`). The first load of a view already does the same today, so the identity call adds no new risk. Q5 is about the picker only. Each session gets a number, `m.sessionGen`, raised in `New` and `switchSession`. `identityMsg{Gen, Identity, Err}` is dropped when its number is not the current one.

| State | Header shows | ReadWrite |
| --- | --- | --- |
| Checking | `123456789012?` (config account) or `account?` | Allowed only without account rules |
| Known | `123456789012` and `ReadOnlyAccess` (the principal) | Allowed unless locked |
| Login needed | `login needed` on the Error color | Refused, because no call can succeed |
| Failed (network, timeout) | `account unknown`, reason in the event log | With account rules, only through the confirm |
| Endpoint set | the STS answer and an `endpoint` badge | Same as Known |

With an endpoint, the app applies all rules as usual. LocalStack answers `000000000000`, which matches no normal rule. `AWS_ENDPOINT_URL` can point at a real endpoint, so a skip of the rules leaves a gap.

When STS or a view fails with "login needed" on the current session, the banner state becomes "Login needed".

`:whoami` opens an info panel with the profile, the credential type, the account, the ARN, the user ID, the region, the endpoint, the mode, the lock reason and the token expiry. This is the only place that shows the full ARN.

### The header and the status bar

`HeaderData` gets `Account`, `AccountState`, `Principal`, `Label`, `LabelLocked` and `Locked`. All AWS text goes through `ui.Sanitize`. The header stays 2 lines, so `chromeHeight` does not change.

- Wide (120 and up): title, label, profile, account, principal, region, mode.
- Medium (80 to 119): label, profile, account, region, mode.
- 60 to 79: label, profile, mode.
- Below 60: label and mode, then breadcrumbs (fixes H3). The title goes, so that the mode fits.

The Medium tier gets the same budget, because a profile name can have any length.

A locked session shows the mode badge as `RO` with the lock icon, or `RO*` without nerd fonts. When the app hides the header on a short terminal, the status bar starts with a short session badge, for example `PROD RO*` or `RW`. It is the label badge and the mode badge of the header, so it takes at most 16 columns from the hints. The badge is also drawn on the error path of `RenderStatusBar`. The app hides the status bar last. So the mode is on screen at every size, except on a terminal so short that the app hides the status bar too (shorter than `MinTableRows` plus 3 lines plus the footer).

The header gets a width budget, because `RenderHeader` truncates only the breadcrumbs today. The profile and the principal are truncated with an ellipsis. For display, the principal drops the `AWSReservedSSO_` prefix and the `_<16 hex>` suffix of an SSO role name, so `AWSReservedSSO_ReadOnlyAccess_0123456789abcdef` shows as `ReadOnlyAccess`. `:whoami` shows the full name. The region also goes through `Sanitize`.

### What does not change

- The app starts in ReadOnly.
- A failed `NewServices` keeps the old session (`session_test.go:77`).
- The three ReadOnly checks stay. The key guard and the confirm read the current session. The service guard reads its own session.
- The picker widget and the header height stay.

## Visible changes

- The sign-in hint is right for `aws login` profiles and names `:login`.
- After a profile switch or a sign-in, the mode is ReadOnly. When it was ReadWrite, the toast says "Profile: prod · ReadOnly".
- `[[rules]]` in the config. A locked session shows a lock on the mode badge. `W` and `--read-write` are refused for it, with the reason.
- The profile picker shows the type, the account, the token state and the lock.
- A pick of a signed-out profile offers to sign in. `:login` and `:whoami` are new.
- The header shows the account, the principal and a rule label. The mode is visible at every size except a very short terminal.

## Plan

Each PR keeps `go test ./...` green and runs golangci-lint with gosec.

- PR 0. This design, and `internal/app/p9_pins_test.go` and `internal/aws/p9_pins_test.go`. `TestP9Hazard_*` pins H1 to H7 as they are today. `TestP9Keep_*` pins what stays: start in ReadOnly, a failed switch keeps the session, a switched session is guarded, the RO badge at 80 columns. Path tests compare the planned cache paths with the SDK helpers.
- PR 1. Error hints with no new UI: `loginNeeded`, `loginCommand` by kind and root, `hasCredentials` with `LoginSession`. It adds the first part of `ClassifyProfile` (kind and root only). H5 and H6 flip.
- PR 2. `ui.SessionMode`, per-session guards, ReadOnly on a profile switch and after a sign-in, the mode copied on a region switch. `ui.SetReadOnly` becomes the thin wrapper. `docs/ARCHITECTURE.md` updates the ReadOnly and session sections. H1 and H7 flip.
- PR 3. `[[rules]]` with checks and warnings, the `WriteDefault` example, match by profile name, locks in `New`, `switchSession`, `applyMode` and for `--read-write`. The lock badge in the header and the session badge in the status bar. H2 and H3 flip. The config section of the docs changes.
- PR 4. `DescribeProfiles` with the token state and the config account, and the new picker rows, loaded in a command. Rules also match by the config account.
- PR 5. `CLIExec`, `LoginCmd`, `:login`, the offer on a pick of a signed-out profile, the rebuild after a sign-in. H4 flips (the app offers a sign-in before the switch).
- PR 6. Identity: sts as a direct module, `Client.CallerIdentity`, `identityMsg` with the session number, match by the STS account, the `checked` gate and its confirm, the account, principal and state in the header, `:whoami`. Size tests change. The tracker marks Phase 9 item 1 and 2 done.

PR 1 does not depend on the others. PR 3 needs PR 2. PR 4 needs PR 1 (`ClassifyProfile`). PR 5 needs PR 4. PR 6 needs PR 4 for the config account.

## Test plan

Unit tests in `internal/aws`:

- `ClassifyProfile` as a table test over temp config files, set with `t.Setenv` for `AWS_CONFIG_FILE`, `AWS_SHARED_CREDENTIALS_FILE`, `HOME` and `AWS_LOGIN_CACHE_DIRECTORY`. Cases: each kind, an sso-session profile with `sso_account_id` and `sso_role_name` (must be `KindSSO`, not legacy), a legacy profile, a role over SSO chain, `credential_source`, `mfa_serial`, a missing `[sso-session]` section, a missing `source_profile`. A role over a login on one profile gives `KindLogin` with `Role` true and `Root` equal to the name. A role with `source_profile` that names a login-only profile gives `KindInvalid` with `SharedConfigAssumeRoleError`. A cycle A to B to A loads, and `Root` is A with the credential kind of A.
- Token state with cache files written at the SDK helper paths: valid, expired, expired with refresh fields, missing, and garbage. A fixed `now`.
- `ErrorText` with real SDK error values: `ssocreds.InvalidTokenError`, the logincreds errors, the expired codes for each kind, a role profile that names its root.
- `LoginCmd`: the argv for each kind, `--profile=` as one element, the SSH flags, the stripped env, control characters removed from the banner. `Run` with a fake `aws` script on `PATH` for exit 0, exit 1 with the Enter pause fed from stdin, and exit 130.
- ARN parsing for assumed-role, user and root.

Unit tests in `internal/ui` and `internal/config`:

- `SessionMode`: a lock refuses ReadWrite, an unchecked session refuses, concurrent reads and writes with `-race`, the `SetReadOnly` wrapper cannot pass a lock.
- Rules: globs, accounts, `default`, the first label wins, any lock wins, bad entries dropped with warnings, unknown keys in `[[rules]]` warned.
- The header at 50, 70, 100 and 130 columns, with a 40-character profile and a 60-character principal at 80 and 120 columns. The status bar badge when the header is hidden, also with an error. The mode is on screen down to the height where the status bar goes.
- Picker labels at 60 and 120 columns with a long profile name and a long load error.

httptest fakes:

- `CallerIdentity` against an `httptest.Server` set as the endpoint, in the style of `internal/aws/ec2_pages_test.go:31`. Cases: a good answer, a 403, a slow answer past the timeout, and an expired-token error that maps to "login needed".

App tests in `internal/app`:

- A switch from ReadWrite to another profile gives ReadOnly. A region switch keeps the mode. A background operation of the old session keeps its mode after a switch.
- `--read-write` on a locked profile starts in ReadOnly. `applyMode` refuses on a locked session.
- An `identityMsg` with an old number is dropped. An STS answer with a locked account locks the session. `W` while checking is refused. `W` after a failed check opens the confirm.
- A pick of a signed-out profile asks. A no still switches.
- `session_test.go:60, 77, 93, 108` stay green.

LocalStack smoke test:

- With the pty smoke test and the LocalStack dev env: the endpoint badge, account `000000000000`, a profile rule that locks, and `W` refused.

Manual checks:

- One real sso-session profile with an expired token, one legacy SSO profile, and one `aws login` profile, both on a local terminal and over SSH. This also confirms the CLI flags `--use-device-code` and `--remote` with the installed CLI before PR 5 merges.

Mutation testing:

- Run a mutation tool (for example gremlins) once by hand on `profileinfo.go`, the rule matcher and `SessionMode` before PR 4 and PR 6 merge. Each surviving mutant of a branch that decides a lock or a kind gets a test or a note in the PR.

## Decisions

- D1. Where the mode lives. Proposed: one `SessionMode` for each session, and the guards of a session read only that session's mode. Other answer: keep one global flag and reset it on a switch. That is simpler, but a background batch of the old session then stops half done.
- D2. What resets the mode. Proposed: a profile switch and a sign-in reset to ReadOnly. A region switch keeps the mode unless the new session is locked. Other answer: reset on every switch.
- D3. Rule format. Proposed: `[[rules]]` with `name`, `profiles`, `accounts`, `read_only` and `label`. Other answer: flat `[safety] read_only_profiles` and `read_only_accounts` lists, with no labels.
- D4. `--read-write` on a locked profile. Proposed: start in ReadOnly, print a line on stderr, and show a warning toast. With account rules, the request waits for the first STS answer and applies only if the session is not locked. Other answer: exit with code 2, and refuse `--read-write` while the account is being checked.
- D5. Picker loading. Proposed: run `DescribeProfiles` in a command and open the picker when it returns. Other answer: open at once with names, and replace the labels when the info arrives.
- D6. How the user starts a sign-in. Proposed: a confirm on a pick of a signed-out profile, plus `:login`. Other answer: an `l` key inside the picker, which needs a new picker key hook.
- D7. Sign-in flow over SSH. Proposed: add `--use-device-code` or `--remote` when `SSH_CONNECTION` is set. Other answer: always use the CLI defaults.
- D8. Account alias. Proposed: no IAM module and no alias in this phase. The rule label gives most of the value. Other answer: add `service/iam` and a best-effort `ListAccountAliases`.
- D9. The checked gate. Proposed: only when an account rule exists. ReadWrite waits for STS, and after a failed check it needs a confirm. Other answer: no gate, so rules by account apply only after STS answers.
- D10. Endpoint override. Proposed: apply all rules as usual and show an `endpoint` badge. Other answer: skip rules by account when an endpoint is set.
- D11. A declined sign-in. Proposed: the switch still happens, in ReadOnly, and the banner shows "login needed". Other answer: cancel the switch.

## Open questions

- Q1. Does a sign-in for the current profile, done to fix an expired token, reset ReadWrite to ReadOnly? Proposed answer: yes. It builds a new session, and the safe default is ReadOnly. The toast says so.
- Q2. Does the picker show the type of the profile itself, or of the root of its chain? Proposed answer: both, as `role>sso`. The sign-in targets the root.
- Q3. Does a sign-in mark every profile that shares the same sso-session or login session as fresh? Proposed answer: yes. `DescribeProfiles` runs again the next time the picker opens, so the shared token shows the new state with no extra code.
- Q4. Can a rule turn a whole set of profiles into "locked unless allowed"? Proposed answer: not in this phase. A later key such as `default_lock = true` with allow rules can come when users ask. The ReadOnly start already gives a safe default.
- Q5. Does the picker probe a `credential_process` or `mfa_serial` profile? Proposed answer: no. Such a probe can prompt on the terminal behind the TUI. These rows show their type and no token state. The identity call after a switch still runs the process, as the first view load does today.
- Q6. Can a rule set its own label color? Proposed answer: not in this phase. A locking rule uses the Error color and other rules use the Accent color. A `color` key can come later with the theme names as values.
- Q7. Does the app check `aws --version` before `aws login`? Proposed answer: no. It reads "invalid choice" from a failed exit and tells the user that `aws login` needs CLI 2.32 or later. That costs no extra process on the happy path.
