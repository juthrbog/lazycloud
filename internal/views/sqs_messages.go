package views

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// ─── Messages ───────────────────────────────────────────────────────────────

type sqsMessagesLoadedMsg struct {
	messages []aws.SQSMessage
	queueURL string
	// resetErr is set when some peeked messages stay hidden. A concrete
	// pointer (not error) so a nil value is never a non-nil interface.
	resetErr *aws.VisibilityResetError
}

// sqsMessageDeletedMsg reports a delete of several messages.
type sqsMessageDeletedMsg struct {
	ids    []string          // IDs of the messages that the delete tried
	failed map[string]string // message ID → error code, for the messages SQS did not delete
}

type sqsRedriveStartedMsg struct {
	taskHandle string
	err        error
}

type sqsRedriveProgressMsg struct {
	tasks []aws.MessageMoveTask
	err   error
}

// ─── Key map ────────────────────────────────────────────────────────────────

type sqsMessagesKeyMap struct {
	Esc         key.Binding
	View        key.Binding
	LoadMore    key.Binding
	Delete      key.Binding
	Manage      key.Binding
	CopyID      key.Binding
	Sort        key.Binding
	SortReverse key.Binding
	Filter      key.Binding
	Refresh     key.Binding
	Select      key.Binding
}

var defaultSQSMessagesKeyMap = sqsMessagesKeyMap{
	Esc:         key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	View:        key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "view body")),
	LoadMore:    key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "load more")),
	Delete:      key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "delete")),
	Manage:      key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "manage")),
	CopyID:      key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy ID")),
	Sort:        key.NewBinding(key.WithKeys("s"), key.WithHelp("s/S", "sort")),
	SortReverse: key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "reverse sort")),
	Filter:      key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
	Refresh:     key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
	Select:      key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "select")),
}

// ─── Model ──────────────────────────────────────────────────────────────────

// SQSMessages displays messages peeked from an SQS queue.
type SQSMessages struct {
	keys      sqsMessagesKeyMap
	sqs       aws.SQSService
	queueURL  string
	queueName string
	queueARN  string // for redrive operations
	isFIFO    bool
	isDLQ     bool  // another queue sends its failed messages to this queue
	dlqErr    error // set when the source queues could not be listed
	// Queue settings that decide whether a peek is safe. See peekBlockReason.
	attrsLoaded bool               // GetQueueAttributes has returned
	attrsErr    error              // set when the settings could not be read
	redrive     *aws.RedrivePolicy // nil when the queue has no DLQ
	visTimeout  int                // queue visibility timeout, seconds
	table       ui.Table
	messages    []aws.SQSMessage
	seen        map[string]bool // dedup by MessageID
	filter      ui.Filter
	spinner     ui.Spinner
	loading     bool
	fetched     bool // true after at least one fetch attempt
	redriving   bool // active redrive in progress
	err         error
	width       int
	height      int
	widthTier   ui.WidthTier
}

func (s *SQSMessages) ID() string    { return "sqs_messages:" + s.queueURL }
func (s *SQSMessages) Title() string { return s.queueName + " Messages" }

// CapturingInput implements nav.InputCapturer.
func (s *SQSMessages) CapturingInput() bool {
	return s.filter.Active()
}

func (s *SQSMessages) KeyMap() []ui.HintBinding {
	hints := []ui.HintBinding{
		{Binding: s.keys.View},
		{Binding: s.keys.LoadMore},
		{Binding: s.keys.Delete, Mode: ui.ModeReadWrite},
		{Binding: s.keys.Manage, Mode: ui.ModeReadWrite},
		{Binding: s.keys.CopyID},
		{Binding: s.keys.Select},
		{Binding: s.keys.Sort},
		{Binding: s.keys.Filter},
		{Binding: s.keys.Refresh},
	}
	ui.ApplyModeAll(hints)
	return hints
}

