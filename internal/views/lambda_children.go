package views

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The lists of one Lambda function: its aliases, versions and triggers
// (event source mappings). See docs/design/lambda.md, "Aliases, versions
// and triggers". Each gets the function name and its log group as route
// parameters: function and log_group.

// lambdaVersionBudget is 4 pages of 50: 200 versions.
const lambdaVersionBudget = 4

// lambdaNoTriggers explains an empty trigger list.
const lambdaNoTriggers = "This list shows only poll-based triggers (SQS, Kinesis, DynamoDB, Kafka, MQ, DocumentDB). " +
	"API Gateway, S3, SNS and EventBridge call the function directly and are set up on their side."

// LambdaChildParams are the route parameters of a child list of f.
func LambdaChildParams(f aws.Function) map[string]string {
	return map[string]string{"function": f.Name, "log_group": f.LogGroupName()}
}

// lambdaMissingHint says why a function can have no log group (H7).
const lambdaMissingHint = "Lambda makes it at the first invocation of the function."

// lambdaLogs opens the log group of a function in the event viewer. When
// the group does not exist, the viewer says why.
func lambdaLogs(group string) tea.Cmd {
	return navigate("logs_events", map[string]string{"log_group": group, "missing_hint": lambdaMissingHint})
}

// ─── Aliases ────────────────────────────────────────────────────────────────

// LambdaAliases lists the aliases of a function.
type LambdaAliases struct {
	listCore[aws.Alias]
	function, logGroup string
}

// NewLambdaAliases creates the alias list of a function.
func NewLambdaAliases(svc aws.LambdaService, function, logGroup string) *LambdaAliases {
	return &LambdaAliases{function: function, logGroup: logGroup, listCore: newListCore(listConfig[aws.Alias]{
		Noun:         "aliases",
		LoadingLabel: "Loading aliases...",
		ErrContext:   "listing aliases",
		Key:          func(a aws.Alias) string { return a.Name },
		Columns:      lambdaAliasColumns,
		Row:          lambdaAliasRow,
		Fetch: fetchPages(svc, func(s aws.LambdaService, ctx context.Context, tok *string) (*aws.AliasPage, error) {
			return s.ListAliasesPage(ctx, function, tok)
		}, splitAliasPage),
		FetchTimeout: callTimeout,
		FetchDetail: func(_ context.Context, a aws.Alias) (msg.TabbedContentMsg, error) {
			info, _ := buildInfo(lambdaAliasInfo(function, a))
			return msg.TabbedContentMsg{PanelTitle: function + ":" + a.Name, Tabs: []msg.TabContent{
				{Title: "Info", Content: info, Format: "text"},
			}}, nil
		},
		Copy: true,
	})}
}

func (v *LambdaAliases) ID() string     { return "lambda_aliases:" + v.function }
func (v *LambdaAliases) Title() string  { return v.function + " aliases" }
func (v *LambdaAliases) Init() tea.Cmd  { return v.init() }
func (v *LambdaAliases) Footer() string { return v.footer() }
func (v *LambdaAliases) View() tea.View { return tea.NewView(v.render("", "")) }

func (v *LambdaAliases) KeyMap() []ui.HintBinding {
	return v.hints(lambdaKeys.Logs, lambdaKeys.Details, lambdaChildCopyName, listKeys.Sort, listKeys.Filter, listKeys.Refresh)
}

// Update returns the view itself. Enter opens the log group of the
// function: Lambda writes the logs of every alias there.
func (v *LambdaAliases) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := m.(tea.KeyPressMsg); ok && !v.filter.Active() && v.err == nil &&
		key.Matches(k, lambdaKeys.Logs.Binding) && v.cursorItem() != nil {
		return v, lambdaLogs(v.logGroup)
	}
	return v, v.update(m)
}

var lambdaChildCopyName = ui.NewHintBinding([]string{"y"}, "y", "copy name")

func splitAliasPage(p *aws.AliasPage) ([]aws.Alias, *string) {
	if p == nil {
		return nil, nil
	}
	return p.Aliases, p.Next
}

func lambdaAliasColumns(tier ui.WidthTier) []ui.Column {
	if tier == ui.TierNarrow {
		return []ui.Column{
			{Title: "Name", Width: 16, Weight: 2, MaxWidth: 64},
			{Title: "Version", Width: 8},
			{Title: "Weights", Width: 12},
		}
	}
	return []ui.Column{
		{Title: "Name", Width: 16, Weight: 2, MaxWidth: 64},
		{Title: "Version", Width: 8},
		{Title: "Weights", Width: 16},
		{Title: "Description", Width: 20, Weight: 3, MaxWidth: 120},
	}
}

