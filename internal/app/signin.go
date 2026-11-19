package app

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// signInDoneMsg reports the end of a sign-in that the AWS CLI ran in the
// terminal.
type signInDoneMsg struct {
	target string          // the profile that the app switches to after the sign-in
	info   aws.ProfileInfo // the profile that signed in
	err    error
	output string // the end of the error output of the CLI
}

// needsSignIn reports whether a profile has no token that the SDK can use
// or refresh. Only a profile that signs in has a token state.
func needsSignIn(info aws.ProfileInfo) bool {
	return info.Token == aws.TokenExpired || info.Token == aws.TokenMissing
}

// signInVerb names the CLI command of a sign-in, without the profile.
func signInVerb(info aws.ProfileInfo) string {
	if info.Kind == aws.KindLogin {
		return "aws login"
	}
	return "aws sso login"
}

// offerSignIn asks whether to sign in before the switch to a signed-out
// profile. A yes signs in and then switches. A no switches anyway: the new
// session starts in ReadOnly, and its calls fail until a sign-in.
func (m Model) offerSignIn(info aws.ProfileInfo) (Model, tea.Cmd) {
	reason := "not signed in"
	if info.Token == aws.TokenExpired {
		reason = "token expired"
	}
	cmd := m.askQuick(fmt.Sprintf("%s needs %s (%s). Sign in now?", info.Name, signInVerb(info), reason),
		func(m Model) (Model, tea.Cmd) { return m, m.startSignIn(info.Name, info) },
		func(m Model) (Model, tea.Cmd) { return m.applyProfile(info.Name) })
	return m, cmd
}

// startSignIn runs the sign-in of info in the terminal, like an SSM
// session. A sign-in changes no AWS resource, so it runs in ReadOnly mode
// too. After it, the app switches to target.
func (m *Model) startSignIn(target string, info aws.ProfileInfo) tea.Cmd {
	c := aws.LoginCmd(info)
	if c == nil {
		_, cmd := m.toasts.Add(fmt.Sprintf("Profile %s has no sign-in (%s)", info.Name, info.Kind), ui.ToastError, 0)
		return cmd
	}
	if !aws.AWSCLIAvailable() {
		_, cmd := m.toasts.Add("aws CLI not found. Sign in from another terminal: "+info.SignInCommand(), ui.ToastError, 0)
		return cmd
	}
	eventlog.Infof(eventlog.CatAWS, "Sign-in started: %s", strings.Join(c.Args(), " "))
	return tea.Exec(c, func(err error) tea.Msg {
		return signInDoneMsg{target: target, info: info, err: err, output: c.ErrorOutput()}
	})
}

// signInDone builds the session again after a good sign-in, so that no
// failure of the old credentials stays. The new session starts in
// ReadOnly. A cancelled or failed sign-in changes nothing.
func (m Model) signInDone(msg signInDoneMsg) (Model, tea.Cmd) {
	switch {
	case msg.err == nil:
		eventlog.Infof(eventlog.CatAWS, "Sign-in done: %s", msg.info.Root)
		return m.requestSessionSwitch(sessionChange{
			profile: msg.target, region: m.config.AWS.Region,
			kind: "Profile", value: displayProfile(msg.target), signIn: true,
		})
	case aws.Cancelled(msg.err):
		eventlog.Infof(eventlog.CatAWS, "Sign-in cancelled: %s", msg.info.Root)
		_, cmd := m.toasts.Add("Sign-in cancelled", ui.ToastInfo, 0)
		return m, cmd
	}
	eventlog.Errorf(eventlog.CatAWS, "Sign-in failed for %s: %v", msg.info.Root, msg.err)
	text := fmt.Sprintf("Sign-in failed for %s (%v)", msg.info.Root, msg.err)
	if msg.info.Kind == aws.KindLogin && strings.Contains(msg.output, "invalid choice") {
		text = "aws login needs AWS CLI 2.32 or later. Update the AWS CLI"
	}
	_, cmd := m.toasts.Add(text, ui.ToastError, 0)
	return m, cmd
}

// loginCommand runs :login. Without a name, it signs in to the current
// profile. With a name, it signs in to that profile and then switches to
// it.
func (m Model) loginCommand(name string) (Model, tea.Cmd) {
	if name == "" {
		name = m.config.AWS.Profile
	}
	info := aws.ClassifyProfile(name, time.Now())
	if info.Kind == aws.KindInvalid {
		_, cmd := m.toasts.Add(fmt.Sprintf("Cannot use profile %s: %s", info.Name, aws.ErrorText(info.Err)), ui.ToastError, 0)
		return m, cmd
	}
	// Name the profile as the user named it: "" is the default profile,
	// and the session keeps "" for it.
	target := name
	return m, m.startSignIn(target, info)
}
