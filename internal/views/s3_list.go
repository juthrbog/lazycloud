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

// S3List displays all S3 buckets.
type S3List struct {
	client  *aws.Client
	table   ui.Table
	buckets []aws.Bucket
	filter  ui.Filter
	spinner ui.Spinner
	loading bool
	err     error
	width   int
	height  int
}

func (s *S3List) ID() string    { return "s3_list" }
func (s *S3List) Title() string { return "S3 Buckets" }
func (s *S3List) KeyMap() []ui.KeyHint {
	return []ui.KeyHint{
		{Key: "enter", Desc: "browse"},
		{Key: "d", Desc: "describe"},
		{Key: "/", Desc: "filter"},
		{Key: "r", Desc: "refresh"},
	}
}

// NewS3List creates the S3 bucket list view.
func NewS3List(client *aws.Client) *S3List {
	columns := []table.Column{
		{Title: "Bucket", Width: 40},
		{Title: "Created", Width: 22},
	}

	t := ui.NewTable(columns, nil)
	return &S3List{
		client:  client,
		table:   t,
		filter:  ui.NewFilter(),
		spinner: ui.NewSpinner("Loading S3 buckets..."),
		loading: true,
	}
}

func (s *S3List) Init() tea.Cmd {
	return tea.Batch(s.spinner.Tick(), s.fetchBuckets())
}

func (s *S3List) fetchBuckets() tea.Cmd {
	client := s.client
	return func() tea.Msg {
		if client == nil {
			return msg.ErrorMsg{Err: fmt.Errorf("AWS client not initialized"), Context: "S3"}
		}
		buckets, err := aws.ListBuckets(context.Background(), client)
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: "listing S3 buckets"}
		}
		eventlog.Infof(eventlog.CatAWS, "Loaded %d S3 buckets", len(buckets))
		return s3BucketsLoadedMsg{buckets: buckets}
	}
}

func (s *S3List) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
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
		s.table.Filter(m.Text)
		return s, nil

	case tea.KeyPressMsg:
		if s.filter.Active() {
			var cmd tea.Cmd
			s.filter, cmd = s.filter.Update(m)
			return s, cmd
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
			for _, b := range s.buckets {
				if b.Name == selected[0] {
					jsonBytes, _ := json.MarshalIndent(b, "", "  ")
					return s, func() tea.Msg {
						return msg.NavigateMsg{
							ViewID: "content",
							Params: map[string]string{
								"title":   b.Name,
								"content": string(jsonBytes),
								"format":  "json",
							},
						}
					}
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
	return tea.NewView(content)
}
