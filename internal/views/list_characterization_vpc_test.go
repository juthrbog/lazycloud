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

// PR 0 characterization tests for vpc_list. They record today's behavior.
// Only the vpc port PR (PR 5) may edit this file.

var charVPCMain = aws.VPC{
	ID: "vpc-aaa111", Name: "main-vpc", CIDRBlock: "10.0.0.0/16",
	State: "available", IsDefault: true, InstanceTenancy: "default",
	DHCPOptionsID: "dopt-abc", OwnerID: "123456789012",
	IPv4Associations: []aws.CIDRAssociation{
		{CIDRBlock: "10.0.0.0/16", State: "associated"},
		{CIDRBlock: "10.1.0.0/16", State: "associating"},
	},
	IPv6Associations: []aws.IPv6CIDRAssociation{{IPv6CIDRBlock: "2600:1f18::/56", State: "associated"}},
	Tags:             map[string]string{"Name": "main-vpc", "env": "prod"},
}

var charVPCDev = aws.VPC{
	ID: "vpc-bbb222", Name: "dev-vpc", CIDRBlock: "172.16.0.0/16",
	State: "pending", InstanceTenancy: "dedicated",
}

// newCharVPC returns a VPC list at width 160 with a mock that returns vpcs
// as one page, and the load already delivered.
func newCharVPC(t *testing.T, vpcs ...aws.VPC) (*VPCList, *awstest.MockEC2Service) {
	t.Helper()
	svc := new(awstest.MockEC2Service)
	svc.On("ListVPCsPage", mock.Anything, netNoToken).Return(&aws.VPCPage{VPCs: vpcs}, nil)
	v := NewVPCList(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})
	pump(v, v.Init(), 5)
	return v, svc
}

func TestCharVPC_IDAndTitle(t *testing.T) {
	v := NewVPCList(nil)
	assert.Equal(t, "vpc_list", v.ID())
	assert.Equal(t, "VPCs", v.Title())
}

func TestCharVPC_KeyMap(t *testing.T) {
	assert.Equal(t, []string{
		`enter | enter/d | details | mode=0 | cat=""`,
		`y | y | copy ID | mode=0 | cat=""`,
		`n | n | subnets | mode=0 | cat=""`,
		`s | s/S | sort | mode=0 | cat=""`,
		`/ | / | filter | mode=0 | cat=""`,
		`r | r | refresh | mode=0 | cat=""`,
	}, netKeyMap(NewVPCList(nil).KeyMap()))
}

func TestCharVPC_CapturingInputWhileFilterOpen(t *testing.T) {
	v, _ := newCharVPC(t, charVPCMain, charVPCDev)
	assert.False(t, v.CapturingInput())
	v.Update(keyPress('/'))
	assert.True(t, v.CapturingInput())

	// n is a filter letter now, not the subnets key.
	assert.Equal(t, []tea.Msg{ui.FilterChangedMsg{Text: "n"}}, netType(v, keyPress('n')), "no NavigateMsg")

	v.Update(ui.FilterChangedMsg{Text: "dev"})
	assert.Equal(t, "1/2 VPCs", netFooter(v))
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.False(t, v.CapturingInput())
	assert.Equal(t, []tea.Msg{ui.FilterChangedMsg{Text: ""}}, collectMsgs(cmd), "esc closes the filter, not the view")

	// enter closes the filter and keeps the text.
	v.Update(keyPress('/'))
	v.Update(ui.FilterChangedMsg{Text: "dev"})
	_, cmd = v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Nil(t, cmd)
	assert.False(t, v.CapturingInput())
	assert.Equal(t, "1/2 VPCs", netFooter(v))
}

func TestCharVPC_InitLoadsOnlyWhileLoading(t *testing.T) {
	v, svc := newCharVPC(t, charVPCMain)
	svc.AssertNumberOfCalls(t, "ListVPCsPage", 1)
	assert.Nil(t, v.Init(), "a cached view does not reload")
	svc.AssertNumberOfCalls(t, "ListVPCsPage", 1)
}

