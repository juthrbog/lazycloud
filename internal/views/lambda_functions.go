package views

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The Lambda function list. See docs/design/lambda.md.

// lambdaPageBudget is 20 pages of 50: 1,000 functions (decision D3).
const lambdaPageBudget = 20

// lambdaHidden replaces the value of an environment variable (decision D5).
const lambdaHidden = "••••"

// lambdaKeys are the keys of the function list that the core does not have.
var lambdaKeys = struct {
	Logs        ui.HintBinding
	Details     ui.HintBinding
	Env         ui.HintBinding
	CopyARN     ui.HintBinding
	Aliases     ui.HintBinding
	Versions    ui.HintBinding
	Triggers    ui.HintBinding
	Invocations ui.HintBinding
}{
	Logs:        ui.NewHintBinding([]string{"enter"}, "enter", "logs"),
	Details:     ui.NewHintBinding([]string{"d"}, "d", "details"),
	Env:         ui.NewHintBinding([]string{"$"}, "$", "details with env values"),
	CopyARN:     ui.NewHintBinding([]string{"y"}, "y", "copy ARN"),
	Aliases:     ui.NewHintBinding([]string{"a"}, "a", "aliases"),
	Versions:    ui.NewHintBinding([]string{"v"}, "v", "versions"),
	Triggers:    ui.NewHintBinding([]string{"t"}, "t", "triggers"),
	Invocations: ui.NewHintBinding([]string{"i"}, "i", "invocations"),
}

// LambdaFunctions displays the Lambda functions.
type LambdaFunctions struct {
	listCore[aws.Function]
	lambda aws.LambdaService
}

// NewLambdaFunctions creates the function list view. The row key is the
// ARN, so y copies it. A focus and :lambda NAME find a function by name.
func NewLambdaFunctions(svc aws.LambdaService) *LambdaFunctions {
	v := &LambdaFunctions{lambda: svc}
	v.listCore = newListCore(listConfig[aws.Function]{
		Noun:         "functions",
		LoadingLabel: "Loading functions...",
		ErrContext:   "listing functions",
		Key:          func(f aws.Function) string { return f.ARN },
		FocusKey:     func(f aws.Function) string { return f.Name },
		FocusMissing: v.focusMissing,
		Columns:      lambdaColumns,
		Row:          lambdaRow,
		Fetch:        fetchPages(svc, aws.LambdaService.ListFunctionsPage, splitFunctionPage),
		FetchTimeout: callTimeout,
		FetchDetail: func(ctx context.Context, f aws.Function) (msg.TabbedContentMsg, error) {
			return lambdaDetail(ctx, svc, f, "", false)
		},
		PageBudget: lambdaPageBudget,
		MoreKey:    true,
		Copy:       true,
	})
	return v
}

func (v *LambdaFunctions) ID() string     { return "lambda_functions" }
func (v *LambdaFunctions) Title() string  { return "Lambda Functions" }
func (v *LambdaFunctions) Init() tea.Cmd  { return v.init() }
func (v *LambdaFunctions) Footer() string { return v.footer() }
func (v *LambdaFunctions) View() tea.View { return tea.NewView(v.render("", "")) }

func (v *LambdaFunctions) KeyMap() []ui.HintBinding {
	return v.hints(lambdaKeys.Logs, lambdaKeys.Details, lambdaKeys.Env, lambdaKeys.Aliases, lambdaKeys.Versions,
		lambdaKeys.Triggers, lambdaKeys.Invocations, lambdaKeys.CopyARN, listKeys.More,
		listKeys.Sort, listKeys.Filter, listKeys.Refresh)
}

