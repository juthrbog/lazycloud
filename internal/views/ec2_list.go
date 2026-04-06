package views

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

type ec2InstancesLoadedMsg struct {
	instances []aws.Instance
}

type ec2InstanceDetailMsg struct {
	detail *aws.InstanceDetail
	err    error
}

type ec2SSMSessionFinishedMsg struct {
	instanceID   string
	instanceName string
	err          error
}

type ec2DelayedRefreshMsg struct{}

type ec2InstanceMutatedMsg struct {
	action     string // "started", "stopped", "rebooted", "terminated"
	instanceID string
	err        error
}

type ec2ListKeyMap struct {
	Esc         key.Binding
	Details     key.Binding
	Describe    key.Binding
	Connect     key.Binding
	Manage      key.Binding
	CopyID      key.Binding
	Sort        key.Binding
	SortReverse key.Binding
	Filter      key.Binding
	Refresh     key.Binding
	Select      key.Binding
}

var defaultEC2ListKeyMap = ec2ListKeyMap{
	Esc:         key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Details:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter/d", "details")),
	Describe:    key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "details")),
	Connect:     key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "connect (SSM)")),
	Manage:      key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "manage")),
	CopyID:      key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy ID")),
	Sort:        key.NewBinding(key.WithKeys("s"), key.WithHelp("s/S", "sort")),
	SortReverse: key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "reverse sort")),
	Filter:      key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
	Refresh:     key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
	Select:      key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "select")),
}

// EC2List displays all EC2 instances.
type EC2List struct {
	keys              ec2ListKeyMap
	ec2               aws.EC2Service
	awsClient         *aws.Client
	table             ui.Table
	instances         []aws.Instance
	filter            ui.Filter
	spinner           ui.Spinner
	loading           bool
	pendingInstanceIDs []string // instances targeted by a pending action
	pendingAction     string // action name awaiting confirmation
	pendingFocus      string // instance ID to auto-open after data loads
	err               error
	width             int
	height            int
	widthTier         ui.WidthTier
}

func (e *EC2List) ID() string    { return "ec2_list" }
func (e *EC2List) Title() string { return "EC2 Instances" }
func (e *EC2List) KeyMap() []ui.HintBinding {
	hints := []ui.HintBinding{
		{Binding: e.keys.Details},
		{Binding: e.keys.Connect},
		{Binding: e.keys.Manage, Mode: ui.ModeReadWrite},
		{Binding: e.keys.CopyID},
		{Binding: e.keys.Select},
		{Binding: e.keys.Sort},
		{Binding: e.keys.Filter},
		{Binding: e.keys.Refresh},
	}
	ui.ApplyModeAll(hints)
	return hints
}

func ec2Columns(tier ui.WidthTier) []table.Column {
	if tier == ui.TierNarrow {
		return []table.Column{
			{Title: "Instance ID", Width: 21},
			{Title: "Name", Width: 24},
			{Title: "State", Width: 16},
			{Title: "Type", Width: 14},
		}
	}
	return []table.Column{
		{Title: "Instance ID", Width: 21},
		{Title: "Name", Width: 24},
		{Title: "State", Width: 16},
		{Title: "Type", Width: 14},
		{Title: "Private IP", Width: 16},
		{Title: "Public IP", Width: 16},
		{Title: "AZ", Width: 14},
		{Title: "Launched", Width: 12},
	}
}

// NewEC2List creates the EC2 instance list view.
func NewEC2List(ec2 aws.EC2Service, awsClient *aws.Client) *EC2List {
	t := ui.NewTable(ec2Columns(ui.TierMedium), nil)
	return &EC2List{
		keys:      defaultEC2ListKeyMap,
		ec2:       ec2,
		awsClient: awsClient,
		table:     t,
		filter:    ui.NewFilter(),
		spinner:   ui.NewSpinner("Loading EC2 instances..."),
		loading:   true,
		widthTier: ui.TierMedium,
	}
}