func TestCharVPC_FirstLoadViewAndFooter(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListVPCsPage", mock.Anything, netNoToken).Return(&aws.VPCPage{VPCs: []aws.VPC{charVPCMain, charVPCDev}}, nil)
	v := NewVPCList(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})

	assert.Equal(t, "\n  ⣾  Loading VPCs...", netView(v))
	assert.Equal(t, "0/0 VPCs  ⣾  Loading VPCs...", netFooter(v))

	pump(v, v.Init(), 5)
	assert.Equal(t, "VPC ID | Name | CIDR Block | State | Default | Tenancy", netHeader(v))
	assert.Equal(t, []string{
		"vpc-aaa111 | main-vpc | 10.0.0.0/16 | " + ui.IconRunning.Icon() + " available | Yes | default",
		"vpc-bbb222 | dev-vpc | 172.16.0.0/16 | " + ui.IconPending.Icon() + " pending | No | dedicated",
	}, netRows(v))
	assert.Equal(t, "2/2 VPCs", netFooter(v))
}

// A gen-0 page built by hand (as the old tests do) is accepted on a fresh
// view, with page number 0 or 1.
func TestCharVPC_LiteralPageAcceptedOnFreshView(t *testing.T) {
	for _, page := range []int{0, 1} {
		v := NewVPCList(new(awstest.MockEC2Service))
		v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})
		v.Update(vpcPageLoadedMsg{vpcs: []aws.VPC{charVPCMain}, pageNum: page}) // private
		assert.Equal(t, "1/1 VPCs", netFooter(v), "page %d", page)
	}
}

func TestCharVPC_ColumnsPerWidth(t *testing.T) {
	cases := map[int]string{
		60:  "VPC ID | Name | CIDR Block | State",
		100: "VPC ID | Name | CIDR Block | State",
		160: "VPC ID | Name | CIDR Block | State | Default | Tenancy",
		200: "VPC ID | Name | CIDR Block | State | Default | Tenancy",
	}
	for w, want := range cases {
		v, _ := newCharVPC(t, charVPCMain)
		v.Update(tea.WindowSizeMsg{Width: w, Height: 12})
		assert.Equal(t, want, netHeader(v), "width %d", w)
	}
	assert.Equal(t, []string{
		"VPC ID w=21 weight=1 max=35 style=false",
		"Name w=24 weight=2 max=60 style=false",
		"CIDR Block w=18 weight=0 max=0 style=false",
		"State w=16 weight=0 max=0 style=true",
	}, netCols(vpcColumns(ui.TierNarrow))) // private
	full := []string{
		"VPC ID w=21 weight=1 max=35 style=false",
		"Name w=24 weight=2 max=60 style=false",
		"CIDR Block w=18 weight=0 max=0 style=false",
		"State w=16 weight=0 max=0 style=true",
		"Default w=8 weight=0 max=0 style=false",
		"Tenancy w=12 weight=0 max=0 style=false",
	}
	assert.Equal(t, full, netCols(vpcColumns(ui.TierMedium))) // private
	assert.Equal(t, full, netCols(vpcColumns(ui.TierWide)))   // private
}

func TestCharVPC_StateCellPlainAndColoredByColumn(t *testing.T) {
	v, _ := newCharVPC(t, charVPCMain, charVPCDev)
	assert.Contains(t, v.View().Content, fgSGR(ui.ActiveTheme.StateRunning))
	assert.Contains(t, v.View().Content, fgSGR(ui.ActiveTheme.StatePending))

	// The filter reads the cell text: the state word matches, the icon the
	// column Style adds does not.
	v.Update(ui.FilterChangedMsg{Text: "available"})
	assert.Equal(t, "1/2 VPCs", netFooter(v))
	v.Update(ui.FilterChangedMsg{Text: ui.IconRunning.Icon()})
	assert.Equal(t, "0/2 VPCs", netFooter(v))
}

