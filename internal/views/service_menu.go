package views

import (
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// ServiceFeature represents a navigable sub-resource within an AWS service.
type ServiceFeature struct {
	Name   string
	ViewID string
	Icon   ui.ServiceIcon
}

// ServiceMenu is an intermediate view that lists the features/resources
// available within a single AWS service (e.g., EC2 → Instances, Security Groups).
type ServiceMenu struct {
	service  string
	features []ServiceFeature
	table    ui.Table
	filter   ui.Filter
	width    int
	height   int
}

func (s *ServiceMenu) ID() string    { return "service_menu:" + s.service }
func (s *ServiceMenu) Title() string { return s.service }
func (s *ServiceMenu) KeyMap() []ui.KeyHint {
	return []ui.KeyHint{
		{Key: "enter", Desc: "select"},
		{Key: "/", Desc: "filter"},
	}
}

// NewServiceMenu creates a feature selector for the given service.
func NewServiceMenu(service string, features []ServiceFeature) *ServiceMenu {
	columns := []table.Column{
		{Title: "", Width: 4},
		{Title: "Resource", Width: 30},
	}

	var rows []table.Row
	for _, f := range features {
		rows = append(rows, table.Row{f.Icon.Icon(), f.Name})
	}

	t := ui.NewTable(columns, rows)
	return &ServiceMenu{
		service:  service,
		features: features,
		table:    t,
		filter:   ui.NewFilter(),
	}
}

func (s *ServiceMenu) Init() tea.Cmd {
	return nil
}

func (s *ServiceMenu) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case tea.WindowSizeMsg:
		s.width = m.Width
		s.height = m.Height
		s.table.SetSize(m.Width, m.Height-2)
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

		switch m.String() {
		case "/":
			s.filter.Activate()
			return s, nil
		case "enter":
			selected := s.table.SelectedRow()
			if selected == nil {
				return s, nil
			}
			for _, f := range s.features {
				if f.Name == selected[1] {
					return s, func() tea.Msg {
						return msg.NavigateMsg{ViewID: f.ViewID}
					}
				}
			}
		case "esc":
			return s, func() tea.Msg {
				return msg.NavigateBackMsg{}
			}
		}
	}

	var cmd tea.Cmd
	s.table, cmd = s.table.Update(m)
	return s, cmd
}

func (s *ServiceMenu) View() tea.View {
	content := s.table.View()
	if s.filter.Active() {
		content = s.filter.View() + "\n" + content
	}
	return tea.NewView(content)
}
