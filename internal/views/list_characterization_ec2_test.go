package views

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	smithy "github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// Characterization tests for ec2_list (Phase 8, PR 0). They record the
// behavior of the view before the port to the shared list core. They go
// through the public surface (Update, View, Footer, KeyMap and the commands
// that Update returns). Lines that use a private name end with "// private".
// A test that records a bug names the row of the divergence table in
// docs/design/phase8-resource-list.md that will change it.

// --- helpers ---------------------------------------------------------------

var charEC2Launch = time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)

func charEC2Instances() []aws.Instance {
	return []aws.Instance{
		{ID: "i-1", Name: "web", State: "running", Type: "t3.micro", PrivateIP: "10.0.0.1",
			PublicIP: "1.2.3.4", AvailabilityZone: "us-east-1a", LaunchTime: charEC2Launch},
		{ID: "i-2", Name: "db", State: "stopped", Type: "t3.large", PrivateIP: "10.0.0.2",
			AvailabilityZone: "us-east-1b"},
		{ID: "i-3", State: "running", Type: "t3.nano"},
	}
}

// charEC2Globals pins the globals that change the rendered text, and
// restores them after the test.
func charEC2Globals(t *testing.T) {
	t.Helper()
	nerd, ro := ui.UseNerdFonts, ui.IsReadOnly()
	theme := ui.ActiveTheme
	t.Cleanup(func() {
		ui.UseNerdFonts = nerd
		ui.SetReadOnly(ro)
		ui.ActiveTheme = theme
		ui.RebuildStyles()
	})
	ui.UseNerdFonts = false
	ui.SetReadOnly(false) // m and o are ReadWrite keys
	ui.ActiveTheme = ui.DefaultTheme
	ui.RebuildStyles()
}

// newCharEC2 builds a view with a mock service and a 160x12 window.
func newCharEC2(t *testing.T, client *aws.Client) (*EC2List, *awstest.MockEC2Service) {
	t.Helper()
	charEC2Globals(t)
	svc := new(awstest.MockEC2Service)
	v := NewEC2List(svc, client)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})
	return v, svc
}

// charEC2ExpectPage makes the next ListInstancesPage call (first page) return
// the instances.
func charEC2ExpectPage(svc *awstest.MockEC2Service, instances ...aws.Instance) {
	svc.On("ListInstancesPage", mock.Anything, (*string)(nil)).
		Return(&aws.InstancePage{Instances: instances}, nil).Once()
}

// charEC2Loaded builds a view and loads the instances through Init.
func charEC2Loaded(t *testing.T, client *aws.Client, instances ...aws.Instance) (*EC2List, *awstest.MockEC2Service) {
	t.Helper()
	v, svc := newCharEC2(t, client)
	charEC2ExpectPage(svc, instances...)
	pump(v, v.Init(), 5)
	return v, svc
}

// charEC2Lines returns the ANSI-stripped View, one entry per line, with the
// trailing spaces and the empty lines removed.
func charEC2Lines(v tea.Model) []string {
	var out []string
	for l := range strings.SplitSeq(plain(v.View().Content), "\n") {
		if l = strings.TrimRight(l, " "); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func charEC2Text(v tea.Model) string { return strings.Join(charEC2Lines(v), "\n") }

// charEC2NoTicks drops spinner ticks.
func charEC2NoTicks(msgs []tea.Msg) []tea.Msg {
	var out []tea.Msg
	for _, m := range msgs {
		if _, tick := m.(spinner.TickMsg); !tick {
			out = append(out, m)
		}
	}
	return out
}

func charEC2Toasts(cmd tea.Cmd) []msg.ToastMsg { return toasts(collectMsgsTimed(cmd, maxWait)) }

// charEC2OneToast runs cmd and returns its only toast.
func charEC2OneToast(t *testing.T, cmd tea.Cmd) msg.ToastMsg {
	t.Helper()
	ts := charEC2Toasts(cmd)
	require.Len(t, ts, 1)
	return ts[0]
}

// charEC2Picker presses m and returns the picker request.
func charEC2Picker(t *testing.T, v *EC2List) msg.RequestActionPickerMsg {
	t.Helper()
	_, cmd := v.Update(keyPress('m'))
	p, ok := firstOfType[msg.RequestActionPickerMsg](collectMsgs(cmd))
	require.True(t, ok)
	return p
}

// charEC2Confirm picks the action and returns the confirm request.
func charEC2Confirm(t *testing.T, v *EC2List, action string) msg.RequestConfirmMsg {
	t.Helper()
	_, cmd := v.Update(ui.PickerResultMsg{ID: "action", Selected: 0, Value: action})
	c, ok := firstOfType[msg.RequestConfirmMsg](collectMsgs(cmd))
	require.True(t, ok)
	return c
}

func charEC2FgSGR(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)
}

func charEC2Detail() *aws.InstanceDetail {
	return &aws.InstanceDetail{
		InstanceID: "i-1", Name: "web", State: "running", InstanceType: "t3.micro",
		Platform: "Linux/UNIX", Architecture: "x86_64",
		PrivateIP: "10.0.0.1", PublicIP: "1.2.3.4",
		PrivateDNS: "ip-10-0-0-1.ec2.internal", PublicDNS: "ec2-1-2-3-4.compute-1.amazonaws.com",
		VpcID: "vpc-1", SubnetID: "subnet-1", AvailabilityZone: "us-east-1a",
		KeyName: "key", AMI: "ami-1", IAMRole: "role", LaunchTime: "2024-01-02T03:04:05Z",
		RootDeviceType: "ebs", RootDeviceName: "/dev/xvda",
		SecurityGroups: []aws.SecurityGroupRef{{ID: "sg-1", Name: "web-sg"}, {ID: "sg-2", Name: "ssh"}},
		Tags:           map[string]string{"Name": "web", "Env": "prod"},
	}
}

// charEC2FakePath puts empty executables with the given names in a temp dir
// and makes it the only PATH entry.
func charEC2FakePath(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		require.NoError(t, os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o700)) //nolint:gosec // test executable in a temp dir
	}
	t.Setenv("PATH", dir)
}

// --- All views -------------------------------------------------------------

func TestCharEC2_IDAndTitle(t *testing.T) {
	v, _ := newCharEC2(t, nil)
	assert.Equal(t, "ec2_list", v.ID())
	assert.Equal(t, "EC2 Instances", v.Title())
}

func TestCharEC2_KeyMapSnapshot(t *testing.T) {
	v, _ := newCharEC2(t, nil)
	snap := func() string {
		var b strings.Builder
		for _, h := range v.KeyMap() {
			fmt.Fprintf(&b, "%s|%s|%s|mode=%d|enabled=%t|cat=%q\n",
				strings.Join(h.Keys(), ","), h.Help().Key, h.Help().Desc, h.Mode, h.Enabled(), h.Category)
		}
		return b.String()
	}
	ui.SetReadOnly(true)
	assert.Equal(t, `enter|enter/d|details|mode=0|enabled=true|cat=""
o|o|connect (SSM)|mode=2|enabled=false|cat=""
m|m|manage|mode=2|enabled=false|cat=""
y|y|copy ID|mode=0|enabled=true|cat=""
space|space|select|mode=0|enabled=true|cat=""
s|s/S|sort|mode=0|enabled=true|cat=""
/|/|filter|mode=0|enabled=true|cat=""
r|r|refresh|mode=0|enabled=true|cat=""
`, snap())
	assert.Equal(t, ui.ModeReadWrite, ui.HintMode(2))

	ui.SetReadOnly(false)
	assert.Equal(t, `enter|enter/d|details|mode=0|enabled=true|cat=""
o|o|connect (SSM)|mode=2|enabled=true|cat=""
m|m|manage|mode=2|enabled=true|cat=""
y|y|copy ID|mode=0|enabled=true|cat=""
space|space|select|mode=0|enabled=true|cat=""
s|s/S|sort|mode=0|enabled=true|cat=""
/|/|filter|mode=0|enabled=true|cat=""
r|r|refresh|mode=0|enabled=true|cat=""
`, snap())
}

// The view does not check the mode itself: in ReadOnly mode, m and o still
// act. The app blocks the disabled keys before they reach the view.
func TestCharEC2_ReadOnlyIsNotCheckedByTheView(t *testing.T) {
	v, _ := charEC2Loaded(t, nil, charEC2Instances()...)
	ui.SetReadOnly(true) // charEC2Globals restores it
	p := charEC2Picker(t, v)
	assert.Equal(t, []string{"Stop", "Reboot", "Terminate"}, p.Options)
	_, cmd := v.Update(keyPress('o'))
	assert.Equal(t, "AWS client not available — check the profile", charEC2OneToast(t, cmd).Text)
}