// Update returns the view itself, so the navigator keeps its identity.
// Enter opens the log group of the function in the event viewer (decision
// D4). $ opens the detail with the environment values shown (decision D5).
func (v *LambdaFunctions) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := m.(tea.KeyPressMsg); ok && !v.filter.Active() && v.err == nil {
		switch it := v.cursorItem(); {
		case it == nil:
		case key.Matches(k, lambdaKeys.Logs.Binding):
			return v, lambdaLogs(it.LogGroupName())
		case key.Matches(k, lambdaKeys.Env.Binding):
			return v, v.revealDetail(*it)
		case key.Matches(k, lambdaKeys.Aliases.Binding):
			return v, navigate("lambda_aliases", lambdaChildParams(*it))
		case key.Matches(k, lambdaKeys.Versions.Binding):
			return v, navigate("lambda_versions", lambdaChildParams(*it))
		case key.Matches(k, lambdaKeys.Triggers.Binding):
			return v, navigate("lambda_triggers", lambdaChildParams(*it))
		case key.Matches(k, lambdaKeys.Invocations.Binding):
			p := lambdaChildParams(*it)
			p["log_format"], p["system_log_level"] = it.LogFormat, it.SystemLogLevel
			return v, navigate("lambda_invocations", p)
		}
	}
	return v, v.update(m)
}

// revealDetail fetches the detail with the environment values shown and
// the Environment tab first, so a narrow terminal, which shows only the
// first tab, shows them.
func (v *LambdaFunctions) revealDetail(f aws.Function) tea.Cmd {
	svc, ctx := v.lambda, v.viewContext()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		d, err := lambdaDetail(ctx, svc, f, "", true)
		if err != nil {
			// ErrorText reads only the local AWS config files, so it needs
			// no request context.
			if cmd := detailErrorToast(err); cmd != nil { //nolint:contextcheck // see above
				return cmd()
			}
			return nil
		}
		return d
	}
}

// focusMissing answers a focus on a name that the loaded pages do not
// have. The list stops after a budget, so the function can still exist.
func (v *LambdaFunctions) focusMissing(name string) tea.Cmd {
	text := fmt.Sprintf("No function named %s in the loaded functions", name)
	if v.more != nil {
		text += " · l for more"
	}
	return toastCmd(msg.ToastInfo(text))
}

func splitFunctionPage(p *aws.FunctionPage) ([]aws.Function, *string) {
	if p == nil {
		return nil, nil
	}
	return p.Functions, p.Next
}

func lambdaColumns(tier ui.WidthTier) []ui.Column {
	switch tier {
	case ui.TierNarrow:
		return []ui.Column{
			{Title: "Name", Width: 17, Weight: 3, MaxWidth: 64},
			{Title: "Runtime", Width: 15},
			{Title: "Modified", Width: 10},
		}
	case ui.TierMedium:
		return []ui.Column{
			{Title: "Name", Width: 21, Weight: 3, MaxWidth: 64},
			{Title: "Runtime", Width: 15},
			{Title: "Memory", Width: 8},
			{Title: "Timeout", Width: 7},
			{Title: "Size", Width: 9},
			{Title: "Modified", Width: 10},
		}
	case ui.TierWide:
	}
	return []ui.Column{
		{Title: "Name", Width: 24, Weight: 3, MaxWidth: 64},
		{Title: "Runtime", Width: 15}, // provided.al2023
		{Title: "Memory", Width: 8},
		{Title: "Timeout", Width: 7},
		{Title: "Size", Width: 9},
		{Title: "Arch", Width: 7},
		{Title: "Package", Width: 7},
		{Title: "Modified", Width: 10},
	}
}