func sqsMessageColumns(tier ui.WidthTier, isFIFO bool) []ui.Column {
	if tier == ui.TierNarrow {
		return []ui.Column{
			{Title: "Message ID", Width: 20, Weight: 1, MaxWidth: 35},
			{Title: "Body", Width: 30, Weight: 2, MaxWidth: 80},
		}
	}
	cols := []ui.Column{
		{Title: "Message ID", Width: 20, Weight: 1, MaxWidth: 35},
		{Title: "Sent", Width: 20, Weight: 1, MaxWidth: 25},
		{Title: "Receives", Width: 10},
	}
	if isFIFO {
		cols = append(cols, ui.Column{Title: "Group ID", Width: 16, Weight: 1, MaxWidth: 30})
	}
	cols = append(cols, ui.Column{Title: "Body", Width: 30, Weight: 2, MaxWidth: 80})
	return cols
}

// NewSQSMessages creates the SQS message browser view.
func NewSQSMessages(sqs aws.SQSService, queueURL, queueName string, isFIFO bool) *SQSMessages {
	t := ui.NewTable(sqsMessageColumns(ui.TierMedium, isFIFO))
	return &SQSMessages{
		keys:      defaultSQSMessagesKeyMap,
		sqs:       sqs,
		queueURL:  queueURL,
		queueName: queueName,
		isFIFO:    isFIFO,
		table:     t,
		seen:      make(map[string]bool),
		filter:    ui.NewFilter(),
		spinner:   ui.NewSpinner("Peeking at messages..."),
		loading:   false,
		widthTier: ui.TierMedium,
	}
}

func (s *SQSMessages) Init() tea.Cmd {
	s.table.DeselectAll()
	// Fetch queue settings (no side effects) but don't auto-peek.
	// The user presses 'l' to consciously trigger ReceiveMessage.
	return s.fetchQueueAttrs()
}

// ─── Peek guard ─────────────────────────────────────────────────────────────

// peekBlockReason returns why a peek is not allowed, or "" when it is.
//
// SQS has no read-only peek. A peek is a real ReceiveMessage: the AWS layer
// makes the messages visible again at once, but SQS still adds 1 to each
// message's receive count. On a queue with a redrive policy, enough receives
// send a message to the dead-letter queue unprocessed. ReadOnly mode must
// never cause that, so it allows a peek only when the queue settings are
// known and show no redrive policy. ReadWrite mode always allows it.
func peekBlockReason(readOnly, attrsLoaded bool, attrsErr error, redrive *aws.RedrivePolicy) string {
	if !readOnly {
		return ""
	}
	switch {
	case !attrsLoaded:
		return "Queue settings still loading — try again"
	case attrsErr != nil:
		return "ReadOnly mode — queue settings unknown, press W to peek"
	case redrive != nil:
		return fmt.Sprintf("ReadOnly mode — peek counts toward the DLQ limit (%d), press W to peek",
			redrive.MaxReceiveCount)
	}
	return ""
}

func (s *SQSMessages) peekBlocked() string {
	return peekBlockReason(ui.IsReadOnly(), s.attrsLoaded, s.attrsErr, s.redrive)
}

// ─── Data fetching ──────────────────────────────────────────────────────────

func (s *SQSMessages) fetchMessages() tea.Cmd {
	svc := s.sqs
	queueURL := s.queueURL
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Peeking messages: %s", queueURL)
		messages, err := svc.ReceiveMessages(context.Background(), queueURL, 10)
		var resetErr *aws.VisibilityResetError
		if err != nil && !errors.As(err, &resetErr) {
			return msg.ErrorMsg{Err: err, Context: "peeking SQS messages"}
		}
		return sqsMessagesLoadedMsg{messages: messages, queueURL: queueURL, resetErr: resetErr}
	}
}