func TestCharEC2_CapturingInputWhileFilterOpen(t *testing.T) {
	v, _ := charEC2Loaded(t, nil, charEC2Instances()...)
	assert.False(t, v.CapturingInput())

	v.Update(keyPress('/'))
	assert.True(t, v.CapturingInput())
	assert.Equal(t, "┃ / filter...", charEC2Lines(v)[0])

	// Typed letters go to the filter, not to the key bindings.
	_, cmd := v.Update(keyPress('d'))
	deliverAll(v, charEC2NoTicks(collectMsgs(cmd))...)
	assert.Equal(t, "1/3 instances", plain(v.Footer()))
	assert.True(t, v.CapturingInput())

	// enter closes the filter and keeps the text.
	v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.False(t, v.CapturingInput())
	assert.Equal(t, "1/3 instances", plain(v.Footer()))

	// esc in the open filter clears it.
	v.Update(keyPress('/'))
	_, cmd = v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	deliverAll(v, collectMsgs(cmd)...)
	assert.False(t, v.CapturingInput())
	assert.Equal(t, "3/3 instances", plain(v.Footer()))
}

func TestCharEC2_InitLoadsOnlyWhileLoading(t *testing.T) {
	v, svc := newCharEC2(t, nil)
	charEC2ExpectPage(svc, charEC2Instances()...)
	cmd := v.Init()
	require.NotNil(t, cmd)
	pump(v, cmd, 5)
	svc.AssertNumberOfCalls(t, "ListInstancesPage", 1)
	assert.Equal(t, "3/3 instances", plain(v.Footer()))

	// A cached view (back from another view) does not reload, but it
	// deselects.
	v.Update(keyPress(' '))
	require.Equal(t, "3/3 instances  (1 selected)", plain(v.Footer()))
	assert.Nil(t, v.Init())
	assert.Equal(t, "3/3 instances", plain(v.Footer()))
	svc.AssertNumberOfCalls(t, "ListInstancesPage", 1)
}

// A fresh view (load generation 0) accepts a page of generation 0 with page
// number 1 or 0. Tests and the app tests build these pages by hand.
func TestCharEC2_FreshViewAcceptsGenZeroPage(t *testing.T) {
	v, _ := newCharEC2(t, nil)
	v.Update(listPageMsg[aws.Instance]{items: charEC2Instances()[:1], page: 1}) // private
	assert.Equal(t, "1/1 instances", plain(v.Footer()))

	v2, _ := newCharEC2(t, nil)
	v2.Update(listPageMsg[aws.Instance]{items: charEC2Instances()[:2]}) // private
	assert.Equal(t, "2/2 instances", plain(v2.Footer()))
}

func TestCharEC2_StalePageDropped(t *testing.T) {
	v, svc := charEC2Loaded(t, nil, charEC2Instances()...)

	charEC2ExpectPage(svc, aws.Instance{ID: "i-old", State: "running"})
	_, first := v.Update(keyPress('r'))
	older := charEC2NoTicks(collectMsgs(first))
	charEC2ExpectPage(svc, aws.Instance{ID: "i-new1", State: "running"}, aws.Instance{ID: "i-new2", State: "running"})
	_, second := v.Update(keyPress('r'))
	newer := charEC2NoTicks(collectMsgs(second))
	require.Len(t, older, 1)
	require.Len(t, newer, 1)

	deliverAll(v, newer...)
	assert.Equal(t, "2/2 instances", plain(v.Footer()))
	_, cmd := v.Update(older[0])
	assert.Nil(t, cmd, "a page from an older load is dropped")
	assert.Equal(t, "2/2 instances", plain(v.Footer()))
	assert.NotContains(t, charEC2Text(v), "i-old")
}

func TestCharEC2_ErrorContext(t *testing.T) {
	v, svc := newCharEC2(t, nil)
	boom := errors.New("boom")
	svc.On("ListInstancesPage", mock.Anything, (*string)(nil)).Return(nil, boom).Once()
	e, ok := firstOfType[msg.ErrorMsg](pump(v, v.Init(), 5))
	require.True(t, ok)
	assert.Equal(t, "listing EC2 instances", e.Context)
	require.ErrorIs(t, e.Err, boom)

	// A view without a service reports an error and does not panic.
	charEC2Globals(t)
	nilView := NewEC2List(nil, nil)
	e, ok = firstOfType[msg.ErrorMsg](pump(nilView, nilView.Init(), 5))
	require.True(t, ok)
	// Design change in PR 7 (fetchPages adapter, as in PR 5): a nil service
	// fails like any load, with the context of the load. It was "EC2".
	assert.Equal(t, "listing EC2 instances", e.Context)
	assert.EqualError(t, e.Err, "AWS client not initialized")
}

func TestCharEC2_ErrorView(t *testing.T) {
	v, _ := newCharEC2(t, nil)
	v.Update(msg.ErrorMsg{Err: errors.New("access denied\x1b[31m"), Context: "listing EC2 instances"})
	out := v.View().Content
	assert.Equal(t, "\n  Error: access denied␛[31m", plain(out), "sanitized aws.ErrorText")
	assert.Contains(t, out, charEC2FgSGR(ui.ActiveTheme.Error), "theme red")
	assert.Equal(t, "0/0 instances", plain(v.Footer()), "the error hides the spinner")

	// A theme switch changes the red.
	ui.ActiveTheme = ui.CatppuccinLatte
	ui.RebuildStyles()
	assert.Contains(t, v.View().Content, charEC2FgSGR(ui.CatppuccinLatte.Error))
}

// Divergence #8 (fixed in PR 7): a good load clears the error, also when it
// arrives on its own. r clears it at once.
func TestCharEC2_GoodLoadClearsError(t *testing.T) {
	v, svc := newCharEC2(t, nil)
	charEC2ExpectPage(svc, charEC2Instances()...)
	page := charEC2NoTicks(collectMsgs(v.Init()))
	v.Update(msg.ErrorMsg{Err: errors.New("throttled")})
	deliverAll(v, page...)
	assert.Equal(t, "3/3 instances", plain(v.Footer()))
	assert.Contains(t, charEC2Text(v), "i-1", "Divergence #8 (fixed in PR 7)")
	assert.NotContains(t, charEC2Text(v), "Error")

	v.Update(msg.ErrorMsg{Err: errors.New("throttled")})
	require.Equal(t, "  Error: throttled", charEC2Text(v))
	charEC2ExpectPage(svc, charEC2Instances()...)
	_, cmd := v.Update(keyPress('r'))
	assert.Contains(t, charEC2Text(v), "i-1", "r clears the error at once; the rows show")
	pump(v, cmd, 5)
	assert.Contains(t, charEC2Text(v), "i-1")
	assert.NotContains(t, charEC2Text(v), "Error")
}

// Divergence #9 (fixed in PR 7): the failure of an older load is dropped.
func TestCharEC2_StaleErrorDropped(t *testing.T) {
	v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
	svc.On("ListInstancesPage", mock.Anything, (*string)(nil)).Return(nil, errors.New("old failure")).Once()
	_, first := v.Update(keyPress('r'))
	older := charEC2NoTicks(collectMsgs(first))
	charEC2ExpectPage(svc, charEC2Instances()...)
	_, second := v.Update(keyPress('r'))
	pump(v, second, 5)
	require.Equal(t, "3/3 instances", plain(v.Footer()))

	require.Len(t, older, 1, "the failure of the older load")
	_, failed := firstOfType[msg.ErrorMsg](pump(v, func() tea.Msg { return older[0] }, 5))
	assert.False(t, failed, "no ErrorMsg for the status bar")
	assert.Contains(t, charEC2Text(v), "i-1")
	assert.NotContains(t, charEC2Text(v), "old failure")
}

func TestCharEC2_FirstLoadView(t *testing.T) {
	v, svc := newCharEC2(t, nil)
	assert.Equal(t, "  ⣾  Loading EC2 instances...", charEC2Text(v))
	assert.Equal(t, "0/0 instances  ⣾  Loading EC2 instances...", plain(v.Footer()))

	charEC2ExpectPage(svc, charEC2Instances()...)
	pump(v, v.Init(), 5)
	assert.Equal(t, strings.Join([]string{
		" Instance ID              Name                            State             Type            Private IP        Public IP         AZ              Launched",
		strings.Repeat("─", 160),
		" i-1                      web                             ● running         t3.micro        10.0.0.1          1.2.3.4           us-east-1a      2024-01-02",
		" i-2                      db                              ○ stopped         t3.large        10.0.0.2                            us-east-1b",
		" i-3                                                      ● running         t3.nano",
	}, "\n"), charEC2Text(v))
	assert.Equal(t, "3/3 instances", plain(v.Footer()))
}

