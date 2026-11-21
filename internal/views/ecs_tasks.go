package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The ECS task list of a service or a cluster. See docs/design/ecs.md.

// ecsTaskPageBudget is 10 pages of 100: 1,000 tasks (decision D3).
const ecsTaskPageBudget = 10

// ecsStoppedNote follows the footer in the stopped mode.
const ecsStoppedNote = "AWS keeps stopped tasks for about an hour"

// ecsTaskKeys are the keys of the task list that the core does not have.
var ecsTaskKeys = struct {
	Open    ui.HintBinding
	Stopped ui.HintBinding
	Running ui.HintBinding
}{
	// enter opens the detail until the container list exists (PR 4).
	Open:    ui.NewHintBinding([]string{"enter"}, "enter", "details"),
	Stopped: ui.NewHintBinding([]string{"x"}, "x", "stopped tasks"),
	Running: ui.NewHintBinding([]string{"x"}, "x", "running tasks"),
}

// ECSTasks lists the tasks of a service, or of a whole cluster when service
// is "". x switches between the running and the stopped tasks (decision
// D4), with the columns of each kind.
type ECSTasks struct {
	listCore[aws.Task]
	ecs     aws.ECSService
	cluster string // ARN
	service string // name, or ""
	stopped bool
}

// NewECSTasks creates the task list. The row key is the ARN, so y copies it.
func NewECSTasks(svc aws.ECSService, cluster, service string) *ECSTasks {
	v := &ECSTasks{ecs: svc, cluster: cluster, service: service}
	v.listCore = newListCore(listConfig[aws.Task]{
		Noun:         "tasks",
		LoadingLabel: "Loading tasks...",
		ErrContext:   "listing tasks",
		Key:          func(t aws.Task) string { return t.ARN },
		FocusKey:     func(t aws.Task) string { return t.ID },
		Columns:      ecsTaskColumns,
		Row:          ecsTaskRow,
		Fetch:        v.fetch(false),
		FetchTimeout: callTimeout,
		// The detail uses the data of the row, with no call.
		FetchDetail: func(_ context.Context, t aws.Task) (msg.TabbedContentMsg, error) {
			return ecsTaskDetail(t), nil
		},
		OnLoaded: func(items []aws.Task, _ string) tea.Cmd {
			return ecsUnreadable("task", items, func(t aws.Task) (string, string) { return t.ID, t.Err })
		},
		PageBudget: ecsTaskPageBudget,
		MoreKey:    true,
		Copy:       true,
	})
	return v
}

func (v *ECSTasks) fetch(stopped bool) fetchFunc[aws.Task] {
	cluster, service := v.cluster, v.service
	return fetchPages(v.ecs, func(s aws.ECSService, ctx context.Context, tok *string) (*aws.TaskPage, error) {
		return s.ListTasksPage(ctx, cluster, service, stopped, tok)
	}, splitTaskPage)
}

func (v *ECSTasks) ID() string { return "ecs_tasks:" + v.cluster + ":" + v.service }

func (v *ECSTasks) Title() string {
	name := v.service
	if name == "" {
		name = aws.ARNName(v.cluster)
	}
	if v.stopped {
		return name + " stopped tasks"
	}
	return name + " tasks"
}

func (v *ECSTasks) Init() tea.Cmd { return v.init() }

func (v *ECSTasks) Footer() string {
	if v.stopped {
		return v.footer() + " · " + ecsStoppedNote
	}
	return v.footer()
}

func (v *ECSTasks) View() tea.View { return tea.NewView(v.render("", "")) }

func (v *ECSTasks) KeyMap() []ui.HintBinding {
	toggle := ecsTaskKeys.Stopped
	if v.stopped {
		toggle = ecsTaskKeys.Running
	}
	return v.hints(ecsTaskKeys.Open, ecsKeys.Details, toggle, ecsKeys.CopyARN, listKeys.More,
		listKeys.Sort, listKeys.Filter, listKeys.Refresh)
}