// lambdaRow builds the row of one function. Memory, timeout and size sort
// as numbers.
func lambdaRow(f aws.Function, tier ui.WidthTier) ui.Row {
	runtime := lambdaRuntimeText(f)
	modified := logDate(f.LastModified.Local()) // "" for a zero time
	if tier == ui.TierNarrow {
		return ui.Row{Key: f.ARN, Cells: table.Row{f.Name, runtime, modified}}
	}
	memory, timeout, size := fmt.Sprintf("%d MB", f.MemoryMB), fmt.Sprintf("%ds", f.TimeoutSec), aws.FormatBytes(f.CodeSize)
	memKey, toKey, sizeKey := ui.SortKeyInt(int64(f.MemoryMB)), ui.SortKeyInt(int64(f.TimeoutSec)), ui.SortKeyBytes(f.CodeSize)
	if tier == ui.TierMedium {
		return ui.Row{
			Key:   f.ARN,
			Cells: table.Row{f.Name, runtime, memory, timeout, size, modified},
			Sort:  table.Row{f.Name, runtime, memKey, toKey, sizeKey, modified},
		}
	}
	arch, pkg := strings.Join(f.Architectures, ","), strings.ToLower(f.PackageType)
	return ui.Row{
		Key:   f.ARN,
		Cells: table.Row{f.Name, runtime, memory, timeout, size, arch, pkg, modified},
		Sort:  table.Row{f.Name, runtime, memKey, toKey, sizeKey, arch, pkg, modified},
	}
}

// lambdaRuntimeText shows the runtime, or "image" for an image function,
// which has none.
func lambdaRuntimeText(f aws.Function) string {
	if f.Runtime == "" && f.PackageType == "Image" {
		return "image"
	}
	return f.Runtime
}

// lambdaDetail builds the detail of a function with GetFunction and
// GetFunctionURL. A failure of GetFunctionURL shows on the URL line, and
// the panel still opens. With a qualifier (a version), it reads that
// version and leaves out the URL, which belongs to the function. With
// reveal, the Environment tab comes first and shows the values.
func lambdaDetail(ctx context.Context, svc aws.LambdaService, f aws.Function, qualifier string, reveal bool) (msg.TabbedContentMsg, error) {
	if svc == nil {
		return msg.TabbedContentMsg{}, errNoClient
	}
	d, err := svc.GetFunction(ctx, f.Name, qualifier)
	if err != nil {
		return msg.TabbedContentMsg{}, err
	}
	title := f.Name
	if qualifier != "" {
		title += ":" + qualifier
	}
	urlText := ""
	var u *aws.FunctionURL
	var urlErr error
	if qualifier == "" {
		u, urlErr = svc.GetFunctionURL(ctx, f.Name)
	}
	switch {
	case qualifier != "":
	case urlErr != nil:
		// ErrorText reads only the local AWS config files, so it needs no
		// request context.
		urlText = "failed: " + aws.ErrorText(urlErr) //nolint:contextcheck // see above
	case u != nil:
		urlText = fmt.Sprintf("%s (auth %s)", u.URL, u.AuthType)
	default:
		urlText = "none"
	}
	info, infoLinks := buildInfo(lambdaInfo(d, urlText))
	network, netLinks := buildInfo(lambdaNetwork(d))
	envTab := msg.TabContent{Title: "Environment", Content: lambdaEnvText(d, reveal), Format: "text"}
	tabs := []msg.TabContent{
		{Title: "Info", Content: info, Format: "text", Links: infoLinks},
		envTab,
		{Title: "Network", Content: network, Format: "text", Links: netLinks},
	}
	if reveal {
		tabs[0], tabs[1] = tabs[1], tabs[0]
	}
	switch {
	case d.TagsError != "":
		tabs = append(tabs, msg.TabContent{Title: "Tags", Content: "Tags failed: " + d.TagsError, Format: "text"})
	case len(d.Tags) > 0:
		tabs = append(tabs, msg.TabContent{Title: "Tags", Content: buildTagsContent(d.Tags), Format: "text"})
	}
	return msg.TabbedContentMsg{PanelTitle: title, Tabs: tabs}, nil
}

