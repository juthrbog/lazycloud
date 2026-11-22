package views

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The task definition families and revisions of ECS, and the detail of a
// revision. See docs/design/ecs.md, "Task definitions".

// ecsFamilyPageBudget is 10 pages of 100: 1,000 families (decision D3).
const ecsFamilyPageBudget = 10

// ecsRevisionPageBudget is 4 pages of 100: 400 revisions (decision D3).
const ecsRevisionPageBudget = 4

// ecsTaskDefKeys are the keys of the task definition lists that the core
// does not have.
var ecsTaskDefKeys = struct {
	Revisions ui.HintBinding
	Open      ui.HintBinding
	Details   ui.HintBinding
	Env       ui.HintBinding
}{
	Revisions: ui.NewHintBinding([]string{"enter"}, "enter", "revisions"),
	Open:      ui.NewHintBinding([]string{"enter"}, "enter", "details"),
	Details:   ui.NewHintBinding([]string{"d"}, "d", "latest revision"),
	Env:       ui.NewHintBinding([]string{"$"}, "$", "details with env values"),
}

// ecsTaskDefLink is the Info line of a task definition: family:revision,
// a link to the revisions of the family with the cursor on this one.
func ecsTaskDefLink(arn string) infoField {
	f := infoField{K: "Task def", V: aws.TaskDefinitionLabel(arn)}
	if family, _ := aws.ParseTaskDefinition(arn); family != "" && arn != "" {
		f.ViewID = "ecs_task_definitions"
		f.Params = map[string]string{"family": family, "focus": arn}
	}
	return f
}

// ecsTaskDefFetch returns the detail of a task definition by its ARN,
// family:revision, or family for the latest active revision.
func ecsTaskDefFetch(svc aws.ECSService, reveal bool) func(context.Context, string) (msg.TabbedContentMsg, error) {
	return func(ctx context.Context, name string) (msg.TabbedContentMsg, error) {
		if svc == nil {
			return msg.TabbedContentMsg{}, errNoClient
		}
		d, err := svc.DescribeTaskDefinition(ctx, name)
		if err != nil {
			return msg.TabbedContentMsg{}, err
		}
		return ecsTaskDefDetail(d, reveal), nil
	}
}

// ecsRevealCmd runs the detail fetch with the environment values shown.
func ecsRevealCmd(ctx context.Context, svc aws.ECSService, name string) tea.Cmd {
	fetch := ecsTaskDefFetch(svc, true)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		d, err := fetch(ctx, name)
		if err != nil {
			// ErrorText reads only the local AWS config files.
			if cmd := detailErrorToast(err); cmd != nil { //nolint:contextcheck // see above
				return cmd()
			}
			return nil
		}
		return d
	}
}

// ─── Families ───────────────────────────────────────────────────────────────

// ECSTaskFamilies lists the active task definition families.
type ECSTaskFamilies struct {
	listCore[string]
	ecs aws.ECSService
}

// NewECSTaskFamilies creates the family list. The row key is the family.
func NewECSTaskFamilies(svc aws.ECSService) *ECSTaskFamilies {
	v := &ECSTaskFamilies{ecs: svc}
	detail := ecsTaskDefFetch(svc, false)
	v.listCore = newListCore(listConfig[string]{
		Noun:         "families",
		LoadingLabel: "Loading task definition families...",
		ErrContext:   "listing task definition families",
		Key:          func(f string) string { return f },
		Columns:      ecsFamilyColumns,
		Row: func(f string, _ ui.WidthTier) ui.Row {
			return ui.Row{Key: f, Cells: table.Row{f}}
		},
		Fetch: fetchPages(svc, aws.ECSService.ListTaskFamiliesPage, func(p *aws.FamilyPage) ([]string, *string) {
			if p == nil {
				return nil, nil
			}
			return p.Families, p.Next
		}),
		FetchTimeout: callTimeout,
		// DescribeTaskDefinition takes a family name for the latest active
		// revision.
		FetchDetail: func(ctx context.Context, f string) (msg.TabbedContentMsg, error) { return detail(ctx, f) },
		PageBudget:  ecsFamilyPageBudget,
		MoreKey:     true,
		Copy:        true,
	})
	return v
}

func ecsFamilyColumns(ui.WidthTier) []ui.Column {
	return []ui.Column{{Title: "Family", Width: 20, Weight: 1, MaxWidth: 255}}
}

func (v *ECSTaskFamilies) ID() string     { return "ecs_task_families" }
func (v *ECSTaskFamilies) Title() string  { return "ECS Task Definitions" }
func (v *ECSTaskFamilies) Init() tea.Cmd  { return v.init() }
func (v *ECSTaskFamilies) Footer() string { return v.footer() }
func (v *ECSTaskFamilies) View() tea.View { return tea.NewView(v.render("", "")) }

func (v *ECSTaskFamilies) KeyMap() []ui.HintBinding {
	return v.hints(ecsTaskDefKeys.Revisions, ecsTaskDefKeys.Details, ecsTaskDefKeys.Env, listKeys.CopyID,
		listKeys.More, listKeys.Sort, listKeys.Filter, listKeys.Refresh)
}

