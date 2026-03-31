package views

import (
	"context"
	"encoding/json"
	"fmt"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

type amiListLoadedMsg struct {
	amis  []aws.AMI
	owned bool
	query string // non-empty when this is a search result
}

type amiListKeyMap struct {
	Esc          key.Binding
	Details      key.Binding
	Describe     key.Binding
	CopyID       key.Binding
	SearchPublic key.Binding
	Sort         key.Binding
	SortReverse  key.Binding
	Filter       key.Binding
	Refresh      key.Binding
}

var defaultAMIListKeyMap = amiListKeyMap{
	Esc:          key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Details:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter/d", "details")),
	Describe:     key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "details")),
	CopyID:       key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy ID")),
	SearchPublic: key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "search public")),
	Sort:         key.NewBinding(key.WithKeys("s"), key.WithHelp("s/S", "sort")),
	SortReverse:  key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "reverse sort")),
	Filter:       key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
	Refresh:      key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
}

// AMIList displays EC2 AMIs.
type AMIList struct {
	keys         amiListKeyMap
	ec2          aws.EC2Service
	table        ui.Table
	amis         []aws.AMI
	filter       ui.Filter
	search       ui.Filter
	lastQuery    string // query used for current search results
	ownedMode    bool
	spinner      ui.Spinner
	loading      bool
	err          error
	width        int
	height       int
	widthTier    ui.WidthTier
}

func (a *AMIList) ID() string    { return "ami_list" }
func (a *AMIList) Title() string { return "AMIs" }
func (a *AMIList) KeyMap() []ui.HintBinding {
	hints := []ui.HintBinding{
		{Binding: a.keys.Details},
		{Binding: a.keys.CopyID},
		{Binding: a.keys.SearchPublic},
		{Binding: a.keys.Sort},
		{Binding: a.keys.Filter},
		{Binding: a.keys.Refresh},
	}
	if !a.ownedMode {
		hints = append(hints, ui.HintBinding{Binding: a.keys.Refresh})
	}
	return hints
}

func amiColumns(tier ui.WidthTier) []table.Column {
	if tier == ui.TierNarrow {
		return []table.Column{
			{Title: "AMI ID", Width: 21},
			{Title: "Name", Width: 34},
			{Title: "State", Width: 14},
		}
	}
	return []table.Column{
		{Title: "AMI ID", Width: 21},
		{Title: "Name", Width: 34},
		{Title: "Owner", Width: 16},
		{Title: "Architecture", Width: 14},
		{Title: "State", Width: 14},
		{Title: "Created", Width: 12},
	}
}

// NewAMIList creates the AMI list view.
func NewAMIList(ec2 aws.EC2Service) *AMIList {
	columns := amiColumns(ui.TierMedium)

	return &AMIList{
		keys:      defaultAMIListKeyMap,
		ec2:       ec2,
		table:     ui.NewTable(columns, nil),
		filter:    ui.NewFilter(),
		search:    ui.NewFilterWithPrompt("?", "search public AMIs..."),
		ownedMode: true,
		spinner:   ui.NewSpinner("Loading AMIs..."),
		loading:   true,
		widthTier: ui.TierMedium,
	}
}

func (a *AMIList) Init() tea.Cmd {
	if !a.loading {
		return nil
	}
	return tea.Batch(a.spinner.Tick(), a.fetchOwned())
}

func (a *AMIList) fetchOwned() tea.Cmd {
	svc := a.ec2
	return func() tea.Msg {
		if svc == nil {
			return msg.ErrorMsg{Err: fmt.Errorf("AWS client not initialized"), Context: "EC2"}
		}
		amis, err := svc.ListOwnedAMIs(context.Background())
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: "listing owned AMIs"}
		}
		eventlog.Infof(eventlog.CatAWS, "Loaded %d owned AMIs", len(amis))
		return amiListLoadedMsg{amis: amis, owned: true}
	}
}

func (a *AMIList) fetchSearch(query string) tea.Cmd {
	svc := a.ec2
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Searching AMIs: %q", query)
		amis, err := svc.SearchAMIs(context.Background(), query)
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: "searching AMIs"}
		}
		eventlog.Infof(eventlog.CatAWS, "Found %d AMIs for %q", len(amis), query)
		return amiListLoadedMsg{amis: amis, owned: false, query: query}
	}
}

