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

// PR 0 characterization tests for subnet_list. They record today's
// behavior. Only the subnet port PR (PR 5) may edit this file.

var charSubnetPub = aws.Subnet{
	ID: "subnet-aaa111", Name: "public-1a", VpcID: "vpc-aaa111",
	ARN:       "arn:aws:ec2:us-east-1:123456789012:subnet/subnet-aaa111",
	CIDRBlock: "10.0.1.0/24", AvailabilityZone: "us-east-1a", AvailabilityZoneID: "use1-az1",
	AvailableIPCount: 251, State: "available", MapPublicIPOnLaunch: true,
	AssignIPv6OnCreation: true, EnableDNS64: true,
	OwnerID:          "123456789012",
	IPv6Associations: []aws.IPv6CIDRAssociation{{IPv6CIDRBlock: "2600:1f18::/64", State: "associated"}},
	Tags:             map[string]string{"Name": "public-1a", "tier": "public"},
}

var charSubnetPriv = aws.Subnet{
	ID: "subnet-bbb222", Name: "private-1b", VpcID: "vpc-aaa111",
	CIDRBlock: "10.0.2.0/24", AvailabilityZone: "us-east-1b",
	AvailableIPCount: 250, State: "pending", DefaultForAZ: true, IPv6Native: true,
}

// newCharSubnet returns a subnet list for vpcID at width 200 with a mock
// that returns subnets as one page, and the load already delivered.
func newCharSubnet(t *testing.T, vpcID string, subnets ...aws.Subnet) (*SubnetList, *awstest.MockEC2Service) {
	t.Helper()
	svc := new(awstest.MockEC2Service)
	svc.On("ListSubnetsPage", mock.Anything, netNoToken, vpcID).Return(&aws.SubnetPage{Subnets: subnets}, nil)
	v := NewSubnetList(svc, vpcID)
	v.Update(tea.WindowSizeMsg{Width: 200, Height: 12})
	pump(v, v.Init(), 5)
	return v, svc
}

func TestCharSubnet_IDAndTitle(t *testing.T) {
	all := NewSubnetList(nil, "")
	assert.Equal(t, "subnet_list", all.ID())
	assert.Equal(t, "Subnets", all.Title())

	one := NewSubnetList(nil, "vpc-aaa111")
	assert.Equal(t, "subnet_list:vpc-aaa111", one.ID())
	assert.Equal(t, "Subnets (vpc-aaa111)", one.Title())
}

// H4: two subnet lists for different VPCs have different IDs (the app routes
// by ID) and each one asks AWS for its own VPC.
func TestCharSubnet_TwoVPCsRouteSeparately(t *testing.T) {
	a, svcA := newCharSubnet(t, "vpc-a", aws.Subnet{ID: "subnet-a", VpcID: "vpc-a"})
	b, svcB := newCharSubnet(t, "vpc-b", aws.Subnet{ID: "subnet-b", VpcID: "vpc-b"})
	assert.NotEqual(t, a.ID(), b.ID())
	svcA.AssertCalled(t, "ListSubnetsPage", mock.Anything, netNoToken, "vpc-a")
	svcB.AssertCalled(t, "ListSubnetsPage", mock.Anything, netNoToken, "vpc-b")
	assert.Equal(t, []string{"Copied: subnet-a"}, netCopied(a))
	assert.Equal(t, []string{"Copied: subnet-b"}, netCopied(b))

	all, svc := newCharSubnet(t, "", charSubnetPub)
	svc.AssertCalled(t, "ListSubnetsPage", mock.Anything, netNoToken, "")
	assert.Equal(t, "1/1 subnets", netFooter(all))
}

func TestCharSubnet_KeyMap(t *testing.T) {
	want := []string{
		`enter | enter/d | details | mode=0 | cat=""`,
		`y | y | copy ID | mode=0 | cat=""`,
		`s | s/S | sort | mode=0 | cat=""`,
		`/ | / | filter | mode=0 | cat=""`,
		`r | r | refresh | mode=0 | cat=""`,
	}
	assert.Equal(t, want, netKeyMap(NewSubnetList(nil, "").KeyMap()))
	assert.Equal(t, want, netKeyMap(NewSubnetList(nil, "vpc-1").KeyMap()))
}