// Update returns the view itself. enter opens the revisions of the family,
// and $ the detail of the latest revision with the environment values.
func (v *ECSTaskFamilies) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := m.(tea.KeyPressMsg); ok && !v.filter.Active() && v.err == nil {
		switch it := v.cursorItem(); {
		case it == nil:
		case key.Matches(k, ecsTaskDefKeys.Revisions.Binding):
			return v, navigate("ecs_task_definitions", map[string]string{"family": *it})
		case key.Matches(k, ecsTaskDefKeys.Env.Binding):
			return v, ecsRevealCmd(v.viewContext(), v.ecs, *it)
		}
	}
	return v, v.update(m)
}

// ─── Revisions ──────────────────────────────────────────────────────────────

// ECSTaskDefinitions lists the active revisions of a family, newest first.
type ECSTaskDefinitions struct {
	listCore[aws.TaskDefinitionRef]
	ecs    aws.ECSService
	family string
}

// NewECSTaskDefinitions creates the revision list of a family. The row key
// is the ARN, so y copies it, and a link focuses on it.
func NewECSTaskDefinitions(svc aws.ECSService, family string) *ECSTaskDefinitions {
	v := &ECSTaskDefinitions{ecs: svc, family: family}
	detail := ecsTaskDefFetch(svc, false)
	v.listCore = newListCore(listConfig[aws.TaskDefinitionRef]{
		Noun:         "revisions",
		LoadingLabel: "Loading revisions...",
		ErrContext:   "listing task definitions",
		Key:          func(r aws.TaskDefinitionRef) string { return r.ARN },
		Columns:      ecsRevisionColumns,
		Row:          ecsRevisionRow,
		Fetch: fetchPages(svc, func(s aws.ECSService, ctx context.Context, tok *string) (*aws.TaskDefinitionPage, error) {
			return s.ListTaskDefinitionsPage(ctx, family, tok)
		}, func(p *aws.TaskDefinitionPage) ([]aws.TaskDefinitionRef, *string) {
			if p == nil {
				return nil, nil
			}
			return p.Revisions, p.Next
		}),
		FetchTimeout: callTimeout,
		FetchDetail: func(ctx context.Context, r aws.TaskDefinitionRef) (msg.TabbedContentMsg, error) {
			return detail(ctx, r.ARN)
		},
		// A link can name an inactive revision, or one past the page
		// budget (H13). Its detail still opens.
		FocusMissing: func(arn string) tea.Cmd {
			return v.detailFetch(aws.TaskDefinitionRef{ARN: arn})
		},
		PageBudget: ecsRevisionPageBudget,
		MoreKey:    true,
		Copy:       true,
	})
	return v
}

func ecsRevisionColumns(ui.WidthTier) []ui.Column {
	return []ui.Column{
		{Title: "Revision", Width: 8},
		{Title: "ARN", Width: 30, Weight: 1, MaxWidth: 255},
	}
}

func ecsRevisionRow(r aws.TaskDefinitionRef, _ ui.WidthTier) ui.Row {
	return ui.Row{
		Key:   r.ARN,
		Cells: table.Row{strconv.Itoa(r.Revision), r.ARN},
		Sort:  table.Row{ui.SortKeyInt(int64(r.Revision)), r.ARN},
	}
}

func (v *ECSTaskDefinitions) ID() string     { return "ecs_task_definitions:" + v.family }
func (v *ECSTaskDefinitions) Title() string  { return v.family + " revisions" }
func (v *ECSTaskDefinitions) Init() tea.Cmd  { return v.init() }
func (v *ECSTaskDefinitions) Footer() string { return v.footer() }
func (v *ECSTaskDefinitions) View() tea.View { return tea.NewView(v.render("", "")) }

func (v *ECSTaskDefinitions) KeyMap() []ui.HintBinding {
	return v.hints(ecsTaskDefKeys.Open, ecsKeys.Details, ecsTaskDefKeys.Env, ecsKeys.CopyARN,
		listKeys.More, listKeys.Sort, listKeys.Filter, listKeys.Refresh)
}

// Update returns the view itself. enter opens the detail, as d does, and $
// the detail with the environment values.
func (v *ECSTaskDefinitions) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := m.(tea.KeyPressMsg); ok && !v.filter.Active() && v.err == nil {
		switch it := v.cursorItem(); {
		case it == nil:
		case key.Matches(k, ecsTaskDefKeys.Open.Binding):
			return v, v.detailFetch(*it)
		case key.Matches(k, ecsTaskDefKeys.Env.Binding):
			return v, ecsRevealCmd(v.viewContext(), v.ecs, it.ARN)
		}
	}
	return v, v.update(m)
}

// ─── Detail ─────────────────────────────────────────────────────────────────

