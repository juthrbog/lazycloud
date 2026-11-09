package views

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
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
	gen          int // load generation that requested this page
	urls         []string
	hasMorePages bool
	token        *string
	pageNum      int
}

type sqsAllAttrsLoadedMsg struct {
	gen    int // load generation that requested these attributes
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

// sqsQueuePurgedMsg reports a purge of several queues. Each queue is tried,
// also after an earlier one fails.
type sqsQueuePurgedMsg struct {
	done   []string // URLs of the purged queues
	failed []itemFailure
}

// Outcome implements msg.Outcome.
func (m sqsQueuePurgedMsg) Outcome() msg.ToastMsg {
	text, ok := resultText("Purged", len(m.done)+len(m.failed), "queue(s)", m.failed)
	if ok {
		return msg.ToastSuccess(text + " (60s cooldown before the next purge)")
	}
	return msg.ToastError(text)
}

// sqsQueueDeletedMsg reports a delete of several queues. Each queue is
// tried, also after an earlier one fails.
type sqsQueueDeletedMsg struct {
	done   []string // URLs of the deleted queues
	failed []itemFailure
}

// Outcome implements msg.Outcome.
func (m sqsQueueDeletedMsg) Outcome() msg.ToastMsg {
	text, ok := resultText("Deleted", len(m.done)+len(m.failed), "queue(s)", m.failed)
	if ok {
		return msg.ToastSuccess(text)
	}
	return msg.ToastError(text)
}

type sqsMessageSentMsg struct {
	err error
}

// Outcome implements msg.Outcome.
func (m sqsMessageSentMsg) Outcome() msg.ToastMsg {
	if m.err != nil {
		return msg.ToastError("Send failed: " + aws.ErrorText(m.err))
	}
	return msg.ToastSuccess("Message sent")
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
	viewCtx
	keys         sqsQueuesKeyMap
	sqs          aws.SQSService
	table        ui.Table
	queues       []aws.Queue
	queueURLs    []string // accumulated during progressive load
	filter       ui.Filter
	spinner      ui.Spinner
	loading      bool
	loadGen      int // generation of the current full load; see startLoad
	attrsLoading bool
	pending      pendingConfirm // targets of Purge and Delete
	sendTarget   string         // queue URL of the open Send Message form
	running      int            // purges and deletes that have not replied
	pendingFocus string         // queue name to focus when the load finishes
	err          error
	width        int
	height       int
	widthTier    ui.WidthTier
}

func (s *SQSQueues) ID() string    { return "sqs_queues" }
func (s *SQSQueues) Title() string { return "SQS Queues" }

// CapturingInput implements nav.InputCapturer.
func (s *SQSQueues) CapturingInput() bool {
	return s.filter.Active()
}

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

func sqsColumns(tier ui.WidthTier) []ui.Column {
	if tier == ui.TierNarrow {
		return []ui.Column{
			{Title: "Queue Name", Width: 30, Weight: 2, MaxWidth: 60},
			{Title: "Messages", Width: 10, Style: ui.CountColor},
		}
	}
	if tier == ui.TierMedium {
		return []ui.Column{
			{Title: "Queue Name", Width: 30, Weight: 2, MaxWidth: 60},
			{Title: "Type", Width: 10},
			{Title: "Messages", Width: 10, Style: ui.CountColor},
			{Title: "In-Flight", Width: 10, Style: ui.CountColor},
			{Title: "Delayed", Width: 10, Style: ui.CountColor},
		}
	}
	return []ui.Column{
		{Title: "Queue Name", Width: 30, Weight: 2, MaxWidth: 60},
		{Title: "Type", Width: 10},
		{Title: "Messages", Width: 10, Style: ui.CountColor},
		{Title: "In-Flight", Width: 10, Style: ui.CountColor},
		{Title: "Delayed", Width: 10, Style: ui.CountColor},
		{Title: "DLQ", Width: 5},
		{Title: "Created", Width: 12, Weight: 1, MaxWidth: 25},
	}
}

// NewSQSQueues creates the SQS queue list view.
func NewSQSQueues(sqs aws.SQSService) *SQSQueues {
	t := ui.NewTable(sqsColumns(ui.TierMedium))
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
	return s.startLoad()
}

// startLoad begins a full load: queue URL pages, then all attributes.
// Results from an older load are dropped, so a refresh during a load
// cannot duplicate rows.
func (s *SQSQueues) startLoad() tea.Cmd {
	s.loadGen++
	s.queues = nil
	s.queueURLs = nil
	return tea.Batch(s.spinner.Tick(), s.fetchQueuesPage(nil, 1))
}

// ─── Data fetching ──────────────────────────────────────────────────────────

func (s *SQSQueues) fetchQueuesPage(token *string, pageNum int) tea.Cmd {
	svc := s.sqs
	gen := s.loadGen
	ctx := s.viewContext()
	return func() tea.Msg {
		if svc == nil {
			return msg.ErrorMsg{Err: errNoClient, Context: "SQS"}
		}
		eventlog.Debugf(eventlog.CatAWS, "Fetching SQS queues (page %d)", pageNum)
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		page, err := svc.ListQueuesPage(ctx, token)
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: "listing SQS queues"}
		}
		return sqsQueuesPageMsg{
			gen:          gen,
			urls:         page.QueueURLs,
			hasMorePages: page.HasMorePages,
			token:        page.Token,
			pageNum:      pageNum,
		}
	}
}

