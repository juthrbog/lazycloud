package views

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// amiFocusLoadedMsg holds the result of a fetch by ID for a focus link to an
// AMI that is not in the list. gen is the load generation when the fetch
// started.
type amiFocusLoadedMsg struct {
	gen  int
	id   string
	amis []aws.AMI
	err  error
}

// amiSearchKey opens the search of public AMIs.
var amiSearchKey = ui.NewHintBinding([]string{"p"}, "p", "search public")

// amiSearchSource marks the messages of the search input.
const amiSearchSource = "ami-search"

// AMIList displays EC2 AMIs: the owned AMIs, or the result of a search of
// public AMIs.
type AMIList struct {
	listCore[aws.AMI]
	ec2         aws.EC2Service
	searchInput ui.Filter
	shown       string // what the rows are: "" for the owned AMIs, else the search query
	linked      int    // rows that a focus link added to the list
}

// NewAMIList creates the AMI list view.
func NewAMIList(ec2 aws.EC2Service) *AMIList {
	a := &AMIList{
		ec2:         ec2,
		searchInput: ui.NewFilterWithPrompt("?", "search public AMIs...").WithSource(amiSearchSource),
	}
	a.listCore = newListCore(listConfig[aws.AMI]{
		Noun:         "AMIs",
		LoadingLabel: "Loading AMIs...",
		ErrContext:   "listing owned AMIs",
		Key:          func(ami aws.AMI) string { return ami.ID },
		Columns:      amiColumns,
		Row:          amiRow,
		Fetch:        fetchAll(ec2, aws.EC2Service.ListOwnedAMIs),
		FetchTimeout: listTimeout,
		OnLoaded:     a.onLoaded,
		Refresh:      refreshBy(ec2, aws.EC2Service.RefreshAMIs, callTimeout),
		OpenDetail:   amiDetail,
		FocusMissing: a.fetchLinked,
		MultiSelect:  true,
		Copy:         true,
		CopyNoun:     "AMI ID",
	})
	return a
}

func (a *AMIList) ID() string     { return "ami_list" }
func (a *AMIList) Title() string  { return "AMIs" }
func (a *AMIList) Init() tea.Cmd  { return a.init() }
func (a *AMIList) Footer() string { return a.footerWith(a.footerBase()) }

// CapturingInput implements nav.InputCapturer: true while the filter or the
// search is open.
func (a *AMIList) CapturingInput() bool {
	return a.filter.Active() || a.searchInput.Active()
}

func (a *AMIList) KeyMap() []ui.HintBinding {
	return a.hints(listKeys.Details, listKeys.CopyID, listKeys.Select, amiSearchKey,
		listKeys.Sort, listKeys.Filter, listKeys.Refresh)
}

func (a *AMIList) View() tea.View {
	above := ""
	if a.searchInput.Active() {
		above = a.searchInput.View() + "\n"
	}
	return tea.NewView(a.render(above))
}

// Update handles the search input, p and the fetch of a linked AMI, and
// hands the rest to the core. It returns the view itself, so the navigator
// keeps its identity.
func (a *AMIList) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case amiFocusLoadedMsg:
		return a, a.showLinked(m)
	case tea.WindowSizeMsg:
		a.searchInput.SetWidth(m.Width)
	case tea.KeyPressMsg:
		if a.searchInput.Active() {
			return a, a.searchKey(m)
		}
		if !a.filter.Active() && key.Matches(m, amiSearchKey.Binding) {
			a.searchInput.Activate()
			return a, nil
		}
	}
	return a, a.update(m)
}

// searchKey handles a key while the search is open. esc closes it; enter
// closes it and starts the search, unless the query is empty.
func (a *AMIList) searchKey(m tea.KeyPressMsg) tea.Cmd {
	switch m.String() {
	case "esc":
		a.searchInput.Deactivate()
		return nil
	case "enter":
		query := a.searchInput.Value()
		a.searchInput.Deactivate()
		if query == "" {
			return nil
		}
		return a.searchAMIs(query)
	}
	var cmd tea.Cmd
	a.searchInput, cmd = a.searchInput.Update(m)
	return cmd
}

// searchAMIs starts a search of public AMIs. It replaces the current load,
// so of a search and an owned load the one started last wins. r goes back
// to the owned AMIs.
func (a *AMIList) searchAMIs(query string) tea.Cmd {
	eventlog.Infof(eventlog.CatAWS, "Searching AMIs: %q", query)
	return a.loadWith(loadSpec[aws.AMI]{
		fetch: fetchAll(a.ec2, func(s aws.EC2Service, ctx context.Context) ([]aws.AMI, error) {
			return s.SearchAMIs(ctx, query)
		}),
		// No overall limit: SearchAMIs reads at most 20 pages of the public
		// catalog, which can take longer than listTimeout for a rare name.
		// The read timeout of the client still stops a stalled page, and
		// leaving the view cancels the search.
		timeout:    0,
		errContext: "searching AMIs",
		label:      "Searching AMIs...",
		tag:        query,
	})
}

