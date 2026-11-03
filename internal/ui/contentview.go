package ui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/x/ansi"
)

// ContentFormat identifies the content type.
type ContentFormat string

const (
	FormatJSON     ContentFormat = "json"
	FormatYAML     ContentFormat = "yaml"
	FormatMarkdown ContentFormat = "markdown"
	FormatHCL      ContentFormat = "hcl"
	FormatShell    ContentFormat = "bash"
	FormatGo       ContentFormat = "go"
	FormatPython   ContentFormat = "python"
	FormatText     ContentFormat = "text"
	FormatAuto     ContentFormat = "auto"
)

// EditorFinishedMsg is sent when the external editor exits.
type EditorFinishedMsg struct {
	Err error
}

// YankedMsg is sent when content is copied to the clipboard.
type YankedMsg struct {
	Lines int
}

// ContentView displays syntax-highlighted, scrollable content with a cursor,
// visual line selection, and yank-to-clipboard.
// ContentLink maps a line to a navigation target for cross-resource navigation.
type ContentLink struct {
	ViewID string
	Params map[string]string
}

// ContentLinkActivatedMsg is emitted when the user presses Enter on a linked line.
type ContentLinkActivatedMsg struct {
	ViewID string
	Params map[string]string
}

type ContentView struct {
	keys        ContentViewKeyMap
	viewport    viewport.Model
	title       string
	raw         string        // original unhighlighted content, for yank and the editor
	text        string        // raw made safe with SanitizeLines, for display
	rawLines    []string      // raw split into lines
	format      ContentFormat // resolved format
	lineNumbers bool
	cursor      int                 // current cursor line (0-indexed)
	visualMode  bool                // visual line selection active
	visualStart int                 // anchor line for visual selection
	links       map[int]ContentLink // line index → navigation target
	width       int
	height      int
	yankMsg     string // transient "yanked N lines" feedback
	embedded    bool   // true when inside TabbedPanel; hides footer
	wrapOff     bool   // when true, disable word wrap and enable horizontal scroll

	// hlLines is the chroma output, one entry for each line. hlStyle is the
	// chroma style it was made with. A key press does not highlight again.
	hlLines []string
	hlStyle string
	layout  contentLayout
	screen  contentScreen
}

// contentScreen is the rows that the viewport shows: the gutter, the link
// prefix, and the text of each row.
//
// plain is made again only when the layout, the gutter, the links, or the
// theme changes. A key press only paints the cursor or selection rows in
// shown, and puts back the rows it painted before. So a key press costs
// the rows that change, not all the rows of the text.
type contentScreen struct {
	valid bool
	theme string
	plain []string // rows without the cursor or selection background
	// shown is the slice the viewport holds. SetContentLines keeps the
	// slice and does not copy it, so a change to a row here shows on the
	// screen without a new SetContentLines call. That call measures each
	// row, which is slow for a long text.
	shown    []string
	from, to int // rows in shown that have a background now
}

// contentLayout is the highlighted text cut into screen rows. With wrap on,
// a long line takes more than one row. With wrap off, each line is one row.
//
// The scroll model: the cursor is on a line, and the viewport offset counts
// rows. So yank, links, and visual mode work on whole lines, but the user
// can still scroll through a line that is taller than the screen.
//
// renderContent makes the layout again only when the text, the width, or
// the wrap setting changes. This keeps a key press on a long line fast.
type contentLayout struct {
	valid     bool
	lineWidth int
	wrap      bool
	rows      []string // text of each row, without the gutter
	lineStart []int    // first row of each line; the last entry is the row count
}

// NewContentView creates a content viewer with the given title, content, and format.
func NewContentView(title, content string, format ContentFormat) ContentView {
	vp := viewport.New()
	cv := ContentView{
		keys:        DefaultContentViewKeyMap(),
		viewport:    vp,
		title:       Sanitize(title),
		raw:         content,
		text:        SanitizeLines(content),
		rawLines:    strings.Split(content, "\n"),
		lineNumbers: true,
	}

	if format == FormatAuto {
		cv.format = detectFormat(title, content)
	} else {
		cv.format = format
	}

	cv.renderContent()
	return cv
}

// SetEmbedded marks this ContentView as embedded inside a TabbedPanel,
// hiding the footer (which duplicates the app-level status bar hints).
func (cv *ContentView) SetEmbedded(v bool) {
	cv.embedded = v
}

