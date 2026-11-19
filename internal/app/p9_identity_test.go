package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/config"
	appmsg "github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// PR 6 tests: the account check of a session with STS.

// stsAccountRule locks the account that the fake STS answers.
var stsAccountRule = config.Rule{Name: "sts-prod", Accounts: []string{"999999999999"}, ReadOnly: true, Label: "LIVE"}

// prodIdentity is an STS answer for the account of stsAccountRule.
var prodIdentity = aws.Identity{
	Account:   "999999999999",
	ARN:       "arn:aws:sts::999999999999:assumed-role/AWSReservedSSO_Admin_0123456789abcdef/jane",
	UserID:    "AROA:jane",
	Principal: "AWSReservedSSO_Admin_0123456789abcdef",
	Session:   "jane",
}

// fakeIdentity makes the STS call return id and err, and counts the calls.
func fakeIdentity(t *testing.T, id aws.Identity, err error) *int {
	t.Helper()
	calls := 0
	before := fetchIdentity
	fetchIdentity = func(context.Context, *aws.Client) (aws.Identity, error) {
		calls++
		return id, err
	}
	t.Cleanup(func() { fetchIdentity = before })
	return &calls
}

// identityOf runs the STS command of cmd and returns its message.
func identityOf(t *testing.T, cmd tea.Cmd) identityMsg {
	t.Helper()
	got, ok := valueOf[identityMsg](collectMsgs(cmd))
	require.True(t, ok, "the command asks STS")
	return got
}

// The first session asks STS in Init, and the answer shows in the header.
func TestP9_StartAsksForTheIdentity(t *testing.T) {
	awsConfigEnv(t, "dev")
	calls := fakeIdentity(t, prodIdentity, nil)
	m := modelWithRules(t, "dev")
	assert.Contains(t, rendered(m), "account?", "checking")

	got := identityOf(t, m.Init())
	m, _ = update(t, m, got)

	assert.Equal(t, 1, *calls)
	assert.Equal(t, ui.AccountKnown, m.ident.state)
	out := rendered(m)
	assert.Contains(t, out, "999999999999")
	assert.Contains(t, out, "Admin", "the short principal")
	assert.NotContains(t, out, "AWSReservedSSO", "the header shows the short principal")
	assert.Contains(t, out, "endpoint", "the tests use an endpoint override")
}

// An answer for an older session is dropped.
func TestP9_OldIdentityIsDropped(t *testing.T) {
	awsConfigEnv(t, "dev", "other")
	fakeIdentity(t, prodIdentity, nil)
	m := modelWithRules(t, "dev", stsAccountRule)
	old := identityOf(t, m.Init())

	m, cmd := m.applyProfile("other")
	require.NotNil(t, findOf[identityMsg](collectMsgs(cmd)), "a switch asks STS again")
	m, _ = update(t, m, old)

	assert.Equal(t, ui.AccountChecking, m.ident.state)
	locked, _ := ui.CurrentMode().Locked()
	assert.False(t, locked, "the answer for dev does not lock other")
}

// With rules by account, ReadWrite waits for STS. A locked account locks
// the session, so ReadWrite stays refused.
func TestP9_STSAccountLocksTheSession(t *testing.T) {
	awsConfigEnv(t, "dev")
	fakeIdentity(t, prodIdentity, nil)
	m := modelWithRules(t, "dev", stsAccountRule)
	assert.False(t, ui.CurrentMode().Checked())

	m, _ = m.applyMode("readwrite")
	assert.True(t, ui.IsReadOnly())
	assert.Contains(t, rendered(m), "Checking the account, try again in a moment")

	m, _ = update(t, m, identityOf(t, m.Init()))

	locked, rule := ui.CurrentMode().Locked()
	assert.True(t, locked)
	assert.Equal(t, "sts-prod", rule)
	assert.Equal(t, "LIVE", m.match.Label, "the rule of the account gives the label")
	assert.Contains(t, rendered(m), "Account 999999999999 is locked to ReadOnly by rule sts-prod")
	m, _ = m.applyMode("readwrite")
	assert.True(t, ui.IsReadOnly())
	assert.Contains(t, rendered(m), "dev is locked to ReadOnly by rule sts-prod")
}