func (e *EC2List) Init() tea.Cmd {
	e.table.DeselectAll()
	if !e.loading {
		return nil
	}
	return tea.Batch(e.spinner.Tick(), e.fetchInstances())
}

func (e *EC2List) fetchInstances() tea.Cmd {
	svc := e.ec2
	return func() tea.Msg {
		if svc == nil {
			return msg.ErrorMsg{Err: fmt.Errorf("AWS client not initialized"), Context: "EC2"}
		}
		instances, err := svc.ListInstances(context.Background())
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: "listing EC2 instances"}
		}
		eventlog.Infof(eventlog.CatAWS, "Loaded %d EC2 instances", len(instances))
		return ec2InstancesLoadedMsg{instances: instances}
	}
}

func (e *EC2List) focusAndFetchDetail(instanceID string) tea.Cmd {
	for i, inst := range e.instances {
		if inst.ID == instanceID {
			e.table.SetCursor(i)
			break
		}
	}
	return e.fetchDetail(instanceID)
}

func (e *EC2List) fetchDetail(instanceID string) tea.Cmd {
	svc := e.ec2
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Fetching details for instance: %s", instanceID)
		detail, err := svc.GetInstanceDetail(context.Background(), instanceID)
		return ec2InstanceDetailMsg{detail: detail, err: err}
	}
}

