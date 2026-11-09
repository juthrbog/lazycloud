package views

import (
	"context"
	"errors"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// PR 0 characterization tests for sg_list. They record today's behavior.
// Only the sg port PR (PR 4) may edit this file.

var charSGWeb = aws.SecurityGroup{
	ID: "sg-111", Name: "web-sg", ARN: "arn:aws:ec2:us-east-1:123456789012:security-group/sg-111",
	Description: "Web servers", VpcID: "vpc-aaa", OwnerID: "123456789012",
	Tags: map[string]string{"Name": "web-sg", "env": "prod"},
	InboundRules: []aws.SecurityGroupRule{
		{Protocol: "tcp", FromPort: 80, ToPort: 80, CIDRs: []string{"0.0.0.0/0"}, Description: "HTTP"},
		{Protocol: "tcp", FromPort: 443, ToPort: 443, CIDRs: []string{"0.0.0.0/0"}},
	},
	OutboundRules: []aws.SecurityGroupRule{
		{Protocol: "-1", FromPort: -1, ToPort: -1, CIDRs: []string{"0.0.0.0/0"}},
	},
}

var charSGDB = aws.SecurityGroup{
	ID: "sg-222", Name: "db-sg", Description: "Database", VpcID: "vpc-aaa",
	InboundRules: []aws.SecurityGroupRule{
		{Protocol: "tcp", FromPort: 5432, ToPort: 5432, SGRefs: []string{"sg-111"}},
	},
}

// newCharSG returns a security group list at width 160 with a mock that
// returns groups, and the produced load already delivered.
func newCharSG(t *testing.T, groups ...aws.SecurityGroup) (*SGList, *awstest.MockEC2Service) {
	t.Helper()
	svc := new(awstest.MockEC2Service)
	svc.On("ListSecurityGroups", mock.Anything).Return(groups, nil)
	v := NewSGList(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})
	pump(v, v.Init(), 3)
	return v, svc
}

func TestCharSG_IDAndTitle(t *testing.T) {
	v := NewSGList(nil)
	assert.Equal(t, "sg_list", v.ID())
	assert.Equal(t, "Security Groups", v.Title())
}

func TestCharSG_KeyMap(t *testing.T) {
	assert.Equal(t, []string{
		`enter | enter/d | details | mode=0 | cat=""`,
		`y | y | copy ID | mode=0 | cat=""`,
		`s | s/S | sort | mode=0 | cat=""`,
		`/ | / | filter | mode=0 | cat=""`,
		`r | r | refresh | mode=0 | cat=""`,
	}, netKeyMap(NewSGList(nil).KeyMap()))
}

func TestCharSG_CapturingInputWhileFilterOpen(t *testing.T) {
	v, _ := newCharSG(t, charSGWeb, charSGDB)
	assert.False(t, v.CapturingInput())

	v.Update(keyPress('/'))
	assert.True(t, v.CapturingInput())
	assert.Contains(t, netView(v), "/ filter...")

	// Typed text goes to the filter (d is not details here), and its
	// FilterChangedMsg filters.
	first := netType(v, keyPress('d'))
	assert.Equal(t, []tea.Msg{ui.FilterChangedMsg{Text: "d"}}, first, "no panel, only the filter text")
	assert.Equal(t, []tea.Msg{ui.FilterChangedMsg{Text: "db"}}, netType(v, keyPress('b')))
	v.Update(ui.FilterChangedMsg{Text: "db"})
	assert.Equal(t, "1/2 security groups", netFooter(v))

	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.False(t, v.CapturingInput())
	assert.Equal(t, []tea.Msg{ui.FilterChangedMsg{Text: ""}}, collectMsgs(cmd), "esc closes the filter, not the view")
	v.Update(ui.FilterChangedMsg{Text: ""})
	assert.Equal(t, "2/2 security groups", netFooter(v))

	// enter closes the filter and keeps the text.
	v.Update(keyPress('/'))
	netType(v, keyPress('w'))
	v.Update(ui.FilterChangedMsg{Text: "w"})
	_, cmd = v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Nil(t, cmd)
	assert.False(t, v.CapturingInput())
	assert.Equal(t, "1/2 security groups", netFooter(v))
	assert.Equal(t, "Group ID | Name | VPC | Description | In | Out", netHeader(v), "the filter bar is gone")
	assert.Equal(t, []string{"sg-111 | web-sg | vpc-aaa | Web servers | 2 | 1"}, netRows(v))
}