func TestCharSubnet_CapturingInputWhileFilterOpen(t *testing.T) {
	v, _ := newCharSubnet(t, "", charSubnetPub, charSubnetPriv)
	assert.False(t, v.CapturingInput())
	v.Update(keyPress('/'))
	assert.True(t, v.CapturingInput())
	assert.Equal(t, []tea.Msg{ui.FilterChangedMsg{Text: "r"}}, netType(v, keyPress('r')), "r is a filter letter now, not refresh")
	assert.Equal(t, "2/2 subnets", netFooter(v), "no reload started")
	v.Update(ui.FilterChangedMsg{Text: "private"})
	assert.Equal(t, "1/2 subnets", netFooter(v))
	v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.False(t, v.CapturingInput(), "enter closes the filter and keeps the text")
	assert.Equal(t, "1/2 subnets", netFooter(v))

	v.Update(keyPress('/'))
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.False(t, v.CapturingInput())
	assert.Equal(t, []tea.Msg{ui.FilterChangedMsg{Text: ""}}, collectMsgs(cmd), "esc closes the filter, not the view")
}

func TestCharSubnet_InitLoadsOnlyWhileLoading(t *testing.T) {
	v, svc := newCharSubnet(t, "", charSubnetPub)
	svc.AssertNumberOfCalls(t, "ListSubnetsPage", 1)
	assert.Nil(t, v.Init(), "a cached view does not reload")
	svc.AssertNumberOfCalls(t, "ListSubnetsPage", 1)
}

func TestCharSubnet_FirstLoadViewAndFooter(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListSubnetsPage", mock.Anything, netNoToken, "").Return(&aws.SubnetPage{Subnets: []aws.Subnet{charSubnetPub, charSubnetPriv}}, nil)
	v := NewSubnetList(svc, "")
	v.Update(tea.WindowSizeMsg{Width: 200, Height: 12})

	assert.Equal(t, "\n  ⣾  Loading subnets...", netView(v))
	assert.Equal(t, "0/0 subnets  ⣾  Loading subnets...", netFooter(v))

	pump(v, v.Init(), 5)
	assert.Equal(t, "Subnet ID | Name | VPC ID | AZ | CIDR Block | Avail IPs | State | Public | Default", netHeader(v))
	assert.Equal(t, []string{
		"subnet-aaa111 | public-1a | vpc-aaa111 | us-east-1a | 10.0.1.0/24 | 251 | " + ui.IconRunning.Icon() + " available | Public | No",
		"subnet-bbb222 | private-1b | vpc-aaa111 | us-east-1b | 10.0.2.0/24 | 250 | " + ui.IconPending.Icon() + " pending | Private | Yes",
	}, netRows(v))
	assert.Equal(t, "2/2 subnets", netFooter(v))
}

func TestCharSubnet_LiteralPageAcceptedOnFreshView(t *testing.T) {
	for _, page := range []int{0, 1} {
		v := NewSubnetList(new(awstest.MockEC2Service), "")
		v.Update(tea.WindowSizeMsg{Width: 200, Height: 12})
		v.Update(subnetPageLoadedMsg{subnets: []aws.Subnet{charSubnetPub}, pageNum: page}) // private
		assert.Equal(t, "1/1 subnets", netFooter(v), "page %d", page)
	}
}

