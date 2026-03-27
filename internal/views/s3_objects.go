package views

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// s3PageLoadedMsg delivers one page of results for progressive loading.
type s3PageLoadedMsg struct {
	bucket       string // identifies the target view
	prefix       string // identifies the target view
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

type s3DeleteResolvedMsg struct {
	keys []string
}

type s3DeleteCompleteMsg struct {
	count int
	err   error
}

type s3DownloadCompleteMsg struct {
	key  string
	path string
	err  error
}

type s3CopyCompleteMsg struct {
	srcKey string
	dstKey string
	err    error
}

type s3MoveCompleteMsg struct {
	srcKey string
	dstKey string
	err    error
}

// s3TableEntry tracks whether a table row is a folder or object.
type s3TableEntry struct {
	isFolder bool
	fullPath string
}

// S3Objects displays objects and folders within an S3 bucket.
type S3Objects struct {
	s3                aws.S3Service
	bucket            string
	prefix            string
	objects           []aws.S3Object
	prefixes          []string
	entries           []s3TableEntry
	table             ui.Table
	filter            ui.Filter
	spinner           ui.Spinner
	copyInput         ui.Filter // reuse Filter as a text input for copy/move dest
	copyMode          string    // "copy" or "move", empty when inactive
	copySrcKey        string    // source key for copy/move
	loading           bool
	pageNum           int
	pendingDeleteEntries []s3TableEntry
	err               error
	width             int
	height            int
	widthTier         ui.WidthTier
}

func (s *S3Objects) ID() string {
	return "s3_objects:" + s.bucket + ":" + s.prefix
}

func (s *S3Objects) KeyMap() []ui.KeyHint {
	return []ui.KeyHint{
		{Key: "enter", Desc: "view"},
		{Key: "d", Desc: "describe"},
		{Key: "w", Desc: "download"},
		{Key: "c", Desc: "copy", Mode: ui.ModeReadWrite},
		{Key: "m", Desc: "move", Mode: ui.ModeReadWrite},
		{Key: "v", Desc: "versions"},
		{Key: "u", Desc: "presign"},
		{Key: "y", Desc: "copy path"},
		{Key: "space", Desc: "select"},
		{Key: "x", Desc: "delete", Mode: ui.ModeReadWrite},
		{Key: "s/S", Desc: "sort"},
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

func s3ObjectColumns(tier ui.WidthTier) []table.Column {
	if tier == ui.TierNarrow {
		return []table.Column{
			{Title: "", Width: 3},
			{Title: "Name", Width: 40},
			{Title: "Size", Width: 10},
		}
	}
	return []table.Column{
		{Title: "", Width: 3},
		{Title: "Name", Width: 40},
		{Title: "Size", Width: 10},
		{Title: "Modified", Width: 20},
		{Title: "Class", Width: 14},
	}
}

// NewS3Objects creates the S3 object browser view.
func NewS3Objects(s3 aws.S3Service, bucket, prefix string) *S3Objects {
	t := ui.NewTable(s3ObjectColumns(ui.TierMedium), nil)
	return &S3Objects{
		s3:      s3,
		bucket:  bucket,
		prefix:  prefix,
		table:   t,
		filter:  ui.NewFilter(),
		spinner:   ui.NewSpinner("Loading objects..."),
		loading:   true,
		widthTier: ui.TierMedium,
	}
}

func (s *S3Objects) Init() tea.Cmd {
	// Already loaded — nothing to do (idempotent for navigator cache reuse).
	if !s.loading {
		return nil
	}
	// If re-entered from the navigator cache while still loading
	// (e.g. user went back before the fetch completed), restart cleanly.
	if s.pageNum > 0 {
		return s.refreshListing()
	}
	return tea.Batch(s.spinner.Tick(), s.fetchPage(nil, 1))
}

// fetchPage fetches a single page and returns its results as a message.
func (s *S3Objects) fetchPage(token *string, pageNum int) tea.Cmd {
	svc := s.s3
	bucket := s.bucket
	prefix := s.prefix
	return func() tea.Msg {
		if svc == nil {
			return msg.ErrorMsg{Err: fmt.Errorf("AWS client not initialized"), Context: "S3"}
		}
		page, err := svc.ListObjectsPage(context.Background(), bucket, prefix, token)
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: fmt.Sprintf("listing objects in %s/%s", bucket, prefix)}
		}
		return s3PageLoadedMsg{
			bucket:       bucket,
			prefix:       prefix,
			objects:      page.Objects,
			prefixes:     page.Prefixes,
			hasMorePages: page.HasMorePages,
			token:        page.Token,
			pageNum:      pageNum,
		}
	}
}

// resolveDeleteKeys expands folders into their contained object keys.
func (s *S3Objects) resolveDeleteKeys(entries []s3TableEntry) tea.Cmd {
	svc := s.s3
	bucket := s.bucket
	return func() tea.Msg {
		var keys []string
		for _, e := range entries {
			if e.isFolder {
				folderKeys, err := svc.ListAllKeys(context.Background(), bucket, e.fullPath)
				if err != nil {
					return msg.ErrorMsg{Err: err, Context: fmt.Sprintf("listing objects in %s", e.fullPath)}
				}
				keys = append(keys, folderKeys...)
			} else {
				keys = append(keys, e.fullPath)
			}
		}
		return s3DeleteResolvedMsg{keys: keys}
	}
}

func (s *S3Objects) deleteObjects(keys []string) tea.Cmd {
	svc := s.s3
	bucket := s.bucket
	count := len(keys)
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Deleting %d objects in s3://%s", count, bucket)
		err := svc.DeleteObjects(context.Background(), bucket, keys)
		return s3DeleteCompleteMsg{count: count, err: err}
	}
}

