package views

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// ─── Messages ───────────────────────────────────────────────────────────────

type sqsQueuesPageMsg struct {
	urls         []string
	hasMorePages bool
	token        *string
	pageNum      int
}

type sqsAllAttrsLoadedMsg struct {
	queues []aws.Queue
}

type sqsQueueDetailMsg struct {
	queue        *aws.Queue
	sourceQueues []string
	err          error
}

type sqsQueueRefreshedMsg struct {
	queue *aws.Queue
	err   error
}

type sqsQueuePurgedMsg struct {
	urls []string
	err  error
}

type sqsQueueDeletedMsg struct {
	url string
	err error
}

type sqsMessageSentMsg struct {
	err error
}

// ─── Key map ────────────────────────────────────────────────────────────────

type sqsQueuesKeyMap struct {
	Esc         key.Binding
	Details     key.Binding
	Describe    key.Binding
	Peek        key.Binding
	Manage      key.Binding
	CopyURL     key.Binding
	Sort        key.Binding
	SortReverse key.Binding
	Filter      key.Binding
	Refresh     key.Binding
	Select      key.Binding
}

var defaultSQSQueuesKeyMap = sqsQueuesKeyMap{
	Esc:         key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Details:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter/d", "details")),
	Describe:    key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "details")),
	Peek:        key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "peek messages")),
	Manage:      key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "manage")),
	CopyURL:     key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy URL")),
	Sort:        key.NewBinding(key.WithKeys("s"), key.WithHelp("s/S", "sort")),
	SortReverse: key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "reverse sort")),
	Filter:      key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
	Refresh:     key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
	Select:      key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "select")),
}

// ─── Model ──────────────────────────────────────────────────────────────────

// SQSQueues displays all SQS queues.
type SQSQueues struct {
	keys             sqsQueuesKeyMap
	sqs              aws.SQSService
	table            ui.Table
	queues           []aws.Queue
	queueURLs        []string // accumulated during progressive load
	filter           ui.Filter
	spinner          ui.Spinner
	loading          bool
	attrsLoading     bool
	pendingQueueURLs []string
	pendingAction    string
	err              error
	width            int
	height           int
	widthTier        ui.WidthTier
}

func (s *SQSQueues) ID() string    { return "sqs_queues" }
func (s *SQSQueues) Title() string { return "SQS Queues" }
func (s *SQSQueues) KeyMap() []ui.HintBinding {
	hints := []ui.HintBinding{
		{Binding: s.keys.Details},
		{Binding: s.keys.Peek},
		{Binding: s.keys.Manage, Mode: ui.ModeReadWrite},
		{Binding: s.keys.CopyURL},
		{Binding: s.keys.Select},
		{Binding: s.keys.Sort},
		{Binding: s.keys.Filter},
		{Binding: s.keys.Refresh},
	}
	ui.ApplyModeAll(hints)
	return hints
}

func sqsColumns(tier ui.WidthTier) []table.Column {
	if tier == ui.TierNarrow {
		return []table.Column{
			{Title: "Queue Name", Width: 30},
			{Title: "Messages", Width: 10},
		}
	}
	if tier == ui.TierMedium {
		return []table.Column{
			{Title: "Queue Name", Width: 30},
			{Title: "Type", Width: 10},
			{Title: "Messages", Width: 10},
			{Title: "In-Flight", Width: 10},
			{Title: "Delayed", Width: 10},
		}
	}
	return []table.Column{
		{Title: "Queue Name", Width: 30},
		{Title: "Type", Width: 10},
		{Title: "Messages", Width: 10},
		{Title: "In-Flight", Width: 10},
		{Title: "Delayed", Width: 10},
		{Title: "DLQ", Width: 5},
		{Title: "Created", Width: 12},
	}
}