func TestCharSubnet_ColumnsPerWidth(t *testing.T) {
	cases := map[int]string{
		60:  "Subnet ID | Name | AZ | CIDR Block | State",
		100: "Subnet ID | Name | AZ | CIDR Block | State",
		160: "Subnet ID | Name | AZ | CIDR Block | State",
		200: "Subnet ID | Name | VPC ID | AZ | CIDR Block | Avail IPs | State | Public | Default",
	}
	for w, want := range cases {
		v, _ := newCharSubnet(t, "", charSubnetPub)
		v.Update(tea.WindowSizeMsg{Width: w, Height: 12})
		assert.Equal(t, want, netHeader(v), "width %d", w)
	}

	type col struct {
		title                   string
		width, weight, maxWidth int
	}
	flat := func(cols []ui.Column) []col {
		out := make([]col, 0, len(cols))
		for _, c := range cols {
			out = append(out, col{c.Title, c.Width, c.Weight, c.MaxWidth})
			assert.Equal(t, c.Title == "State", c.Style != nil, "only State has a Style")
		}
		return out
	}
	assert.Equal(t, []col{
		{"Subnet ID", 24, 1, 35}, {"Name", 24, 2, 60}, {"AZ", 14, 0, 0},
		{"CIDR Block", 18, 0, 0}, {"State", 16, 0, 0},
	}, flat(subnetColumns(ui.TierNarrow))) // private
	assert.Equal(t, []col{
		{"Subnet ID", 15, 1, 35}, {"Name", 15, 2, 60}, {"AZ", 13, 0, 0},
		{"CIDR Block", 15, 0, 0}, {"State", 10, 0, 0},
	}, flat(subnetColumns(ui.TierMedium))) // private
	assert.Equal(t, []col{
		{"Subnet ID", 24, 1, 35}, {"Name", 24, 2, 60}, {"VPC ID", 21, 1, 35},
		{"AZ", 14, 0, 0}, {"CIDR Block", 18, 0, 0}, {"Avail IPs", 10, 0, 0},
		{"State", 16, 0, 0}, {"Public", 8, 0, 0}, {"Default", 8, 0, 0},
	}, flat(subnetColumns(ui.TierWide))) // private
}

// The narrow and medium tiers have 5 columns each, and the view looks only
// at the column count, so the widths depend on the resize history.
func TestCharSubnet_TierChangeByColumnCount(t *testing.T) {
	header := func(widths ...int) string {
		svc := new(awstest.MockEC2Service)
		svc.On("ListSubnetsPage", mock.Anything, netNoToken, "").Return(&aws.SubnetPage{Subnets: []aws.Subnet{charSubnetPub}}, nil)
		v := NewSubnetList(svc, "")
		for _, w := range widths {
			v.Update(tea.WindowSizeMsg{Width: w, Height: 12})
		}
		pump(v, v.Init(), 5)
		return netLines(v)[0]
	}
	wideToNarrow := header(200, 60)
	viaMedium := header(200, 100, 60)
	fresh := header(60)
	// Divergence #12: changes in PR 5. All three should use the narrow
	// widths; only the wide-to-narrow path does today.
	assert.Equal(t, " Subnet ID      Name           AZ        CIDR Block  State", wideToNarrow, "narrow widths")
	assert.Equal(t, " Subnet ID     Name          AZ          CIDR Block    State", viaMedium, "medium widths kept at width 60")
	assert.Equal(t, fresh, viaMedium, "a fresh view at width 60 keeps the medium widths too")
	assert.Equal(t, " Subnet ID               Name                            AZ             CIDR Block       State", header(100))
	assert.Equal(t, header(200, 100), header(100), "medium at width 100")
}

func TestCharSubnet_StateCellPlainAndColoredByColumn(t *testing.T) {
	v, _ := newCharSubnet(t, "", charSubnetPub, charSubnetPriv)
	assert.Contains(t, v.View().Content, fgSGR(ui.ActiveTheme.StateRunning))
	v.Update(ui.FilterChangedMsg{Text: ui.IconRunning.Icon()})
	assert.Equal(t, "0/2 subnets", netFooter(v), "the icon is not in the cell")
	v.Update(ui.FilterChangedMsg{Text: "pending"})
	assert.Equal(t, "1/2 subnets", netFooter(v))
}

func TestCharSubnet_AvailIPsSortAsText(t *testing.T) {
	many := aws.Subnet{ID: "subnet-many", AvailableIPCount: 1000}
	few := aws.Subnet{ID: "subnet-few", AvailableIPCount: 999}
	v, _ := newCharSubnet(t, "", few, many)
	v.Update(ui.PickerResultMsg{ID: "sort", Selected: 5, Value: "Avail IPs"})
	// Divergence #46: changes in PR 3. "1000" sorts before "999" as text.
	assert.Equal(t, []string{"subnet-many | 1000 | Private | No", "subnet-few | 999 | Private | No"}, netRows(v))
}

