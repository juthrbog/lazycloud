package views

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

func newTestVPCList() (*VPCList, *awstest.MockEC2Service) {
	m := new(awstest.MockEC2Service)
	view := NewVPCList(m)
	view.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
	return view, m
}

func loadVPCs(view *VPCList, vpcs []aws.VPC) {
	view.Update(vpcPageLoadedMsg{vpcs: vpcs})
}

var testVPC1 = aws.VPC{
	ID: "vpc-aaa111", Name: "main-vpc", CIDRBlock: "10.0.0.0/16",
	State: "available", IsDefault: true, InstanceTenancy: "default",
	DHCPOptionsID: "dopt-abc", OwnerID: "123456789012",
	IPv4Associations: []aws.CIDRAssociation{{CIDRBlock: "10.0.0.0/16", State: "associated"}},
	Tags:             map[string]string{"Name": "main-vpc", "env": "prod"},
}

var testVPC2 = aws.VPC{
	ID: "vpc-bbb222", Name: "dev-vpc", CIDRBlock: "172.16.0.0/16",
	State: "available", IsDefault: false, InstanceTenancy: "default",
	OwnerID: "123456789012",
}

// --- Load ---

func TestVPCList_LoadedVPCsPopulateTable(t *testing.T) {
	view, _ := newTestVPCList()
	loadVPCs(view, []aws.VPC{testVPC1, testVPC2})

	assert.False(t, view.loading)
	assert.Len(t, view.vpcs, 2)
	_, total := view.table.RowCount()
	assert.Equal(t, 2, total)
}

// --- Detail ---

func TestVPCList_EnterEmitsDetailPanel(t *testing.T) {
	view, _ := newTestVPCList()
	loadVPCs(view, []aws.VPC{testVPC1})

	_, cmd := view.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	result := cmd()
	tabbed, ok := result.(msg.TabbedContentMsg)
	require.True(t, ok, "expected TabbedContentMsg, got %T", result)
	assert.Equal(t, "main-vpc (vpc-aaa111)", tabbed.PanelTitle)
	assert.GreaterOrEqual(t, len(tabbed.Tabs), 3)
	assert.Equal(t, "Info", tabbed.Tabs[0].Title)
	assert.Equal(t, "CIDR Blocks", tabbed.Tabs[1].Title)
	assert.Equal(t, "JSON", tabbed.Tabs[2].Title)
}

func TestVPCList_DetailIncludesSubnetsLink(t *testing.T) {
	view, _ := newTestVPCList()
	loadVPCs(view, []aws.VPC{testVPC1})

	_, cmd := view.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	result := cmd()
	tabbed := result.(msg.TabbedContentMsg)

	assert.Contains(t, tabbed.Tabs[0].Content, "View subnet")
	require.NotEmpty(t, tabbed.Tabs[0].Links)

	// Find the subnets link
	var found bool
	for _, link := range tabbed.Tabs[0].Links {
		if link.ViewID == "subnet_list" {
			assert.Equal(t, "vpc-aaa111", link.Params["vpc_id"])
			found = true
			break
		}
	}
	assert.True(t, found, "expected subnet_list link in info tab")
}

func TestVPCList_DetailHasTagsTab(t *testing.T) {
	view, _ := newTestVPCList()
	loadVPCs(view, []aws.VPC{testVPC1})

	_, cmd := view.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	result := cmd()
	tabbed := result.(msg.TabbedContentMsg)
	assert.Equal(t, "Tags", tabbed.Tabs[len(tabbed.Tabs)-1].Title)
}

// --- Copy ID ---

func TestVPCList_CopyID(t *testing.T) {
	view, _ := newTestVPCList()
	loadVPCs(view, []aws.VPC{testVPC1})

	_, cmd := view.Update(keyPress('y'))
	require.NotNil(t, cmd)
}

// --- Navigate to subnets ---

func TestVPCList_NavigateToSubnets(t *testing.T) {
	view, _ := newTestVPCList()
	loadVPCs(view, []aws.VPC{testVPC1})

	_, cmd := view.Update(keyPress('n'))
	require.NotNil(t, cmd)

	result := cmd()
	nav, ok := result.(msg.NavigateMsg)
	require.True(t, ok, "expected NavigateMsg, got %T", result)
	assert.Equal(t, "subnet_list", nav.ViewID)
	assert.Equal(t, "vpc-aaa111", nav.Params["vpc_id"])
}

// --- Refresh ---

func TestVPCList_RefreshReloads(t *testing.T) {
	view, _ := newTestVPCList()
	loadVPCs(view, []aws.VPC{testVPC1})

	_, cmd := view.Update(keyPress('r'))
	assert.NotNil(t, cmd)
	assert.True(t, view.loading)
}

// --- Responsive columns ---

func TestVPCList_NarrowTierColumns(t *testing.T) {
	cols := vpcColumns(ui.TierNarrow)
	assert.Equal(t, 4, len(cols))
	assert.Equal(t, "VPC ID", cols[0].Title)
	assert.Equal(t, "State", cols[3].Title)
}

func TestVPCList_MediumTierColumns(t *testing.T) {
	cols := vpcColumns(ui.TierMedium)
	assert.Equal(t, 6, len(cols))
	assert.Equal(t, "Tenancy", cols[5].Title)
}

// --- Footer ---

func TestVPCList_Footer(t *testing.T) {
	view, _ := newTestVPCList()
	loadVPCs(view, []aws.VPC{testVPC1, testVPC2})

	assert.Contains(t, view.Footer(), "2/2 VPCs")
}

// --- findVPC ---

func TestVPCList_FindVPC(t *testing.T) {
	view, _ := newTestVPCList()
	loadVPCs(view, []aws.VPC{testVPC1, testVPC2})

	found := view.findVPC("vpc-bbb222")
	require.NotNil(t, found)
	assert.Equal(t, "dev-vpc", found.Name)

	assert.Nil(t, view.findVPC("vpc-nope"))
}
