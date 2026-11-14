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

type sqsQueueDetailMsg struct {
	queue        *aws.Queue
	sourceQueues []string
	err          error
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

// ─── Keys ───────────────────────────────────────────────────────────────────

// sqsKeys are the keys only the SQS list has. CopyURL is only the hint of y,
// which the core handles.
var sqsKeys = struct {
	Peek    ui.HintBinding
	Manage  ui.HintBinding
	CopyURL ui.HintBinding
}{
	Peek:    ui.NewHintBinding([]string{"p"}, "p", "peek messages"),
	Manage:  ui.HintBinding{Binding: key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "manage")), Mode: ui.ModeReadWrite},
	CopyURL: ui.NewHintBinding([]string{"y"}, "y", "copy URL"),
}

// ─── Model ──────────────────────────────────────────────────────────────────

// SQSQueues displays all SQS queues. A load has two phases: the queue URL
// pages, then the attributes of all queues in one call.
type SQSQueues struct {
	listCore[aws.Queue]
	sqs        aws.SQSService
	pending    pendingConfirm // targets of Purge and Delete
	sendTarget string         // queue URL of the open Send Message form
}

// NewSQSQueues creates the SQS queue list view.
func NewSQSQueues(sqs aws.SQSService) *SQSQueues {
	s := &SQSQueues{sqs: sqs}
	s.listCore = newListCore(listConfig[aws.Queue]{
		Noun:         "queues",
		LoadingLabel: "Loading SQS queues...",
		ErrContext:   "listing SQS queues",
		Key:          func(q aws.Queue) string { return q.URL },
		Columns:      sqsColumns,
		Row:          sqsRow,
		Fetch:        fetchPages(sqs, aws.SQSService.ListQueuesPage, splitQueuePage),
		FetchTimeout: callTimeout,
		Complete: &completePhase[aws.Queue]{
			Run:     fetchQueueAttributes(sqs),
			Timeout: listTimeout,
			Label:   "Loading attributes for %d queues...",
			Context: "fetching SQS queue attributes",
		},
		Derive:      markDLQQueues,
		OnLoaded:    func(queues []aws.Queue, _ string) tea.Cmd { return unreadableToast(queues) },
		Refresh:     refreshEach(sqs, aws.SQSService.GetQueueAttributes, callTimeout),
		FocusKey:    func(q aws.Queue) string { return q.Name }, // DLQ links name a queue
		OpenDetail:  func(q aws.Queue) tea.Cmd { return s.fetchQueueDetail(q.URL) },
		MultiSelect: true,
		Copy:        true,
		CopyNoun:    "queue URL",
	})
	return s
}

func (s *SQSQueues) ID() string     { return "sqs_queues" }
func (s *SQSQueues) Title() string  { return "SQS Queues" }
func (s *SQSQueues) Init() tea.Cmd  { return s.init() }
func (s *SQSQueues) Footer() string { return s.footer() }
func (s *SQSQueues) View() tea.View { return tea.NewView(s.render("")) }

func (s *SQSQueues) KeyMap() []ui.HintBinding {
	return s.hints(listKeys.Details, sqsKeys.Peek, sqsKeys.Manage, sqsKeys.CopyURL,
		listKeys.Select, listKeys.Sort, listKeys.Filter, listKeys.Refresh)
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

// Update handles the Manage flow, peek and the detail reply, and hands the
// rest to the core. It returns the view itself, so the navigator keeps its
// identity.
func (s *SQSQueues) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case ui.PickerResultMsg:
		if m.ID == "action" {
			return s, s.onActionPicked(m)
		}
	case msg.FormResultMsg:
		return s, s.onForm(m)
	case ui.ConfirmResultMsg:
		return s, s.onConfirm(m)
	case sqsQueuePurgedMsg:
		return s, s.onPurged(m)
	case sqsQueueDeletedMsg:
		return s, s.onDeleted(m)
	case sqsMessageSentMsg:
		return s, s.onSent(m)
	case sqsQueueDetailMsg:
		return s, s.onDetail(m)
	case tea.KeyPressMsg:
		// The error screen hides the rows, so p and m must not act on them.
		if !s.CapturingInput() && s.err == nil {
			switch {
			case key.Matches(m, sqsKeys.Peek.Binding):
				return s, s.peek()
			case key.Matches(m, sqsKeys.Manage.Binding):
				return s, s.manage()
			}
		}
	}
	return s, s.update(m)
}

// ─── Data fetching ──────────────────────────────────────────────────────────

// splitQueuePage turns a page of queue URLs into queues with a URL and a
// name. The second load phase reads the other fields.
func splitQueuePage(p *aws.QueuePage) ([]aws.Queue, *string) {
	queues := make([]aws.Queue, len(p.QueueURLs))
	for i, url := range p.QueueURLs {
		queues[i] = aws.Queue{URL: url, Name: aws.QueueNameFromURL(url)}
	}
	return queues, nextToken(p.HasMorePages, p.Token)
}