// Update returns the view itself. enter opens the detail, as d does. x
// switches between the running and the stopped tasks; it also works on the
// error screen, because it loads the other list.
func (v *ECSTasks) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := m.(tea.KeyPressMsg); ok && !v.filter.Active() {
		switch {
		case key.Matches(k, ecsTaskKeys.Stopped.Binding):
			return v, v.toggle()
		case v.err == nil && key.Matches(k, ecsTaskKeys.Open.Binding):
			if it := v.cursorItem(); it != nil {
				return v, v.detailFetch(*it)
			}
		}
	}
	return v, v.update(m)
}

// toggle switches the list to the other kind of tasks and loads it.
func (v *ECSTasks) toggle() tea.Cmd {
	v.stopped = !v.stopped
	if v.stopped {
		return v.switchList(v.fetch(true), ecsStoppedColumns, ecsStoppedRow, "Loading stopped tasks...")
	}
	return v.switchList(v.fetch(false), ecsTaskColumns, ecsTaskRow, v.cfg.LoadingLabel)
}

func splitTaskPage(p *aws.TaskPage) ([]aws.Task, *string) {
	if p == nil {
		return nil, nil
	}
	return p.Tasks, p.Next
}

// ecsAge shows how long ago t was, for example "45s", "12m", "3h12m" or
// "2d4h", or "" for a zero time.
func ecsAge(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := max(listNow().Sub(t), 0)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
}

// ecsTimeKey sorts an age column by its time, so the oldest comes first in
// ascending order. A zero time sorts first.
func ecsTimeKey(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return ui.SortKeyInt(t.UnixMilli())
}

func ecsTaskColumns(tier ui.WidthTier) []ui.Column {
	task := ui.Column{Title: "Task", Width: 12, Weight: 2, MaxWidth: 36}
	status := ui.Column{Title: "Status", Width: 16, Style: ui.StateColor} // ◌ DEPROVISIONING
	switch tier {
	case ui.TierNarrow:
		return []ui.Column{task, status, {Title: "Started", Width: 8}}
	case ui.TierMedium:
		// It fits 80 columns.
		return []ui.Column{
			task, status,
			{Title: "Health", Width: 11, Style: ui.HealthColor}, // ○ UNHEALTHY
			{Title: "Task def", Width: 12, Weight: 1, MaxWidth: 48},
			{Title: "Started", Width: 8},
			{Title: "Launch", Width: 8, Weight: 1, MaxWidth: 32}, // grows for FARGATE_SPOT
		}
	case ui.TierWide:
	}
	// It fits 120 columns.
	return []ui.Column{
		task, status,
		{Title: "Health", Width: 11, Style: ui.HealthColor},
		{Title: "Task def", Width: 12, Weight: 1, MaxWidth: 48},
		{Title: "Started", Width: 8},
		{Title: "Launch", Width: 12, Weight: 1, MaxWidth: 32}, // FARGATE_SPOT
		{Title: "CPU", Width: 5},
		{Title: "Memory", Width: 6},
		{Title: "Zone", Width: 10},
		{Title: "Group", Width: 8, Weight: 1, MaxWidth: 48},
	}
}

// ecsTaskLaunch is the capacity provider of a task, or its launch type when
// it has no capacity provider.
func ecsTaskLaunch(t aws.Task) string {
	if t.CapacityProvider != "" {
		return t.CapacityProvider
	}
	return t.LaunchType
}