func (s *SQSMessages) fetchQueueAttrs() tea.Cmd {
	svc := s.sqs
	queueURL := s.queueURL
	return func() tea.Msg {
		q, err := svc.GetQueueAttributes(context.Background(), queueURL)
		if err == nil && q == nil {
			err = errors.New("no queue attributes returned")
		}
		if err != nil {
			return sqsQueueAttrsMsg{err: err}
		}
		// A queue is a dead-letter queue when another queue sends its failed
		// messages to it. A RedriveAllowPolicy is optional, so most DLQs
		// have none.
		sources, srcErr := svc.ListDeadLetterSourceQueues(context.Background(), queueURL)
		return sqsQueueAttrsMsg{queue: q, sources: sources, sourcesErr: srcErr}
	}
}

type sqsQueueAttrsMsg struct {
	queue      *aws.Queue
	err        error
	sources    []string // URLs of the queues that use this queue as their DLQ
	sourcesErr error    // set when the source queues could not be listed
}

// peekWarnings returns toast commands for the side effects of a peek that the
// user must know about: messages that are still hidden from consumers, and
// messages whose next receive will move them to the dead-letter queue.
func (s *SQSMessages) peekWarnings(m sqsMessagesLoadedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if m.resetErr != nil {
		hidden := "the queue's visibility timeout"
		if s.visTimeout > 0 {
			hidden = fmt.Sprintf("up to %ds", s.visTimeout)
		}
		text := fmt.Sprintf("%d message(s) stay hidden from consumers for %s", m.resetErr.Failed, hidden)
		eventlog.Errorf(eventlog.CatAWS, "Peek on %s: %v", s.queueName, m.resetErr)
		cmds = append(cmds, func() tea.Msg { return msg.ToastError(text) })
	}
	if s.redrive != nil {
		limit := s.redrive.MaxReceiveCount
		atLimit := 0
		for _, sm := range m.messages {
			if sm.ApproximateReceiveCount >= limit {
				atLimit++
			}
		}
		if atLimit > 0 {
			text := fmt.Sprintf("%d message(s) at the DLQ limit (%d) — the next receive moves them to the DLQ", atLimit, limit)
			eventlog.Warnf(eventlog.CatAWS, "Peek on %s: %s", s.queueName, text)
			cmds = append(cmds, func() tea.Msg { return msg.ToastError(text) })
		}
	}
	return cmds
}

// startRedrive moves the messages of this DLQ back to their source queues.
func (s *SQSMessages) startRedrive() tea.Cmd {
	arn := s.queueARN
	svc := s.sqs
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Starting redrive from: %s", arn)
		handle, err := svc.StartMessageMoveTask(context.Background(), arn, "")
		return sqsRedriveStartedMsg{taskHandle: handle, err: err}
	}
}

func (s *SQSMessages) pollRedrive() tea.Cmd {
	svc := s.sqs
	arn := s.queueARN
	return func() tea.Msg {
		time.Sleep(2 * time.Second)
		tasks, err := svc.ListMessageMoveTasks(context.Background(), arn)
		return sqsRedriveProgressMsg{tasks: tasks, err: err}
	}
}

// ─── Update ─────────────────────────────────────────────────────────────────

