package ui

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The caps of a LogView (decision D4 of docs/design/cwlogs.md). When a cap
// is reached, Append drops the oldest lines, and Prepend adds only the lines
// that fit.
const (
	LogMaxLines = 10000
	LogMaxBytes = 32 << 20
)

const (
	// logShowMax is the part of a message, in bytes, that a row can show.
	// A message can be up to 1 MB. The rest shows only in the detail.
	logShowMax = 64 << 10
	// logEntryCost is the bytes that each line costs on top of its text,
	// for the byte cap.
	logEntryCost = 64
	// logSourceMax is the widest source column, in cells.
	logSourceMax = 32
	// logSourceMinWidth is the narrowest screen that shows the source
	// column.
	logSourceMinWidth = 70
	logTimeShort      = "15:04:05.000"
	logTimeLong       = "2006-01-02 15:04:05.000"
)

// LogLine is one line of a LogView, for example one log event.
type LogLine struct {
	Time    time.Time
	Source  string // where the line comes from, for example a log stream
	Message string // the text as it came, for a copy and for the detail
	ID      string // an ID for the caller, for example an event ID
	// Data is kept with the line for the caller, for example the whole
	// event for its detail. LogView does not read it, and the byte cap
	// does not count it.
	Data any
}

// LogViewFilterSource is the Source of the FilterChangedMsg of the filter
// of a LogView. A view drops such a message, because LogView applies the
// filter as the user types.
const LogViewFilterSource = "logview"

// LogLineOpenedMsg is sent when the user presses enter on a line.
type LogLineOpenedMsg struct {
	Line LogLine
}

// logEntry is a line with the text that the rows show. LogView makes the
// text safe once, when the line comes in.
type logEntry struct {
	line  LogLine
	show  string   // the message made safe, cut to logShowMax bytes
	parts []string // show split at the line feeds
	src   string   // the source made safe
	cut   bool     // show holds only the start of the message
	cost  int      // the bytes that the line counts for the byte cap
}

func newLogEntry(l LogLine) logEntry {
	msg := strings.TrimRight(l.Message, "\r\n")
	cut := false
	if len(msg) > logShowMax {
		i := logShowMax
		for i > 0 && !utf8.RuneStart(msg[i]) {
			i--
		}
		msg, cut = msg[:i], true
	}
	show := SanitizeLines(msg)
	if strings.Contains(show, "\t") {
		show = strings.ReplaceAll(show, "\t", "    ")
	}
	return logEntry{
		line:  l,
		show:  show,
		parts: strings.Split(show, "\n"),
		src:   Sanitize(l.Source),
		cut:   cut,
		cost:  len(l.Message) + len(l.Source) + len(l.ID) + logEntryCost,
	}
}

// more reports whether the rows cannot show all of the message.
func (e *logEntry) more() bool { return len(e.parts) > 1 || e.cut }

// logStyles holds the escape sequences of the styles that a LogView uses.
// It is made again when the theme or the NoColor setting changes.
type logStyles struct {
	valid   bool
	theme   string
	noColor bool
	time    string
	source  string
	err     string
	warn    string
	cursor  string
	visual  string
}

// logLevelWords finds the words that mark an error or a warning.
var logLevelWords = regexp.MustCompile(`\b(?:ERROR|FATAL|CRITICAL|PANIC|WARN|WARNING|\w*Exception)\b`)