// NewSQSQueues creates the SQS queue list view.
func NewSQSQueues(sqs aws.SQSService) *SQSQueues {
	t := ui.NewTable(sqsColumns(ui.TierMedium), nil)
	return &SQSQueues{
		keys:      defaultSQSQueuesKeyMap,
		sqs:       sqs,
		table:     t,
		filter:    ui.NewFilter(),
		spinner:   ui.NewSpinner("Loading SQS queues..."),
		loading:   true,
		widthTier: ui.TierMedium,
	}
}

func (s *SQSQueues) Init() tea.Cmd {
	s.table.DeselectAll()
	if !s.loading {
		return nil
	}
	return tea.Batch(s.spinner.Tick(), s.fetchQueuesPage(nil, 1))
}

// ─── Data fetching ──────────────────────────────────────────────────────────

func (s *SQSQueues) fetchQueuesPage(token *string, pageNum int) tea.Cmd {
	svc := s.sqs
	return func() tea.Msg {
		page, err := svc.ListQueuesPage(context.Background(), token)
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: "listing SQS queues"}
		}
		return sqsQueuesPageMsg{
			urls:         page.QueueURLs,
			hasMorePages: page.HasMorePages,
			token:        page.Token,
			pageNum:      pageNum,
		}
	}
}

func (s *SQSQueues) fetchAllAttributes(urls []string) tea.Cmd {
	svc := s.sqs
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Fetching attributes for %d SQS queues", len(urls))
		queues, err := svc.FetchAllQueueAttributes(context.Background(), urls)
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: "fetching SQS queue attributes"}
		}
		return sqsAllAttrsLoadedMsg{queues: queues}
	}
}

func (s *SQSQueues) refreshQueue(queueURL string) tea.Cmd {
	svc := s.sqs
	return func() tea.Msg {
		queue, err := svc.GetQueueAttributes(context.Background(), queueURL)
		return sqsQueueRefreshedMsg{queue: queue, err: err}
	}
}

func (s *SQSQueues) fetchQueueDetail(queueURL string) tea.Cmd {
	svc := s.sqs
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Fetching details for queue: %s", queueURL)
		queue, err := svc.GetQueueAttributes(context.Background(), queueURL)
		if err != nil {
			return sqsQueueDetailMsg{err: err}
		}
		sourceQueues, _ := svc.ListDeadLetterSourceQueues(context.Background(), queueURL)
		return sqsQueueDetailMsg{queue: queue, sourceQueues: sourceQueues}
	}
}

// ─── Update ─────────────────────────────────────────────────────────────────

