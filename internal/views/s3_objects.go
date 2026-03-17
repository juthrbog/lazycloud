package views

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// s3PageLoadedMsg delivers one page of results for progressive loading.
type s3PageLoadedMsg struct {
	objects      []aws.S3Object
	prefixes     []string
	hasMorePages bool
	token        *string
	pageNum      int
}

type s3PreviewCheckMsg struct {
	meta *aws.ObjectMeta
}

type s3ObjectContentMsg struct {
	content string
	key     string
}

type s3PresignedURLMsg struct {
	url string
	key string
}

// s3TableEntry tracks whether a table row is a folder or object.
type s3TableEntry struct {
	isFolder bool
	fullPath string
}

// S3Objects displays objects and folders within an S3 bucket.
type S3Objects struct {
	client   *aws.Client
	bucket   string
	prefix   string
	objects  []aws.S3Object
	prefixes []string
	entries  []s3TableEntry
	table    ui.Table
	filter   ui.Filter
	spinner  ui.Spinner
	loading  bool // true while any pages remain
	pageNum  int  // pages loaded so far
	err      error
	width    int
	height   int
}

func (s *S3Objects) ID() string {
	return "s3_objects:" + s.bucket + ":" + s.prefix
}

func (s *S3Objects) KeyMap() []ui.KeyHint {
	return []ui.KeyHint{
		{Key: "enter", Desc: "view"},
		{Key: "d", Desc: "describe"},
		{Key: "u", Desc: "presign URL"},
		{Key: "y", Desc: "copy path"},
		{Key: "/", Desc: "filter"},
		{Key: "r", Desc: "refresh"},
	}
}

func (s *S3Objects) Title() string {
	if s.prefix == "" {
		return s.bucket
	}
	trimmed := strings.TrimSuffix(s.prefix, "/")
	return path.Base(trimmed) + "/"
}

// NewS3Objects creates the S3 object browser view.
func NewS3Objects(client *aws.Client, bucket, prefix string) *S3Objects {
	columns := []table.Column{
		{Title: "", Width: 3},
		{Title: "Name", Width: 40},
		{Title: "Size", Width: 10},
		{Title: "Modified", Width: 20},
		{Title: "Class", Width: 14},
	}

	t := ui.NewTable(columns, nil)
	return &S3Objects{
		client:  client,
		bucket:  bucket,
		prefix:  prefix,
		table:   t,
		filter:  ui.NewFilter(),
		spinner: ui.NewSpinner("Loading objects..."),
		loading: true,
	}
}

func (s *S3Objects) Init() tea.Cmd {
	return tea.Batch(s.spinner.Tick(), s.fetchPage(nil, 1))
}

// fetchPage fetches a single page and returns its results as a message.
func (s *S3Objects) fetchPage(token *string, pageNum int) tea.Cmd {
	client := s.client
	bucket := s.bucket
	prefix := s.prefix
	return func() tea.Msg {
		if client == nil {
			return msg.ErrorMsg{Err: fmt.Errorf("AWS client not initialized"), Context: "S3"}
		}
		page, err := aws.ListObjectsPage(context.Background(), client, bucket, prefix, token)
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: fmt.Sprintf("listing objects in %s/%s", bucket, prefix)}
		}
		return s3PageLoadedMsg{
			objects:      page.Objects,
			prefixes:     page.Prefixes,
			hasMorePages: page.HasMorePages,
			token:        page.Token,
			pageNum:      pageNum,
		}
	}
}

func (s *S3Objects) previewCheck(key string) tea.Cmd {
	client := s.client
	bucket := s.bucket
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "HeadObject (preview check): s3://%s/%s", bucket, key)
		meta, err := aws.HeadObject(context.Background(), client, bucket, key)
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: fmt.Sprintf("head s3://%s/%s", bucket, key)}
		}
		return s3PreviewCheckMsg{meta: meta}
	}
}

func (s *S3Objects) fetchObjectContent(key string) tea.Cmd {
	client := s.client
	bucket := s.bucket
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Fetching content: s3://%s/%s", bucket, key)
		content, err := aws.GetObjectContent(context.Background(), client, bucket, key, 1<<20)
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: fmt.Sprintf("reading s3://%s/%s", bucket, key)}
		}
		return s3ObjectContentMsg{content: content, key: key}
	}
}