// LogView shows lines of a log, oldest first, with a cursor. It holds the
// lines in a ring with two caps: LogMaxLines lines and LogMaxBytes bytes.
// Append adds new lines at the end and Prepend adds older lines at the top.
// Neither changes the lines that it already has.
//
// LogView styles only the rows on the screen, each time it draws. So a
// resize and a theme switch cost nothing until the next draw.
//
// The cursor, the top of the screen and the visual anchor are the numbers
// of lines. The first line that LogView ever held has number 0. Prepend
// gives numbers below it, and Append gives numbers above. So these keep
// their line when lines come in or drop out.
type LogView struct {
	keys LogViewKeyMap

	entries  []logEntry
	base     int // the number of entries[0]
	bytes    int // the cost of all entries
	dropped  int // lines dropped from the top for the caps
	maxLines int
	maxBytes int

	cursor int // the number of the cursor line
	top    int // the number of the line at the top of the screen
	anchor int // the number of the line where visual mode started

	follow bool
	wrap   bool
	visual bool

	filter  Filter
	query   string // the filter text in lower case
	visible []int  // the numbers of the lines that match query, in order

	width, height int
	srcWidth      int // the widest source, at most logSourceMax
	longTimes     bool
	loc           *time.Location
	emptyText     string
	note          string // for example "3 lines copied"

	st *logStyles
}

// NewLogView returns an empty LogView that follows the end.
func NewLogView() LogView {
	return LogView{
		keys:      DefaultLogViewKeyMap(),
		maxLines:  LogMaxLines,
		maxBytes:  LogMaxBytes,
		follow:    true,
		filter:    NewFilterWithPrompt("/", "filter lines...").WithSource(LogViewFilterSource),
		width:     80,
		height:    10,
		loc:       time.Local,
		emptyText: "No lines",
		st:        &logStyles{},
	}
}

// SetCaps changes the caps. A test uses it with small caps.
func (lv *LogView) SetCaps(lines, bytes int) {
	lv.maxLines, lv.maxBytes = max(lines, 1), max(bytes, 1)
	lv.trimTop()
	lv.settle()
}

// SetSize sets the size of the view, the status row included.
func (lv *LogView) SetSize(w, h int) {
	lv.width, lv.height = max(w, 1), max(h, 2)
	lv.filter.SetWidth(lv.width)
	lv.settle()
}

// SetLocation sets the zone of the time column. The default is local time.
func (lv *LogView) SetLocation(loc *time.Location) {
	if loc != nil {
		lv.loc = loc
	}
}

// SetLongTimes shows the date in the time column, for a range of more than
// one day.
func (lv *LogView) SetLongTimes(v bool) { lv.longTimes = v }

// SetEmptyText sets the text that shows when the view has no lines.
func (lv *LogView) SetEmptyText(s string) { lv.emptyText = Sanitize(s) }

// SetFollow turns follow on or off. With follow on, new lines move the
// cursor to the end when it is on the last line.
func (lv *LogView) SetFollow(v bool) { lv.follow = v }

// Following reports whether follow is on.
func (lv *LogView) Following() bool { return lv.follow }

// Wrapped reports whether wrap is on.
func (lv *LogView) Wrapped() bool { return lv.wrap }

// Len returns the number of lines that the view holds.
func (lv *LogView) Len() int { return len(lv.entries) }

// Dropped returns the number of old lines that the caps dropped.
func (lv *LogView) Dropped() int { return lv.dropped }

// FilterText returns the text of the local filter.
func (lv *LogView) FilterText() string { return lv.filter.Value() }

// CapturingInput reports whether the filter input has the keys.
func (lv *LogView) CapturingInput() bool { return lv.filter.Active() }

// InVisualMode reports whether visual select is on.
func (lv *LogView) InVisualMode() bool { return lv.visual }

// WantsEsc reports whether esc has a use in the view: it ends visual mode,
// or it clears the filter. Else the caller can use esc to go back.
func (lv *LogView) WantsEsc() bool {
	return lv.filter.Active() || lv.visual || lv.query != ""
}

// Selected returns the cursor line.
func (lv *LogView) Selected() (LogLine, bool) {
	p := lv.posNear(lv.cursor)
	if p < 0 {
		return LogLine{}, false
	}
	return lv.entryAt(p).line, true
}

// Clear removes all lines and the count of dropped lines, and turns follow
// on. The filter stays, also while the user types it, so a reload does not
// send the next keys to the view.
func (lv *LogView) Clear() {
	clear(lv.entries)
	lv.entries = lv.entries[:0]
	lv.base, lv.bytes, lv.dropped, lv.srcWidth = 0, 0, 0, 0
	lv.cursor, lv.top, lv.anchor = 0, 0, 0
	lv.visual, lv.follow, lv.note = false, true, ""
	if lv.query != "" {
		lv.visible = []int{}
	}
}

