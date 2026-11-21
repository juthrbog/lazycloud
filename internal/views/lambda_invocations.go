package views

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The recent invocations of a Lambda function, read from the REPORT lines
// of its log group. See docs/design/lambda.md, "Recent invocations".

const (
	// lambdaInvocationLimit is the Limit of one FilterLogEvents call, and
	// lambdaInvocationPages the page budget: one load shows at most 1,000
	// rows.
	lambdaInvocationLimit = 100
	lambdaInvocationPages = 10
	// lambdaInvocationWindow is the length of one window of the walk.
	lambdaInvocationWindow = time.Hour
)

// The filter patterns of the REPORT lines in each log format (H9).
const (
	lambdaReportText = `"REPORT RequestId"`
	lambdaReportJSON = `{ $.type = "platform.report" }`
)

// lambdaInvocation is one invocation, from its REPORT line.
type lambdaInvocation struct {
	Time       time.Time
	Stream     string
	EventID    string
	RequestID  string
	DurationMS float64
	BilledMS   float64
	InitMS     float64 // 0: a warm start
	MemoryMB   int
	UsedMB     int
	Status     string // "ok", "error", "timeout", "failure", or "" when the line did not parse
	ErrorType  string
	Raw        string
	Parsed     bool
}

// key is the row key: the event ID, which is unique for each line. An
// async invocation that Lambda retries writes a REPORT line for each try,
// all with the same request ID, and each must stay a row.
func (in lambdaInvocation) key() string {
	if in.EventID != "" {
		return in.EventID
	}
	return in.RequestID + "@" + strconv.FormatInt(in.Time.UnixMilli(), 10)
}

func (in lambdaInvocation) cold() bool { return in.InitMS > 0 }

func (in lambdaInvocation) failed() bool { return in.Parsed && in.Status != "ok" }

// parseReport reads a REPORT line in the text or the JSON format. A line it
// cannot read gives Parsed false, with the raw text.
func parseReport(e aws.LogEvent) lambdaInvocation {
	in := lambdaInvocation{Time: e.Time, Stream: e.Stream, EventID: e.ID, Raw: e.Message}
	text := strings.TrimSpace(e.Message)
	if strings.HasPrefix(text, "{") {
		return parseReportJSON(in, text)
	}
	return parseReportText(in, text)
}

// parseReportText reads "REPORT RequestId: <id>\tDuration: 0.86 ms\t...":
// the fields are split by tabs, and the spacing around them differs.
func parseReportText(in lambdaInvocation, text string) lambdaInvocation {
	rest, ok := strings.CutPrefix(text, "REPORT ")
	if !ok {
		return in
	}
	in.Status = "ok"
	for field := range strings.SplitSeq(rest, "\t") {
		k, v, ok := strings.Cut(strings.TrimSpace(field), ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "RequestId":
			in.RequestID = v
		case "Duration":
			in.DurationMS = reportNumber(v)
		case "Billed Duration":
			in.BilledMS = reportNumber(v)
		case "Init Duration":
			in.InitMS = reportNumber(v)
		case "Memory Size":
			in.MemoryMB = int(reportNumber(v))
		case "Max Memory Used":
			in.UsedMB = int(reportNumber(v))
		case "Status":
			in.Status = strings.ToLower(v)
		case "Error Type":
			in.ErrorType = v
		}
	}
	in.Parsed = in.RequestID != ""
	if !in.Parsed {
		in.Status = ""
	}
	return in
}

// reportNumber reads "26.73 ms" or "128 MB".
func reportNumber(v string) float64 {
	n, _, _ := strings.Cut(v, " ")
	f, _ := strconv.ParseFloat(n, 64)
	return f
}

