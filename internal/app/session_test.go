package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/config"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// These tests cover the session switch and the navigation reset of Phase 5
// in docs/reviews/2026-10-11: H6, M11, L3, and L5.

// awsConfigEnv points the AWS config files at a temporary file that has only
// the given profiles, so a test never reads the real ~/.aws files.
func awsConfigEnv(t *testing.T, profiles ...string) {
	t.Helper()
	dir := t.TempDir()
	content := "[default]\nregion = us-east-1\n"
	for _, p := range profiles {
		content += "\n[profile " + p + "]\nregion = us-west-2\n"
	}
	cfgFile := filepath.Join(dir, "config")
	require.NoError(t, os.WriteFile(cfgFile, []byte(content), 0o600))
	t.Setenv("AWS_CONFIG_FILE", cfgFile)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
}

// modelWithProfile starts the app with profile, against a closed local port.
func modelWithProfile(profile string) Model {
	cfg := config.DefaultConfig()
	cfg.AWS.Profile = profile
	cfg.AWS.Endpoint = "http://127.0.0.1:1"
	m := New(cfg)
	m.width, m.height = 140, 40
	return m
}

func rendered(m Model) string {
	return ansi.Strip(m.View().Content)
}

// H6: when the client cannot be built at startup, the app used to keep
// services around a nil client, and the first AWS call panicked. Now it
// starts without a session, says so, and opens no AWS view.
func TestStartWithoutSession(t *testing.T) {
	awsConfigEnv(t)
	m := modelWithProfile("typo")
	require.Error(t, m.svcErr)
	assert.Nil(t, m.svc.S3)
	assert.Contains(t, rendered(m), "No AWS session")

	m, _ = update(t, m, msg.NavigateMsg{ViewID: "s3_list"})
	assert.Equal(t, 1, m.nav.Depth(), "no AWS view opens without a session")
	assert.True(t, m.toasts.HasActive())

	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
	assert.Equal(t, "eventlog", m.nav.Current().ID(), "the event log needs no session")
}

// H6: a profile that cannot be used must not replace a working session or
// reset the views.
func TestFailedProfileSwitchKeepsSession(t *testing.T) {
	awsConfigEnv(t)
	m := modelWithProfile("")
	require.NoError(t, m.svcErr)
	m.svc.EC2 = new(awstest.MockEC2Service)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "ec2_list"})
	before := m.svc

	m, _ = m.applyProfile("typo")
	assert.Same(t, before.Client, m.svc.Client, "the session stays")
	assert.Same(t, before.EC2, m.svc.EC2)
	assert.Empty(t, m.config.AWS.Profile, "the configuration stays")
	assert.Equal(t, 2, m.nav.Depth(), "the views stay")
	assert.Contains(t, rendered(m), "Cannot switch profile to typo")
}

func TestProfileSwitchStartsSession(t *testing.T) {
	awsConfigEnv(t, "good")
	m := modelWithProfile("typo")
	require.Error(t, m.svcErr)

	m, _ = m.applyProfile("good")
	require.NoError(t, m.svcErr)
	assert.NotNil(t, m.svc.S3)
	assert.Equal(t, "us-west-2", m.activeRegion(), "the region comes from the profile")
	assert.Empty(t, m.err)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "s3_list"})
	assert.Equal(t, "s3_list", m.nav.Current().ID())
}

// The guarded services survive a session switch.
func TestSwitchedSessionIsGuarded(t *testing.T) {
	awsConfigEnv(t, "good")
	ui.SetReadOnly(true)
	m := modelWithProfile("")
	m, _ = m.applyProfile("good")
	require.NoError(t, m.svcErr)
	assert.ErrorIs(t, m.svc.S3.DeleteBucket(t.Context(), "b"), aws.ErrReadOnly)
}

// M11: after a link, the user can open more views. Back goes to the view
// that showed the panel, not one level up.
func TestCrossNavBackPopsToLinkOrigin(t *testing.T) {
	m := newTestModel(140, 40)
	m.svc.EC2 = new(awstest.MockEC2Service)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "ec2_list"})
	m.openPanel("web-server-1", "{}", "json")

	m, _ = update(t, m, ui.ContentLinkActivatedMsg{ViewID: "vpc_list", Params: map[string]string{"focus": "vpc-1"}})
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "subnet_list", Params: map[string]string{"vpc_id": "vpc-1"}})
	require.Equal(t, 4, m.nav.Depth())

	m, _ = update(t, m, keyBackspace)
	assert.Equal(t, "ec2_list", m.nav.Current().ID())
	assert.True(t, m.panels.IsOpen())
	assert.Equal(t, "web-server-1", m.panels.Title())
}