func (s *SQSQueues) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case ui.PickerResultMsg:
		if m.ID == "sort" {
			if m.Value == "_clear" {
				s.table.ClearSort()
			} else if m.Selected >= 0 {
				s.table.Sort(m.Selected)
			}
		} else if m.ID == "action" && m.Selected >= 0 {
			urls := s.pendingQueueURLs
			switch m.Value {
			case "Send Message":
				q := s.selectedQueue()
				if q == nil {
					return s, nil
				}
				fields := []msg.FormField{
					{Key: "body", Type: "text", Title: "Message Body", Required: true,
						Placeholder: "Enter message body (JSON or plain text)"},
				}
				if q.FifoQueue {
					// FIFO queues require MessageGroupId and don't support per-message delay.
					fields = append(fields,
						msg.FormField{Key: "group_id", Type: "input", Title: "Message Group ID",
							Required: true, Placeholder: "Enter group ID",
							Description: "Required. Messages in the same group are processed in order.",
							Validate: huh.ValidateMaxLength(128)},
					)
					if !q.ContentBasedDeduplication {
						fields = append(fields,
							msg.FormField{Key: "dedup_id", Type: "input", Title: "Deduplication ID",
								Required: true, Placeholder: "Enter deduplication ID",
								Description: "Required (content-based dedup is disabled on this queue).",
								Validate: huh.ValidateMaxLength(128)},
						)
					} else {
						fields = append(fields,
							msg.FormField{Key: "dedup_id", Type: "input", Title: "Deduplication ID",
								Placeholder: "Leave empty for content-based dedup",
								Description: "Optional. Auto-generated from message body if empty.",
								Validate: huh.ValidateMaxLength(128)},
						)
					}
				} else {
					// Standard queues support per-message delay.
					fields = append(fields,
						msg.FormField{Key: "delay", Type: "input", Title: "Delay Seconds",
							Placeholder: "0", Description: "Optional. Seconds to delay delivery (0-900).",
							Validate: func(s string) error {
								if s == "" {
									return nil
								}
								v, err := strconv.Atoi(s)
								if err != nil {
									return fmt.Errorf("must be a number")
								}
								if v < 0 || v > 900 {
									return fmt.Errorf("must be between 0 and 900")
								}
								return nil
							}},
					)
				}
				s.pendingQueueURLs = []string{q.URL}
				return s, func() tea.Msg {
					return msg.RequestFormMsg{
						ID:     "sqs_send_message",
						Title:  "Send Message",
						Fields: fields,
					}
				}
			case "Purge Queue":
				s.pendingAction = "purge"
				count := len(urls)
				return s, func() tea.Msg {
					return msg.RequestConfirmMsg{
						Message: fmt.Sprintf("Purge %d queue(s)? This removes all messages and cannot be undone.", count),
						Action:  "sqs_purge",
					}
				}
			case "Delete Queue":
				s.pendingAction = "delete"
				count := len(urls)
				return s, func() tea.Msg {
					return msg.RequestConfirmMsg{
						Message: fmt.Sprintf("Delete %d queue(s)? All messages will be lost.", count),
						Action:  "sqs_delete",
					}
				}
			}
		}
		return s, nil

	case msg.FormResultMsg:
		if m.ID == "sqs_send_message" && !m.Aborted {
			if len(s.pendingQueueURLs) == 0 {
				return s, nil
			}
			queueURL := s.pendingQueueURLs[0]
			s.pendingQueueURLs = nil
			return s, s.sendMessage(queueURL, m.Values)
		}
		s.pendingQueueURLs = nil
		return s, nil

	case ui.ConfirmResultMsg:
		if !m.Confirmed {
			s.pendingQueueURLs = nil
			s.pendingAction = ""
			return s, nil
		}
		urls := s.pendingQueueURLs
		s.pendingQueueURLs = nil
		s.pendingAction = ""
		switch m.Action {
		case "sqs_purge":
			return s, s.purgeQueues(urls)
		case "sqs_delete":
			return s, s.deleteQueues(urls)
		}
		return s, nil

	case sqsQueuePurgedMsg:
		if m.err != nil {
			return s, func() tea.Msg {
				return msg.ToastError("Purge failed: " + m.err.Error())
			}
		}
		// Re-fetch attributes for just the purged queues.
		cmds := []tea.Cmd{func() tea.Msg {
			return msg.ToastSuccess("Queue purged (60s cooldown before next purge)")
		}}
		for _, url := range m.urls {
			cmds = append(cmds, s.refreshQueue(url))
		}
		return s, tea.Batch(cmds...)

	case sqsQueueRefreshedMsg:
		if m.err != nil || m.queue == nil {
			s.spinner.Hide()
			return s, nil
		}
		for i := range s.queues {
			if s.queues[i].URL == m.queue.URL {
				m.queue.IsDLQ = s.queues[i].IsDLQ // preserve derived field
				s.queues[i] = *m.queue
				break
			}
		}
		s.spinner.Hide()
		rows, sortKeys := s.buildRows(s.queues)
		s.table.SetRowsWithSortKeys(rows, sortKeys)
		return s, nil

	case sqsQueueDeletedMsg:
		if m.err != nil {
			return s, func() tea.Msg {
				return msg.ToastError("Delete failed: " + m.err.Error())
			}
		}
		// Remove deleted queue from local state and refresh
		s.removeQueue(m.url)
		return s, func() tea.Msg {
			return msg.ToastSuccess("Queue deleted")
		}

	case sqsMessageSentMsg:
		if m.err != nil {
			eventlog.Errorf(eventlog.CatAWS, "SendMessage failed: %v", m.err)
			return s, func() tea.Msg {
				return msg.ToastError("Send failed: " + m.err.Error())
			}
		}
		return s, func() tea.Msg {
			return msg.ToastSuccess("Message sent")
		}

	case sqsQueuesPageMsg:
		s.queueURLs = append(s.queueURLs, m.urls...)

		if m.hasMorePages {
			s.spinner.Show(fmt.Sprintf("Loading queues... %d so far", len(s.queueURLs)))
			return s, tea.Batch(s.spinner.Tick(), s.fetchQueuesPage(m.token, m.pageNum+1))
		}

		// All URLs collected — now fetch attributes in parallel
		if len(s.queueURLs) == 0 {
			s.loading = false
			s.spinner.Hide()
			eventlog.Infof(eventlog.CatAWS, "No SQS queues found")
			return s, nil
		}

		s.attrsLoading = true
		s.spinner.Show(fmt.Sprintf("Loading attributes for %d queues...", len(s.queueURLs)))
		return s, tea.Batch(s.spinner.Tick(), s.fetchAllAttributes(s.queueURLs))

	case sqsAllAttrsLoadedMsg:
		s.loading = false
		s.attrsLoading = false
		s.spinner.Hide()
		s.queues = m.queues
		markDLQQueues(s.queues)
		rows, sortKeys := s.buildRows(s.queues)
		s.table.SetRowsWithSortKeys(rows, sortKeys)
		eventlog.Infof(eventlog.CatAWS, "Loaded %d SQS queues", len(s.queues))
		return s, nil

	case sqsQueueDetailMsg:
		if m.err != nil {
			s.err = m.err
			return s, nil
		}
		if m.queue == nil {
			return s, nil
		}
		q := m.queue
		title := q.Name
		tabs := buildSQSDetailTabs(q, m.sourceQueues)
		return s, func() tea.Msg {
			return msg.TabbedContentMsg{PanelTitle: title, Tabs: tabs}
		}

	case msg.ErrorMsg:
		s.loading = false
		s.attrsLoading = false
		s.spinner.Hide()
		s.err = m.Err
		return s, nil

	case tea.WindowSizeMsg:
		s.width = m.Width
		s.height = m.Height
		newTier := ui.GetWidthTier(m.Width)
		s.widthTier = newTier

		cols := sqsColumns(newTier)
		if !ui.ColumnsFit(cols, m.Width) {
			cols = sqsColumns(ui.TierNarrow)
			s.widthTier = ui.TierNarrow
		}
		if len(cols) != len(s.table.Columns()) {
			s.table.SetColumns(cols)
			if len(s.queues) > 0 {
				rows, sortKeys := s.buildRows(s.queues)
				s.table.SetRowsWithSortKeys(rows, sortKeys)
			}
		}
		s.table.SetSize(m.Width, m.Height-3)
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

		switch {
		case key.Matches(m, s.keys.Esc):
			if s.table.SelectionCount() > 0 {
				s.table.DeselectAll()
				return s, nil
			}
			return s, func() tea.Msg { return msg.NavigateBackMsg{} }
		case key.Matches(m, s.keys.Sort):
			columns, currentCol := s.table.SortColumnNames()
			return s, func() tea.Msg {
				return msg.RequestSortPickerMsg{Columns: columns, CurrentCol: currentCol}
			}
		case key.Matches(m, s.keys.SortReverse):
			s.table.SortReverse()
			return s, nil
		case key.Matches(m, s.keys.Filter):
			s.filter.Activate()
			return s, nil
		case key.Matches(m, s.keys.Refresh):
			if urls := s.selectedQueueURLs(); len(urls) > 0 && s.table.SelectionCount() > 0 {
				eventlog.Infof(eventlog.CatAWS, "Selective refresh: %d SQS queues", len(urls))
				s.spinner.Show(fmt.Sprintf("Refreshing %d queues...", len(urls)))
				cmds := []tea.Cmd{s.spinner.Tick()}
				for _, url := range urls {
					cmds = append(cmds, s.refreshQueue(url))
				}
				return s, tea.Batch(cmds...)
			}
			s.loading = true
			s.err = nil
			s.queues = nil
			s.queueURLs = nil
			s.spinner.Show("Loading SQS queues...")
			return s, tea.Batch(s.spinner.Tick(), s.fetchQueuesPage(nil, 1))
		case key.Matches(m, s.keys.Details, s.keys.Describe):
			q := s.selectedQueue()
			if q != nil {
				return s, s.fetchQueueDetail(q.URL)
			}
		case key.Matches(m, s.keys.Peek):
			q := s.selectedQueue()
			if q != nil {
				queueURL := q.URL
				queueName := q.Name
				fifo := fmt.Sprintf("%t", q.FifoQueue)
				return s, func() tea.Msg {
					return msg.NavigateMsg{
						ViewID: "sqs_messages",
						Params: map[string]string{
							"queue_url":  queueURL,
							"queue_name": queueName,
							"fifo":       fifo,
						},
					}
				}
			}
		case key.Matches(m, s.keys.CopyURL):
			urls := s.selectedQueueURLs()
			if len(urls) == 0 {
				return s, nil
			}
			text := strings.Join(urls, "\n")
			return s, tea.Batch(
				tea.SetClipboard(text),
				func() tea.Msg {
					return msg.ToastSuccess(fmt.Sprintf("Copied %d queue URL(s)", len(urls)))
				},
			)
		case key.Matches(m, s.keys.Select):
			s.table.ToggleSelect()
			return s, nil
		case key.Matches(m, s.keys.Manage):
			if ui.ReadOnly {
				return s, func() tea.Msg {
					return msg.ToastError("ReadOnly mode — press W to switch")
				}
			}
			urls := s.selectedQueueURLs()
			if len(urls) == 0 {
				return s, nil
			}
			s.pendingQueueURLs = urls
			title := "Manage Queue"
			if len(urls) > 1 {
				title = fmt.Sprintf("Manage %d Queues", len(urls))
			}
			actions := []string{"Send Message", "Purge Queue", "Delete Queue"}
			return s, func() tea.Msg {
				return msg.RequestActionPickerMsg{
					Title:   title,
					Options: actions,
				}
			}
		}
	}

	if s.loading && len(s.queues) == 0 {
		var cmd tea.Cmd
		s.spinner, cmd = s.spinner.Update(m)
		return s, cmd
	}

	var cmds []tea.Cmd
	if s.loading || s.attrsLoading {
		var cmd tea.Cmd
		s.spinner, cmd = s.spinner.Update(m)
		cmds = append(cmds, cmd)
	}
	var cmd tea.Cmd
	s.table, cmd = s.table.Update(m)
	cmds = append(cmds, cmd)
	return s, tea.Batch(cmds...)
}