func (e *EC2List) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case ui.PickerResultMsg:
		if m.ID == "sort" {
			if m.Value == "_clear" {
				e.table.ClearSort()
			} else if m.Selected >= 0 {
				e.table.Sort(m.Selected)
			}
		} else if m.ID == "action" && m.Selected >= 0 {
			ids := e.pendingInstanceIDs
			switch m.Value {
			case "Start":
				for _, id := range ids {
					e.setInstanceState(id, "pending")
				}
				e.spinner.Show(fmt.Sprintf("starting %d instance(s)...", len(ids)))
				return e, tea.Batch(e.spinner.Tick(), e.startInstances(ids))
			case "Stop", "Reboot", "Terminate":
				e.pendingAction = m.Value
				action := m.Value
				count := len(ids)
				return e, func() tea.Msg {
					return msg.RequestConfirmMsg{
						Message: fmt.Sprintf("%s %d instance(s)?", action, count),
						Action:  "ec2_" + strings.ToLower(action),
					}
				}
			}
		}
		return e, nil

	case ui.ConfirmResultMsg:
		if !m.Confirmed {
			e.pendingInstanceIDs = nil
			e.pendingAction = ""
			return e, nil
		}
		ids := e.pendingInstanceIDs
		action := e.pendingAction
		e.pendingInstanceIDs = nil
		e.pendingAction = ""
		for _, id := range ids {
			if ts := transitionalState(action); ts != "" {
				e.setInstanceState(id, ts)
			}
		}
		e.spinner.Show(fmt.Sprintf("%s %d instance(s)...", strings.ToLower(action)+"ing", len(ids)))
		switch m.Action {
		case "ec2_stop":
			return e, tea.Batch(e.spinner.Tick(), e.stopInstances(ids))
		case "ec2_reboot":
			return e, tea.Batch(e.spinner.Tick(), e.rebootInstances(ids))
		case "ec2_terminate":
			return e, tea.Batch(e.spinner.Tick(), e.terminateInstances(ids))
		}
		return e, nil

	case ec2InstanceMutatedMsg:
		e.table.DeselectAll()
		if m.err != nil {
			e.spinner.Hide()
			e.err = m.err
			return e, func() tea.Msg {
				return msg.ToastError(m.action + " failed: " + m.err.Error())
			}
		}
		// Delay refresh to give AWS time to register the state transition.
		// Without this, DescribeInstances may return the old state and
		// overwrite the optimistic update.
		e.spinner.Show("Waiting for state change...")
		action := m.action
		id := m.instanceID
		delayedRefresh := func() tea.Msg {
			time.Sleep(2 * time.Second)
			return ec2DelayedRefreshMsg{}
		}
		return e, tea.Batch(e.spinner.Tick(), delayedRefresh, func() tea.Msg {
			return msg.ToastSuccess("Instance " + action + ": " + id)
		})

	case ec2DelayedRefreshMsg:
		e.spinner.Show("Refreshing instances...")
		return e, tea.Batch(e.spinner.Tick(), e.fetchInstances())

	case ec2SSMSessionFinishedMsg:
		if m.err != nil {
			return e, func() tea.Msg {
				return msg.ToastError("SSM session failed: " + m.err.Error())
			}
		}
		label := m.instanceID
		if m.instanceName != "" {
			label = m.instanceName
		}
		// Refresh instance list — state may have changed during the session
		e.loading = true
		e.spinner.Show("Refreshing instances...")
		return e, tea.Batch(e.spinner.Tick(), e.fetchInstances(), func() tea.Msg {
			return msg.ToastSuccess("Session ended: " + label)
		})

	case ec2InstancesLoadedMsg:
		e.loading = false
		e.spinner.Hide()
		e.instances = m.instances
		rows, sortKeys := e.buildRows(m.instances)
		e.table.SetRowsWithSortKeys(rows, sortKeys)
		if e.pendingFocus != "" {
			id := e.pendingFocus
			e.pendingFocus = ""
			return e, e.focusAndFetchDetail(id)
		}
		return e, nil

	case msg.FocusResourceMsg:
		if e.loading {
			e.pendingFocus = m.ResourceID
			return e, nil
		}
		return e, e.focusAndFetchDetail(m.ResourceID)

	case ec2InstanceDetailMsg:
		if m.err != nil {
			e.err = m.err
			return e, nil
		}
		if m.detail == nil {
			return e, nil
		}
		d := m.detail
		title := d.InstanceID
		if d.Name != "" {
			title = d.Name + " (" + d.InstanceID + ")"
		}
		infoContent, infoLinks := buildEC2InfoContentWithLinks(d)
		tabs := []msg.TabContent{
			{Title: "Info", Content: infoContent, Format: "text", Links: infoLinks},
			{Title: "JSON", Content: d.DetailJSON(), Format: "json"},
		}
		if len(d.SecurityGroups) > 0 {
			sgContent, sgLinks := buildSGContentWithLinks(d.SecurityGroups)
			tabs = append(tabs, msg.TabContent{
				Title: "Security Groups", Content: sgContent, Format: "text", Links: sgLinks,
			})
		}
		if len(d.Tags) > 0 {
			tabs = append(tabs, msg.TabContent{
				Title: "Tags", Content: buildTagsContent(d.Tags), Format: "text",
			})
		}
		return e, func() tea.Msg {
			return msg.TabbedContentMsg{PanelTitle: title, Tabs: tabs}
		}

	case msg.ErrorMsg:
		e.loading = false
		e.spinner.Hide()
		e.err = m.Err
		return e, nil

	case tea.WindowSizeMsg:
		e.width = m.Width
		e.height = m.Height
		newTier := ui.GetWidthTier(m.Width)
		e.widthTier = newTier

		cols := ec2Columns(newTier)
		if !ui.ColumnsFit(cols, m.Width) {
			cols = ec2Columns(ui.TierNarrow)
			e.widthTier = ui.TierNarrow
		}
		if len(cols) != len(e.table.Columns()) {
			e.table.SetColumns(cols)
			if len(e.instances) > 0 {
				rows, sortKeys := e.buildRows(e.instances)
				e.table.SetRowsWithSortKeys(rows, sortKeys)
			}
		}
		e.table.SetSize(m.Width, m.Height-3)
		e.filter.SetWidth(m.Width)
		return e, nil

	case ui.FilterChangedMsg:
		e.table.Filter(m.Text)
		return e, nil

	case tea.KeyPressMsg:
		if e.filter.Active() {
			var cmd tea.Cmd
			e.filter, cmd = e.filter.Update(m)
			return e, cmd
		}

		switch {
		case key.Matches(m, e.keys.Esc):
			if e.table.SelectionCount() > 0 {
				e.table.DeselectAll()
				return e, nil
			}
			return e, func() tea.Msg { return msg.NavigateBackMsg{} }
		case key.Matches(m, e.keys.Sort):
			columns, currentCol := e.table.SortColumnNames()
			return e, func() tea.Msg {
				return msg.RequestSortPickerMsg{Columns: columns, CurrentCol: currentCol}
			}
		case key.Matches(m, e.keys.SortReverse):
			e.table.SortReverse()
			return e, nil
		case key.Matches(m, e.keys.Filter):
			e.filter.Activate()
			return e, nil
		case key.Matches(m, e.keys.Refresh):
			e.loading = true
			e.err = nil
			e.spinner.Show("Loading EC2 instances...")
			return e, tea.Batch(e.spinner.Tick(), e.fetchInstances())
		case key.Matches(m, e.keys.Details, e.keys.Describe):
			selected := e.table.SelectedRow()
			if selected != nil {
				instanceID := selected[0]
				return e, e.fetchDetail(instanceID)
			}
		case key.Matches(m, e.keys.CopyID):
			ids := e.selectedInstanceIDs()
			if len(ids) == 0 {
				return e, nil
			}
			text := strings.Join(ids, "\n")
			return e, tea.Batch(
				tea.SetClipboard(text),
				func() tea.Msg {
					return msg.ToastSuccess(fmt.Sprintf("Copied %d instance ID(s)", len(ids)))
				},
			)
		case key.Matches(m, e.keys.Select):
			e.table.ToggleSelect()
			return e, nil
		case key.Matches(m, e.keys.Manage):
			if ui.ReadOnly {
				return e, func() tea.Msg {
					return msg.ToastError("ReadOnly mode — press W to switch")
				}
			}
			ids := e.selectedInstanceIDs()
			if len(ids) == 0 {
				return e, nil
			}
			actions := e.commonActionsForSelection(ids)
			if len(actions) == 0 {
				return e, func() tea.Msg {
					return msg.ToastError("No common actions for selected instances")
				}
			}
			e.pendingInstanceIDs = ids
			title := "Manage Instance"
			if len(ids) > 1 {
				title = fmt.Sprintf("Manage %d Instances", len(ids))
			}
			return e, func() tea.Msg {
				return msg.RequestActionPickerMsg{
					Title:   title,
					Options: actions,
				}
			}
		case key.Matches(m, e.keys.Connect):
			selected := e.table.SelectedRow()
			if selected == nil {
				return e, nil
			}
			inst := e.findInstance(selected[0])
			if inst == nil {
				return e, nil
			}
			if inst.State != "running" {
				state := inst.State
				return e, func() tea.Msg {
					return msg.ToastError("Instance is " + state + " — must be running for SSM")
				}
			}
			if !aws.SSMPluginAvailable() {
				return e, func() tea.Msg {
					return msg.ToastError("session-manager-plugin not found — install it first")
				}
			}
			label := inst.ID
			if inst.Name != "" {
				label = inst.Name + " (" + inst.ID + ")"
			}
			eventlog.Infof(eventlog.CatAWS, "Starting SSM session: %s", label)
			id := inst.ID
			name := inst.Name
			ssmCmd := e.awsClient.SSMSessionCmd(id, label)
			return e, tea.ExecProcess(ssmCmd, func(err error) tea.Msg {
				return ec2SSMSessionFinishedMsg{instanceID: id, instanceName: name, err: err}
			})
		}
	}

	if e.loading {
		var cmd tea.Cmd
		e.spinner, cmd = e.spinner.Update(m)
		return e, cmd
	}

	var cmd tea.Cmd
	e.table, cmd = e.table.Update(m)
	return e, cmd
}