func TestCharVPC_Paging(t *testing.T) {
	tok := "t2"
	svc := new(awstest.MockEC2Service)
	svc.On("ListVPCsPage", mock.Anything, netNoToken).Return(&aws.VPCPage{
		VPCs: []aws.VPC{charVPCMain, charVPCDev}, HasMorePages: true, Token: &tok,
	}, nil)
	svc.On("ListVPCsPage", mock.Anything, &tok).Return(&aws.VPCPage{
		VPCs: []aws.VPC{charVPCDev, {ID: "vpc-ccc333", Name: "third"}},
	}, nil)
	v := NewVPCList(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})

	page1 := netLoadMsg(v.Init())
	_, next := v.Update(page1)
	require.NotNil(t, next)
	// Divergence #5: changes in PR 5. No "N so far" progress label today.
	assert.Equal(t, "2/2 VPCs  ⣾  Loading VPCs...", netFooter(v))
	assert.Contains(t, netView(v), "vpc-aaa111", "rows of page 1 show while page 2 loads")

	page2 := netLoadMsg(next)
	_, done := v.Update(page2)
	assert.Nil(t, done)
	// Divergence #14: changes in PR 5. The table drops the duplicate row; the
	// view's items keep it (not visible through the public surface).
	assert.Equal(t, "3/3 VPCs", netFooter(v))
	assert.Equal(t, []string{
		"vpc-aaa111 | main-vpc | 10.0.0.0/16 | " + ui.IconRunning.Icon() + " available | Yes | default",
		"vpc-bbb222 | dev-vpc | 172.16.0.0/16 | " + ui.IconPending.Icon() + " pending | No | dedicated",
		"vpc-ccc333 | third | No",
	}, netRows(v), "page 2 is appended after page 1")
	svc.AssertNumberOfCalls(t, "ListVPCsPage", 2)
}

func TestCharVPC_StalePageDropped(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListVPCsPage", mock.Anything, netNoToken).Return(&aws.VPCPage{VPCs: []aws.VPC{charVPCMain}}, nil).Once()
	svc.On("ListVPCsPage", mock.Anything, netNoToken).Return(&aws.VPCPage{VPCs: []aws.VPC{charVPCDev}}, nil).Once()
	v := NewVPCList(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})

	_, first := v.Update(keyPress('r'))
	_, second := v.Update(keyPress('r'))
	old := netLoadMsg(first)
	cur := netLoadMsg(second)
	deliverAll(v, cur, old)
	assert.Equal(t, []string{"vpc-bbb222 | dev-vpc | 172.16.0.0/16 | " + ui.IconPending.Icon() + " pending | No | dedicated"}, netRows(v))
	assert.Equal(t, "1/1 VPCs", netFooter(v))
}

func TestCharVPC_RefreshClearsItems(t *testing.T) {
	v, svc := newCharVPC(t, charVPCMain, charVPCDev)
	_, cmd := v.Update(keyPress('r'))
	require.NotNil(t, cmd)
	// Divergence #7: changes in PR 5. The reload drops the items, so the
	// full-screen spinner shows, details do nothing and the rows stay only
	// in the table (footer).
	assert.Equal(t, "\n  ⣾  Loading VPCs...", netView(v))
	assert.Equal(t, "2/2 VPCs  ⣾  Loading VPCs...", netFooter(v))
	_, enter := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Nil(t, enter)
	// y and n still use the table cursor.
	assert.Equal(t, []string{"Copied: vpc-aaa111"}, netCopied(v))

	v.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	assert.Equal(t, "0/0 VPCs  ⣾  Loading VPCs...", netFooter(v), "Divergence #7: a tier change mid-reload empties the table")

	pump(v, cmd, 5)
	svc.AssertNumberOfCalls(t, "ListVPCsPage", 2)
	assert.Equal(t, "2/2 VPCs", netFooter(v))
}

func TestCharVPC_NavKeysFrozenDuringReload(t *testing.T) {
	v, _ := newCharVPC(t, charVPCMain, charVPCDev)
	_, cmd := v.Update(keyPress('r'))
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	// Divergence #6: changes in PR 5. Today the key goes to the spinner.
	assert.Equal(t, []string{"Copied: vpc-aaa111"}, netCopied(v))
	pump(v, cmd, 5)
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	assert.Equal(t, []string{"Copied: vpc-bbb222"}, netCopied(v))
}