func (s *SQSQueues) fetchAllAttributes(urls []string) tea.Cmd {
	svc := s.sqs
	gen := s.loadGen
	ctx := s.viewContext()
	return func() tea.Msg {
		if svc == nil {
			return msg.ErrorMsg{Err: errNoClient, Context: "SQS"}
		}
		eventlog.Infof(eventlog.CatAWS, "Fetching attributes for %d SQS queues", len(urls))
		ctx, cancel := context.WithTimeout(ctx, listTimeout)
		defer cancel()
		queues, err := svc.FetchAllQueueAttributes(ctx, urls)
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: "fetching SQS queue attributes"}
		}
		return sqsAllAttrsLoadedMsg{gen: gen, queues: queues}
	}
}

func (s *SQSQueues) refreshQueue(queueURL string) tea.Cmd {
	svc := s.sqs
	ctx := s.viewContext()
	return func() tea.Msg {
		if svc == nil {
			return sqsQueueRefreshedMsg{err: errNoClient}
		}
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		queue, err := svc.GetQueueAttributes(ctx, queueURL)
		return sqsQueueRefreshedMsg{queue: queue, err: err}
	}
}

func (s *SQSQueues) fetchQueueDetail(queueURL string) tea.Cmd {
	svc := s.sqs
	ctx := s.viewContext()
	return func() tea.Msg {
		if svc == nil {
			return sqsQueueDetailMsg{err: errNoClient}
		}
		eventlog.Infof(eventlog.CatAWS, "Fetching details for queue: %s", queueURL)
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		queue, err := svc.GetQueueAttributes(ctx, queueURL)
		if err != nil {
			return sqsQueueDetailMsg{err: err}
		}
		sourceQueues, _ := svc.ListDeadLetterSourceQueues(ctx, queueURL)
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
			urls := s.pending.keys
			switch m.Value {
			case "Send Message":
				// Send acts on the same queue as Purge and Delete: the
				// selection, or the cursor queue. The menu offers it only
				// for one queue.
				if len(urls) != 1 {
					return s, func() tea.Msg {
						return msg.ToastError("Select one queue to send a message")
					}
				}
				q := s.findQueue(urls[0])
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
							Validate:    huh.ValidateMaxLength(128)},
					)
					if !q.ContentBasedDeduplication {
						fields = append(fields,
							msg.FormField{Key: "dedup_id", Type: "input", Title: "Deduplication ID",
								Required: true, Placeholder: "Enter deduplication ID",
								Description: "Required (content-based dedup is disabled on this queue).",
								Validate:    huh.ValidateMaxLength(128)},
						)
					} else {
						fields = append(fields,
							msg.FormField{Key: "dedup_id", Type: "input", Title: "Deduplication ID",
								Placeholder: "Leave empty for content-based dedup",
								Description: "Optional. Auto-generated from message body if empty.",
								Validate:    huh.ValidateMaxLength(128)},
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
				s.sendTarget = q.URL
				s.pending.clear()
				return s, func() tea.Msg {
					return msg.RequestFormMsg{
						ID:     "sqs_send_message",
						Title:  "Send Message",
						Fields: fields,
					}
				}
			case "Purge Queue":
				return s, s.pending.ask(msg.RequestConfirmMsg{
					Message: confirmMessage("Purge", len(urls), "queue(s)", s.queueNameList(urls),
						"This removes all messages and cannot be undone."),
					Action: "sqs_purge",
				})
			case "Delete Queue":
				return s, s.pending.ask(msg.RequestConfirmMsg{
					Message: confirmMessage("Delete", len(urls), "queue(s)", s.queueNameList(urls),
						"All messages will be lost."),
					Action: "sqs_delete",
				})
			}
		}
		return s, nil

	case msg.FormResultMsg:
		// Every form reply clears the target, also an aborted form.
		queueURL := s.sendTarget
		s.sendTarget = ""
		if m.ID == "sqs_send_message" && !m.Aborted && queueURL != "" {
			return s, s.sendMessage(queueURL, m.Values)
		}
		return s, nil

	case ui.ConfirmResultMsg:
		urls, ok := s.pending.take(m)
		if !ok {
			return s, nil
		}
		if ui.IsReadOnly() {
			// The mode changed after the picker opened.
			return s, func() tea.Msg { return msg.ToastError(sqsReadOnlyText) }
		}
		switch m.Action {
		case "sqs_purge":
			return s, s.startAction(fmt.Sprintf("Purging %d queue(s)...", len(urls)), s.purgeQueues(urls))
		case "sqs_delete":
			return s, s.startAction(fmt.Sprintf("Deleting %d queue(s)...", len(urls)), s.deleteQueues(urls))
		}
		return s, nil

	case sqsQueuePurgedMsg:
		s.finishAction()
		for _, f := range m.failed {
			eventlog.Errorf(eventlog.CatAWS, "Purge of %s failed: %v", f.name, f.err)
		}
		eventlog.Infof(eventlog.CatAWS, "Purged %d queue(s)", len(m.done))
		toast := m.Outcome()
		cmds := []tea.Cmd{func() tea.Msg { return toast }}
		// Re-fetch attributes for just the purged queues.
		for _, url := range m.done {
			cmds = append(cmds, s.refreshQueue(url))
		}
		return s, tea.Batch(cmds...)

	case sqsQueueRefreshedMsg:
		if m.err != nil || m.queue == nil {
			if m.err != nil {
				eventlog.Errorf(eventlog.CatAWS, "Queue refresh failed: %v", m.err)
			}
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
		s.table.SetRows(s.buildRows(s.queues))
		return s, nil

	case sqsQueueDeletedMsg:
		s.finishAction()
		for _, f := range m.failed {
			eventlog.Errorf(eventlog.CatAWS, "Delete of queue %s failed: %v", f.name, f.err)
		}
		for _, url := range m.done {
			eventlog.Infof(eventlog.CatAWS, "Deleted queue: %s", url)
		}
		// Remove only the deleted queues. The failed ones stay in the list.
		s.removeQueues(m.done)
		s.table.DeselectAll()
		toast := m.Outcome()
		return s, func() tea.Msg { return toast }

	case sqsMessageSentMsg:
		if m.err != nil {
			eventlog.Errorf(eventlog.CatAWS, "SendMessage failed: %v", m.err)
		} else {
			eventlog.Infof(eventlog.CatAWS, "Message sent")
		}
		toast := m.Outcome()
		return s, func() tea.Msg { return toast }

	case sqsQueuesPageMsg:
		if m.gen != s.loadGen {
			return s, nil // page from a superseded load
		}
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
		if m.gen != s.loadGen {
			return s, nil // attributes from a superseded load
		}
		s.loading = false
		s.attrsLoading = false
		s.spinner.Hide()
		s.queues = m.queues
		markDLQQueues(s.queues)
		s.table.SetRows(s.buildRows(s.queues))
		eventlog.Infof(eventlog.CatAWS, "Loaded %d SQS queues", len(s.queues))
		var cmds []tea.Cmd
		var unread []itemFailure
		for _, q := range s.queues {
			if q.Err != nil {
				eventlog.Errorf(eventlog.CatAWS, "Could not read queue %s: %v", q.Name, q.Err)
				unread = append(unread, itemFailure{name: q.Name, err: q.Err})
			}
		}
		if len(unread) > 0 {
			text := fmt.Sprintf("Could not read %d queue(s): %s. Their counts show ?.", len(unread), failureText(unread))
			cmds = append(cmds, func() tea.Msg { return msg.ToastError(text) })
		}
		if s.pendingFocus != "" {
			name := s.pendingFocus
			s.pendingFocus = ""
			cmds = append(cmds, s.focusQueue(name))
		}
		return s, tea.Batch(cmds...)

	case msg.FocusResourceMsg:
		// DLQ links name a queue in this list.
		if s.loading || s.attrsLoading {
			s.pendingFocus = m.ResourceID
			return s, nil
		}
		return s, s.focusQueue(m.ResourceID)

	case sqsQueueDetailMsg:
		if m.err != nil {
			eventlog.Errorf(eventlog.CatAWS, "Queue details failed: %v", m.err)
			return s, detailErrorToast(m.err)
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
		cols, newTier := ui.BestFitTier(m.Width, sqsColumns)
		s.widthTier = newTier
		if len(cols) != len(s.table.Columns()) {
			s.table.SetColumns(cols)
			if len(s.queues) > 0 {
				s.table.SetRows(s.buildRows(s.queues))
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
			s.spinner.Show("Loading SQS queues...")
			return s, s.startLoad()
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
			urls := s.selectedQueueURLs()
			if len(urls) == 0 {
				return s, nil
			}
			s.pending.hold(urls)
			title := "Manage Queue"
			actions := []string{"Send Message", "Purge Queue", "Delete Queue"}
			if len(urls) > 1 {
				title = fmt.Sprintf("Manage %d Queues", len(urls))
				actions = []string{"Purge Queue", "Delete Queue"}
			}
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
	if s.loading || s.attrsLoading || s.running > 0 {
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

// selectedQueue returns the queue under the cursor. The queue URL is the
// row key.
func (s *SQSQueues) selectedQueue() *aws.Queue {
	return s.findQueue(s.table.CursorKey())
}

// findQueue returns the queue with the given URL, or nil.
func (s *SQSQueues) findQueue(url string) *aws.Queue {
	for i := range s.queues {
		if s.queues[i].URL == url {
			return &s.queues[i]
		}
	}
	return nil
}

// selectedQueueURLs returns the URLs of the selected queues, or the cursor
// queue URL if none is selected.
func (s *SQSQueues) selectedQueueURLs() []string {
	return s.table.TargetKeys()
}

// focusQueue moves the cursor to the queue with the given name and shows its
// details.
func (s *SQSQueues) focusQueue(name string) tea.Cmd {
	for _, q := range s.queues {
		if q.Name == name {
			s.table.SetCursorByKey(q.URL)
			return s.fetchQueueDetail(q.URL)
		}
	}
	return nil
}

// removeQueues removes the queues with the given URLs from the list.
func (s *SQSQueues) removeQueues(urls []string) {
	if len(urls) == 0 {
		return
	}
	s.queues = slices.DeleteFunc(s.queues, func(q aws.Queue) bool {
		return slices.Contains(urls, q.URL)
	})
	s.table.SetRows(s.buildRows(s.queues))
}

// ─── Mutations ──────────────────────────────────────────────────────────────

// queueNames returns the name of each queue URL, as the list shows it. The
// commands get this copy, because they must not read the view state.
func (s *SQSQueues) queueNames(urls []string) map[string]string {
	names := make(map[string]string, len(urls))
	for _, url := range urls {
		names[url] = aws.QueueNameFromURL(url)
		if q := s.findQueue(url); q != nil && q.Name != "" {
			names[url] = q.Name
		}
	}
	return names
}

// queueNameList returns the queue names in the order of urls, for a
// confirm prompt.
func (s *SQSQueues) queueNameList(urls []string) []string {
	names := s.queueNames(urls)
	list := make([]string, len(urls))
	for i, url := range urls {
		list[i] = names[url]
	}
	return list
}

// sqsReadOnlyText is the toast when ReadOnly mode is turned on between the
// action picker and the confirm reply. The app uses the same text when it
// blocks an RW key.
const sqsReadOnlyText = "ReadOnly mode — press W to switch"

// startAction shows the spinner with label while a purge or delete runs.
func (s *SQSQueues) startAction(label string, cmd tea.Cmd) tea.Cmd {
	s.running++
	s.spinner.Show(label)
	return tea.Batch(s.spinner.Tick(), cmd)
}

// finishAction hides the action spinner when the last purge or delete
// replies. A full load keeps its spinner.
func (s *SQSQueues) finishAction() {
	if s.running > 0 {
		s.running--
	}
	if s.running == 0 && !s.loading && !s.attrsLoading {
		s.spinner.Hide()
	}
}

func (s *SQSQueues) purgeQueues(urls []string) tea.Cmd {
	svc := s.sqs
	names := s.queueNames(urls)
	ctx := s.viewContext()
	return func() tea.Msg {
		// runEach gives each queue its own limit, so many queues do not
		// share one minute.
		done, failed := runEach(ctx, urls, names, func(qctx context.Context, url string) error {
			eventlog.Infof(eventlog.CatAWS, "Purging queue: %s", url)
			return svc.PurgeQueue(qctx, url)
		})
		return sqsQueuePurgedMsg{done: done, failed: failed}
	}
}

func (s *SQSQueues) deleteQueues(urls []string) tea.Cmd {
	svc := s.sqs
	names := s.queueNames(urls)
	ctx := s.viewContext()
	return func() tea.Msg {
		done, failed := runEach(ctx, urls, names, func(qctx context.Context, url string) error {
			eventlog.Infof(eventlog.CatAWS, "Deleting queue: %s", url)
			return svc.DeleteQueue(qctx, url)
		})
		return sqsQueueDeletedMsg{done: done, failed: failed}
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
	ctx := s.viewContext()
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Sending message to %s", queueURL)
		ctx, cancel := detached(ctx)
		defer cancel()
		err := svc.SendMessage(ctx, queueURL, body, delay, groupID, dedupID)
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

func (s *SQSQueues) buildRows(queues []aws.Queue) []ui.Row {
	rows := make([]ui.Row, 0, len(queues))
	for _, q := range queues {
		msgCount := strconv.Itoa(q.ApproximateMessageCount)
		inFlight := strconv.Itoa(q.ApproximateInFlightCount)
		delayed := strconv.Itoa(q.ApproximateDelayedCount)

		rawMsg := ui.SortKeyInt(int64(q.ApproximateMessageCount))
		rawFlight := ui.SortKeyInt(int64(q.ApproximateInFlightCount))
		rawDelayed := ui.SortKeyInt(int64(q.ApproximateDelayedCount))

		qType := q.Type
		if q.Err != nil {
			// The queue could not be read, so its counts are unknown, not 0.
			msgCount, inFlight, delayed = "?", "?", "?"
			rawMsg, rawFlight, rawDelayed = "", "", ""
			qType = "unknown"
		}

		created := ""
		if !q.CreatedTimestamp.IsZero() {
			created = q.CreatedTimestamp.Format("2006-01-02")
		}

		dlq := ""
		if q.IsDLQ {
			dlq = "Yes"
		}

		r := ui.Row{Key: q.URL}
		switch s.widthTier {
		case ui.TierNarrow:
			r.Cells = table.Row{q.Name, msgCount}
			r.Sort = table.Row{q.Name, rawMsg}
		case ui.TierMedium:
			r.Cells = table.Row{q.Name, qType, msgCount, inFlight, delayed}
			r.Sort = table.Row{q.Name, qType, rawMsg, rawFlight, rawDelayed}
		default:
			r.Cells = table.Row{q.Name, qType, msgCount, inFlight, delayed, dlq, created}
			r.Sort = table.Row{q.Name, qType, rawMsg, rawFlight, rawDelayed, dlq, created}
		}
		rows = append(rows, r)
	}
	return rows
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
	switch {
	case q.SqsManagedSseEnabled:
		writeField(&b, "SSE", "SQS-managed (SSE-SQS)")
	case q.KmsMasterKeyID != "":
		writeField(&b, "SSE", "KMS")
		writeField(&b, "KMS Key ID", q.KmsMasterKeyID)
		writeField(&b, "KMS Reuse Period", fmt.Sprintf("%ds", q.KmsDataKeyReusePeriod))
	default:
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
	switch {
	case s.loading && len(s.queues) == 0:
		content = "\n  " + s.spinner.View()
	case s.err != nil:
		content = "\n  " + ui.S.Error.Render("Error: "+ui.Sanitize(aws.ErrorText(s.err)))
	default:
		content = s.table.View()
		if s.filter.Active() {
			content = s.filter.View() + "\n" + content
		}
	}
	return tea.NewView(content)
}