// chromeLines returns the number of non-viewport lines (header + optional footer).
func (cv *ContentView) chromeLines() int {
	if cv.embedded {
		return 1 // header only
	}
	return 2 // header + footer
}

// SetSize sets the viewer dimensions.
func (cv *ContentView) SetSize(w, h int) {
	cv.width = w
	cv.height = h
	cv.viewport.SetWidth(w)
	cv.viewport.SetHeight(h - cv.chromeLines())
	cv.relayout()
}

// SetLinks sets navigable line links. Lines with links show a visual indicator
// and emit NavigateMsg when Enter is pressed on them.
func (cv *ContentView) SetLinks(links map[int]ContentLink) {
	cv.links = links
	cv.screen.valid = false
	// The link prefix makes the text narrower, so the wrap can change.
	cv.relayout()
}

// HasLinkAtCursor reports whether the current cursor line has a navigation link.
func (cv ContentView) HasLinkAtCursor() bool {
	if cv.links == nil {
		return false
	}
	_, ok := cv.links[cv.cursor]
	return ok
}

// SetContent replaces the content.
func (cv *ContentView) SetContent(title, content string, format ContentFormat) {
	cv.title = Sanitize(title)
	cv.raw = content
	cv.text = SanitizeLines(content)
	cv.rawLines = strings.Split(content, "\n")
	cv.hlLines = nil
	cv.layout.valid = false
	cv.cursor = 0
	cv.visualMode = false
	if format == FormatAuto {
		cv.format = detectFormat(title, content)
	} else {
		cv.format = format
	}
	cv.renderContent()
	// The cursor is on the first line, so show the top of the new text.
	cv.viewport.GotoTop()
}

// ToggleWrap toggles word wrapping. When off, the viewport enables
// horizontal scrolling via h/l keys and ANSI-aware line clipping.
func (cv *ContentView) ToggleWrap() {
	cv.wrapOff = !cv.wrapOff
	if !cv.wrapOff {
		// With wrap on, all the text fits the width.
		cv.viewport.SetXOffset(0)
	}
	cv.relayout()
}

// ToggleLineNumbers toggles line number display.
func (cv *ContentView) ToggleLineNumbers() {
	cv.lineNumbers = !cv.lineNumbers
	cv.screen.valid = false
	// The gutter width changes, so the wrap can change.
	cv.relayout()
}

// relayout draws the content again after a change that can wrap the lines
// in a different way: the size, the wrap, the gutter, or the links. Then a
// line can start at a different row, so the old row offset can point at
// other lines. relayout keeps the line at the top of the screen at the top,
// and then makes sure that the cursor line is on the screen.
func (cv *ContentView) relayout() {
	top, inner := -1, 0
	if cv.layout.valid {
		off := cv.viewport.YOffset()
		top = cv.lineAtRow(off)
		inner = off - cv.layout.lineStart[top]
	}
	cv.renderContent()
	if top >= 0 {
		start, end := cv.lineRows(top)
		cv.viewport.SetYOffset(start + min(max(inner, 0), end-start-1))
	}
	cv.ensureCursorVisible()
}

// Raw returns the original unhighlighted content.
func (cv ContentView) Raw() string {
	return cv.raw
}

// InVisualMode returns whether visual line selection is active.
func (cv ContentView) InVisualMode() bool {
	return cv.visualMode
}

// CancelVisual exits visual mode without yanking.
func (cv *ContentView) CancelVisual() {
	cv.visualMode = false
	cv.renderContent()
}

// OpenInEditorCmd returns a tea.Cmd that opens the content in $EDITOR.
func (cv ContentView) OpenInEditorCmd() tea.Cmd {
	ext := formatToExt(cv.format)
	tmpFile, err := os.CreateTemp("", "lazycloud-*"+ext)
	if err != nil {
		return func() tea.Msg { return EditorFinishedMsg{Err: err} }
	}

	_, err = tmpFile.WriteString(cv.raw)
	// Close can report a write that failed, so the editor does not open a
	// part of the content.
	if closeErr := tmpFile.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmpFile.Name())
		return func() tea.Msg { return EditorFinishedMsg{Err: err} }
	}

	editor := getEditor()
	// The user closes the editor, so no context cancels it.
	c := exec.CommandContext(context.Background(), editor, tmpFile.Name()) //nolint:gosec // editor is from $EDITOR env, standard pattern
	return tea.ExecProcess(c, func(err error) tea.Msg {
		_ = os.Remove(tmpFile.Name())
		return EditorFinishedMsg{Err: err}
	})
}

