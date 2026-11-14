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
	"github.com/juthrbog/lazycloud/internal/ops"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// s3BucketDeletedMsg reports a delete of several buckets. Each bucket is
// tried, also after an earlier one fails.
type s3BucketDeletedMsg struct {
	done     []string // names of the deleted buckets
	failed   []itemFailure
	canceled int // buckets not deleted because the user cancelled or the app quits
}

// Outcome implements msg.Outcome.
func (m s3BucketDeletedMsg) Outcome() msg.ToastMsg {
	total := len(m.done) + len(m.failed) + m.canceled
	if m.canceled > 0 {
		text := fmt.Sprintf("Bucket delete cancelled: deleted %d of %d bucket(s)", len(m.done), total)
		if len(m.failed) > 0 {
			return msg.ToastError(text + ". Failed: " + failureText(m.failed))
		}
		return msg.ToastInfo(text)
	}
	text, ok := resultText("Deleted", total, "bucket(s)", m.failed)
	if ok {
		return msg.ToastSuccess(text)
	}
	return msg.ToastError(text)
}

type s3BucketCreatedMsg struct {
	name string
	err  error
}

// Outcome implements msg.Outcome.
func (m s3BucketCreatedMsg) Outcome() msg.ToastMsg {
	if m.err != nil {
		return msg.ToastError("Bucket " + m.name + " not created: " + aws.ErrorText(m.err))
	}
	return msg.ToastSuccess("Bucket created: " + m.name)
}

type s3BucketPropsMsg struct {
	props *aws.BucketProperties
	err   error
}

// s3Keys are the keys of the S3 list. The core opens no detail for this
// list (OpenDetail is nil), so the view handles enter and d itself.
var s3Keys = struct {
	Browse     ui.HintBinding
	Properties ui.HintBinding
	NewBucket  ui.HintBinding
	Delete     ui.HintBinding
}{
	Browse:     ui.NewHintBinding([]string{"enter"}, "enter", "browse"),
	Properties: ui.NewHintBinding([]string{"d"}, "d", "properties"),
	NewBucket:  ui.HintBinding{Binding: key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new bucket")), Mode: ui.ModeReadWrite},
	Delete:     ui.HintBinding{Binding: key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "delete bucket")), Mode: ui.ModeReadWrite},
}

// s3CreateSource marks the messages of the create input.
const s3CreateSource = "s3-create"

// S3List displays all S3 buckets.
type S3List struct {
	listCore[aws.Bucket]
	s3            aws.S3Service
	defaultRegion string
	createInput   ui.Filter // the name of a new bucket; shown below every screen
	// pendingDelete holds the bucket names of the delete_bucket confirm.
	pendingDelete pendingConfirm
}

// NewS3List creates the S3 bucket list view.
func NewS3List(s3 aws.S3Service, defaultRegion string) *S3List {
	s := &S3List{s3: s3, defaultRegion: defaultRegion}
	s.listCore = newListCore(listConfig[aws.Bucket]{
		Noun:         "buckets",
		LoadingLabel: "Loading S3 buckets...",
		ErrContext:   "listing S3 buckets",
		Key:          func(b aws.Bucket) string { return b.Name },
		Columns:      s3BucketColumns,
		Row:          s3BucketRow,
		Fetch:        fetchAll(s3, aws.S3Service.ListBuckets),
		// ListBuckets reads every page in the aws layer, so it gets the
		// limit of a whole-list call.
		FetchTimeout: listTimeout,
		MultiSelect:  true,
	})
	return s
}

func (s *S3List) ID() string     { return "s3_list" }
func (s *S3List) Title() string  { return "S3 Buckets" }
func (s *S3List) Init() tea.Cmd  { return s.init() }
func (s *S3List) Footer() string { return s.footer() }

// CapturingInput implements nav.InputCapturer: true while the filter or the
// create input is open.
func (s *S3List) CapturingInput() bool {
	return s.filter.Active() || s.createInput.Active()
}

func (s *S3List) KeyMap() []ui.HintBinding {
	return s.hints(s3Keys.Browse, s3Keys.Properties, s3Keys.NewBucket, s3Keys.Delete,
		listKeys.Select, listKeys.Sort, listKeys.Filter, listKeys.Refresh)
}

func (s *S3List) View() tea.View {
	below := ""
	if s.createInput.Active() {
		below = s.createInput.View()
	}
	return tea.NewView(s.render("", below))
}

func s3BucketColumns(tier ui.WidthTier) []ui.Column {
	if tier == ui.TierNarrow {
		return []ui.Column{
			{Title: "Bucket", Width: 40, Weight: 2, MaxWidth: 80},
		}
	}
	return []ui.Column{
		{Title: "Bucket", Width: 40, Weight: 2, MaxWidth: 80},
		{Title: "Created", Width: 22, Weight: 1, MaxWidth: 30},
	}
}