// fetchQueueAttributes is the second load phase: one call reads the
// attributes of all queues.
func fetchQueueAttributes(svc aws.SQSService) func(context.Context, []aws.Queue) ([]aws.Queue, error) {
	return func(ctx context.Context, queues []aws.Queue) ([]aws.Queue, error) {
		if svc == nil {
			return nil, errNoClient
		}
		urls := make([]string, len(queues))
		for i, q := range queues {
			urls[i] = q.URL
		}
		eventlog.Infof(eventlog.CatAWS, "Fetching attributes for %d SQS queues", len(urls))
		return svc.FetchAllQueueAttributes(ctx, urls)
	}
}

// unreadableToast logs each queue whose attributes could not be read and
// returns one toast that names them, or nil.
func unreadableToast(queues []aws.Queue) tea.Cmd {
	var unread []itemFailure
	for _, q := range queues {
		if q.Err != nil {
			eventlog.Errorf(eventlog.CatAWS, "Could not read queue %s: %v", q.Name, q.Err)
			unread = append(unread, itemFailure{name: q.Name, err: q.Err})
		}
	}
	if len(unread) == 0 {
		return nil
	}
	text := fmt.Sprintf("Could not read %d queue(s): %s. Their counts show ?.", len(unread), failureText(unread))
	return func() tea.Msg { return msg.ToastError(text) }
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

// onDetail opens the detail panel. An error gives a toast and the list stays.
func (s *SQSQueues) onDetail(m sqsQueueDetailMsg) tea.Cmd {
	if m.err != nil {
		eventlog.Errorf(eventlog.CatAWS, "Queue details failed: %v", m.err)
		return detailErrorToast(m.err)
	}
	if m.queue == nil {
		return nil
	}
	title := m.queue.Name
	tabs := buildSQSDetailTabs(m.queue, m.sourceQueues)
	return func() tea.Msg {
		return msg.TabbedContentMsg{PanelTitle: title, Tabs: tabs}
	}
}

// ─── Keys of the view ───────────────────────────────────────────────────────

// peek opens the messages of the cursor queue.
func (s *SQSQueues) peek() tea.Cmd {
	q := s.cursorItem()
	if q == nil {
		return nil
	}
	params := map[string]string{
		"queue_url":  q.URL,
		"queue_name": q.Name,
		"fifo":       fmt.Sprintf("%t", q.FifoQueue),
	}
	return func() tea.Msg {
		return msg.NavigateMsg{ViewID: "sqs_messages", Params: params}
	}
}

// manage opens the action picker for the targets.
func (s *SQSQueues) manage() tea.Cmd {
	urls := s.targets()
	if len(urls) == 0 {
		return nil
	}
	s.pending.hold(urls)
	title := "Manage Queue"
	actions := []string{"Send Message", "Purge Queue", "Delete Queue"}
	if len(urls) > 1 {
		title = fmt.Sprintf("Manage %d Queues", len(urls))
		actions = []string{"Purge Queue", "Delete Queue"}
	}
	return func() tea.Msg {
		return msg.RequestActionPickerMsg{Title: title, Options: actions}
	}
}

// ─── Mutations ──────────────────────────────────────────────────────────────

// onActionPicked opens the send form or the confirm prompt of the picked
// action.
func (s *SQSQueues) onActionPicked(m ui.PickerResultMsg) tea.Cmd {
	if m.Selected < 0 {
		return nil
	}
	urls := s.pending.keys
	switch m.Value {
	case "Send Message":
		// Send acts on the same queue as Purge and Delete: the selection,
		// or the cursor queue. The menu offers it only for one queue.
		if len(urls) != 1 {
			return func() tea.Msg {
				return msg.ToastError("Select one queue to send a message")
			}
		}
		q := s.find(urls[0])
		if q == nil {
			return nil
		}
		fields := sendFormFields(q)
		s.sendTarget = q.URL
		s.pending.clear()
		return func() tea.Msg {
			return msg.RequestFormMsg{ID: "sqs_send_message", Title: "Send Message", Fields: fields}
		}
	case "Purge Queue":
		return s.pending.ask(msg.RequestConfirmMsg{
			Message: confirmMessage("Purge", len(urls), "queue(s)", s.queueNameList(urls),
				"This removes all messages and cannot be undone."),
			Action: "sqs_purge",
		})
	case "Delete Queue":
		return s.pending.ask(msg.RequestConfirmMsg{
			Message: confirmMessage("Delete", len(urls), "queue(s)", s.queueNameList(urls),
				"All messages will be lost."),
			Action: "sqs_delete",
		})
	}
	return nil
}

// sendFormFields returns the fields of the Send Message form for q.
func sendFormFields(q *aws.Queue) []msg.FormField {
	fields := []msg.FormField{
		{Key: "body", Type: "text", Title: "Message Body", Required: true,
			Placeholder: "Enter message body (JSON or plain text)"},
	}
	if !q.FifoQueue {
		// Standard queues support per-message delay.
		return append(fields, msg.FormField{Key: "delay", Type: "input", Title: "Delay Seconds",
			Placeholder: "0", Description: "Optional. Seconds to delay delivery (0-900).",
			Validate: validateDelay})
	}
	// FIFO queues require MessageGroupId and don't support per-message delay.
	fields = append(fields, msg.FormField{Key: "group_id", Type: "input", Title: "Message Group ID",
		Required: true, Placeholder: "Enter group ID",
		Description: "Required. Messages in the same group are processed in order.",
		Validate:    huh.ValidateMaxLength(128)})
	if !q.ContentBasedDeduplication {
		return append(fields, msg.FormField{Key: "dedup_id", Type: "input", Title: "Deduplication ID",
			Required: true, Placeholder: "Enter deduplication ID",
			Description: "Required (content-based dedup is disabled on this queue).",
			Validate:    huh.ValidateMaxLength(128)})
	}
	return append(fields, msg.FormField{Key: "dedup_id", Type: "input", Title: "Deduplication ID",
		Placeholder: "Leave empty for content-based dedup",
		Description: "Optional. Auto-generated from message body if empty.",
		Validate:    huh.ValidateMaxLength(128)})
}

// validateDelay accepts an empty delay or a number from 0 to 900.
func validateDelay(s string) error {
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
}

// onForm sends the message of the Send Message form. Every form reply
// clears the target, also an aborted form.
func (s *SQSQueues) onForm(m msg.FormResultMsg) tea.Cmd {
	queueURL := s.sendTarget
	s.sendTarget = ""
	if m.ID == "sqs_send_message" && !m.Aborted && queueURL != "" {
		return s.sendMessage(queueURL, m.Values)
	}
	return nil
}

// onConfirm runs a confirmed purge or delete.
func (s *SQSQueues) onConfirm(m ui.ConfirmResultMsg) tea.Cmd {
	urls, ok := s.pending.take(m)
	if !ok {
		return nil
	}
	if ui.IsReadOnly() {
		// The mode changed after the picker opened.
		return func() tea.Msg { return msg.ToastError(sqsReadOnlyText) }
	}
	// The spinner shows until the reply, also across a refresh reply.
	switch m.Action {
	case "sqs_purge":
		return tea.Batch(s.startTask(fmt.Sprintf("Purging %d queue(s)...", len(urls))), s.purgeQueues(urls))
	case "sqs_delete":
		return tea.Batch(s.startTask(fmt.Sprintf("Deleting %d queue(s)...", len(urls))), s.deleteQueues(urls))
	}
	return nil
}

// onPurged shows the result and refreshes the purged queues.
func (s *SQSQueues) onPurged(m sqsQueuePurgedMsg) tea.Cmd {
	s.endTask()
	for _, f := range m.failed {
		eventlog.Errorf(eventlog.CatAWS, "Purge of %s failed: %v", f.name, f.err)
	}
	eventlog.Infof(eventlog.CatAWS, "Purged %d queue(s)", len(m.done))
	toast := m.Outcome()
	cmds := []tea.Cmd{func() tea.Msg { return toast }}
	if len(m.done) > 0 {
		cmds = append(cmds, s.refreshKeys(m.done))
	}
	return tea.Batch(cmds...)
}

// onDeleted shows the result and removes only the deleted queues. The
// failed ones stay in the list.
func (s *SQSQueues) onDeleted(m sqsQueueDeletedMsg) tea.Cmd {
	s.endTask()
	for _, f := range m.failed {
		eventlog.Errorf(eventlog.CatAWS, "Delete of queue %s failed: %v", f.name, f.err)
	}
	for _, url := range m.done {
		eventlog.Infof(eventlog.CatAWS, "Deleted queue: %s", url)
	}
	s.removeKeys(m.done)
	s.table.DeselectAll()
	toast := m.Outcome()
	return func() tea.Msg { return toast }
}

func (s *SQSQueues) onSent(m sqsMessageSentMsg) tea.Cmd {
	if m.err != nil {
		eventlog.Errorf(eventlog.CatAWS, "SendMessage failed: %v", m.err)
	} else {
		eventlog.Infof(eventlog.CatAWS, "Message sent")
	}
	toast := m.Outcome()
	return func() tea.Msg { return toast }
}

// queueNames returns the name of each queue URL, as the list shows it. The
// commands get this copy, because they must not read the view state.
func (s *SQSQueues) queueNames(urls []string) map[string]string {
	names := make(map[string]string, len(urls))
	for _, url := range urls {
		names[url] = aws.QueueNameFromURL(url)
		if q := s.find(url); q != nil && q.Name != "" {
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

// markDLQQueues sets IsDLQ on each queue: true when another queue in the
// list names it as its dead-letter queue, or when it has a redrive allow
// policy. It sets false too, so a refresh or a delete cannot leave a stale
// mark. It is the Derive of the list.
func markDLQQueues(queues []aws.Queue) {
	dlqARNs := make(map[string]bool)
	for _, q := range queues {
		if q.RedrivePolicy != nil {
			dlqARNs[q.RedrivePolicy.DeadLetterTargetArn] = true
		}
	}
	for i := range queues {
		queues[i].IsDLQ = dlqARNs[queues[i].ARN] || queues[i].RedriveAllowPolicy != nil
	}
}

// ─── Row building ───────────────────────────────────────────────────────────

func sqsRow(q aws.Queue, tier ui.WidthTier) ui.Row {
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
	switch tier {
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
	return r
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