func TestCharSG_InitLoadsOnlyWhileLoading(t *testing.T) {
	v, svc := newCharSG(t, charSGWeb)
	svc.AssertNumberOfCalls(t, "ListSecurityGroups", 1)
	assert.Nil(t, v.Init(), "a cached view does not reload")
	svc.AssertNumberOfCalls(t, "ListSecurityGroups", 1)
}

func TestCharSG_FirstLoadViewAndFooter(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListSecurityGroups", mock.Anything).Return([]aws.SecurityGroup{charSGWeb, charSGDB}, nil)
	v := NewSGList(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})

	assert.Equal(t, "\n  ⣾  Loading security groups...", netView(v))
	assert.Equal(t, "0/0 security groups  ⣾  Loading security groups...", netFooter(v))

	pump(v, v.Init(), 5)
	assert.Equal(t, "Group ID | Name | VPC | Description | In | Out", netHeader(v))
	assert.Equal(t, []string{
		"sg-111 | web-sg | vpc-aaa | Web servers | 2 | 1",
		"sg-222 | db-sg | vpc-aaa | Database | 1 | 0",
	}, netRows(v))
	assert.Equal(t, "2/2 security groups", netFooter(v))
}

// A load message built by hand is accepted on a fresh view (sg has no gen).
func TestCharSG_LiteralLoadAcceptedOnFreshView(t *testing.T) {
	v := NewSGList(new(awstest.MockEC2Service))
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})
	v.Update(sgListLoadedMsg{groups: []aws.SecurityGroup{charSGWeb}}) // private
	assert.Equal(t, "1/1 security groups", netFooter(v))
}

func TestCharSG_ColumnsPerWidth(t *testing.T) {
	cases := map[int]string{
		60:  "Group ID | Name | VPC | In | Out",
		100: "Group ID | Name | VPC | In | Out",
		160: "Group ID | Name | VPC | Description | In | Out",
		200: "Group ID | Name | VPC | Description | In | Out",
	}
	for w, want := range cases {
		v, _ := newCharSG(t, charSGWeb)
		v.Update(tea.WindowSizeMsg{Width: w, Height: 12})
		assert.Equal(t, want, netHeader(v), "width %d", w)
	}
	assert.Equal(t, []ui.Column{
		{Title: "Group ID", Width: 21, Weight: 1, MaxWidth: 35},
		{Title: "Name", Width: 24, Weight: 2, MaxWidth: 60},
		{Title: "VPC", Width: 21, Weight: 1, MaxWidth: 35},
		{Title: "In", Width: 4},
		{Title: "Out", Width: 4},
	}, sgColumns(ui.TierNarrow)) // private
	assert.Equal(t, []ui.Column{
		{Title: "Group ID", Width: 21, Weight: 1, MaxWidth: 35},
		{Title: "Name", Width: 24, Weight: 2, MaxWidth: 60},
		{Title: "VPC", Width: 21, Weight: 1, MaxWidth: 35},
		{Title: "Description", Width: 30, Weight: 2, MaxWidth: 60},
		{Title: "In", Width: 4},
		{Title: "Out", Width: 4},
	}, sgColumns(ui.TierMedium)) // private
	assert.Equal(t, sgColumns(ui.TierMedium), sgColumns(ui.TierWide)) // private
}