// ─── Selection helpers ──────────────────────────────────────────────────────

func (s *SQSQueues) selectedQueue() *aws.Queue {
	row := s.table.SelectedRow()
	if row == nil {
		return nil
	}
	name := row[0]
	for i := range s.queues {
		if s.queues[i].Name == name {
			return &s.queues[i]
		}
	}
	return nil
}

func (s *SQSQueues) selectedQueueURLs() []string {
	indices := s.table.SelectedIndices()
	if len(indices) == 0 {
		row := s.table.SelectedRow()
		if row == nil {
			return nil
		}
		for _, q := range s.queues {
			if q.Name == row[0] {
				return []string{q.URL}
			}
		}
		return nil
	}
	urls := make([]string, 0, len(indices))
	for _, idx := range indices {
		if idx < len(s.queues) {
			urls = append(urls, s.queues[idx].URL)
		}
	}
	return urls
}

func (s *SQSQueues) removeQueue(url string) {
	for i, q := range s.queues {
		if q.URL == url {
			s.queues = append(s.queues[:i], s.queues[i+1:]...)
			break
		}
	}
	rows, sortKeys := s.buildRows(s.queues)
	s.table.SetRowsWithSortKeys(rows, sortKeys)
}

// ─── Mutations ──────────────────────────────────────────────────────────────