func TestCharEC2_FooterWhileLoadingPages(t *testing.T) {
	v, svc := newCharEC2(t, nil)
	tok := "page2"
	all := charEC2Instances()
	svc.On("ListInstancesPage", mock.Anything, (*string)(nil)).
		Return(&aws.InstancePage{Instances: all[:2], HasMorePages: true, Token: &tok}, nil).Once()
	svc.On("ListInstancesPage", mock.Anything, &tok).
		Return(&aws.InstancePage{Instances: all[2:]}, nil).Once()

	msgs := charEC2NoTicks(collectMsgs(v.Init()))
	_, next := v.Update(msgs[0])
	assert.Equal(t, "2/2 instances  (loading... 2 so far)  ⣾  Loading instances... 2 so far", plain(v.Footer()))
	assert.Contains(t, charEC2Text(v), "i-2", "rows show while later pages load")

	// The table takes keys during the load (cursor moves, copy works).
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, cp := v.Update(keyPress('y'))
	got := collectMsgs(cp)
	assert.Contains(t, got, tea.SetClipboard("i-2")())
	assert.Equal(t, []msg.ToastMsg{msg.ToastSuccess("Copied 1 instance ID(s)")}, toasts(got))

	pump(v, next, 5)
	assert.Equal(t, "3/3 instances", plain(v.Footer()))
	svc.AssertNumberOfCalls(t, "ListInstancesPage", 2)
}

func TestCharEC2_FooterWithSelectionAndFilter(t *testing.T) {
	v, _ := charEC2Loaded(t, nil, charEC2Instances()...)
	assert.Equal(t, "3/3 instances", plain(v.Footer()))
	v.Update(keyPress(' '))
	assert.Equal(t, "3/3 instances  (1 selected)", plain(v.Footer()))
	assert.Contains(t, charEC2Lines(v)[2], " ✓ i-1", "the selection marker")
	v.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	assert.Equal(t, "3/3 instances", plain(v.Footer()), "space toggles")

	v.Update(ui.FilterChangedMsg{Text: "web"})
	assert.Equal(t, "1/3 instances", plain(v.Footer()))
}

// Divergence #7 (fixed in PR 7): the rows stay during a full reload, with the
// spinner in the footer. The full-screen spinner shows only on the first load.
func TestCharEC2_ReloadView(t *testing.T) {
	v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
	charEC2ExpectPage(svc, charEC2Instances()...)
	_, cmd := v.Update(keyPress('r'))
	assert.Contains(t, charEC2Text(v), "i-1")
	// Divergence #15 (fixed in PR 7): "so far" shows only once a page of this
	// load arrived. It counted the old rows.
	assert.Equal(t, "3/3 instances  ⣾  Loading EC2 instances...", plain(v.Footer()))
	pump(v, cmd, 5)
	assert.Equal(t, "3/3 instances", plain(v.Footer()))
}

func TestCharEC2_ColumnsPerWidth(t *testing.T) {
	v, _ := charEC2Loaded(t, nil, charEC2Instances()...)
	headers := map[int]string{
		// 4 columns
		60: " Instance ID      Name               State         Type",
		// 5 columns
		100: " Instance ID                 Name                           State       Type        Private IP",
		// 8 columns
		160: " Instance ID              Name                            State             Type            Private IP        Public IP         AZ              Launched",
		200: " Instance ID                        Name                                                State             Type            Private IP        Public IP         AZ              Launched",
	}
	rows := map[int]string{
		60:  " i-1              web                ● running     t3.micro",
		100: " i-1                         web                            ● running   t3.micro    10.0.0.1",
		160: " i-1                      web                             ● running         t3.micro        10.0.0.1          1.2.3.4           us-east-1a      2024-01-02",
		200: " i-1                                web                                                 ● running         t3.micro        10.0.0.1          1.2.3.4           us-east-1a      2024-01-02",
	}
	for _, w := range []int{60, 100, 160, 200} {
		v.Update(tea.WindowSizeMsg{Width: w, Height: 12})
		lines := charEC2Lines(v)
		assert.Equal(t, headers[w], lines[0], "width %d", w)
		assert.Equal(t, rows[w], lines[2], "cells per tier, width %d", w)
	}
	// Back from wide to narrow rebuilds the rows with 4 cells.
	v.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	assert.Equal(t, rows[60], charEC2Lines(v)[2])

	// Base widths per tier (private: the port keeps ec2Columns).
	type col struct {
		title string
		w     int
	}
	base := func(tier ui.WidthTier) []col {
		var out []col
		for _, c := range ec2Columns(tier) { // private
			out = append(out, col{c.Title, c.Width})
		}
		return out
	}
	assert.Equal(t, []col{{"Instance ID", 21}, {"Name", 24}, {"State", 16}, {"Type", 14}}, base(ui.TierNarrow))
	assert.Equal(t, []col{{"Instance ID", 19}, {"Name", 15}, {"State", 10}, {"Type", 10}, {"Private IP", 15}}, base(ui.TierMedium))
	assert.Equal(t, []col{{"Instance ID", 21}, {"Name", 24}, {"State", 16}, {"Type", 14},
		{"Private IP", 16}, {"Public IP", 16}, {"AZ", 14}, {"Launched", 12}}, base(ui.TierWide))
}

func TestCharEC2_SortPicker(t *testing.T) {
	v, _ := charEC2Loaded(t, nil, charEC2Instances()...)
	_, cmd := v.Update(keyPress('s'))
	req, ok := firstOfType[msg.RequestSortPickerMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, msg.RequestSortPickerMsg{
		Columns:    []string{"Instance ID", "Name", "State", "Type", "Private IP", "Public IP", "AZ", "Launched"},
		CurrentCol: -1,
	}, req)

	ids := func() []string {
		var out []string
		for _, l := range charEC2Lines(v)[2:] {
			out = append(out, strings.Fields(l)[0])
		}
		return out
	}
	require.Equal(t, []string{"i-1", "i-2", "i-3"}, ids())

	v.Update(ui.PickerResultMsg{ID: "sort", Selected: 3, Value: "Type"})
	assert.Equal(t, []string{"i-2", "i-1", "i-3"}, ids(), "t3.large, t3.micro, t3.nano")
	_, cmd = v.Update(keyPress('s'))
	req, _ = firstOfType[msg.RequestSortPickerMsg](collectMsgs(cmd))
	assert.Equal(t, 3, req.CurrentCol)

	v.Update(keyPress('S'))
	assert.Equal(t, []string{"i-3", "i-1", "i-2"}, ids(), "S reverses")
	v.Update(keyPress('S'))

	// The sort survives a tier change.
	v.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	assert.Equal(t, []string{"i-2", "i-1", "i-3"}, ids())
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})
	assert.Equal(t, []string{"i-2", "i-1", "i-3"}, ids())

	// A cancelled picker does nothing; _clear clears.
	v.Update(ui.PickerResultMsg{ID: "sort", Selected: -1})
	assert.Equal(t, []string{"i-2", "i-1", "i-3"}, ids())
	v.Update(ui.PickerResultMsg{ID: "sort", Selected: 0, Value: "_clear"})
	assert.Equal(t, []string{"i-1", "i-2", "i-3"}, ids())
}