func TestCharSG_RefreshKeepsRowsAndCallsListAgain(t *testing.T) {
	v, svc := newCharSG(t, charSGWeb, charSGDB)
	_, cmd := v.Update(keyPress('r'))
	require.NotNil(t, cmd)
	// Reload keeps the table on screen (sg keeps its items); the spinner
	// shows only in the footer.
	assert.Equal(t, "Group ID | Name | VPC | Description | In | Out", netHeader(v))
	assert.Equal(t, []string{
		"sg-111 | web-sg | vpc-aaa | Web servers | 2 | 1",
		"sg-222 | db-sg | vpc-aaa | Database | 1 | 0",
	}, netRows(v))
	assert.NotContains(t, netView(v), "Loading")
	assert.Equal(t, "2/2 security groups  ⣾  Loading security groups...", netFooter(v))

	// Details still work during the reload, because the items are kept.
	_, enter := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, ok := firstOfType[msg.TabbedContentMsg](collectMsgs(enter))
	assert.True(t, ok)

	pump(v, cmd, 5)
	svc.AssertNumberOfCalls(t, "ListSecurityGroups", 2)
	assert.Equal(t, "2/2 security groups", netFooter(v))
}

func TestCharSG_NavKeysFrozenDuringReload(t *testing.T) {
	v, _ := newCharSG(t, charSGWeb, charSGDB)
	_, cmd := v.Update(keyPress('r'))
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	// Divergence #6: changes in PR 4. Today the key goes to the spinner.
	assert.Equal(t, []string{"Copied: sg-111"}, netCopied(v))
	pump(v, cmd, 5)

	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	assert.Equal(t, []string{"Copied: sg-222"}, netCopied(v))
}

// sg has no load generation: the last reply wins, even from an older load.
func TestCharSG_StaleReplyLastWins(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListSecurityGroups", mock.Anything).Return([]aws.SecurityGroup{charSGWeb}, nil).Once()
	svc.On("ListSecurityGroups", mock.Anything).Return([]aws.SecurityGroup{charSGDB}, nil).Once()
	v := NewSGList(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})

	_, first := v.Update(keyPress('r'))
	_, second := v.Update(keyPress('r'))
	old := netLoadMsg(first)
	cur := netLoadMsg(second)
	deliverAll(v, cur, old)
	// No divergence row; the gen guard is a deliberate change: changes in PR 4.
	assert.Equal(t, []string{"sg-111 | web-sg | vpc-aaa | Web servers | 2 | 1"}, netRows(v))
}

func TestCharSG_ErrorContextAndView(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListSecurityGroups", mock.Anything).Return([]aws.SecurityGroup(nil), errors.New("boom\x1b[31m"))
	v := NewSGList(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})
	seen := pump(v, v.Init(), 5)

	em, ok := firstOfType[msg.ErrorMsg](seen)
	require.True(t, ok)
	assert.Equal(t, "listing security groups", em.Context)
	assert.Equal(t, "\n  Error: boom␛[31m", netView(v), "sanitized aws.ErrorText")
	assert.Contains(t, v.View().Content, fgSGR(ui.ActiveTheme.Error))
	assert.Equal(t, "0/0 security groups", netFooter(v))

	v.Update(msg.ErrorMsg{Err: context.Canceled})
	assert.Equal(t, "\n  Error: cancelled", netView(v), "uses aws.ErrorText")
}

func TestCharSG_NilServiceGivesErrorMsg(t *testing.T) {
	v := NewSGList(nil)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})
	seen := pump(v, v.Init(), 5)
	em, ok := firstOfType[msg.ErrorMsg](seen)
	require.True(t, ok)
	assert.Equal(t, "EC2", em.Context)
	require.EqualError(t, em.Err, "AWS client not initialized")
	assert.Equal(t, "\n  Error: AWS client not initialized", netView(v))
}

func TestCharSG_ErrorClearedOnlyByRefresh(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListSecurityGroups", mock.Anything).Return([]aws.SecurityGroup{charSGWeb}, nil)
	v := NewSGList(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})
	load := netLoadMsg(v.Init())

	v.Update(msg.ErrorMsg{Err: errors.New("denied"), Context: "x"})
	v.Update(load)
	// Divergence #8: changes in PR 4. A good load does not clear the error.
	assert.Equal(t, "\n  Error: denied", netView(v))
	assert.Equal(t, "1/1 security groups", netFooter(v))

	pump(v, func() tea.Msg { return keyPress('r') }, 5)
	assert.Equal(t, []string{"sg-111 | web-sg | vpc-aaa | Web servers | 2 | 1"}, netRows(v))
}

