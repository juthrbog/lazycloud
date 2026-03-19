package views

import (
	"context"
	"fmt"
	"strings"

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
}

func (e *EC2List) ID() string    { return "ec2_list" }
func (e *EC2List) Title() string { return "EC2 Instances" }
func (e *EC2List) KeyMap() []ui.KeyHint {
	hints := []ui.KeyHint{
		{Key: "enter/d", Desc: "details"},
		{Key: "o", Desc: "connect (SSM)"},
	}
	if !ui.ReadOnly {
		hints = append(hints, ui.KeyHint{Key: "m", Desc: "manage"})
	}
	hints = append(hints,
		ui.KeyHint{Key: "y", Desc: "copy ID"},
		ui.KeyHint{Key: "s/S", Desc: "sort"},
		ui.KeyHint{Key: "/", Desc: "filter"},
		ui.KeyHint{Key: "r", Desc: "refresh"},
	)
	return hints
}

// NewEC2List creates the EC2 instance list view.
func NewEC2List(ec2 aws.EC2Service, awsClient *aws.Client) *EC2List {
	columns := []table.Column{
		{Title: "Instance ID", Width: 21},
		{Title: "Name", Width: 24},
		{Title: "State", Width: 16},
		{Title: "Type", Width: 14},
		{Title: "Private IP", Width: 16},
		{Title: "Public IP", Width: 16},
		{Title: "AZ", Width: 14},
		{Title: "Launched", Width: 12},
	}

	t := ui.NewTable(columns, nil)
	return &EC2List{
		ec2:       ec2,
		awsClient: awsClient,
		table:     t,
		filter:    ui.NewFilter(),
		spinner:   ui.NewSpinner("Loading EC2 instances..."),
		loading:   true,
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
				return e, e.startInstance(id)
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
		e.pendingInstanceID = ""
		switch m.Action {
		case "ec2_stop":
			return e, e.stopInstance(id)
		case "ec2_reboot":
			return e, e.rebootInstance(id)
		case "ec2_terminate":
			return e, e.terminateInstance(id)
		}
		return e, nil

	case ec2InstanceMutatedMsg:
		if m.err != nil {
			e.err = m.err
			return e, func() tea.Msg {
				return msg.ToastError(m.action + " failed: " + m.err.Error())
			}
		}
		e.loading = true
		e.spinner.Show("Refreshing instances...")
		action := m.action
		id := m.instanceID
		return e, tea.Batch(e.spinner.Tick(), e.fetchInstances(), func() tea.Msg {
			return msg.ToastSuccess("Instance " + action + ": " + id)
		})

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
		title := m.detail.InstanceID
		if m.detail.Name != "" {
			title = m.detail.Name + " (" + m.detail.InstanceID + ")"
		}
		content := m.detail.DetailJSON()
		return e, func() tea.Msg {
			return msg.NavigateMsg{
				ViewID: "content",
				Params: map[string]string{
					"title":   title,
					"content": content,
					"format":  "json",
				},
			}
		}

	case msg.ErrorMsg:
		e.loading = false
		e.spinner.Hide()
		e.err = m.Err
		return e, nil

	case tea.WindowSizeMsg:
		e.width = m.Width
		e.height = m.Height
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
	for _, inst := range instances {
		launched := ""
		if !inst.LaunchTime.IsZero() {
			launched = inst.LaunchTime.Format("2006-01-02")
		}
		rows = append(rows, table.Row{
			inst.ID,
			inst.Name,
			ui.StateColor(inst.State),
			inst.Type,
			inst.PrivateIP,
			inst.PublicIP,
			inst.AvailabilityZone,
			launched,
		})
		// Sort keys use plain state string (no ANSI codes) for correct sorting
		sortKeys = append(sortKeys, table.Row{
			inst.ID,
			inst.Name,
			inst.State,
			inst.Type,
			inst.PrivateIP,
			inst.PublicIP,
			inst.AvailabilityZone,
			launched,
		})
	}
	return rows, sortKeys
}

func (e *EC2List) View() tea.View {
	var content string
	if e.loading {
		content = "\n  " + e.spinner.View()
	} else if e.err != nil {
		content = "\n  " + ui.ErrorStyle.Render("Error: "+e.err.Error())
	} else {
		content = e.table.View()
		if e.filter.Active() {
			content = e.filter.View() + "\n" + content
		}
		filtered, total := e.table.RowCount()
		content += fmt.Sprintf("\n %d/%d instances", filtered, total)
	}
	return tea.NewView(content)
}