func (s *SQSQueues) purgeQueues(urls []string) tea.Cmd {
	svc := s.sqs
	return func() tea.Msg {
		for _, url := range urls {
			eventlog.Infof(eventlog.CatAWS, "Purging queue: %s", url)
			if err := svc.PurgeQueue(context.Background(), url); err != nil {
				return sqsQueuePurgedMsg{urls: urls, err: err}
			}
		}
		return sqsQueuePurgedMsg{urls: urls}
	}
}

func (s *SQSQueues) deleteQueues(urls []string) tea.Cmd {
	svc := s.sqs
	return func() tea.Msg {
		for _, url := range urls {
			eventlog.Infof(eventlog.CatAWS, "Deleting queue: %s", url)
			if err := svc.DeleteQueue(context.Background(), url); err != nil {
				return sqsQueueDeletedMsg{url: url, err: err}
			}
		}
		return sqsQueueDeletedMsg{url: urls[0]}
	}
}

func (s *SQSQueues) sendMessage(queueURL string, values map[string]string) tea.Cmd {
	svc := s.sqs
	body := values["body"]
	delay := int32(0)
	if d, ok := values["delay"]; ok && d != "" {
		if v, err := strconv.Atoi(d); err == nil && v > 0 && v <= 900 {
			delay = int32(v) //nolint:gosec // bounded to 0-900
		}
	}
	groupID := values["group_id"]
	dedupID := values["dedup_id"]
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Sending message to %s", queueURL)
		err := svc.SendMessage(context.Background(), queueURL, body, delay, groupID, dedupID)
		return sqsMessageSentMsg{err: err}
	}
}

