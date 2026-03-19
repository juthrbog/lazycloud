package views

import (
	"context"
	"encoding/json"
	"fmt"
	"path"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

type s3VersionsLoadedMsg struct {
	versions []aws.ObjectVersion
}

// S3Versions displays all versions of a specific S3 object.
type S3Versions struct {
	s3       aws.S3Service
	bucket   string
	key      string
	versions []aws.ObjectVersion
	table    ui.Table
	spinner  ui.Spinner
	loading  bool
	err      error
	width    int
	height   int
}

func (s *S3Versions) ID() string {
	return "s3_versions:" + s.bucket + ":" + s.key
}

func (s *S3Versions) Title() string {
	return path.Base(s.key) + " versions"
}

func (s *S3Versions) KeyMap() []ui.KeyHint {
	return []ui.KeyHint{
		{Key: "enter", Desc: "view"},
		{Key: "d", Desc: "describe"},
		{Key: "s/S", Desc: "sort"},
		{Key: "r", Desc: "refresh"},
	}
}

// NewS3Versions creates the version list view for an S3 object.
func NewS3Versions(s3 aws.S3Service, bucket, key string) *S3Versions {
	columns := []table.Column{
		{Title: "Version ID", Width: 36},
		{Title: "Size", Width: 10},
		{Title: "Modified", Width: 20},
		{Title: "Latest", Width: 8},
		{Title: "Delete Marker", Width: 14},
	}

	t := ui.NewTable(columns, nil)
	return &S3Versions{
		s3:      s3,
		bucket:  bucket,
		key:     key,
		table:   t,
		spinner: ui.NewSpinner("Loading versions..."),
		loading: true,
	}
}

func (s *S3Versions) Init() tea.Cmd {
	if !s.loading {
		return nil
	}
	return tea.Batch(s.spinner.Tick(), s.fetchVersions())
}

func (s *S3Versions) fetchVersions() tea.Cmd {
	svc := s.s3
	bucket := s.bucket
	key := s.key
	return func() tea.Msg {
		versions, err := svc.ListObjectVersions(context.Background(), bucket, key)
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: fmt.Sprintf("listing versions of %s", key)}
		}
		eventlog.Infof(eventlog.CatAWS, "Loaded %d versions of s3://%s/%s", len(versions), bucket, key)
		return s3VersionsLoadedMsg{versions: versions}
	}
}

func (s *S3Versions) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case s3VersionsLoadedMsg:
		s.loading = false
		s.spinner.Hide()
		s.versions = m.versions
		var rows []table.Row
		var sortKeys []table.Row
		for _, v := range m.versions {
			latest := ""
			if v.IsLatest {
				latest = "✓"
			}
			delMarker := ""
			if v.IsDeleteMarker {
				delMarker = "✓"
			}
			rows = append(rows, table.Row{
				v.VersionID,
				aws.FormatBytes(v.Size),
				v.LastModified.Format("2006-01-02 15:04:05"),
				latest,
				delMarker,
			})
			sortKeys = append(sortKeys, table.Row{
				v.VersionID,
				ui.SortKeyBytes(v.Size),
				v.LastModified.Format("2006-01-02 15:04:05"),
				latest,
				delMarker,
			})
		}
		s.table.SetRowsWithSortKeys(rows, sortKeys)
		return s, nil

	case ui.PickerResultMsg:
		if m.ID == "sort" {
			if m.Value == "_clear" {
				s.table.ClearSort()
			} else if m.Selected >= 0 {
				s.table.Sort(m.Selected)
			}
		}
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
		return s, nil

	case tea.KeyPressMsg:
		switch m.String() {
		case "esc":
			return s, func() tea.Msg { return msg.NavigateBackMsg{} }
		case "s":
			columns, currentCol := s.table.SortColumnNames()
			return s, func() tea.Msg {
				return msg.RequestSortPickerMsg{Columns: columns, CurrentCol: currentCol}
			}
		case "S":
			s.table.SortReverse()
			return s, nil
		case "r":
			s.loading = true
			s.spinner.Show("Loading versions...")
			return s, tea.Batch(s.spinner.Tick(), s.fetchVersions())
		case "enter", "d":
			idx := s.table.SelectedIndex()
			if idx < 0 || idx >= len(s.versions) {
				return s, nil
			}
			v := s.versions[idx]
			jsonBytes, _ := json.MarshalIndent(v, "", "  ")
			return s, func() tea.Msg {
				return msg.NavigateMsg{
					ViewID: "content",
					Params: map[string]string{
						"title":   fmt.Sprintf("%s (v%s)", path.Base(s.key), v.VersionID[:8]),
						"content": string(jsonBytes),
						"format":  "json",
					},
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

func (s *S3Versions) View() tea.View {
	var content string
	if s.loading {
		content = "\n  " + s.spinner.View()
	} else if s.err != nil {
		content = "\n  " + ui.ErrorStyle.Render("Error: "+s.err.Error())
	} else {
		content = s.table.View()
		content += fmt.Sprintf("\n %d versions  s3://%s/%s", len(s.versions), s.bucket, s.key)
	}
	return tea.NewView(content)
}