func lambdaInfo(d *aws.FunctionDetail, urlText string) []infoField {
	state := d.State
	if d.StateReason != "" {
		state += " (" + d.StateReason + ")"
	}
	update := d.LastUpdateStatus
	if d.LastUpdateReason != "" {
		update += " (" + d.LastUpdateReason + ")"
	}
	concurrency := "not reserved"
	if d.ReservedConcurrency != nil {
		concurrency = fmt.Sprintf("%d reserved", *d.ReservedConcurrency)
	}
	logs := d.LogGroupName()
	if d.LogFormat != "" {
		logs += " (" + d.LogFormat
		if d.AppLogLevel != "" || d.SystemLogLevel != "" {
			logs += fmt.Sprintf(", app %s, system %s", orDash(d.AppLogLevel), orDash(d.SystemLogLevel))
		}
		logs += ")"
	}
	snap := ""
	if d.SnapStartApplyOn != "" && d.SnapStartApplyOn != "None" {
		snap = d.SnapStartApplyOn + ", " + d.SnapStartStatus
	}
	layers := make([]string, 0, len(d.Layers))
	for _, l := range d.Layers {
		layers = append(layers, strings.TrimSuffix(l.Name()+":"+l.Version(), ":"))
	}
	ephemeral := ""
	if d.EphemeralMB > 0 {
		ephemeral = fmt.Sprintf("%d MB", d.EphemeralMB)
	}
	return []infoField{
		{K: "Name", V: d.Name},
		{K: "ARN", V: d.ARN},
		{K: "Version", V: d.Version},
		{K: "State", V: state},
		{K: "Last update", V: update},
		{K: "Description", V: d.Description},
		{K: "Runtime", V: lambdaRuntimeText(d.Function)},
		{K: "Handler", V: d.Handler},
		{K: "Image", V: d.ImageURI},
		{K: "Package", V: d.PackageType},
		{K: "Architecture", V: strings.Join(d.Architectures, ", ")},
		{K: "Memory", V: fmt.Sprintf("%d MB", d.MemoryMB)},
		{K: "Timeout", V: fmt.Sprintf("%d s", d.TimeoutSec)},
		{K: "Ephemeral", V: ephemeral},
		{K: "Code size", V: aws.FormatBytes(d.CodeSize)},
		{K: "Code SHA256", V: d.CodeSHA256},
		{K: "Concurrency", V: concurrency},
		{K: "Role", V: d.Role},
		{K: "Log group", V: logs},
		{K: "Function URL", V: urlText},
		{K: "Tracing", V: d.TracingMode},
		{K: "SnapStart", V: snap},
		{K: "Layers", V: strings.Join(layers, ", ")},
		{K: "Dead letter", V: d.DeadLetterARN},
		{K: "KMS key", V: d.KMSKeyARN},
		{K: "Modified", V: logTime(d.LastModified.Local())},
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// lambdaNetwork lists the VPC, the subnets and the security groups, each a
// link to its list. A function outside a VPC sends empty fields.
func lambdaNetwork(d *aws.FunctionDetail) []infoField {
	if d.VPCID == "" {
		return []infoField{{K: "VPC", V: "No VPC"}}
	}
	fields := []infoField{{K: "VPC", V: d.VPCID, ViewID: "vpc_list", Params: map[string]string{"focus": d.VPCID}}}
	for _, s := range d.SubnetIDs {
		fields = append(fields, infoField{K: "Subnet", V: s, ViewID: "subnet_list", Params: map[string]string{"focus": s}})
	}
	for _, g := range d.SecurityGroupIDs {
		fields = append(fields, infoField{K: "Security group", V: g, ViewID: "sg_list", Params: map[string]string{"focus": g}})
	}
	return fields
}

// lambdaEnvText lists the environment variables, sorted by name, with the
// values hidden unless reveal is set (decision D5).
func lambdaEnvText(d *aws.FunctionDetail, reveal bool) string {
	var b strings.Builder
	if d.EnvError != "" {
		fmt.Fprintf(&b, "Environment error: %s\n\n", d.EnvError)
	}
	if len(d.Env) == 0 {
		b.WriteString("  No environment variables\n")
		return b.String()
	}
	if !reveal {
		b.WriteString("Values are hidden. Press $ in the list to show them.\n\n")
	}
	for _, k := range slices.Sorted(maps.Keys(d.Env)) {
		v := lambdaHidden
		if reveal {
			v = d.Env[k]
		}
		fmt.Fprintf(&b, "%-24s %s\n", k, v)
	}
	return b.String()
}
