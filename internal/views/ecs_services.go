package views

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The ECS service list of a cluster. See docs/design/ecs.md.

// ecsServicePageBudget is 10 pages of 100: 1,000 services. A page costs 11
// calls, so 10 pages fit in the burst of the quota and half a second of
// refill (decision D3).
const ecsServicePageBudget = 10

// ecsEventsNote heads the Events tab: AWS keeps only the newest events.
const ecsEventsNote = "AWS keeps the 100 latest events of a service."

// ecsServiceOpen is enter on a service: it opens the tasks of the service.
var ecsServiceOpen = ui.NewHintBinding([]string{"enter"}, "enter", "tasks")

// ECSServices lists the services of one cluster.
type ECSServices struct {
	listCore[aws.ClusterService]
	cluster string // ARN
}

// NewECSServices creates the service list of a cluster. The row key is the
// ARN, so y copies it.
func NewECSServices(svc aws.ECSService, cluster string) *ECSServices {
	v := &ECSServices{cluster: cluster}
	v.listCore = newListCore(listConfig[aws.ClusterService]{
		Noun:         "services",
		LoadingLabel: "Loading services...",
		ErrContext:   "listing services",
		Key:          func(s aws.ClusterService) string { return s.ARN },
		FocusKey:     func(s aws.ClusterService) string { return s.Name },
		Columns:      ecsServiceColumns,
		Row:          ecsServiceRow,
		Fetch: fetchPages(svc, func(s aws.ECSService, ctx context.Context, tok *string) (*aws.ServicePage, error) {
			return s.ListServicesPage(ctx, cluster, tok)
		}, splitServicePage),
		// One page is up to 11 calls, and a throttle slows them (H11).
		FetchTimeout: listTimeout,
		FetchDetail: func(ctx context.Context, s aws.ClusterService) (msg.TabbedContentMsg, error) {
			return ecsServiceDetail(ctx, svc, s)
		},
		OnLoaded: func(items []aws.ClusterService, _ string) tea.Cmd {
			return ecsUnreadable("service", items, func(s aws.ClusterService) (string, string) { return s.Name, s.Err })
		},
		PageBudget: ecsServicePageBudget,
		MoreKey:    true,
		Copy:       true,
	})
	return v
}

func (v *ECSServices) ID() string     { return "ecs_services:" + v.cluster }
func (v *ECSServices) Title() string  { return aws.ARNName(v.cluster) + " services" }
func (v *ECSServices) Init() tea.Cmd  { return v.init() }
func (v *ECSServices) Footer() string { return v.footer() }
func (v *ECSServices) View() tea.View { return tea.NewView(v.render("", "")) }

func (v *ECSServices) KeyMap() []ui.HintBinding {
	return v.hints(ecsServiceOpen, ecsKeys.Details, ecsKeys.CopyARN, listKeys.More,
		listKeys.Sort, listKeys.Filter, listKeys.Refresh)
}

// Update returns the view itself. enter opens the tasks of the service.
func (v *ECSServices) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := m.(tea.KeyPressMsg); ok && !v.filter.Active() && v.err == nil &&
		key.Matches(k, ecsServiceOpen.Binding) {
		if it := v.cursorItem(); it != nil {
			return v, navigate("ecs_tasks", map[string]string{"cluster": v.cluster, "service": it.Name})
		}
	}
	return v, v.update(m)
}

func splitServicePage(p *aws.ServicePage) ([]aws.ClusterService, *string) {
	if p == nil {
		return nil, nil
	}
	return p.Services, p.Next
}