// parseReportJSON reads a platform.report object of the JSON format.
func parseReportJSON(in lambdaInvocation, text string) lambdaInvocation {
	var line struct {
		Type   string `json:"type"`
		Record struct {
			RequestID string `json:"requestId"`
			Status    string `json:"status"`
			ErrorType string `json:"errorType"`
			Metrics   struct {
				DurationMS       float64 `json:"durationMs"`
				BilledDurationMS float64 `json:"billedDurationMs"`
				InitDurationMS   float64 `json:"initDurationMs"`
				MemorySizeMB     int     `json:"memorySizeMB"`
				MaxMemoryUsedMB  int     `json:"maxMemoryUsedMB"`
			} `json:"metrics"`
		} `json:"record"`
	}
	if json.Unmarshal([]byte(text), &line) != nil || line.Type != "platform.report" || line.Record.RequestID == "" {
		return in
	}
	r := line.Record
	in.RequestID, in.ErrorType = r.RequestID, r.ErrorType
	in.DurationMS, in.BilledMS, in.InitMS = r.Metrics.DurationMS, r.Metrics.BilledDurationMS, r.Metrics.InitDurationMS
	in.MemoryMB, in.UsedMB = r.Metrics.MemorySizeMB, r.Metrics.MaxMemoryUsedMB
	in.Status = strings.ToLower(r.Status)
	if in.Status == "success" || in.Status == "" {
		in.Status = "ok"
	}
	in.Parsed = true
	return in
}

// invocationWalk is the token of the walk: the window and the AWS token
// within it. The list core keeps it as an opaque string.
type invocationWalk struct {
	Start int64   `json:"s"`           // ms
	End   int64   `json:"e,omitempty"` // ms; 0: open, the first window
	Token *string `json:"t,omitempty"`
}

func (w invocationWalk) encode() *string {
	b, _ := json.Marshal(w)
	s := "win:" + string(b)
	return &s
}

func decodeWalk(tok *string) (invocationWalk, bool) {
	var w invocationWalk
	if tok == nil {
		return w, false
	}
	raw, ok := strings.CutPrefix(*tok, "win:")
	if !ok || json.Unmarshal([]byte(raw), &w) != nil {
		return w, false
	}
	return w, true
}

// LambdaInvocations lists the recent invocations of a function.
type LambdaInvocations struct {
	listCore[lambdaInvocation]
	logs                   aws.LogsService
	function, logGroup     string
	logFormat, systemLevel string    // the logging config of the function
	floor                  time.Time // the oldest time that can hold lines
	missing                bool      // the log group does not exist
	lookedUp               bool
	// shared is true for a custom log group, which other functions can
	// write to as well. Their streams are named
	// "YYYY/MM/DD/<function>[<version>]<id>", so the list keeps only the
	// streams of this function.
	shared bool
}

// NewLambdaInvocations creates the invocation list from the route
// parameters function, log_group, log_format and system_log_level.
func NewLambdaInvocations(logs aws.LogsService, p map[string]string) *LambdaInvocations {
	v := &LambdaInvocations{logs: logs, function: p["function"], logGroup: p["log_group"],
		logFormat: p["log_format"], systemLevel: p["system_log_level"], floor: logsEpoch}
	v.shared = v.logGroup != "/aws/lambda/"+v.function
	v.listCore = newListCore(listConfig[lambdaInvocation]{
		Noun:         "invocations",
		LoadingLabel: "Loading invocations...",
		ErrContext:   "listing invocations",
		Key:          lambdaInvocation.key,
		Columns:      lambdaInvocationColumns,
		Row:          lambdaInvocationRow,
		Fetch:        v.fetch,
		FetchTimeout: callTimeout,
		FetchDetail: func(_ context.Context, in lambdaInvocation) (msg.TabbedContentMsg, error) {
			return lambdaInvocationDetail(v.function, in), nil
		},
		PageBudget: lambdaInvocationPages,
		MoreKey:    true,
	})
	return v
}

func (v *LambdaInvocations) ID() string    { return "lambda_invocations:" + v.function }
func (v *LambdaInvocations) Title() string { return v.function + " invocations" }

// Init looks the log group up first, for the floor of the walk (its
// creation and its retention) and to say when it does not exist.
func (v *LambdaInvocations) Init() tea.Cmd {
	if v.lookedUp {
		return v.init()
	}
	logs, ctx, group := v.logs, v.viewContext(), v.logGroup
	return func() tea.Msg {
		if logs == nil {
			return lambdaGroupMsg{err: errNoClient}
		}
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		g, err := logs.GetLogGroup(ctx, group)
		return lambdaGroupMsg{group: g, err: err}
	}
}