func lambdaAliasRow(a aws.Alias, tier ui.WidthTier) ui.Row {
	weights := lambdaWeights(a.Weights)
	if tier == ui.TierNarrow {
		return ui.Row{Key: a.Name, Cells: table.Row{a.Name, a.Version, weights}}
	}
	return ui.Row{Key: a.Name, Cells: table.Row{a.Name, a.Version, weights, a.Description}}
}

// lambdaWeights shows the extra versions of an alias with their part of
// the calls, for example "2: 10%", sorted by version.
func lambdaWeights(w map[string]float64) string {
	if len(w) == 0 {
		return ""
	}
	versions := make([]string, 0, len(w))
	for v := range w {
		versions = append(versions, v)
	}
	slices.SortFunc(versions, func(a, b string) int {
		x, errA := strconv.Atoi(a)
		y, errB := strconv.Atoi(b)
		if errA == nil && errB == nil {
			return x - y
		}
		return strings.Compare(a, b)
	})
	parts := make([]string, 0, len(versions))
	for _, v := range versions {
		parts = append(parts, v+": "+lambdaPercent(w[v]))
	}
	return strings.Join(parts, ", ")
}

// lambdaPercent shows a weight as a percent, rounded to 3 decimal places,
// so the float noise of 0.07*100 (7.000000000000001) does not show. AWS
// takes weights with at most 5 decimal places.
func lambdaPercent(p float64) string {
	return strconv.FormatFloat(math.Round(p*100*1000)/1000, 'f', -1, 64) + "%"
}

func lambdaAliasInfo(function string, a aws.Alias) []infoField {
	routing := ""
	if len(a.Weights) > 0 {
		var rest float64
		for _, p := range a.Weights {
			rest += p
		}
		routing = fmt.Sprintf("%s: %s, %s", a.Version, lambdaPercent(1-rest), lambdaWeights(a.Weights))
	}
	return []infoField{
		{K: "Name", V: a.Name},
		{K: "ARN", V: a.ARN},
		{K: "Function", V: function},
		{K: "Version", V: a.Version},
		{K: "Routing", V: routing},
		{K: "Description", V: a.Description},
	}
}

// ─── Versions ───────────────────────────────────────────────────────────────

// LambdaVersions lists the versions of a function, with $LATEST.
type LambdaVersions struct {
	listCore[aws.Function]
	function, logGroup string
}

// NewLambdaVersions creates the version list of a function. It stops after
// 4 pages, and l loads more.
func NewLambdaVersions(svc aws.LambdaService, function, logGroup string) *LambdaVersions {
	v := &LambdaVersions{function: function, logGroup: logGroup}
	v.listCore = newListCore(listConfig[aws.Function]{
		Noun:         "versions",
		LoadingLabel: "Loading versions...",
		ErrContext:   "listing versions",
		Key:          func(f aws.Function) string { return f.Version },
		Columns:      lambdaVersionColumns,
		Row:          lambdaVersionRow,
		Fetch: fetchPages(svc, func(s aws.LambdaService, ctx context.Context, tok *string) (*aws.FunctionPage, error) {
			return s.ListVersionsPage(ctx, function, tok)
		}, splitFunctionPage),
		FetchTimeout: callTimeout,
		FetchDetail: func(ctx context.Context, f aws.Function) (msg.TabbedContentMsg, error) {
			return lambdaDetail(ctx, svc, f, f.Version, false)
		},
		// A version past the page budget still opens its detail (D5).
		FocusMissing: func(version string) tea.Cmd {
			return v.fetchMissing(version, func(context.Context, string) (aws.Function, error) {
				return aws.Function{Name: function, Version: version}, nil
			})
		},
		PageBudget: lambdaVersionBudget,
		MoreKey:    true,
		Copy:       true,
	})
	return v
}

func (v *LambdaVersions) ID() string     { return "lambda_versions:" + v.function }
func (v *LambdaVersions) Title() string  { return v.function + " versions" }
func (v *LambdaVersions) Init() tea.Cmd  { return v.init() }
func (v *LambdaVersions) Footer() string { return v.footer() }
func (v *LambdaVersions) View() tea.View { return tea.NewView(v.render("", "")) }

func (v *LambdaVersions) KeyMap() []ui.HintBinding {
	return v.hints(lambdaKeys.Logs, lambdaKeys.Details, lambdaVersionCopy, listKeys.More,
		listKeys.Sort, listKeys.Filter, listKeys.Refresh)
}