func ecsServiceColumns(tier ui.WidthTier) []ui.Column {
	tasks := ui.Column{Title: "Tasks", Width: 9, Style: ui.RatioColor}        // 1000/1000
	rollout := ui.Column{Title: "Rollout", Width: 13, Style: ui.RolloutColor} // ◌ IN_PROGRESS
	switch tier {
	case ui.TierNarrow:
		return []ui.Column{
			{Title: "Name", Width: 16, Weight: 3, MaxWidth: 64},
			tasks,
			rollout,
		}
	case ui.TierMedium:
		// It fits 80 columns: Tasks shows the pending tasks as the gap
		// between running and desired.
		return []ui.Column{
			{Title: "Name", Width: 14, Weight: 3, MaxWidth: 64},
			{Title: "Status", Width: 10, Style: ui.StateColor}, // ◌ DRAINING
			tasks,
			{Title: "Launch", Width: 8, Weight: 1, MaxWidth: 32},
			{Title: "Task definition", Width: 12, Weight: 2, MaxWidth: 48},
			rollout,
		}
	case ui.TierWide:
	}
	// It fits 120 columns.
	return []ui.Column{
		{Title: "Name", Width: 17, Weight: 3, MaxWidth: 64},
		{Title: "Status", Width: 10, Style: ui.StateColor},
		tasks,
		{Title: "Pending", Width: 7, Style: ui.CountColor},
		{Title: "Launch", Width: 10, Weight: 1, MaxWidth: 32},
		{Title: "Task definition", Width: 16, Weight: 2, MaxWidth: 48},
		rollout,
		{Title: "Strategy", Width: 10},
		{Title: "Created", Width: 10},
	}
}

// ecsLaunchText shows the launch type, or the capacity providers when the
// service uses a strategy and has no launch type.
func ecsLaunchText(launchType string, strategy []aws.CapacityProviderItem) string {
	if launchType != "" {
		return launchType
	}
	names := make([]string, 0, len(strategy))
	for _, it := range strategy {
		names = append(names, it.Name)
	}
	return strings.Join(names, ",")
}

// ecsServiceRow builds the row of one service. Tasks sorts by the running
// count. A service that AWS could not describe shows ?.
func ecsServiceRow(s aws.ClusterService, tier ui.WidthTier) ui.Row {
	failed := s.Err != ""
	tasks, tasksKey := fmt.Sprintf("%d/%d", s.Running, s.Desired), ui.SortKeyInt(int64(s.Running))
	status := s.Status
	if failed {
		tasks, tasksKey, status = "?", "", "?"
	}
	rollout := ""
	if p := s.Primary(); p != nil {
		rollout = p.RolloutState
	}
	if tier == ui.TierNarrow {
		return ui.Row{Key: s.ARN, Cells: table.Row{s.Name, tasks, rollout}, Sort: table.Row{s.Name, tasksKey, rollout}}
	}
	launch := ecsLaunchText(s.LaunchType, s.CapacityStrategy)
	taskDef := aws.TaskDefinitionLabel(s.TaskDefinition)
	if tier == ui.TierMedium {
		return ui.Row{
			Key:   s.ARN,
			Cells: table.Row{s.Name, status, tasks, launch, taskDef, rollout},
			Sort:  table.Row{s.Name, status, tasksKey, launch, taskDef, rollout},
		}
	}
	pending, penKey := ecsCount(s.Pending, failed)
	created := logDate(s.CreatedAt.Local())
	return ui.Row{
		Key:   s.ARN,
		Cells: table.Row{s.Name, status, tasks, pending, launch, taskDef, rollout, s.Strategy, created},
		Sort:  table.Row{s.Name, status, tasksKey, penKey, launch, taskDef, rollout, s.Strategy, created},
	}
}