// markDLQQueues cross-references redrive policies to mark queues that are
// used as dead-letter queues by other queues in the list.
func markDLQQueues(queues []aws.Queue) {
	// Collect all ARNs that are DLQ targets.
	dlqARNs := make(map[string]bool)
	for _, q := range queues {
		if q.RedrivePolicy != nil {
			dlqARNs[q.RedrivePolicy.DeadLetterTargetArn] = true
		}
	}
	// Mark queues whose ARN appears as a DLQ target.
	for i := range queues {
		if dlqARNs[queues[i].ARN] || queues[i].RedriveAllowPolicy != nil {
			queues[i].IsDLQ = true
		}
	}
}

// ─── Row building ───────────────────────────────────────────────────────────

func (s *SQSQueues) buildRows(queues []aws.Queue) ([]table.Row, []table.Row) {
	rows := make([]table.Row, 0, len(queues))
	sortKeys := make([]table.Row, 0, len(queues))
	for _, q := range queues {
		msgCount := fmt.Sprintf("%d", q.ApproximateMessageCount)
		inFlight := fmt.Sprintf("%d", q.ApproximateInFlightCount)
		delayed := fmt.Sprintf("%d", q.ApproximateDelayedCount)

		created := ""
		if !q.CreatedTimestamp.IsZero() {
			created = q.CreatedTimestamp.Format("2006-01-02")
		}

		dlq := ""
		if q.IsDLQ {
			dlq = "Yes"
		}

		switch s.widthTier {
		case ui.TierNarrow:
			rows = append(rows, table.Row{q.Name, msgCount})
			sortKeys = append(sortKeys, table.Row{q.Name, msgCount})
		case ui.TierMedium:
			rows = append(rows, table.Row{q.Name, q.Type, msgCount, inFlight, delayed})
			sortKeys = append(sortKeys, table.Row{q.Name, q.Type, msgCount, inFlight, delayed})
		default:
			rows = append(rows, table.Row{q.Name, q.Type, msgCount, inFlight, delayed, dlq, created})
			sortKeys = append(sortKeys, table.Row{q.Name, q.Type, msgCount, inFlight, delayed, dlq, created})
		}
	}
	return rows, sortKeys
}

