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

type subnetPageLoadedMsg struct {
	subnets      []aws.Subnet
	hasMorePages bool
	token        *string
	pageNum      int
}

type subnetListKeyMap struct {
	Esc         key.Binding
	Details     key.Binding
	Describe    key.Binding
	CopyID      key.Binding
	Sort        key.Binding
	SortReverse key.Binding
	Filter      key.Binding
	Refresh     key.Binding
}

var defaultSubnetListKeyMap = subnetListKeyMap{
	Esc:         key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Details:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter/d", "details")),
	Describe:    key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "details")),
	CopyID:      key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy ID")),
	Sort:        key.NewBinding(key.WithKeys("s"), key.WithHelp("s/S", "sort")),
	SortReverse: key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "reverse sort")),
	Filter:      key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
	Refresh:     key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
}

// SubnetList displays EC2 subnets, optionally filtered by VPC.
type SubnetList struct {
	keys         subnetListKeyMap
	ec2          aws.EC2Service
	table        ui.Table
	subnets      []aws.Subnet
	vpcID        string // optional VPC filter
	filter       ui.Filter
	spinner      ui.Spinner
	loading      bool
	err          error
	width        int
	height       int
	widthTier    ui.WidthTier
	pendingFocus string
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

func (s *SubnetList) KeyMap() []ui.HintBinding {
	return []ui.HintBinding{
		{Binding: s.keys.Details},
		{Binding: s.keys.CopyID},
		{Binding: s.keys.Sort},
		{Binding: s.keys.Filter},
		{Binding: s.keys.Refresh},
	}
}

func subnetColumns(tier ui.WidthTier) []ui.Column {
	if tier == ui.TierNarrow {
		return []ui.Column{
			{Title: "Subnet ID", Width: 24, Weight: 1, MaxWidth: 35},
			{Title: "Name", Width: 24, Weight: 2, MaxWidth: 60},
			{Title: "AZ", Width: 14},
			{Title: "CIDR Block", Width: 18},
			{Title: "State", Width: 16},
		}
	}
	if tier == ui.TierMedium {
		return []ui.Column{
			{Title: "Subnet ID", Width: 15, Weight: 1, MaxWidth: 35},
			{Title: "Name", Width: 15, Weight: 2, MaxWidth: 60},
			{Title: "AZ", Width: 13},
			{Title: "CIDR Block", Width: 15},
			{Title: "State", Width: 10},
		}
	}
	return []ui.Column{
		{Title: "Subnet ID", Width: 24, Weight: 1, MaxWidth: 35},
		{Title: "Name", Width: 24, Weight: 2, MaxWidth: 60},
		{Title: "VPC ID", Width: 21, Weight: 1, MaxWidth: 35},
		{Title: "AZ", Width: 14},
		{Title: "CIDR Block", Width: 18},
		{Title: "Avail IPs", Width: 10},
		{Title: "State", Width: 16},
		{Title: "Public", Width: 8},
		{Title: "Default", Width: 8},
	}
}

// NewSubnetList creates the subnet list view.
// When vpcID is non-empty, only subnets in that VPC are shown.
func NewSubnetList(ec2 aws.EC2Service, vpcID string) *SubnetList {
	return &SubnetList{
		keys:      defaultSubnetListKeyMap,
		ec2:       ec2,
		vpcID:     vpcID,
		table:     ui.NewTable(subnetColumns(ui.TierMedium), nil),
		filter:    ui.NewFilter(),
		spinner:   ui.NewSpinner("Loading subnets..."),
		loading:   true,
		widthTier: ui.TierMedium,
	}
}

func (s *SubnetList) Init() tea.Cmd {
	if !s.loading {
		return nil
	}
	return tea.Batch(s.spinner.Tick(), s.fetchPage(nil, 1))
}

func (s *SubnetList) fetchPage(token *string, pageNum int) tea.Cmd {
	svc := s.ec2
	vpcID := s.vpcID
	return func() tea.Msg {
		if svc == nil {
			return msg.ErrorMsg{Err: fmt.Errorf("AWS client not initialized"), Context: "EC2"}
		}
		eventlog.Debugf(eventlog.CatAWS, "Fetching subnets (page %d)", pageNum)
		page, err := svc.ListSubnetsPage(context.Background(), token, vpcID)
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: "listing subnets"}
		}
		eventlog.Infof(eventlog.CatAWS, "Loaded %d subnets (page %d)", len(page.Subnets), pageNum)
		return subnetPageLoadedMsg{
			subnets:      page.Subnets,
			hasMorePages: page.HasMorePages,
			token:        page.Token,
			pageNum:      pageNum,
		}
	}
}

