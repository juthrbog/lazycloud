package views

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

type sgListLoadedMsg struct {
	groups []aws.SecurityGroup
}

type sgListKeyMap struct {
	Esc         key.Binding
	Details     key.Binding
	Describe    key.Binding
	CopyID      key.Binding
	Sort        key.Binding
	SortReverse key.Binding
	Filter      key.Binding
	Refresh     key.Binding
}

var defaultSGListKeyMap = sgListKeyMap{
	Esc:         key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Details:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter/d", "details")),
	Describe:    key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "details")),
	CopyID:      key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy ID")),
	Sort:        key.NewBinding(key.WithKeys("s"), key.WithHelp("s/S", "sort")),
	SortReverse: key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "reverse sort")),
	Filter:      key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
	Refresh:     key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
}

// SGList displays EC2 security groups.
type SGList struct {
	keys      sgListKeyMap
	ec2       aws.EC2Service
	table     ui.Table
	groups    []aws.SecurityGroup
	filter    ui.Filter
	spinner   ui.Spinner
	loading   bool
	err       error
	width     int
	height    int
	widthTier ui.WidthTier
}

func (s *SGList) ID() string    { return "sg_list" }
func (s *SGList) Title() string { return "Security Groups" }
func (s *SGList) KeyMap() []ui.HintBinding {
	return []ui.HintBinding{
		{Binding: s.keys.Details},
		{Binding: s.keys.CopyID},
		{Binding: s.keys.Sort},
		{Binding: s.keys.Filter},
		{Binding: s.keys.Refresh},
	}
}

func sgColumns(tier ui.WidthTier) []table.Column {
	if tier == ui.TierNarrow {
		return []table.Column{
			{Title: "Group ID", Width: 21},
			{Title: "Name", Width: 24},
			{Title: "VPC", Width: 21},
			{Title: "In", Width: 4},
			{Title: "Out", Width: 4},
		}
	}
	return []table.Column{
		{Title: "Group ID", Width: 21},
		{Title: "Name", Width: 24},
		{Title: "VPC", Width: 21},
		{Title: "Description", Width: 30},
		{Title: "In", Width: 4},
		{Title: "Out", Width: 4},
	}
}

// NewSGList creates the security group list view.
func NewSGList(ec2 aws.EC2Service) *SGList {
	return &SGList{
		keys:      defaultSGListKeyMap,
		ec2:       ec2,
		table:     ui.NewTable(sgColumns(ui.TierMedium), nil),
		filter:    ui.NewFilter(),
		spinner:   ui.NewSpinner("Loading security groups..."),
		loading:   true,
		widthTier: ui.TierMedium,
	}
}

func (s *SGList) Init() tea.Cmd {
	if !s.loading {
		return nil
	}
	return tea.Batch(s.spinner.Tick(), s.fetchSecurityGroups())
}

func (s *SGList) fetchSecurityGroups() tea.Cmd {
	svc := s.ec2
	return func() tea.Msg {
		if svc == nil {
			return msg.ErrorMsg{Err: fmt.Errorf("AWS client not initialized"), Context: "EC2"}
		}
		groups, err := svc.ListSecurityGroups(context.Background())
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: "listing security groups"}
		}
		eventlog.Infof(eventlog.CatAWS, "Loaded %d security groups", len(groups))
		return sgListLoadedMsg{groups: groups}
	}
}