func TestCharSubnet_Paging(t *testing.T) {
	tok := "t2"
	svc := new(awstest.MockEC2Service)
	svc.On("ListSubnetsPage", mock.Anything, netNoToken, "vpc-aaa111").Return(&aws.SubnetPage{
		Subnets: []aws.Subnet{charSubnetPub}, HasMorePages: true, Token: &tok,
	}, nil)
	svc.On("ListSubnetsPage", mock.Anything, &tok, "vpc-aaa111").Return(&aws.SubnetPage{
		Subnets: []aws.Subnet{charSubnetPub, charSubnetPriv},
	}, nil)
	v := NewSubnetList(svc, "vpc-aaa111")
	v.Update(tea.WindowSizeMsg{Width: 200, Height: 12})

	_, next := v.Update(netLoadMsg(v.Init()))
	require.NotNil(t, next)
	// Divergence #5: changes in PR 5. No "N so far" progress label today.
	assert.Equal(t, "1/1 subnets  ⣾  Loading subnets...", netFooter(v))

	_, done := v.Update(netLoadMsg(next))
	assert.Nil(t, done)
	// Divergence #14: changes in PR 5. The table drops the duplicate row.
	assert.Equal(t, "2/2 subnets", netFooter(v))
	svc.AssertNumberOfCalls(t, "ListSubnetsPage", 2)
}

func TestCharSubnet_StalePageDropped(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListSubnetsPage", mock.Anything, netNoToken, "").Return(&aws.SubnetPage{Subnets: []aws.Subnet{charSubnetPub}}, nil).Once()
	svc.On("ListSubnetsPage", mock.Anything, netNoToken, "").Return(&aws.SubnetPage{Subnets: []aws.Subnet{charSubnetPriv}}, nil).Once()
	v := NewSubnetList(svc, "")
	v.Update(tea.WindowSizeMsg{Width: 200, Height: 12})

	_, first := v.Update(keyPress('r'))
	_, second := v.Update(keyPress('r'))
	old := netLoadMsg(first)
	cur := netLoadMsg(second)
	deliverAll(v, cur, old)
	assert.Equal(t, "1/1 subnets", netFooter(v))
	assert.Equal(t, []string{"Copied: subnet-bbb222"}, netCopied(v))
}

func TestCharSubnet_RefreshClearsItems(t *testing.T) {
	v, svc := newCharSubnet(t, "", charSubnetPub, charSubnetPriv)
	_, cmd := v.Update(keyPress('r'))
	require.NotNil(t, cmd)
	// Divergence #7: changes in PR 5.
	assert.Equal(t, "\n  ⣾  Loading subnets...", netView(v))
	assert.Equal(t, "2/2 subnets  ⣾  Loading subnets...", netFooter(v))
	_, enter := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Nil(t, enter, "Divergence #7: details do nothing during a reload")

	v.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	assert.Equal(t, "0/0 subnets  ⣾  Loading subnets...", netFooter(v), "Divergence #7: a tier change mid-reload empties the table")

	pump(v, cmd, 5)
	svc.AssertNumberOfCalls(t, "ListSubnetsPage", 2)
	assert.Equal(t, "2/2 subnets", netFooter(v))
}

func TestCharSubnet_NavKeysFrozenDuringReload(t *testing.T) {
	v, _ := newCharSubnet(t, "", charSubnetPub, charSubnetPriv)
	_, cmd := v.Update(keyPress('r'))
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	// Divergence #6: changes in PR 5.
	assert.Equal(t, []string{"Copied: subnet-aaa111"}, netCopied(v))
	pump(v, cmd, 5)
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	assert.Equal(t, []string{"Copied: subnet-bbb222"}, netCopied(v))
}