// ecsTaskRow builds the row of a running task. An empty value stays an
// empty cell. A task that AWS could not describe shows ?.
func ecsTaskRow(t aws.Task, tier ui.WidthTier) ui.Row {
	status := t.LastStatus
	if t.Err != "" {
		status = "?"
	}
	started, startKey := ecsAge(t.StartedAt), ecsTimeKey(t.StartedAt)
	if tier == ui.TierNarrow {
		return ui.Row{Key: t.ARN, Cells: table.Row{t.ID, status, started}, Sort: table.Row{t.ID, status, startKey}}
	}
	taskDef, launch := aws.TaskDefinitionLabel(t.TaskDefinition), ecsTaskLaunch(t)
	if tier == ui.TierMedium {
		return ui.Row{
			Key:   t.ARN,
			Cells: table.Row{t.ID, status, t.Health, taskDef, started, launch},
			Sort:  table.Row{t.ID, status, t.Health, taskDef, startKey, launch},
		}
	}
	cpuKey, memKey := ecsNumberKey(t.CPU), ecsNumberKey(t.Memory)
	return ui.Row{
		Key:   t.ARN,
		Cells: table.Row{t.ID, status, t.Health, taskDef, started, launch, t.CPU, t.Memory, t.AvailabilityZone, t.Group},
		Sort:  table.Row{t.ID, status, t.Health, taskDef, startKey, launch, cpuKey, memKey, t.AvailabilityZone, t.Group},
	}
}

// ecsNumberKey sorts a number that AWS sends as text, such as the CPU
// units of a task. Text that is not a number sorts as it is.
func ecsNumberKey(s string) string {
	var n int64
	if _, err := fmt.Sscan(s, &n); err != nil {
		return s
	}
	return ui.SortKeyInt(n)
}

func ecsStoppedColumns(tier ui.WidthTier) []ui.Column {
	task := ui.Column{Title: "Task", Width: 12, Weight: 2, MaxWidth: 36}
	reason := ui.Column{Title: "Reason", Width: 20, Weight: 3, MaxWidth: 120}
	stopped := ui.Column{Title: "Stopped", Width: 8}
	switch tier {
	case ui.TierNarrow:
		return []ui.Column{task, {Title: "Reason", Width: 16, Weight: 3, MaxWidth: 120}, stopped}
	case ui.TierMedium:
		// Stop code grows to the longest code, ServiceSchedulerInitiated.
		return []ui.Column{task, {Title: "Stop code", Width: 17, Weight: 3, MaxWidth: 25}, reason, stopped,
			{Title: "Task def", Width: 12, Weight: 1, MaxWidth: 48}}
	case ui.TierWide:
	}
	return []ui.Column{task, {Title: "Stop code", Width: 25}, reason, stopped,
		{Title: "Task def", Width: 12, Weight: 1, MaxWidth: 48}, {Title: "Started", Width: 8}}
}

// ecsStopTime is the time a task stopped: StoppedAt, else ExecutionStoppedAt,
// else StoppingAt. AWS can leave each one empty.
func ecsStopTime(t aws.Task) time.Time {
	for _, at := range []time.Time{t.StoppedAt, t.ExecutionStoppedAt, t.StoppingAt} {
		if !at.IsZero() {
			return at
		}
	}
	return time.Time{}
}

// ecsStoppedRow builds the row of a stopped task, with its stop code and
// reason inline, so no detail is needed to see why it stopped.
func ecsStoppedRow(t aws.Task, tier ui.WidthTier) ui.Row {
	reason := t.StoppedReason
	if t.Err != "" {
		reason = "? " + t.Err
	}
	at := ecsStopTime(t)
	stopped, stopKey := ecsAge(at), ecsTimeKey(at)
	if tier == ui.TierNarrow {
		return ui.Row{Key: t.ARN, Cells: table.Row{t.ID, reason, stopped}, Sort: table.Row{t.ID, reason, stopKey}}
	}
	taskDef := aws.TaskDefinitionLabel(t.TaskDefinition)
	if tier == ui.TierMedium {
		return ui.Row{
			Key:   t.ARN,
			Cells: table.Row{t.ID, t.StopCode, reason, stopped, taskDef},
			Sort:  table.Row{t.ID, t.StopCode, reason, stopKey, taskDef},
		}
	}
	started := ecsAge(t.StartedAt)
	return ui.Row{
		Key:   t.ARN,
		Cells: table.Row{t.ID, t.StopCode, reason, stopped, taskDef, started},
		Sort:  table.Row{t.ID, t.StopCode, reason, stopKey, taskDef, ecsTimeKey(t.StartedAt)},
	}
}