func (s *S3Objects) presignObject(key string) tea.Cmd {
	client := s.client
	bucket := s.bucket
	return func() tea.Msg {
		url, err := aws.PresignGetObject(context.Background(), client, bucket, key, time.Hour)
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: fmt.Sprintf("presigning s3://%s/%s", bucket, key)}
		}
		eventlog.Infof(eventlog.CatAWS, "Presigned URL generated for s3://%s/%s (1h expiry)", bucket, key)
		return s3PresignedURLMsg{url: url, key: key}
	}
}

func (s *S3Objects) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case s3PageLoadedMsg:
		s.pageNum = m.pageNum

		// Append new data (prefixes only come on first page typically, but handle any)
		s.objects = append(s.objects, m.objects...)
		s.prefixes = append(s.prefixes, m.prefixes...)
		s.buildTable()

		if m.hasMorePages {
			// Chain: immediately fetch the next page
			s.spinner.Show(fmt.Sprintf("Loading... %d items", len(s.objects)+len(s.prefixes)))
			return s, tea.Batch(s.spinner.Tick(), s.fetchPage(m.token, m.pageNum+1))
		}

		// All pages loaded
		s.loading = false
		s.spinner.Hide()
		eventlog.Infof(eventlog.CatAWS, "Loaded %d objects, %d folders in s3://%s/%s (%d pages)",
			len(s.objects), len(s.prefixes), s.bucket, s.prefix, s.pageNum)
		return s, nil

	case s3PreviewCheckMsg:
		meta := m.meta
		if !isPreviewable(meta.ContentType, meta.Key, meta.Size) {
			// Can't preview — fall back to showing metadata
			eventlog.Infof(eventlog.CatUI, "Not previewable (%s, %s), showing metadata instead",
				meta.ContentType, aws.FormatBytes(meta.Size))
			jsonBytes, _ := json.MarshalIndent(meta, "", "  ")
			return s, func() tea.Msg {
				return msg.NavigateMsg{
					ViewID: "content",
					Params: map[string]string{
						"title":   path.Base(meta.Key) + " (metadata)",
						"content": string(jsonBytes),
						"format":  "json",
					},
				}
			}
		}
		return s, s.fetchObjectContent(meta.Key)

	case s3ObjectContentMsg:
		return s, func() tea.Msg {
			return msg.NavigateMsg{
				ViewID: "content",
				Params: map[string]string{
					"title":   path.Base(m.key),
					"content": m.content,
					"format":  "",
				},
			}
		}

	case s3PresignedURLMsg:
		_ = clipboard.WriteAll(m.url)
		return s, func() tea.Msg {
			return msg.StatusMsg{Text: "Presigned URL copied: " + path.Base(m.key)}
		}

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
			// Full refresh: clear and restart
			s.objects = nil
			s.prefixes = nil
			s.entries = nil
			s.pageNum = 0
			s.loading = true
			s.err = nil
			s.spinner.Show("Loading objects...")
			s.table.SetRows(nil)
			return s, tea.Batch(s.spinner.Tick(), s.fetchPage(nil, 1))
		case "enter":
			entry := s.selectedEntry()
			if entry == nil {
				return s, nil
			}
			if entry.isFolder {
				return s, func() tea.Msg {
					return msg.NavigateMsg{
						ViewID: "s3_objects",
						Params: map[string]string{
							"bucket": s.bucket,
							"prefix": entry.fullPath,
						},
					}
				}
			}
			// Preview file content
			return s, s.previewCheck(entry.fullPath)
		case "d":
			// Describe: show object metadata as JSON
			entry := s.selectedEntry()
			if entry == nil || entry.isFolder {
				return s, nil
			}
			eventlog.Infof(eventlog.CatUI, "Describe object: %s", entry.fullPath)
			return s, s.fetchMetadataAndNavigate(entry.fullPath)
		case "u":
			entry := s.selectedEntry()
			if entry == nil || entry.isFolder {
				return s, nil
			}
			return s, s.presignObject(entry.fullPath)
		case "y":
			entry := s.selectedEntry()
			if entry == nil {
				return s, nil
			}
			arn := fmt.Sprintf("s3://%s/%s", s.bucket, entry.fullPath)
			_ = clipboard.WriteAll(arn)
			return s, func() tea.Msg {
				return msg.StatusMsg{Text: "Copied: " + arn}
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

func (s *S3Objects) View() tea.View {
	var content string
	if len(s.objects) == 0 && len(s.prefixes) == 0 && s.loading {
		// No data yet, show spinner only
		content = "\n  " + s.spinner.View()
	} else if s.err != nil {
		content = "\n  " + ui.ErrorStyle.Render("Error: "+s.err.Error())
	} else {
		content = s.table.View()
		if s.filter.Active() {
			content = s.filter.View() + "\n" + content
		}

		filtered, total := s.table.RowCount()
		status := fmt.Sprintf("\n %d/%d items  s3://%s/%s", filtered, total, s.bucket, s.prefix)
		if s.loading {
			status += fmt.Sprintf("  (loading... %d items so far)", total)
		}
		content += status
	}
	return tea.NewView(content)
}

func (s *S3Objects) selectedEntry() *s3TableEntry {
	idx := s.table.SelectedIndex()
	if idx < 0 || idx >= len(s.entries) {
		return nil
	}
	return &s.entries[idx]
}

func (s *S3Objects) buildTable() {
	s.entries = nil
	var rows []table.Row

	for _, prefix := range s.prefixes {
		name := prefix[len(s.prefix):]
		s.entries = append(s.entries, s3TableEntry{isFolder: true, fullPath: prefix})
		rows = append(rows, table.Row{
			"📁",
			name,
			"-",
			"",
			"",
		})
	}

	for _, obj := range s.objects {
		name := obj.Key[len(s.prefix):]
		s.entries = append(s.entries, s3TableEntry{isFolder: false, fullPath: obj.Key})
		rows = append(rows, table.Row{
			"📄",
			name,
			aws.FormatBytes(obj.Size),
			obj.LastModified.Format("2006-01-02 15:04:05"),
			obj.StorageClass,
		})
	}

	s.table.SetRows(rows)
}

func (s *S3Objects) fetchMetadataAndNavigate(key string) tea.Cmd {
	client := s.client
	bucket := s.bucket
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "HeadObject: s3://%s/%s", bucket, key)
		meta, err := aws.HeadObject(context.Background(), client, bucket, key)
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: fmt.Sprintf("head s3://%s/%s", bucket, key)}
		}
		jsonBytes, _ := json.MarshalIndent(meta, "", "  ")
		return msg.NavigateMsg{
			ViewID: "content",
			Params: map[string]string{
				"title":   path.Base(key) + " (metadata)",
				"content": string(jsonBytes),
				"format":  "json",
			},
		}
	}
}