func (s *S3Objects) previewCheck(key string) tea.Cmd {
	svc := s.s3
	bucket := s.bucket
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "HeadObject (preview check): s3://%s/%s", bucket, key)
		meta, err := svc.HeadObject(context.Background(), bucket, key)
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: fmt.Sprintf("head s3://%s/%s", bucket, key)}
		}
		return s3PreviewCheckMsg{meta: meta}
	}
}

func (s *S3Objects) fetchObjectContent(key string) tea.Cmd {
	svc := s.s3
	bucket := s.bucket
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Fetching content: s3://%s/%s", bucket, key)
		content, err := svc.GetObjectContent(context.Background(), bucket, key, 1<<20)
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: fmt.Sprintf("reading s3://%s/%s", bucket, key)}
		}
		return s3ObjectContentMsg{content: content, key: key}
	}
}

func (s *S3Objects) presignObject(key string) tea.Cmd {
	svc := s.s3
	bucket := s.bucket
	return func() tea.Msg {
		url, err := svc.PresignGetObject(context.Background(), bucket, key, time.Hour)
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: fmt.Sprintf("presigning s3://%s/%s", bucket, key)}
		}
		eventlog.Infof(eventlog.CatAWS, "Presigned URL generated for s3://%s/%s (1h expiry)", bucket, key)
		return s3PresignedURLMsg{url: url, key: key}
	}
}

func (s *S3Objects) refreshListing() tea.Cmd {
	s.objects = nil
	s.prefixes = nil
	s.entries = nil
	s.pageNum = 0
	s.loading = true
	s.spinner.Show("Refreshing...")
	s.table.SetRows(nil)
	return tea.Batch(s.spinner.Tick(), s.fetchPage(nil, 1))
}

func (s *S3Objects) downloadObject(key string) tea.Cmd {
	svc := s.s3
	bucket := s.bucket
	destPath := aws.DefaultDownloadPath(key)
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Downloading s3://%s/%s → %s", bucket, key, destPath)
		err := svc.DownloadObject(context.Background(), bucket, key, destPath)
		return s3DownloadCompleteMsg{key: key, path: destPath, err: err}
	}
}

func (s *S3Objects) copyObject(srcKey, dstKey string) tea.Cmd {
	svc := s.s3
	bucket := s.bucket
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Copying s3://%s/%s → s3://%s/%s", bucket, srcKey, bucket, dstKey)
		err := svc.CopyObject(context.Background(), bucket, srcKey, bucket, dstKey)
		return s3CopyCompleteMsg{srcKey: srcKey, dstKey: dstKey, err: err}
	}
}