func TestCharSubnet_ErrorContextAndView(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListSubnetsPage", mock.Anything, netNoToken, "vpc-1").Return(nil, errors.New("boom\x1b[31m"))
	v := NewSubnetList(svc, "vpc-1")
	v.Update(tea.WindowSizeMsg{Width: 200, Height: 12})
	em, ok := firstOfType[msg.ErrorMsg](pump(v, v.Init(), 5))
	require.True(t, ok)
	assert.Equal(t, "listing subnets", em.Context)
	assert.Equal(t, "\n  Error: boom␛[31m", netView(v))
	assert.Contains(t, v.View().Content, fgSGR(ui.ActiveTheme.Error))
	assert.Equal(t, "0/0 subnets", netFooter(v))
}

func TestCharSubnet_NilServiceGivesErrorMsg(t *testing.T) {
	v := NewSubnetList(nil, "vpc-1")
	v.Update(tea.WindowSizeMsg{Width: 200, Height: 12})
	em, ok := firstOfType[msg.ErrorMsg](pump(v, v.Init(), 5))
	require.True(t, ok)
	assert.Equal(t, "EC2", em.Context)
	require.EqualError(t, em.Err, "AWS client not initialized")
	assert.Equal(t, "\n  Error: AWS client not initialized", netView(v))
}

func TestCharSubnet_ErrorClearedOnlyByRefresh(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListSubnetsPage", mock.Anything, netNoToken, "").Return(&aws.SubnetPage{Subnets: []aws.Subnet{charSubnetPub}}, nil)
	v := NewSubnetList(svc, "")
	v.Update(tea.WindowSizeMsg{Width: 200, Height: 12})
	load := netLoadMsg(v.Init())
	v.Update(msg.ErrorMsg{Err: errors.New("denied")})
	v.Update(load)
	// Divergence #8: changes in PR 5.
	assert.Equal(t, "\n  Error: denied", netView(v))
	assert.Equal(t, "1/1 subnets", netFooter(v))

	pump(v, func() tea.Msg { return keyPress('r') }, 5)
	assert.Equal(t, []string{
		"subnet-aaa111 | public-1a | vpc-aaa111 | us-east-1a | 10.0.1.0/24 | 251 | " + ui.IconRunning.Icon() + " available | Public | No",
	}, netRows(v))
}

func TestCharSubnet_StaleErrorApplied(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListSubnetsPage", mock.Anything, netNoToken, "").Return(nil, errors.New("old failure")).Once()
	svc.On("ListSubnetsPage", mock.Anything, netNoToken, "").Return(&aws.SubnetPage{Subnets: []aws.Subnet{charSubnetPub}}, nil).Once()
	v := NewSubnetList(svc, "")
	v.Update(tea.WindowSizeMsg{Width: 200, Height: 12})
	_, first := v.Update(keyPress('r'))
	_, second := v.Update(keyPress('r'))
	old := netLoadMsg(first)
	cur := netLoadMsg(second)
	deliverAll(v, cur, old)
	// Divergence #9: changes in PR 5.
	assert.Equal(t, "\n  Error: old failure", netView(v))
}

func TestCharSubnet_SortPicker(t *testing.T) {
	v, _ := newCharSubnet(t, "", charSubnetPub, charSubnetPriv)
	_, cmd := v.Update(keyPress('s'))
	assert.Equal(t, []tea.Msg{msg.RequestSortPickerMsg{
		Columns:    []string{"Subnet ID", "Name", "VPC ID", "AZ", "CIDR Block", "Avail IPs", "State", "Public", "Default"},
		CurrentCol: -1,
	}}, collectMsgs(cmd))

	v.Update(ui.PickerResultMsg{ID: "other", Selected: 1, Value: "Name"})
	assert.Equal(t, "subnet-aaa111", netRows(v)[0][:13], "a picker with another ID is ignored")

	v.Update(ui.PickerResultMsg{ID: "sort", Selected: 1, Value: "Name"})
	assert.Equal(t, "subnet-bbb222", netRows(v)[0][:13])
	_, cmd = v.Update(keyPress('s'))
	req, ok := firstOfType[msg.RequestSortPickerMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, 1, req.CurrentCol)

	v.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	assert.Equal(t, "Subnet ID | Name ▲ | AZ | CIDR Block | State", netHeader(v), "the sort survives a tier change")
	assert.Equal(t, "subnet-bbb222", netRows(v)[0][:13])

	v.Update(ui.PickerResultMsg{ID: "sort", Selected: -1})
	assert.Equal(t, "subnet-bbb222", netRows(v)[0][:13], "a cancelled picker keeps the sort")

	v.Update(keyPress('S'))
	assert.Equal(t, "Subnet ID | Name ▼ | AZ | CIDR Block | State", netHeader(v))
	assert.Equal(t, "subnet-aaa111", netRows(v)[0][:13], "S reverses the sort")

	v.Update(ui.PickerResultMsg{ID: "sort", Value: "_clear"})
	assert.Equal(t, "Subnet ID | Name | AZ | CIDR Block | State", netHeader(v))
	assert.Equal(t, "subnet-aaa111", netRows(v)[0][:13])
	_, cmd = v.Update(keyPress('s'))
	req, _ = firstOfType[msg.RequestSortPickerMsg](collectMsgs(cmd))
	assert.Equal(t, -1, req.CurrentCol, "_clear clears the sort")
}

func TestCharSubnet_CopySpaceEsc(t *testing.T) {
	v, _ := newCharSubnet(t, "", charSubnetPub, charSubnetPriv)
	_, cmd := v.Update(keyPress('y'))
	msgs := collectMsgs(cmd)
	require.Len(t, msgs, 2, "clipboard + toast")
	assert.Equal(t, []msg.ToastMsg{msg.ToastSuccess("Copied: subnet-aaa111")}, toasts(msgs))

	before := netView(v)
	_, cmd = v.Update(keyPress(' '))
	assert.Empty(t, collectMsgs(cmd), "space does nothing")
	assert.Equal(t, before, netView(v))
	assert.NotContains(t, netView(v), "✓")

	_, cmd = v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Equal(t, []tea.Msg{msg.NavigateBackMsg{}}, collectMsgs(cmd))

	empty, _ := newCharSubnet(t, "")
	assert.Empty(t, netCopied(empty))
}

func TestCharSubnet_DetailTabs(t *testing.T) {
	v, _ := newCharSubnet(t, "", charSubnetPub, charSubnetPriv)
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	tm, ok := firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "public-1a (subnet-aaa111)", tm.PanelTitle)
	require.Len(t, tm.Tabs, 5)

	assert.Equal(t, msg.TabContent{
		Title: "Info", Format: "text",
		Content: "Subnet ID        subnet-aaa111\n" +
			"Name             public-1a\n" +
			"ARN              arn:aws:ec2:us-east-1:123456789012:subnet/subnet-aaa111\n" +
			"VPC              vpc-aaa111\n" +
			"AZ               us-east-1a\n" +
			"AZ ID            use1-az1\n" +
			"CIDR Block       10.0.1.0/24\n" +
			"Available IPs    251\n" +
			"State            available\n" +
			"Owner            123456789012\n",
		Links: []msg.TabLink{{Line: 3, ViewID: "vpc_list", Params: map[string]string{"focus": "vpc-aaa111"}}},
	}, tm.Tabs[0])
	assert.Equal(t, msg.TabContent{
		Title: "Config", Format: "text",
		Content: "Public IP on Launch      Yes\n" +
			"Assign IPv6              Yes\n" +
			"Default for AZ           No\n" +
			"DNS64 Enabled            Yes\n" +
			"IPv6 Native              No\n",
	}, tm.Tabs[1])
	assert.Equal(t, msg.TabContent{
		Title: "IPv6", Format: "text",
		Content: "2600:1f18::/64                               associated\n",
	}, tm.Tabs[2])
	assert.Equal(t, msg.TabContent{Title: "JSON", Format: "json", Content: charSubnetPub.DetailJSON()}, tm.Tabs[3])
	assert.Equal(t, msg.TabContent{
		Title: "Tags", Format: "text",
		Content: "Name                     public-1a\n" +
			"tier                     public\n",
	}, tm.Tabs[4])

	// d opens the same panel. No IPv6 and no tags: Info, Config, JSON.
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd = v.Update(keyPress('d'))
	tm, ok = firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "private-1b (subnet-bbb222)", tm.PanelTitle)
	titles := make([]string, 0, len(tm.Tabs))
	for _, tab := range tm.Tabs {
		titles = append(titles, tab.Title)
	}
	assert.Equal(t, []string{"Info", "Config", "JSON"}, titles)
	assert.Equal(t, []msg.TabLink{{Line: 2, ViewID: "vpc_list", Params: map[string]string{"focus": "vpc-aaa111"}}}, tm.Tabs[0].Links)
	assert.Equal(t, "Public IP on Launch      No\n"+
		"Assign IPv6              No\n"+
		"Default for AZ           Yes\n"+
		"DNS64 Enabled            No\n"+
		"IPv6 Native              Yes\n", tm.Tabs[1].Content)
}

