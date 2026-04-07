package views

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// ─── Messages ───────────────────────────────────────────────────────────────

type sqsMessagesLoadedMsg struct {
	messages []aws.SQSMessage
	queueURL string
}

type sqsMessageDeletedMsg struct {
	count int
	err   error
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
	isDLQ     bool
	table     ui.Table
	messages  []aws.SQSMessage
	seen      map[string]bool // dedup by MessageID
	filter    ui.Filter
	spinner   ui.Spinner
	loading   bool
	redriving bool // active redrive in progress
	err       error
	width     int
	height    int
	widthTier ui.WidthTier
}

func (s *SQSMessages) ID() string    { return "sqs_messages" }
func (s *SQSMessages) Title() string { return s.queueName + " Messages" }
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

func sqsMessageColumns(tier ui.WidthTier, isFIFO bool) []table.Column {
	if tier == ui.TierNarrow {
		return []table.Column{
			{Title: "Message ID", Width: 20},
			{Title: "Body", Width: 30},
		}
	}
	cols := []table.Column{
		{Title: "Message ID", Width: 20},
		{Title: "Sent", Width: 20},
		{Title: "Receives", Width: 10},
	}
	if isFIFO {
		cols = append(cols, table.Column{Title: "Group ID", Width: 16})
	}
	cols = append(cols, table.Column{Title: "Body", Width: 30})
	return cols
}

// NewSQSMessages creates the SQS message browser view.
func NewSQSMessages(sqs aws.SQSService, queueURL, queueName string, isFIFO bool) *SQSMessages {
	t := ui.NewTable(sqsMessageColumns(ui.TierMedium, isFIFO), nil)
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
	// Fetch queue ARN (lightweight, no side effects) but don't auto-peek.
	// The user presses 'l' to consciously trigger ReceiveMessage.
	return s.fetchQueueARN()
}

// ─── Data fetching ──────────────────────────────────────────────────────────

func (s *SQSMessages) fetchMessages() tea.Cmd {
	svc := s.sqs
	queueURL := s.queueURL
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Peeking messages: %s", queueURL)
		messages, err := svc.ReceiveMessages(context.Background(), queueURL, 10)
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: "peeking SQS messages"}
		}
		return sqsMessagesLoadedMsg{messages: messages, queueURL: queueURL}
	}
}

func (s *SQSMessages) fetchQueueARN() tea.Cmd {
	svc := s.sqs
	queueURL := s.queueURL
	return func() tea.Msg {
		q, err := svc.GetQueueAttributes(context.Background(), queueURL)
		if err == nil && q != nil {
			return sqsQueueARNMsg{arn: q.ARN, isDLQ: q.RedriveAllowPolicy != nil}
		}
		return nil
	}
}

