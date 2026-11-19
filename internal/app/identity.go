package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/config"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	appmsg "github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// identityTimeout limits the STS call that checks the account of a new
// session.
var identityTimeout = 10 * time.Second

// fetchIdentity asks STS for the identity of a session. Tests replace it.
var fetchIdentity = func(ctx context.Context, c *aws.Client) (aws.Identity, error) {
	return c.CallerIdentity(ctx)
}

// sessionIdentity is what the app knows about the account of the current
// session.
type sessionIdentity struct {
	state ui.AccountState
	// configAccount is the account from the AWS config files, shown while
	// STS has not answered. It is "" when the config does not name one.
	configAccount string
	id            aws.Identity // set in the state AccountKnown
	err           error        // set in the states AccountLoginNeeded and AccountUnknown
}

// identityMsg is the STS answer for the session with the number gen.
type identityMsg struct {
	gen int
	id  aws.Identity
	err error
}

// identityCmd asks STS for the identity of the current session.
func (m Model) identityCmd() tea.Cmd {
	if m.svc.Client == nil {
		return nil // no session
	}
	gen, client := m.sessionGen, m.svc.Client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), identityTimeout)
		defer cancel()
		id, err := fetchIdentity(ctx, client)
		return identityMsg{gen: gen, id: id, err: err}
	}
}

// accountMatch applies the rules by account to a session whose account
// is now known. It can only add a lock, and a label when the session has
// none. It returns the rule that locks the session now, or "" when the
// session was locked before or no rule locks it.
func accountMatch(cfg config.Config, profile, account string, mode *ui.SessionMode, match *config.Match) string {
	am := cfg.MatchSession(profile, account)
	if match.Label == "" && am.Label != "" {
		match.Label, match.LabelLocks = am.Label, am.LabelLocks
	}
	if am.Lock == "" {
		return ""
	}
	if locked, _ := mode.Locked(); locked {
		return ""
	}
	mode.Lock(am.Lock)
	match.Lock = am.Lock
	return am.Lock
}

// identityDone handles the STS answer for the current session. A known
// account can lock the session by a rule. The check ends either way, and a
// --read-write that waited for it applies now if no rule locks the session.
func (m Model) identityDone(msg identityMsg) (Model, tea.Cmd) {
	if msg.gen != m.sessionGen {
		return m, nil // the answer for a session that is gone
	}
	mode := ui.CurrentMode()
	pending := m.pendingRW
	m.pendingRW = false
	profile := displayProfile(m.config.AWS.Profile)

	if msg.err != nil {
		// A region switch keeps the identity of the old session until STS
		// answers. A failed answer drops it.
		m.ident.id, m.ident.err = aws.Identity{}, msg.err
		m.ident.state = ui.AccountUnknown
		if aws.LoginNeeded(msg.err) {
			m.ident.state = ui.AccountLoginNeeded
		}
		eventlog.Warnf(eventlog.CatAWS, "Cannot check the account of profile %s: %v", profile, msg.err)
		if !pending {
			return m, nil
		}
		w := "--read-write ignored: the app cannot check the account (" + aws.ErrorText(msg.err) + ")"
		eventlog.Warnf(eventlog.CatApp, "%s", w)
		_, cmd := m.toasts.Add(w, ui.ToastError, 0)
		return m, cmd
	}

	m.ident.state, m.ident.id, m.ident.err = ui.AccountKnown, msg.id, nil
	eventlog.Infof(eventlog.CatAWS, "Account of profile %s: %s (%s)", profile, msg.id.Account, msg.id.ARN)
	var cmds []tea.Cmd
	if rule := accountMatch(m.config, m.config.AWS.Profile, msg.id.Account, mode, &m.match); rule != "" {
		text := fmt.Sprintf("Account %s is locked to ReadOnly by rule %s", msg.id.Account, rule)
		eventlog.Infof(eventlog.CatConfig, "%s", text)
		_, cmd := m.toasts.Add(text, ui.ToastInfo, 0)
		cmds = append(cmds, cmd)
	}
	mode.SetChecked(true)
	if pending {
		if err := mode.SetReadWrite(); err != nil {
			w := fmt.Sprintf("--read-write ignored: profile %s is %s", profile, err)
			eventlog.Warnf(eventlog.CatApp, "%s", w)
			_, cmd := m.toasts.Add(w, ui.ToastError, 0)
			cmds = append(cmds, cmd)
		} else {
			eventlog.Warnf(eventlog.CatApp, "Mode changed → ReadWrite (--read-write)")
		}
	}
	return m, tea.Batch(cmds...)
}

