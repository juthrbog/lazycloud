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

type vpcPageLoadedMsg struct {
	vpcs         []aws.VPC
	hasMorePages bool
	token        *string
	pageNum      int
}

type vpcListKeyMap struct {
	Esc         key.Binding
	Details     key.Binding
	Describe    key.Binding
	CopyID      key.Binding
	Subnets     key.Binding
	Sort        key.Binding
	SortReverse key.Binding
	Filter      key.Binding
	Refresh     key.Binding
}

var defaultVPCListKeyMap = vpcListKeyMap{
	Esc:         key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Details:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter/d", "details")),
	Describe:    key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "details")),
	CopyID:      key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy ID")),
	Subnets:     key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "subnets")),
	Sort:        key.NewBinding(key.WithKeys("s"), key.WithHelp("s/S", "sort")),
	SortReverse: key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "reverse sort")),
	Filter:      key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
	Refresh:     key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
}

// VPCList displays EC2 VPCs.
type VPCList struct {
	keys         vpcListKeyMap
	ec2          aws.EC2Service
	table        ui.Table
	vpcs         []aws.VPC
	filter       ui.Filter
	spinner      ui.Spinner
	loading      bool
	err          error
	width        int
	height       int
	widthTier    ui.WidthTier
	pendingFocus string
}

func (v *VPCList) ID() string    { return "vpc_list" }
func (v *VPCList) Title() string { return "VPCs" }
func (v *VPCList) KeyMap() []ui.HintBinding {
	return []ui.HintBinding{
		{Binding: v.keys.Details},
		{Binding: v.keys.CopyID},
		{Binding: v.keys.Subnets},
		{Binding: v.keys.Sort},
		{Binding: v.keys.Filter},
		{Binding: v.keys.Refresh},
	}
}

func vpcColumns(tier ui.WidthTier) []table.Column {
	if tier == ui.TierNarrow {
		return []table.Column{
			{Title: "VPC ID", Width: 21},
			{Title: "Name", Width: 24},
			{Title: "CIDR Block", Width: 18},
			{Title: "State", Width: 16},
		}
	}
	return []table.Column{
		{Title: "VPC ID", Width: 21},
		{Title: "Name", Width: 24},
		{Title: "CIDR Block", Width: 18},
		{Title: "State", Width: 16},
		{Title: "Default", Width: 8},
		{Title: "Tenancy", Width: 12},
	}
}

// NewVPCList creates the VPC list view.
func NewVPCList(ec2 aws.EC2Service) *VPCList {
	return &VPCList{
		keys:      defaultVPCListKeyMap,
		ec2:       ec2,
		table:     ui.NewTable(vpcColumns(ui.TierMedium), nil),
		filter:    ui.NewFilter(),
		spinner:   ui.NewSpinner("Loading VPCs..."),
		loading:   true,
		widthTier: ui.TierMedium,
	}
}

func (v *VPCList) Init() tea.Cmd {
	if !v.loading {
		return nil
	}
	return tea.Batch(v.spinner.Tick(), v.fetchPage(nil, 1))
}

func (v *VPCList) fetchPage(token *string, pageNum int) tea.Cmd {
	svc := v.ec2
	return func() tea.Msg {
		if svc == nil {
			return msg.ErrorMsg{Err: fmt.Errorf("AWS client not initialized"), Context: "EC2"}
		}
		page, err := svc.ListVPCsPage(context.Background(), token)
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: "listing VPCs"}
		}
		eventlog.Infof(eventlog.CatAWS, "Loaded %d VPCs (page %d)", len(page.VPCs), pageNum)
		return vpcPageLoadedMsg{
			vpcs:         page.VPCs,
			hasMorePages: page.HasMorePages,
			token:        page.Token,
			pageNum:      pageNum,
		}
	}
}