func (s *SubnetList) Update(m tea.Msg) (tea.Model, tea.Cmd) {
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

	case subnetPageLoadedMsg:
		s.subnets = append(s.subnets, m.subnets...)
		s.table.SetRows(buildSubnetRows(s.subnets, s.widthTier))
		if m.hasMorePages {
			return s, s.fetchPage(m.token, m.pageNum+1)
		}
		s.loading = false
		s.spinner.Hide()
		if s.pendingFocus != "" {
			id := s.pendingFocus
			s.pendingFocus = ""
			return s, s.focusAndOpenDetail(id)
		}
		return s, nil

	case msg.FocusResourceMsg:
		if s.loading {
			s.pendingFocus = m.ResourceID
			return s, nil
		}
		return s, s.focusAndOpenDetail(m.ResourceID)

	case msg.ErrorMsg:
		s.loading = false
		s.spinner.Hide()
		s.err = m.Err
		return s, nil

	case tea.WindowSizeMsg:
		s.width = m.Width
		s.height = m.Height
		cols, newTier := ui.BestFitTier(m.Width, subnetColumns)
		s.widthTier = newTier
		if len(cols) != len(s.table.Columns()) {
			s.table.SetColumns(cols)
			if len(s.subnets) > 0 {
				s.table.SetRows(buildSubnetRows(s.subnets, s.widthTier))
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
			s.subnets = nil
			s.err = nil
			s.spinner.Show("Loading subnets...")
			return s, tea.Batch(s.spinner.Tick(), s.fetchPage(nil, 1))
		case key.Matches(m, s.keys.Details, s.keys.Describe):
			selected := s.table.SelectedRow()
			if selected == nil {
				return s, nil
			}
			subnet := s.findSubnet(selected[0])
			if subnet == nil {
				return s, nil
			}
			return s, s.openDetailCmd(subnet)
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

func (s *SubnetList) findSubnet(id string) *aws.Subnet {
	for i := range s.subnets {
		if s.subnets[i].ID == id {
			return &s.subnets[i]
		}
	}
	return nil
}

func (s *SubnetList) focusAndOpenDetail(id string) tea.Cmd {
	for i, subnet := range s.subnets {
		if subnet.ID == id {
			s.table.SetCursor(i)
			break
		}
	}
	subnet := s.findSubnet(id)
	if subnet == nil {
		return nil
	}
	return s.openDetailCmd(subnet)
}

func (s *SubnetList) openDetailCmd(subnet *aws.Subnet) tea.Cmd {
	title := subnet.ID
	if subnet.Name != "" {
		title = subnet.Name + " (" + subnet.ID + ")"
	}
	infoContent, infoLinks := buildSubnetInfoContent(subnet)
	tabs := []msg.TabContent{
		{Title: "Info", Content: infoContent, Format: "text", Links: infoLinks},
		{Title: "Config", Content: buildSubnetConfigContent(subnet), Format: "text"},
	}
	if len(subnet.IPv6Associations) > 0 {
		tabs = append(tabs, msg.TabContent{
			Title: "IPv6", Content: buildSubnetIPv6Content(subnet), Format: "text",
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

func buildSubnetRows(subnets []aws.Subnet, tier ui.WidthTier) []table.Row {
	rows := make([]table.Row, 0, len(subnets))
	for _, s := range subnets {
		state := ui.StateColor(s.State)
		switch tier {
		case ui.TierNarrow:
			rows = append(rows, table.Row{s.ID, s.Name, s.AvailabilityZone, s.CIDRBlock, state})
		case ui.TierMedium:
			rows = append(rows, table.Row{
				s.ID, s.Name, s.AvailabilityZone, s.CIDRBlock, state,
			})
		default:
			pub := "Private"
			if s.MapPublicIPOnLaunch {
				pub = "Public"
			}
			def := "No"
			if s.DefaultForAZ {
				def = "Yes"
			}
			rows = append(rows, table.Row{
				s.ID, s.Name, s.VpcID, s.AvailabilityZone, s.CIDRBlock,
				fmt.Sprintf("%d", s.AvailableIPCount), state, pub, def,
			})
		}
	}
	return rows
}

func buildSubnetInfoContent(subnet *aws.Subnet) (string, []msg.TabLink) {
	type field struct {
		k, v   string
		viewID string
		params map[string]string
	}
	fields := []field{
		{k: "Subnet ID", v: subnet.ID},
		{k: "Name", v: subnet.Name},
		{k: "ARN", v: subnet.ARN},
		{k: "VPC", v: subnet.VpcID, viewID: "vpc_list", params: map[string]string{"focus": subnet.VpcID}},
		{k: "AZ", v: subnet.AvailabilityZone},
		{k: "AZ ID", v: subnet.AvailabilityZoneID},
		{k: "CIDR Block", v: subnet.CIDRBlock},
		{k: "Available IPs", v: fmt.Sprintf("%d", subnet.AvailableIPCount)},
		{k: "State", v: subnet.State},
		{k: "Owner", v: subnet.OwnerID},
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

func buildSubnetConfigContent(subnet *aws.Subnet) string {
	yn := func(b bool) string {
		if b {
			return "Yes"
		}
		return "No"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%-24s %s\n", "Public IP on Launch", yn(subnet.MapPublicIPOnLaunch))
	fmt.Fprintf(&b, "%-24s %s\n", "Assign IPv6", yn(subnet.AssignIPv6OnCreation))
	fmt.Fprintf(&b, "%-24s %s\n", "Default for AZ", yn(subnet.DefaultForAZ))
	fmt.Fprintf(&b, "%-24s %s\n", "DNS64 Enabled", yn(subnet.EnableDNS64))
	fmt.Fprintf(&b, "%-24s %s\n", "IPv6 Native", yn(subnet.IPv6Native))
	return b.String()
}

func buildSubnetIPv6Content(subnet *aws.Subnet) string {
	var b strings.Builder
	for _, a := range subnet.IPv6Associations {
		fmt.Fprintf(&b, "%-44s %s\n", a.IPv6CIDRBlock, a.State)
	}
	return b.String()
}

func (s *SubnetList) Footer() string {
	filtered, total := s.table.RowCount()
	footer := fmt.Sprintf("%d/%d subnets", filtered, total)
	if s.spinner.Visible() {
		footer += "  " + s.spinner.View()
	}
	return footer
}

func (s *SubnetList) View() tea.View {
	var content string
	if s.loading && len(s.subnets) == 0 {
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