var lambdaVersionCopy = ui.NewHintBinding([]string{"y"}, "y", "copy version")

// Update returns the view itself. Enter opens the log group of the
// function: Lambda writes the logs of every version there.
func (v *LambdaVersions) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := m.(tea.KeyPressMsg); ok && !v.filter.Active() && v.err == nil &&
		key.Matches(k, lambdaKeys.Logs.Binding) && v.cursorItem() != nil {
		return v, lambdaLogs(v.logGroup)
	}
	return v, v.update(m)
}

func lambdaVersionColumns(tier ui.WidthTier) []ui.Column {
	if tier == ui.TierNarrow {
		return []ui.Column{
			{Title: "Version", Width: 8},
			{Title: "Description", Width: 20, Weight: 3, MaxWidth: 120},
			{Title: "Modified", Width: 10},
		}
	}
	return []ui.Column{
		{Title: "Version", Width: 8},
		{Title: "Description", Width: 20, Weight: 3, MaxWidth: 120},
		{Title: "Runtime", Width: 15},
		{Title: "Size", Width: 9},
		{Title: "Modified", Width: 10},
	}
}

// lambdaVersionRow builds the row of one version. The versions sort as
// numbers, and $LATEST sorts first.
func lambdaVersionRow(f aws.Function, tier ui.WidthTier) ui.Row {
	versionKey := ui.SortKeyInt(-1)
	if n, err := strconv.Atoi(f.Version); err == nil {
		versionKey = ui.SortKeyInt(int64(n))
	}
	modified := logDate(f.LastModified.Local())
	if tier == ui.TierNarrow {
		return ui.Row{Key: f.Version, Cells: table.Row{f.Version, f.Description, modified},
			Sort: table.Row{versionKey, f.Description, modified}}
	}
	return ui.Row{
		Key:   f.Version,
		Cells: table.Row{f.Version, f.Description, lambdaRuntimeText(f), aws.FormatBytes(f.CodeSize), modified},
		Sort:  table.Row{versionKey, f.Description, lambdaRuntimeText(f), ui.SortKeyBytes(f.CodeSize), modified},
	}
}

// ─── Triggers ───────────────────────────────────────────────────────────────

// LambdaTriggers lists the event source mappings of a function.
type LambdaTriggers struct {
	listCore[aws.Trigger]
	function string
}

// NewLambdaTriggers creates the trigger list of a function. The detail
// comes from the list data, so enter and d open it.
func NewLambdaTriggers(svc aws.LambdaService, function string) *LambdaTriggers {
	return &LambdaTriggers{function: function, listCore: newListCore(listConfig[aws.Trigger]{
		Noun:         "triggers",
		LoadingLabel: "Loading triggers...",
		ErrContext:   "listing triggers",
		Key:          func(t aws.Trigger) string { return t.UUID },
		Columns:      lambdaTriggerColumns,
		Row:          lambdaTriggerRow,
		Fetch: fetchPages(svc, func(s aws.LambdaService, ctx context.Context, tok *string) (*aws.TriggerPage, error) {
			return s.ListTriggersPage(ctx, function, tok)
		}, splitTriggerPage),
		FetchTimeout: callTimeout,
		OpenDetail:   lambdaTriggerDetail,
	})}
}

func (v *LambdaTriggers) ID() string     { return "lambda_triggers:" + v.function }
func (v *LambdaTriggers) Title() string  { return v.function + " triggers" }
func (v *LambdaTriggers) Init() tea.Cmd  { return v.init() }
func (v *LambdaTriggers) Footer() string { return v.footer() }
func (v *LambdaTriggers) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	return v, v.update(m)
}

// View shows the note about push sources in place of an empty table. The
// note wraps to the width, so it fits a narrow window.
func (v *LambdaTriggers) View() tea.View {
	if !v.loading && v.err == nil && len(v.items) == 0 && !v.filter.Active() {
		note := ansi.Wordwrap(lambdaNoTriggers, max(v.width-4, 20), " ")
		var b strings.Builder
		b.WriteString("\n")
		for line := range strings.SplitSeq(note, "\n") {
			b.WriteString("  " + ui.S.Info.Render(line) + "\n")
		}
		return tea.NewView(strings.TrimSuffix(b.String(), "\n"))
	}
	return tea.NewView(v.render("", ""))
}