// An account that no rule locks allows ReadWrite after the check.
func TestP9_CheckedAccountAllowsReadWrite(t *testing.T) {
	awsConfigEnv(t, "dev")
	fakeIdentity(t, aws.Identity{Account: "123456789012"}, nil)
	m := modelWithRules(t, "dev", stsAccountRule)

	m, _ = update(t, m, identityOf(t, m.Init()))
	m, _ = m.applyMode("readwrite")

	assert.True(t, ui.CurrentMode().Checked())
	assert.False(t, ui.IsReadOnly())
	assert.Empty(t, m.match.Label)
}

// Without rules by account, ReadWrite needs no check.
func TestP9_NoAccountRulesNoGate(t *testing.T) {
	awsConfigEnv(t, "dev")
	m := modelWithRules(t, "dev", prodRule)

	assert.True(t, ui.CurrentMode().Checked())
	_, _ = m.applyMode("readwrite")
	assert.False(t, ui.IsReadOnly())
}

// After a failed check, W asks first. A yes switches without the rules by
// account. A no keeps ReadOnly.
func TestP9_FailedCheckAsksBeforeReadWrite(t *testing.T) {
	awsConfigEnv(t, "dev")
	fakeIdentity(t, aws.Identity{}, errors.New("dial tcp: no route to host"))
	m := modelWithRules(t, "dev", stsAccountRule)
	m, _ = update(t, m, identityOf(t, m.Init()))
	assert.Equal(t, ui.AccountUnknown, m.ident.state)
	assert.Contains(t, rendered(m), "account unknown")

	m, _ = m.applyMode("readwrite")
	assert.Contains(t, rendered(m), "The app cannot check the account (dial tcp: no route to host). Rules by account cannot apply. Switch to ReadWrite?")
	m = answer(t, m, press("n"))
	assert.True(t, ui.IsReadOnly())

	m, _ = m.applyMode("readwrite")
	m = answer(t, m, press("y"))
	assert.False(t, ui.IsReadOnly())
	assert.True(t, ui.CurrentMode().Checked())
}

// A yes after a switch does not change the new session.
func TestP9_ConfirmOfAnOldSessionDoesNothing(t *testing.T) {
	awsConfigEnv(t, "dev", "other")
	fakeIdentity(t, aws.Identity{}, errors.New("boom"))
	m := modelWithRules(t, "dev", stsAccountRule)
	m, _ = update(t, m, identityOf(t, m.Init()))
	m, _ = m.applyMode("readwrite")
	require.True(t, m.confirm.Visible())

	// A switch while the confirm is open, as a sign-in can do.
	m, _ = m.switchSession(sessionChange{profile: "other", kind: "Profile", value: "other"})
	m = answer(t, m, press("y"))

	assert.True(t, ui.IsReadOnly())
	assert.False(t, ui.CurrentMode().Checked())
}

// When STS says the credentials have expired, the header says so, and W
// asks for a sign-in.
func TestP9_LoginNeededFromSTS(t *testing.T) {
	awsConfigEnv(t, "dev")
	fakeIdentity(t, aws.Identity{}, errors.New("refresh cached SSO token failed"))
	m := modelWithRules(t, "dev", stsAccountRule)
	m, _ = update(t, m, identityOf(t, m.Init()))

	assert.Equal(t, ui.AccountLoginNeeded, m.ident.state)
	assert.Contains(t, rendered(m), "login needed")
	m, _ = m.applyMode("readwrite")
	assert.True(t, ui.IsReadOnly())
	assert.Contains(t, rendered(m), "Profile dev needs a sign-in first (:login)")
}