// onLoaded runs after a full load. The rows are now the owned AMIs or the
// result of the query in tag, and a linked row is gone.
func (a *AMIList) onLoaded(_ []aws.AMI, tag string) tea.Cmd {
	a.shown = tag
	a.linked = 0
	return nil
}

func (a *AMIList) footerBase() string {
	filtered, total := a.table.RowCount()
	switch {
	case a.shown != "":
		return fmt.Sprintf("%d/%d results for %q", filtered, total, a.shown)
	case a.linked > 0:
		return fmt.Sprintf("%d/%d AMIs (owned + %d linked)", filtered, total, a.linked)
	}
	return fmt.Sprintf("%d/%d AMIs (owned)", filtered, total)
}

// fetchLinked fetches an AMI that a focus link asks for but the list does
// not hold. The list holds only the owned AMIs or a search result, and most
// instances use an AMI of another account.
func (a *AMIList) fetchLinked(id string) tea.Cmd {
	gen := a.loadGen
	refresh := a.cfg.Refresh
	ctx := a.viewContext()
	return tea.Batch(a.busy("Loading AMI..."), func() tea.Msg {
		amis, err := refresh(ctx, []string{id})
		return amiFocusLoadedMsg{gen: gen, id: id, amis: amis, err: err}
	})
}

// showLinked adds a fetched AMI to the list and opens it. When the fetch
// finds nothing, a toast says so and the list stays as it is. When a full
// load started after the fetch, the rows belong to that load: the panel
// opens, but the AMI gets no row.
func (a *AMIList) showLinked(m amiFocusLoadedMsg) tea.Cmd {
	a.idle()
	if errors.Is(m.err, context.Canceled) {
		return nil
	}
	if m.err != nil {
		eventlog.Errorf(eventlog.CatAWS, "AMI %s: %v", m.id, m.err)
		text := fmt.Sprintf("Could not load AMI %s: %s", m.id, aws.ErrorText(m.err))
		return func() tea.Msg { return msg.ToastError(text) }
	}
	i := slices.IndexFunc(m.amis, func(ami aws.AMI) bool { return ami.ID == m.id })
	if i < 0 {
		text := fmt.Sprintf("AMI %s not found or not visible to this account", m.id)
		return func() tea.Msg { return msg.ToastError(text) }
	}
	if m.gen != a.loadGen {
		return amiDetail(m.amis[i])
	}
	if a.find(m.id) == nil {
		a.setItems(append(slices.Clip(a.items), m.amis[i]))
		a.linked++
	}
	return a.focus(m.id)
}

// amiDetail opens the detail panel of an AMI: its JSON in one tab.
func amiDetail(ami aws.AMI) tea.Cmd {
	content, _ := json.MarshalIndent(ami, "", "  ")
	title := ami.ID
	if ami.Name != "" {
		title = ami.Name + " (" + ami.ID + ")"
	}
	tabs := []msg.TabContent{{Title: "JSON", Content: string(content), Format: "json"}}
	return func() tea.Msg {
		return msg.TabbedContentMsg{PanelTitle: title, Tabs: tabs}
	}
}

func amiColumns(tier ui.WidthTier) []ui.Column {
	if tier == ui.TierNarrow {
		return []ui.Column{
			{Title: "AMI ID", Width: 21, Weight: 1, MaxWidth: 35},
			{Title: "Name", Width: 34, Weight: 2, MaxWidth: 60},
			{Title: "State", Width: 14, Style: ui.StateColor},
		}
	}
	if tier == ui.TierMedium {
		return []ui.Column{
			{Title: "AMI ID", Width: 21, Weight: 1, MaxWidth: 35},
			{Title: "Name", Width: 20, Weight: 2, MaxWidth: 60},
			{Title: "Architecture", Width: 12},
			{Title: "State", Width: 10, Style: ui.StateColor},
		}
	}
	return []ui.Column{
		{Title: "AMI ID", Width: 21, Weight: 1, MaxWidth: 35},
		{Title: "Name", Width: 34, Weight: 2, MaxWidth: 60},
		{Title: "Owner", Width: 16, Weight: 1, MaxWidth: 25},
		{Title: "Architecture", Width: 14},
		{Title: "State", Width: 14, Style: ui.StateColor},
		{Title: "Created", Width: 12, Weight: 1, MaxWidth: 25},
	}
}

// amiRow builds the row of one AMI. The wide row shows the owner alias
// before the owner ID, and the date only.
func amiRow(ami aws.AMI, tier ui.WidthTier) ui.Row {
	var cells table.Row
	switch tier {
	case ui.TierNarrow:
		cells = table.Row{ami.ID, ami.Name, ami.State}
	case ui.TierMedium:
		cells = table.Row{ami.ID, ami.Name, ami.Architecture, ami.State}
	default:
		owner := ami.OwnerID
		if ami.OwnerAlias != "" {
			owner = ami.OwnerAlias
		}
		created := ami.CreationDate
		if len(created) >= 10 {
			created = created[:10]
		}
		cells = table.Row{ami.ID, ami.Name, owner, ami.Architecture, ami.State, created}
	}
	return ui.Row{Key: ami.ID, Cells: cells}
}