func TestCharSubnet_PanelTitleWithoutNameAndZeroIPs(t *testing.T) {
	v, _ := newCharSubnet(t, "", aws.Subnet{ID: "subnet-noname"})
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	tm, ok := firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "subnet-noname", tm.PanelTitle)
	assert.Equal(t, "Subnet ID        subnet-noname\nAvailable IPs    0\n", tm.Tabs[0].Content, "Available IPs always shows")
	assert.Empty(t, tm.Tabs[0].Links)
}

func TestCharSubnet_DetailWithNoRowFallsThrough(t *testing.T) {
	v, _ := newCharSubnet(t, "")
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Empty(t, collectMsgs(cmd))
}

func TestCharSubnet_Focus(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListSubnetsPage", mock.Anything, netNoToken, "").Return(&aws.SubnetPage{Subnets: []aws.Subnet{charSubnetPub, charSubnetPriv}}, nil)
	v := NewSubnetList(svc, "")
	v.Update(tea.WindowSizeMsg{Width: 200, Height: 12})
	load := netLoadMsg(v.Init())

	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "subnet-bbb222"})
	assert.Nil(t, cmd, "focus while loading is stored")
	_, cmd = v.Update(load)
	tm, ok := firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "private-1b (subnet-bbb222)", tm.PanelTitle)
	assert.Equal(t, []string{"Copied: subnet-bbb222"}, netCopied(v))

	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: "subnet-aaa111"})
	tm, ok = firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
	require.True(t, ok)
	assert.Equal(t, "public-1a (subnet-aaa111)", tm.PanelTitle)

	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: "subnet-missing"})
	assert.Nil(t, cmd, "focus on a missing ID does nothing")
	assert.Equal(t, []string{"Copied: subnet-aaa111"}, netCopied(v), "the cursor stays")

	v.Update(ui.FilterChangedMsg{Text: "private"})
	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: "subnet-aaa111"})
	_, ok = firstOfType[msg.TabbedContentMsg](collectMsgs(cmd))
	assert.True(t, ok, "Divergence #22 (keep): the panel opens")
	assert.Equal(t, []string{"Copied: subnet-bbb222"}, netCopied(v), "the cursor stays on the visible row")
}