// --read-write waits for the check of the first session.
func TestP9_ReadWriteFlagWaitsForTheCheck(t *testing.T) {
	for _, tc := range []struct {
		name   string
		id     aws.Identity
		err    error
		rw     bool
		toast  string
		marker string
	}{
		{name: "account not locked", id: aws.Identity{Account: "123456789012"}, rw: true},
		{name: "account locked", id: prodIdentity, toast: "--read-write ignored: profile dev is locked to ReadOnly by rule sts-prod"},
		{name: "check failed", err: errors.New("boom"), toast: "--read-write ignored: the app cannot check the account (boom)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			awsConfigEnv(t, "dev")
			readWrite(t)
			fakeIdentity(t, tc.id, tc.err)
			m := modelWithRules(t, "dev", stsAccountRule)
			assert.True(t, ui.IsReadOnly(), "ReadOnly until STS answers")
			assert.True(t, m.pendingRW)
			assert.Empty(t, m.StartWarnings())

			m, _ = update(t, m, identityOf(t, m.Init()))

			assert.False(t, m.pendingRW)
			assert.Equal(t, tc.rw, !ui.IsReadOnly())
			if tc.toast != "" {
				assert.Contains(t, rendered(m), tc.toast)
			}
		})
	}
}

// A switch drops a --read-write that waits: the new profile starts in
// ReadOnly.
func TestP9_SwitchDropsTheWaitingReadWrite(t *testing.T) {
	awsConfigEnv(t, "dev", "other")
	readWrite(t)
	fakeIdentity(t, aws.Identity{Account: "123456789012"}, nil)
	m := modelWithRules(t, "dev", stsAccountRule)

	m, cmd := m.applyProfile("other")
	m, _ = update(t, m, identityOf(t, cmd))

	assert.False(t, m.pendingRW)
	assert.True(t, ui.IsReadOnly())
}

// A region switch keeps the answer of STS and its lock, because the
// credentials are the same. The new session is locked before STS answers
// again.
func TestP9_RegionSwitchKeepsTheIdentity(t *testing.T) {
	awsConfigEnv(t, "dev")
	fakeIdentity(t, prodIdentity, nil)
	m := modelWithRules(t, "dev", stsAccountRule)
	m, _ = update(t, m, identityOf(t, m.Init()))

	m, cmd := m.applyRegion("eu-west-1")

	assert.Equal(t, ui.AccountKnown, m.ident.state)
	locked, rule := ui.CurrentMode().Locked()
	assert.True(t, locked, "locked before STS answers again")
	assert.Equal(t, "sts-prod", rule)
	assert.True(t, ui.CurrentMode().Checked())
	assert.NotNil(t, findOf[identityMsg](collectMsgs(cmd)), "it asks STS again")
}

// A region switch of a checked account that no rule locks keeps
// ReadWrite.
func TestP9_RegionSwitchKeepsTheCheck(t *testing.T) {
	awsConfigEnv(t, "dev")
	fakeIdentity(t, aws.Identity{Account: "123456789012"}, nil)
	m := modelWithRules(t, "dev", stsAccountRule)
	m, _ = update(t, m, identityOf(t, m.Init()))
	m, _ = m.applyMode("readwrite")
	require.False(t, ui.IsReadOnly())

	m, _ = m.applyRegion("eu-west-1")

	assert.False(t, ui.IsReadOnly())
	assert.Contains(t, rendered(m), "123456789012")
}

// A sign-in starts the check again, also for the same profile.
func TestP9_SignInChecksAgain(t *testing.T) {
	awsConfigEnv(t, "dev")
	fakeIdentity(t, aws.Identity{Account: "123456789012"}, nil)
	m := modelWithRules(t, "dev", stsAccountRule)
	m, _ = update(t, m, identityOf(t, m.Init()))

	m, _ = m.signInDone(signInDoneMsg{target: "dev", info: aws.ProfileInfo{Name: "dev", Root: "dev"}})

	assert.Equal(t, ui.AccountChecking, m.ident.state)
	assert.False(t, ui.CurrentMode().Checked())
}

// Without a session, the app asks nothing.
func TestP9_NoSessionNoIdentity(t *testing.T) {
	awsConfigEnv(t)
	m := modelWithRules(t, "missing", stsAccountRule)
	require.Error(t, m.svcErr)

	assert.Nil(t, findOf[identityMsg](collectMsgs(m.Init())))
	assert.Equal(t, ui.AccountUnknown, m.ident.state)
}

