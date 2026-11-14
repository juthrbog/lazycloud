package views

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// SubnetList displays EC2 subnets, optionally filtered by VPC.
type SubnetList struct {
	listCore[aws.Subnet]
	vpcID string // optional VPC filter; part of the view ID
}

// NewSubnetList creates the subnet list view.
// When vpcID is non-empty, only subnets in that VPC are shown.
func NewSubnetList(ec2 aws.EC2Service, vpcID string) *SubnetList {
	// The func literal takes the service, so fetchPages can check it for nil.
	list := func(svc aws.EC2Service, ctx context.Context, token *string) (*aws.SubnetPage, error) {
		return svc.ListSubnetsPage(ctx, token, vpcID)
	}
	return &SubnetList{
		vpcID: vpcID,
		listCore: newListCore(listConfig[aws.Subnet]{
			Noun:         "subnets",
			LoadingLabel: "Loading subnets...",
			ErrContext:   "listing subnets",
			Key:          func(s aws.Subnet) string { return s.ID },
			Columns:      subnetColumns,
			Row:          subnetRow,
			Fetch:        fetchPages(ec2, list, splitSubnetPage),
			FetchTimeout: callTimeout,
			OpenDetail:   subnetDetail,
			Copy:         true,
		}),
	}
}

func (s *SubnetList) ID() string {
	if s.vpcID != "" {
		return "subnet_list:" + s.vpcID
	}
	return "subnet_list"
}

func (s *SubnetList) Title() string {
	if s.vpcID != "" {
		return "Subnets (" + s.vpcID + ")"
	}
	return "Subnets"
}

func (s *SubnetList) Init() tea.Cmd  { return s.init() }
func (s *SubnetList) Footer() string { return s.footer() }
func (s *SubnetList) View() tea.View { return tea.NewView(s.render("", "")) }

func (s *SubnetList) KeyMap() []ui.HintBinding {
	return s.hints(listKeys.Details, listKeys.CopyID, listKeys.Sort, listKeys.Filter, listKeys.Refresh)
}

// Update returns the view itself, so the navigator keeps its identity.
func (s *SubnetList) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	return s, s.update(m)
}

func splitSubnetPage(p *aws.SubnetPage) ([]aws.Subnet, *string) {
	return p.Subnets, nextToken(p.HasMorePages, p.Token)
}

func subnetColumns(tier ui.WidthTier) []ui.Column {
	// Narrow is the fallback when no other tier fits, so it is the
	// smallest set. Medium has the same columns with more room.
	if tier == ui.TierNarrow {
		return []ui.Column{
			{Title: "Subnet ID", Width: 15, Weight: 1, MaxWidth: 35},
			{Title: "Name", Width: 15, Weight: 2, MaxWidth: 60},
			{Title: "AZ", Width: 13},
			{Title: "CIDR Block", Width: 15},
			{Title: "State", Width: 10, Style: ui.StateColor},
		}
	}
	if tier == ui.TierMedium {
		return []ui.Column{
			{Title: "Subnet ID", Width: 24, Weight: 1, MaxWidth: 35},
			{Title: "Name", Width: 24, Weight: 2, MaxWidth: 60},
			{Title: "AZ", Width: 14},
			{Title: "CIDR Block", Width: 18},
			{Title: "State", Width: 16, Style: ui.StateColor},
		}
	}
	return []ui.Column{
		{Title: "Subnet ID", Width: 24, Weight: 1, MaxWidth: 35},
		{Title: "Name", Width: 24, Weight: 2, MaxWidth: 60},
		{Title: "VPC ID", Width: 21, Weight: 1, MaxWidth: 35},
		{Title: "AZ", Width: 14},
		{Title: "CIDR Block", Width: 18},
		{Title: "Avail IPs", Width: 10},
		{Title: "State", Width: 16, Style: ui.StateColor},
		{Title: "Public", Width: 8},
		{Title: "Default", Width: 8},
	}
}