func TestCharVPC_ErrorContextAndView(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListVPCsPage", mock.Anything, netNoToken).Return(nil, errors.New("boom\x1b[31m"))
	v := NewVPCList(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})
	seen := pump(v, v.Init(), 5)

	em, ok := firstOfType[msg.ErrorMsg](seen)
	require.True(t, ok)
	assert.Equal(t, "listing VPCs", em.Context)
	assert.Equal(t, "\n  Error: boom␛[31m", netView(v))
	assert.Contains(t, v.View().Content, fgSGR(ui.ActiveTheme.Error))
	assert.Equal(t, "0/0 VPCs", netFooter(v))
}

func TestCharVPC_NilServiceGivesErrorMsg(t *testing.T) {
	v := NewVPCList(nil)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})
	em, ok := firstOfType[msg.ErrorMsg](pump(v, v.Init(), 5))
	require.True(t, ok)
	assert.Equal(t, "EC2", em.Context)
	require.EqualError(t, em.Err, "AWS client not initialized")
	assert.Equal(t, "\n  Error: AWS client not initialized", netView(v))
}

func TestCharVPC_ErrorClearedOnlyByRefresh(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListVPCsPage", mock.Anything, netNoToken).Return(&aws.VPCPage{VPCs: []aws.VPC{charVPCMain}}, nil)
	v := NewVPCList(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})
	load := netLoadMsg(v.Init())

	v.Update(msg.ErrorMsg{Err: errors.New("denied")})
	v.Update(load)
	// Divergence #8: changes in PR 5. A good load does not clear the error.
	assert.Equal(t, "\n  Error: denied", netView(v))
	assert.Equal(t, "1/1 VPCs", netFooter(v))

	pump(v, func() tea.Msg { return keyPress('r') }, 5)
	assert.Equal(t, []string{
		"vpc-aaa111 | main-vpc | 10.0.0.0/16 | " + ui.IconRunning.Icon() + " available | Yes | default",
	}, netRows(v))
}

func TestCharVPC_StaleErrorApplied(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListVPCsPage", mock.Anything, netNoToken).Return(nil, errors.New("old failure")).Once()
	svc.On("ListVPCsPage", mock.Anything, netNoToken).Return(&aws.VPCPage{VPCs: []aws.VPC{charVPCMain}}, nil).Once()
	v := NewVPCList(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})

	_, first := v.Update(keyPress('r'))
	_, second := v.Update(keyPress('r'))
	old := netLoadMsg(first)
	cur := netLoadMsg(second)
	deliverAll(v, cur, old)
	// Divergence #9: changes in PR 5.
	assert.Equal(t, "\n  Error: old failure", netView(v))
}

func TestCharVPC_SortPicker(t *testing.T) {
	v, _ := newCharVPC(t, charVPCMain, charVPCDev)
	_, cmd := v.Update(keyPress('s'))
	assert.Equal(t, []tea.Msg{msg.RequestSortPickerMsg{
		Columns:    []string{"VPC ID", "Name", "CIDR Block", "State", "Default", "Tenancy"},
		CurrentCol: -1,
	}}, collectMsgs(cmd))

	v.Update(ui.PickerResultMsg{ID: "other", Selected: 1, Value: "Name"})
	assert.Equal(t, "vpc-aaa111", netRows(v)[0][:10], "a picker with another ID is ignored")

	v.Update(ui.PickerResultMsg{ID: "sort", Selected: 1, Value: "Name"})
	assert.Equal(t, "vpc-bbb222", netRows(v)[0][:10])
	_, cmd = v.Update(keyPress('s'))
	req, ok := firstOfType[msg.RequestSortPickerMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, 1, req.CurrentCol)

	v.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	assert.Equal(t, "VPC ID | Name ▲ | CIDR Block | State", netHeader(v), "the sort survives a tier change")
	assert.Equal(t, "vpc-bbb222", netRows(v)[0][:10])

	v.Update(ui.PickerResultMsg{ID: "sort", Selected: -1})
	assert.Equal(t, "vpc-bbb222", netRows(v)[0][:10], "a cancelled picker keeps the sort")

	v.Update(keyPress('S'))
	assert.Equal(t, "VPC ID | Name ▼ | CIDR Block | State", netHeader(v))
	assert.Equal(t, "vpc-aaa111", netRows(v)[0][:10], "S reverses the sort")

	v.Update(ui.PickerResultMsg{ID: "sort", Value: "_clear"})
	assert.Equal(t, "VPC ID | Name | CIDR Block | State", netHeader(v))
	assert.Equal(t, "vpc-aaa111", netRows(v)[0][:10])
	_, cmd = v.Update(keyPress('s'))
	req, _ = firstOfType[msg.RequestSortPickerMsg](collectMsgs(cmd))
	assert.Equal(t, -1, req.CurrentCol, "_clear clears the sort")
}