// :whoami shows the full identity.
func TestP9_Whoami(t *testing.T) {
	awsConfigEnv(t, "dev")
	fakeIdentity(t, prodIdentity, nil)
	m := modelWithRules(t, "dev", stsAccountRule)
	m, _ = update(t, m, identityOf(t, m.Init()))

	m, cmd := m.executeCommand("whoami")
	nav, ok := valueOf[appmsg.NavigateMsg](collectMsgs(cmd))
	require.True(t, ok)
	content := nav.Params["content"]

	for _, want := range []string{
		"Profile      dev",
		"Credentials  default",
		"Account      999999999999",
		"ARN          " + prodIdentity.ARN,
		"User ID      AROA:jane",
		"Region       us-west-2",
		"Endpoint     http://127.0.0.1:1",
		"Mode         ReadOnly (locked by rule sts-prod)",
	} {
		assert.Contains(t, content, want)
	}
	m = pump(t, m, cmd)
	assert.Contains(t, rendered(m), "AWSReservedSSO_Admin_0123456789abcdef", "the panel shows the full ARN")
}

// :whoami names the state of a check that has not ended or failed.
func TestP9_WhoamiStates(t *testing.T) {
	awsConfigEnv(t, "dev")
	m := modelWithRules(t, "dev")
	assert.Equal(t, "checking", m.accountText())
	m.ident.configAccount = "111111111111"
	assert.Equal(t, "111111111111 (from the config, checking)", m.accountText())
	m.ident.state = ui.AccountLoginNeeded
	assert.Equal(t, "login needed", m.accountText())
	m.ident.state = ui.AccountUnknown
	assert.Equal(t, "unknown (the check failed)", m.accountText())

	m.ident.err = errors.New("boom")
	_, cmd := m.whoamiCommand()
	nav, _ := valueOf[appmsg.NavigateMsg](collectMsgs(cmd))
	content := nav.Params["content"]
	assert.Contains(t, content, "Error        boom")
	assert.NotContains(t, content, "ARN")
}

// The command bar knows :whoami.
func TestP9_WhoamiIsACommand(t *testing.T) {
	awsConfigEnv(t, "dev")
	m := modelWithRules(t, "dev")

	_, cmd := m.executeCommand("whoami")

	assert.NotNil(t, cmd)
	_, cmd = m.executeCommand("whoami extra")
	assert.Nil(t, findOf[appmsg.NavigateMsg](collectMsgs(cmd)), "an argument is an unknown command")
}

func TestP9_IdentityCmdUsesTheTimeout(t *testing.T) {
	awsConfigEnv(t, "dev")
	var deadline bool
	before := fetchIdentity
	fetchIdentity = func(ctx context.Context, _ *aws.Client) (aws.Identity, error) {
		_, deadline = ctx.Deadline()
		return aws.Identity{}, nil
	}
	t.Cleanup(func() { fetchIdentity = before })
	m := modelWithRules(t, "dev")

	got := identityOf(t, m.identityCmd())

	assert.True(t, deadline)
	assert.Equal(t, m.sessionGen, got.gen)
	assert.True(t, strings.HasPrefix(m.sessionLabel(), "dev/"))
}

// Review: a ReadOnly that the user picks while STS checks ends the
// --read-write that waits, so the STS answer does not switch to ReadWrite.
func TestP9_UserChoiceEndsTheWaitingReadWrite(t *testing.T) {
	awsConfigEnv(t, "dev")
	readWrite(t)
	fakeIdentity(t, aws.Identity{Account: "123456789012"}, nil)
	m := modelWithRules(t, "dev", stsAccountRule)
	require.True(t, m.pendingRW)

	m, _ = m.applyMode("readonly")
	m, _ = update(t, m, identityOf(t, m.Init()))

	assert.True(t, ui.IsReadOnly())
}

