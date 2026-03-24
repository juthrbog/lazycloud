package views

import (
	"context"
	"encoding/json"
	"fmt"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"

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

// AMIList displays EC2 AMIs.
type AMIList struct {
	ec2          aws.EC2Service
	table        ui.Table
	amis         []aws.AMI
	filter       ui.Filter
	search       textinput.Model
	searchActive bool
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
func (a *AMIList) KeyMap() []ui.KeyHint {
	hints := []ui.KeyHint{
		{Key: "enter/d", Desc: "details"},
		{Key: "y", Desc: "copy ID"},
		{Key: "p", Desc: "search public"},
		{Key: "s/S", Desc: "sort"},
		{Key: "/", Desc: "filter"},
		{Key: "r", Desc: "refresh"},
	}
	if !a.ownedMode {
		hints = append(hints, ui.KeyHint{Key: "r", Desc: "back to owned"})
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

	ti := textinput.New()
	ti.Prompt = "? "
	ti.Placeholder = "search public AMIs..."

	return &AMIList{
		ec2:       ec2,
		table:     ui.NewTable(columns, nil),
		filter:    ui.NewFilter(),
		search:    ti,
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
		wasNarrow := a.widthTier == ui.TierNarrow
		isNarrow := newTier == ui.TierNarrow
		a.widthTier = newTier
		if wasNarrow != isNarrow {
			a.table.SetColumns(amiColumns(newTier))
			if len(a.amis) > 0 {
				a.table.SetRows(buildAMIRows(a.amis, newTier))
			}
		}
		a.table.SetSize(m.Width, m.Height-3)
		a.filter.SetWidth(m.Width)
		a.search.SetWidth(m.Width - 4)
		return a, nil

	case ui.FilterChangedMsg:
		a.table.Filter(m.Text)
		return a, nil

	case tea.KeyPressMsg:
		// Search input active
		if a.searchActive {
			switch m.String() {
			case "esc":
				a.searchActive = false
				a.search.SetValue("")
				a.search.Blur()
				return a, nil
			case "enter":
				query := a.search.Value()
				a.search.SetValue("")
				a.search.Blur()
				a.searchActive = false
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

		switch m.String() {
		case "esc":
			return a, func() tea.Msg { return msg.NavigateBackMsg{} }
		case "s":
			columns, currentCol := a.table.SortColumnNames()
			return a, func() tea.Msg {
				return msg.RequestSortPickerMsg{Columns: columns, CurrentCol: currentCol}
			}
		case "S":
			a.table.SortReverse()
			return a, nil
		case "/":
			a.filter.Activate()
			return a, nil
		case "p":
			a.searchActive = true
			a.search.Focus()
			return a, nil
		case "y":
			selected := a.table.SelectedRow()
			if selected != nil {
				id := selected[0]
				_ = clipboard.WriteAll(id)
				return a, func() tea.Msg {
					return msg.ToastSuccess("Copied: " + id)
				}
			}
		case "r":
			a.loading = true
			a.err = nil
			a.ownedMode = true
			a.lastQuery = ""
			a.spinner.Show("Loading AMIs...")
			return a, tea.Batch(a.spinner.Tick(), a.fetchOwned())
		case "enter", "d":
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

func (a *AMIList) View() tea.View {
	var content string
	if a.loading && len(a.amis) == 0 {
		content = "\n  " + a.spinner.View()
	} else if a.err != nil {
		content = "\n  " + ui.ErrorStyle.Render("Error: "+a.err.Error())
	} else {
		content = a.table.View()
		if a.searchActive {
			content = ui.S.FilterPrompt.Render("?") + " " + a.search.View() + "\n" + content
		} else if a.filter.Active() {
			content = a.filter.View() + "\n" + content
		}
		filtered, total := a.table.RowCount()
		var status string
		if a.ownedMode {
			status = fmt.Sprintf("\n %d/%d AMIs (owned)", filtered, total)
		} else {
			status = fmt.Sprintf("\n %d/%d results for %q", filtered, total, a.lastQuery)
		}
		if a.spinner.Visible() {
			status += "  " + a.spinner.View()
		}
		content += status
	}
	return tea.NewView(content)
}