func (cv *ContentView) lineCount() int {
	return len(cv.rawLines)
}

func (cv *ContentView) clampCursor() {
	if cv.cursor < 0 {
		cv.cursor = 0
	}
	if cv.cursor >= cv.lineCount() {
		cv.cursor = cv.lineCount() - 1
	}
}

// selectionRange returns the ordered start/end of the selection (inclusive).
func (cv *ContentView) selectionRange() (int, int) {
	if !cv.visualMode {
		return cv.cursor, cv.cursor
	}
	lo, hi := cv.visualStart, cv.cursor
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo, hi
}

func (cv *ContentView) yankSelection() tea.Cmd {
	lo, hi := cv.selectionRange()
	selected := cv.rawLines[lo : hi+1]
	text := strings.Join(selected, "\n")
	count := hi - lo + 1

	cv.visualMode = false
	cv.yankMsg = fmt.Sprintf("%d line(s) yanked", count)

	cv.renderContent()
	return tea.Batch(
		tea.SetClipboard(text),
		func() tea.Msg { return YankedMsg{Lines: count} },
	)
}

// lineRows returns the rows of line i: from start up to, but not
// including, end.
func (cv *ContentView) lineRows(i int) (start, end int) {
	return cv.layout.lineStart[i], cv.layout.lineStart[i+1]
}

// lineAtRow returns the line that holds the given row. A row after the end
// gives the last line.
func (cv *ContentView) lineAtRow(row int) int {
	n := cv.lineCount()
	i := sort.Search(n, func(i int) bool { return cv.layout.lineStart[i+1] > row })
	return min(i, n-1)
}

// ensureCursorVisible scrolls so that the cursor line is on the screen. A
// line that fits is shown in full. A line taller than the screen fills the
// screen, and the offset stays inside that line.
func (cv *ContentView) ensureCursorVisible() {
	h := max(cv.viewport.Height(), 1)
	off := cv.viewport.YOffset()
	start, end := cv.lineRows(cv.cursor)

	if end-start <= h {
		if start < off {
			off = start
		} else if end > off+h {
			off = end - h
		}
	} else {
		off = min(max(off, start), end-h)
	}
	cv.viewport.SetYOffset(off)
}

// scrollInCursorLine scrolls by delta rows when the cursor line has rows
// off the screen in that direction. It does not scroll past the cursor
// line. It returns false when there is nothing to scroll, so the caller
// moves the cursor to the next line.
func (cv *ContentView) scrollInCursorLine(delta int) bool {
	if cv.wrapOff || delta == 0 {
		return false
	}
	h := max(cv.viewport.Height(), 1)
	off := cv.viewport.YOffset()
	start, end := cv.lineRows(cv.cursor)

	switch {
	case delta > 0 && end > off+h:
		cv.viewport.SetYOffset(min(off+delta, end-h))
		return true
	case delta < 0 && start < off:
		cv.viewport.SetYOffset(max(off+delta, start))
		return true
	}
	return false
}

// moveCursorTo puts the cursor on line i and scrolls to it.
func (cv *ContentView) moveCursorTo(i int) {
	cv.cursor = i
	cv.clampCursor()
	cv.renderContent()
	cv.ensureCursorVisible()
}