// lambdaGroupMsg is the result of the group lookup of the invocation list.
type lambdaGroupMsg struct {
	group *aws.LogGroup
	err   error
}

func (v *LambdaInvocations) onGroup(m lambdaGroupMsg) tea.Cmd {
	v.lookedUp = true
	now := logsNow()
	switch {
	case errors.Is(m.err, aws.ErrLogGroupNotFound):
		v.missing = true
		return nil
	case m.err == nil && m.group != nil:
		if !m.group.Created.IsZero() && m.group.Created.Add(-logsBackfill).After(v.floor) {
			v.floor = m.group.Created.Add(-logsBackfill)
		}
		if d := m.group.RetentionDays; d > 0 && now.Add(-time.Duration(d)*24*time.Hour).After(v.floor) {
			v.floor = now.Add(-time.Duration(d) * 24 * time.Hour)
		}
	}
	// Another lookup error: the walk stops only at 2024-01-01, and the load
	// shows its own error if the calls fail.
	return v.init()
}

// fetch reads one page of REPORT lines, newest first. When a window ends,
// it returns a token for the window before, until the floor.
func (v *LambdaInvocations) fetch(ctx context.Context, tok *string) ([]lambdaInvocation, *string, error) {
	if v.logs == nil {
		return nil, nil, errNoClient
	}
	w, ok := decodeWalk(tok)
	if !ok {
		w = invocationWalk{Start: logsNow().Add(-lambdaInvocationWindow).UnixMilli()}
	}
	floor := v.floor.UnixMilli()
	w.Start = max(w.Start, floor)
	q := aws.EventQuery{Group: v.logGroup, Pattern: v.pattern(), Start: time.UnixMilli(w.Start),
		Limit: lambdaInvocationLimit, Token: w.Token}
	if w.End != 0 {
		q.End = time.UnixMilli(w.End)
	}
	p, err := v.logs.FilterEvents(ctx, q)
	if err != nil {
		return nil, nil, err
	}
	items := make([]lambdaInvocation, 0, len(p.Events))
	for _, e := range slices.Backward(p.Events) { // the page is ascending
		if v.shared && !strings.Contains(e.Stream, "/"+v.function+"[") {
			continue // another function that writes to the same group
		}
		items = append(items, parseReport(e))
	}
	if !p.End {
		w.Token = p.Next
		return items, w.encode(), nil
	}
	if w.Start <= floor {
		return items, nil, nil
	}
	end := w.Start - 1 // both ends are inclusive
	next := invocationWalk{Start: max(w.Start-lambdaInvocationWindow.Milliseconds(), floor), End: end}
	return items, next.encode(), nil
}

// pattern is the filter pattern of the REPORT lines in the log format of
// the function.
func (v *LambdaInvocations) pattern() string {
	if strings.EqualFold(v.logFormat, "JSON") {
		return lambdaReportJSON
	}
	return lambdaReportText
}

func (v *LambdaInvocations) KeyMap() []ui.HintBinding {
	return v.hints(lambdaInvocationLines, lambdaKeys.Details, listKeys.More, listKeys.Sort, listKeys.Filter, listKeys.Refresh)
}

var lambdaInvocationLines = ui.NewHintBinding([]string{"enter"}, "enter", "log lines")

// Update returns the view itself. Enter opens the stream of the invocation
// with a range from its start to its REPORT line.
func (v *LambdaInvocations) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case lambdaGroupMsg:
		return v, v.onGroup(m)
	case tea.KeyPressMsg:
		if v.filter.Active() {
			break
		}
		switch {
		case v.missing && key.Matches(m, listKeys.Refresh.Binding):
			// The group can exist now, after the first invocation: look it
			// up again.
			v.missing, v.lookedUp = false, false
			return v, v.Init()
		case (!v.lookedUp || v.missing) && key.Matches(m, listKeys.Refresh.Binding, listKeys.More.Binding):
			// fetch reads the floor that the lookup sets, so no load starts
			// before the lookup ends.
			return v, nil
		case v.err == nil && key.Matches(m, lambdaInvocationLines.Binding):
			if it := v.cursorItem(); it != nil {
				return v, v.openLines(*it)
			}
		}
	}
	return v, v.update(m)
}