func TestCharSG_StaleErrorApplied(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListSecurityGroups", mock.Anything).Return([]aws.SecurityGroup(nil), errors.New("old failure")).Once()
	svc.On("ListSecurityGroups", mock.Anything).Return([]aws.SecurityGroup{charSGWeb}, nil).Once()
	v := NewSGList(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})

	_, first := v.Update(keyPress('r'))
	_, second := v.Update(keyPress('r'))
	old := netLoadMsg(first)
	cur := netLoadMsg(second)
	deliverAll(v, cur, old)
	// Divergence #9: changes in PR 4.
	assert.Equal(t, "\n  Error: old failure", netView(v))
}

func TestCharSG_SortPicker(t *testing.T) {
	v, _ := newCharSG(t, charSGWeb, charSGDB)

	_, cmd := v.Update(keyPress('s'))
	assert.Equal(t, []tea.Msg{msg.RequestSortPickerMsg{
		Columns:    []string{"Group ID", "Name", "VPC", "Description", "In", "Out"},
		CurrentCol: -1,
	}}, collectMsgs(cmd))

	v.Update(ui.PickerResultMsg{ID: "other", Selected: 1, Value: "Name"})
	assert.Equal(t, "sg-111", netRows(v)[0][:6], "a picker with another ID is ignored")

	v.Update(ui.PickerResultMsg{ID: "sort", Selected: 1, Value: "Name"})
	assert.Equal(t, []string{
		"sg-222 | db-sg | vpc-aaa | Database | 1 | 0",
		"sg-111 | web-sg | vpc-aaa | Web servers | 2 | 1",
	}, netRows(v))
	_, cmd = v.Update(keyPress('s'))
	req, ok := firstOfType[msg.RequestSortPickerMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, 1, req.CurrentCol)

	v.Update(ui.PickerResultMsg{ID: "sort", Selected: -1})
	assert.Equal(t, "sg-222", netRows(v)[0][:6], "a cancelled picker keeps the sort")

	// The sort survives a tier change.
	v.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	assert.Equal(t, "Group ID | Name ▲ | VPC | In | Out", netHeader(v))
	assert.Equal(t, "sg-222", netRows(v)[0][:6])
	_, cmd = v.Update(keyPress('s'))
	req, _ = firstOfType[msg.RequestSortPickerMsg](collectMsgs(cmd))
	assert.Equal(t, 1, req.CurrentCol)

	v.Update(keyPress('S'))
	assert.Equal(t, "sg-111", netRows(v)[0][:6], "S reverses the sort")
	assert.Equal(t, "Group ID | Name ▼ | VPC | In | Out", netHeader(v))

	v.Update(ui.PickerResultMsg{ID: "sort", Value: "_clear", Selected: 0})
	assert.Equal(t, "sg-111", netRows(v)[0][:6])
	assert.Equal(t, "Group ID | Name | VPC | In | Out", netHeader(v))
	_, cmd = v.Update(keyPress('s'))
	req, _ = firstOfType[msg.RequestSortPickerMsg](collectMsgs(cmd))
	assert.Equal(t, -1, req.CurrentCol, "_clear clears the sort")
}

func TestCharSG_InCountSortsAsNumber(t *testing.T) {
	many := aws.SecurityGroup{ID: "sg-many", Name: "many", InboundRules: make([]aws.SecurityGroupRule, 10)}
	few := aws.SecurityGroup{ID: "sg-few", Name: "few", InboundRules: make([]aws.SecurityGroupRule, 9)}
	v, _ := newCharSG(t, few, many)
	v.Update(ui.PickerResultMsg{ID: "sort", Selected: 4, Value: "In"})
	rows := netRows(v)
	require.Len(t, rows, 2)
	// Divergence #46 (fixed in PR 3): 9 sorts before 10.
	assert.Equal(t, "sg-few | few | 9 | 0", rows[0])
	assert.Equal(t, "sg-many | many | 10 | 0", rows[1])
}

