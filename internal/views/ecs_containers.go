package views

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The container list of an ECS task, with the jump to the log stream of a
// container. See docs/design/ecs.md, "The container list and the jump to
// the logs".

// errTaskGone replaces aws.ErrTaskNotFound on the error screen.
var errTaskGone = errors.New("The task is gone. " + ecsStoppedNote + ".") //nolint:staticcheck // shown as a sentence

// The hints of the event viewer for a container.
const (
	ecsMissingGroupHint  = "The task definition sends the logs to this group, but the group does not exist."
	ecsMissingStreamHint = "The task writes the stream when its container starts."
)

// ecsLogMargin widens the time range of the logs on each side, so a line
// from just before the start or after the stop is not cut off.
const ecsLogMargin = time.Minute

// ecsContainerKeys are the keys of the container list that the core does
// not have.
var ecsContainerKeys = struct {
	Logs ui.HintBinding
}{
	Logs: ui.NewHintBinding([]string{"enter"}, "enter", "logs"),
}

// ecsContainer is a row: a container of the task, with its log
// configuration from the task definition.
type ecsContainer struct {
	aws.Container
	Task *aws.Task      // the task, shared by the rows
	Log  *aws.LogConfig // nil: the definition has no log configuration
	// LogErr is the error of the task definition call. The Logs column
	// then shows ?, and enter shows the error (H5).
	LogErr error
}

// key is the container ARN, or the name when AWS sends no ARN.
func (c ecsContainer) key() string {
	if c.ARN != "" {
		return c.ARN
	}
	return c.Name
}

// ECSContainers lists the containers of one task.
type ECSContainers struct {
	listCore[ecsContainer]
	cluster string // ARN
	task    string // ARN
	region  string // the Region of the session
}

// NewECSContainers creates the container list of a task. region is the
// Region of the session, to see logs that are in another Region.
func NewECSContainers(svc aws.ECSService, cluster, task, region string) *ECSContainers {
	v := &ECSContainers{cluster: cluster, task: task, region: region}
	v.listCore = newListCore(listConfig[ecsContainer]{
		Noun:         "containers",
		LoadingLabel: "Loading containers...",
		ErrContext:   "listing containers",
		Key:          ecsContainer.key,
		FocusKey:     func(c ecsContainer) string { return c.Name },
		Columns:      ecsContainerColumns,
		Row:          v.row,
		Fetch:        ecsContainerFetch(svc, cluster, task),
		FetchTimeout: callTimeout,
		// The detail uses the data of the row, with no call.
		FetchDetail: func(_ context.Context, c ecsContainer) (msg.TabbedContentMsg, error) {
			return ecsContainerDetail(c), nil //nolint:contextcheck // ErrorText reads only the local AWS config files.
		},
		OnLoaded: func(items []ecsContainer, _ string) tea.Cmd {
			if len(items) == 0 || items[0].LogErr == nil {
				return nil
			}
			text := fmt.Sprintf("Could not read the task definition %s: %s. The Logs column shows ?.",
				aws.TaskDefinitionLabel(items[0].Task.TaskDefinition), aws.ErrorText(items[0].LogErr))
			return toastCmd(msg.ToastError(text))
		},
		Copy: true,
	})
	return v
}

// ecsContainerFetch reads the task and then its task definition, and joins
// each container with its log configuration. Each r repeats both calls.
func ecsContainerFetch(svc aws.ECSService, cluster, task string) fetchFunc[ecsContainer] {
	return func(ctx context.Context, _ *string) ([]ecsContainer, *string, error) {
		if svc == nil {
			return nil, nil, errNoClient
		}
		t, err := svc.DescribeTask(ctx, cluster, task)
		if errors.Is(err, aws.ErrTaskNotFound) {
			return nil, nil, errTaskGone
		}
		if err != nil {
			return nil, nil, err
		}
		var def *aws.TaskDefinition
		var logErr error
		if t.TaskDefinition != "" {
			def, logErr = svc.DescribeTaskDefinition(ctx, t.TaskDefinition)
		}
		items := make([]ecsContainer, 0, len(t.Containers))
		for _, c := range t.Containers {
			it := ecsContainer{Container: c, Task: t, LogErr: logErr}
			if def != nil {
				if d := def.Container(c.Name); d != nil {
					it.Log = d.Log
				}
			}
			items = append(items, it)
		}
		return items, nil, nil
	}
}

func (v *ECSContainers) ID() string { return "ecs_containers:" + v.task }

func (v *ECSContainers) Title() string { return aws.ARNName(v.task) + " containers" }

func (v *ECSContainers) Init() tea.Cmd { return v.init() }

func (v *ECSContainers) Footer() string { return v.footer() }

func (v *ECSContainers) View() tea.View { return tea.NewView(v.render("", "")) }

func (v *ECSContainers) KeyMap() []ui.HintBinding {
	return v.hints(ecsContainerKeys.Logs, ecsKeys.Details, ecsKeys.CopyARN,
		listKeys.Sort, listKeys.Filter, listKeys.Refresh)
}

