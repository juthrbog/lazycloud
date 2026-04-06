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

func newTestSGList() (*SGList, *awstest.MockEC2Service) {
	m := new(awstest.MockEC2Service)
	view := NewSGList(m)
	view.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
	return view, m
}

func loadSGs(view *SGList, groups []aws.SecurityGroup) {
	view.Update(sgListLoadedMsg{groups: groups})
}

var testSG1 = aws.SecurityGroup{
	ID: "sg-111", Name: "web-sg", Description: "Web servers", VpcID: "vpc-aaa", OwnerID: "123456789012",
	InboundRules: []aws.SecurityGroupRule{
		{Protocol: "tcp", FromPort: 80, ToPort: 80, CIDRs: []string{"0.0.0.0/0"}},
		{Protocol: "tcp", FromPort: 443, ToPort: 443, CIDRs: []string{"0.0.0.0/0"}},
	},
	OutboundRules: []aws.SecurityGroupRule{
		{Protocol: "-1", FromPort: -1, ToPort: -1, CIDRs: []string{"0.0.0.0/0"}},
	},
}

var testSG2 = aws.SecurityGroup{
	ID: "sg-222", Name: "db-sg", Description: "Database", VpcID: "vpc-aaa", OwnerID: "123456789012",
	InboundRules: []aws.SecurityGroupRule{
		{Protocol: "tcp", FromPort: 5432, ToPort: 5432, SGRefs: []string{"sg-111"}, Description: "Postgres from web tier"},
	},
	OutboundRules: []aws.SecurityGroupRule{},
}

// --- Load ---

func TestSGList_LoadedGroupsPopulateTable(t *testing.T) {
	view, _ := newTestSGList()
	loadSGs(view, []aws.SecurityGroup{testSG1, testSG2})

	assert.False(t, view.loading)
	assert.Len(t, view.groups, 2)
	_, total := view.table.RowCount()
	assert.Equal(t, 2, total)
}

// --- Detail ---

func TestSGList_EnterEmitsDetailPanel(t *testing.T) {
	view, _ := newTestSGList()
	loadSGs(view, []aws.SecurityGroup{testSG1})

	_, cmd := view.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	result := cmd()
	tabbed, ok := result.(msg.TabbedContentMsg)
	require.True(t, ok, "expected TabbedContentMsg, got %T", result)
	assert.Equal(t, "web-sg (sg-111)", tabbed.PanelTitle)
	// Info, Inbound, Outbound, JSON
	assert.GreaterOrEqual(t, len(tabbed.Tabs), 4)
	assert.Equal(t, "Info", tabbed.Tabs[0].Title)
	assert.Equal(t, "Inbound", tabbed.Tabs[1].Title)
	assert.Equal(t, "Outbound", tabbed.Tabs[2].Title)
	assert.Equal(t, "JSON", tabbed.Tabs[3].Title)
}

// --- Copy ID ---

func TestSGList_CopyID(t *testing.T) {
	view, _ := newTestSGList()
	loadSGs(view, []aws.SecurityGroup{testSG1})

	_, cmd := view.Update(keyPress('y'))
	require.NotNil(t, cmd)
}

// --- Refresh ---

func TestSGList_RefreshReloads(t *testing.T) {
	view, _ := newTestSGList()
	loadSGs(view, []aws.SecurityGroup{testSG1})

	_, cmd := view.Update(keyPress('r'))
	assert.NotNil(t, cmd)
	assert.True(t, view.loading)
}

// --- Responsive columns ---

func TestSGList_NarrowTierColumns(t *testing.T) {
	cols := sgColumns(ui.TierNarrow)
	assert.Equal(t, 5, len(cols))
	assert.Equal(t, "Group ID", cols[0].Title)
	assert.Equal(t, "Out", cols[4].Title)
}

func TestSGList_MediumTierColumns(t *testing.T) {
	cols := sgColumns(ui.TierMedium)
	assert.Equal(t, 6, len(cols))
	assert.Equal(t, "Description", cols[3].Title)
}

// --- findSG ---

func TestSGList_FindSG(t *testing.T) {
	view, _ := newTestSGList()
	loadSGs(view, []aws.SecurityGroup{testSG1, testSG2})

	found := view.findSG("sg-222")
	require.NotNil(t, found)
	assert.Equal(t, "db-sg", found.Name)

	assert.Nil(t, view.findSG("sg-nope"))
}

// --- Footer ---

func TestSGList_Footer(t *testing.T) {
	view, _ := newTestSGList()
	loadSGs(view, []aws.SecurityGroup{testSG1, testSG2})

	assert.Contains(t, view.Footer(), "2/2 security groups")
}

// --- Rule formatting ---

func TestFormatProtocol(t *testing.T) {
	assert.Equal(t, "All", formatProtocol("-1"))
	assert.Equal(t, "TCP", formatProtocol("tcp"))
	assert.Equal(t, "UDP", formatProtocol("udp"))
	assert.Equal(t, "ICMP", formatProtocol("icmp"))
	assert.Equal(t, "ICMPv6", formatProtocol("icmpv6"))
	assert.Equal(t, "47", formatProtocol("47"))
}

func TestFormatPorts(t *testing.T) {
	assert.Equal(t, "All", formatPorts("-1", -1, -1))
	assert.Equal(t, "80", formatPorts("tcp", 80, 80))
	assert.Equal(t, "80-443", formatPorts("tcp", 80, 443))
	assert.Equal(t, "All", formatPorts("icmp", -1, -1))
	assert.Equal(t, "Type 8", formatPorts("icmp", 8, -1))
}

func TestFormatRuleSources(t *testing.T) {
	r := aws.SecurityGroupRule{CIDRs: []string{"10.0.0.0/8"}, SGRefs: []string{"sg-abc"}}
	assert.Equal(t, "10.0.0.0/8, sg-abc", formatRuleSources(r))

	empty := aws.SecurityGroupRule{}
	assert.Equal(t, "-", formatRuleSources(empty))
}

func TestBuildRulesContent(t *testing.T) {
	rules := []aws.SecurityGroupRule{
		{Protocol: "tcp", FromPort: 80, ToPort: 80, CIDRs: []string{"0.0.0.0/0"}, Description: "HTTP"},
	}
	content := buildRulesContent(rules)
	assert.Contains(t, content, "TCP")
	assert.Contains(t, content, "80")
	assert.Contains(t, content, "0.0.0.0/0")
	assert.Contains(t, content, "HTTP")
}

func TestBuildRulesContent_Empty(t *testing.T) {
	content := buildRulesContent(nil)
	assert.Contains(t, content, "No rules")
}