func ecsTaskDetail(t aws.Task) msg.TabbedContentMsg {
	if t.Err != "" {
		info, _ := buildInfo([]infoField{{K: "Task", V: t.ID}, {K: "ARN", V: t.ARN}, {K: "Error", V: t.Err}})
		return msg.TabbedContentMsg{PanelTitle: t.ID, Tabs: []msg.TabContent{{Title: "Info", Content: info, Format: "text"}}}
	}
	status := t.LastStatus
	if t.DesiredStatus != "" && t.DesiredStatus != t.LastStatus {
		status += " (desired " + t.DesiredStatus + ")"
	}
	ephemeral := ""
	if t.EphemeralGiB > 0 {
		ephemeral = fmt.Sprintf("%d GiB", t.EphemeralGiB)
	}
	platform := strings.TrimSpace(t.PlatformFamily + " " + t.PlatformVersion)
	info, _ := buildInfo([]infoField{
		{K: "Task", V: t.ID},
		{K: "ARN", V: t.ARN},
		{K: "Status", V: status},
		{K: "Health", V: t.Health},
		{K: "Stop code", V: t.StopCode},
		{K: "Stop reason", V: t.StoppedReason},
		{K: "Task def", V: aws.TaskDefinitionLabel(t.TaskDefinition)},
		{K: "Launch", V: ecsTaskLaunch(t)},
		{K: "Platform", V: platform},
		{K: "CPU", V: t.CPU},
		{K: "Memory", V: t.Memory},
		{K: "Ephemeral", V: ephemeral},
		{K: "Zone", V: t.AvailabilityZone},
		{K: "Group", V: t.Group},
		{K: "Started by", V: t.StartedBy},
		{K: "Connectivity", V: t.Connectivity},
		{K: "Exec", V: yesNo(t.ExecEnabled)},
		{K: "Created", V: logTime(t.CreatedAt.Local())},
		{K: "Pull started", V: logTime(t.PullStartedAt.Local())},
		{K: "Started", V: logTime(t.StartedAt.Local())},
		{K: "Stopping", V: logTime(t.StoppingAt.Local())},
		{K: "Stopped", V: logTime(t.StoppedAt.Local())},
	})
	network, netLinks := buildInfo(ecsTaskNetwork(t))
	tabs := []msg.TabContent{
		{Title: "Info", Content: info, Format: "text"},
		{Title: "Containers", Content: ecsContainersText(t.Containers), Format: "text"},
		{Title: "Network", Content: network, Format: "text", Links: netLinks},
	}
	if len(t.Tags) > 0 {
		tabs = append(tabs, msg.TabContent{Title: "Tags", Content: buildTagsContent(t.Tags), Format: "text"})
	}
	return msg.TabbedContentMsg{PanelTitle: t.ID, Tabs: tabs}
}

// ecsContainersText shows one block for each container of a task.
func ecsContainersText(cs []aws.Container) string {
	if len(cs) == 0 {
		return "  No containers\n"
	}
	var b strings.Builder
	for i, c := range cs {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(c.Name + "\n")
		exit := ""
		if c.ExitCode != nil {
			exit = fmt.Sprintf("%d", *c.ExitCode)
		}
		fields, _ := buildInfo([]infoField{
			{K: "  Image", V: c.Image},
			{K: "  Status", V: c.LastStatus},
			{K: "  Health", V: c.Health},
			{K: "  Exit code", V: exit},
			{K: "  Reason", V: c.Reason},
		})
		b.WriteString(fields)
	}
	return b.String()
}

// ecsTaskNetwork shows the ENI, its private IP and its subnet, which links
// to the subnet list.
func ecsTaskNetwork(t aws.Task) []infoField {
	if t.ENI == "" && t.PrivateIP == "" && t.SubnetID == "" {
		return []infoField{{K: "Network", V: "No network interface"}}
	}
	fields := []infoField{{K: "ENI", V: t.ENI}, {K: "Private IP", V: t.PrivateIP}}
	if t.SubnetID != "" {
		fields = append(fields, infoField{K: "Subnet", V: t.SubnetID, ViewID: "subnet_list", Params: map[string]string{"focus": t.SubnetID}})
	}
	return fields
}