func (s *SQSMessages) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case ui.PickerResultMsg:
		if m.ID == "sort" {
			if m.Value == "_clear" {
				s.table.ClearSort()
			} else if m.Selected >= 0 {
				s.table.Sort(m.Selected)
			}
		} else if m.ID == "action" && m.Selected >= 0 {
			switch m.Value {
			case "Start Redrive":
				if s.queueARN == "" {
					return s, func() tea.Msg {
						return msg.ToastError("Queue ARN not available")
					}
				}
				// A redrive moves every message of the DLQ, so it asks.
				// The messages are moved, not lost, so y/n is enough.
				text := fmt.Sprintf("Move all messages of %s back to their source queues?", s.queueName)
				return s, func() tea.Msg {
					return msg.RequestConfirmMsg{Message: text, Action: "sqs_redrive", Quick: true}
				}
			}
		}
		return s, nil

	case ui.ConfirmResultMsg:
		if !m.Confirmed {
			return s, nil
		}
		switch m.Action {
		case "sqs_delete_messages":
			return s, s.deleteSelectedMessages()
		case "sqs_redrive":
			return s, s.startRedrive()
		}
		return s, nil

	case sqsQueueAttrsMsg:
		s.attrsLoaded = true
		if m.err != nil {
			s.attrsErr = m.err
			eventlog.Warnf(eventlog.CatAWS, "Could not read attributes of %s: %v", s.queueName, m.err)
			return s, nil
		}
		s.attrsErr = nil
		s.queueARN = m.queue.ARN
		s.isDLQ = len(m.sources) > 0
		s.dlqErr = m.sourcesErr
		if m.sourcesErr != nil {
			eventlog.Warnf(eventlog.CatAWS, "Could not list the source queues of %s: %v", s.queueName, m.sourcesErr)
		}
		s.redrive = m.queue.RedrivePolicy
		s.visTimeout = m.queue.VisibilityTimeout
		return s, nil

	case sqsMessagesLoadedMsg:
		s.loading = false
		s.fetched = true
		s.spinner.Hide()
		warnings := s.peekWarnings(m)
		newCount := 0
		for _, m := range m.messages {
			if !s.seen[m.MessageID] {
				s.seen[m.MessageID] = true
				s.messages = append(s.messages, m)
				newCount++
			} else {
				// Update existing message with fresh attributes (e.g., ReceiveCount).
				for i := range s.messages {
					if s.messages[i].MessageID == m.MessageID {
						s.messages[i] = m
						break
					}
				}
			}
		}
		s.rebuildRows()
		if newCount > 0 {
			eventlog.Infof(eventlog.CatAWS, "Peeked %d new messages (%d total)", newCount, len(s.messages))
		}
		return s, tea.Batch(warnings...)

	case sqsMessageDeletedMsg:
		// Only the messages that SQS deleted leave the list. A failed one
		// stays, so the user can see it and try again.
		var failures []itemFailure
		deleted := make(map[string]bool, len(m.ids))
		for _, id := range m.ids {
			if code, ok := m.failed[id]; ok {
				failures = append(failures, itemFailure{name: truncateID(id), err: errors.New(code)})
				continue
			}
			deleted[id] = true
			delete(s.seen, id)
		}
		s.messages = slices.DeleteFunc(s.messages, func(sm aws.SQSMessage) bool { return deleted[sm.MessageID] })
		s.table.DeselectAll()
		s.rebuildRows()
		eventlog.Infof(eventlog.CatAWS, "Deleted %d SQS messages", len(deleted))
		// On a standard queue, SQS also accepts the delete of an old receipt
		// handle that leaves the message in the queue. Say what SQS
		// confirmed, not more.
		verb := "Deleted"
		if !s.isFIFO {
			verb = "SQS accepted the delete of"
		}
		text, ok := resultText(verb, len(m.ids), "message(s)", failures)
		return s, func() tea.Msg {
			if ok {
				return msg.ToastSuccess(text)
			}
			return msg.ToastError(text)
		}

	case sqsRedriveStartedMsg:
		if m.err != nil {
			eventlog.Errorf(eventlog.CatAWS, "Redrive failed: %v", m.err)
			return s, func() tea.Msg {
				return msg.ToastError("Redrive failed: " + m.err.Error())
			}
		}
		eventlog.Infof(eventlog.CatAWS, "Redrive started")
		s.redriving = true
		s.spinner.Show("Redrive in progress...")
		return s, tea.Batch(s.spinner.Tick(), s.pollRedrive(), func() tea.Msg {
			return msg.ToastSuccess("Redrive started")
		})

	case sqsRedriveProgressMsg:
		if m.err != nil || !s.redriving {
			return s, nil
		}
		for _, task := range m.tasks {
			switch task.Status {
			case "COMPLETED":
				s.redriving = false
				s.spinner.Hide()
				moved := task.ApproximateNumberOfMsgsMoved
				eventlog.Infof(eventlog.CatAWS, "Redrive completed: %d messages moved", moved)
				return s, func() tea.Msg {
					return msg.ToastSuccess(fmt.Sprintf("Redrive completed: %d messages moved", moved))
				}
			case "FAILED", "CANCELLED":
				s.redriving = false
				s.spinner.Hide()
				status := task.Status
				reason := task.FailureReason
				eventlog.Errorf(eventlog.CatAWS, "Redrive %s: %s", strings.ToLower(status), reason)
				return s, func() tea.Msg {
					return msg.ToastError(fmt.Sprintf("Redrive %s: %s", strings.ToLower(status), reason))
				}
			case "RUNNING":
				moved := task.ApproximateNumberOfMsgsMoved
				total := task.ApproximateNumberOfMessages
				s.spinner.Show(fmt.Sprintf("Redrive: %d/%d messages moved...", moved, total))
				return s, tea.Batch(s.spinner.Tick(), s.pollRedrive())
			}
		}
		// No matching tasks found — stop polling
		s.redriving = false
		s.spinner.Hide()
		return s, nil

	case msg.ErrorMsg:
		s.loading = false
		s.spinner.Hide()
		s.err = m.Err
		return s, nil

	case tea.WindowSizeMsg:
		s.width = m.Width
		s.height = m.Height
		isFIFO := s.isFIFO
		cols, newTier := ui.BestFitTier(m.Width, func(t ui.WidthTier) []ui.Column {
			return sqsMessageColumns(t, isFIFO)
		})
		s.widthTier = newTier
		if len(cols) != len(s.table.Columns()) {
			s.table.SetColumns(cols)
			s.rebuildRows()
		}
		s.table.SetSize(m.Width, m.Height-4) // -4 for warning banner
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
			if reason := s.peekBlocked(); reason != "" {
				return s, func() tea.Msg { return msg.ToastError(reason) }
			}
			s.loading = true
			s.fetched = false
			s.err = nil
			s.messages = nil
			s.seen = make(map[string]bool)
			s.spinner.Show("Peeking at messages...")
			return s, tea.Batch(s.spinner.Tick(), s.fetchMessages())
		case key.Matches(m, s.keys.LoadMore):
			if s.loading {
				return s, nil
			}
			if reason := s.peekBlocked(); reason != "" {
				return s, func() tea.Msg { return msg.ToastError(reason) }
			}
			s.loading = true
			s.spinner.Show("Loading more messages...")
			return s, tea.Batch(s.spinner.Tick(), s.fetchMessages())
		case key.Matches(m, s.keys.View):
			selected := s.selectedMessage()
			if selected == nil {
				return s, nil
			}
			body := selected.Body
			format := "text"
			if json.Valid([]byte(body)) {
				var indented bytes.Buffer
				if json.Indent(&indented, []byte(body), "", "  ") == nil {
					body = indented.String()
				}
				format = "json"
			}
			title := "Message " + truncateID(selected.MessageID)
			return s, func() tea.Msg {
				return msg.NavigateMsg{
					ViewID: "content",
					Params: map[string]string{
						"title":   title,
						"content": body,
						"format":  format,
					},
				}
			}
		case key.Matches(m, s.keys.CopyID):
			ids := s.selectedMessageIDs()
			if len(ids) == 0 {
				return s, nil
			}
			text := strings.Join(ids, "\n")
			return s, tea.Batch(
				tea.SetClipboard(text),
				func() tea.Msg {
					return msg.ToastSuccess(fmt.Sprintf("Copied %d message ID(s)", len(ids)))
				},
			)
		case key.Matches(m, s.keys.Select):
			s.table.ToggleSelect()
			return s, nil
		case key.Matches(m, s.keys.Delete):
			handles := s.selectedReceiptHandles()
			if len(handles) == 0 {
				return s, nil
			}
			message := fmt.Sprintf("Delete %d message(s)?", len(handles))
			if !s.isFIFO {
				message += "\n" + staleHandleNote
			}
			return s, func() tea.Msg {
				return msg.RequestConfirmMsg{
					Message: message,
					Action:  "sqs_delete_messages",
				}
			}
		case key.Matches(m, s.keys.Manage):
			if s.dlqErr != nil {
				code := aws.ErrorCode(s.dlqErr)
				return s, func() tea.Msg {
					return msg.ToastError("Cannot tell whether this queue is a DLQ: " + code)
				}
			}
			if !s.isDLQ {
				return s, func() tea.Msg {
					return msg.ToastError("Redrive needs a DLQ. No queue sends its failed messages to this queue.")
				}
			}
			return s, func() tea.Msg {
				return msg.RequestActionPickerMsg{
					Title:   "DLQ Actions",
					Options: []string{"Start Redrive"},
				}
			}
		}
	}

	if s.loading && len(s.messages) == 0 {
		var cmd tea.Cmd
		s.spinner, cmd = s.spinner.Update(m)
		return s, cmd
	}

	var cmds []tea.Cmd
	if s.loading || s.redriving {
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

// findMessage returns the message with the given ID, or nil.
func (s *SQSMessages) findMessage(id string) *aws.SQSMessage {
	for i := range s.messages {
		if s.messages[i].MessageID == id {
			return &s.messages[i]
		}
	}
	return nil
}

// selectedMessage returns the message under the cursor. The message ID is
// the row key.
func (s *SQSMessages) selectedMessage() *aws.SQSMessage {
	return s.findMessage(s.table.CursorKey())
}

// selectedMessageIDs returns the IDs of the selected messages, or the cursor
// message ID if none is selected.
func (s *SQSMessages) selectedMessageIDs() []string {
	return s.table.TargetKeys()
}

// selectedReceiptHandles returns the receipt handles of the messages that
// selectedMessageIDs returns.
func (s *SQSMessages) selectedReceiptHandles() []string {
	ids := s.selectedMessageIDs()
	handles := make([]string, 0, len(ids))
	for _, id := range ids {
		if m := s.findMessage(id); m != nil {
			handles = append(handles, m.ReceiptHandle)
		}
	}
	return handles
}

// staleHandleNote explains a limit of SQS on standard queues: the delete
// of an old receipt handle succeeds, but the message stays when it was
// received again after that handle.
const staleHandleNote = "If a consumer received a message after this peek, SQS reports\n" +
	"success but keeps the message. To be sure, cancel and press r first."

// deleteSelectedMessages deletes the selected messages, or the cursor
// message. The rows stay until the result arrives. Then only the messages
// that SQS deleted leave the list.
func (s *SQSMessages) deleteSelectedMessages() tea.Cmd {
	ids := s.selectedMessageIDs()
	idByHandle := make(map[string]string, len(ids))
	handles := make([]string, 0, len(ids))
	for _, id := range ids {
		if m := s.findMessage(id); m != nil {
			idByHandle[m.ReceiptHandle] = id
			handles = append(handles, m.ReceiptHandle)
		}
	}
	if len(handles) == 0 {
		return nil
	}
	svc := s.sqs
	queueURL := s.queueURL
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Deleting %d messages from %s", len(handles), queueURL)
		err := svc.DeleteMessageBatch(context.Background(), queueURL, handles)
		res := sqsMessageDeletedMsg{failed: map[string]string{}}
		for _, h := range handles {
			res.ids = append(res.ids, idByHandle[h])
		}
		var batchErr *aws.BatchDeleteError
		switch {
		case err == nil:
		case errors.As(err, &batchErr):
			for _, f := range batchErr.Failed {
				eventlog.Errorf(eventlog.CatAWS, "Delete of message %s failed: %s %s", idByHandle[f.ReceiptHandle], f.Code, f.Message)
				res.failed[idByHandle[f.ReceiptHandle]] = f.Code
			}
		default:
			// The call did not run, for example in ReadOnly mode.
			eventlog.Errorf(eventlog.CatAWS, "Delete of %d messages failed: %v", len(handles), err)
			for _, h := range handles {
				res.failed[idByHandle[h]] = aws.ErrorCode(err)
			}
		}
		return res
	}
}

