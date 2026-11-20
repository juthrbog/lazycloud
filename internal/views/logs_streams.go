package views

import (
	"context"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// logStreamLagNote says that AWS updates the last event time late.
const logStreamLagNote = "AWS updates this time late, usually within an hour"

// LogStreams displays the streams of one log group, the stream with the
// newest event first.
type LogStreams struct {
	listCore[aws.LogStream]
	group string
}

// NewLogStreams creates the stream list of group.
func NewLogStreams(logs aws.LogsService, group string) *LogStreams {
	return &LogStreams{group: group, listCore: newListCore(listConfig[aws.LogStream]{
		Noun:         "log streams",
		LoadingLabel: "Loading log streams...",
		ErrContext:   "listing log streams",
		Key:          func(s aws.LogStream) string { return s.Name },
		Columns:      logStreamColumns,
		Row:          logStreamRow,
		Fetch: fetchPages(logs, func(l aws.LogsService, ctx context.Context, tok *string) (*aws.LogStreamPage, error) {
			return l.ListLogStreamsPage(ctx, group, tok)
		}, splitLogStreamPage),
		FetchTimeout: callTimeout,
		OpenDetail:   logStreamDetail,
		PageBudget:   logStreamPageBudget,
		MoreKey:      true,
		Copy:         true,
	})}
}

func (s *LogStreams) ID() string     { return "logs_streams:" + s.group }
func (s *LogStreams) Title() string  { return s.group }
func (s *LogStreams) Init() tea.Cmd  { return s.init() }
func (s *LogStreams) View() tea.View { return tea.NewView(s.render("", "")) }

// Footer adds a note while more streams exist: AWS takes no name prefix
// with the order by the last event, so the filter sees only the streams
// that loaded.
func (s *LogStreams) Footer() string {
	f := s.footer()
	if s.more != nil {
		f += " · filter acts on the loaded streams"
	}
	return f
}

func (s *LogStreams) KeyMap() []ui.HintBinding {
	return s.hints(listKeys.Details, logsKeys.CopyName, listKeys.More,
		listKeys.Sort, listKeys.Filter, listKeys.Refresh)
}

// Update returns the view itself, so the navigator keeps its identity.
func (s *LogStreams) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	return s, s.update(m)
}

func splitLogStreamPage(p *aws.LogStreamPage) ([]aws.LogStream, *string) {
	if p == nil {
		return nil, nil
	}
	return p.Streams, p.Next
}

func logStreamColumns(tier ui.WidthTier) []ui.Column {
	if tier == ui.TierNarrow {
		// The name starts small, so the whole time fits down to 43
		// columns. Its weight gives it the rest of a wider window.
		return []ui.Column{
			{Title: "Name", Width: 20, Weight: 3, MaxWidth: 120},
			{Title: "Last event", Width: 19},
		}
	}
	return []ui.Column{
		{Title: "Name", Width: 30, Weight: 3, MaxWidth: 120},
		{Title: "Last event", Width: 19},
		{Title: "First event", Width: 19},
		{Title: "Created", Width: 19},
	}
}

// logStreamRow builds the row of one stream. The times sort as text, and
// their form sorts in time order.
func logStreamRow(s aws.LogStream, tier ui.WidthTier) ui.Row {
	last := logTime(s.LastEvent)
	if tier == ui.TierNarrow {
		return ui.Row{Key: s.Name, Cells: table.Row{s.Name, last}}
	}
	return ui.Row{Key: s.Name, Cells: table.Row{s.Name, last, logTime(s.FirstEvent), logTime(s.Created)}}
}

// logStreamDetail opens the detail panel of a stream.
func logStreamDetail(s aws.LogStream) tea.Cmd {
	last := logTime(s.LastEvent)
	if last != "" {
		last += " (" + logStreamLagNote + ")"
	}
	info, links := buildInfo([]infoField{
		{K: "Name", V: s.Name},
		{K: "ARN", V: s.ARN},
		{K: "Created", V: logTime(s.Created)},
		{K: "First event", V: logTime(s.FirstEvent)},
		{K: "Last event", V: last},
		{K: "Last ingestion", V: logTime(s.LastIngestion)},
	})
	return func() tea.Msg {
		return msg.TabbedContentMsg{PanelTitle: s.Name, Tabs: []msg.TabContent{
			{Title: "Info", Content: info, Format: "text", Links: links},
		}}
	}
}