// Update handles cursor movement, visual selection, yank, and viewport scrolling.
func (cv ContentView) Update(msg tea.Msg) (ContentView, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		k := cv.keys
		switch {
		// With wrap on, a line can be taller than the screen. Then j, k,
		// ctrl+d, and ctrl+u first scroll through that line, and move the
		// cursor only when the end of the line is on the screen.
		case key.Matches(msg, k.CursorDown):
			if !cv.scrollInCursorLine(1) {
				cv.moveCursorTo(cv.cursor + 1)
			}
			return cv, nil
		case key.Matches(msg, k.CursorUp):
			if !cv.scrollInCursorLine(-1) {
				cv.moveCursorTo(cv.cursor - 1)
			}
			return cv, nil
		case key.Matches(msg, k.GotoTop):
			cv.moveCursorTo(0)
			cv.viewport.GotoTop()
			return cv, nil
		case key.Matches(msg, k.GotoBottom):
			// Show the last row, also when the last line is taller than
			// the screen.
			cv.moveCursorTo(cv.lineCount() - 1)
			cv.viewport.GotoBottom()
			return cv, nil
		case key.Matches(msg, k.HalfPageDown):
			half := cv.viewport.Height() / 2
			if !cv.scrollInCursorLine(half) {
				start, _ := cv.lineRows(cv.cursor)
				next := cv.lineAtRow(start + half)
				if next == cv.cursor && half > 0 {
					// The jump stays in the cursor line, so go one line on.
					next++
				}
				cv.moveCursorTo(next)
			}
			return cv, nil
		case key.Matches(msg, k.HalfPageUp):
			half := cv.viewport.Height() / 2
			if !cv.scrollInCursorLine(-half) {
				start, _ := cv.lineRows(cv.cursor)
				cv.moveCursorTo(cv.lineAtRow(max(start-half, 0)))
			}
			return cv, nil
		case key.Matches(msg, k.VisualToggle):
			if cv.visualMode {
				cv.visualMode = false
			} else {
				cv.visualMode = true
				cv.visualStart = cv.cursor
			}
			cv.renderContent()
			return cv, nil
		case key.Matches(msg, k.Yank):
			cmd := cv.yankSelection()
			return cv, cmd
		case key.Matches(msg, k.Enter):
			if cv.links != nil {
				if link, ok := cv.links[cv.cursor]; ok {
					viewID := link.ViewID
					params := link.Params
					return cv, func() tea.Msg {
						return ContentLinkActivatedMsg{ViewID: viewID, Params: params}
					}
				}
			}
			return cv, nil
		case key.Matches(msg, k.LineNumbers):
			cv.ToggleLineNumbers()
			return cv, nil
		case key.Matches(msg, k.WrapToggle):
			cv.ToggleWrap()
			return cv, nil
		case key.Matches(msg, k.ScrollLeft, k.ScrollRight):
			if cv.wrapOff {
				var cmd tea.Cmd
				cv.viewport, cmd = cv.viewport.Update(msg)
				// The cursor background must reach the new right edge.
				cv.renderContent()
				return cv, cmd
			}
			return cv, nil
		}
	}

	// Don't pass j/k/g/G to viewport — we handle scrolling via cursor
	return cv, nil
}

// View renders the content viewer.
func (cv ContentView) View() string {
	s := S

	// Title bar
	titleText := s.Title.Render(cv.title)
	formatBadge := s.FormatBadge.Render(string(cv.format))

	posInfo := s.PositionInfo.Render(
		fmt.Sprintf(" Ln %d/%d  %.0f%%", cv.cursor+1, cv.lineCount(), cv.viewport.ScrollPercent()*100),
	)

	modeInfo := ""
	if cv.visualMode {
		lo, hi := cv.selectionRange()
		modeInfo = s.ModeIndicator.Render(
			fmt.Sprintf("  VISUAL (%d lines)", hi-lo+1),
		)
	}

	yankInfo := ""
	if cv.yankMsg != "" {
		yankInfo = "  " + s.CommandCursor.Render(cv.yankMsg)
	}

	wrapInfo := ""
	if cv.wrapOff {
		wrapInfo = "  " + s.Info.Render("nowrap")
		if cv.viewport.XOffset() > 0 {
			wrapInfo += s.PositionInfo.Render(
				fmt.Sprintf("  Col %d", cv.viewport.XOffset()),
			)
		}
	}

	header := titleText + "  " + formatBadge + posInfo + modeInfo + yankInfo + wrapInfo

	if cv.embedded {
		// Drop title (tab bar already shows it); hide format badge for plain text
		if cv.format != FormatText {
			header = formatBadge + posInfo + modeInfo + yankInfo + wrapInfo
		} else {
			header = posInfo + modeInfo + yankInfo + wrapInfo
		}
		return header + "\n" + cv.viewport.View()
	}

	// Footer (only shown in standalone mode, not when embedded in TabbedPanel)
	hints := "j/k move  V visual  y yank  e editor  n lines  g/G top/bottom  esc back"
	if len(cv.links) > 0 {
		hints = "enter navigate  " + hints
	}
	footer := s.Muted.Render(hints)

	return header + "\n" + cv.viewport.View() + "\n" + footer
}