// ecsServiceDetail builds the detail of a service from its row, and reads
// its current service deployment. A failure of that call shows on its
// line, and the panel still opens (decision D5).
func ecsServiceDetail(ctx context.Context, svc aws.ECSService, s aws.ClusterService) (msg.TabbedContentMsg, error) {
	if s.Err != "" {
		info, _ := buildInfo([]infoField{{K: "Name", V: s.Name}, {K: "ARN", V: s.ARN}, {K: "Error", V: s.Err}})
		return msg.TabbedContentMsg{PanelTitle: s.Name, Tabs: []msg.TabContent{{Title: "Info", Content: info, Format: "text"}}}, nil
	}
	var current *aws.ServiceDeployment
	var currentErr error
	if s.CurrentDeployment != "" {
		if svc == nil {
			return msg.TabbedContentMsg{}, errNoClient
		}
		current, currentErr = svc.DescribeServiceDeployment(ctx, s.CurrentDeployment)
	}
	info, infoLinks := buildInfo(ecsServiceInfo(s))
	network, netLinks := buildInfo(ecsServiceNetwork(s))
	tabs := []msg.TabContent{
		{Title: "Info", Content: info, Format: "text", Links: infoLinks},
		//nolint:contextcheck // ErrorText reads only the local AWS config files.
		{Title: "Deployments", Content: ecsDeploymentsText(s, current, currentErr), Format: "text"},
		{Title: "Events", Content: ecsEventsText(s.Events), Format: "text"},
		{Title: "Network", Content: network, Format: "text", Links: netLinks},
	}
	if len(s.Tags) > 0 {
		tabs = append(tabs, msg.TabContent{Title: "Tags", Content: buildTagsContent(s.Tags), Format: "text"})
	}
	return msg.TabbedContentMsg{PanelTitle: s.Name, Tabs: tabs}, nil
}

func ecsServiceInfo(s aws.ClusterService) []infoField {
	launch := s.LaunchType
	if launch == "" {
		launch = ecsStrategyText(s.CapacityStrategy)
	}
	circuit := "off"
	if s.CircuitBreaker {
		circuit = "on, without rollback"
		if s.CircuitRollback {
			circuit = "on, with rollback"
		}
	}
	strategy := s.Strategy
	if s.MinHealthyPercent > 0 || s.MaxPercent > 0 {
		strategy += fmt.Sprintf(" (min %d%%, max %d%%)", s.MinHealthyPercent, s.MaxPercent)
	}
	express := ""
	if s.Managed {
		express = "Yes, Express Mode manages this service"
	}
	return []infoField{
		{K: "Name", V: s.Name},
		{K: "ARN", V: s.ARN},
		{K: "Status", V: s.Status},
		{K: "Tasks", V: fmt.Sprintf("%d running, %d desired, %d pending", s.Running, s.Desired, s.Pending)},
		{K: "Launch", V: launch},
		{K: "Platform", V: s.PlatformVersion},
		ecsTaskDefLink(s.TaskDefinition),
		{K: "Scheduling", V: s.SchedulingStrategy},
		{K: "Controller", V: s.Controller},
		{K: "Strategy", V: strings.TrimSpace(strategy)},
		{K: "Circuit breaker", V: circuit},
		{K: "Exec", V: yesNo(s.ExecEnabled)},
		{K: "Express Mode", V: express},
		{K: "Created", V: logTime(s.CreatedAt.Local())},
		{K: "Created by", V: s.CreatedBy},
	}
}