func (s *SGList) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case ui.PickerResultMsg:
		if m.ID == "sort" {
			if m.Value == "_clear" {
				s.table.ClearSort()
			} else if m.Selected >= 0 {
				s.table.Sort(m.Selected)
			}
		}
		return s, nil

	case sgListLoadedMsg:
		s.loading = false
		s.spinner.Hide()
		s.groups = m.groups
		rows := buildSGRows(m.groups, s.widthTier)
		s.table.SetRows(rows)
		return s, nil

	case msg.ErrorMsg:
		s.loading = false
		s.spinner.Hide()
		s.err = m.Err
		return s, nil

	case tea.WindowSizeMsg:
		s.width = m.Width
		s.height = m.Height
		newTier := ui.GetWidthTier(m.Width)
		s.widthTier = newTier

		cols := sgColumns(newTier)
		if !ui.ColumnsFit(cols, m.Width) {
			cols = sgColumns(ui.TierNarrow)
			s.widthTier = ui.TierNarrow
		}
		if len(cols) != len(s.table.Columns()) {
			s.table.SetColumns(cols)
			if len(s.groups) > 0 {
				s.table.SetRows(buildSGRows(s.groups, s.widthTier))
			}
		}
		s.table.SetSize(m.Width, m.Height-3)
		s.filter.SetWidth(m.Width)
		return s, nil

	case ui.FilterChangedMsg:
		s.table.Filter(m.Text)
		return s, nil

	case tea.KeyPressMsg:
		if s.filter.Active() {
			var cmd tea.Cmd
			s.filter, cmd = s.filter.Update(m)
			return s, cmd
		}

		switch {
		case key.Matches(m, s.keys.Esc):
			return s, func() tea.Msg { return msg.NavigateBackMsg{} }
		case key.Matches(m, s.keys.Sort):
			columns, currentCol := s.table.SortColumnNames()
			return s, func() tea.Msg {
				return msg.RequestSortPickerMsg{Columns: columns, CurrentCol: currentCol}
			}
		case key.Matches(m, s.keys.SortReverse):
			s.table.SortReverse()
			return s, nil
		case key.Matches(m, s.keys.Filter):
			s.filter.Activate()
			return s, nil
		case key.Matches(m, s.keys.CopyID):
			selected := s.table.SelectedRow()
			if selected != nil {
				id := selected[0]
				return s, tea.Batch(
					tea.SetClipboard(id),
					func() tea.Msg { return msg.ToastSuccess("Copied: " + id) },
				)
			}
		case key.Matches(m, s.keys.Refresh):
			s.loading = true
			s.err = nil
			s.spinner.Show("Loading security groups...")
			return s, tea.Batch(s.spinner.Tick(), s.fetchSecurityGroups())
		case key.Matches(m, s.keys.Details, s.keys.Describe):
			selected := s.table.SelectedRow()
			if selected == nil {
				return s, nil
			}
			sgID := selected[0]
			sg := s.findSG(sgID)
			if sg == nil {
				return s, nil
			}
			title := sg.ID
			if sg.Name != "" {
				title = sg.Name + " (" + sg.ID + ")"
			}
			infoContent, infoLinks := buildSGInfoContent(sg)
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
			return s, func() tea.Msg {
				return msg.TabbedContentMsg{PanelTitle: title, Tabs: tabs}
			}
		}
	}

	if s.loading {
		var cmd tea.Cmd
		s.spinner, cmd = s.spinner.Update(m)
		return s, cmd
	}

	var cmd tea.Cmd
	s.table, cmd = s.table.Update(m)
	return s, cmd
}

func (s *SGList) findSG(id string) *aws.SecurityGroup {
	for i := range s.groups {
		if s.groups[i].ID == id {
			return &s.groups[i]
		}
	}
	return nil
}

func buildSGRows(groups []aws.SecurityGroup, tier ui.WidthTier) []table.Row {
	rows := make([]table.Row, 0, len(groups))
	narrow := tier == ui.TierNarrow
	for _, sg := range groups {
		inCount := fmt.Sprintf("%d", len(sg.InboundRules))
		outCount := fmt.Sprintf("%d", len(sg.OutboundRules))
		if narrow {
			rows = append(rows, table.Row{sg.ID, sg.Name, sg.VpcID, inCount, outCount})
		} else {
			rows = append(rows, table.Row{sg.ID, sg.Name, sg.VpcID, sg.Description, inCount, outCount})
		}
	}
	return rows
}

func buildSGInfoContent(sg *aws.SecurityGroup) (string, []msg.TabLink) {
	type field struct {
		k, v   string
		viewID string
		params map[string]string
	}
	fields := []field{
		{k: "Group ID", v: sg.ID},
		{k: "Name", v: sg.Name},
		{k: "ARN", v: sg.ARN},
		{k: "Description", v: sg.Description},
		{k: "VPC", v: sg.VpcID},
		{k: "Owner", v: sg.OwnerID},
	}
	var b strings.Builder
	var links []msg.TabLink
	lineIdx := 0
	for _, f := range fields {
		if f.v != "" {
			fmt.Fprintf(&b, "%-16s %s\n", f.k, f.v)
			if f.viewID != "" {
				links = append(links, msg.TabLink{
					Line:   lineIdx,
					ViewID: f.viewID,
					Params: f.params,
				})
			}
			lineIdx++
		}
	}
	return b.String(), links
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

func (s *SGList) Footer() string {
	filtered, total := s.table.RowCount()
	footer := fmt.Sprintf("%d/%d security groups", filtered, total)
	if s.spinner.Visible() {
		footer += "  " + s.spinner.View()
	}
	return footer
}

func (s *SGList) View() tea.View {
	var content string
	if s.loading && len(s.groups) == 0 {
		content = "\n  " + s.spinner.View()
	} else if s.err != nil {
		content = "\n  " + ui.ErrorStyle.Render("Error: "+s.err.Error())
	} else {
		content = s.table.View()
		if s.filter.Active() {
			content = s.filter.View() + "\n" + content
		}
	}
	return tea.NewView(content)
}