// C3: cursor, selection and sort follow the key across a reload.
// C4: the filter deselects hidden rows. M8: the marker is not in the key.
func TestCharEC2_TableKeepsIdentity(t *testing.T) {
	v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
	v.Update(ui.PickerResultMsg{ID: "sort", Selected: 1, Value: "Name"})
	v.Update(msg.FocusResourceMsg{ResourceID: "i-2"}) // cursor to i-2; the detail cmd is not run
	v.Update(keyPress(' '))                           // select i-2
	ids := func() []string {
		var out []string
		for _, l := range charEC2Lines(v)[2:] {
			out = append(out, strings.Fields(strings.TrimPrefix(l, " ✓"))[0])
		}
		return out
	}
	require.Equal(t, []string{"i-3", "i-2", "i-1"}, ids())

	// The refresh renames i-2, so the sort moves it to the end (SetRows).
	svc.On("RefreshInstances", mock.Anything, []string{"i-2"}).
		Return([]aws.Instance{{ID: "i-2", Name: "zz", State: "stopped"}}, nil).Once()
	_, cmd := v.Update(keyPress('r'))
	pump(v, cmd, 3)
	assert.Equal(t, "3/3 instances  (1 selected)", plain(v.Footer()))
	assert.Equal(t, []string{"i-3", "i-1", "i-2"}, ids(), "the sort is kept")
	assert.Contains(t, charEC2Lines(v)[4], "✓ i-2", "selection follows the key")
	_, cp := v.Update(keyPress('y'))
	assert.Contains(t, collectMsgs(cp), tea.SetClipboard("i-2")(), "the target follows the key")

	// M8: details use the plain key of the selected cursor row.
	svc.On("GetInstanceDetail", mock.Anything, "i-2").Return(&aws.InstanceDetail{InstanceID: "i-2"}, nil).Once()
	_, cmd = v.Update(keyPress('d'))
	collectMsgs(cmd)
	svc.AssertCalled(t, "GetInstanceDetail", mock.Anything, "i-2")

	// C4
	v.Update(ui.FilterChangedMsg{Text: "web"})
	assert.Equal(t, "1/3 instances", plain(v.Footer()))
	v.Update(ui.FilterChangedMsg{Text: ""})
	assert.Equal(t, "3/3 instances", plain(v.Footer()), "the hidden row stays deselected")
}

func TestCharEC2_StateCellsArePlainAndColoredByColumn(t *testing.T) {
	v, _ := charEC2Loaded(t, nil, charEC2Instances()...)
	out := v.View().Content
	assert.Equal(t, 2, strings.Count(out, ui.FgRender(ui.ActiveTheme.StateRunning, "● running")))
	assert.Equal(t, 1, strings.Count(out, ui.FgRender(ui.ActiveTheme.StateStopped, "○ stopped")))

	// The filter matches the plain text, not the icon.
	v.Update(ui.FilterChangedMsg{Text: "●"})
	assert.Equal(t, "0/3 instances", plain(v.Footer()))
	v.Update(ui.FilterChangedMsg{Text: "stopped"})
	assert.Equal(t, "1/3 instances", plain(v.Footer()))
}

func TestCharEC2_FocusWhileLoadingOpensAfterLoad(t *testing.T) {
	v, svc := newCharEC2(t, nil)
	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "i-2"})
	assert.Nil(t, cmd)

	charEC2ExpectPage(svc, charEC2Instances()...)
	svc.On("GetInstanceDetail", mock.Anything, "i-2").Return(&aws.InstanceDetail{InstanceID: "i-2", Name: "db"}, nil).Once()
	seen := pump(v, v.Init(), 5)
	panel, ok := firstOfType[msg.TabbedContentMsg](seen)
	require.True(t, ok)
	assert.Equal(t, "db (i-2)", panel.PanelTitle)

	// The cursor moved to the focused row.
	_, cp := v.Update(keyPress('y'))
	assert.Contains(t, collectMsgs(cp), tea.SetClipboard("i-2")())
}

func TestCharEC2_FocusOnUnlistedIDFetchesDetail(t *testing.T) {
	v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
	svc.On("GetInstanceDetail", mock.Anything, "i-elsewhere").
		Return(&aws.InstanceDetail{InstanceID: "i-elsewhere"}, nil).Once()
	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "i-elsewhere"})
	require.NotNil(t, cmd)
	panel, ok := firstOfType[msg.TabbedContentMsg](pump(v, cmd, 3))
	require.True(t, ok)
	assert.Equal(t, "i-elsewhere", panel.PanelTitle)
}

// Divergence #22 (keep): a focus on a row that the filter hides opens the
// panel and leaves the cursor and the filter as they are.
func TestCharEC2_FocusOnFilteredRow(t *testing.T) {
	v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
	v.Update(ui.FilterChangedMsg{Text: "web"})
	svc.On("GetInstanceDetail", mock.Anything, "i-2").Return(&aws.InstanceDetail{InstanceID: "i-2"}, nil).Once()
	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "i-2"})
	_, ok := firstOfType[msg.TabbedContentMsg](pump(v, cmd, 3))
	assert.True(t, ok)
	assert.Equal(t, "1/3 instances", plain(v.Footer()))
	_, cp := v.Update(keyPress('y'))
	assert.Contains(t, collectMsgs(cp), tea.SetClipboard("i-1")())
}

// Divergence #51 (keep): a stored focus survives an ErrorMsg and opens after
// the next good load.
func TestCharEC2_PendingFocusSurvivesError(t *testing.T) {
	v, svc := newCharEC2(t, nil)
	v.Update(msg.FocusResourceMsg{ResourceID: "i-1"})
	v.Update(msg.ErrorMsg{Err: errors.New("boom")})

	charEC2ExpectPage(svc, charEC2Instances()...)
	svc.On("GetInstanceDetail", mock.Anything, "i-1").Return(charEC2Detail(), nil).Once()
	_, cmd := v.Update(keyPress('r'))
	panel, ok := firstOfType[msg.TabbedContentMsg](pump(v, cmd, 5))
	require.True(t, ok)
	assert.Equal(t, "web (i-1)", panel.PanelTitle)
}

func TestCharEC2_UpdateReturnsSamePointer(t *testing.T) {
	v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
	svc.On("ListInstancesPage", mock.Anything, mock.Anything).Return(&aws.InstancePage{}, nil).Maybe()
	svc.On("GetInstanceDetail", mock.Anything, mock.Anything).Return(&aws.InstanceDetail{}, nil).Maybe()
	svc.On("RefreshInstances", mock.Anything, mock.Anything).Return([]aws.Instance{}, nil).Maybe()
	msgs := []tea.Msg{
		tea.WindowSizeMsg{Width: 100, Height: 20},
		keyPress(' '), keyPress('/'), tea.KeyPressMsg{Code: tea.KeyEscape},
		keyPress('s'), keyPress('S'), keyPress('y'), keyPress('m'), keyPress('o'), keyPress('d'),
		tea.KeyPressMsg{Code: tea.KeyEnter}, tea.KeyPressMsg{Code: tea.KeyDown}, keyPress('r'),
		tea.KeyPressMsg{Code: tea.KeyEscape}, tea.KeyPressMsg{Code: tea.KeyEscape},
		ui.PickerResultMsg{ID: "sort", Selected: 1, Value: "Name"},
		ui.PickerResultMsg{ID: "action", Selected: 0, Value: "Stop"},
		ui.PickerResultMsg{ID: "other", Selected: 0},
		ui.ConfirmResultMsg{Confirmed: false, Action: "ec2_stop"},
		ui.FilterChangedMsg{Text: "x"},
		msg.FocusResourceMsg{ResourceID: "i-1"},
		msg.ErrorMsg{Err: errors.New("e")},
		spinner.TickMsg{},
		struct{}{},
	}
	for _, m := range msgs {
		got, _ := v.Update(m)
		require.Same(t, v, got, "%T", m)
	}
}

// --- ec2_list --------------------------------------------------------------

func TestCharEC2_ConnectChecks(t *testing.T) {
	t.Run("nil client", func(t *testing.T) {
		v, _ := newCharEC2(t, nil)
		_, cmd := v.Update(keyPress('o'))
		assert.Equal(t, msg.ToastError("AWS client not available — check the profile"), charEC2OneToast(t, cmd))
	})
	t.Run("no rows", func(t *testing.T) {
		v, _ := charEC2Loaded(t, &aws.Client{})
		_, cmd := v.Update(keyPress('o'))
		assert.Nil(t, cmd)
	})
	t.Run("not running, uses the cursor", func(t *testing.T) {
		charEC2FakePath(t, "aws", "session-manager-plugin")
		v, _ := charEC2Loaded(t, &aws.Client{}, charEC2Instances()...)
		v.Update(keyPress(' ')) // select i-1 (running)
		v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		_, cmd := v.Update(keyPress('o'))
		assert.Equal(t, msg.ToastError("Instance is stopped — must be running for SSM"), charEC2OneToast(t, cmd))
	})
	t.Run("no aws CLI", func(t *testing.T) {
		charEC2FakePath(t, "session-manager-plugin")
		v, _ := charEC2Loaded(t, &aws.Client{}, charEC2Instances()...)
		_, cmd := v.Update(keyPress('o'))
		assert.Equal(t, msg.ToastError("aws CLI not found — install it first"), charEC2OneToast(t, cmd))
	})
	t.Run("no plugin", func(t *testing.T) {
		charEC2FakePath(t, "aws")
		v, _ := charEC2Loaded(t, &aws.Client{}, charEC2Instances()...)
		_, cmd := v.Update(keyPress('o'))
		assert.Equal(t, msg.ToastError("session-manager-plugin not found — install it first"), charEC2OneToast(t, cmd))
	})
	t.Run("all present", func(t *testing.T) {
		charEC2FakePath(t, "aws", "session-manager-plugin")
		v, _ := charEC2Loaded(t, &aws.Client{}, charEC2Instances()...)
		_, cmd := v.Update(keyPress('o'))
		require.NotNil(t, cmd)
		res := cmd() // tea.Exec builds a message; it does not run the CLI here
		_, isToast := res.(msg.ToastMsg)
		assert.False(t, isToast)
	})
}