// ─── Detail tabs ────────────────────────────────────────────────────────────

func buildSQSDetailTabs(q *aws.Queue, sourceQueues []string) []msg.TabContent {
	msgContent, msgLinks := buildSQSMessageCountsContent(q)
	tabs := []msg.TabContent{
		{Title: "Info", Content: buildSQSInfoContent(q), Format: "text"},
		{Title: "Configuration", Content: buildSQSConfigContent(q), Format: "text"},
		{Title: "Messages", Content: msgContent, Format: "text", Links: msgLinks},
	}

	dlqContent, dlqLinks := buildSQSDLQContent(q, sourceQueues)
	tabs = append(tabs, msg.TabContent{
		Title: "DLQ Config", Content: dlqContent, Format: "text", Links: dlqLinks,
	})

	tabs = append(tabs, msg.TabContent{
		Title: "Encryption", Content: buildSQSEncryptionContent(q), Format: "text",
	})

	if q.FifoQueue {
		tabs = append(tabs, msg.TabContent{
			Title: "FIFO Settings", Content: buildSQSFIFOContent(q), Format: "text",
		})
	}

	return tabs
}

func buildSQSInfoContent(q *aws.Queue) string {
	var b strings.Builder
	writeField(&b, "Name", q.Name)
	writeField(&b, "ARN", q.ARN)
	writeField(&b, "URL", q.URL)
	writeField(&b, "Type", q.Type)
	if !q.CreatedTimestamp.IsZero() {
		writeField(&b, "Created", q.CreatedTimestamp.Format("2006-01-02 15:04:05"))
	}
	if !q.LastModifiedTimestamp.IsZero() {
		writeField(&b, "Last Modified", q.LastModifiedTimestamp.Format("2006-01-02 15:04:05"))
	}
	return b.String()
}

func buildSQSConfigContent(q *aws.Queue) string {
	var b strings.Builder
	writeField(&b, "Visibility Timeout", fmt.Sprintf("%ds", q.VisibilityTimeout))
	writeField(&b, "Delay Seconds", fmt.Sprintf("%ds", q.DelaySeconds))
	writeField(&b, "Max Message Size", formatBytes(q.MaximumMessageSize))
	writeField(&b, "Retention Period", formatDuration(q.MessageRetentionPeriod))
	writeField(&b, "Receive Wait Time", fmt.Sprintf("%ds", q.ReceiveMessageWaitTime))
	return b.String()
}

func buildSQSMessageCountsContent(q *aws.Queue) (string, []msg.TabLink) {
	var b strings.Builder
	writeField(&b, "Approximate Messages", fmt.Sprintf("%d", q.ApproximateMessageCount))
	writeField(&b, "In-Flight", fmt.Sprintf("%d", q.ApproximateInFlightCount))
	writeField(&b, "Delayed", fmt.Sprintf("%d", q.ApproximateDelayedCount))
	links := []msg.TabLink{{
		Line:   0, // "Approximate Messages" line
		ViewID: "sqs_messages",
		Params: map[string]string{
			"queue_url":  q.URL,
			"queue_name": q.Name,
			"fifo":       fmt.Sprintf("%t", q.FifoQueue),
		},
	}}
	return b.String(), links
}

