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

type s3BucketsLoadedMsg struct {
	buckets []aws.Bucket
}

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

type s3ListKeyMap struct {
	Esc         key.Binding
	Browse      key.Binding
	Properties  key.Binding
	NewBucket   key.Binding
	Delete      key.Binding
	Select      key.Binding
	Sort        key.Binding
	SortReverse key.Binding
	Filter      key.Binding
	Refresh     key.Binding
}

var defaultS3ListKeyMap = s3ListKeyMap{
	Esc:         key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Browse:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "browse")),
	Properties:  key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "properties")),
	NewBucket:   key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new bucket")),
	Delete:      key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "delete bucket")),
	Select:      key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "select")),
	Sort:        key.NewBinding(key.WithKeys("s"), key.WithHelp("s/S", "sort")),
	SortReverse: key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "reverse sort")),
	Filter:      key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
	Refresh:     key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
}

// S3List displays all S3 buckets.
type S3List struct {
	viewCtx
	keys          s3ListKeyMap
	s3            aws.S3Service
	defaultRegion string
	table         ui.Table
	buckets       []aws.Bucket
	filter        ui.Filter
	createInput   ui.Filter // reuse as text input for new bucket name
	creating      bool
	spinner       ui.Spinner
	loading       bool
	// pendingDelete holds the bucket names of the delete_bucket confirm.
	pendingDelete pendingConfirm
	err           error
	width         int
	height        int
	widthTier     ui.WidthTier
}

func (s *S3List) ID() string    { return "s3_list" }
func (s *S3List) Title() string { return "S3 Buckets" }

// CapturingInput implements nav.InputCapturer.
func (s *S3List) CapturingInput() bool {
	return s.filter.Active() || (s.creating && s.createInput.Active())
}

// s3CreateSource marks the messages of the create input.
const s3CreateSource = "s3-create"

func (s *S3List) KeyMap() []ui.HintBinding {
	hints := []ui.HintBinding{
		{Binding: s.keys.Browse},
		{Binding: s.keys.Properties},
		{Binding: s.keys.NewBucket, Mode: ui.ModeReadWrite},
		{Binding: s.keys.Delete, Mode: ui.ModeReadWrite},
		{Binding: s.keys.Select},
		{Binding: s.keys.Sort},
		{Binding: s.keys.Filter},
		{Binding: s.keys.Refresh},
	}
	ui.ApplyModeAll(hints)
	return hints
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

// NewS3List creates the S3 bucket list view.
func NewS3List(s3 aws.S3Service, defaultRegion string) *S3List {
	t := ui.NewTable(s3BucketColumns(ui.TierMedium))
	return &S3List{
		keys:          defaultS3ListKeyMap,
		s3:            s3,
		defaultRegion: defaultRegion,
		table:         t,
		filter:        ui.NewFilter(),
		spinner:       ui.NewSpinner("Loading S3 buckets..."),
		loading:       true,
		widthTier:     ui.TierMedium,
	}
}

func (s *S3List) Init() tea.Cmd {
	s.table.DeselectAll()
	if !s.loading {
		return nil
	}
	return tea.Batch(s.spinner.Tick(), s.fetchBuckets())
}

func (s *S3List) fetchBuckets() tea.Cmd {
	svc := s.s3
	ctx := s.viewContext()
	return func() tea.Msg {
		if svc == nil {
			return msg.ErrorMsg{Err: fmt.Errorf("AWS client not initialized"), Context: "S3"}
		}
		eventlog.Debugf(eventlog.CatAWS, "Fetching S3 buckets")
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		buckets, err := svc.ListBuckets(ctx)
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: "listing S3 buckets"}
		}
		eventlog.Infof(eventlog.CatAWS, "Loaded %d S3 buckets", len(buckets))
		return s3BucketsLoadedMsg{buckets: buckets}
	}
}