func TestCharSG_CursorAndSortKeptAcrossReload(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListSecurityGroups", mock.Anything).Return([]aws.SecurityGroup{charSGWeb, charSGDB}, nil).Once()
	svc.On("ListSecurityGroups", mock.Anything).Return([]aws.SecurityGroup{{ID: "sg-000", Name: "aaa"}, charSGWeb, charSGDB}, nil).Once()
	v := NewSGList(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})
	pump(v, v.Init(), 5)
	v.Update(ui.PickerResultMsg{ID: "sort", Selected: 0, Value: "Group ID"})
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, []string{"Copied: sg-222"}, netCopied(v))

	_, cmd := v.Update(keyPress('r'))
	pump(v, cmd, 5)
	assert.Equal(t, []string{"Copied: sg-222"}, netCopied(v), "the cursor follows the key")
	assert.Equal(t, "sg-000", netRows(v)[0][:6], "the sort is kept")
}

func TestCharSG_CopyToast(t *testing.T) {
	v, _ := newCharSG(t, charSGWeb)
	_, cmd := v.Update(keyPress('y'))
	msgs := collectMsgs(cmd)
	require.Len(t, msgs, 2, "clipboard + toast")
	assert.Equal(t, []msg.ToastMsg{msg.ToastSuccess("Copied: sg-111")}, toasts(msgs))

	empty, _ := newCharSG(t)
	assert.Empty(t, netCopied(empty), "no row, no toast")
}

func TestCharSG_SpaceDoesNothing(t *testing.T) {
	v, _ := newCharSG(t, charSGWeb, charSGDB)
	before := netView(v)
	_, cmd := v.Update(keyPress(' '))
	assert.Empty(t, collectMsgs(cmd))
	assert.Equal(t, before, netView(v))
	assert.NotContains(t, netView(v), "✓")
	assert.Equal(t, "2/2 security groups", netFooter(v))
}

func TestCharSG_EscGoesBack(t *testing.T) {
	v, _ := newCharSG(t, charSGWeb)
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Equal(t, []tea.Msg{msg.NavigateBackMsg{}}, collectMsgs(cmd))
}

func TestCharSG_DetailTabs(t *testing.T) {
	v, _ := newCharSG(t, charSGWeb, charSGDB)
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	tm, ok := firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "web-sg (sg-111)", tm.PanelTitle)
	require.Len(t, tm.Tabs, 5)

	assert.Equal(t, msg.TabContent{
		Title: "Info", Format: "text",
		Content: "Group ID         sg-111\n" +
			"Name             web-sg\n" +
			"ARN              arn:aws:ec2:us-east-1:123456789012:security-group/sg-111\n" +
			"Description      Web servers\n" +
			"VPC              vpc-aaa\n" +
			"Owner            123456789012\n",
		Links: []msg.TabLink{{Line: 4, ViewID: "vpc_list", Params: map[string]string{"focus": "vpc-aaa"}}},
	}, tm.Tabs[0])
	assert.Equal(t, msg.TabContent{
		Title: "Inbound", Format: "text",
		Content: "Proto    Ports          Source/Dest                              Description\n" +
			"─────    ─────          ───────────                              ───────────\n" +
			"TCP      80             0.0.0.0/0                                HTTP\n" +
			"TCP      443            0.0.0.0/0                                \n",
	}, tm.Tabs[1])
	assert.Equal(t, msg.TabContent{
		Title: "Outbound", Format: "text",
		Content: "Proto    Ports          Source/Dest                              Description\n" +
			"─────    ─────          ───────────                              ───────────\n" +
			"All      All            0.0.0.0/0                                \n",
	}, tm.Tabs[2])
	assert.Equal(t, "JSON", tm.Tabs[3].Title)
	assert.Equal(t, "json", tm.Tabs[3].Format)
	assert.Equal(t, charSGWeb.DetailJSON(), tm.Tabs[3].Content)
	assert.Empty(t, tm.Tabs[3].Links)
	assert.Equal(t, msg.TabContent{
		Title: "Tags", Format: "text",
		Content: "Name                     web-sg\n" +
			"env                      prod\n",
	}, tm.Tabs[4])

	// d opens the same panel; a group with no tags has no Tags tab; empty
	// fields are skipped, so the VPC link moves up a line.
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd = v.Update(keyPress('d'))
	tm, ok = firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "db-sg (sg-222)", tm.PanelTitle)
	titles := make([]string, 0, len(tm.Tabs))
	for _, tab := range tm.Tabs {
		titles = append(titles, tab.Title)
	}
	assert.Equal(t, []string{"Info", "Inbound", "Outbound", "JSON"}, titles)
	assert.Equal(t, []msg.TabLink{{Line: 3, ViewID: "vpc_list", Params: map[string]string{"focus": "vpc-aaa"}}}, tm.Tabs[0].Links)
	assert.Equal(t, "  No rules", tm.Tabs[2].Content)
}