// refuseUnchecked answers W while the account of the session is not
// checked. While STS has not answered, the user must wait. After a failed
// check, a confirm lets the user switch without the rules by account.
// applyMode refuses a session that needs a sign-in before this.
func (m Model) refuseUnchecked() (Model, tea.Cmd) {
	if m.ident.state != ui.AccountUnknown {
		_, cmd := m.toasts.Add("Checking the account, try again in a moment", ui.ToastInfo, 0)
		return m, cmd
	}
	profile := displayProfile(m.config.AWS.Profile)
	gen := m.sessionGen
	cmd := m.askQuick(fmt.Sprintf("The app cannot check the account (%s). Rules by account cannot apply. Switch to ReadWrite?", aws.ErrorText(m.ident.err)),
		func(m Model) (Model, tea.Cmd) {
			if m.sessionGen != gen {
				return m, nil // the session changed while the confirm was open
			}
			ui.CurrentMode().SetChecked(true)
			eventlog.Warnf(eventlog.CatApp, "Account of profile %s not checked: ReadWrite confirmed", profile)
			return m.applyMode("readwrite")
		}, nil)
	return m, cmd
}

// whoamiCommand runs :whoami. It shows the identity of the session in a
// panel, and is the only place that shows the full ARN.
func (m Model) whoamiCommand() (Model, tea.Cmd) {
	info := aws.ClassifyProfile(m.config.AWS.Profile, time.Now())
	rows := [][2]string{
		{"Profile", displayProfile(m.config.AWS.Profile)},
		{"Credentials", credentialsText(info)},
		{"Account", m.accountText()},
	}
	if m.ident.state == ui.AccountKnown {
		rows = append(rows,
			[2]string{"ARN", m.ident.id.ARN},
			[2]string{"User ID", m.ident.id.UserID})
	}
	if m.ident.err != nil {
		rows = append(rows, [2]string{"Error", aws.ErrorText(m.ident.err)})
	}
	endpoint := "AWS"
	if m.config.AWS.Endpoint != "" {
		endpoint = m.config.AWS.Endpoint
	}
	rows = append(rows,
		[2]string{"Region", m.activeRegion()},
		[2]string{"Endpoint", endpoint},
		[2]string{"Mode", modeText(ui.CurrentMode())})
	if t := tokenText(info); t != "" {
		rows = append(rows, [2]string{"Token", t})
	}
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%-12s %s\n", r[0], ui.Sanitize(r[1]))
	}
	content := b.String()
	return m, func() tea.Msg {
		return appmsg.NavigateMsg{ViewID: "content", Params: map[string]string{
			"title": "whoami", "content": content, "format": string(ui.FormatText),
		}}
	}
}

// headerAccount is the account that the header shows: the answer of STS,
// or the account from the config while STS has not answered.
func (m Model) headerAccount() string {
	if m.ident.state == ui.AccountKnown {
		return m.ident.id.Account
	}
	return m.ident.configAccount
}

// headerPrincipal is the principal that the header shows, short for an
// SSO role. It is "" until STS names it, and after a failed check.
func (m Model) headerPrincipal() string {
	return aws.DisplayPrincipal(m.ident.id.Principal)
}

// accountText names the account of the session and the state of its
// check, for :whoami.
func (m Model) accountText() string {
	switch m.ident.state {
	case ui.AccountKnown:
		return m.ident.id.Account
	case ui.AccountLoginNeeded:
		return "login needed"
	case ui.AccountUnknown:
		return "unknown (the check failed)"
	default: // STS has not answered yet
		if m.ident.configAccount != "" {
			return m.ident.configAccount + " (from the config, checking)"
		}
		return "checking"
	}
}

func credentialsText(info aws.ProfileInfo) string {
	text := info.Kind.String()
	if info.Role {
		text += " + role"
	}
	if info.Root != "" && info.Root != info.Name {
		text += " (signs in with profile " + info.Root + ")"
	}
	return text
}

func modeText(mode *ui.SessionMode) string {
	if locked, rule := mode.Locked(); locked {
		return "ReadOnly (locked by rule " + rule + ")"
	}
	if mode.ReadOnly() {
		return "ReadOnly"
	}
	return "ReadWrite"
}

func tokenText(info aws.ProfileInfo) string {
	expires := ""
	if !info.Expires.IsZero() {
		expires = ", expires " + info.Expires.Local().Format("2006-01-02 15:04")
	}
	switch info.Token {
	case aws.TokenValid:
		return "valid" + expires
	case aws.TokenRefresh:
		return "expired, the SDK refreshes it" + expires
	case aws.TokenExpired:
		return "expired" + expires
	case aws.TokenMissing:
		return "not signed in"
	case aws.TokenUnknown:
		return "unknown"
	default: // the profile has no token
		return ""
	}
}
