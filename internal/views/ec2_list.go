package views

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"

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

// EC2List displays all EC2 instances.
type EC2List struct {
	ec2               aws.EC2Service
	awsClient         *aws.Client
	table             ui.Table
	instances         []aws.Instance
	filter            ui.Filter
	spinner           ui.Spinner
	loading           bool
	pendingInstanceID string // instance targeted by a pending action
	pendingAction     string // action name awaiting confirmation
	err               error
	width             int
	height            int
	widthTier         ui.WidthTier
}

func (e *EC2List) ID() string    { return "ec2_list" }
func (e *EC2List) Title() string { return "EC2 Instances" }
func (e *EC2List) KeyMap() []ui.KeyHint {
	return []ui.KeyHint{
		{Key: "enter/d", Desc: "details"},
		{Key: "o", Desc: "connect (SSM)"},
		{Key: "m", Desc: "manage", Mode: ui.ModeReadWrite},
		{Key: "y", Desc: "copy ID"},
		{Key: "s/S", Desc: "sort"},
		{Key: "/", Desc: "filter"},
		{Key: "r", Desc: "refresh"},
	}
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
			id := e.pendingInstanceID
			switch m.Value {
			case "Start":
				ts := transitionalState("Start")
				e.setInstanceState(id, ts)
				e.spinner.Show("starting " + id + "...")
				return e, tea.Batch(e.spinner.Tick(), e.startInstance(id))
			case "Stop", "Reboot", "Terminate":
				e.pendingAction = m.Value
				action := m.Value
				return e, func() tea.Msg {
					return msg.RequestConfirmMsg{
						Message: action + " instance " + id + "?",
						Action:  "ec2_" + strings.ToLower(action),
					}
				}
			}
		}
		return e, nil

	case ui.ConfirmResultMsg:
		if !m.Confirmed {
			e.pendingInstanceID = ""
			e.pendingAction = ""
			return e, nil
		}
		id := e.pendingInstanceID
		action := e.pendingAction
		e.pendingInstanceID = ""
		e.pendingAction = ""
		if ts := transitionalState(action); ts != "" {
			e.setInstanceState(id, ts)
			e.spinner.Show(ts + " " + id + "...")
		}
		switch m.Action {
		case "ec2_stop":
			return e, tea.Batch(e.spinner.Tick(), e.stopInstance(id))
		case "ec2_reboot":
			return e, tea.Batch(e.spinner.Tick(), e.rebootInstance(id))
		case "ec2_terminate":
			return e, tea.Batch(e.spinner.Tick(), e.terminateInstance(id))
		}
		return e, nil

	case ec2InstanceMutatedMsg:
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
		return e, nil

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

		switch m.String() {
		case "esc":
			return e, func() tea.Msg { return msg.NavigateBackMsg{} }
		case "s":
			columns, currentCol := e.table.SortColumnNames()
			return e, func() tea.Msg {
				return msg.RequestSortPickerMsg{Columns: columns, CurrentCol: currentCol}
			}
		case "S":
			e.table.SortReverse()
			return e, nil
		case "/":
			e.filter.Activate()
			return e, nil
		case "r":
			e.loading = true
			e.err = nil
			e.spinner.Show("Loading EC2 instances...")
			return e, tea.Batch(e.spinner.Tick(), e.fetchInstances())
		case "enter", "d":
			selected := e.table.SelectedRow()
			if selected != nil {
				instanceID := selected[0]
				return e, e.fetchDetail(instanceID)
			}
		case "y":
			selected := e.table.SelectedRow()
			if selected != nil {
				id := selected[0]
				_ = clipboard.WriteAll(id)
				return e, func() tea.Msg {
					return msg.ToastSuccess("Copied: " + id)
				}
			}
		case "m":
			if ui.ReadOnly {
				return e, func() tea.Msg {
					return msg.ToastError("ReadOnly mode — press W to switch")
				}
			}
			selected := e.table.SelectedRow()
			if selected == nil {
				return e, nil
			}
			inst := e.findInstance(selected[0])
			if inst == nil {
				return e, nil
			}
			actions := e.actionsForState(inst.State)
			if len(actions) == 0 {
				state := inst.State
				return e, func() tea.Msg {
					return msg.ToastError("No actions available for " + state + " instance")
				}
			}
			e.pendingInstanceID = inst.ID
			return e, func() tea.Msg {
				return msg.RequestActionPickerMsg{
					Title:   "Manage Instance",
					Options: actions,
				}
			}
		case "o":
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

func (e *EC2List) startInstance(id string) tea.Cmd {
	svc := e.ec2
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Starting instance: %s", id)
		err := svc.StartInstance(context.Background(), id)
		return ec2InstanceMutatedMsg{action: "started", instanceID: id, err: err}
	}
}

func (e *EC2List) stopInstance(id string) tea.Cmd {
	svc := e.ec2
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Stopping instance: %s", id)
		err := svc.StopInstance(context.Background(), id)
		return ec2InstanceMutatedMsg{action: "stopped", instanceID: id, err: err}
	}
}

func (e *EC2List) rebootInstance(id string) tea.Cmd {
	svc := e.ec2
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Rebooting instance: %s", id)
		err := svc.RebootInstance(context.Background(), id)
		return ec2InstanceMutatedMsg{action: "rebooted", instanceID: id, err: err}
	}
}

func (e *EC2List) terminateInstance(id string) tea.Cmd {
	svc := e.ec2
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Terminating instance: %s", id)
		err := svc.TerminateInstance(context.Background(), id)
		return ec2InstanceMutatedMsg{action: "terminated", instanceID: id, err: err}
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
			b.WriteString(fmt.Sprintf("%-16s %s\n", f.k, f.v))
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
		b.WriteString(fmt.Sprintf("%-22s %s\n", sg.ID, sg.Name))
		links = append(links, msg.TabLink{
			Line:   i,
			ViewID: "sg_detail",
			Params: map[string]string{"id": sg.ID},
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
		b.WriteString(fmt.Sprintf("%-24s %s\n", k, tags[k]))
	}
	return b.String()
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
		filtered, total := e.table.RowCount()
		status := fmt.Sprintf("\n %d/%d instances", filtered, total)
		if e.spinner.Visible() {
			status += "  " + e.spinner.View()
		}
		content += status
	}
	return tea.NewView(content)
}