// Update returns the view itself. enter opens the logs of the container.
func (v *ECSContainers) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := m.(tea.KeyPressMsg); ok && !v.filter.Active() && v.err == nil &&
		key.Matches(k, ecsContainerKeys.Logs.Binding) {
		if it := v.cursorItem(); it != nil {
			return v, ecsLogJump(*it, v.region)
		}
	}
	return v, v.update(m)
}

func ecsContainerColumns(tier ui.WidthTier) []ui.Column {
	status := ui.Column{Title: "Status", Width: 9, Style: ui.StateColor} // ● RUNNING
	switch tier {
	case ui.TierNarrow:
		return []ui.Column{
			{Title: "Name", Width: 12, Weight: 1, MaxWidth: 40}, status,
			{Title: "Logs", Width: 14, Weight: 2, MaxWidth: 64},
		}
	case ui.TierMedium:
		// It fits 80 columns.
		return []ui.Column{
			{Title: "Name", Width: 12, Weight: 1, MaxWidth: 40},
			{Title: "Image", Width: 14, Weight: 2, MaxWidth: 80},
			status,
			{Title: "Exit", Width: 4},
			{Title: "Health", Width: 11, Style: ui.HealthColor}, // ○ UNHEALTHY
			{Title: "Logs", Width: 14, Weight: 2, MaxWidth: 64},
		}
	case ui.TierWide:
	}
	// It fits 120 columns.
	return []ui.Column{
		{Title: "Name", Width: 16, Weight: 1, MaxWidth: 40},
		{Title: "Image", Width: 20, Weight: 2, MaxWidth: 80},
		status,
		{Title: "Exit", Width: 4},
		{Title: "Health", Width: 11, Style: ui.HealthColor},
		{Title: "Reason", Width: 20, Weight: 3, MaxWidth: 120},
		{Title: "Logs", Width: 18, Weight: 2, MaxWidth: 64},
	}
}

// row builds the row of a container. An empty value stays an empty cell.
func (v *ECSContainers) row(c ecsContainer, tier ui.WidthTier) ui.Row {
	logs := v.logsCell(c)
	if tier == ui.TierNarrow {
		return ui.Row{Key: c.key(), Cells: table.Row{c.Name, c.LastStatus, logs}}
	}
	exit, exitKey := "", ""
	if c.ExitCode != nil {
		exit, exitKey = strconv.Itoa(int(*c.ExitCode)), ui.SortKeyInt(int64(*c.ExitCode))
	}
	if tier == ui.TierMedium {
		return ui.Row{
			Key:   c.key(),
			Cells: table.Row{c.Name, c.Image, c.LastStatus, exit, c.Health, logs},
			Sort:  table.Row{c.Name, c.Image, c.LastStatus, exitKey, c.Health, logs},
		}
	}
	return ui.Row{
		Key:   c.key(),
		Cells: table.Row{c.Name, c.Image, c.LastStatus, exit, c.Health, c.Reason, logs},
		Sort:  table.Row{c.Name, c.Image, c.LastStatus, exitKey, c.Health, c.Reason, logs},
	}
}

// logsCell shows where the logs go: the group, with its Region when that
// is not the Region of the session, or the driver when it is not awslogs.
// It shows ? when the task definition call failed.
func (v *ECSContainers) logsCell(c ecsContainer) string {
	switch {
	case c.LogErr != nil:
		return "?"
	case c.Log == nil:
		return ""
	case c.Log.Driver != string(ecstypes.LogDriverAwslogs):
		return c.Log.Driver
	}
	if r := c.Log.Region(); r != "" && v.region != "" && r != v.region {
		return c.Log.Group() + " (" + r + ")"
	}
	return c.Log.Group()
}

// ecsLogStart is the earliest of the pull, create and start times of the
// task, minus a margin, or the zero time when none is set.
func ecsLogStart(t *aws.Task) time.Time {
	var start time.Time
	for _, at := range []time.Time{t.PullStartedAt, t.CreatedAt, t.StartedAt} {
		if !at.IsZero() && (start.IsZero() || at.Before(start)) {
			start = at
		}
	}
	if start.IsZero() {
		return start
	}
	return start.Add(-ecsLogMargin)
}