func (v *VPCList) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case ui.PickerResultMsg:
		if m.ID == "sort" {
			if m.Value == "_clear" {
				v.table.ClearSort()
			} else if m.Selected >= 0 {
				v.table.Sort(m.Selected)
			}
		}
		return v, nil

	case vpcPageLoadedMsg:
		v.vpcs = append(v.vpcs, m.vpcs...)
		v.table.SetRows(buildVPCRows(v.vpcs, v.widthTier))
		if m.hasMorePages {
			return v, v.fetchPage(m.token, m.pageNum+1)
		}
		v.loading = false
		v.spinner.Hide()
		if v.pendingFocus != "" {
			id := v.pendingFocus
			v.pendingFocus = ""
			return v, v.focusAndOpenDetail(id)
		}
		return v, nil

	case msg.FocusResourceMsg:
		if v.loading {
			v.pendingFocus = m.ResourceID
			return v, nil
		}
		return v, v.focusAndOpenDetail(m.ResourceID)

	case msg.ErrorMsg:
		v.loading = false
		v.spinner.Hide()
		v.err = m.Err
		return v, nil

	case tea.WindowSizeMsg:
		v.width = m.Width
		v.height = m.Height
		newTier := ui.GetWidthTier(m.Width)
		v.widthTier = newTier

		cols := vpcColumns(newTier)
		if !ui.ColumnsFit(cols, m.Width) {
			cols = vpcColumns(ui.TierNarrow)
			v.widthTier = ui.TierNarrow
		}
		if len(cols) != len(v.table.Columns()) {
			v.table.SetColumns(cols)
			if len(v.vpcs) > 0 {
				v.table.SetRows(buildVPCRows(v.vpcs, v.widthTier))
			}
		}
		v.table.SetSize(m.Width, m.Height-3)
		v.filter.SetWidth(m.Width)
		return v, nil

	case ui.FilterChangedMsg:
		v.table.Filter(m.Text)
		return v, nil

	case tea.KeyPressMsg:
		if v.filter.Active() {
			var cmd tea.Cmd
			v.filter, cmd = v.filter.Update(m)
			return v, cmd
		}

		switch {
		case key.Matches(m, v.keys.Esc):
			return v, func() tea.Msg { return msg.NavigateBackMsg{} }
		case key.Matches(m, v.keys.Sort):
			columns, currentCol := v.table.SortColumnNames()
			return v, func() tea.Msg {
				return msg.RequestSortPickerMsg{Columns: columns, CurrentCol: currentCol}
			}
		case key.Matches(m, v.keys.SortReverse):
			v.table.SortReverse()
			return v, nil
		case key.Matches(m, v.keys.Filter):
			v.filter.Activate()
			return v, nil
		case key.Matches(m, v.keys.CopyID):
			selected := v.table.SelectedRow()
			if selected != nil {
				id := selected[0]
				return v, tea.Batch(
					tea.SetClipboard(id),
					func() tea.Msg { return msg.ToastSuccess("Copied: " + id) },
				)
			}
		case key.Matches(m, v.keys.Refresh):
			v.loading = true
			v.vpcs = nil
			v.err = nil
			v.spinner.Show("Loading VPCs...")
			return v, tea.Batch(v.spinner.Tick(), v.fetchPage(nil, 1))
		case key.Matches(m, v.keys.Subnets):
			selected := v.table.SelectedRow()
			if selected == nil {
				return v, nil
			}
			vpcID := selected[0]
			return v, func() tea.Msg {
				return msg.NavigateMsg{ViewID: "subnet_list", Params: map[string]string{"vpc_id": vpcID}}
			}
		case key.Matches(m, v.keys.Details, v.keys.Describe):
			selected := v.table.SelectedRow()
			if selected == nil {
				return v, nil
			}
			vpc := v.findVPC(selected[0])
			if vpc == nil {
				return v, nil
			}
			return v, v.openDetailCmd(vpc)
		}
	}

	if v.loading {
		var cmd tea.Cmd
		v.spinner, cmd = v.spinner.Update(m)
		return v, cmd
	}

	var cmd tea.Cmd
	v.table, cmd = v.table.Update(m)
	return v, cmd
}

func (v *VPCList) findVPC(id string) *aws.VPC {
	for i := range v.vpcs {
		if v.vpcs[i].ID == id {
			return &v.vpcs[i]
		}
	}
	return nil
}

func (v *VPCList) focusAndOpenDetail(id string) tea.Cmd {
	for i, vpc := range v.vpcs {
		if vpc.ID == id {
			v.table.SetCursor(i)
			break
		}
	}
	vpc := v.findVPC(id)
	if vpc == nil {
		return nil
	}
	return v.openDetailCmd(vpc)
}

func (v *VPCList) openDetailCmd(vpc *aws.VPC) tea.Cmd {
	title := vpc.ID
	if vpc.Name != "" {
		title = vpc.Name + " (" + vpc.ID + ")"
	}
	infoContent, infoLinks := buildVPCInfoContent(vpc)
	tabs := []msg.TabContent{
		{Title: "Info", Content: infoContent, Format: "text", Links: infoLinks},
		{Title: "CIDR Blocks", Content: buildVPCCIDRContent(vpc), Format: "text"},
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

func buildVPCRows(vpcs []aws.VPC, tier ui.WidthTier) []table.Row {
	rows := make([]table.Row, 0, len(vpcs))
	narrow := tier == ui.TierNarrow
	for _, vpc := range vpcs {
		state := ui.StateColor(vpc.State)
		if narrow {
			rows = append(rows, table.Row{vpc.ID, vpc.Name, vpc.CIDRBlock, state})
		} else {
			def := "No"
			if vpc.IsDefault {
				def = "Yes"
			}
			rows = append(rows, table.Row{vpc.ID, vpc.Name, vpc.CIDRBlock, state, def, vpc.InstanceTenancy})
		}
	}
	return rows
}

func buildVPCInfoContent(vpc *aws.VPC) (string, []msg.TabLink) {
	type field struct {
		k, v   string
		viewID string
		params map[string]string
	}
	def := "No"
	if vpc.IsDefault {
		def = "Yes"
	}
	fields := []field{
		{k: "VPC ID", v: vpc.ID},
		{k: "Name", v: vpc.Name},
		{k: "State", v: vpc.State},
		{k: "CIDR Block", v: vpc.CIDRBlock},
		{k: "Default", v: def},
		{k: "Tenancy", v: vpc.InstanceTenancy},
		{k: "DHCP Options", v: vpc.DHCPOptionsID},
		{k: "Owner", v: vpc.OwnerID},
		{k: "Subnets", v: "View subnets", viewID: "subnet_list", params: map[string]string{"vpc_id": vpc.ID}},
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

func (v *VPCList) Footer() string {
	filtered, total := v.table.RowCount()
	footer := fmt.Sprintf("%d/%d VPCs", filtered, total)
	if v.spinner.Visible() {
		footer += "  " + v.spinner.View()
	}
	return footer
}

func (v *VPCList) View() tea.View {
	var content string
	if v.loading && len(v.vpcs) == 0 {
		content = "\n  " + v.spinner.View()
	} else if v.err != nil {
		content = "\n  " + ui.ErrorStyle.Render("Error: "+v.err.Error())
	} else {
		content = v.table.View()
		if v.filter.Active() {
			content = v.filter.View() + "\n" + content
		}
	}
	return tea.NewView(content)
}