func s3BucketRow(b aws.Bucket, tier ui.WidthTier) ui.Row {
	if tier == ui.TierNarrow {
		return ui.Row{Key: b.Name, Cells: table.Row{b.Name}}
	}
	return ui.Row{Key: b.Name, Cells: table.Row{b.Name, b.CreationDate.Format("2006-01-02 15:04:05")}}
}

// Update handles the create input, browse, properties, delete and their
// replies, and hands the rest to the core. It returns the view itself, so
// the navigator keeps its identity.
func (s *S3List) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case ui.ConfirmResultMsg:
		return s, s.onConfirm(m)
	case s3BucketDeletedMsg:
		return s, s.onDeleted(m)
	case s3BucketCreatedMsg:
		return s, s.onCreated(m)
	case s3BucketPropsMsg:
		return s, s.onProps(m)
	case tea.WindowSizeMsg:
		s.createInput.SetWidth(m.Width) // the core resizes the rest
	case tea.KeyPressMsg:
		if cmd, ok := s.viewKey(m); ok {
			return s, cmd
		}
	}
	return s, s.update(m)
}

// viewKey handles the keys of the view. It returns false for a key that
// goes to the core.
func (s *S3List) viewKey(m tea.KeyPressMsg) (tea.Cmd, bool) {
	if s.filter.Active() {
		return nil, false
	}
	if s.createInput.Active() {
		return s.createKey(m), true
	}
	if key.Matches(m, s3Keys.NewBucket.Binding) {
		// Also on the error screen: a new bucket does not need the list.
		s.createInput = ui.NewFilterWithPrompt("new bucket", "bucket-name").WithSource(s3CreateSource)
		s.createInput.SetWidth(s.width)
		s.createInput.Activate()
		return nil, true
	}
	if s.err != nil {
		// The error screen hides the rows, so enter, d and x must not act
		// on them.
		return nil, false
	}
	switch {
	case key.Matches(m, s3Keys.Browse.Binding):
		bucket := s.table.CursorKey()
		if bucket == "" {
			return nil, false
		}
		return func() tea.Msg {
			return msg.NavigateMsg{
				ViewID: "s3_objects",
				Params: map[string]string{"bucket": bucket, "prefix": ""},
			}
		}, true
	case key.Matches(m, s3Keys.Properties.Binding):
		bucket := s.table.CursorKey()
		if bucket == "" {
			return nil, true
		}
		return s.fetchBucketProperties(bucket), true
	case key.Matches(m, s3Keys.Delete.Binding):
		return s.askDelete(), true
	}
	return nil, false
}

// createKey handles a key while the create input is open. esc closes it;
// enter closes it and creates the bucket, unless the name is empty.
func (s *S3List) createKey(m tea.KeyPressMsg) tea.Cmd {
	switch m.String() {
	case "enter":
		name := s.createInput.Value()
		s.createInput.Deactivate()
		if name == "" {
			return nil
		}
		return s.createBucket(name)
	case "esc":
		s.createInput.Deactivate()
		return nil
	}
	var cmd tea.Cmd
	s.createInput, cmd = s.createInput.Update(m)
	return cmd
}

func (s *S3List) fetchBucketProperties(bucket string) tea.Cmd {
	svc := s.s3
	ctx := s.viewContext()
	return func() tea.Msg {
		if svc == nil {
			return s3BucketPropsMsg{err: errNoClient}
		}
		eventlog.Infof(eventlog.CatAWS, "Fetching properties for bucket: %s", bucket)
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		props, err := svc.GetBucketProperties(ctx, bucket)
		if err == nil && props == nil {
			err = fmt.Errorf("no properties for bucket %s", bucket)
		}
		return s3BucketPropsMsg{props: props, err: err}
	}
}

// onProps opens the properties in the tabbed panel, with one JSON tab. An
// error gives a toast and the list stays.
func (s *S3List) onProps(m s3BucketPropsMsg) tea.Cmd {
	if m.err != nil {
		eventlog.Errorf(eventlog.CatAWS, "Bucket properties failed: %v", m.err)
		return detailErrorToast(m.err)
	}
	content, _ := json.MarshalIndent(m.props, "", "  ")
	title := m.props.Name + " (properties)"
	tabs := []msg.TabContent{{Title: "JSON", Content: string(content), Format: "json"}}
	return func() tea.Msg {
		return msg.TabbedContentMsg{PanelTitle: title, Tabs: tabs}
	}
}