func TestCharSG_PanelTitleWithoutName(t *testing.T) {
	v, _ := newCharSG(t, aws.SecurityGroup{ID: "sg-noname"})
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	tm, ok := firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "sg-noname", tm.PanelTitle)
	assert.Equal(t, "Group ID         sg-noname\n", tm.Tabs[0].Content)
	assert.Empty(t, tm.Tabs[0].Links)
}

func TestCharSG_DetailWithNoRowFallsThrough(t *testing.T) {
	v, _ := newCharSG(t)
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Empty(t, collectMsgs(cmd))
}

func TestCharSG_RuleFormatting(t *testing.T) {
	sg := aws.SecurityGroup{
		ID: "sg-rules",
		InboundRules: []aws.SecurityGroupRule{
			{Protocol: "tcp", FromPort: 80, ToPort: 80, CIDRs: []string{"0.0.0.0/0"}, Description: "HTTP"},
			{Protocol: "tcp", FromPort: 8000, ToPort: 8443, CIDRs: []string{"10.0.0.0/8"}, IPv6CIDRs: []string{"::/0"}},
			{Protocol: "udp", FromPort: 53, ToPort: 53, PrefixLists: []string{"pl-123"}},
			{Protocol: "icmp", FromPort: -1, ToPort: -1, SGRefs: []string{"sg-222"}},
			{Protocol: "icmp", FromPort: 8, ToPort: -1, CIDRs: []string{"10.1.0.0/16"}},
			{Protocol: "icmpv6", FromPort: -1, ToPort: -1, IPv6CIDRs: []string{"::/0"}},
			{Protocol: "-1", FromPort: -1, ToPort: -1, SGRefs: []string{"sg-111"}, Description: "A description that is longer than thirty"},
			{Protocol: "47"},
		},
	}
	v, _ := newCharSG(t, sg)
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	tm, ok := firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "Proto    Ports          Source/Dest                              Description\n"+
		"─────    ─────          ───────────                              ───────────\n"+
		"TCP      80             0.0.0.0/0                                HTTP\n"+
		"TCP      8000-8443      10.0.0.0/8, ::/0                         \n"+
		"UDP      53             pl-123                                   \n"+
		"ICMP     All            sg-222                                   \n"+
		"ICMP     Type 8         10.1.0.0/16                              \n"+
		"ICMPv6   All            ::/0                                     \n"+
		"All      All            sg-111                                   A description that is longe...\n"+
		"47       0              -                                        \n", tm.Tabs[1].Content)
	assert.Equal(t, "  No rules", tm.Tabs[2].Content)
}