func (e *EC2List) findInstance(id string) *aws.Instance {
	for i := range e.instances {
		if e.instances[i].ID == id {
			return &e.instances[i]
		}
	}
	return nil
}

// selectedInstanceIDs returns the IDs of all selected instances,
// or the cursor instance ID if nothing is selected.
func (e *EC2List) selectedInstanceIDs() []string {
	indices := e.table.SelectedIndices()
	if len(indices) == 0 {
		row := e.table.SelectedRow()
		if row == nil {
			return nil
		}
		return []string{row[0]}
	}
	ids := make([]string, 0, len(indices))
	for _, idx := range indices {
		if idx < len(e.instances) {
			ids = append(ids, e.instances[idx].ID)
		}
	}
	return ids
}

// commonActionsForSelection returns the actions available for ALL selected instances.
// Returns nil if the selection contains instances in mixed states with no common actions.
func (e *EC2List) commonActionsForSelection(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	var common []string
	for i, id := range ids {
		inst := e.findInstance(id)
		if inst == nil {
			return nil
		}
		actions := e.actionsForState(inst.State)
		if len(actions) == 0 {
			return nil
		}
		if i == 0 {
			common = actions
			continue
		}
		// Intersect
		set := make(map[string]bool, len(actions))
		for _, a := range actions {
			set[a] = true
		}
		filtered := common[:0]
		for _, a := range common {
			if set[a] {
				filtered = append(filtered, a)
			}
		}
		common = filtered
	}
	return common
}

