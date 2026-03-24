package ui

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/atotto/clipboard"
	"github.com/muesli/reflow/wordwrap"
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
type ContentView struct {
	viewport    viewport.Model
	title       string
	raw         string        // original unhighlighted content
	rawLines    []string      // raw split into lines
	format      ContentFormat // resolved format
	lineNumbers bool
	cursor      int  // current cursor line (0-indexed)
	visualMode  bool // visual line selection active
	visualStart int  // anchor line for visual selection
	width       int
	height      int
	yankMsg     string // transient "yanked N lines" feedback
}

// NewContentView creates a content viewer with the given title, content, and format.
func NewContentView(title, content string, format ContentFormat) ContentView {
	vp := viewport.New()
	cv := ContentView{
		viewport:    vp,
		title:       title,
		raw:         content,
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

// SetSize sets the viewer dimensions.
func (cv *ContentView) SetSize(w, h int) {
	cv.width = w
	cv.height = h
	cv.viewport.SetWidth(w)
	cv.viewport.SetHeight(h - 2)
	cv.renderContent()
}

// SetContent replaces the content.
func (cv *ContentView) SetContent(title, content string, format ContentFormat) {
	cv.title = title
	cv.raw = content
	cv.rawLines = strings.Split(content, "\n")
	cv.cursor = 0
	cv.visualMode = false
	if format == FormatAuto {
		cv.format = detectFormat(title, content)
	} else {
		cv.format = format
	}
	cv.renderContent()
}

// ToggleLineNumbers toggles line number display.
func (cv *ContentView) ToggleLineNumbers() {
	cv.lineNumbers = !cv.lineNumbers
	cv.renderContent()
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

	if _, err := tmpFile.WriteString(cv.raw); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return func() tea.Msg { return EditorFinishedMsg{Err: err} }
	}
	tmpFile.Close()

	editor := getEditor()
	c := exec.Command(editor, tmpFile.Name())
	return tea.ExecProcess(c, func(err error) tea.Msg {
		os.Remove(tmpFile.Name())
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

	err := clipboard.WriteAll(text)
	if err != nil {
		cv.yankMsg = "yank failed: " + err.Error()
	}

	cv.renderContent()
	return func() tea.Msg { return YankedMsg{Lines: count} }
}

func (cv *ContentView) ensureCursorVisible() {
	vpHeight := cv.viewport.Height()
	yOffset := cv.viewport.YOffset()

	if cv.cursor < yOffset {
		cv.viewport.SetYOffset(cv.cursor)
	} else if cv.cursor >= yOffset+vpHeight {
		cv.viewport.SetYOffset(cv.cursor - vpHeight + 1)
	}
}

// Update handles cursor movement, visual selection, yank, and viewport scrolling.
func (cv ContentView) Update(msg tea.Msg) (ContentView, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		key := msg.String()

		switch key {
		// Cursor movement
		case "j", "down":
			cv.cursor++
			cv.clampCursor()
			cv.renderContent()
			cv.ensureCursorVisible()
			return cv, nil
		case "k", "up":
			cv.cursor--
			cv.clampCursor()
			cv.renderContent()
			cv.ensureCursorVisible()
			return cv, nil
		case "g":
			cv.cursor = 0
			cv.renderContent()
			cv.ensureCursorVisible()
			return cv, nil
		case "G":
			cv.cursor = cv.lineCount() - 1
			cv.renderContent()
			cv.ensureCursorVisible()
			return cv, nil
		case "ctrl+d":
			cv.cursor += cv.viewport.Height() / 2
			cv.clampCursor()
			cv.renderContent()
			cv.ensureCursorVisible()
			return cv, nil
		case "ctrl+u":
			cv.cursor -= cv.viewport.Height() / 2
			cv.clampCursor()
			cv.renderContent()
			cv.ensureCursorVisible()
			return cv, nil

		// Visual mode
		case "V":
			if cv.visualMode {
				cv.visualMode = false
			} else {
				cv.visualMode = true
				cv.visualStart = cv.cursor
			}
			cv.renderContent()
			return cv, nil

		// Yank
		case "y":
			cmd := cv.yankSelection()
			return cv, cmd

		// Line numbers toggle
		case "n":
			cv.ToggleLineNumbers()
			return cv, nil
		}
	}

	// Don't pass j/k/g/G to viewport — we handle scrolling via cursor
	return cv, nil
}

// View renders the content viewer.
func (cv ContentView) View() string {
	t := ActiveTheme

	// Title bar
	titleText := S.Title.Render(cv.title)
	formatBadge := lipgloss.NewStyle().
		Foreground(t.BrightText).
		Background(t.Secondary).
		Padding(0, 1).
		Render(string(cv.format))

	posInfo := lipgloss.NewStyle().Foreground(t.Muted).Render(
		fmt.Sprintf(" Ln %d/%d  %.0f%%", cv.cursor+1, cv.lineCount(), cv.viewport.ScrollPercent()*100),
	)

	modeInfo := ""
	if cv.visualMode {
		lo, hi := cv.selectionRange()
		modeInfo = lipgloss.NewStyle().Foreground(t.Warning).Bold(true).Render(
			fmt.Sprintf("  VISUAL (%d lines)", hi-lo+1),
		)
	}

	yankInfo := ""
	if cv.yankMsg != "" {
		yankInfo = "  " + lipgloss.NewStyle().Foreground(t.Accent).Render(cv.yankMsg)
	}

	header := titleText + "  " + formatBadge + posInfo + modeInfo + yankInfo

	// Footer
	hints := "j/k move  V visual  y yank  e editor  n lines  g/G top/bottom  esc back"
	footer := lipgloss.NewStyle().Foreground(t.Muted).Render(hints)

	return header + "\n" + cv.viewport.View() + "\n" + footer
}

func (cv *ContentView) renderContent() {
	width := cv.width
	if width <= 0 {
		width = 80
	}

	// Highlight each line individually so we can apply cursor/selection styling
	highlighted := syntaxHighlight(cv.raw, string(cv.format))
	hLines := strings.Split(highlighted, "\n")

	t := ActiveTheme
	numStyle := lipgloss.NewStyle().Foreground(t.Muted)
	numWidth := len(fmt.Sprintf("%d", len(hLines)))

	// Calculate the width available for line content (full width minus gutter)
	gutterWidth := 0
	if cv.lineNumbers {
		gutterWidth = numWidth + 3 // digits + " │ "
	}
	lineWidth := width - gutterWidth - 2
	if lineWidth < 10 {
		lineWidth = 10
	}

	cursorStyle := lipgloss.NewStyle().Background(t.Overlay).Width(lineWidth)
	selectStyle := lipgloss.NewStyle().Background(t.Secondary).Width(lineWidth)

	lo, hi := cv.selectionRange()

	var b strings.Builder
	for i, line := range hLines {
		// Line number
		if cv.lineNumbers {
			num := numStyle.Render(fmt.Sprintf("%*d │ ", numWidth, i+1))
			b.WriteString(num)
		}

		// Apply cursor/selection highlight
		if cv.visualMode && i >= lo && i <= hi {
			b.WriteString(selectStyle.Render(line))
		} else if i == cv.cursor {
			b.WriteString(cursorStyle.Render(line))
		} else {
			b.WriteString(line)
		}

		if i < len(hLines)-1 {
			b.WriteByte('\n')
		}
	}

	content := b.String()

	// ANSI-aware word wrap
	if width > 0 {
		content = wordwrap.String(content, width-2)
	}

	cv.viewport.SetContent(content)
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
	return "vi"
}