const maxPreviewBytes int64 = 1 << 20 // 1MB

// isPreviewable checks if an object is suitable for text preview.
// Uses ContentType when meaningful, falls back to file extension for
// empty or generic types (application/octet-stream).
func isPreviewable(contentType, key string, size int64) bool {
	if size > maxPreviewBytes {
		return false
	}

	ct := strings.ToLower(strings.TrimSpace(contentType))

	// If we have a meaningful ContentType, trust it as the primary signal
	if ct != "" && ct != "application/octet-stream" && ct != "binary/octet-stream" {
		if strings.HasPrefix(ct, "text/") {
			return true
		}
		previewableTypes := map[string]bool{
			"application/json":          true,
			"application/xml":           true,
			"application/javascript":    true,
			"application/x-yaml":        true,
			"application/yaml":          true,
			"application/toml":          true,
			"application/x-sh":          true,
			"application/x-shellscript": true,
			"application/sql":           true,
			"application/graphql":       true,
			"application/xhtml+xml":     true,
			"application/x-httpd-php":   true,
		}
		if allowed, found := previewableTypes[ct]; found {
			return allowed
		}
		// Known binary types — reject without checking extension
		if strings.HasPrefix(ct, "image/") || strings.HasPrefix(ct, "video/") ||
			strings.HasPrefix(ct, "audio/") || strings.HasPrefix(ct, "font/") {
			return false
		}
	}

	// ContentType is empty, generic, or unrecognized — fall back to extension
	ext := strings.ToLower(path.Ext(key))
	textExts := map[string]bool{
		".txt": true, ".json": true, ".yaml": true, ".yml": true,
		".xml": true, ".html": true, ".htm": true, ".css": true,
		".js": true, ".ts": true, ".go": true, ".py": true,
		".rb": true, ".java": true, ".rs": true, ".c": true,
		".h": true, ".cpp": true, ".sh": true, ".bash": true,
		".zsh": true, ".fish": true, ".md": true, ".csv": true,
		".tsv": true, ".log": true, ".conf": true, ".cfg": true,
		".ini": true, ".toml": true, ".env": true, ".tf": true,
		".hcl": true, ".sql": true, ".graphql": true, ".svg": true,
	}
	return textExts[ext]
}