func buildSQSDLQContent(q *aws.Queue, sourceQueues []string) (string, []msg.TabLink) {
	var b strings.Builder
	var links []msg.TabLink
	lineIdx := 0

	if q.RedrivePolicy != nil {
		writeField(&b, "Target DLQ ARN", q.RedrivePolicy.DeadLetterTargetArn)
		lineIdx++
		writeField(&b, "Max Receive Count", fmt.Sprintf("%d", q.RedrivePolicy.MaxReceiveCount))
		lineIdx++
		// Pretty-print the raw JSON
		rp, _ := json.MarshalIndent(q.RedrivePolicy, "", "  ")
		fmt.Fprintf(&b, "\nRedrive Policy:\n%s\n", string(rp))
		lineIdx += strings.Count(string(rp), "\n") + 3
	} else {
		writeField(&b, "Redrive Policy", "None")
		lineIdx++
	}

	if q.RedriveAllowPolicy != nil {
		rap, _ := json.MarshalIndent(q.RedriveAllowPolicy, "", "  ")
		fmt.Fprintf(&b, "\nRedrive Allow Policy:\n%s\n", string(rap))
		lineIdx += strings.Count(string(rap), "\n") + 3
	}

	if len(sourceQueues) > 0 {
		fmt.Fprintf(&b, "\nDLQ Source Queues:\n")
		lineIdx += 2
		for _, srcURL := range sourceQueues {
			name := aws.QueueNameFromURL(srcURL)
			fmt.Fprintf(&b, "  %s\n", name)
			links = append(links, msg.TabLink{
				Line:   lineIdx,
				ViewID: "sqs_queues",
				Params: map[string]string{"focus": name},
			})
			lineIdx++
		}
	}

	return b.String(), links
}

func buildSQSEncryptionContent(q *aws.Queue) string {
	var b strings.Builder
	if q.SqsManagedSseEnabled {
		writeField(&b, "SSE", "SQS-managed (SSE-SQS)")
	} else if q.KmsMasterKeyID != "" {
		writeField(&b, "SSE", "KMS")
		writeField(&b, "KMS Key ID", q.KmsMasterKeyID)
		writeField(&b, "KMS Reuse Period", fmt.Sprintf("%ds", q.KmsDataKeyReusePeriod))
	} else {
		writeField(&b, "SSE", "Disabled")
	}
	return b.String()
}

func buildSQSFIFOContent(q *aws.Queue) string {
	var b strings.Builder
	writeField(&b, "Content-Based Dedup", fmt.Sprintf("%t", q.ContentBasedDeduplication))
	if q.DeduplicationScope != "" {
		writeField(&b, "Dedup Scope", q.DeduplicationScope)
	}
	if q.FifoThroughputLimit != "" {
		writeField(&b, "Throughput Limit", q.FifoThroughputLimit)
	}
	return b.String()
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func writeField(b *strings.Builder, key, value string) {
	fmt.Fprintf(b, "%-22s %s\n", key, value)
}

func formatBytes(bytes int) string {
	if bytes >= 1024*1024 {
		return fmt.Sprintf("%d MB", bytes/(1024*1024))
	}
	if bytes >= 1024 {
		return fmt.Sprintf("%d KB", bytes/1024)
	}
	return fmt.Sprintf("%d bytes", bytes)
}

func formatDuration(seconds int) string {
	if seconds >= 86400 {
		return fmt.Sprintf("%d days", seconds/86400)
	}
	if seconds >= 3600 {
		return fmt.Sprintf("%d hours", seconds/3600)
	}
	if seconds >= 60 {
		return fmt.Sprintf("%d minutes", seconds/60)
	}
	return fmt.Sprintf("%d seconds", seconds)
}

// ─── Footer & View ──────────────────────────────────────────────────────────

func (s *SQSQueues) Footer() string {
	filtered, total := s.table.RowCount()
	footer := fmt.Sprintf("%d/%d queues", filtered, total)
	if s.loading && total > 0 {
		footer += fmt.Sprintf("  (loading... %d so far)", total)
	}
	if sel := s.table.SelectionCount(); sel > 0 {
		footer += fmt.Sprintf("  (%d selected)", sel)
	}
	if s.spinner.Visible() {
		footer += "  " + s.spinner.View()
	}
	return footer
}

func (s *SQSQueues) View() tea.View {
	var content string
	if s.loading && len(s.queues) == 0 {
		content = "\n  " + s.spinner.View()
	} else if s.err != nil {
		content = "\n  " + ui.ErrorStyle.Render("Error: "+s.err.Error())
	} else {
		content = s.table.View()
		if s.filter.Active() {
			content = s.filter.View() + "\n" + content
		}
	}
	return tea.NewView(content)
}