// Review: a failed check after a region switch drops the principal of the
// old session from the header.
func TestP9_FailedCheckDropsTheOldPrincipal(t *testing.T) {
	awsConfigEnv(t, "dev")
	calls := fakeIdentity(t, prodIdentity, nil)
	m := modelWithRules(t, "dev")
	m, _ = update(t, m, identityOf(t, m.Init()))
	require.Contains(t, rendered(m), "Admin")

	fetchIdentity = func(context.Context, *aws.Client) (aws.Identity, error) {
		*calls++
		return aws.Identity{}, errors.New("boom")
	}
	m, cmd := m.applyRegion("eu-west-1")
	assert.Contains(t, rendered(m), "Admin", "kept until STS answers")
	m, _ = update(t, m, identityOf(t, cmd))

	assert.NotContains(t, rendered(m), "Admin")
	assert.Contains(t, rendered(m), "account unknown")
	assert.Empty(t, m.ident.id)
}

// Review: --read-write with no session says that it is ignored.
func TestP9_ReadWriteFlagWithoutASession(t *testing.T) {
	awsConfigEnv(t)
	readWrite(t)

	m := modelWithRules(t, "missing", stsAccountRule)

	assert.True(t, ui.IsReadOnly())
	assert.False(t, m.pendingRW)
	assert.Equal(t, []string{"--read-write ignored: no AWS session"}, m.StartWarnings())
}

// Review: a session that needs a sign-in refuses ReadWrite, also without
// rules by account.
func TestP9_LoginNeededRefusesReadWrite(t *testing.T) {
	awsConfigEnv(t, "dev")
	fakeIdentity(t, aws.Identity{}, errors.New("refresh cached SSO token failed"))
	m := modelWithRules(t, "dev")
	m, _ = update(t, m, identityOf(t, m.Init()))
	require.True(t, ui.CurrentMode().Checked(), "no rules by account")

	m, _ = m.applyMode("readwrite")

	assert.True(t, ui.IsReadOnly())
	assert.Contains(t, rendered(m), "Profile dev needs a sign-in first (:login)")
}

// A region switch before STS answers keeps the session unchecked.
func TestP9_RegionSwitchBeforeTheCheck(t *testing.T) {
	awsConfigEnv(t, "dev")
	m := modelWithRules(t, "dev", stsAccountRule)
	require.False(t, ui.CurrentMode().Checked())

	m, _ = m.applyRegion("eu-west-1")

	assert.False(t, ui.CurrentMode().Checked())
	assert.Equal(t, ui.AccountChecking, m.ident.state)
}

// The rules by account add a label only when the session has none, and
// do not replace a lock by name.
func TestP9_STSMatchKeepsTheFirstLabelAndLock(t *testing.T) {
	awsConfigEnv(t, "prod-admin")
	fakeIdentity(t, prodIdentity, nil)
	m := modelWithRules(t, "prod-admin", prodRule, stsAccountRule)
	require.Equal(t, "PROD", m.match.Label)

	m, _ = update(t, m, identityOf(t, m.Init()))

	assert.Equal(t, "PROD", m.match.Label)
	_, rule := ui.CurrentMode().Locked()
	assert.Equal(t, "prod", rule, "the lock by name stays")
	assert.NotContains(t, rendered(m), "Account 999999999999 is locked", "no toast for a session that was locked")
}

// The label of a rule that matched the config account stays when STS
// names another account with another label.
func TestP9_STSMatchKeepsTheLabelOfTheConfigAccount(t *testing.T) {
	pickerEnv(t)
	fakeIdentity(t, prodIdentity, nil)
	cfgRule := config.Rule{Name: "cfg", Accounts: []string{"111111111111"}, Label: "CFG"}
	m := modelWithRules(t, "dev", cfgRule, stsAccountRule)
	require.Equal(t, "CFG", m.match.Label)

	m, _ = update(t, m, identityOf(t, m.Init()))

	assert.Equal(t, "CFG", m.match.Label)
	_, rule := ui.CurrentMode().Locked()
	assert.Equal(t, "sts-prod", rule, "the STS account still locks")
}