// ecsLogJump opens the log stream of a container in the event viewer. When
// the container cannot have a stream there, it shows a toast (H5).
func ecsLogJump(c ecsContainer, region string) tea.Cmd {
	l := c.Log
	switch {
	case c.LogErr != nil:
		return toastCmd(msg.ToastError("Could not read the task definition: " + aws.ErrorText(c.LogErr)))
	case l == nil:
		return toastCmd(msg.ToastInfo("The container has no log configuration."))
	case l.Driver != string(ecstypes.LogDriverAwslogs):
		return toastCmd(msg.ToastInfo("The container sends its logs with " + l.Driver + ", not to CloudWatch Logs."))
	case l.Group() == "":
		return toastCmd(msg.ToastInfo("The log configuration of the container names no log group."))
	case l.Region() != "" && region != "" && l.Region() != region:
		return toastCmd(msg.ToastInfo("The logs are in " + l.Region() + ". Press R to switch the Region."))
	}
	p := map[string]string{"log_group": l.Group(), "missing_hint": ecsMissingGroupHint}
	ms := func(t time.Time) string { return strconv.FormatInt(t.UnixMilli(), 10) }
	start := ecsLogStart(c.Task)
	// The branch reads LastStatus only, because LocalStack sets StoppedAt
	// on a running task.
	stop := ecsStopTime(*c.Task)
	switch {
	case c.Task.LastStatus == "STOPPED" && !start.IsZero() && !stop.IsZero():
		p["log_start"], p["log_end"] = ms(start), ms(stop.Add(ecsLogMargin))
	case !start.IsZero():
		// The viewer shows the last hour, and l walks back to the start.
		p["stream_first"] = ms(start)
	}
	stream := l.Stream(c.Name, c.Task.ID)
	if stream == "" {
		// Without a prefix (EC2 only), each stream has the Docker container
		// ID as its name, so the whole group opens.
		return tea.Batch(navigate("logs_events", p),
			toastCmd(msg.ToastInfo("The log configuration has no stream prefix, so each stream has the Docker container ID as its name. The whole group opens.")))
	}
	p["log_stream"] = stream
	p["missing_stream_hint"] = ecsMissingStreamHint
	return navigate("logs_events", p)
}

func ecsContainerDetail(c ecsContainer) msg.TabbedContentMsg {
	exit := ""
	if c.ExitCode != nil {
		exit = strconv.Itoa(int(*c.ExitCode))
	}
	info, _ := buildInfo([]infoField{
		{K: "Name", V: c.Name},
		{K: "ARN", V: c.ARN},
		{K: "Image", V: c.Image},
		{K: "Digest", V: c.ImageDigest},
		{K: "Runtime ID", V: c.RuntimeID},
		{K: "Status", V: c.LastStatus},
		{K: "Health", V: c.Health},
		{K: "Exit code", V: exit},
		{K: "Reason", V: c.Reason},
		{K: "CPU", V: c.CPU},
		{K: "Memory", V: c.Memory},
	})
	tabs := []msg.TabContent{
		{Title: "Info", Content: info, Format: "text"},
		{Title: "Network", Content: ecsContainerNetwork(c.Container), Format: "text"},
	}
	if len(c.Agents) > 0 {
		tabs = append(tabs, msg.TabContent{Title: "Agents", Content: ecsAgentsText(c.Agents), Format: "text"})
	}
	tabs = append(tabs, msg.TabContent{Title: "Logs", Content: ecsLogText(c), Format: "text"})
	return msg.TabbedContentMsg{PanelTitle: c.Name, Tabs: tabs}
}

// ecsContainerNetwork shows the private IPs and the port bindings.
func ecsContainerNetwork(c aws.Container) string {
	if len(c.PrivateIPs) == 0 && len(c.Bindings) == 0 {
		return "  No network interfaces or port bindings\n"
	}
	fields := []infoField{{K: "Private IPs", V: strings.Join(c.PrivateIPs, ", ")}}
	for _, b := range c.Bindings {
		v := fmt.Sprintf("%d -> %d", b.HostPort, b.ContainerPort)
		if b.Protocol != "" {
			v += "/" + b.Protocol
		}
		if b.BindIP != "" {
			v = b.BindIP + ":" + v
		}
		fields = append(fields, infoField{K: "Port", V: v})
	}
	text, _ := buildInfo(fields)
	return text
}

// ecsAgentsText shows one block for each managed agent, for example the
// ExecuteCommandAgent of ECS Exec.
func ecsAgentsText(agents []aws.ManagedAgent) string {
	var b strings.Builder
	for i, a := range agents {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(a.Name + "\n")
		fields, _ := buildInfo([]infoField{
			{K: "  Status", V: a.Status},
			{K: "  Reason", V: a.Reason},
			{K: "  Started", V: logTime(a.StartedAt.Local())},
		})
		b.WriteString(fields)
	}
	return b.String()
}

// ecsLogText shows the log configuration of the container and its stream.
func ecsLogText(c ecsContainer) string {
	switch {
	case c.LogErr != nil:
		return "  Could not read the task definition: " + aws.ErrorText(c.LogErr) + "\n"
	case c.Log == nil:
		return "  No log configuration\n"
	}
	fields := []infoField{{K: "Driver", V: c.Log.Driver}, {K: "Stream", V: c.Log.Stream(c.Name, c.Task.ID)}}
	for _, k := range slices.Sorted(maps.Keys(c.Log.Options)) {
		fields = append(fields, infoField{K: k, V: c.Log.Options[k]})
	}
	text, _ := buildInfo(fields)
	return text
}