// M11: :home starts over. Back on Home restores nothing. A theme switch
// keeps the views and the panel (A8, TestA8_H1_SwitchBelowHomeKeepsTheViews).
func TestResetClearsPanelAndHistory(t *testing.T) {
	for name, reset := range map[string]func(Model) Model{
		"home command": func(m Model) Model { m, _ = m.executeCommand("home"); return m },
	} {
		t.Run(name, func(t *testing.T) {
			m := newTestModel(140, 40)
			m.svc.EC2 = new(awstest.MockEC2Service)
			m, _ = update(t, m, msg.NavigateMsg{ViewID: "ec2_list"})
			m.openPanel("web-server-1", "{}", "json")
			m.panels.Push(m.nav.Depth())
			m.panels.StashState(panelState{title: "old", owner: m.nav.Current()})
			require.True(t, m.panels.IsOpen(), "the panel is open and a panel is stashed")

			m = reset(m)
			assert.Equal(t, 1, m.nav.Depth())
			assert.False(t, m.panels.IsOpen())
			assert.Empty(t, m.panels.history)
			assert.Empty(t, m.panels.StashTitle())

			m, _ = update(t, m, keyBackspace)
			assert.False(t, m.panels.IsOpen(), "backspace on Home restores no old panel")
		})
	}
}

// runTick runs a tick command and returns its message, also when it is an
// animation frame (collectMsgs skips those).
func runTick(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	require.NotNil(t, cmd)
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case got := <-done:
		return got
	case <-time.After(time.Second):
		t.Fatal("the tick command did not return")
		return nil
	}
}

// runAll runs cmd and the commands of nested batches, and returns every
// message, also animation frames. A command that takes longer than 200 ms,
// such as a toast dismiss, gives no message.
func runAll(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case got := <-done:
		if b, ok := got.(tea.BatchMsg); ok {
			var out []tea.Msg
			for _, c := range b {
				out = append(out, runAll(c)...)
			}
			return out
		}
		return []tea.Msg{got}
	case <-time.After(200 * time.Millisecond):
		return nil
	}
}

// P3: the home animation pauses while the terminal does not have the focus.
// The focus starts a new tick chain, and a frame of the old chain ends it.
func TestHomeAnimationPausesWithoutFocus(t *testing.T) {
	m := newTestModel(140, 40)
	assert.True(t, m.View().ReportFocus, "the app asks the terminal for focus reports")
	oldFrame := runTick(t, m.nav.InitCurrent())

	m, _ = update(t, m, tea.BlurMsg{})
	_, next := update(t, m, oldFrame)
	assert.Nil(t, next, "the chain stops without the focus")

	m, restart := update(t, m, tea.FocusMsg{})
	require.NotNil(t, restart, "the focus starts the animation again")
	_, next = update(t, m, oldFrame)
	assert.Nil(t, next, "a frame of the old chain ends it")
	_, next = update(t, m, runTick(t, restart))
	assert.NotNil(t, next, "the new chain goes on")
}

// P3: a focus report without a blur before it starts no second chain, and
// the focus below the home view starts no animation.
func TestFocusStartsTheAnimationOnlyAfterABlurOnHome(t *testing.T) {
	m := newTestModel(140, 40)
	m, cmd := update(t, m, tea.FocusMsg{})
	assert.Nil(t, cmd)

	// The EC2 list starts a load in Init, so a focus that called Init would
	// return a command.
	m.svc.EC2 = new(awstest.MockEC2Service)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "ec2_list"})
	require.NotNil(t, m.nav.InitCurrent(), "Init of the list returns a command")
	m, _ = update(t, m, tea.BlurMsg{})
	_, cmd = update(t, m, tea.FocusMsg{})
	assert.Nil(t, cmd)
}

// L3: Home shown again starts a new tick chain. A frame of the old chain
// that was still in flight must end that chain, not start a second one.
func TestHomeKeepsOneTickChain(t *testing.T) {
	m := newTestModel(140, 40)
	oldFrame := runTick(t, m.nav.InitCurrent())
	require.IsType(t, nav.Routed{}, oldFrame)

	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
	m, restart := update(t, m, msg.NavigateBackMsg{})
	require.Equal(t, 1, m.nav.Depth())
	require.NotNil(t, restart, "returning to Home starts the animation")

	_, next := update(t, m, oldFrame)
	assert.Nil(t, next, "the old chain ends")
	_, next = update(t, m, runTick(t, restart))
	assert.NotNil(t, next, "the new chain goes on")
}

// A8: a theme switch on the home view keeps its tick chain. It starts no
// second chain.
func TestThemeSwitchKeepsHomeAnimation(t *testing.T) {
	a8Theme(t)
	m := newTestModel(140, 40)
	frame := runTick(t, m.nav.InitCurrent())

	m, cmd := m.applyTheme("nord")
	for _, got := range runAll(cmd) {
		if r, ok := got.(nav.Routed); ok {
			_, isFrame := r.Msg.(ui.AnimationFrameMsg)
			assert.False(t, isFrame, "no second chain")
		}
	}
	_, next := update(t, m, frame)
	assert.NotNil(t, next, "the chain goes on")
}

// L5: the root of the nearest view under the top of the stack opens again
// after a session switch. Subnets stay subnets, the event log does not send
// the user back to Home, and the objects of a bucket go back to the buckets.
func TestTopLevelViewIDUsesRouteRoots(t *testing.T) {
	m := newTestModel(140, 40)
	m.svc.EC2 = new(awstest.MockEC2Service)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "subnet_list"})
	assert.Equal(t, "subnet_list", m.topLevelViewID())

	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
	assert.Equal(t, "subnet_list", m.topLevelViewID())

	m, _ = update(t, m, msg.NavigateMsg{ViewID: "s3_objects", Params: map[string]string{"bucket": "b"}})
	assert.Equal(t, "s3_list", m.topLevelViewID(), "s3_objects has the root s3_list")
}