func TestCharVPC_CursorKeptAcrossReload(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListVPCsPage", mock.Anything, netNoToken).Return(&aws.VPCPage{VPCs: []aws.VPC{charVPCMain, charVPCDev}}, nil).Once()
	svc.On("ListVPCsPage", mock.Anything, netNoToken).Return(&aws.VPCPage{VPCs: []aws.VPC{{ID: "vpc-000"}, charVPCMain, charVPCDev}}, nil).Once()
	v := NewVPCList(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})
	pump(v, v.Init(), 5)
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, []string{"Copied: vpc-bbb222"}, netCopied(v))

	_, cmd := v.Update(keyPress('r'))
	pump(v, cmd, 5)
	assert.Equal(t, []string{"Copied: vpc-bbb222"}, netCopied(v))
}

func TestCharVPC_SubnetsKey(t *testing.T) {
	v, _ := newCharVPC(t, charVPCMain, charVPCDev)
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd := v.Update(keyPress('n'))
	assert.Equal(t, []tea.Msg{msg.NavigateMsg{
		ViewID: "subnet_list", Params: map[string]string{"vpc_id": "vpc-bbb222"},
	}}, collectMsgs(cmd))

	empty, _ := newCharVPC(t)
	_, cmd = empty.Update(keyPress('n'))
	assert.Nil(t, cmd, "an empty cursor does nothing")
}

func TestCharVPC_CopyToast(t *testing.T) {
	v, _ := newCharVPC(t, charVPCMain)
	_, cmd := v.Update(keyPress('y'))
	msgs := collectMsgs(cmd)
	require.Len(t, msgs, 2, "clipboard + toast")
	assert.Equal(t, []msg.ToastMsg{msg.ToastSuccess("Copied: vpc-aaa111")}, toasts(msgs))

	empty, _ := newCharVPC(t)
	assert.Empty(t, netCopied(empty))
}

func TestCharVPC_SpaceDoesNothing(t *testing.T) {
	v, _ := newCharVPC(t, charVPCMain, charVPCDev)
	before := netView(v)
	_, cmd := v.Update(keyPress(' '))
	assert.Empty(t, collectMsgs(cmd))
	assert.Equal(t, before, netView(v))
	assert.NotContains(t, netView(v), "✓")
	assert.Equal(t, "2/2 VPCs", netFooter(v))
}

func TestCharVPC_EscGoesBack(t *testing.T) {
	v, _ := newCharVPC(t, charVPCMain)
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Equal(t, []tea.Msg{msg.NavigateBackMsg{}}, collectMsgs(cmd))
}