func TestCharSG_FocusWhileLoadingOpensAfterLoad(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListSecurityGroups", mock.Anything).Return([]aws.SecurityGroup{charSGWeb, charSGDB}, nil)
	v := NewSGList(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})
	load := netLoadMsg(v.Init())

	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "sg-222"})
	assert.Nil(t, cmd, "focus while loading is stored")

	_, cmd = v.Update(load)
	tm, ok := firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "db-sg (sg-222)", tm.PanelTitle)
	assert.Equal(t, []string{"Copied: sg-222"}, netCopied(v), "the cursor is on the focused row")
}

func TestCharSG_FocusAfterLoad(t *testing.T) {
	v, _ := newCharSG(t, charSGWeb, charSGDB)
	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "sg-222"})
	tm, ok := firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "db-sg (sg-222)", tm.PanelTitle)

	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: "sg-missing"})
	assert.Nil(t, cmd, "focus on a missing ID does nothing")
	assert.Equal(t, []string{"Copied: sg-222"}, netCopied(v), "the cursor stays")
}

func TestCharSG_FocusOnFilteredRow(t *testing.T) {
	v, _ := newCharSG(t, charSGWeb, charSGDB)
	v.Update(ui.FilterChangedMsg{Text: "db"})
	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "sg-111"})
	tm, ok := firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
	require.True(t, ok, "Divergence #22 (keep): the panel opens")
	assert.Equal(t, "web-sg (sg-111)", tm.PanelTitle)
	assert.Equal(t, []string{"Copied: sg-222"}, netCopied(v), "the cursor stays on the visible row")
	assert.Equal(t, "1/2 security groups", netFooter(v))
}

func TestCharSG_PendingFocusSurvivesError(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListSecurityGroups", mock.Anything).Return([]aws.SecurityGroup{charSGWeb, charSGDB}, nil)
	v := NewSGList(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})
	v.Update(msg.FocusResourceMsg{ResourceID: "sg-222"})
	v.Update(msg.ErrorMsg{Err: errors.New("x")})

	_, cmd := v.Update(keyPress('r'))
	// Divergence #51 (keep): the stored focus opens after the next load.
	seen := pump(v, cmd, 5)
	tm, ok := firstOfType[msg.TabbedContentMsg](seen)
	require.True(t, ok)
	assert.Equal(t, "db-sg (sg-222)", tm.PanelTitle)
}

func TestCharSG_UpdateReturnsSamePointer(t *testing.T) {
	v, _ := newCharSG(t, charSGWeb)
	_, reload := v.Update(keyPress('r'))
	load := netLoadMsg(reload)
	for _, m := range []tea.Msg{
		spinner.TickMsg{},
		tea.WindowSizeMsg{Width: 100, Height: 12},
		keyPress('s'), keyPress('S'), keyPress('y'), keyPress('d'), keyPress(' '),
		tea.KeyPressMsg{Code: tea.KeyDown},
		ui.PickerResultMsg{ID: "sort", Selected: 0},
		ui.FilterChangedMsg{Text: "x"},
		msg.FocusResourceMsg{ResourceID: "sg-111"},
		keyPress('/'), keyPress('x'), tea.KeyPressMsg{Code: tea.KeyEnter},
		keyPress('/'), tea.KeyPressMsg{Code: tea.KeyEscape},
		keyPress('r'),
		load,
		msg.ErrorMsg{Err: errors.New("x")},
		tea.KeyPressMsg{Code: tea.KeyEscape},
	} {
		got, _ := v.Update(m)
		require.Same(t, v, got, "%T", m)
	}
}

func TestCharSG_SetContextReachesFetch(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListSecurityGroups", mock.MatchedBy(func(ctx context.Context) bool {
		_, hasDeadline := ctx.Deadline()
		return ctx.Value(netCtxKey{}) == "nav" && hasDeadline
	})).Return([]aws.SecurityGroup{charSGWeb}, nil)
	v := NewSGList(svc)
	v.SetContext(context.WithValue(t.Context(), netCtxKey{}, "nav"))
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})
	pump(v, v.Init(), 5)
	svc.AssertExpectations(t)
	assert.Equal(t, "1/1 security groups", netFooter(v))
}