func (v *LambdaTriggers) KeyMap() []ui.HintBinding {
	return v.hints(listKeys.Details, listKeys.Sort, listKeys.Filter, listKeys.Refresh)
}

func splitTriggerPage(p *aws.TriggerPage) ([]aws.Trigger, *string) {
	if p == nil {
		return nil, nil
	}
	return p.Triggers, p.Next
}

func lambdaTriggerColumns(tier ui.WidthTier) []ui.Column {
	if tier == ui.TierNarrow {
		return []ui.Column{
			{Title: "Type", Width: 10},
			{Title: "Source", Width: 16, Weight: 3, MaxWidth: 80},
			{Title: "State", Width: 12, Style: ui.StateColor},
		}
	}
	return []ui.Column{
		{Title: "Type", Width: 10},
		{Title: "Source", Width: 20, Weight: 3, MaxWidth: 80},
		{Title: "State", Width: 12, Style: ui.StateColor},
		{Title: "Batch", Width: 6},
		{Title: "Modified", Width: 16},
	}
}

func lambdaTriggerRow(t aws.Trigger, tier ui.WidthTier) ui.Row {
	src := lambdaSourceName(t.SourceARN)
	if tier == ui.TierNarrow {
		return ui.Row{Key: t.UUID, Cells: table.Row{t.SourceType(), src, t.State}}
	}
	modified := ""
	if !t.LastModified.IsZero() {
		modified = t.LastModified.Local().Format("2006-01-02 15:04")
	}
	return ui.Row{
		Key:   t.UUID,
		Cells: table.Row{t.SourceType(), src, t.State, strconv.Itoa(int(t.BatchSize)), modified},
		Sort:  table.Row{t.SourceType(), src, t.State, ui.SortKeyInt(int64(t.BatchSize)), modified},
	}
}

// lambdaSourceName is the short name of a source, by the kind of its ARN:
// the table of a DynamoDB stream (its stream label holds ":"), the
// cluster of MSK, the broker of MQ, and else the last part of the
// resource, for example the queue name.
func lambdaSourceName(arn string) string {
	if arn == "" {
		return "self-managed"
	}
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 || parts[5] == "" {
		return arn
	}
	resource := parts[5]
	switch parts[2] {
	case "dynamodb":
		if t, ok := strings.CutPrefix(resource, "table/"); ok {
			name, _, _ := strings.Cut(t, "/")
			return name
		}
	case "kafka":
		if c, ok := strings.CutPrefix(resource, "cluster/"); ok {
			name, _, _ := strings.Cut(c, "/")
			return name
		}
	case "mq":
		if b, ok := strings.CutPrefix(resource, "broker:"); ok {
			name, _, _ := strings.Cut(b, ":")
			return name
		}
	}
	if i := strings.LastIndexAny(resource, ":/"); i >= 0 && i < len(resource)-1 {
		return resource[i+1:]
	}
	return resource
}

// lambdaTriggerDetail opens the detail of a trigger. For an SQS source,
// the source line links to the queue in the SQS list.
func lambdaTriggerDetail(t aws.Trigger) tea.Cmd {
	src := infoField{K: "Source", V: t.SourceARN}
	if t.SourceType() == "SQS" {
		src.ViewID, src.Params = "sqs_queues", map[string]string{"focus": lambdaSourceName(t.SourceARN)}
	}
	window := ""
	if t.BatchWindowSec > 0 {
		window = fmt.Sprintf("%d s", t.BatchWindowSec)
	}
	info, links := buildInfo([]infoField{
		{K: "UUID", V: t.UUID},
		{K: "Type", V: t.SourceType()},
		src,
		{K: "State", V: t.State},
		{K: "State reason", V: t.StateReason},
		{K: "Last result", V: t.LastResult},
		{K: "Batch size", V: strconv.Itoa(int(t.BatchSize))},
		{K: "Batch window", V: window},
		{K: "Start position", V: t.StartPosition},
		{K: "Function", V: t.FunctionARN},
		{K: "Modified", V: logTime(t.LastModified.Local())},
	})
	filters := "  No filters"
	if len(t.Filters) > 0 {
		filters = strings.Join(t.Filters, "\n") + "\n"
	}
	tabs := []msg.TabContent{
		{Title: "Info", Content: info, Format: "text", Links: links},
		{Title: "Filters", Content: filters, Format: "auto"},
	}
	title := t.SourceType() + " " + lambdaSourceName(t.SourceARN)
	return func() tea.Msg { return msg.TabbedContentMsg{PanelTitle: title, Tabs: tabs} }
}
