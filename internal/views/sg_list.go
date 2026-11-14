package views

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// SGList displays EC2 security groups.
type SGList struct {
	listCore[aws.SecurityGroup]
}

// NewSGList creates the security group list view.
func NewSGList(ec2 aws.EC2Service) *SGList {
	return &SGList{listCore: newListCore(listConfig[aws.SecurityGroup]{
		Noun:         "security groups",
		LoadingLabel: "Loading security groups...",
		ErrContext:   "listing security groups",
		Key:          func(sg aws.SecurityGroup) string { return sg.ID },
		Columns:      sgColumns,
		Row:          sgRow,
		Fetch:        fetchAll(ec2, aws.EC2Service.ListSecurityGroups),
		FetchTimeout: listTimeout,
		OpenDetail:   sgDetail,
		Copy:         true,
	})}
}

func (s *SGList) ID() string     { return "sg_list" }
func (s *SGList) Title() string  { return "Security Groups" }
func (s *SGList) Init() tea.Cmd  { return s.init() }
func (s *SGList) Footer() string { return s.footer() }
func (s *SGList) View() tea.View { return tea.NewView(s.render("", "")) }

func (s *SGList) KeyMap() []ui.HintBinding {
	return s.hints(listKeys.Details, listKeys.CopyID, listKeys.Sort, listKeys.Filter, listKeys.Refresh)
}

// Update returns the view itself, so the navigator keeps its identity.
func (s *SGList) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	return s, s.update(m)
}

func sgColumns(tier ui.WidthTier) []ui.Column {
	if tier == ui.TierNarrow {
		return []ui.Column{
			{Title: "Group ID", Width: 21, Weight: 1, MaxWidth: 35},
			{Title: "Name", Width: 24, Weight: 2, MaxWidth: 60},
			{Title: "VPC", Width: 21, Weight: 1, MaxWidth: 35},
			{Title: "In", Width: 4},
			{Title: "Out", Width: 4},
		}
	}
	return []ui.Column{
		{Title: "Group ID", Width: 21, Weight: 1, MaxWidth: 35},
		{Title: "Name", Width: 24, Weight: 2, MaxWidth: 60},
		{Title: "VPC", Width: 21, Weight: 1, MaxWidth: 35},
		{Title: "Description", Width: 30, Weight: 2, MaxWidth: 60},
		{Title: "In", Width: 4},
		{Title: "Out", Width: 4},
	}
}

// sgRow builds the row of one group. In and Out sort as numbers.
func sgRow(sg aws.SecurityGroup, tier ui.WidthTier) ui.Row {
	inCount := fmt.Sprintf("%d", len(sg.InboundRules))
	outCount := fmt.Sprintf("%d", len(sg.OutboundRules))
	inKey := ui.SortKeyInt(int64(len(sg.InboundRules)))
	outKey := ui.SortKeyInt(int64(len(sg.OutboundRules)))
	if tier == ui.TierNarrow {
		return ui.Row{
			Key:   sg.ID,
			Cells: table.Row{sg.ID, sg.Name, sg.VpcID, inCount, outCount},
			Sort:  table.Row{sg.ID, sg.Name, sg.VpcID, inKey, outKey},
		}
	}
	return ui.Row{
		Key:   sg.ID,
		Cells: table.Row{sg.ID, sg.Name, sg.VpcID, sg.Description, inCount, outCount},
		Sort:  table.Row{sg.ID, sg.Name, sg.VpcID, sg.Description, inKey, outKey},
	}
}

// sgDetail opens the detail panel of a group.
func sgDetail(sg aws.SecurityGroup) tea.Cmd {
	title := sg.ID
	if sg.Name != "" {
		title = sg.Name + " (" + sg.ID + ")"
	}
	infoContent, infoLinks := buildSGInfoContent(&sg)
	tabs := []msg.TabContent{
		{Title: "Info", Content: infoContent, Format: "text", Links: infoLinks},
		{Title: "Inbound", Content: buildRulesContent(sg.InboundRules), Format: "text"},
		{Title: "Outbound", Content: buildRulesContent(sg.OutboundRules), Format: "text"},
		{Title: "JSON", Content: sg.DetailJSON(), Format: "json"},
	}
	if len(sg.Tags) > 0 {
		tabs = append(tabs, msg.TabContent{
			Title: "Tags", Content: buildTagsContent(sg.Tags), Format: "text",
		})
	}
	return func() tea.Msg {
		return msg.TabbedContentMsg{PanelTitle: title, Tabs: tabs}
	}
}

func buildSGInfoContent(sg *aws.SecurityGroup) (string, []msg.TabLink) {
	fields := []infoField{
		{K: "Group ID", V: sg.ID},
		{K: "Name", V: sg.Name},
		{K: "ARN", V: sg.ARN},
		{K: "Description", V: sg.Description},
		{K: "VPC", V: sg.VpcID, ViewID: "vpc_list", Params: map[string]string{"focus": sg.VpcID}},
		{K: "Owner", V: sg.OwnerID},
	}
	return buildInfo(fields)
}

func buildRulesContent(rules []aws.SecurityGroupRule) string {
	if len(rules) == 0 {
		return "  No rules"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-8s %-14s %-40s %s\n", "Proto", "Ports", "Source/Dest", "Description")
	fmt.Fprintf(&b, "%-8s %-14s %-40s %s\n", "─────", "─────", "───────────", "───────────")
	for _, r := range rules {
		proto := formatProtocol(r.Protocol)
		ports := formatPorts(r.Protocol, r.FromPort, r.ToPort)
		sources := formatRuleSources(r)
		desc := r.Description
		if len(desc) > 30 {
			desc = desc[:27] + "..."
		}
		fmt.Fprintf(&b, "%-8s %-14s %-40s %s\n", proto, ports, sources, desc)
	}
	return b.String()
}

func formatProtocol(protocol string) string {
	switch protocol {
	case "-1":
		return "All"
	case "tcp":
		return "TCP"
	case "udp":
		return "UDP"
	case "icmp":
		return "ICMP"
	case "icmpv6":
		return "ICMPv6"
	default:
		return protocol
	}
}

func formatPorts(protocol string, from, to int32) string {
	if protocol == "-1" {
		return "All"
	}
	if protocol == "icmp" || protocol == "icmpv6" {
		if from == -1 {
			return "All"
		}
		return fmt.Sprintf("Type %d", from)
	}
	if from == to {
		return fmt.Sprintf("%d", from)
	}
	return fmt.Sprintf("%d-%d", from, to)
}

func formatRuleSources(r aws.SecurityGroupRule) string {
	var parts []string
	parts = append(parts, r.CIDRs...)
	parts = append(parts, r.IPv6CIDRs...)
	parts = append(parts, r.SGRefs...)
	parts = append(parts, r.PrefixLists...)
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}
