package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The CloudWatch Logs lists. See docs/design/cwlogs.md.

const (
	// logGroupPageBudget is 20 pages of 50: 1,000 groups (decision D6).
	logGroupPageBudget = 20
	// logStreamPageBudget is 4 pages of 50: 200 streams (decision D6).
	logStreamPageBudget = 4
)

// logsKeys are the keys of the log lists that the core does not have.
var logsKeys = struct {
	Streams  ui.HintBinding
	Details  ui.HintBinding
	CopyName ui.HintBinding
}{
	Streams:  ui.NewHintBinding([]string{"v"}, "enter/v", "streams"),
	Details:  ui.NewHintBinding([]string{"d"}, "d", "details"),
	CopyName: ui.NewHintBinding([]string{"y"}, "y", "copy name"),
}

// logGroupEnter opens the streams of the cursor group. PR 5 of the design
// moves enter to the events of the group, and v keeps the streams.
var logGroupEnter = key.NewBinding(key.WithKeys("enter"))

// LogGroups displays the CloudWatch Logs log groups.
type LogGroups struct {
	listCore[aws.LogGroup]
}

// NewLogGroups creates the log group list view.
func NewLogGroups(logs aws.LogsService) *LogGroups {
	return &LogGroups{listCore: newListCore(listConfig[aws.LogGroup]{
		Noun:         "log groups",
		LoadingLabel: "Loading log groups...",
		ErrContext:   "listing log groups",
		Key:          func(g aws.LogGroup) string { return g.Name },
		Columns:      logGroupColumns,
		Row:          logGroupRow,
		Fetch:        fetchPages(logs, aws.LogsService.ListLogGroupsPage, splitLogGroupPage),
		FetchTimeout: callTimeout,
		FetchDetail:  logGroupDetail(logs),
		PageBudget:   logGroupPageBudget,
		MoreKey:      true,
		Copy:         true,
	})}
}

func (g *LogGroups) ID() string     { return "logs_groups" }
func (g *LogGroups) Title() string  { return "Log Groups" }
func (g *LogGroups) Init() tea.Cmd  { return g.init() }
func (g *LogGroups) Footer() string { return g.footer() }
func (g *LogGroups) View() tea.View { return tea.NewView(g.render("", "")) }

func (g *LogGroups) KeyMap() []ui.HintBinding {
	return g.hints(logsKeys.Streams, logsKeys.Details, logsKeys.CopyName, listKeys.More,
		listKeys.Sort, listKeys.Filter, listKeys.Refresh)
}

// Update returns the view itself, so the navigator keeps its identity.
func (g *LogGroups) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := m.(tea.KeyPressMsg); ok && !g.filter.Active() && g.err == nil &&
		key.Matches(k, logGroupEnter, logsKeys.Streams.Binding) {
		if name := g.table.CursorKey(); name != "" {
			return g, navigate("logs_streams", map[string]string{"log_group": name})
		}
	}
	return g, g.update(m)
}

// navigate returns a command that opens the view viewID.
func navigate(viewID string, params map[string]string) tea.Cmd {
	return func() tea.Msg { return msg.NavigateMsg{ViewID: viewID, Params: params} }
}

func splitLogGroupPage(p *aws.LogGroupPage) ([]aws.LogGroup, *string) {
	if p == nil {
		return nil, nil
	}
	return p.Groups, p.Next
}

func logGroupColumns(tier ui.WidthTier) []ui.Column {
	if tier == ui.TierNarrow {
		return []ui.Column{
			{Title: "Name", Width: 30, Weight: 3, MaxWidth: 120},
			{Title: "Retention", Width: 10},
		}
	}
	return []ui.Column{
		{Title: "Name", Width: 30, Weight: 3, MaxWidth: 120},
		{Title: "Class", Width: 10},
		{Title: "Retention", Width: 10},
		{Title: "Stored", Width: 10},
		{Title: "Created", Width: 10},
	}
}

// logGroupRow builds the row of one group. Retention and Stored sort as
// numbers, and "never" sorts after every number of days.
func logGroupRow(g aws.LogGroup, tier ui.WidthTier) ui.Row {
	retention := logRetentionText(g.RetentionDays)
	retentionKey := ui.SortKeyInt(int64(g.RetentionDays))
	if g.RetentionDays == 0 {
		retentionKey = ui.SortKeyInt(1 << 40)
	}
	if tier == ui.TierNarrow {
		return ui.Row{
			Key:   g.Name,
			Cells: table.Row{g.Name, retention},
			Sort:  table.Row{g.Name, retentionKey},
		}
	}
	created := logDate(g.Created)
	return ui.Row{
		Key:   g.Name,
		Cells: table.Row{g.Name, logClassText(g.Class), retention, aws.FormatBytes(g.StoredBytes), created},
		Sort:  table.Row{g.Name, logClassText(g.Class), retentionKey, ui.SortKeyBytes(g.StoredBytes), created},
	}
}

// logRetentionText shows a retention in days. 0 means that the events
// never expire.
func logRetentionText(days int) string {
	switch days {
	case 0:
		return "never"
	case 1:
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}

// logClassText shows a log class in words.
func logClassText(class string) string {
	switch class {
	case "STANDARD":
		return "Standard"
	case "INFREQUENT_ACCESS":
		return "Infrequent"
	case "DELIVERY":
		return "Delivery"
	}
	return class
}

// logDate shows the date of t, or "" for a zero time.
func logDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

// logTime shows the date and time of t, or "" for a zero time.
func logTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}

// logGroupDetail returns the FetchDetail of the group list. The Info tab
// comes from the row. The Tags tab needs ListTagsForResource, which many
// read-only roles do not allow, so a failure shows in the tab and the
// panel still opens.
func logGroupDetail(logs aws.LogsService) func(context.Context, aws.LogGroup) (msg.TabbedContentMsg, error) {
	return func(ctx context.Context, g aws.LogGroup) (msg.TabbedContentMsg, error) {
		if logs == nil {
			return msg.TabbedContentMsg{}, errNoClient
		}
		info, links := buildInfo(logGroupInfo(g))
		var tags string
		switch t, err := logs.ListLogGroupTags(ctx, g.ARN); {
		case err != nil:
			// ErrorText reads only the local AWS config files, so it
			// needs no request context.
			tags = "Tags failed: " + aws.ErrorText(err) //nolint:contextcheck // see above
		case len(t) == 0:
			tags = "  No tags"
		default:
			tags = buildTagsContent(t)
		}
		return msg.TabbedContentMsg{PanelTitle: g.Name, Tabs: []msg.TabContent{
			{Title: "Info", Content: info, Format: "text", Links: links},
			{Title: "Tags", Content: tags, Format: "text"},
		}}, nil
	}
}

func logGroupInfo(g aws.LogGroup) []infoField {
	kms := g.KMSKeyID
	if kms == "" {
		kms = "none (AWS owned key)"
	}
	return []infoField{
		{K: "Name", V: g.Name},
		{K: "ARN", V: g.ARN},
		{K: "Class", V: logClassText(g.Class)},
		{K: "Retention", V: logRetentionText(g.RetentionDays)},
		{K: "Stored", V: aws.FormatBytes(g.StoredBytes)},
		{K: "Created", V: logTime(g.Created)},
		{K: "Metric filters", V: fmt.Sprintf("%d", g.MetricFilters)},
		{K: "KMS key", V: kms},
		{K: "Data protection", V: strings.ToLower(g.DataProtection)},
		{K: "Delete protected", V: yesNo(g.DeletionProtection)},
	}
}