func TestCharVPC_DetailTabs(t *testing.T) {
	v, _ := newCharVPC(t, charVPCMain, charVPCDev)
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	tm, ok := firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "main-vpc (vpc-aaa111)", tm.PanelTitle)
	require.Len(t, tm.Tabs, 4)

	assert.Equal(t, msg.TabContent{
		Title: "Info", Format: "text",
		Content: "VPC ID           vpc-aaa111\n" +
			"Name             main-vpc\n" +
			"State            available\n" +
			"CIDR Block       10.0.0.0/16\n" +
			"Default          Yes\n" +
			"Tenancy          default\n" +
			"DHCP Options     dopt-abc\n" +
			"Owner            123456789012\n" +
			"Subnets          View subnets\n",
		Links: []msg.TabLink{{Line: 8, ViewID: "subnet_list", Params: map[string]string{"vpc_id": "vpc-aaa111"}}},
	}, tm.Tabs[0])
	assert.Equal(t, msg.TabContent{
		Title: "CIDR Blocks", Format: "text",
		Content: "IPv4 CIDR Blocks\n" +
			"────────────────\n" +
			"  10.0.0.0/16          associated\n" +
			"  10.1.0.0/16          associating\n" +
			"\nIPv6 CIDR Blocks\n" +
			"────────────────\n" +
			"  2600:1f18::/56                               associated\n",
	}, tm.Tabs[1])
	assert.Equal(t, msg.TabContent{Title: "JSON", Format: "json", Content: charVPCMain.DetailJSON()}, tm.Tabs[2])
	assert.Equal(t, msg.TabContent{
		Title: "Tags", Format: "text",
		Content: "Name                     main-vpc\n" +
			"env                      prod\n",
	}, tm.Tabs[3])

	// d opens the same panel. No tags: no Tags tab. No associations: the
	// CIDR tab shows the main block. Empty fields are skipped.
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd = v.Update(keyPress('d'))
	tm, ok = firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "dev-vpc (vpc-bbb222)", tm.PanelTitle)
	require.Len(t, tm.Tabs, 3)
	assert.Equal(t, "VPC ID           vpc-bbb222\n"+
		"Name             dev-vpc\n"+
		"State            pending\n"+
		"CIDR Block       172.16.0.0/16\n"+
		"Default          No\n"+
		"Tenancy          dedicated\n"+
		"Subnets          View subnets\n", tm.Tabs[0].Content)
	assert.Equal(t, []msg.TabLink{{Line: 6, ViewID: "subnet_list", Params: map[string]string{"vpc_id": "vpc-bbb222"}}}, tm.Tabs[0].Links)
	assert.Equal(t, "IPv4 CIDR Blocks\n────────────────\n  172.16.0.0/16\n", tm.Tabs[1].Content)
	assert.Equal(t, "JSON", tm.Tabs[2].Title)
}

func TestCharVPC_PanelTitleWithoutName(t *testing.T) {
	v, _ := newCharVPC(t, aws.VPC{ID: "vpc-noname"})
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	tm, ok := firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "vpc-noname", tm.PanelTitle)
}

func TestCharVPC_DetailWithNoRowFallsThrough(t *testing.T) {
	v, _ := newCharVPC(t)
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Empty(t, collectMsgs(cmd))
}

func TestCharVPC_FocusWhileLoadingOpensAfterLoad(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListVPCsPage", mock.Anything, netNoToken).Return(&aws.VPCPage{VPCs: []aws.VPC{charVPCMain, charVPCDev}}, nil)
	v := NewVPCList(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})
	load := netLoadMsg(v.Init())

	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "vpc-bbb222"})
	assert.Nil(t, cmd, "focus while loading is stored")
	_, cmd = v.Update(load)
	tm, ok := firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "dev-vpc (vpc-bbb222)", tm.PanelTitle)
	assert.Equal(t, []string{"Copied: vpc-bbb222"}, netCopied(v))
}

// The stored focus waits for the last page.
func TestCharVPC_FocusWaitsForLastPage(t *testing.T) {
	tok := "t2"
	svc := new(awstest.MockEC2Service)
	svc.On("ListVPCsPage", mock.Anything, netNoToken).Return(&aws.VPCPage{VPCs: []aws.VPC{charVPCMain}, HasMorePages: true, Token: &tok}, nil)
	svc.On("ListVPCsPage", mock.Anything, &tok).Return(&aws.VPCPage{VPCs: []aws.VPC{charVPCDev}}, nil)
	v := NewVPCList(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})
	v.Update(msg.FocusResourceMsg{ResourceID: "vpc-aaa111"})

	_, next := v.Update(netLoadMsg(v.Init()))
	_, isPanel := firstOfType[msg.TabbedContentMsg](netNoTicks(collectMsgs(next)))
	assert.False(t, isPanel, "page 1 does not open the panel")

	_, next = v.Update(netLoadMsg(next))
	tm, ok := firstOfType[msg.TabbedContentMsg](collectMsgs(next))
	require.True(t, ok)
	assert.Equal(t, "main-vpc (vpc-aaa111)", tm.PanelTitle)
}