func TestCharEC2_ManagePicker(t *testing.T) {
	t.Run("stopped", func(t *testing.T) {
		v, _ := charEC2Loaded(t, nil, charEC2Instances()...)
		v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		assert.Equal(t, msg.RequestActionPickerMsg{Title: "Manage Instance", Options: []string{"Start"}}, charEC2Picker(t, v))
	})
	t.Run("running", func(t *testing.T) {
		v, _ := charEC2Loaded(t, nil, charEC2Instances()...)
		assert.Equal(t, msg.RequestActionPickerMsg{Title: "Manage Instance", Options: []string{"Stop", "Reboot", "Terminate"}}, charEC2Picker(t, v))
	})
	t.Run("two running selected, cursor elsewhere", func(t *testing.T) {
		v, _ := charEC2Loaded(t, nil, charEC2Instances()...)
		v.Update(keyPress(' '))
		v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		v.Update(keyPress(' '))
		v.Update(tea.KeyPressMsg{Code: tea.KeyUp})
		assert.Equal(t, msg.RequestActionPickerMsg{Title: "Manage 2 Instances", Options: []string{"Stop", "Reboot", "Terminate"}}, charEC2Picker(t, v))
	})
	t.Run("mixed", func(t *testing.T) {
		v, _ := charEC2Loaded(t, nil, charEC2Instances()...)
		v.Update(keyPress(' '))
		v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		v.Update(keyPress(' '))
		_, cmd := v.Update(keyPress('m'))
		assert.Equal(t, msg.ToastError("No common actions for selected instances"), charEC2OneToast(t, cmd))
	})
	t.Run("transitional state", func(t *testing.T) {
		v, _ := charEC2Loaded(t, nil, aws.Instance{ID: "i-p", State: "pending"})
		_, cmd := v.Update(keyPress('m'))
		assert.Equal(t, msg.ToastError("No common actions for selected instances"), charEC2OneToast(t, cmd))
	})
	t.Run("no rows", func(t *testing.T) {
		v, _ := charEC2Loaded(t, nil)
		_, cmd := v.Update(keyPress('m'))
		assert.Nil(t, cmd)
	})
}

func TestCharEC2_ConfirmRequests(t *testing.T) {
	cases := []struct {
		action, selectIDs string
		want              msg.RequestConfirmMsg
	}{
		{"Start", "i-2", msg.RequestConfirmMsg{Message: "Start 1 instance(s)?\n  db (i-2)\n", Action: "ec2_start", Quick: true}},
		{"Stop", "i-1", msg.RequestConfirmMsg{Message: "Stop 1 instance(s)?\n  web (i-1)\n", Action: "ec2_stop"}},
		// i-3 has no name, so the prompt shows its ID.
		{"Reboot", "i-1,i-3", msg.RequestConfirmMsg{Message: "Reboot 2 instance(s)?\n  web (i-1)\n  i-3\n", Action: "ec2_reboot"}},
		{"Terminate", "i-1,i-3", msg.RequestConfirmMsg{Message: "Terminate 2 instance(s)?\n  web (i-1)\n  i-3\n", Action: "ec2_terminate"}},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			v, _ := charEC2Loaded(t, nil, charEC2Instances()...)
			for id := range strings.SplitSeq(tc.selectIDs, ",") {
				v.Update(msg.FocusResourceMsg{ResourceID: id}) // moves the cursor; its detail cmd is not run
				v.Update(keyPress(' '))
			}
			charEC2Picker(t, v)
			// Divergence #24 (fixed in PR 2): the message lists the names.
			assert.Equal(t, tc.want, charEC2Confirm(t, v, tc.action))
		})
	}
}

// The targets are taken at m. A later change of the selection or the cursor
// does not change them.
func TestCharEC2_TargetsFrozenAtManage(t *testing.T) {
	v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
	v.Update(keyPress(' ')) // select i-1
	charEC2Picker(t, v)
	v.Update(keyPress(' ')) // deselect
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	charEC2Confirm(t, v, "Stop")

	svc.On("StopInstances", mock.Anything, []string{"i-1"}).Return(nil).Once()
	_, cmd := v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "ec2_stop"})
	collectMsgsTimed(cmd, maxWait)
	svc.AssertExpectations(t)
}

// A decline clears the targets. A confirm that comes after the decline then
// does nothing.
func TestCharEC2_DeclineClearsTargets(t *testing.T) {
	v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
	charEC2Picker(t, v)
	charEC2Confirm(t, v, "Stop")
	_, cmd := v.Update(ui.ConfirmResultMsg{Confirmed: false, Action: "ec2_stop"})
	assert.Nil(t, cmd)
	assert.Equal(t, "3/3 instances", plain(v.Footer()))
	assert.Contains(t, charEC2Text(v), "● running", "no optimistic state")

	// Divergence #25 (fixed in PR 2).
	_, cmd = v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "ec2_stop"})
	assert.Nil(t, cmd)
	assert.Equal(t, "3/3 instances", plain(v.Footer()))
	svc.AssertNotCalled(t, "StopInstances", mock.Anything, mock.Anything)
}

// The confirm Action is checked against the pending action. A reply for
// another action clears the targets and runs nothing.
func TestCharEC2_ConfirmActionChecked(t *testing.T) {
	t.Run("other ec2 action", func(t *testing.T) {
		v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
		charEC2Picker(t, v)
		charEC2Confirm(t, v, "Stop")
		_, cmd := v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "ec2_terminate"})
		// Divergence #25 (fixed in PR 2).
		assert.Nil(t, cmd)
		assert.Equal(t, "3/3 instances", plain(v.Footer()))
		assert.Contains(t, charEC2Lines(v)[2], "● running")
		svc.AssertNotCalled(t, "TerminateInstances", mock.Anything, mock.Anything)
		svc.AssertNotCalled(t, "StopInstances", mock.Anything, mock.Anything)
	})
	t.Run("unknown action", func(t *testing.T) {
		v, _ := charEC2Loaded(t, nil, charEC2Instances()...)
		charEC2Picker(t, v)
		charEC2Confirm(t, v, "Stop")
		_, cmd := v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "delete_bucket"})
		assert.Nil(t, cmd)
		// Divergence #25 and #26 (fixed in PR 2): no spinner.
		assert.Equal(t, "3/3 instances", plain(v.Footer()))
		assert.Contains(t, charEC2Lines(v)[2], "● running")
	})
}

func TestCharEC2_OptimisticStatesAndSpinner(t *testing.T) {
	cases := []struct {
		action, id, method, state, footer string
	}{
		{"Start", "i-2", "StartInstances", "◌ pending", "3/3 instances  ⣾  Starting 1 instance(s)..."},
		// Divergence #26 (fixed in PR 2): "Stopping" and "Terminating".
		{"Stop", "i-1", "StopInstances", "◌ stopping", "3/3 instances  ⣾  Stopping 1 instance(s)..."},
		// Divergence #49 (keep): Reboot shows "pending".
		{"Reboot", "i-1", "RebootInstances", "◌ pending", "3/3 instances  ⣾  Rebooting 1 instance(s)..."},
		{"Terminate", "i-1", "TerminateInstances", "◌ shutting-down", "3/3 instances  ⣾  Terminating 1 instance(s)..."},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
			v.Update(msg.FocusResourceMsg{ResourceID: tc.id})
			charEC2Picker(t, v)
			c := charEC2Confirm(t, v, tc.action)
			svc.On(tc.method, mock.Anything, []string{tc.id}).Return(nil).Once()
			_, cmd := v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: c.Action})
			assert.Equal(t, tc.footer, plain(v.Footer()))
			var row string
			for _, l := range charEC2Lines(v) {
				if strings.HasPrefix(l, " "+tc.id+" ") {
					row = l
				}
			}
			assert.Contains(t, row, tc.state)
			collectMsgsTimed(cmd, maxWait)
			svc.AssertExpectations(t)
		})
	}
}

