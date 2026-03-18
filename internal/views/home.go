package views

import (
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

type serviceEntry struct {
	Name     string
	Icon     ui.ServiceIcon
	Features []ServiceFeature
}

var services = []serviceEntry{
	{Name: "S3", Icon: ui.IconS3, Features: []ServiceFeature{
		{Name: "Buckets", ViewID: "s3_list", Icon: ui.IconS3},
	}},
}

// ServiceFeatures returns the features for the named service, or nil if not found.
func ServiceFeatures(name string) []ServiceFeature {
	for _, svc := range services {
		if svc.Name == name {
			return svc.Features
		}
	}
	return nil
}

// Home is the service selector dashboard.
type Home struct {
	table  ui.Table
	filter ui.Filter
	width  int
	height int
}

func (h *Home) ID() string    { return "home" }
func (h *Home) Title() string { return "Services" }
func (h *Home) KeyMap() []ui.KeyHint {
	return []ui.KeyHint{
		{Key: "enter", Desc: "select"},
		{Key: "/", Desc: "filter"},
	}
}

// NewHome creates the home service selector view.
func NewHome() *Home {
	columns := []table.Column{
		{Title: "", Width: 4},
		{Title: "Service", Width: 26},
	}

	var rows []table.Row
	for _, s := range services {
		rows = append(rows, table.Row{s.Icon.Icon(), s.Name})
	}

	t := ui.NewTable(columns, rows)
	return &Home{
		table:  t,
		filter: ui.NewFilter(),
	}
}

func (h *Home) Init() tea.Cmd {
	return nil
}

func (h *Home) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case tea.WindowSizeMsg:
		h.width = m.Width
		h.height = m.Height
		h.table.SetSize(m.Width, m.Height-2)
		h.filter.SetWidth(m.Width)
		return h, nil

	case ui.FilterChangedMsg:
		h.table.Filter(m.Text)
		return h, nil

	case tea.KeyPressMsg:
		if h.filter.Active() {
			var cmd tea.Cmd
			h.filter, cmd = h.filter.Update(m)
			return h, cmd
		}

		switch m.String() {
		case "/":
			h.filter.Activate()
			return h, nil
		case "enter":
			selected := h.table.SelectedRow()
			if selected == nil {
				return h, nil
			}
			for _, svc := range services {
				if svc.Name == selected[1] {
					return h, h.navigateService(svc)
				}
			}
		}
	}

	var cmd tea.Cmd
	h.table, cmd = h.table.Update(m)
	return h, cmd
}

// navigateService either goes directly to the resource view (single feature)
// or opens an intermediate service menu (multiple features).
func (h *Home) navigateService(svc serviceEntry) tea.Cmd {
	if len(svc.Features) == 1 {
		return func() tea.Msg {
			return msg.NavigateMsg{ViewID: svc.Features[0].ViewID}
		}
	}
	return func() tea.Msg {
		return msg.NavigateMsg{
			ViewID: "service_menu",
			Params: map[string]string{"service": svc.Name},
		}
	}
}

func (h *Home) View() tea.View {
	content := h.table.View()
	if h.filter.Active() {
		content = h.filter.View() + "\n" + content
	}
	return tea.NewView(content)
}