func TestCharVPC_FocusAfterLoad(t *testing.T) {
	v, _ := newCharVPC(t, charVPCMain, charVPCDev)
	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "vpc-bbb222"})
	tm, ok := firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "dev-vpc (vpc-bbb222)", tm.PanelTitle)

	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: "vpc-missing"})
	assert.Nil(t, cmd, "focus on a missing ID does nothing")
	assert.Equal(t, []string{"Copied: vpc-bbb222"}, netCopied(v))
}

func TestCharVPC_FocusOnFilteredRow(t *testing.T) {
	v, _ := newCharVPC(t, charVPCMain, charVPCDev)
	v.Update(ui.FilterChangedMsg{Text: "dev"})
	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "vpc-aaa111"})
	_, ok := firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
	assert.True(t, ok, "Divergence #22 (keep): the panel opens")
	assert.Equal(t, []string{"Copied: vpc-bbb222"}, netCopied(v), "the cursor stays on the visible row")
}

func TestCharVPC_PendingFocusSurvivesError(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListVPCsPage", mock.Anything, netNoToken).Return(&aws.VPCPage{VPCs: []aws.VPC{charVPCMain, charVPCDev}}, nil)
	v := NewVPCList(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})
	v.Update(msg.FocusResourceMsg{ResourceID: "vpc-bbb222"})
	v.Update(msg.ErrorMsg{Err: errors.New("x")})

	_, cmd := v.Update(keyPress('r'))
	// Divergence #51 (keep): the stored focus opens after the next load.
	tm, ok := firstOfType[msg.TabbedContentMsg](pump(v, cmd, 5))
	require.True(t, ok)
	assert.Equal(t, "dev-vpc (vpc-bbb222)", tm.PanelTitle)
}

func TestCharVPC_UpdateReturnsSamePointer(t *testing.T) {
	v, _ := newCharVPC(t, charVPCMain)
	_, first := v.Update(keyPress('r'))
	_, second := v.Update(keyPress('r'))
	stale := netLoadMsg(first)
	cur := netLoadMsg(second)
	for _, m := range []tea.Msg{
		spinner.TickMsg{},
		tea.WindowSizeMsg{Width: 100, Height: 12},
		keyPress('s'), keyPress('S'), keyPress('y'), keyPress('n'), keyPress('d'), keyPress(' '),
		tea.KeyPressMsg{Code: tea.KeyDown},
		ui.PickerResultMsg{ID: "sort", Selected: 0},
		ui.FilterChangedMsg{Text: "x"},
		msg.FocusResourceMsg{ResourceID: "vpc-aaa111"},
		keyPress('/'), keyPress('x'), tea.KeyPressMsg{Code: tea.KeyEnter},
		keyPress('/'), tea.KeyPressMsg{Code: tea.KeyEscape},
		stale, cur,
		keyPress('r'),
		msg.ErrorMsg{Err: errors.New("x")},
		tea.KeyPressMsg{Code: tea.KeyEscape},
	} {
		got, _ := v.Update(m)
		require.Same(t, v, got, "%T", m)
	}
}

func TestCharVPC_SetContextReachesFetch(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListVPCsPage", mock.MatchedBy(func(ctx context.Context) bool {
		_, hasDeadline := ctx.Deadline()
		return ctx.Value(netCtxKey{}) == "nav" && hasDeadline
	}), netNoToken).Return(&aws.VPCPage{VPCs: []aws.VPC{charVPCMain}}, nil)
	v := NewVPCList(svc)
	v.SetContext(context.WithValue(t.Context(), netCtxKey{}, "nav"))
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 12})
	pump(v, v.Init(), 5)
	svc.AssertExpectations(t)
	assert.Equal(t, "1/1 VPCs", netFooter(v))
}
