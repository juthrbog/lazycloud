package views

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// vpcSubnetsKey opens the subnets of the VPC under the cursor.
var vpcSubnetsKey = ui.NewHintBinding([]string{"n"}, "n", "subnets")

// VPCList displays EC2 VPCs.
type VPCList struct {
	listCore[aws.VPC]
}

// NewVPCList creates the VPC list view.
func NewVPCList(ec2 aws.EC2Service) *VPCList {
	return &VPCList{listCore: newListCore(listConfig[aws.VPC]{
		Noun:         "VPCs",
		LoadingLabel: "Loading VPCs...",
		ErrContext:   "listing VPCs",
		Key:          func(v aws.VPC) string { return v.ID },
		Columns:      vpcColumns,
		Row:          vpcRow,
		Fetch:        fetchPages(ec2, aws.EC2Service.ListVPCsPage, splitVPCPage),
		FetchTimeout: callTimeout,
		OpenDetail:   vpcDetail,
		Copy:         true,
	})}
}

func (v *VPCList) ID() string     { return "vpc_list" }
func (v *VPCList) Title() string  { return "VPCs" }
func (v *VPCList) Init() tea.Cmd  { return v.init() }
func (v *VPCList) Footer() string { return v.footer() }
func (v *VPCList) View() tea.View { return tea.NewView(v.render()) }

func (v *VPCList) KeyMap() []ui.HintBinding {
	return v.hints(listKeys.Details, listKeys.CopyID, vpcSubnetsKey,
		listKeys.Sort, listKeys.Filter, listKeys.Refresh)
}

// Update handles n (subnets of the cursor VPC) and hands the rest to the
// core. It returns the view itself, so the navigator keeps its identity.
func (v *VPCList) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := m.(tea.KeyPressMsg); ok && !v.CapturingInput() && key.Matches(k, vpcSubnetsKey.Binding) {
		id := v.table.CursorKey()
		if id == "" {
			return v, nil
		}
		return v, func() tea.Msg {
			return msg.NavigateMsg{ViewID: "subnet_list", Params: map[string]string{"vpc_id": id}}
		}
	}
	return v, v.update(m)
}

func splitVPCPage(p *aws.VPCPage) ([]aws.VPC, *string) {
	return p.VPCs, nextToken(p.HasMorePages, p.Token)
}

func vpcColumns(tier ui.WidthTier) []ui.Column {
	if tier == ui.TierNarrow {
		return []ui.Column{
			{Title: "VPC ID", Width: 21, Weight: 1, MaxWidth: 35},
			{Title: "Name", Width: 24, Weight: 2, MaxWidth: 60},
			{Title: "CIDR Block", Width: 18},
			{Title: "State", Width: 16, Style: ui.StateColor},
		}
	}
	return []ui.Column{
		{Title: "VPC ID", Width: 21, Weight: 1, MaxWidth: 35},
		{Title: "Name", Width: 24, Weight: 2, MaxWidth: 60},
		{Title: "CIDR Block", Width: 18},
		{Title: "State", Width: 16, Style: ui.StateColor},
		{Title: "Default", Width: 8},
		{Title: "Tenancy", Width: 12},
	}
}

// vpcRow builds the row of one VPC.
func vpcRow(vpc aws.VPC, tier ui.WidthTier) ui.Row {
	if tier == ui.TierNarrow {
		return ui.Row{Key: vpc.ID, Cells: table.Row{vpc.ID, vpc.Name, vpc.CIDRBlock, vpc.State}}
	}
	return ui.Row{
		Key:   vpc.ID,
		Cells: table.Row{vpc.ID, vpc.Name, vpc.CIDRBlock, vpc.State, yesNo(vpc.IsDefault), vpc.InstanceTenancy},
	}
}

// vpcDetail opens the detail panel of a VPC.
func vpcDetail(vpc aws.VPC) tea.Cmd {
	title := vpc.ID
	if vpc.Name != "" {
		title = vpc.Name + " (" + vpc.ID + ")"
	}
	infoContent, infoLinks := buildVPCInfoContent(&vpc)
	tabs := []msg.TabContent{
		{Title: "Info", Content: infoContent, Format: "text", Links: infoLinks},
		{Title: "CIDR Blocks", Content: buildVPCCIDRContent(&vpc), Format: "text"},
		{Title: "JSON", Content: vpc.DetailJSON(), Format: "json"},
	}
	if len(vpc.Tags) > 0 {
		tabs = append(tabs, msg.TabContent{
			Title: "Tags", Content: buildTagsContent(vpc.Tags), Format: "text",
		})
	}
	return func() tea.Msg {
		return msg.TabbedContentMsg{PanelTitle: title, Tabs: tabs}
	}
}

func buildVPCInfoContent(vpc *aws.VPC) (string, []msg.TabLink) {
	fields := []infoField{
		{K: "VPC ID", V: vpc.ID},
		{K: "Name", V: vpc.Name},
		{K: "State", V: vpc.State},
		{K: "CIDR Block", V: vpc.CIDRBlock},
		{K: "Default", V: yesNo(vpc.IsDefault)},
		{K: "Tenancy", V: vpc.InstanceTenancy},
		{K: "DHCP Options", V: vpc.DHCPOptionsID},
		{K: "Owner", V: vpc.OwnerID},
		{K: "Subnets", V: "View subnets", ViewID: "subnet_list", Params: map[string]string{"vpc_id": vpc.ID}},
	}
	return buildInfo(fields)
}

func buildVPCCIDRContent(vpc *aws.VPC) string {
	var b strings.Builder
	b.WriteString("IPv4 CIDR Blocks\n")
	b.WriteString("────────────────\n")
	if len(vpc.IPv4Associations) == 0 {
		b.WriteString("  " + vpc.CIDRBlock + "\n")
	} else {
		for _, a := range vpc.IPv4Associations {
			fmt.Fprintf(&b, "  %-20s %s\n", a.CIDRBlock, a.State)
		}
	}

	if len(vpc.IPv6Associations) > 0 {
		b.WriteString("\nIPv6 CIDR Blocks\n")
		b.WriteString("────────────────\n")
		for _, a := range vpc.IPv6Associations {
			fmt.Fprintf(&b, "  %-44s %s\n", a.IPv6CIDRBlock, a.State)
		}
	}

	return b.String()
}