// openLines opens the lines of one invocation: its stream, from the REPORT
// time minus the duration and the init, minus one second, to the REPORT
// time. A stream runs one invocation at a time, so the range holds that
// invocation and its init. A filter on the request ID would miss lines
// that the function prints without it.
func (v *LambdaInvocations) openLines(in lambdaInvocation) tea.Cmd {
	took := time.Duration((in.DurationMS + in.InitMS) * float64(time.Millisecond))
	start := in.Time.Add(-took - time.Second)
	return navigate("logs_events", map[string]string{
		"log_group":    v.logGroup,
		"log_stream":   in.Stream,
		"log_start":    strconv.FormatInt(start.UnixMilli(), 10),
		"log_end":      strconv.FormatInt(in.Time.UnixMilli(), 10),
		"missing_hint": lambdaMissingHint,
	})
}

// Footer counts the cold starts and the errors of the loaded rows, and
// shows the loaded span.
func (v *LambdaInvocations) Footer() string {
	if v.missing {
		return "No log group named " + v.logGroup
	}
	var cold, errs int
	for _, in := range v.items {
		if in.cold() {
			cold++
		}
		if in.failed() {
			errs++
		}
	}
	shown, total := v.table.RowCount()
	s := fmt.Sprintf("%d/%d invocations · %d cold · %d errors", shown, total, cold, errs)
	if len(v.items) > 0 {
		// The pages come newest first.
		newest, oldest := v.items[0].Time, v.items[len(v.items)-1].Time
		long := newest.Sub(oldest) > 24*time.Hour || newest.Local().YearDay() != oldest.Local().YearDay()
		s += " · " + logClock(oldest, long) + " to " + logClock(newest, long)
	}
	if strings.EqualFold(v.systemLevel, "WARN") {
		s += " · only failed invocations are logged"
	}
	return v.footerWith(s)
}

// View shows a note in place of the table when the log group does not
// exist, or when the list found no line.
func (v *LambdaInvocations) View() tea.View {
	switch {
	case v.missing:
		return tea.NewView(v.note("No log group named " + v.logGroup + ". " + lambdaMissingHint))
	case v.lookedUp && !v.loading && v.err == nil && len(v.items) == 0 && !v.filter.Active():
		format := "text"
		if strings.EqualFold(v.logFormat, "JSON") {
			format = "JSON"
		}
		text := fmt.Sprintf("No REPORT lines in the %s format in the loaded time.", format)
		if v.more != nil {
			text += " l looks further back."
		}
		return tea.NewView(v.note(text))
	}
	return tea.NewView(v.render("", ""))
}