func (s *S3List) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case ui.ConfirmResultMsg:
		buckets, ok := s.pendingDelete.take(m)
		if !ok {
			return s, nil
		}
		// The mode can change while the prompt is open. take has cleared
		// the targets, so nothing runs. The text is the app's refusal for a
		// ReadWrite key in ReadOnly mode.
		if ui.IsReadOnly() {
			return s, func() tea.Msg { return msg.ToastError("ReadOnly mode — press W to switch") }
		}
		return s, s.deleteBuckets(buckets)

	case s3BucketDeletedMsg:
		s.table.DeselectAll()
		for _, b := range m.done {
			eventlog.Infof(eventlog.CatAWS, "Deleted bucket: %s", b)
		}
		toast := m.Outcome()
		// Reload also after a failure: the list must show which buckets are
		// left. A failed bucket can be partly emptied.
		s.loading = true
		s.spinner.Show("Loading S3 buckets...")
		return s, tea.Batch(s.spinner.Tick(), s.fetchBuckets(), func() tea.Msg { return toast })

	case s3BucketCreatedMsg:
		if m.err != nil {
			s.err = m.err
			return s, nil
		}
		eventlog.Infof(eventlog.CatAWS, "Created bucket: %s", m.name)
		s.loading = true
		s.spinner.Show("Loading S3 buckets...")
		name := m.name
		return s, tea.Batch(s.spinner.Tick(), s.fetchBuckets(), func() tea.Msg {
			return msg.ToastSuccess("Bucket created: " + name)
		})

	case s3BucketPropsMsg:
		if m.err != nil {
			s.err = m.err
			return s, nil
		}
		jsonBytes, _ := json.MarshalIndent(m.props, "", "  ")
		return s, func() tea.Msg {
			return msg.NavigateMsg{
				ViewID: "content",
				Params: map[string]string{
					"title":   m.props.Name + " (properties)",
					"content": string(jsonBytes),
					"format":  "json",
				},
			}
		}

	case ui.PickerResultMsg:
		if m.ID == "sort" {
			if m.Value == "_clear" {
				s.table.ClearSort()
			} else if m.Selected >= 0 {
				s.table.Sort(m.Selected)
			}
		}
		return s, nil

	case s3BucketsLoadedMsg:
		s.loading = false
		s.spinner.Hide()
		s.buckets = m.buckets
		s.rebuildRows()
		return s, nil

	case msg.ErrorMsg:
		s.loading = false
		s.spinner.Hide()
		s.err = m.Err
		return s, nil

	case tea.WindowSizeMsg:
		s.width = m.Width
		s.height = m.Height
		cols, newTier := ui.BestFitTier(m.Width, s3BucketColumns)
		s.widthTier = newTier
		if len(cols) != len(s.table.Columns()) {
			s.table.SetColumns(cols)
			s.rebuildRows()
		}
		s.table.SetSize(m.Width, m.Height-3)
		s.filter.SetWidth(m.Width)
		return s, nil

	case ui.FilterChangedMsg:
		// The create input sends these too. Its text is a bucket name, not a
		// filter for the table.
		if m.Source == "" {
			s.table.Filter(m.Text)
		}
		return s, nil

	case tea.KeyPressMsg:
		if s.filter.Active() {
			var cmd tea.Cmd
			s.filter, cmd = s.filter.Update(m)
			return s, cmd
		}

		// Create bucket input
		if s.creating && s.createInput.Active() {
			switch m.String() {
			case "enter":
				name := s.createInput.Value()
				s.createInput.Deactivate()
				s.creating = false
				if name != "" {
					return s, s.createBucket(name)
				}
				return s, nil
			case "esc":
				s.createInput.Deactivate()
				s.creating = false
				return s, nil
			default:
				var cmd tea.Cmd
				s.createInput, cmd = s.createInput.Update(m)
				return s, cmd
			}
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
			s.spinner.Show("Loading S3 buckets...")
			return s, tea.Batch(s.spinner.Tick(), s.fetchBuckets())
		case key.Matches(m, s.keys.Browse):
			if bucket := s.table.CursorKey(); bucket != "" {
				return s, func() tea.Msg {
					return msg.NavigateMsg{
						ViewID: "s3_objects",
						Params: map[string]string{"bucket": bucket, "prefix": ""},
					}
				}
			}
		case key.Matches(m, s.keys.Properties):
			bucket := s.table.CursorKey()
			if bucket == "" {
				return s, nil
			}
			return s, s.fetchBucketProperties(bucket)
		case key.Matches(m, s.keys.NewBucket):
			s.creating = true
			s.createInput = ui.NewFilterWithPrompt("new bucket", "bucket-name").WithSource(s3CreateSource)
			s.createInput.SetWidth(s.width)
			s.createInput.Activate()
			return s, nil
		case key.Matches(m, s.keys.Select):
			s.table.ToggleSelect()
			return s, nil
		case key.Matches(m, s.keys.Delete):
			names := s.selectedBucketNames()
			if len(names) == 0 {
				return s, nil
			}
			s.pendingDelete.hold(names)
			message, phrase := deleteBucketsPrompt(names)
			return s, s.pendingDelete.ask(msg.RequestConfirmMsg{
				Message: message,
				Action:  "delete_bucket",
				Phrase:  phrase,
			})
		}
	}

	if s.loading {
		var cmd tea.Cmd
		s.spinner, cmd = s.spinner.Update(m)
		return s, cmd
	}

	var cmd tea.Cmd
	s.table, cmd = s.table.Update(m)
	return s, cmd
}