// ─── Row building ───────────────────────────────────────────────────────────

func (s *SQSMessages) rebuildRows() {
	rows := make([]ui.Row, 0, len(s.messages))
	for _, m := range s.messages {
		sent := ""
		sentSortKey := ""
		if !m.SentTimestamp.IsZero() {
			sent = m.SentTimestamp.Format("2006-01-02 15:04:05")
			sentSortKey = sent
		}
		receives := fmt.Sprintf("%d", m.ApproximateReceiveCount)
		receivesSort := ui.SortKeyInt(int64(m.ApproximateReceiveCount))
		bodyPreview := sanitizeBody(m.Body)

		r := ui.Row{Key: m.MessageID}
		switch s.widthTier {
		case ui.TierNarrow:
			r.Cells = table.Row{m.MessageID, bodyPreview}
			r.Sort = table.Row{m.MessageID, m.Body}
		default:
			if s.isFIFO {
				r.Cells = table.Row{m.MessageID, sent, receives, m.MessageGroupID, bodyPreview}
				r.Sort = table.Row{m.MessageID, sentSortKey, receivesSort, m.MessageGroupID, m.Body}
			} else {
				r.Cells = table.Row{m.MessageID, sent, receives, bodyPreview}
				r.Sort = table.Row{m.MessageID, sentSortKey, receivesSort, m.Body}
			}
		}
		rows = append(rows, r)
	}
	s.table.SetRows(rows)
}

