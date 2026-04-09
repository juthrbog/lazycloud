package views

import (
	"context"
	"encoding/json"
	"fmt"
	"path"

	"charm.land/bubbles/v2/key"
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

type s3VersionsKeyMap struct {
	Esc         key.Binding
	View        key.Binding
	Describe    key.Binding
	Sort        key.Binding
	SortReverse key.Binding
	Refresh     key.Binding
}

var defaultS3VersionsKeyMap = s3VersionsKeyMap{
	Esc:         key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	View:        key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "view")),
	Describe:    key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "describe")),
	Sort:        key.NewBinding(key.WithKeys("s"), key.WithHelp("s/S", "sort")),
	SortReverse: key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "reverse sort")),
	Refresh:     key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
}

// S3Versions displays all versions of a specific S3 object.
type S3Versions struct {
	keys     s3VersionsKeyMap
	s3       aws.S3Service
	bucket   string
	key      string
	versions []aws.ObjectVersion
	table    ui.Table
	spinner  ui.Spinner
	loading  bool
	err       error
	width     int
	height    int
	widthTier ui.WidthTier
}

func (s *S3Versions) ID() string {
	return "s3_versions:" + s.bucket + ":" + s.key
}

func (s *S3Versions) Title() string {
	return path.Base(s.key) + " versions"
}

func (s *S3Versions) KeyMap() []ui.HintBinding {
	return []ui.HintBinding{
		{Binding: s.keys.View},
		{Binding: s.keys.Describe},
		{Binding: s.keys.Sort},
		{Binding: s.keys.Refresh},
	}
}

func s3VersionColumns(tier ui.WidthTier) []ui.Column {
	if tier == ui.TierNarrow {
		return []ui.Column{
			{Title: "Version ID", Width: 36, Weight: 1, MaxWidth: 50},
			{Title: "Size", Width: 10},
			{Title: "Latest", Width: 8},
		}
	}
	return []ui.Column{
		{Title: "Version ID", Width: 36, Weight: 1, MaxWidth: 50},
		{Title: "Size", Width: 10},
		{Title: "Modified", Width: 20, Weight: 1, MaxWidth: 25},
		{Title: "Latest", Width: 8},
		{Title: "Delete Marker", Width: 14},
	}
}

// NewS3Versions creates the version list view for an S3 object.
func NewS3Versions(s3 aws.S3Service, bucket, key string) *S3Versions {
	t := ui.NewTable(s3VersionColumns(ui.TierMedium), nil)
	return &S3Versions{
		keys:      defaultS3VersionsKeyMap,
		s3:        s3,
		bucket:    bucket,
		key:       key,
		table:     t,
		spinner:   ui.NewSpinner("Loading versions..."),
		widthTier: ui.TierMedium,
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
		eventlog.Debugf(eventlog.CatAWS, "Fetching versions for s3://%s/%s", bucket, key)
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
		s.rebuildRows()
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
		cols, newTier := ui.BestFitTier(m.Width, s3VersionColumns)
		s.widthTier = newTier
		if len(cols) != len(s.table.Columns()) {
			s.table.SetColumns(cols)
			s.rebuildRows()
		}
		s.table.SetSize(m.Width, m.Height-3)
		return s, nil

	case tea.KeyPressMsg:
		switch {
		case key.Matches(m, s.keys.Esc):
			return s, func() tea.Msg { return msg.NavigateBackMsg{} }
		case key.Matches(m, s.keys.Sort):
			columns, currentCol := s.table.SortColumnNames()
			return s, func() tea.Msg {
				return msg.RequestSortPickerMsg{Columns: columns, CurrentCol: currentCol}
			}
		case key.Matches(m, s.keys.SortReverse):
			s.table.SortReverse()
			return s, nil
		case key.Matches(m, s.keys.Refresh):
			s.loading = true
			s.spinner.Show("Loading versions...")
			return s, tea.Batch(s.spinner.Tick(), s.fetchVersions())
		case key.Matches(m, s.keys.View, s.keys.Describe):
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

func (s *S3Versions) rebuildRows() {
	narrow := s.widthTier == ui.TierNarrow
	var rows []table.Row
	var sortKeys []table.Row
	for _, v := range s.versions {
		latest := ""
		if v.IsLatest {
			latest = "✓"
		}
		if narrow {
			rows = append(rows, table.Row{v.VersionID, aws.FormatBytes(v.Size), latest})
			sortKeys = append(sortKeys, table.Row{v.VersionID, ui.SortKeyBytes(v.Size), latest})
		} else {
			delMarker := ""
			if v.IsDeleteMarker {
				delMarker = "✓"
			}
			rows = append(rows, table.Row{
				v.VersionID, aws.FormatBytes(v.Size),
				v.LastModified.Format("2006-01-02 15:04:05"), latest, delMarker,
			})
			sortKeys = append(sortKeys, table.Row{
				v.VersionID, ui.SortKeyBytes(v.Size),
				v.LastModified.Format("2006-01-02 15:04:05"), latest, delMarker,
			})
		}
	}
	s.table.SetRowsWithSortKeys(rows, sortKeys)
}

func (s *S3Versions) Footer() string {
	return fmt.Sprintf("%d versions  s3://%s/%s", len(s.versions), s.bucket, s.key)
}

func (s *S3Versions) View() tea.View {
	var content string
	if s.loading {
		content = "\n  " + s.spinner.View()
	} else if s.err != nil {
		content = "\n  " + ui.ErrorStyle.Render("Error: "+s.err.Error())
	} else {
		content = s.table.View()
	}
	return tea.NewView(content)
}
