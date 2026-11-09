package views

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

func newTestSubnetList(vpcID string) *SubnetList {
	m := new(awstest.MockEC2Service)
	view := NewSubnetList(m, vpcID)
	view.Update(tea.WindowSizeMsg{Width: 200, Height: 24})
	return view
}

func loadSubnets(view *SubnetList, subnets []aws.Subnet) {
	deliverItems(view, &view.listCore, subnets)
}

var testSubnet1 = aws.Subnet{
	ID: "subnet-aaa111", Name: "public-1a", VpcID: "vpc-aaa111",
	CIDRBlock: "10.0.1.0/24", AvailabilityZone: "us-east-1a", AvailabilityZoneID: "use1-az1",
	AvailableIPCount: 251, State: "available", MapPublicIPOnLaunch: true, DefaultForAZ: false,
	OwnerID: "123456789012", ARN: "arn:aws:ec2:us-east-1:123456789012:subnet/subnet-aaa111",
	Tags: map[string]string{"Name": "public-1a", "tier": "public"},
}

var testSubnet2 = aws.Subnet{
	ID: "subnet-bbb222", Name: "private-1b", VpcID: "vpc-aaa111",
	CIDRBlock: "10.0.2.0/24", AvailabilityZone: "us-east-1b", AvailabilityZoneID: "use1-az2",
	AvailableIPCount: 250, State: "available", MapPublicIPOnLaunch: false, DefaultForAZ: true,
	OwnerID: "123456789012",
}

// --- Load ---

func TestSubnetList_LoadedSubnetsPopulateTable(t *testing.T) {
	view := newTestSubnetList("")
	loadSubnets(view, []aws.Subnet{testSubnet1, testSubnet2})

	assert.False(t, view.loading)
	assert.Len(t, view.items, 2)
	_, total := view.table.RowCount()
	assert.Equal(t, 2, total)
}

// --- Detail ---

func TestSubnetList_EnterEmitsDetailPanel(t *testing.T) {
	view := newTestSubnetList("")
	loadSubnets(view, []aws.Subnet{testSubnet1})

	_, cmd := view.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	result := cmd()
	tabbed, ok := result.(msg.TabbedContentMsg)
	require.True(t, ok, "expected TabbedContentMsg, got %T", result)
	assert.Equal(t, "public-1a (subnet-aaa111)", tabbed.PanelTitle)
	assert.GreaterOrEqual(t, len(tabbed.Tabs), 3)
	assert.Equal(t, "Info", tabbed.Tabs[0].Title)
	assert.Equal(t, "Config", tabbed.Tabs[1].Title)
}

func TestSubnetList_DetailHasVPCLink(t *testing.T) {
	view := newTestSubnetList("")
	loadSubnets(view, []aws.Subnet{testSubnet1})

	_, cmd := view.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	result := cmd()
	tabbed := result.(msg.TabbedContentMsg)

	require.NotEmpty(t, tabbed.Tabs[0].Links)
	link := tabbed.Tabs[0].Links[0]
	assert.Equal(t, "vpc_list", link.ViewID)
	assert.Equal(t, "vpc-aaa111", link.Params["focus"])
}

func TestSubnetList_DetailHasTagsTab(t *testing.T) {
	view := newTestSubnetList("")
	loadSubnets(view, []aws.Subnet{testSubnet1})

	_, cmd := view.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	result := cmd()
	tabbed := result.(msg.TabbedContentMsg)
	assert.Equal(t, "Tags", tabbed.Tabs[len(tabbed.Tabs)-1].Title)
}

// --- Copy ID ---

func TestSubnetList_CopyID(t *testing.T) {
	view := newTestSubnetList("")
	loadSubnets(view, []aws.Subnet{testSubnet1})

	_, cmd := view.Update(keyPress('y'))
	require.NotNil(t, cmd)
}

// --- Refresh ---

func TestSubnetList_RefreshReloads(t *testing.T) {
	view := newTestSubnetList("")
	loadSubnets(view, []aws.Subnet{testSubnet1})

	_, cmd := view.Update(keyPress('r'))
	assert.NotNil(t, cmd)
	assert.True(t, view.loading)
}