func (s *S3List) fetchBucketProperties(bucket string) tea.Cmd {
	svc := s.s3
	ctx := s.viewContext()
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Fetching properties for bucket: %s", bucket)
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		props, err := svc.GetBucketProperties(ctx, bucket)
		return s3BucketPropsMsg{props: props, err: err}
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

// selectedBucketNames returns the names of the visible selected buckets, or
// the cursor bucket name if none is selected. A selected bucket that the
// filter hides is not included.
func (s *S3List) selectedBucketNames() []string {
	return s.table.TargetKeys()
}

// deleteBucketsPrompt returns the confirm message for deleting buckets and
// the phrase the user must type: the bucket name for one bucket, or
// "delete N buckets" for several.
func deleteBucketsPrompt(names []string) (message, phrase string) {
	const warning = "Every object version and delete marker is deleted\n" +
		"permanently, then the bucket. You cannot undo this."
	if len(names) == 1 {
		return fmt.Sprintf("Delete bucket '%s'?\n%s", names[0], warning), names[0]
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

func (s *S3List) rebuildRows() {
	narrow := s.widthTier == ui.TierNarrow
	rows := make([]ui.Row, 0, len(s.buckets))
	for _, b := range s.buckets {
		cells := table.Row{b.Name, b.CreationDate.Format("2006-01-02 15:04:05")}
		if narrow {
			cells = table.Row{b.Name}
		}
		rows = append(rows, ui.Row{Key: b.Name, Cells: cells})
	}
	s.table.SetRows(rows)
}

func (s *S3List) Footer() string {
	filtered, total := s.table.RowCount()
	footer := fmt.Sprintf("%d/%d buckets", filtered, total)
	if sel := s.table.SelectionCount(); sel > 0 {
		footer += fmt.Sprintf("  (%d selected)", sel)
	}
	return footer
}

func (s *S3List) View() tea.View {
	var content string
	switch {
	case s.loading:
		content = "\n  " + s.spinner.View()
	case s.err != nil:
		content = "\n  " + ui.S.Error.Render("Error: "+ui.Sanitize(aws.ErrorText(s.err)))
	default:
		content = s.table.View()
		if s.filter.Active() {
			content = s.filter.View() + "\n" + content
		}
	}

	if s.creating && s.createInput.Active() {
		content += "\n" + s.createInput.View()
	}

	return tea.NewView(content)
}
