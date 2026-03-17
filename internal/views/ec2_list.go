package views

import (
	"encoding/json"
	"fmt"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

type mockInstance struct {
	ID         string
	Name       string
	State      string
	Type       string
	PrivateIP  string
	PublicIP   string
	LaunchTime string
}

var mockInstances = []mockInstance{
	{ID: "i-0abc123def456", Name: "web-server-1", State: "running", Type: "t3.medium", PrivateIP: "10.0.1.50", PublicIP: "54.123.45.67", LaunchTime: "2026-01-15"},
	{ID: "i-0def789ghi012", Name: "api-server-1", State: "running", Type: "t3.large", PrivateIP: "10.0.1.51", PublicIP: "54.123.45.68", LaunchTime: "2026-02-01"},
	{ID: "i-0ghi345jkl678", Name: "worker-1", State: "stopped", Type: "m5.xlarge", PrivateIP: "10.0.2.10", PublicIP: "", LaunchTime: "2025-12-20"},
	{ID: "i-0jkl901mno234", Name: "bastion", State: "running", Type: "t3.micro", PrivateIP: "10.0.0.5", PublicIP: "54.123.45.70", LaunchTime: "2026-03-01"},
	{ID: "i-0mno567pqr890", Name: "db-proxy", State: "running", Type: "t3.small", PrivateIP: "10.0.3.20", PublicIP: "", LaunchTime: "2026-02-15"},
}

type ec2LoadedMsg struct {
	instances []mockInstance
}

// EC2List displays EC2 instances with mock data.
type EC2List struct {
	table   ui.Table
	filter  ui.Filter
	spinner ui.Spinner
	loading bool
	err     error
	width   int
	height  int
}

func (e *EC2List) ID() string    { return "ec2_list" }
func (e *EC2List) Title() string { return "EC2 Instances" }

// NewEC2List creates an EC2 list view with mock data.
func NewEC2List() *EC2List {
	columns := []table.Column{
		{Title: "ID", Width: 18},
		{Title: "Name", Width: 16},
		{Title: "State", Width: 10},
		{Title: "Type", Width: 12},
		{Title: "Private IP", Width: 14},
		{Title: "Public IP", Width: 16},
		{Title: "Launched", Width: 12},
	}

	t := ui.NewTable(columns, nil)
	return &EC2List{
		table:   t,
		filter:  ui.NewFilter(),
		spinner: ui.NewSpinner("Loading EC2 instances..."),
		loading: true,
	}
}

func (e *EC2List) Init() tea.Cmd {
	return tea.Batch(e.spinner.Tick(), e.fetchInstances())
}

func (e *EC2List) fetchInstances() tea.Cmd {
	return func() tea.Msg {
		time.Sleep(500 * time.Millisecond)
		return ec2LoadedMsg{instances: mockInstances}
	}
}

func (e *EC2List) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case ec2LoadedMsg:
		e.loading = false
		e.spinner.Hide()
		var rows []table.Row
		for _, inst := range m.instances {
			rows = append(rows, table.Row{
				inst.ID, inst.Name, ui.StateColor(inst.State), inst.Type,
				inst.PrivateIP, inst.PublicIP, inst.LaunchTime,
			})
		}
		e.table.SetRows(rows)
		return e, nil

	case msg.ErrorMsg:
		e.loading = false
		e.spinner.Hide()
		e.err = m.Err
		return e, nil

	case msg.RefreshMsg:
		e.loading = true
		e.spinner.Show("Loading EC2 instances...")
		e.err = nil
		return e, tea.Batch(e.spinner.Tick(), e.fetchInstances())

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
		case "/":
			e.filter.Activate()
			return e, nil
		case "r":
			e.loading = true
			e.spinner.Show("Loading EC2 instances...")
			return e, tea.Batch(e.spinner.Tick(), e.fetchInstances())
		case "enter", "d":
			selected := e.table.SelectedRow()
			if selected == nil {
				return e, nil
			}
			// Find the mock instance for detail view
			for _, inst := range mockInstances {
				if inst.ID == selected[0] {
					jsonBytes, _ := json.MarshalIndent(inst, "", "  ")
					return e, func() tea.Msg {
						return msg.NavigateMsg{
							ViewID: "content",
							Params: map[string]string{
								"title":   inst.ID,
								"content": string(jsonBytes),
								"format":  "json",
							},
						}
					}
				}
			}
		}
	}

	// Spinner updates
	if e.loading {
		var cmd tea.Cmd
		e.spinner, cmd = e.spinner.Update(m)
		return e, cmd
	}

	var cmd tea.Cmd
	e.table, cmd = e.table.Update(m)
	return e, cmd
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