// renderContent draws the rows into the viewport. It runs on each key press,
// so the slow parts (chroma, the wrap, and the gutter) come from caches.
func (cv *ContentView) renderContent() {
	width := cv.width
	if width <= 0 {
		width = 80
	}

	cv.highlight()
	numWidth := len(fmt.Sprintf("%d", len(cv.hlLines)))

	// Calculate the width available for line content (full width minus gutter)
	gutterWidth := 0
	if cv.lineNumbers {
		gutterWidth = numWidth + 3 // digits + " │ "
	}
	lineWidth := max(width-gutterWidth-2, 10)

	// In panels that have links, the link prefix takes part of the line.
	hasLinks := len(cv.links) > 0
	if hasLinks {
		prefixWidth := lipgloss.Width(S.LinkIndicator.Render("→ "))
		gutterWidth += prefixWidth
		lineWidth = max(lineWidth-prefixWidth, 10)
	}
	cv.buildLayout(lineWidth)

	if !cv.screen.valid || cv.screen.theme != ActiveTheme.Name {
		cv.decorate(numWidth, hasLinks)
	}

	padTo := lineWidth
	if cv.wrapOff {
		// With wrap off, the viewport cuts each row at the X offset. Fill
		// the background up to the right edge of the screen, so it shows
		// at any offset.
		padTo = max(padTo, cv.viewport.XOffset()+cv.viewport.Width()-gutterWidth)
	}
	cv.paintHighlight(padTo)
}

// decorate makes the plain rows: the gutter, the link prefix, and the text
// of each row. It gives them to the viewport.
func (cv *ContentView) decorate(numWidth int, hasLinks bool) {
	numStyle := S.LineNumber
	linkPrefix := S.LinkIndicator.Render("→ ")
	linkSpacer := strings.Repeat(" ", lipgloss.Width(linkPrefix))
	// A wrapped row after the first row of a line gets an empty gutter.
	blankNum := numStyle.Render(fmt.Sprintf("%*s │ ", numWidth, ""))

	plain := make([]string, 0, len(cv.layout.rows))
	var b strings.Builder
	for i := range cv.lineCount() {
		_, isLinked := cv.links[i]
		start, end := cv.lineRows(i)
		for r := start; r < end; r++ {
			b.Reset()
			first := r == start

			// Line number
			if cv.lineNumbers {
				if first {
					b.WriteString(numStyle.Render(fmt.Sprintf("%*d │ ", numWidth, i+1)))
				} else {
					b.WriteString(blankNum)
				}
			}

			// Link indicator prefix (or spacer for alignment)
			if isLinked && first {
				b.WriteString(linkPrefix)
			} else if hasLinks {
				b.WriteString(linkSpacer)
			}

			b.WriteString(cv.layout.rows[r])
			plain = append(plain, b.String())
		}
	}

	cv.screen = contentScreen{
		valid: true,
		theme: ActiveTheme.Name,
		plain: plain,
		shown: slices.Clone(plain),
	}
	// The layout already cut the rows to the width, so the viewport must
	// not wrap them again. Then one viewport line is one row, and the
	// offset counts rows.
	cv.viewport.SoftWrap = false
	cv.viewport.SetContentLines(cv.screen.shown)
}

// paintHighlight puts the cursor or the selection background on its rows
// in shown, and puts back the plain rows that had it before. It fills each
// painted row with the background up to padTo cells of text.
func (cv *ContentView) paintHighlight(padTo int) {
	s := &cv.screen
	copy(s.shown[s.from:s.to], s.plain[s.from:s.to])

	t := ActiveTheme
	bg := ansi.NewStyle().BackgroundColor(t.Overlay).String()
	if cv.visualMode {
		bg = ansi.NewStyle().BackgroundColor(t.Secondary).String()
	}
	lo, hi := cv.selectionRange()
	s.from, _ = cv.lineRows(lo)
	_, s.to = cv.lineRows(hi)

	var b strings.Builder
	for r := s.from; r < s.to; r++ {
		row := cv.layout.rows[r]
		// The plain row is the gutter and prefix, then the text.
		head := s.plain[r][:len(s.plain[r])-len(row)]

		b.Reset()
		b.WriteString(head)
		// Chroma ends each token with a reset, which also removes the
		// background. Set the background again after each reset, and fill
		// the rest of the row with it.
		b.WriteString(bg)
		b.WriteString(ReapplyAfterResets(row, bg))
		if pad := padTo - ansi.StringWidth(row); pad > 0 {
			b.WriteString(strings.Repeat(" ", pad))
		}
		b.WriteString(ansi.ResetStyle)
		s.shown[r] = b.String()
	}

	// The viewport keeps no rows for one empty row, so it does not hold
	// shown. Then give it the painted row.
	if len(s.plain) == 1 && ansi.StringWidth(s.plain[0]) == 0 {
		cv.viewport.SetContentLines(s.shown)
	}
}