// setInstanceState optimistically updates an instance's state in the local
// data and rebuilds the table so the UI reflects the change immediately.
func (e *EC2List) setInstanceState(id, state string) {
	if inst := e.findInstance(id); inst != nil {
		inst.State = state
	}
	rows, sortKeys := e.buildRows(e.instances)
	e.table.SetRowsWithSortKeys(rows, sortKeys)
}

// transitionalState returns the EC2 transitional state for a given action.
func transitionalState(action string) string {
	switch action {
	case "Start":
		return "pending"
	case "Stop":
		return "stopping"
	case "Reboot":
		return "pending"
	case "Terminate":
		return "shutting-down"
	default:
		return ""
	}
}

func (e *EC2List) actionsForState(state string) []string {
	switch state {
	case "stopped":
		return []string{"Start"}
	case "running":
		return []string{"Stop", "Reboot", "Terminate"}
	default:
		return nil
	}
}

func (e *EC2List) startInstances(ids []string) tea.Cmd {
	svc := e.ec2
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Starting %d instances", len(ids))
		err := svc.StartInstances(context.Background(), ids)
		return ec2InstanceMutatedMsg{action: "started", instanceID: strings.Join(ids, ", "), err: err}
	}
}

func (e *EC2List) stopInstances(ids []string) tea.Cmd {
	svc := e.ec2
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Stopping %d instances", len(ids))
		err := svc.StopInstances(context.Background(), ids)
		return ec2InstanceMutatedMsg{action: "stopped", instanceID: strings.Join(ids, ", "), err: err}
	}
}

func (e *EC2List) rebootInstances(ids []string) tea.Cmd {
	svc := e.ec2
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Rebooting %d instances", len(ids))
		err := svc.RebootInstances(context.Background(), ids)
		return ec2InstanceMutatedMsg{action: "rebooted", instanceID: strings.Join(ids, ", "), err: err}
	}
}

func (e *EC2List) terminateInstances(ids []string) tea.Cmd {
	svc := e.ec2
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Terminating %d instances", len(ids))
		err := svc.TerminateInstances(context.Background(), ids)
		return ec2InstanceMutatedMsg{action: "terminated", instanceID: strings.Join(ids, ", "), err: err}
	}
}