// Mutations use a detached context: leaving the view (its context is
// cancelled) does not stop them.
func TestCharEC2_MutationDetachedAndOutcome(t *testing.T) {
	v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
	ctx, cancel := context.WithCancel(context.Background())
	v.SetContext(ctx)
	charEC2Picker(t, v)
	charEC2Confirm(t, v, "Stop")
	_, cmd := v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "ec2_stop"})
	cancel()

	svc.On("StopInstances", mock.MatchedBy(func(c context.Context) bool {
		_, hasDeadline := c.Deadline()
		return c.Err() == nil && hasDeadline
	}), []string{"i-1"}).Return(nil).Once()
	res := charEC2NoTicks(collectMsgsTimed(cmd, maxWait))
	svc.AssertExpectations(t)
	require.Len(t, res, 1)
	o, ok := res[0].(msg.Outcome)
	require.True(t, ok)
	assert.Equal(t, msg.ToastSuccess("Instance stopped: i-1"), o.Outcome())
}

func TestCharEC2_OutcomeTexts(t *testing.T) {
	v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
	v.Update(keyPress(' '))
	v.Update(msg.FocusResourceMsg{ResourceID: "i-3"})
	v.Update(keyPress(' '))
	charEC2Picker(t, v)
	charEC2Confirm(t, v, "Terminate")
	_, cmd := v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "ec2_terminate"})
	svc.On("TerminateInstances", mock.Anything, []string{"i-1", "i-3"}).
		Return(&smithy.GenericAPIError{Code: "OperationNotPermitted", Message: "protected"}).Once()
	res := charEC2NoTicks(collectMsgsTimed(cmd, maxWait))
	require.Len(t, res, 1)
	o, ok := res[0].(msg.Outcome)
	require.True(t, ok)
	assert.Equal(t, msg.ToastError("Terminate failed for i-1, i-3: OperationNotPermitted"), o.Outcome())
}

// A failed mutation deselects, refreshes the targets, and shows a toast.
func TestCharEC2_MutationFailureRefreshesTargets(t *testing.T) {
	v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
	v.Update(keyPress(' '))
	v.Update(msg.FocusResourceMsg{ResourceID: "i-3"})
	v.Update(keyPress(' '))
	charEC2Picker(t, v)
	charEC2Confirm(t, v, "Stop")
	svc.On("StopInstances", mock.Anything, []string{"i-1", "i-3"}).
		Return(&smithy.GenericAPIError{Code: "UnauthorizedOperation", Message: "no"}).Once()
	svc.On("RefreshInstances", mock.Anything, []string{"i-1", "i-3"}).
		Return([]aws.Instance{{ID: "i-1", State: "stopping"}, {ID: "i-3", State: "running"}}, nil).Once()

	_, cmd := v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "ec2_stop"})
	require.Equal(t, "3/3 instances  (2 selected)  ⣾  Stopping 2 instance(s)...", plain(v.Footer()))
	mutated := charEC2NoTicks(collectMsgsTimed(cmd, maxWait))
	require.Len(t, mutated, 1)
	_, cmd = v.Update(mutated[0])
	assert.Equal(t, "3/3 instances  ⣾  Refreshing 2 instances...", plain(v.Footer()), "deselected")

	seen := pump(v, cmd, 3)
	assert.Equal(t, []msg.ToastMsg{msg.ToastError(
		"Stop failed: UnauthorizedOperation. The list shows the current state of 2 instance(s).")}, toasts(seen))
	svc.AssertExpectations(t)
	assert.Equal(t, "3/3 instances", plain(v.Footer()))
	lines := charEC2Lines(v)
	assert.Contains(t, lines[2], "◌ stopping")
	assert.Contains(t, lines[4], "● running")
	// The refresh result replaces the whole instance: i-1 loses its name.
	assert.NotContains(t, lines[2], "web")
}

// A good mutation deselects, shows the Outcome toast, and reloads after 2s.
// That reload does not set loading, so the table stays and a focus opens at
// once.
func TestCharEC2_MutationSuccessDelayedReload(t *testing.T) {
	v, svc := charEC2Loaded(t, &aws.Client{}, charEC2Instances()...)
	v.Update(keyPress(' '))
	charEC2Picker(t, v)
	charEC2Confirm(t, v, "Stop")
	svc.On("StopInstances", mock.Anything, []string{"i-1"}).Return(nil).Once()
	_, cmd := v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "ec2_stop"})
	require.Equal(t, "3/3 instances  (1 selected)  ⣾  Stopping 1 instance(s)...", plain(v.Footer()))
	mutated := charEC2NoTicks(collectMsgsTimed(cmd, maxWait))
	require.Len(t, mutated, 1)

	_, cmd = v.Update(mutated[0])
	assert.Equal(t, "3/3 instances  ⣾  Waiting for state change...", plain(v.Footer()), "deselected")
	got := charEC2NoTicks(collectMsgsTimed(cmd, maxWait))
	// The 2s reload tick is skipped by collectMsgsTimed; only the toast is left.
	assert.Equal(t, []tea.Msg{msg.ToastSuccess("Instance stopped: i-1")}, got)

	// The delayed reload itself (private message: its tick takes 2s).
	charEC2ExpectPage(svc, charEC2Instances()...)
	_, reload := v.Update(ec2DelayedRefreshMsg{}) // private
	assert.Equal(t, "3/3 instances  ⣾  Refreshing instances...", plain(v.Footer()))
	assert.Contains(t, charEC2Text(v), "◌ stopping", "the table stays during the delayed reload")

	// Divergence #7 (fixed in PR 7): the instances stay during the reload, so
	// o finds the cursor instance. It is stopping, by the state guess.
	_, ocmd := v.Update(keyPress('o'))
	assert.Equal(t, msg.ToastError("Instance is stopping — must be running for SSM"), charEC2OneToast(t, ocmd))
	// Divergence #7 (fixed in PR 7): m works on the real rows. The cursor
	// moves to i-2, which is stopped.
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	assert.Equal(t, []string{"Start"}, charEC2Picker(t, v).Options)

	// Divergence #53 (keep): focus opens at once.
	svc.On("GetInstanceDetail", mock.Anything, "i-2").Return(&aws.InstanceDetail{InstanceID: "i-2"}, nil).Once()
	_, fcmd := v.Update(msg.FocusResourceMsg{ResourceID: "i-2"})
	require.NotNil(t, fcmd)
	_, ok := firstOfType[msg.TabbedContentMsg](pump(v, fcmd, 2))
	assert.True(t, ok)

	pump(v, reload, 5)
	assert.Equal(t, "3/3 instances", plain(v.Footer()))
	assert.Contains(t, charEC2Lines(v)[2], "● running", "the reload replaces the optimistic state")
	svc.AssertExpectations(t)
}

func TestCharEC2_SSMSessionEnd(t *testing.T) {
	t.Run("error", func(t *testing.T) {
		v, _ := charEC2Loaded(t, nil, charEC2Instances()...)
		_, cmd := v.Update(ec2SSMSessionFinishedMsg{instanceID: "i-1", err: errors.New("exit status 255")}) // private
		assert.Equal(t, msg.ToastError("SSM session failed: exit status 255"), charEC2OneToast(t, cmd))
		assert.Equal(t, "3/3 instances", plain(v.Footer()), "no reload")
	})
	for _, tc := range []struct{ name, id, label string }{
		{"web", "i-1", "web"},
		{"", "i-3", "i-3"},
	} {
		t.Run("ok "+tc.label, func(t *testing.T) {
			v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
			v.Update(keyPress(' ')) // select i-1
			charEC2ExpectPage(svc, charEC2Instances()...)
			_, cmd := v.Update(ec2SSMSessionFinishedMsg{instanceID: tc.id, instanceName: tc.name}) // private
			// Divergence #7 (fixed in PR 7): the rows stay. Divergence #53
			// (keep): this reload sets loading, unlike the delayed one.
			assert.True(t, v.loading) // private
			assert.Contains(t, charEC2Text(v), "i-1")
			assert.Equal(t, "3/3 instances  (1 selected)  ⣾  Refreshing instances...", plain(v.Footer()))
			seen := pump(v, cmd, 5)
			assert.Equal(t, []msg.ToastMsg{msg.ToastSuccess("Session ended: " + tc.label)}, toasts(seen))
			svc.AssertNumberOfCalls(t, "ListInstancesPage", 2)
			assert.Equal(t, "3/3 instances  (1 selected)", plain(v.Footer()), "the selection survives the reload")
		})
	}
}