func truncateID(id string) string {
	if len(id) > 16 {
		return id[:16] + "…"
	}
	return id
}

func sanitizeBody(body string) string {
	body = strings.ReplaceAll(body, "\n", " ")
	body = strings.ReplaceAll(body, "\r", "")
	return body
}

// ─── Footer & View ──────────────────────────────────────────────────────────

func (s *SQSMessages) Footer() string {
	filtered, total := s.table.RowCount()
	footer := fmt.Sprintf("%d/%d messages", filtered, total)
	if sel := s.table.SelectionCount(); sel > 0 {
		footer += fmt.Sprintf("  (%d selected)", sel)
	}
	if s.spinner.Visible() {
		footer += "  " + s.spinner.View()
	}
	return footer
}

// bannerText explains what a peek does to this queue. It is one line so the
// table height (m.Height-4) stays correct.
func (s *SQSMessages) bannerText() string {
	switch {
	case s.redrive != nil && ui.IsReadOnly():
		return fmt.Sprintf("⚠ Peek blocked in ReadOnly mode: each peek adds 1 to the receive count (DLQ after %d)",
			s.redrive.MaxReceiveCount)
	case s.redrive != nil:
		return fmt.Sprintf("⚠ Each peek adds 1 to the receive count. This queue moves messages to its DLQ after %d",
			s.redrive.MaxReceiveCount)
	case s.attrsErr != nil && ui.IsReadOnly():
		return "⚠ Peek blocked in ReadOnly mode: could not read queue settings"
	}
	return "⚠ Peek makes messages visible again at once, but adds 1 to their receive count"
}

func (s *SQSMessages) View() tea.View {
	t := ui.ActiveTheme
	warnStyle := lipgloss.NewStyle().Foreground(t.StatePending)
	text := "  " + s.bannerText()
	if s.width > 0 {
		text = ansi.Truncate(text, s.width, "…")
	}
	banner := warnStyle.Render(text)

	var content string
	if s.loading && len(s.messages) == 0 {
		content = "\n  " + s.spinner.View()
	} else if s.err != nil {
		content = "\n  " + ui.ErrorStyle.Render("Error: "+ui.Sanitize(s.err.Error()))
	} else if len(s.messages) == 0 && s.fetched {
		content = "\n  No messages in queue. Press l to peek again."
	} else if len(s.messages) == 0 && s.attrsLoaded && s.peekBlocked() != "" {
		content = "\n  Press W to switch to ReadWrite mode, then l to peek at messages"
	} else if len(s.messages) == 0 {
		content = "\n  Press l to peek at messages"
	} else {
		content = s.table.View()
		if s.filter.Active() {
			content = s.filter.View() + "\n" + content
		}
	}
	return tea.NewView(banner + "\n" + content)
}