type sqsQueueARNMsg struct {
	arn   string
	isDLQ bool
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
				arn := s.queueARN
				svc := s.sqs
				return s, func() tea.Msg {
					eventlog.Infof(eventlog.CatAWS, "Starting redrive from: %s", arn)
					handle, err := svc.StartMessageMoveTask(context.Background(), arn, "")
					return sqsRedriveStartedMsg{taskHandle: handle, err: err}
				}
			}
		}
		return s, nil

	case ui.ConfirmResultMsg:
		if !m.Confirmed {
			return s, nil
		}
		if m.Action == "sqs_delete_messages" {
			return s, s.deleteSelectedMessages()
		}
		return s, nil

	case sqsQueueARNMsg:
		s.queueARN = m.arn
		s.isDLQ = m.isDLQ
		return s, nil

	case sqsMessagesLoadedMsg:
		s.loading = false
		s.spinner.Hide()
		newCount := 0
		for _, m := range m.messages {
			if !s.seen[m.MessageID] {
				s.seen[m.MessageID] = true
				s.messages = append(s.messages, m)
				newCount++
			}
		}
		s.rebuildRows()
		if newCount > 0 {
			eventlog.Infof(eventlog.CatAWS, "Peeked %d new messages (%d total)", newCount, len(s.messages))
		}
		return s, nil

	case sqsMessageDeletedMsg:
		if m.err != nil {
			return s, func() tea.Msg {
				return msg.ToastError("Delete failed: " + m.err.Error())
			}
		}
		count := m.count
		return s, func() tea.Msg {
			return msg.ToastSuccess(fmt.Sprintf("Deleted %d message(s)", count))
		}

	case sqsRedriveStartedMsg:
		if m.err != nil {
			return s, func() tea.Msg {
				return msg.ToastError("Redrive failed: " + m.err.Error())
			}
		}
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
				return s, func() tea.Msg {
					return msg.ToastSuccess(fmt.Sprintf("Redrive completed: %d messages moved", moved))
				}
			case "FAILED", "CANCELLED":
				s.redriving = false
				s.spinner.Hide()
				status := task.Status
				reason := task.FailureReason
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
		newTier := ui.GetWidthTier(m.Width)
		s.widthTier = newTier

		cols := sqsMessageColumns(newTier, s.isFIFO)
		if !ui.ColumnsFit(cols, m.Width) {
			cols = sqsMessageColumns(ui.TierNarrow, s.isFIFO)
			s.widthTier = ui.TierNarrow
		}
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
			s.loading = true
			s.err = nil
			s.messages = nil
			s.seen = make(map[string]bool)
			s.spinner.Show("Peeking at messages...")
			return s, tea.Batch(s.spinner.Tick(), s.fetchMessages())
		case key.Matches(m, s.keys.LoadMore):
			if s.loading {
				return s, nil
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
			if ui.ReadOnly {
				return s, func() tea.Msg {
					return msg.ToastError("ReadOnly mode — press W to switch")
				}
			}
			handles := s.selectedReceiptHandles()
			if len(handles) == 0 {
				return s, nil
			}
			count := len(handles)
			return s, func() tea.Msg {
				return msg.RequestConfirmMsg{
					Message: fmt.Sprintf("Delete %d message(s)?", count),
					Action:  "sqs_delete_messages",
				}
			}
		case key.Matches(m, s.keys.Manage):
			if ui.ReadOnly {
				return s, func() tea.Msg {
					return msg.ToastError("ReadOnly mode — press W to switch")
				}
			}
			if !s.isDLQ {
				return s, func() tea.Msg {
					return msg.ToastError("Redrive is only available for DLQ queues")
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

func (s *SQSMessages) selectedMessage() *aws.SQSMessage {
	row := s.table.SelectedRow()
	if row == nil || len(s.messages) == 0 {
		return nil
	}
	id := row[0]
	for i := range s.messages {
		if truncateID(s.messages[i].MessageID) == id || s.messages[i].MessageID == id {
			return &s.messages[i]
		}
	}
	return nil
}

func (s *SQSMessages) selectedMessageIDs() []string {
	indices := s.table.SelectedIndices()
	if len(indices) == 0 {
		row := s.table.SelectedRow()
		if row == nil {
			return nil
		}
		m := s.selectedMessage()
		if m != nil {
			return []string{m.MessageID}
		}
		return nil
	}
	ids := make([]string, 0, len(indices))
	for _, idx := range indices {
		if idx < len(s.messages) {
			ids = append(ids, s.messages[idx].MessageID)
		}
	}
	return ids
}

func (s *SQSMessages) selectedReceiptHandles() []string {
	indices := s.table.SelectedIndices()
	if len(indices) == 0 {
		m := s.selectedMessage()
		if m != nil {
			return []string{m.ReceiptHandle}
		}
		return nil
	}
	handles := make([]string, 0, len(indices))
	for _, idx := range indices {
		if idx < len(s.messages) {
			handles = append(handles, s.messages[idx].ReceiptHandle)
		}
	}
	return handles
}

func (s *SQSMessages) deleteSelectedMessages() tea.Cmd {
	handles := s.selectedReceiptHandles()
	if len(handles) == 0 {
		return nil
	}
	svc := s.sqs
	queueURL := s.queueURL
	count := len(handles)

	// Remove from local state
	selected := make(map[string]bool, count)
	for _, h := range handles {
		selected[h] = true
	}
	var remaining []aws.SQSMessage
	for _, m := range s.messages {
		if !selected[m.ReceiptHandle] {
			remaining = append(remaining, m)
		} else {
			delete(s.seen, m.MessageID)
		}
	}
	s.messages = remaining
	s.table.DeselectAll()
	s.rebuildRows()

	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Deleting %d messages from %s", count, queueURL)
		err := svc.DeleteMessageBatch(context.Background(), queueURL, handles)
		return sqsMessageDeletedMsg{count: count, err: err}
	}
}

// ─── Row building ───────────────────────────────────────────────────────────

func (s *SQSMessages) rebuildRows() {
	rows := make([]table.Row, 0, len(s.messages))
	sortKeys := make([]table.Row, 0, len(s.messages))
	for _, m := range s.messages {
		id := truncateID(m.MessageID)
		sent := ""
		sentSortKey := ""
		if !m.SentTimestamp.IsZero() {
			sent = m.SentTimestamp.Format("2006-01-02 15:04:05")
			sentSortKey = sent
		}
		receives := fmt.Sprintf("%d", m.ApproximateReceiveCount)
		bodyPreview := truncateBody(m.Body, 40)

		switch s.widthTier {
		case ui.TierNarrow:
			rows = append(rows, table.Row{id, bodyPreview})
			sortKeys = append(sortKeys, table.Row{m.MessageID, m.Body})
		default:
			if s.isFIFO {
				rows = append(rows, table.Row{id, sent, receives, m.MessageGroupID, bodyPreview})
				sortKeys = append(sortKeys, table.Row{m.MessageID, sentSortKey, receives, m.MessageGroupID, m.Body})
			} else {
				rows = append(rows, table.Row{id, sent, receives, bodyPreview})
				sortKeys = append(sortKeys, table.Row{m.MessageID, sentSortKey, receives, m.Body})
			}
		}
	}
	s.table.SetRowsWithSortKeys(rows, sortKeys)
}

func truncateID(id string) string {
	if len(id) > 16 {
		return id[:16] + "…"
	}
	return id
}

func truncateBody(body string, maxLen int) string {
	// Replace newlines for preview
	body = strings.ReplaceAll(body, "\n", " ")
	body = strings.ReplaceAll(body, "\r", "")
	if len(body) > maxLen {
		return body[:maxLen] + "…"
	}
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

func (s *SQSMessages) View() tea.View {
	t := ui.ActiveTheme
	warnStyle := lipgloss.NewStyle().Foreground(t.StatePending)
	banner := warnStyle.Render("  ⚠ Peek mode (VisibilityTimeout=0) — ReceiveCount is incremented on each fetch")

	var content string
	if s.loading && len(s.messages) == 0 {
		content = "\n  " + s.spinner.View()
	} else if s.err != nil {
		content = "\n  " + ui.ErrorStyle.Render("Error: "+s.err.Error())
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