// ecsTaskDefDetail builds the detail of a revision: Info, Containers,
// Environment and Tags. The environment values are hidden unless reveal is
// set. Then the Environment tab comes first, so a narrow terminal, which
// shows only the first tab, shows them.
func ecsTaskDefDetail(d *aws.TaskDefinition, reveal bool) msg.TabbedContentMsg {
	ephemeral := ""
	if d.EphemeralGiB > 0 {
		ephemeral = fmt.Sprintf("%d GiB", d.EphemeralGiB)
	}
	platform := strings.TrimSpace(d.OSFamily + " " + d.CPUArchitecture)
	info, _ := buildInfo([]infoField{
		{K: "Family", V: d.Family},
		{K: "Revision", V: strconv.Itoa(int(d.Revision))},
		{K: "ARN", V: d.ARN},
		{K: "Status", V: d.Status},
		{K: "Registered", V: logTime(d.RegisteredAt.Local())},
		{K: "Registered by", V: d.RegisteredBy},
		{K: "Deregistered", V: logTime(d.DeregisteredAt.Local())},
		{K: "CPU", V: d.CPU},
		{K: "Memory", V: d.Memory},
		{K: "Network mode", V: d.NetworkMode},
		{K: "Requires", V: strings.Join(d.RequiresCompat, ", ")},
		{K: "Compatible", V: strings.Join(d.Compatibilities, ", ")},
		{K: "Platform", V: platform},
		{K: "Ephemeral", V: ephemeral},
		{K: "Task role", V: d.TaskRole},
		{K: "Execution role", V: d.ExecutionRole},
		{K: "Volumes", V: strings.Join(d.Volumes, ", ")},
	})
	env := msg.TabContent{Title: "Environment", Content: ecsEnvText(d.Containers, reveal), Format: "text"}
	tabs := []msg.TabContent{
		{Title: "Info", Content: info, Format: "text"},
		{Title: "Containers", Content: ecsContainerDefsText(d.Containers), Format: "text"},
	}
	if reveal {
		tabs = append([]msg.TabContent{env}, tabs...)
	} else {
		tabs = append(tabs, env)
	}
	if len(d.Tags) > 0 {
		tabs = append(tabs, msg.TabContent{Title: "Tags", Content: buildTagsContent(d.Tags), Format: "text"})
	}
	return msg.TabbedContentMsg{PanelTitle: aws.TaskDefinitionLabel(d.ARN), Tabs: tabs}
}

// ecsContainerDefsText shows one block for each container definition.
// Secrets show as names and sources, never values.
func ecsContainerDefsText(cs []aws.ContainerDefinition) string {
	if len(cs) == 0 {
		return "  No containers\n"
	}
	num := func(n int32) string {
		if n == 0 {
			return ""
		}
		return strconv.Itoa(int(n))
	}
	var b strings.Builder
	for i, c := range cs {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(c.Name + "\n")
		fields := []infoField{
			{K: "  Image", V: c.Image},
			{K: "  Essential", V: yesNo(c.Essential)},
			{K: "  CPU", V: num(c.CPU)},
			{K: "  Memory", V: num(c.Memory)},
			{K: "  Memory soft", V: num(c.MemoryReservation)},
			{K: "  Entry point", V: strings.Join(c.EntryPoint, " ")},
			{K: "  Command", V: strings.Join(c.Command, " ")},
			{K: "  Env files", V: strings.Join(c.EnvFiles, ", ")},
		}
		for _, p := range c.Ports {
			v := p.ContainerPortRange
			if p.ContainerPort != 0 {
				v = strconv.Itoa(int(p.ContainerPort))
			}
			if p.HostPort != 0 {
				v = fmt.Sprintf("%d -> %d", p.HostPort, p.ContainerPort)
			}
			if p.Protocol != "" {
				v += "/" + p.Protocol
			}
			if p.Name != "" {
				v += " (" + p.Name + ")"
			}
			fields = append(fields, infoField{K: "  Port", V: v})
		}
		if c.Log != nil {
			fields = append(fields, infoField{K: "  Log driver", V: c.Log.Driver})
			for _, k := range slices.Sorted(maps.Keys(c.Log.Options)) {
				fields = append(fields, infoField{K: "    " + k, V: c.Log.Options[k]})
			}
		}
		for _, s := range c.Secrets {
			fields = append(fields, infoField{K: "  Secret", V: s.Name + " from " + s.From})
		}
		text, _ := buildInfo(fields)
		b.WriteString(text)
	}
	return b.String()
}

// ecsEnvText lists the environment variables of each container, sorted by
// name, with the values hidden unless reveal is set (decision D7).
func ecsEnvText(cs []aws.ContainerDefinition, reveal bool) string {
	var b strings.Builder
	if !reveal && slices.ContainsFunc(cs, func(c aws.ContainerDefinition) bool { return len(c.Env) > 0 }) {
		b.WriteString("Values are hidden. Press $ in the list to show them.\n\n")
	}
	for i, c := range cs {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(c.Name + "\n")
		if len(c.Env) == 0 {
			b.WriteString("  No environment variables\n")
			continue
		}
		for _, k := range slices.Sorted(maps.Keys(c.Env)) {
			v := lambdaHidden
			if reveal {
				v = c.Env[k]
			}
			fmt.Fprintf(&b, "  %-24s %s\n", k, v)
		}
	}
	if len(cs) == 0 {
		b.WriteString("  No containers\n")
	}
	return b.String()
}