// Restyle makes the styles again with the active theme (Restyler).
func (lv *LogView) Restyle() { lv.st.valid = false }

// Append adds lines at the end, in the order given. When the view has more
// lines or bytes than its caps, it drops the oldest lines. With follow on
// and the cursor on the last line, the cursor moves to the new last line.
func (lv *LogView) Append(lines []LogLine) {
	if len(lines) == 0 {
		return
	}
	empty := lv.count() == 0
	atEnd := !empty && lv.posNear(lv.cursor) == lv.count()-1
	for _, l := range lines {
		e := newLogEntry(l)
		n := lv.base + len(lv.entries)
		lv.entries = append(lv.entries, e)
		lv.bytes += e.cost
		lv.noteSource(&e)
		if lv.query != "" && e.matches(lv.query) {
			lv.visible = append(lv.visible, n)
		}
	}
	lv.trimTop()
	switch {
	// In visual mode, follow waits, so that the selection does not grow.
	case lv.follow && !lv.visual && (empty || atEnd):
		lv.cursor = lv.lastNumber()
	case empty:
		lv.cursor, lv.top = lv.base, lv.base
	}
	lv.settle()
}

// Prepend adds older lines at the top. lines must be oldest first, and
// older than the lines in the view. The cursor stays on its line. When the
// caps leave no room for all lines, Prepend keeps the newest lines that fit.
// It returns the number of lines that it added. After the caps dropped
// lines, it adds none, because an older line would leave a gap.
func (lv *LogView) Prepend(lines []LogLine) int {
	if len(lines) == 0 || lv.dropped > 0 {
		return 0
	}
	room := lv.maxLines - len(lv.entries)
	bytes := lv.bytes
	add := make([]logEntry, 0, min(len(lines), max(room, 0)))
	for i := len(lines) - 1; i >= 0 && len(add) < room; i-- {
		e := newLogEntry(lines[i])
		if bytes+e.cost > lv.maxBytes && (len(add) > 0 || len(lv.entries) > 0) {
			break
		}
		bytes += e.cost
		add = append(add, e)
	}
	if len(add) == 0 {
		return 0
	}
	empty := lv.count() == 0
	k := len(add)
	entries := make([]logEntry, 0, k+len(lv.entries))
	for i := k - 1; i >= 0; i-- {
		entries = append(entries, add[i])
		lv.noteSource(&add[i])
	}
	lv.entries = append(entries, lv.entries...)
	lv.bytes = bytes
	lv.base -= k
	if lv.query != "" {
		var front []int
		for i := range k {
			if lv.entries[i].matches(lv.query) {
				front = append(front, lv.base+i)
			}
		}
		lv.visible = append(front, lv.visible...)
	}
	if empty {
		lv.cursor, lv.top = lv.base, lv.base
		if lv.follow {
			lv.cursor = lv.lastNumber()
		}
	}
	lv.settle()
	return k
}

// trimTop drops the oldest lines until the view keeps both caps. It keeps
// at least one line.
func (lv *LogView) trimTop() {
	k, bytes := 0, lv.bytes
	for len(lv.entries)-k > 1 && (len(lv.entries)-k > lv.maxLines || bytes > lv.maxBytes) {
		bytes -= lv.entries[k].cost
		k++
	}
	if k == 0 {
		return
	}
	// The array keeps the dropped entries until append makes a new one, so
	// drop their text now.
	clear(lv.entries[:k])
	lv.entries = lv.entries[k:]
	lv.bytes = bytes
	lv.base += k
	lv.dropped += k
	if lv.query != "" {
		i := sort.SearchInts(lv.visible, lv.base)
		lv.visible = lv.visible[i:]
	}
}