func TestCharEC2_DetailTabs(t *testing.T) {
	v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
	svc.On("GetInstanceDetail", mock.Anything, "i-1").Return(charEC2Detail(), nil)
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, keyPress('d')} {
		_, cmd := v.Update(k)
		panel, ok := firstOfType[msg.TabbedContentMsg](pump(v, cmd, 3))
		require.True(t, ok, k.String())
		assert.Equal(t, "web (i-1)", panel.PanelTitle)
		require.Len(t, panel.Tabs, 4)

		info := panel.Tabs[0]
		assert.Equal(t, "Info", info.Title)
		assert.Equal(t, "text", info.Format)
		assert.Equal(t, `Instance ID      i-1
Name             web
State            running
Type             t3.micro
Platform         Linux/UNIX
Architecture     x86_64
Private IP       10.0.0.1
Public IP        1.2.3.4
Private DNS      ip-10-0-0-1.ec2.internal
Public DNS       ec2-1-2-3-4.compute-1.amazonaws.com
VPC              vpc-1
Subnet           subnet-1
AZ               us-east-1a
Key Name         key
AMI              ami-1
IAM Role         role
Launch Time      2024-01-02T03:04:05Z
Root Device      ebs (/dev/xvda)
`, info.Content)
		assert.Equal(t, []msg.TabLink{
			{Line: 10, ViewID: "vpc_list", Params: map[string]string{"focus": "vpc-1"}},
			{Line: 11, ViewID: "subnet_list", Params: map[string]string{"focus": "subnet-1"}},
			{Line: 14, ViewID: "ami_list", Params: map[string]string{"focus": "ami-1"}},
		}, info.Links)

		assert.Equal(t, "JSON", panel.Tabs[1].Title)
		assert.Equal(t, "json", panel.Tabs[1].Format)
		assert.Equal(t, charEC2Detail().DetailJSON(), panel.Tabs[1].Content)
		assert.Contains(t, panel.Tabs[1].Content, `"instance_id": "i-1"`)
		assert.Empty(t, panel.Tabs[1].Links)

		assert.Equal(t, msg.TabContent{
			Title: "Security Groups", Format: "text",
			Content: "sg-1                   web-sg\nsg-2                   ssh\n",
			Links: []msg.TabLink{
				{Line: 0, ViewID: "sg_list", Params: map[string]string{"focus": "sg-1"}},
				{Line: 1, ViewID: "sg_list", Params: map[string]string{"focus": "sg-2"}},
			},
		}, panel.Tabs[2])
		assert.Equal(t, msg.TabContent{
			Title: "Tags", Format: "text",
			Content: "Env                      prod\nName                     web\n",
		}, panel.Tabs[3])
	}
}

func TestCharEC2_DetailMinimal(t *testing.T) {
	v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
	svc.On("GetInstanceDetail", mock.Anything, "i-1").
		Return(&aws.InstanceDetail{InstanceID: "i-1", State: "running"}, nil).Once()
	_, cmd := v.Update(keyPress('d'))
	panel, ok := firstOfType[msg.TabbedContentMsg](pump(v, cmd, 3))
	require.True(t, ok)
	assert.Equal(t, "i-1", panel.PanelTitle)
	require.Len(t, panel.Tabs, 2)
	assert.Equal(t, "Instance ID      i-1\nState            running\n", panel.Tabs[0].Content,
		"empty fields and an empty Root Device are left out")
	assert.Empty(t, panel.Tabs[0].Links)
	assert.Equal(t, "JSON", panel.Tabs[1].Title)
}

func TestCharEC2_DetailError(t *testing.T) {
	v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
	svc.On("GetInstanceDetail", mock.Anything, "i-1").Return(nil, errors.New("denied")).Once()
	_, cmd := v.Update(keyPress('d'))
	seen := pump(v, cmd, 3)
	// Divergence #19 (fixed in PR 3): a toast, and the list stays.
	assert.Equal(t, []msg.ToastMsg{msg.ToastError("Details failed: denied")}, toasts(seen))
	assert.Contains(t, charEC2Text(v), " i-1 ", "the list stays")
	assert.Equal(t, "3/3 instances", plain(v.Footer()))

	// A nil detail with no error does nothing.
	v2, svc2 := charEC2Loaded(t, nil, charEC2Instances()...)
	svc2.On("GetInstanceDetail", mock.Anything, "i-1").Return(nil, nil).Once()
	_, cmd = v2.Update(keyPress('d'))
	seen = pump(v2, cmd, 3)
	require.Len(t, seen, 1, "only the detail reply itself")
	_, isPanel := seen[0].(msg.TabbedContentMsg)
	assert.False(t, isPanel)
	assert.Contains(t, charEC2Text(v2), " i-1 ", "the list stays")
	assert.Equal(t, "3/3 instances", plain(v2.Footer()))
}

// Divergence #48 (keep): details with no cursor row fall through to the
// table and call nothing.
func TestCharEC2_DetailWithNoRows(t *testing.T) {
	v, svc := charEC2Loaded(t, nil)
	_, cmd := v.Update(keyPress('d'))
	assert.Empty(t, charEC2NoTicks(collectMsgs(cmd)))
	svc.AssertNotCalled(t, "GetInstanceDetail", mock.Anything, mock.Anything)
}

func TestCharEC2_CopyToast(t *testing.T) {
	v, _ := charEC2Loaded(t, nil, charEC2Instances()...)
	_, cmd := v.Update(keyPress('y'))
	got := collectMsgs(cmd)
	assert.Equal(t, []tea.Msg{tea.SetClipboard("i-1")(), msg.ToastSuccess("Copied 1 instance ID(s)")}, got)

	v.Update(keyPress(' '))
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	v.Update(keyPress(' '))
	_, cmd = v.Update(keyPress('y'))
	got = collectMsgs(cmd)
	assert.Equal(t, []tea.Msg{tea.SetClipboard("i-1\ni-3")(), msg.ToastSuccess("Copied 2 instance ID(s)")}, got)

	empty, _ := charEC2Loaded(t, nil)
	_, cmd = empty.Update(keyPress('y'))
	assert.Nil(t, cmd)
}

func TestCharEC2_Esc(t *testing.T) {
	v, _ := charEC2Loaded(t, nil, charEC2Instances()...)
	v.Update(keyPress(' '))
	require.Equal(t, "3/3 instances  (1 selected)", plain(v.Footer()))
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	// Divergence #1: esc deselects first (MultiSelect keeps it).
	assert.Nil(t, cmd)
	assert.Equal(t, "3/3 instances", plain(v.Footer()))

	_, cmd = v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Equal(t, []tea.Msg{msg.NavigateBackMsg{}}, collectMsgs(cmd))
}

func TestCharEC2_RefreshKey(t *testing.T) {
	t.Run("full", func(t *testing.T) {
		v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
		charEC2ExpectPage(svc, charEC2Instances()[:2]...)
		_, cmd := v.Update(keyPress('r'))
		// Divergence #7 (fixed in PR 7): the rows stay.
		assert.Contains(t, charEC2Text(v), "i-1")
		assert.Equal(t, "3/3 instances  ⣾  Loading EC2 instances...", plain(v.Footer()))
		pump(v, cmd, 5)
		svc.AssertNumberOfCalls(t, "ListInstancesPage", 2)
		svc.AssertNotCalled(t, "RefreshInstances", mock.Anything, mock.Anything)
		assert.Equal(t, "2/2 instances", plain(v.Footer()))
	})
	t.Run("selective", func(t *testing.T) {
		v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
		v.Update(keyPress(' '))
		v.Update(msg.FocusResourceMsg{ResourceID: "i-2"})
		v.Update(keyPress(' '))
		svc.On("RefreshInstances", mock.Anything, []string{"i-1", "i-2"}).
			Return([]aws.Instance{{ID: "i-2", Name: "db", State: "pending"}}, nil).Once()
		_, cmd := v.Update(keyPress('r'))
		assert.Equal(t, "3/3 instances  (2 selected)  ⣾  Refreshing 2 instances...", plain(v.Footer()))
		assert.Contains(t, charEC2Text(v), "i-1", "not loading: the table stays")
		seen := pump(v, cmd, 3)
		assert.Empty(t, toasts(seen), "no toast on success")
		svc.AssertNumberOfCalls(t, "ListInstancesPage", 1)
		assert.Equal(t, "3/3 instances  (2 selected)", plain(v.Footer()))
		lines := charEC2Lines(v)
		assert.Contains(t, lines[3], "◌ pending")
		// Divergence #50 (keep): i-1 is not in the reply and stays as it was.
		assert.Contains(t, lines[2], "● running")
	})
	t.Run("selective new ID ignored", func(t *testing.T) {
		v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
		v.Update(keyPress(' '))
		svc.On("RefreshInstances", mock.Anything, []string{"i-1"}).
			Return([]aws.Instance{{ID: "i-new", State: "running"}}, nil).Once()
		_, cmd := v.Update(keyPress('r'))
		pump(v, cmd, 3)
		// Divergence #50 (keep).
		assert.Equal(t, "3/3 instances  (1 selected)", plain(v.Footer()))
	})
	t.Run("selective error", func(t *testing.T) {
		v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
		v.Update(keyPress(' '))
		svc.On("RefreshInstances", mock.Anything, []string{"i-1"}).
			Return([]aws.Instance(nil), errors.New("throttled")).Once()
		_, cmd := v.Update(keyPress('r'))
		seen := pump(v, cmd, 3)
		assert.Equal(t, []msg.ToastMsg{msg.ToastError("Refresh failed: throttled")}, toasts(seen))
		assert.Equal(t, "3/3 instances  (1 selected)", plain(v.Footer()))
	})
}