func (a *AMIList) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case ui.PickerResultMsg:
		if m.ID == "sort" {
			if m.Value == "_clear" {
				a.table.ClearSort()
			} else if m.Selected >= 0 {
				a.table.Sort(m.Selected)
			}
		}
		return a, nil

	case amiListLoadedMsg:
		a.loading = false
		a.spinner.Hide()
		a.amis = m.amis
		a.ownedMode = m.owned
		a.lastQuery = m.query
		rows := buildAMIRows(m.amis, a.widthTier)
		a.table.SetRows(rows)
		return a, nil

	case msg.ErrorMsg:
		a.loading = false
		a.spinner.Hide()
		a.err = m.Err
		return a, nil

	case tea.WindowSizeMsg:
		a.width = m.Width
		a.height = m.Height
		newTier := ui.GetWidthTier(m.Width)
		a.widthTier = newTier

		cols := amiColumns(newTier)
		if !ui.ColumnsFit(cols, m.Width) {
			cols = amiColumns(ui.TierNarrow)
			a.widthTier = ui.TierNarrow
		}
		if len(cols) != len(a.table.Columns()) {
			a.table.SetColumns(cols)
			if len(a.amis) > 0 {
				a.table.SetRows(buildAMIRows(a.amis, a.widthTier))
			}
		}
		a.table.SetSize(m.Width, m.Height-3)
		a.filter.SetWidth(m.Width)
		a.search.SetWidth(m.Width)
		return a, nil

	case ui.FilterChangedMsg:
		a.table.Filter(m.Text)
		return a, nil

	case tea.KeyPressMsg:
		// Search input active
		if a.search.Active() {
			switch m.String() {
			case "esc":
				a.search.Deactivate()
				return a, nil
			case "enter":
				query := a.search.Value()
				a.search.Deactivate()
				if query == "" {
					return a, nil
				}
				a.loading = true
				a.err = nil
				a.spinner.Show("Searching AMIs...")
				return a, tea.Batch(a.spinner.Tick(), a.fetchSearch(query))
			}
			var cmd tea.Cmd
			a.search, cmd = a.search.Update(m)
			return a, cmd
		}

		// Filter input active
		if a.filter.Active() {
			var cmd tea.Cmd
			a.filter, cmd = a.filter.Update(m)
			return a, cmd
		}

		switch {
		case key.Matches(m, a.keys.Esc):
			return a, func() tea.Msg { return msg.NavigateBackMsg{} }
		case key.Matches(m, a.keys.Sort):
			columns, currentCol := a.table.SortColumnNames()
			return a, func() tea.Msg {
				return msg.RequestSortPickerMsg{Columns: columns, CurrentCol: currentCol}
			}
		case key.Matches(m, a.keys.SortReverse):
			a.table.SortReverse()
			return a, nil
		case key.Matches(m, a.keys.Filter):
			a.filter.Activate()
			return a, nil
		case key.Matches(m, a.keys.SearchPublic):
			a.search.Activate()
			return a, nil
		case key.Matches(m, a.keys.CopyID):
			selected := a.table.SelectedRow()
			if selected != nil {
				id := selected[0]
				return a, tea.Batch(
					tea.SetClipboard(id),
					func() tea.Msg { return msg.ToastSuccess("Copied: " + id) },
				)
			}
		case key.Matches(m, a.keys.Refresh):
			a.loading = true
			a.err = nil
			a.ownedMode = true
			a.lastQuery = ""
			a.spinner.Show("Loading AMIs...")
			return a, tea.Batch(a.spinner.Tick(), a.fetchOwned())
		case key.Matches(m, a.keys.Details, a.keys.Describe):
			selected := a.table.SelectedRow()
			if selected == nil {
				return a, nil
			}
			amiID := selected[0]
			ami := a.findAMI(amiID)
			if ami == nil {
				return a, nil
			}
			content, _ := json.MarshalIndent(ami, "", "  ")
			title := ami.ID
			if ami.Name != "" {
				title = ami.Name + " (" + ami.ID + ")"
			}
			c := string(content)
			return a, func() tea.Msg {
				return msg.NavigateMsg{
					ViewID: "content",
					Params: map[string]string{
						"title":   title,
						"content": c,
						"format":  "json",
					},
				}
			}
		}
	}

	if a.loading {
		var cmd tea.Cmd
		a.spinner, cmd = a.spinner.Update(m)
		return a, cmd
	}

	var cmd tea.Cmd
	a.table, cmd = a.table.Update(m)
	return a, cmd
}

func (a *AMIList) findAMI(id string) *aws.AMI {
	for i := range a.amis {
		if a.amis[i].ID == id {
			return &a.amis[i]
		}
	}
	return nil
}

func buildAMIRows(amis []aws.AMI, tier ui.WidthTier) []table.Row {
	rows := make([]table.Row, 0, len(amis))
	narrow := tier == ui.TierNarrow
	for _, ami := range amis {
		owner := ami.OwnerID
		if ami.OwnerAlias != "" {
			owner = ami.OwnerAlias
		}
		created := ami.CreationDate
		if len(created) >= 10 {
			created = created[:10]
		}
		if narrow {
			rows = append(rows, table.Row{
				ami.ID, ami.Name, ui.StateColor(ami.State),
			})
		} else {
			rows = append(rows, table.Row{
				ami.ID, ami.Name, owner, ami.Architecture,
				ui.StateColor(ami.State), created,
			})
		}
	}
	return rows
}

func (a *AMIList) Footer() string {
	filtered, total := a.table.RowCount()
	var footer string
	if a.ownedMode {
		footer = fmt.Sprintf("%d/%d AMIs (owned)", filtered, total)
	} else {
		footer = fmt.Sprintf("%d/%d results for %q", filtered, total, a.lastQuery)
	}
	if a.spinner.Visible() {
		footer += "  " + a.spinner.View()
	}
	return footer
}

func (a *AMIList) View() tea.View {
	var content string
	if a.loading && len(a.amis) == 0 {
		content = "\n  " + a.spinner.View()
	} else if a.err != nil {
		content = "\n  " + ui.ErrorStyle.Render("Error: "+a.err.Error())
	} else {
		content = a.table.View()
		if a.search.Active() {
			content = a.search.View() + "\n" + content
		} else if a.filter.Active() {
			content = a.filter.View() + "\n" + content
		}
	}
	return tea.NewView(content)
}