func (s *S3Objects) moveObject(srcKey, dstKey string) tea.Cmd {
	svc := s.s3
	bucket := s.bucket
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Moving s3://%s/%s → s3://%s/%s", bucket, srcKey, bucket, dstKey)
		err := svc.MoveObject(context.Background(), bucket, srcKey, bucket, dstKey)
		return s3MoveCompleteMsg{srcKey: srcKey, dstKey: dstKey, err: err}
	}
}

func (s *S3Objects) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case ui.ConfirmResultMsg:
		if m.Confirmed && m.Action == "delete_objects" && len(s.pendingDeleteEntries) > 0 {
			entries := s.pendingDeleteEntries
			s.pendingDeleteEntries = nil
			s.spinner.Show("Resolving keys...")
			return s, tea.Batch(s.spinner.Tick(), s.resolveDeleteKeys(entries))
		}
		s.pendingDeleteEntries = nil
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

	case s3DeleteResolvedMsg:
		if len(m.keys) == 0 {
			s.spinner.Hide()
			return s, func() tea.Msg {
				return msg.ToastError("No objects found to delete")
			}
		}
		return s, s.deleteObjects(m.keys)

	case s3DeleteCompleteMsg:
		if m.err != nil {
			s.err = m.err
			return s, nil
		}
		eventlog.Infof(eventlog.CatAWS, "Deleted %d objects in s3://%s/%s", m.count, s.bucket, s.prefix)
		s.objects = nil
		s.prefixes = nil
		s.entries = nil
		s.pageNum = 0
		s.loading = true
		s.spinner.Show("Refreshing...")
		s.table.SetRows(nil)
		count := m.count
		return s, tea.Batch(s.spinner.Tick(), s.fetchPage(nil, 1), func() tea.Msg {
			return msg.ToastSuccess(fmt.Sprintf("Deleted %d object(s)", count))
		})

	case s3DownloadCompleteMsg:
		if m.err != nil {
			s.err = m.err
			return s, nil
		}
		return s, func() tea.Msg {
			return msg.ToastSuccess(fmt.Sprintf("Downloaded → %s", m.path))
		}

	case s3CopyCompleteMsg:
		if m.err != nil {
			s.err = m.err
			return s, nil
		}
		eventlog.Infof(eventlog.CatAWS, "Copied %s → %s", m.srcKey, m.dstKey)
		dstKey := m.dstKey
		return s, tea.Batch(s.refreshListing(), func() tea.Msg {
			return msg.ToastSuccess("Copied → " + path.Base(dstKey))
		})

	case s3MoveCompleteMsg:
		if m.err != nil {
			s.err = m.err
			return s, nil
		}
		eventlog.Infof(eventlog.CatAWS, "Moved %s → %s", m.srcKey, m.dstKey)
		dstKey := m.dstKey
		return s, tea.Batch(s.refreshListing(), func() tea.Msg {
			return msg.ToastSuccess("Moved → " + path.Base(dstKey))
		})

	case s3PageLoadedMsg:
		// Discard messages from a different S3Objects view (e.g. user navigated back
		// before loading finished, and the message was routed to the wrong view).
		if m.bucket != s.bucket || m.prefix != s.prefix {
			return s, nil
		}

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
		return s, tea.Batch(
			tea.SetClipboard(m.url),
			func() tea.Msg { return msg.ToastSuccess("Presigned URL copied: " + path.Base(m.key)) },
		)

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

		cols := s3ObjectColumns(newTier)
		if !ui.ColumnsFit(cols, m.Width) {
			cols = s3ObjectColumns(ui.TierNarrow)
			s.widthTier = ui.TierNarrow
		}
		if len(cols) != len(s.table.Columns()) {
			s.table.SetColumns(cols)
			if len(s.objects) > 0 || len(s.prefixes) > 0 {
				s.buildTable()
			}
		}
		s.table.SetSize(m.Width, m.Height-3)
		s.filter.SetWidth(m.Width)
		return s, nil

	case ui.FilterChangedMsg:
		// Ignore filter messages from the copy/move input
		if s.copyMode == "" {
			s.table.Filter(m.Text)
		}
		return s, nil

	case tea.KeyPressMsg:
		if s.filter.Active() {
			var cmd tea.Cmd
			s.filter, cmd = s.filter.Update(m)
			return s, cmd
		}

		// Copy/move destination input
		if s.copyInput.Active() {
			switch m.String() {
			case "enter":
				dest := s.copyInput.Value()
				if dest == "" {
					dest = s.copySrcKey + "_copy"
				}
				src := s.copySrcKey
				mode := s.copyMode
				s.copyInput.Deactivate()
				s.copyMode = ""
				s.copySrcKey = ""
				if mode == "move" {
					return s, s.moveObject(src, dest)
				}
				return s, s.copyObject(src, dest)
			case "esc":
				s.copyInput.Deactivate()
				s.copyMode = ""
				s.copySrcKey = ""
				return s, nil
			default:
				var cmd tea.Cmd
				s.copyInput, cmd = s.copyInput.Update(m)
				return s, cmd
			}
		}

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
			return s, tea.Batch(
				tea.SetClipboard(arn),
				func() tea.Msg { return msg.ToastSuccess("Copied: " + arn) },
			)
		case "space", " ":
			s.table.ToggleSelect()
			return s, nil
		case "x", "X":
			if ui.ReadOnly {
				return s, func() tea.Msg {
					return msg.ToastError("ReadOnly mode — press W to switch")
				}
			}
			entries := s.collectDeleteEntries()
			if len(entries) == 0 {
				return s, func() tea.Msg {
					return msg.ToastError("No items to delete (select with space first)")
				}
			}
			s.pendingDeleteEntries = entries
			// Build a human-readable confirmation message
			var folders, objects int
			for _, e := range entries {
				if e.isFolder {
					folders++
				} else {
					objects++
				}
			}
			var parts []string
			if folders > 0 {
				parts = append(parts, fmt.Sprintf("%d folder(s)", folders))
			}
			if objects > 0 {
				parts = append(parts, fmt.Sprintf("%d object(s)", objects))
			}
			message := "Delete " + strings.Join(parts, " and ") + "?"
			return s, func() tea.Msg {
				return msg.RequestConfirmMsg{
					Message: message,
					Action:  "delete_objects",
				}
			}
		case "w":
			entry := s.selectedEntry()
			if entry == nil || entry.isFolder {
				return s, nil
			}
			return s, s.downloadObject(entry.fullPath)
		case "c":
			if ui.ReadOnly {
				return s, func() tea.Msg {
					return msg.ToastError("ReadOnly mode — press W to switch")
				}
			}
			entry := s.selectedEntry()
			if entry == nil || entry.isFolder {
				return s, nil
			}
			s.copySrcKey = entry.fullPath
			s.copyMode = "copy"
			s.copyInput = ui.NewFilter()
			s.copyInput.Activate()
			return s, nil
		case "m":
			if ui.ReadOnly {
				return s, func() tea.Msg {
					return msg.ToastError("ReadOnly mode — press W to switch")
				}
			}
			entry := s.selectedEntry()
			if entry == nil || entry.isFolder {
				return s, nil
			}
			s.copySrcKey = entry.fullPath
			s.copyMode = "move"
			s.copyInput = ui.NewFilter()
			s.copyInput.Activate()
			return s, nil
		case "v":
			entry := s.selectedEntry()
			if entry == nil || entry.isFolder {
				return s, nil
			}
			return s, func() tea.Msg {
				return msg.NavigateMsg{
					ViewID: "s3_versions",
					Params: map[string]string{
						"bucket": s.bucket,
						"key":    entry.fullPath,
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
		if sel := s.table.SelectionCount(); sel > 0 {
			status += fmt.Sprintf("  (%d selected)", sel)
		}
		content += status
	}

	// Copy/move destination input
	if s.copyInput.Active() {
		label := "Copy to"
		if s.copyMode == "move" {
			label = "Move to"
		}
		content += fmt.Sprintf("\n %s: %s", label, s.copyInput.View())
	}

	return tea.NewView(content)
}

// collectDeleteEntries gathers entries (objects and folders) for deletion.
// If items are selected, only deletes selected items that are currently visible.
// If nothing is selected, deletes the item under the cursor.
func (s *S3Objects) collectDeleteEntries() []s3TableEntry {
	var entries []s3TableEntry

	// Build a set of currently visible allRows indices
	visibleSet := make(map[int]bool)
	for _, allIdx := range s.table.FilteredIndices() {
		visibleSet[allIdx] = true
	}

	if s.table.SelectionCount() > 0 {
		for _, allIdx := range s.table.SelectedIndices() {
			if visibleSet[allIdx] && allIdx < len(s.entries) {
				entries = append(entries, s.entries[allIdx])
			}
		}
	} else {
		entry := s.selectedEntry()
		if entry != nil {
			entries = append(entries, *entry)
		}
	}
	return entries
}

func (s *S3Objects) selectedEntry() *s3TableEntry {
	allIdx := s.table.SelectedAllRowIndex()
	if allIdx < 0 || allIdx >= len(s.entries) {
		return nil
	}
	return &s.entries[allIdx]
}

func (s *S3Objects) buildTable() {
	s.entries = nil
	var rows []table.Row
	var sortKeys []table.Row
	narrow := s.widthTier == ui.TierNarrow

	for _, prefix := range s.prefixes {
		name := prefix[len(s.prefix):]
		s.entries = append(s.entries, s3TableEntry{isFolder: true, fullPath: prefix})
		if narrow {
			rows = append(rows, table.Row{"📁", name, "-"})
			sortKeys = append(sortKeys, table.Row{"0", name, ui.SortKeyBytes(0)})
		} else {
			rows = append(rows, table.Row{"📁", name, "-", "", ""})
			sortKeys = append(sortKeys, table.Row{"0", name, ui.SortKeyBytes(0), "", ""})
		}
	}

	for _, obj := range s.objects {
		name := obj.Key[len(s.prefix):]
		s.entries = append(s.entries, s3TableEntry{isFolder: false, fullPath: obj.Key})
		if narrow {
			rows = append(rows, table.Row{"📄", name, aws.FormatBytes(obj.Size)})
			sortKeys = append(sortKeys, table.Row{"1", name, ui.SortKeyBytes(obj.Size)})
		} else {
			rows = append(rows, table.Row{
				"📄", name, aws.FormatBytes(obj.Size),
				obj.LastModified.Format("2006-01-02 15:04:05"), obj.StorageClass,
			})
			sortKeys = append(sortKeys, table.Row{
				"1", name, ui.SortKeyBytes(obj.Size),
				obj.LastModified.Format("2006-01-02 15:04:05"), obj.StorageClass,
			})
		}
	}

	s.table.SetRowsWithSortKeys(rows, sortKeys)
}

func (s *S3Objects) fetchMetadataAndNavigate(key string) tea.Cmd {
	svc := s.s3
	bucket := s.bucket
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "HeadObject: s3://%s/%s", bucket, key)
		meta, err := svc.HeadObject(context.Background(), bucket, key)
		if err != nil {
			return msg.ErrorMsg{Err: err, Context: fmt.Sprintf("head s3://%s/%s", bucket, key)}
		}
		jsonBytes, _ := json.MarshalIndent(meta, "", "  ")
		title := path.Base(key) + " (metadata)"
		tabs := []msg.TabContent{
			{Title: "Info", Content: buildS3InfoContent(meta), Format: "text"},
			{Title: "JSON", Content: string(jsonBytes), Format: "json"},
		}
		if len(meta.Metadata) > 0 {
			tabs = append(tabs, msg.TabContent{
				Title: "Metadata", Content: buildS3MetadataContent(meta.Metadata), Format: "text",
			})
		}
		return msg.TabbedContentMsg{PanelTitle: title, Tabs: tabs}
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

func buildS3InfoContent(meta *aws.ObjectMeta) string {
	fields := []struct{ k, v string }{
		{"Key", meta.Key},
		{"Size", aws.FormatBytes(meta.Size)},
		{"Content Type", meta.ContentType},
		{"Storage Class", meta.StorageClass},
		{"Last Modified", meta.LastModified.Format("2006-01-02 15:04:05")},
		{"ETag", meta.ETag},
	}
	var b strings.Builder
	for _, f := range fields {
		if f.v != "" {
			b.WriteString(fmt.Sprintf("%-16s %s\n", f.k, f.v))
		}
	}
	return b.String()
}

func buildS3MetadataContent(metadata map[string]string) string {
	keys := make([]string, 0, len(metadata))
	for k := range metadata {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(fmt.Sprintf("%-24s %s\n", k, metadata[k]))
	}
	return b.String()
}