// Divergence #10 (fixed in PR 7): a selective refresh reply from before a
// full load is dropped, and the spinner of the load stays.
func TestCharEC2_RefreshReplyDuringFullLoad(t *testing.T) {
	v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
	v.Update(keyPress(' '))
	svc.On("RefreshInstances", mock.Anything, []string{"i-1"}).
		Return([]aws.Instance{{ID: "i-1", Name: "stale", State: "running"}}, nil).Once()
	_, sel := v.Update(keyPress('r'))
	reply := charEC2NoTicks(collectMsgs(sel))
	require.Len(t, reply, 1)

	v.Update(tea.KeyPressMsg{Code: tea.KeyEscape}) // deselect
	charEC2ExpectPage(svc, charEC2Instances()...)
	_, full := v.Update(keyPress('r'))
	require.Equal(t, "3/3 instances  ⣾  Loading EC2 instances...", plain(v.Footer()))

	deliverAll(v, reply...)
	assert.Equal(t, "3/3 instances  ⣾  Loading EC2 instances...", plain(v.Footer()))
	assert.NotContains(t, charEC2Text(v), "stale")

	pump(v, full, 5)
	assert.Equal(t, "3/3 instances", plain(v.Footer()))

	// The same old reply after the full load ended must not overwrite the
	// fresh rows either.
	deliverAll(v, reply...)
	assert.Equal(t, "3/3 instances", plain(v.Footer()))
	assert.NotContains(t, charEC2Text(v), "stale")
}

// o and m are letters while the filter is open.
func TestCharEC2_FilterTakesManageAndConnectKeys(t *testing.T) {
	v, _ := charEC2Loaded(t, nil, charEC2Instances()...)
	v.Update(keyPress('/'))

	for _, r := range "mo" {
		_, cmd := v.Update(keyPress(r))
		got := charEC2NoTicks(collectMsgs(cmd))
		assert.Empty(t, toasts(got), string(r))
		_, picker := firstOfType[msg.RequestActionPickerMsg](got)
		assert.False(t, picker, string(r))
		deliverAll(v, got...)
	}

	assert.True(t, v.CapturingInput())
	assert.Contains(t, charEC2Lines(v)[0], "/ mo")
	assert.Equal(t, "0/3 instances", plain(v.Footer()))
}

// The error screen hides the rows, so o and m must not act on them. The
// rows stay in memory since the items are kept during a reload.
func TestCharEC2_ErrorScreenBlocksManageAndConnect(t *testing.T) {
	v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
	svc.On("ListInstancesPage", mock.Anything, (*string)(nil)).Return(nil, errors.New("throttled")).Once()
	_, cmd := v.Update(keyPress('r'))
	pump(v, cmd, 5)
	require.Equal(t, "  Error: throttled", charEC2Text(v))

	for _, r := range "mo" {
		_, cmd := v.Update(keyPress(r))
		got := charEC2NoTicks(collectMsgs(cmd))
		assert.Empty(t, toasts(got), string(r))
		_, picker := firstOfType[msg.RequestActionPickerMsg](got)
		assert.False(t, picker, string(r))
	}
	assert.Empty(t, v.pending.keys, "no targets are held")

	// A good load brings the keys back.
	charEC2ExpectPage(svc, charEC2Instances()...)
	_, cmd = v.Update(keyPress('r'))
	pump(v, cmd, 5)
	assert.Equal(t, []string{"Stop", "Reboot", "Terminate"}, charEC2Picker(t, v).Options)
}

// Divergence #11 (fixed in PR 7): the spinner ticks whenever it shows, also
// during a selective refresh.
func TestCharEC2_SpinnerTicks(t *testing.T) {
	v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
	v.Update(keyPress(' '))
	svc.On("RefreshInstances", mock.Anything, mock.Anything).Return([]aws.Instance{}, nil).Maybe()
	_, cmd := v.Update(keyPress('r'))
	var tick tea.Msg
	for _, m := range collectMsgs(cmd) {
		if _, ok := m.(spinner.TickMsg); ok {
			tick = m
		}
	}
	require.NotNil(t, tick)
	_, next := v.Update(tick)
	_, ticked := firstOfType[spinner.TickMsg](collectMsgsTimed(next, maxWait))
	assert.True(t, ticked)

	// While loading with no rows, a tick schedules the next tick.
	v2, svc2 := newCharEC2(t, nil)
	svc2.On("ListInstancesPage", mock.Anything, mock.Anything).Return(&aws.InstancePage{}, nil).Maybe()
	first, ok := firstOfType[spinner.TickMsg](collectMsgs(v2.Init()))
	require.True(t, ok)
	_, next = v2.Update(first)
	require.NotNil(t, next)
	_, ticked = firstOfType[spinner.TickMsg](collectMsgs(next))
	assert.True(t, ticked)
}

// Keys during a load. With no rows yet (first load), keys that the view does
// not handle go only to the spinner, and the action keys find no target.
func TestCharEC2_NavKeysDuringFirstLoad(t *testing.T) {
	v, svc := newCharEC2(t, nil)
	charEC2ExpectPage(svc, charEC2Instances()...)
	page := charEC2NoTicks(collectMsgs(v.Init()))
	for _, k := range []tea.KeyPressMsg{
		{Code: tea.KeyDown}, {Code: tea.KeyEnter}, keyPress('y'), keyPress('m'), keyPress(' '), keyPress('d'),
	} {
		_, cmd := v.Update(k)
		assert.Nil(t, cmd, k.String())
	}
	assert.Equal(t, "0/0 instances  ⣾  Loading EC2 instances...", plain(v.Footer()))
	assert.Equal(t, "  ⣾  Loading EC2 instances...", charEC2Text(v))
	svc.AssertNotCalled(t, "GetInstanceDetail", mock.Anything, mock.Anything)

	deliverAll(v, page...)
	_, cp := v.Update(keyPress('y'))
	assert.Contains(t, collectMsgs(cp), tea.SetClipboard("i-1")(), "the down key before the rows was lost")
}

// Divergence #7 (fixed in PR 7): the rows and the instances stay during a
// full reload, and the table takes keys.
func TestCharEC2_NavKeysDuringReload(t *testing.T) {
	v, svc := charEC2Loaded(t, nil, charEC2Instances()...)
	charEC2ExpectPage(svc, charEC2Instances()...)
	_, reload := v.Update(keyPress('r'))

	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, cp := v.Update(keyPress('y'))
	assert.Equal(t, []tea.Msg{tea.SetClipboard("i-2")(), msg.ToastSuccess("Copied 1 instance ID(s)")}, collectMsgs(cp),
		"y copies the row the cursor moved to")
	assert.Equal(t, []string{"Start"}, charEC2Picker(t, v).Options, "m sees the instance")
	v.Update(keyPress(' '))
	assert.Equal(t, "3/3 instances  (1 selected)  ⣾  Loading EC2 instances...", plain(v.Footer()))

	pump(v, reload, 5)
	assert.Equal(t, "3/3 instances  (1 selected)", plain(v.Footer()))
	v.Update(tea.KeyPressMsg{Code: tea.KeyEscape}) // deselect, so y uses the cursor
	_, cp = v.Update(keyPress('y'))
	assert.Contains(t, collectMsgs(cp), tea.SetClipboard("i-2")(), "the cursor stayed on i-2")
}