func TestCharSubnet_PendingFocusSurvivesError(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListSubnetsPage", mock.Anything, netNoToken, "").Return(&aws.SubnetPage{Subnets: []aws.Subnet{charSubnetPub, charSubnetPriv}}, nil)
	v := NewSubnetList(svc, "")
	v.Update(tea.WindowSizeMsg{Width: 200, Height: 12})
	v.Update(msg.FocusResourceMsg{ResourceID: "subnet-bbb222"})
	v.Update(msg.ErrorMsg{Err: errors.New("x")})
	_, cmd := v.Update(keyPress('r'))
	// Divergence #51 (keep).
	tm, ok := firstOfType[msg.TabbedContentMsg](pump(v, cmd, 5))
	require.True(t, ok)
	assert.Equal(t, "private-1b (subnet-bbb222)", tm.PanelTitle)
}

func TestCharSubnet_UpdateReturnsSamePointer(t *testing.T) {
	v, _ := newCharSubnet(t, "vpc-aaa111", charSubnetPub)
	_, first := v.Update(keyPress('r'))
	_, second := v.Update(keyPress('r'))
	stale := netLoadMsg(first)
	cur := netLoadMsg(second)
	for _, m := range []tea.Msg{
		spinner.TickMsg{},
		tea.WindowSizeMsg{Width: 100, Height: 12},
		keyPress('s'), keyPress('S'), keyPress('y'), keyPress('d'), keyPress(' '),
		tea.KeyPressMsg{Code: tea.KeyDown},
		ui.PickerResultMsg{ID: "sort", Selected: 0},
		ui.FilterChangedMsg{Text: "x"},
		msg.FocusResourceMsg{ResourceID: "subnet-aaa111"},
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

func TestCharSubnet_SetContextReachesFetch(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("ListSubnetsPage", mock.MatchedBy(func(ctx context.Context) bool {
		_, hasDeadline := ctx.Deadline()
		return ctx.Value(netCtxKey{}) == "nav" && hasDeadline
	}), netNoToken, "vpc-1").Return(&aws.SubnetPage{Subnets: []aws.Subnet{charSubnetPub}}, nil)
	v := NewSubnetList(svc, "vpc-1")
	v.SetContext(context.WithValue(t.Context(), netCtxKey{}, "nav"))
	v.Update(tea.WindowSizeMsg{Width: 200, Height: 12})
	pump(v, v.Init(), 5)
	svc.AssertExpectations(t)
	assert.Equal(t, "1/1 subnets", netFooter(v))
}