func (lv *LogView) noteSource(e *logEntry) {
	if e.src != "" {
		lv.srcWidth = max(lv.srcWidth, min(ansi.StringWidth(e.src), logSourceMax))
	}
}

func (e *logEntry) matches(query string) bool {
	return strings.Contains(strings.ToLower(e.src), query) ||
		strings.Contains(strings.ToLower(e.show), query)
}

// setQuery sets the filter. The cursor moves to the nearest line that
// matches, at or after the old cursor line.
func (lv *LogView) setQuery(q string) {
	q = strings.ToLower(q)
	if q == lv.query {
		return
	}
	lv.query = q
	lv.visible = nil
	if q != "" {
		lv.visible = []int{}
		for i := range lv.entries {
			if lv.entries[i].matches(q) {
				lv.visible = append(lv.visible, lv.base+i)
			}
		}
	}
	lv.visual = false
	if p := lv.posNear(lv.cursor); p >= 0 {
		lv.cursor = lv.numberAt(p)
	}
	lv.settle()
}

// count returns the number of lines that show: all lines, or the lines that
// match the filter.
func (lv *LogView) count() int {
	if lv.query == "" {
		return len(lv.entries)
	}
	return len(lv.visible)
}

// numberAt returns the number of the line at position p of the lines that
// show.
func (lv *LogView) numberAt(p int) int {
	if lv.query == "" {
		return lv.base + p
	}
	return lv.visible[p]
}

func (lv *LogView) entryAt(p int) *logEntry {
	return &lv.entries[lv.numberAt(p)-lv.base]
}

func (lv *LogView) lastNumber() int {
	if n := lv.count(); n > 0 {
		return lv.numberAt(n - 1)
	}
	return lv.base
}

// posNear returns the position of line number n among the lines that show.
// When line n does not show, it returns the next line that shows, or the
// last line. It returns -1 when no line shows.
func (lv *LogView) posNear(n int) int {
	c := lv.count()
	if c == 0 {
		return -1
	}
	var p int
	if lv.query == "" {
		p = n - lv.base
	} else {
		p = sort.SearchInts(lv.visible, n)
	}
	return min(max(p, 0), c-1)
}

// bodyHeight returns the rows for lines, without the status row.
func (lv *LogView) bodyHeight() int { return max(lv.height-1, 1) }

// layout returns the widths of the columns. A width of 0 hides the column.
func (lv *LogView) layout() (timeW, srcW, msgW int) {
	timeW = len(logTimeShort)
	if lv.longTimes {
		timeW = len(logTimeLong)
	}
	if lv.width < timeW+1+8 {
		timeW = 0
	}
	if lv.srcWidth > 0 && lv.width >= logSourceMinWidth {
		srcW = lv.srcWidth
	}
	msgW = lv.width
	if timeW > 0 {
		msgW -= timeW + 1
	}
	if srcW > 0 {
		msgW -= srcW + 1
	}
	return timeW, srcW, max(msgW, 1)
}

// messageRows returns the rows of the message of e, at most limit rows.
// With wrap off, it is one row: the first line of the message, with a ↵
// mark when the message has more. With wrap on, it is all lines of the
// message, wrapped, and the last row ends with … when rows are left out.
func (lv *LogView) messageRows(e *logEntry, msgW, limit int) []string {
	if !lv.wrap {
		if !e.more() {
			return []string{cutCells(e.parts[0], msgW)}
		}
		if msgW < 3 {
			return []string{cutCells(e.parts[0], msgW)}
		}
		return []string{cutCells(e.parts[0], msgW-2) + " ↵"}
	}
	var rows []string
	more := e.cut
	for _, part := range e.parts {
		if len(rows) >= limit {
			more = true
			break
		}
		// A row holds at most 4 bytes for each cell, so a longer part
		// cannot fill the rows that are left. The text after a cut part
		// does not show.
		cut := false
		if keep := msgW * 4 * (limit - len(rows)); len(part) > keep {
			part, cut = part[:runeStartBefore(part, keep)], true
		}
		for row := range strings.SplitSeq(ansi.Wrap(part, msgW, ""), "\n") {
			if len(rows) >= limit {
				more = true
				break
			}
			// Wrap cannot split a wide character, so a row of one cell
			// can still be too wide.
			rows = append(rows, cutCells(row, msgW))
		}
		if cut {
			more = true
			break
		}
	}
	if more && msgW >= 3 {
		last := len(rows) - 1
		rows[last] = cutCells(rows[last], msgW-2) + " …"
	}
	return rows
}