// highlight runs chroma on the text when the text or the chroma style
// changed. It makes one entry in hlLines for each line of the text.
func (cv *ContentView) highlight() {
	style := ActiveTheme.ChromaStyle
	if cv.hlLines != nil && cv.hlStyle == style {
		return
	}

	// Highlight the safe text: AWS content can hold terminal control
	// sequences, and chroma keeps them.
	hl := strings.Split(syntaxHighlight(cv.text, string(cv.format)), "\n")

	// Some lexers add a line feed at the end. Keep one entry for each line,
	// so that the line numbers agree with the cursor and with yank.
	n := cv.lineCount()
	if len(hl) > n {
		hl = hl[:n]
	}
	for len(hl) < n {
		hl = append(hl, "")
	}

	cv.hlLines = hl
	cv.hlStyle = style
	cv.layout.valid = false
}

// buildLayout cuts the highlighted lines into rows of lineWidth cells when
// wrap is on. It does nothing when the layout is still good.
func (cv *ContentView) buildLayout(lineWidth int) {
	wrap := !cv.wrapOff
	l := &cv.layout
	if l.valid && l.lineWidth == lineWidth && l.wrap == wrap {
		return
	}

	rows := make([]string, 0, len(cv.hlLines))
	lineStart := make([]int, 0, len(cv.hlLines)+1)
	for _, line := range cv.hlLines {
		lineStart = append(lineStart, len(rows))
		if !wrap {
			rows = append(rows, endRow(line))
			continue
		}
		// Wrap also cuts a word that is longer than the row, so no row
		// is wider than lineWidth.
		for row := range strings.SplitSeq(ansi.Wrap(line, lineWidth, ""), "\n") {
			rows = append(rows, endRow(row))
		}
	}
	lineStart = append(lineStart, len(rows))

	*l = contentLayout{
		valid:     true,
		lineWidth: lineWidth,
		wrap:      wrap,
		rows:      rows,
		lineStart: lineStart,
	}
	// The screen rows come from the layout rows.
	cv.screen.valid = false
}

// endRow adds a reset at the end of a row that has escape sequences. The
// wrap can cut a token in two, and then the token color would go on into
// the gutter of the next row.
func endRow(row string) string {
	if strings.Contains(row, "\x1b") {
		return row + ansi.ResetStyle
	}
	return row
}

// syntaxHighlight applies chroma syntax highlighting to content.
func syntaxHighlight(content, format string) string {
	lexer := lexers.Get(format)
	if lexer == nil {
		lexer = lexers.Analyse(content)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	style := styles.Get(ActiveTheme.ChromaStyle)
	if style == nil {
		style = styles.Fallback
	}

	formatter := formatters.Get("terminal256")
	if formatter == nil {
		formatter = formatters.Fallback
	}

	iterator, err := lexer.Tokenise(nil, content)
	if err != nil {
		return content
	}

	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return content
	}

	return buf.String()
}

// detectFormat guesses the content format from the filename and content.
func detectFormat(filename, content string) ContentFormat {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".json":
		return FormatJSON
	case ".yaml", ".yml":
		return FormatYAML
	case ".md", ".markdown":
		return FormatMarkdown
	case ".hcl", ".tf":
		return FormatHCL
	case ".sh", ".bash":
		return FormatShell
	case ".go":
		return FormatGo
	case ".py":
		return FormatPython
	}

	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return FormatJSON
	}
	if strings.HasPrefix(trimmed, "---") {
		return FormatYAML
	}
	if strings.HasPrefix(trimmed, "#!/") {
		return FormatShell
	}
	if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "##") {
		return FormatMarkdown
	}

	return FormatText
}

// formatToExt returns a file extension for temp files.
func formatToExt(f ContentFormat) string {
	switch f {
	case FormatJSON:
		return ".json"
	case FormatYAML:
		return ".yaml"
	case FormatMarkdown:
		return ".md"
	case FormatHCL:
		return ".hcl"
	case FormatShell:
		return ".sh"
	case FormatGo:
		return ".go"
	case FormatPython:
		return ".py"
	default:
		return ".txt"
	}
}

// getEditor returns the user's preferred editor.
func getEditor() string {
	if editor := os.Getenv("EDITOR"); editor != "" {
		return editor
	}
	if visual := os.Getenv("VISUAL"); visual != "" {
		return visual
	}
	if _, err := exec.LookPath("vim"); err == nil {
		return "vim"
	}
	if _, err := exec.LookPath("nano"); err == nil {
		return "nano"
	}
	if runtime.GOOS == "windows" {
		return "notepad"
	}
	return "vi"
}
