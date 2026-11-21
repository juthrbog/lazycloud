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
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The ECS cluster list. See docs/design/ecs.md.

// ecsKeys are the keys of the ECS lists that the core does not have.
var ecsKeys = struct {
	Services ui.HintBinding
	Details  ui.HintBinding
	CopyARN  ui.HintBinding
}{
	Services: ui.NewHintBinding([]string{"enter"}, "enter", "services"),
	Details:  ui.NewHintBinding([]string{"d"}, "d", "details"),
	CopyARN:  ui.NewHintBinding([]string{"y"}, "y", "copy ARN"),
}

// ECSClusters displays the ECS clusters. It loads every page, because an
// account has few clusters (decision D3).
type ECSClusters struct {
	listCore[aws.Cluster]
}

// NewECSClusters creates the cluster list view. The row key is the ARN, so
// y copies it. A focus and :ecs NAME find a cluster by name.
func NewECSClusters(svc aws.ECSService) *ECSClusters {
	v := &ECSClusters{}
	v.listCore = newListCore(listConfig[aws.Cluster]{
		Noun:         "clusters",
		LoadingLabel: "Loading clusters...",
		ErrContext:   "listing clusters",
		Key:          func(c aws.Cluster) string { return c.ARN },
		FocusKey:     func(c aws.Cluster) string { return c.Name },
		FocusMissing: v.focusMissing,
		Columns:      ecsClusterColumns,
		Row:          ecsClusterRow,
		Fetch:        fetchPages(svc, aws.ECSService.ListClustersPage, splitClusterPage),
		FetchTimeout: callTimeout,
		// The detail uses the data of the row, with no call.
		FetchDetail: func(_ context.Context, c aws.Cluster) (msg.TabbedContentMsg, error) {
			return ecsClusterDetail(c), nil
		},
		OnLoaded: func(items []aws.Cluster, _ string) tea.Cmd {
			return ecsUnreadable("cluster", items, func(c aws.Cluster) (string, string) { return c.Name, c.Err })
		},
		Copy: true,
	})
	return v
}

func (v *ECSClusters) ID() string     { return "ecs_clusters" }
func (v *ECSClusters) Title() string  { return "ECS Clusters" }
func (v *ECSClusters) Init() tea.Cmd  { return v.init() }
func (v *ECSClusters) Footer() string { return v.footer() }
func (v *ECSClusters) View() tea.View { return tea.NewView(v.render("", "")) }

func (v *ECSClusters) KeyMap() []ui.HintBinding {
	return v.hints(ecsKeys.Services, ecsKeys.Details, ecsKeys.CopyARN, listKeys.Sort, listKeys.Filter, listKeys.Refresh)
}

// Update returns the view itself, so the navigator keeps its identity.
// enter opens the services of the cluster.
func (v *ECSClusters) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := m.(tea.KeyPressMsg); ok && !v.filter.Active() && v.err == nil &&
		key.Matches(k, ecsKeys.Services.Binding) {
		if it := v.cursorItem(); it != nil {
			return v, navigate("ecs_services", map[string]string{"cluster": it.ARN})
		}
	}
	return v, v.update(m)
}

// focusMissing answers a focus on a name that the list does not have.
func (v *ECSClusters) focusMissing(name string) tea.Cmd {
	return toastCmd(msg.ToastInfo(fmt.Sprintf("No cluster named %s in the loaded clusters", name)))
}

func splitClusterPage(p *aws.ClusterPage) ([]aws.Cluster, *string) {
	if p == nil {
		return nil, nil
	}
	return p.Clusters, p.Next
}

func ecsClusterColumns(tier ui.WidthTier) []ui.Column {
	status := ui.Column{Title: "Status", Width: 16, Style: ui.StateColor} // ◌ DEPROVISIONING
	switch tier {
	case ui.TierNarrow:
		return []ui.Column{
			{Title: "Name", Width: 16, Weight: 3, MaxWidth: 64},
			status,
			{Title: "Running", Width: 7},
		}
	case ui.TierMedium:
		return []ui.Column{
			{Title: "Name", Width: 20, Weight: 3, MaxWidth: 64},
			status,
			{Title: "Services", Width: 8},
			{Title: "Running", Width: 7},
			{Title: "Pending", Width: 7, Style: ui.CountColor},
		}
	case ui.TierWide:
	}
	return []ui.Column{
		{Title: "Name", Width: 24, Weight: 3, MaxWidth: 64},
		status,
		{Title: "Services", Width: 8},
		{Title: "Running", Width: 7},
		{Title: "Pending", Width: 7, Style: ui.CountColor},
		{Title: "Instances", Width: 9},
		{Title: "Capacity providers", Width: 18, Weight: 1, MaxWidth: 48},
	}
}

// ecsCount is a count cell and its sort key. A cluster that AWS could not
// describe shows ? and sorts first.
func ecsCount(n int32, failed bool) (string, string) {
	if failed {
		return "?", ""
	}
	return strconv.Itoa(int(n)), ui.SortKeyInt(int64(n))
}