// --- VPC-filtered mode ---

func TestSubnetList_FilteredByVPC_ID(t *testing.T) {
	view := newTestSubnetList("vpc-aaa111")
	assert.Equal(t, "subnet_list:vpc-aaa111", view.ID())
	assert.Equal(t, "Subnets (vpc-aaa111)", view.Title())
}

func TestSubnetList_Unfiltered_ID(t *testing.T) {
	view := newTestSubnetList("")
	assert.Equal(t, "subnet_list", view.ID())
	assert.Equal(t, "Subnets", view.Title())
}

// --- Responsive columns ---

func TestSubnetList_NarrowTierColumns(t *testing.T) {
	cols := subnetColumns(ui.TierNarrow)
	assert.Len(t, cols, 5)
	assert.Equal(t, "Subnet ID", cols[0].Title)
	assert.Equal(t, "State", cols[4].Title)
}

func TestSubnetList_MediumTierColumns(t *testing.T) {
	cols := subnetColumns(ui.TierMedium)
	assert.Len(t, cols, 5)
	assert.Equal(t, "CIDR Block", cols[3].Title)
	assert.Equal(t, "State", cols[4].Title)
}

// --- Footer ---

func TestSubnetList_Footer(t *testing.T) {
	view := newTestSubnetList("")
	loadSubnets(view, []aws.Subnet{testSubnet1, testSubnet2})

	assert.Contains(t, view.Footer(), "2/2 subnets")
}

// --- find ---

func TestSubnetList_FindSubnet(t *testing.T) {
	view := newTestSubnetList("")
	loadSubnets(view, []aws.Subnet{testSubnet1, testSubnet2})

	found := view.find("subnet-bbb222")
	require.NotNil(t, found)
	assert.Equal(t, "private-1b", found.Name)

	assert.Nil(t, view.find("subnet-nope"))
}

// --- resize ---

// Divergence #12 (fixed in PR 5): the narrow and medium tiers both have 5
// columns, so the widths must follow the tier, not the column count.
func TestSubnetList_ResizeWideMediumNarrowUsesTierWidths(t *testing.T) {
	view := newTestSubnetList("") // width 200: wide
	loadSubnets(view, []aws.Subnet{testSubnet1, testSubnet2})
	view.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	steps := []struct {
		width int
		tier  ui.WidthTier
	}{
		{200, ui.TierWide},
		{120, ui.TierMedium},
		{60, ui.TierNarrow},
		{120, ui.TierMedium},
	}
	for _, s := range steps {
		view.Update(tea.WindowSizeMsg{Width: s.width, Height: 24})
		assert.Equal(t, s.tier, view.widthTier, "width %d", s.width)
		assert.Equal(t, netCols(subnetColumns(s.tier)), netCols(view.table.Columns()), "width %d: column specs of the tier", s.width)

		fresh := newTestSubnetList("")
		fresh.Update(tea.WindowSizeMsg{Width: s.width, Height: 24})
		loadSubnets(fresh, []aws.Subnet{testSubnet1, testSubnet2})
		assert.Equal(t, netLines(fresh)[0], netLines(view)[0], "width %d: same header as a fresh view", s.width)
		assert.Equal(t, "subnet-bbb222", view.table.CursorKey(), "width %d: the cursor stays", s.width)
	}
}

// No line of the list is wider than the window. Before the narrow tier was
// the smallest set, the table was one column too wide at 80 columns and its
// header line wrapped. Below the width of the narrow set, DistributeWidths
// shrinks the columns and every list is a few columns too wide (L28).
func TestSubnetList_FitsTheWidth(t *testing.T) {
	for _, w := range []int{80, 100, 120, 160, 200} {
		view := newTestSubnetList("")
		view.Update(tea.WindowSizeMsg{Width: w, Height: 24})
		loadSubnets(view, []aws.Subnet{testSubnet1, testSubnet2})
		for i, line := range strings.Split(view.View().Content, "\n") {
			assert.LessOrEqual(t, ansi.StringWidth(line), w, "width %d, line %d", w, i)
		}
	}
}