func (s *S3List) createBucket(name string) tea.Cmd {
	svc := s.s3
	region := s.defaultRegion
	if region == "" {
		region = "us-east-1"
	}
	ctx := s.viewContext()
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Creating bucket: %s in %s", name, region)
		ctx, cancel := detached(ctx)
		defer cancel()
		err := svc.CreateBucket(ctx, name, region)
		return s3BucketCreatedMsg{name: name, err: err}
	}
}

// onCreated shows the result. After a good create the list reloads, and
// the table keeps the selection by name.
func (s *S3List) onCreated(m s3BucketCreatedMsg) tea.Cmd {
	toast := m.Outcome()
	if m.err != nil {
		eventlog.Errorf(eventlog.CatAWS, "Create bucket %s failed: %v", m.name, m.err)
		return func() tea.Msg { return toast }
	}
	eventlog.Infof(eventlog.CatAWS, "Created bucket: %s", m.name)
	return tea.Batch(s.reload(s.cfg.LoadingLabel), func() tea.Msg { return toast })
}

// askDelete asks to confirm a delete of the targets.
func (s *S3List) askDelete() tea.Cmd {
	names := s.targets()
	if len(names) == 0 {
		return nil
	}
	s.pendingDelete.hold(names)
	message, phrase := deleteBucketsPrompt(names)
	return s.pendingDelete.ask(msg.RequestConfirmMsg{
		Message: message,
		Action:  "delete_bucket",
		Phrase:  phrase,
	})
}

// onConfirm starts a confirmed delete.
func (s *S3List) onConfirm(m ui.ConfirmResultMsg) tea.Cmd {
	buckets, ok := s.pendingDelete.take(m)
	if !ok {
		return nil
	}
	// The mode can change while the prompt is open. take has cleared the
	// targets, so nothing runs. The text is the app's refusal for a
	// ReadWrite key in ReadOnly mode.
	if ui.IsReadOnly() {
		return func() tea.Msg { return msg.ToastError("ReadOnly mode — press W to switch") }
	}
	return s.deleteBuckets(buckets)
}

// onDeleted deselects, shows the result and reloads. It reloads also after
// a failure: the list must show which buckets are left, and a failed bucket
// can be partly emptied.
func (s *S3List) onDeleted(m s3BucketDeletedMsg) tea.Cmd {
	s.table.DeselectAll()
	for _, b := range m.done {
		eventlog.Infof(eventlog.CatAWS, "Deleted bucket: %s", b)
	}
	toast := m.Outcome()
	return tea.Batch(s.reload(s.cfg.LoadingLabel), func() tea.Msg { return toast })
}

// deleteBucketsPrompt returns the confirm message for deleting buckets and
// the phrase the user must type: the bucket name for one bucket, or
// "delete N buckets" for several.
func deleteBucketsPrompt(names []string) (message, phrase string) {
	const warning = "Every object version and delete marker is deleted\n" +
		"permanently, then the bucket. You cannot undo this."
	if len(names) == 1 {
		return fmt.Sprintf("Delete bucket '%s'?\n%s", ui.Sanitize(names[0]), warning), names[0]
	}
	message = fmt.Sprintf("Delete %d buckets?\n%s%s", len(names), promptNameList(names), warning)
	return message, fmt.Sprintf("delete %d buckets", len(names))
}

// deleteBuckets empties and deletes the buckets in a background operation.
// It can take long, so it keeps running when the user leaves the view.
func (s *S3List) deleteBuckets(buckets []string) tea.Cmd {
	svc := s.s3
	label := "Delete bucket " + buckets[0]
	if len(buckets) > 1 {
		label = fmt.Sprintf("Delete %d buckets", len(buckets))
	}
	return func() tea.Msg {
		return msg.StartOpMsg{Label: label, Run: func(ctx context.Context) tea.Msg {
			var res s3BucketDeletedMsg
			for i, bucket := range buckets {
				ops.Report(ctx, ops.Progress{Done: int64(i), Total: int64(len(buckets))})
				if ctx.Err() != nil {
					res.canceled++
					continue
				}
				eventlog.Infof(eventlog.CatAWS, "Emptying and deleting bucket: %s", bucket)
				if err := svc.EmptyAndDeleteBucket(ctx, bucket); err != nil {
					if canceled(err) {
						// The bucket can be partly emptied; the list reloads.
						eventlog.Infof(eventlog.CatAWS, "Delete of bucket %s cancelled", bucket)
						res.canceled++
						continue
					}
					eventlog.Errorf(eventlog.CatAWS, "Failed to delete bucket %s: %v", bucket, err)
					res.failed = append(res.failed, itemFailure{name: bucket, err: err})
					continue
				}
				res.done = append(res.done, bucket)
			}
			return res
		}}
	}
}