// cutCells cuts plain text s to w cells, with … at the end when it cuts.
// s must hold no escape sequences.
func cutCells(s string, w int) string {
	if w <= 0 {
		return ""
	}
	// A cell takes at most 4 bytes for most text, so skip the rest before
	// the slow width count. Text such as combining marks takes more bytes
	// for each cell, so a cut piece always gets the … mark.
	if limit := w*4 + 4; len(s) > limit {
		s = s[:runeStartBefore(s, limit)]
		return ansi.Truncate(s, w-1, "") + "…"
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// runeStartBefore returns the largest i <= n where a character of s starts.
func runeStartBefore(s string, n int) int {
	for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
		n--
	}
	return n
}

// entryHeight returns the rows of the line at position p.
func (lv *LogView) entryHeight(p int) int {
	if !lv.wrap {
		return 1
	}
	_, _, msgW := lv.layout()
	return len(lv.messageRows(lv.entryAt(p), msgW, lv.bodyHeight()))
}

// settle puts the cursor and the top of the screen on lines that show, and
// scrolls so that the cursor line is on the screen. When the lines after
// the top do not fill the screen, it shows more lines above.
func (lv *LogView) settle() {
	c := lv.count()
	if c == 0 {
		return
	}
	p := lv.posNear(lv.cursor)
	lv.cursor = lv.numberAt(p)
	tp := lv.posNear(lv.top)
	h := lv.bodyHeight()
	if p < tp {
		tp = p
	} else {
		// The first position from which the cursor line still fits.
		used, q := 0, p
		for q >= 0 {
			eh := lv.entryHeight(q)
			if used+eh > h && q != p {
				break
			}
			used += eh
			q--
		}
		tp = max(tp, q+1)
	}
	// Fill the screen: move the top up while the lines from the top to the
	// end leave rows empty.
	used := 0
	for q := tp; q < c && used < h; q++ {
		used += lv.entryHeight(q)
	}
	for tp > 0 {
		eh := lv.entryHeight(tp - 1)
		if used+eh > h {
			break
		}
		used += eh
		tp--
	}
	lv.top = lv.numberAt(tp)
	if lv.visual {
		lv.anchor = lv.numberAt(lv.posNear(lv.anchor))
	}
}

func (lv *LogView) moveTo(p int) {
	if c := lv.count(); c > 0 {
		lv.cursor = lv.numberAt(min(max(p, 0), c-1))
		lv.settle()
	}
}

// selection returns the first and last positions of the visual selection,
// or the cursor position twice.
func (lv *LogView) selection() (int, int) {
	p := lv.posNear(lv.cursor)
	if !lv.visual {
		return p, p
	}
	a := lv.posNear(lv.anchor)
	return min(a, p), max(a, p)
}

// Update handles the keys of the view. It returns a command for a copy and
// for enter on a line.
func (lv *LogView) Update(msg tea.Msg) tea.Cmd {
	if _, paste := msg.(tea.PasteMsg); paste && lv.filter.Active() {
		var cmd tea.Cmd
		lv.filter, cmd = lv.filter.Update(msg)
		lv.setQuery(lv.filter.Value())
		return cmd
	}
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	if lv.filter.Active() {
		// The filter applies at once. Its FilterChangedMsg, with the source
		// LogViewFilterSource, can come after a later key, so the caller
		// drops it.
		var cmd tea.Cmd
		lv.filter, cmd = lv.filter.Update(km)
		lv.setQuery(lv.filter.Value())
		return cmd
	}
	lv.note = ""
	k := lv.keys
	p := lv.posNear(lv.cursor)
	switch {
	case key.Matches(km, k.CursorDown):
		lv.moveTo(p + 1)
	case key.Matches(km, k.CursorUp):
		lv.follow = false
		lv.moveTo(p - 1)
	case key.Matches(km, k.HalfPageDown):
		lv.moveTo(p + max(lv.bodyHeight()/2, 1))
	case key.Matches(km, k.HalfPageUp):
		lv.follow = false
		lv.moveTo(p - max(lv.bodyHeight()/2, 1))
	case key.Matches(km, k.GotoTop):
		lv.follow = false
		lv.moveTo(0)
	case key.Matches(km, k.GotoBottom):
		lv.follow = true
		lv.moveTo(lv.count() - 1)
	case key.Matches(km, k.WrapToggle):
		lv.wrap = !lv.wrap
		lv.settle()
	case key.Matches(km, k.Filter):
		lv.filter.Activate()
	case key.Matches(km, k.VisualToggle):
		if p >= 0 {
			lv.visual = !lv.visual
			lv.anchor = lv.cursor
		}
	case key.Matches(km, k.Yank):
		return lv.yank()
	case key.Matches(km, k.Open):
		if l, ok := lv.Selected(); ok {
			return func() tea.Msg { return LogLineOpenedMsg{Line: l} }
		}
	case key.Matches(km, k.Esc):
		switch {
		case lv.visual:
			lv.visual = false
		case lv.query != "":
			lv.filter.Deactivate()
			lv.setQuery("")
		}
	}
	return nil
}

// yank copies the messages of the selected lines, one line each, and ends
// visual mode.
func (lv *LogView) yank() tea.Cmd {
	lo, hi := lv.selection()
	if lo < 0 {
		return nil
	}
	msgs := make([]string, 0, hi-lo+1)
	for q := lo; q <= hi; q++ {
		msgs = append(msgs, strings.TrimRight(lv.entryAt(q).line.Message, "\r\n"))
	}
	n := len(msgs)
	lv.visual = false
	lv.note = fmt.Sprintf("%d line(s) copied", n)
	return tea.Batch(
		tea.SetClipboard(strings.Join(msgs, "\n")),
		func() tea.Msg { return YankedMsg{Lines: n} },
	)
}

// styles returns the escape sequences of the active theme. It makes them
// again after Restyle, a theme switch or a NoColor change.
func (lv *LogView) styles() *logStyles {
	t := ActiveTheme
	st := lv.st
	if st.valid && st.theme == t.Name && st.noColor == NoColor() {
		return st
	}
	*st = logStyles{valid: true, theme: t.Name, noColor: NoColor()}
	if NoColor() {
		// Without colors, a background and a color do not show.
		st.cursor = ansi.NewStyle().Reverse(true).String()
		st.visual = st.cursor
		st.err = ansi.NewStyle().Bold().String()
		st.warn = st.err
		return st
	}
	st.time = ansi.NewStyle().ForegroundColor(t.Muted).String()
	st.source = ansi.NewStyle().ForegroundColor(t.Info).String()
	st.err = ansi.NewStyle().ForegroundColor(t.Error).Bold().String()
	st.warn = ansi.NewStyle().ForegroundColor(t.Warning).String()
	st.cursor = ansi.NewStyle().BackgroundColor(t.Overlay).String()
	st.visual = ansi.NewStyle().BackgroundColor(t.Secondary).String()
	return st
}

// paint returns s in the style seq, or s when seq is empty.
func paint(seq, s string) string {
	if seq == "" || s == "" {
		return s
	}
	return seq + s + ansi.ResetStyle
}

// levels styles the words for errors and warnings in a row.
func levels(st *logStyles, row string) string {
	if !strings.ContainsAny(row, "EFCPWx") {
		return row
	}
	return logLevelWords.ReplaceAllStringFunc(row, func(w string) string {
		if strings.HasPrefix(w, "WARN") {
			return paint(st.warn, w)
		}
		return paint(st.err, w)
	})
}

// padCells fills s with spaces up to w cells.
func padCells(s string, w int) string {
	if pad := w - ansi.StringWidth(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// View draws the lines on the screen and the status row. The filter input
// takes the place of the status row while it is open. The output has
// exactly the height of the view, and no row is wider than the view.
func (lv *LogView) View() string {
	st := lv.styles()
	h := lv.bodyHeight()
	out := make([]string, 0, lv.height)

	c := lv.count()
	if c == 0 {
		text := lv.emptyText
		if lv.query != "" && len(lv.entries) > 0 {
			text = "No lines match the filter"
		}
		out = append(out, paint(st.time, cutCells(text, lv.width)))
	} else {
		lv.settle()
		timeW, srcW, msgW := lv.layout()
		lo, hi := lv.selection()
		blankTime := strings.Repeat(" ", timeW)
		blankSrc := strings.Repeat(" ", srcW)
		for p := lv.posNear(lv.top); p < c && len(out) < h; p++ {
			e := lv.entryAt(p)
			rows := lv.messageRows(e, msgW, h)
			for i, msgRow := range rows {
				if len(out) >= h {
					break
				}
				var b strings.Builder
				if timeW > 0 {
					if i == 0 {
						b.WriteString(paint(st.time, lv.timeText(e.line.Time, timeW)))
					} else {
						b.WriteString(blankTime)
					}
					b.WriteByte(' ')
				}
				if srcW > 0 {
					if i == 0 {
						b.WriteString(paint(st.source, padCells(cutCells(e.src, srcW), srcW)))
					} else {
						b.WriteString(blankSrc)
					}
					b.WriteByte(' ')
				}
				b.WriteString(levels(st, msgRow))
				row := b.String()
				if p >= lo && p <= hi {
					bg := st.cursor
					if lv.visual {
						bg = st.visual
					}
					row = bg + ReapplyAfterResets(row, bg) +
						strings.Repeat(" ", max(lv.width-ansi.StringWidth(row), 0)) + ansi.ResetStyle
				}
				out = append(out, row)
			}
		}
	}
	for len(out) < h {
		out = append(out, "")
	}
	if lv.filter.Active() {
		// The filter row has styles, so it needs the cut that knows escape
		// sequences.
		out = append(out, ansi.Truncate(lv.filter.View(), lv.width, "")+ansi.ResetStyle)
	} else {
		out = append(out, paint(st.time, cutCells(lv.status(), lv.width)))
	}
	return strings.Join(out, "\n")
}

// timeText formats t for the time column, or blanks when t is zero.
func (lv *LogView) timeText(t time.Time, w int) string {
	if t.IsZero() {
		return strings.Repeat(" ", w)
	}
	layout := logTimeShort
	if lv.longTimes {
		layout = logTimeLong
	}
	return t.In(lv.loc).Format(layout)
}

// status returns the text of the status row.
func (lv *LogView) status() string {
	var parts []string
	c := lv.count()
	if c > 0 {
		pos := fmt.Sprintf("%d/%d", lv.posNear(lv.cursor)+1, c)
		if lv.query != "" {
			pos += fmt.Sprintf(" of %d", len(lv.entries))
		}
		parts = append(parts, pos)
	}
	if lv.query != "" {
		parts = append(parts, "filter: "+Sanitize(lv.filter.Value()))
	}
	if lv.follow {
		parts = append(parts, "follow")
	}
	if lv.wrap {
		parts = append(parts, "wrap")
	}
	if lv.visual {
		lo, hi := lv.selection()
		parts = append(parts, fmt.Sprintf("VISUAL (%d lines)", hi-lo+1))
	}
	if lv.dropped > 0 {
		parts = append(parts, fmt.Sprintf("%d older lines dropped", lv.dropped))
	}
	if lv.note != "" {
		parts = append(parts, lv.note)
	}
	return strings.Join(parts, " · ")
}
