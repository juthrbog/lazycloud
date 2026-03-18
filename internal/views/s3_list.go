package views

import (
	"context"
	"encoding/json"
	"fmt"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

type s3BucketsLoadedMsg struct {
	buckets []aws.Bucket
}

type s3BucketDeletedMsg struct {
	bucket string
	err    error
}

type s3BucketCreatedMsg struct {
	name string
	err  error
}

type s3BucketPropsMsg struct {
	props *aws.BucketProperties
	err   error
}

// S3List displays all S3 buckets.
type S3List struct {
	s3                  aws.S3Service
	defaultRegion       string
	table               ui.Table
	buckets             []aws.Bucket
	filter              ui.Filter
	createInput         ui.Filter // reuse as text input for new bucket name
	creating            bool
	spinner             ui.Spinner
	loading             bool
	pendingDeleteBucket string
	err                 error
	width               int
	height              int
}

func (s *S3List) ID() string    { return "s3_list" }
func (s *S3List) Title() string { return "S3 Buckets" }
func (s *S3List) KeyMap() []ui.KeyHint {
	hints := []ui.KeyHint{
		{Key: "enter", Desc: "browse"},
		{Key: "d", Desc: "properties"},
	}
	if !ui.ReadOnly {
		hints = append(hints,
			ui.KeyHint{Key: "n", Desc: "new bucket"},
			ui.KeyHint{Key: "x", Desc: "delete bucket"},
		)
	}
	hints = append(hints,
		ui.KeyHint{Key: "/", Desc: "filter"},
		ui.KeyHint{Key: "r", Desc: "refresh"},
	)
	return hints
}

// NewS3List creates the S3 bucket list view.
func NewS3List(s3 aws.S3Service, defaultRegion string) *S3List {
	columns := []table.Column{
		{Title: "Bucket", Width: 40},
		{Title: "Created", Width: 22},
	}

	t := ui.NewTable(columns, nil)
	return &S3List{
		s3:            s3,
		defaultRegion: defaultRegion,
		table:         t,
		filter:        ui.NewFilter(),
		spinner:       ui.NewSpinner("Loading S3 buckets..."),
		loading:       true,
	}
}

func (s *S3List) Init() tea.Cmd {
	if !s.loading {
		return nil
	}
	return tea.Batch(s.spinner.Tick(), s.fetchBuckets())
}

func (s *S3List) fetchBuckets() tea.Cmd {
	svc := s.s3
	return func() tea.Msg {
		if svc == nil {
			return msg.ErrorMsg{Err: fmt.Errorf("AWS client not initialized"), Context: "S3"}
		}
		buckets, err := svc.ListBuckets(context.Background())
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
		if m.Confirmed && m.Action == "delete_bucket" && s.pendingDeleteBucket != "" {
			bucket := s.pendingDeleteBucket
			s.pendingDeleteBucket = ""
			return s, s.deleteBucket(bucket)
		}
		s.pendingDeleteBucket = ""
		return s, nil

	case s3BucketDeletedMsg:
		if m.err != nil {
			s.err = m.err
			return s, nil
		}
		eventlog.Infof(eventlog.CatAWS, "Deleted bucket: %s", m.bucket)
		s.loading = true
		s.spinner.Show("Loading S3 buckets...")
		bucket := m.bucket
		return s, tea.Batch(s.spinner.Tick(), s.fetchBuckets(), func() tea.Msg {
			return msg.ToastSuccess("Bucket deleted: " + bucket)
		})

	case s3BucketCreatedMsg:
		if m.err != nil {
			s.err = m.err
			return s, nil
		}
		eventlog.Infof(eventlog.CatAWS, "Created bucket: %s", m.name)
		s.table.Filter("") // clear any filter from the create input
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

	case s3BucketsLoadedMsg:
		s.loading = false
		s.spinner.Hide()
		s.buckets = m.buckets
		var rows []table.Row
		for _, b := range m.buckets {
			rows = append(rows, table.Row{
				b.Name,
				b.CreationDate.Format("2006-01-02 15:04:05"),
			})
		}
		s.table.SetRows(rows)
		return s, nil

	case msg.ErrorMsg:
		s.loading = false
		s.spinner.Hide()
		s.err = m.Err
		return s, nil

	case tea.WindowSizeMsg:
		s.width = m.Width
		s.height = m.Height
		s.table.SetSize(m.Width, m.Height-3)
		s.filter.SetWidth(m.Width)
		return s, nil

	case ui.FilterChangedMsg:
		// Ignore filter messages from the create input
		if !s.creating {
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

		switch m.String() {
		case "esc":
			return s, func() tea.Msg { return msg.NavigateBackMsg{} }
		case "/":
			s.filter.Activate()
			return s, nil
		case "r":
			s.loading = true
			s.err = nil
			s.spinner.Show("Loading S3 buckets...")
			return s, tea.Batch(s.spinner.Tick(), s.fetchBuckets())
		case "enter":
			selected := s.table.SelectedRow()
			if selected != nil {
				bucket := selected[0]
				return s, func() tea.Msg {
					return msg.NavigateMsg{
						ViewID: "s3_objects",
						Params: map[string]string{"bucket": bucket, "prefix": ""},
					}
				}
			}
		case "d":
			selected := s.table.SelectedRow()
			if selected == nil {
				return s, nil
			}
			bucket := selected[0]
			return s, s.fetchBucketProperties(bucket)
		case "n":
			if ui.ReadOnly {
				return s, func() tea.Msg {
					return msg.ToastError("ReadOnly mode — press W to switch")
				}
			}
			s.creating = true
			s.createInput = ui.NewFilter()
			s.createInput.Activate()
			return s, nil
		case "x":
			if ui.ReadOnly {
				return s, func() tea.Msg {
					return msg.ToastError("ReadOnly mode — press W to switch")
				}
			}
			selected := s.table.SelectedRow()
			if selected == nil {
				return s, nil
			}
			s.pendingDeleteBucket = selected[0]
			bucket := selected[0]
			return s, func() tea.Msg {
				return msg.RequestConfirmMsg{
					Message: fmt.Sprintf("Delete bucket '%s'?\nThis will delete all objects first.", bucket),
					Action:  "delete_bucket",
				}
			}
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
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Fetching properties for bucket: %s", bucket)
		props, err := svc.GetBucketProperties(context.Background(), bucket)
		return s3BucketPropsMsg{props: props, err: err}
	}
}

func (s *S3List) createBucket(name string) tea.Cmd {
	svc := s.s3
	region := s.defaultRegion
	if region == "" {
		region = "us-east-1"
	}
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Creating bucket: %s in %s", name, region)
		err := svc.CreateBucket(context.Background(), name, region)
		return s3BucketCreatedMsg{name: name, err: err}
	}
}

func (s *S3List) deleteBucket(bucket string) tea.Cmd {
	svc := s.s3
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Emptying and deleting bucket: %s", bucket)
		err := svc.EmptyAndDeleteBucket(context.Background(), bucket)
		return s3BucketDeletedMsg{bucket: bucket, err: err}
	}
}

func (s *S3List) View() tea.View {
	var content string
	if s.loading {
		content = "\n  " + s.spinner.View()
	} else if s.err != nil {
		content = "\n  " + ui.ErrorStyle.Render("Error: "+s.err.Error())
	} else {
		content = s.table.View()
		if s.filter.Active() {
			content = s.filter.View() + "\n" + content
		}
		filtered, total := s.table.RowCount()
		content += fmt.Sprintf("\n %d/%d buckets", filtered, total)
	}

	if s.creating && s.createInput.Active() {
		content += "\n New bucket name: " + s.createInput.View()
	}

	return tea.NewView(content)
}