// ecsClusterRow builds the row of one cluster. The counts sort as numbers.
func ecsClusterRow(c aws.Cluster, tier ui.WidthTier) ui.Row {
	failed := c.Err != ""
	status := c.Status
	if failed {
		status = "?"
	}
	running, runKey := ecsCount(c.RunningTasks, failed)
	if tier == ui.TierNarrow {
		return ui.Row{Key: c.ARN, Cells: table.Row{c.Name, status, running}, Sort: table.Row{c.Name, status, runKey}}
	}
	services, svcKey := ecsCount(c.ActiveServices, failed)
	pending, penKey := ecsCount(c.PendingTasks, failed)
	if tier == ui.TierMedium {
		return ui.Row{
			Key:   c.ARN,
			Cells: table.Row{c.Name, status, services, running, pending},
			Sort:  table.Row{c.Name, status, svcKey, runKey, penKey},
		}
	}
	instances, instKey := ecsCount(c.Instances, failed)
	providers := strings.Join(c.CapacityProviders, ", ")
	return ui.Row{
		Key:   c.ARN,
		Cells: table.Row{c.Name, status, services, running, pending, instances, providers},
		Sort:  table.Row{c.Name, status, svcKey, runKey, penKey, instKey, providers},
	}
}

// ecsStrategyText shows a capacity provider strategy, for example
// "FARGATE (base 1, weight 2), FARGATE_SPOT (weight 1)".
func ecsStrategyText(items []aws.CapacityProviderItem) string {
	parts := make([]string, 0, len(items))
	for _, it := range items {
		var w []string
		if it.Base > 0 {
			w = append(w, fmt.Sprintf("base %d", it.Base))
		}
		w = append(w, fmt.Sprintf("weight %d", it.Weight))
		parts = append(parts, fmt.Sprintf("%s (%s)", it.Name, strings.Join(w, ", ")))
	}
	return strings.Join(parts, ", ")
}

func ecsClusterDetail(c aws.Cluster) msg.TabbedContentMsg {
	if c.Err != "" {
		info, _ := buildInfo([]infoField{{K: "Name", V: c.Name}, {K: "ARN", V: c.ARN}, {K: "Error", V: c.Err}})
		return msg.TabbedContentMsg{PanelTitle: c.Name, Tabs: []msg.TabContent{{Title: "Info", Content: info, Format: "text"}}}
	}
	execLog := c.ExecLogging
	var dest []string
	if c.ExecLogGroup != "" {
		dest = append(dest, "log group "+c.ExecLogGroup)
	}
	if c.ExecS3Bucket != "" {
		dest = append(dest, "S3 bucket "+c.ExecS3Bucket)
	}
	if len(dest) > 0 {
		execLog += " (" + strings.Join(dest, ", ") + ")"
	}
	info, _ := buildInfo([]infoField{
		{K: "Name", V: c.Name},
		{K: "ARN", V: c.ARN},
		{K: "Status", V: c.Status},
		{K: "Services", V: strconv.Itoa(int(c.ActiveServices))},
		{K: "Running tasks", V: strconv.Itoa(int(c.RunningTasks))},
		{K: "Pending tasks", V: strconv.Itoa(int(c.PendingTasks))},
		{K: "Instances", V: strconv.Itoa(int(c.Instances))},
		{K: "Insights", V: orDash(c.ContainerInsights)},
		{K: "Exec logging", V: execLog},
		{K: "Exec KMS key", V: c.ExecKMSKey},
		{K: "Capacity", V: strings.Join(c.CapacityProviders, ", ")},
		{K: "Default strategy", V: ecsStrategyText(c.DefaultStrategy)},
	})
	tabs := []msg.TabContent{{Title: "Info", Content: info, Format: "text"}}
	if len(c.Tags) > 0 {
		tabs = append(tabs, msg.TabContent{Title: "Tags", Content: buildTagsContent(c.Tags), Format: "text"})
	}
	return msg.TabbedContentMsg{PanelTitle: c.Name, Tabs: tabs}
}

// ecsUnreadable logs each item that AWS could not describe and returns one
// toast that names them, or nil. Their counts show ?.
func ecsUnreadable[T any](noun string, items []T, fields func(T) (name, reason string)) tea.Cmd {
	var names []string
	for _, it := range items {
		name, reason := fields(it)
		if reason == "" {
			continue
		}
		eventlog.Errorf(eventlog.CatAWS, "Could not describe %s %s: %s", noun, name, reason)
		names = append(names, fmt.Sprintf("%s (%s)", name, reason))
	}
	if len(names) == 0 {
		return nil
	}
	shown := names[:min(len(names), maxFailedNames)]
	text := strings.Join(shown, ", ")
	if n := len(names) - len(shown); n > 0 {
		text += fmt.Sprintf(" and %d more", n)
	}
	text = fmt.Sprintf("Could not describe %d %s(s): %s. Their counts show ?.", len(names), noun, text)
	return func() tea.Msg { return msg.ToastError(text) }
}