// subnetRow builds the row of one subnet. Avail IPs sorts as a number.
func subnetRow(s aws.Subnet, tier ui.WidthTier) ui.Row {
	if tier == ui.TierNarrow || tier == ui.TierMedium {
		return ui.Row{Key: s.ID, Cells: table.Row{s.ID, s.Name, s.AvailabilityZone, s.CIDRBlock, s.State}}
	}
	pub := "Private"
	if s.MapPublicIPOnLaunch {
		pub = "Public"
	}
	def := yesNo(s.DefaultForAZ)
	return ui.Row{
		Key: s.ID,
		Cells: table.Row{
			s.ID, s.Name, s.VpcID, s.AvailabilityZone, s.CIDRBlock,
			fmt.Sprintf("%d", s.AvailableIPCount), s.State, pub, def,
		},
		Sort: table.Row{
			s.ID, s.Name, s.VpcID, s.AvailabilityZone, s.CIDRBlock,
			ui.SortKeyInt(int64(s.AvailableIPCount)), s.State, pub, def,
		},
	}
}

// subnetDetail opens the detail panel of a subnet.
func subnetDetail(subnet aws.Subnet) tea.Cmd {
	title := subnet.ID
	if subnet.Name != "" {
		title = subnet.Name + " (" + subnet.ID + ")"
	}
	infoContent, infoLinks := buildSubnetInfoContent(&subnet)
	tabs := []msg.TabContent{
		{Title: "Info", Content: infoContent, Format: "text", Links: infoLinks},
		{Title: "Config", Content: buildSubnetConfigContent(&subnet), Format: "text"},
	}
	if len(subnet.IPv6Associations) > 0 {
		tabs = append(tabs, msg.TabContent{
			Title: "IPv6", Content: buildSubnetIPv6Content(&subnet), Format: "text",
		})
	}
	tabs = append(tabs, msg.TabContent{
		Title: "JSON", Content: subnet.DetailJSON(), Format: "json",
	})
	if len(subnet.Tags) > 0 {
		tabs = append(tabs, msg.TabContent{
			Title: "Tags", Content: buildTagsContent(subnet.Tags), Format: "text",
		})
	}
	return func() tea.Msg {
		return msg.TabbedContentMsg{PanelTitle: title, Tabs: tabs}
	}
}

func buildSubnetInfoContent(subnet *aws.Subnet) (string, []msg.TabLink) {
	fields := []infoField{
		{K: "Subnet ID", V: subnet.ID},
		{K: "Name", V: subnet.Name},
		{K: "ARN", V: subnet.ARN},
		{K: "VPC", V: subnet.VpcID, ViewID: "vpc_list", Params: map[string]string{"focus": subnet.VpcID}},
		{K: "AZ", V: subnet.AvailabilityZone},
		{K: "AZ ID", V: subnet.AvailabilityZoneID},
		{K: "CIDR Block", V: subnet.CIDRBlock},
		{K: "Available IPs", V: fmt.Sprintf("%d", subnet.AvailableIPCount)},
		{K: "State", V: subnet.State},
		{K: "Owner", V: subnet.OwnerID},
	}
	return buildInfo(fields)
}

func buildSubnetConfigContent(subnet *aws.Subnet) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-24s %s\n", "Public IP on Launch", yesNo(subnet.MapPublicIPOnLaunch))
	fmt.Fprintf(&b, "%-24s %s\n", "Assign IPv6", yesNo(subnet.AssignIPv6OnCreation))
	fmt.Fprintf(&b, "%-24s %s\n", "Default for AZ", yesNo(subnet.DefaultForAZ))
	fmt.Fprintf(&b, "%-24s %s\n", "DNS64 Enabled", yesNo(subnet.EnableDNS64))
	fmt.Fprintf(&b, "%-24s %s\n", "IPv6 Native", yesNo(subnet.IPv6Native))
	return b.String()
}

func buildSubnetIPv6Content(subnet *aws.Subnet) string {
	var b strings.Builder
	for _, a := range subnet.IPv6Associations {
		fmt.Fprintf(&b, "%-44s %s\n", a.IPv6CIDRBlock, a.State)
	}
	return b.String()
}