// note wraps text to the width, under an empty line.
func (v *LambdaInvocations) note(text string) string {
	var b strings.Builder
	b.WriteString("\n")
	for line := range strings.SplitSeq(ansi.Wordwrap(text, max(v.width-4, 20), " "), "\n") {
		b.WriteString("  " + ui.S.Info.Render(line) + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func lambdaInvocationColumns(tier ui.WidthTier) []ui.Column {
	cols := []ui.Column{
		{Title: "Time", Width: 12},
		{Title: "Duration", Width: 11},
		{Title: "Memory", Width: 14}, // 10240/10240 MB
		{Title: "Init", Width: 10},
		{Title: "Status", Width: 10, Weight: 1, MaxWidth: 40},
	}
	switch tier {
	case ui.TierNarrow:
		return []ui.Column{cols[0], cols[1], cols[3], cols[4]}
	case ui.TierMedium:
		return append(cols[:2:2], ui.Column{Title: "Billed", Width: 9}, cols[2], cols[3], cols[4])
	case ui.TierWide:
	}
	return append(cols[:2:2], ui.Column{Title: "Billed", Width: 9}, cols[2], cols[3], cols[4],
		ui.Column{Title: "Stream", Width: 20, Weight: 2, MaxWidth: 60})
}

// lambdaInvocationRow builds the row of one invocation. Durations sort as
// numbers. A line that did not parse shows its raw text in the Status
// column.
func lambdaInvocationRow(in lambdaInvocation, tier ui.WidthTier) ui.Row {
	// The time sorts by the whole time, so rows from two days keep their
	// order.
	at, atKey := in.Time.Local().Format("15:04:05.000"), ui.SortKeyInt(in.Time.UnixMilli())
	if !in.Parsed {
		raw := strings.Join(strings.Fields(in.Raw), " ")
		switch tier {
		case ui.TierNarrow:
			return ui.Row{Key: in.key(), Cells: table.Row{at, "", "", raw}, Sort: table.Row{atKey, "", "", raw}}
		case ui.TierMedium:
			return ui.Row{Key: in.key(), Cells: table.Row{at, "", "", "", "", raw}, Sort: table.Row{atKey, "", "", "", "", raw}}
		case ui.TierWide:
		}
		return ui.Row{Key: in.key(), Cells: table.Row{at, "", "", "", "", raw, in.Stream},
			Sort: table.Row{atKey, "", "", "", "", raw, in.Stream}}
	}
	duration, init, billed, memory := lambdaMS(in.DurationMS), "", "", ""
	if in.cold() {
		init = lambdaMS(in.InitMS)
	}
	// Managed Instances report only the duration.
	if in.MemoryMB > 0 {
		billed, memory = lambdaMS(in.BilledMS), fmt.Sprintf("%d/%d MB", in.UsedMB, in.MemoryMB)
	}
	status := in.Status
	if in.ErrorType != "" {
		status += " " + in.ErrorType
	}
	durKey, initKey := ui.SortKeyInt(int64(in.DurationMS*1000)), ui.SortKeyInt(int64(in.InitMS*1000))
	switch tier {
	case ui.TierNarrow:
		return ui.Row{Key: in.key(), Cells: table.Row{at, duration, init, status},
			Sort: table.Row{atKey, durKey, initKey, status}}
	case ui.TierMedium:
		return ui.Row{Key: in.key(), Cells: table.Row{at, duration, billed, memory, init, status},
			Sort: table.Row{atKey, durKey, ui.SortKeyInt(int64(in.BilledMS)), ui.SortKeyInt(int64(in.UsedMB)), initKey, status}}
	case ui.TierWide:
	}
	return ui.Row{Key: in.key(), Cells: table.Row{at, duration, billed, memory, init, status, in.Stream},
		Sort: table.Row{atKey, durKey, ui.SortKeyInt(int64(in.BilledMS)), ui.SortKeyInt(int64(in.UsedMB)), initKey, status, in.Stream}}
}

// lambdaMS shows a duration in milliseconds: "12.5 ms", or "1.24 s" from
// one second on.
func lambdaMS(ms float64) string {
	if ms >= 1000 {
		return strconv.FormatFloat(ms/1000, 'f', 2, 64) + " s"
	}
	return strconv.FormatFloat(ms, 'f', -1, 64) + " ms"
}

// lambdaInvocationDetail shows every field of the REPORT line, and the raw
// line.
func lambdaInvocationDetail(function string, in lambdaInvocation) msg.TabbedContentMsg {
	init := ""
	if in.cold() {
		init = lambdaMS(in.InitMS) + " (cold start)"
	}
	memory := ""
	if in.MemoryMB > 0 {
		memory = fmt.Sprintf("%d of %d MB", in.UsedMB, in.MemoryMB)
	}
	info, _ := buildInfo([]infoField{
		{K: "Request ID", V: in.RequestID},
		{K: "Time", V: logTimeMillis(in.Time.Local())},
		{K: "Status", V: in.Status},
		{K: "Error type", V: in.ErrorType},
		{K: "Duration", V: lambdaMSIf(in.Parsed, in.DurationMS)},
		{K: "Billed", V: lambdaMSIf(in.Parsed, in.BilledMS)},
		{K: "Init", V: init},
		{K: "Memory", V: memory},
		{K: "Stream", V: in.Stream},
	})
	title := function + " " + in.RequestID
	if !in.Parsed {
		title = function + " " + logTimeMillis(in.Time.Local())
	}
	return msg.TabbedContentMsg{PanelTitle: title, Tabs: []msg.TabContent{
		{Title: "Info", Content: info, Format: "text"},
		{Title: "Raw", Content: in.Raw, Format: "auto"},
	}}
}

func lambdaMSIf(ok bool, ms float64) string {
	if !ok {
		return ""
	}
	return lambdaMS(ms)
}