func (e *EC2List) buildRows(instances []aws.Instance) ([]table.Row, []table.Row) {
	rows := make([]table.Row, 0, len(instances))
	sortKeys := make([]table.Row, 0, len(instances))
	narrow := e.widthTier == ui.TierNarrow
	for _, inst := range instances {
		launched := ""
		if !inst.LaunchTime.IsZero() {
			launched = inst.LaunchTime.Format("2006-01-02")
		}
		if narrow {
			rows = append(rows, table.Row{
				inst.ID, inst.Name, ui.StateColor(inst.State), inst.Type,
			})
			sortKeys = append(sortKeys, table.Row{
				inst.ID, inst.Name, inst.State, inst.Type,
			})
		} else {
			rows = append(rows, table.Row{
				inst.ID, inst.Name, ui.StateColor(inst.State), inst.Type,
				inst.PrivateIP, inst.PublicIP, inst.AvailabilityZone, launched,
			})
			sortKeys = append(sortKeys, table.Row{
				inst.ID, inst.Name, inst.State, inst.Type,
				inst.PrivateIP, inst.PublicIP, inst.AvailabilityZone, launched,
			})
		}
	}
	return rows, sortKeys
}

func buildEC2InfoContentWithLinks(d *aws.InstanceDetail) (string, []msg.TabLink) {
	type field struct {
		k, v   string
		viewID string
		params map[string]string
	}
	fields := []field{
		{k: "Instance ID", v: d.InstanceID},
		{k: "Name", v: d.Name},
		{k: "State", v: d.State},
		{k: "Type", v: d.InstanceType},
		{k: "Platform", v: d.Platform},
		{k: "Architecture", v: d.Architecture},
		{k: "Private IP", v: d.PrivateIP},
		{k: "Public IP", v: d.PublicIP},
		{k: "Private DNS", v: d.PrivateDNS},
		{k: "Public DNS", v: d.PublicDNS},
		{k: "VPC", v: d.VpcID},
		{k: "Subnet", v: d.SubnetID},
		{k: "AZ", v: d.AvailabilityZone},
		{k: "Key Name", v: d.KeyName},
		{k: "AMI", v: d.AMI, viewID: "ami_list"},
		{k: "IAM Role", v: d.IAMRole},
		{k: "Launch Time", v: d.LaunchTime},
		{k: "Root Device", v: d.RootDeviceType + " (" + d.RootDeviceName + ")"},
	}
	var b strings.Builder
	var links []msg.TabLink
	lineIdx := 0
	for _, f := range fields {
		if f.v != "" && f.v != " ()" {
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

func buildSGContentWithLinks(sgs []aws.SecurityGroupRef) (string, []msg.TabLink) {
	var b strings.Builder
	var links []msg.TabLink
	for i, sg := range sgs {
		fmt.Fprintf(&b, "%-22s %s\n", sg.ID, sg.Name)
		links = append(links, msg.TabLink{
			Line:   i,
			ViewID: "sg_list",
			Params: map[string]string{"focus": sg.ID},
		})
	}
	return b.String(), links
}

func buildTagsContent(tags map[string]string) string {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%-24s %s\n", k, tags[k])
	}
	return b.String()
}

func (e *EC2List) Footer() string {
	filtered, total := e.table.RowCount()
	footer := fmt.Sprintf("%d/%d instances", filtered, total)
	if sel := e.table.SelectionCount(); sel > 0 {
		footer += fmt.Sprintf("  (%d selected)", sel)
	}
	if e.spinner.Visible() {
		footer += "  " + e.spinner.View()
	}
	return footer
}

func (e *EC2List) View() tea.View {
	var content string
	if e.loading && len(e.instances) == 0 {
		// Initial load — spinner only
		content = "\n  " + e.spinner.View()
	} else if e.err != nil {
		content = "\n  " + ui.ErrorStyle.Render("Error: "+e.err.Error())
	} else {
		content = e.table.View()
		if e.filter.Active() {
			content = e.filter.View() + "\n" + content
		}
	}
	return tea.NewView(content)
}
