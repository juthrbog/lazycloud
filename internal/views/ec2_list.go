package views

import (
	"context"
	"fmt"

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

// EC2List displays all EC2 instances.
type EC2List struct {
	ec2       aws.EC2Service
	table     ui.Table
	instances []aws.Instance
	filter    ui.Filter
	spinner   ui.Spinner
	loading   bool
	err       error
	width     int
	height    int
}

func (e *EC2List) ID() string    { return "ec2_list" }
func (e *EC2List) Title() string { return "EC2 Instances" }
func (e *EC2List) KeyMap() []ui.KeyHint {
	return []ui.KeyHint{
		{Key: "enter/d", Desc: "details"},
		{Key: "y", Desc: "copy ID"},
		{Key: "s/S", Desc: "sort"},
		{Key: "/", Desc: "filter"},
		{Key: "r", Desc: "refresh"},
	}
}

// NewEC2List creates the EC2 instance list view.
func NewEC2List(ec2 aws.EC2Service) *EC2List {
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
		ec2:     ec2,
		table:   t,
		filter:  ui.NewFilter(),
		spinner: ui.NewSpinner("Loading EC2 instances..."),
		loading: true,
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
		}
		return e, nil

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