// ecsDeploymentsText shows the current service deployment, then each entry
// of Service.Deployments.
func ecsDeploymentsText(s aws.ClusterService, current *aws.ServiceDeployment, currentErr error) string {
	var b strings.Builder
	switch {
	case currentErr != nil:
		fmt.Fprintf(&b, "Current deployment: failed: %s\n\n", aws.ErrorText(currentErr))
	case current != nil:
		b.WriteString("Current deployment\n")
		fields, _ := buildInfo([]infoField{
			{K: "  Status", V: strings.TrimSpace(current.Status + " " + parens(current.StatusReason))},
			{K: "  Stage", V: current.LifecycleStage},
			{K: "  Target", V: ecsRevisionText(current)},
			{K: "  Rollback", V: strings.TrimSpace(current.RollbackReason + " " + parens(logTime(current.RollbackStarted.Local())))},
			{K: "  Circuit", V: ecsCircuitText(current)},
			{K: "  Alarms", V: ecsAlarmText(current)},
			{K: "  Started", V: logTime(current.StartedAt.Local())},
			{K: "  Finished", V: logTime(current.FinishedAt.Local())},
		})
		b.WriteString(fields + "\n")
	}
	if len(s.Deployments) == 0 {
		b.WriteString("  No deployments\n")
		return b.String()
	}
	for i, d := range s.Deployments {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%s %s\n", d.Status, d.ID)
		fields, _ := buildInfo([]infoField{
			{K: "  Task def", V: aws.TaskDefinitionLabel(d.TaskDefinition)},
			{K: "  Tasks", V: fmt.Sprintf("%d running, %d desired, %d pending", d.Running, d.Desired, d.Pending)},
			{K: "  Failed tasks", V: ecsNonZero(d.Failed)},
			{K: "  Rollout", V: strings.TrimSpace(d.RolloutState + " " + parens(d.RolloutReason))},
			{K: "  Created", V: logTime(d.CreatedAt.Local())},
			{K: "  Updated", V: logTime(d.UpdatedAt.Local())},
		})
		b.WriteString(fields)
	}
	return b.String()
}

func ecsRevisionText(d *aws.ServiceDeployment) string {
	if d.TargetRevision == "" {
		return ""
	}
	return fmt.Sprintf("%s (%d running of %d, %d pending)", aws.ARNName(d.TargetRevision),
		d.TargetRunning, d.TargetRequested, d.TargetPending)
}

func ecsCircuitText(d *aws.ServiceDeployment) string {
	if d.CircuitStatus == "" {
		return ""
	}
	return fmt.Sprintf("%s (%d of %d failures)", d.CircuitStatus, d.CircuitFailures, d.CircuitThreshold)
}

func ecsAlarmText(d *aws.ServiceDeployment) string {
	if d.AlarmStatus == "" {
		return ""
	}
	if len(d.TriggeredAlarms) == 0 {
		return d.AlarmStatus
	}
	return d.AlarmStatus + ", triggered: " + strings.Join(d.TriggeredAlarms, ", ")
}

// parens puts s in parentheses, or returns "" for an empty s.
func parens(s string) string {
	if s == "" {
		return ""
	}
	return "(" + s + ")"
}

func ecsNonZero(n int32) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(int(n))
}

// ecsEventsText lists the events, newest first, as AWS sends them.
func ecsEventsText(events []aws.ServiceEvent) string {
	var b strings.Builder
	b.WriteString(ecsEventsNote + "\n\n")
	if len(events) == 0 {
		b.WriteString("  No events\n")
		return b.String()
	}
	for _, e := range events {
		fmt.Fprintf(&b, "%s  %s\n", logTime(e.Time.Local()), e.Message)
	}
	return b.String()
}

// ecsServiceNetwork lists the subnets and the security groups, each a link
// to its list, then the load balancers and the service registries.
func ecsServiceNetwork(s aws.ClusterService) []infoField {
	var fields []infoField
	for _, id := range s.Subnets {
		fields = append(fields, infoField{K: "Subnet", V: id, ViewID: "subnet_list", Params: map[string]string{"focus": id}})
	}
	for _, id := range s.SecurityGroups {
		fields = append(fields, infoField{K: "Security group", V: id, ViewID: "sg_list", Params: map[string]string{"focus": id}})
	}
	if s.PublicIP != "" {
		fields = append(fields, infoField{K: "Public IP", V: s.PublicIP})
	}
	for _, lb := range s.LoadBalancers {
		target := lb.TargetGroupARN
		if target == "" {
			target = lb.Name
		}
		fields = append(fields, infoField{K: "Load balancer", V: fmt.Sprintf("%s → %s:%d", target, lb.Container, lb.Port)})
	}
	for _, r := range s.Registries {
		fields = append(fields, infoField{K: "Registry", V: r})
	}
	if len(fields) == 0 {
		return []infoField{{K: "Network", V: "No awsvpc configuration"}}
	}
	return fields
}
