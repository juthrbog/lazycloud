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
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

type ec2InstanceDetailMsg struct {
	detail *aws.InstanceDetail
	err    error
}

type ec2SSMSessionFinishedMsg struct {
	instanceID   string
	instanceName string
	err          error
}

type ec2DelayedRefreshMsg struct{}

type ec2InstanceMutatedMsg struct {
	action     string   // "started", "stopped", "rebooted", "terminated"
	ids        []string // the instances of the action
	instanceID string   // the IDs joined, for the log and the toast
	err        error
}

// Outcome implements msg.Outcome.
func (m ec2InstanceMutatedMsg) Outcome() msg.ToastMsg {
	if m.err != nil {
		return msg.ToastError(fmt.Sprintf("%s failed for %s: %s", actionName(m.action), m.instanceID, aws.ErrorCode(m.err)))
	}
	return msg.ToastSuccess("Instance " + m.action + ": " + m.instanceID)
}

// ec2ReadOnlyText is the toast when ReadOnly mode is on at the confirm
// reply. It is the same text that the app shows for a blocked key.
const ec2ReadOnlyText = "ReadOnly mode — press W to switch"

// ec2Action describes one Manage action.
type ec2Action struct {
	label        string // picker label and prompt verb, for example "Stop"
	busy         string // spinner verb, for example "Stopping"
	past         string // ec2InstanceMutatedMsg action, for example "stopped"
	confirmID    string // confirm Action id, for example "ec2_stop"
	transitional string // optimistic state until the next refresh
	// quick asks with a y/n prompt. Starting is reversible and destroys
	// nothing, so y/n is enough. It still costs money, so it asks.
	quick bool
	run   func(aws.EC2Service, context.Context, []string) error
}

var ec2Actions = []ec2Action{
	{"Start", "Starting", "started", "ec2_start", "pending", true, aws.EC2Service.StartInstances},
	{"Stop", "Stopping", "stopped", "ec2_stop", "stopping", false, aws.EC2Service.StopInstances},
	{"Reboot", "Rebooting", "rebooted", "ec2_reboot", "pending", false, aws.EC2Service.RebootInstances},
	{"Terminate", "Terminating", "terminated", "ec2_terminate", "shutting-down", false, aws.EC2Service.TerminateInstances},
}

func ec2ActionByLabel(label string) (ec2Action, bool) {
	for _, a := range ec2Actions {
		if a.label == label {
			return a, true
		}
	}
	return ec2Action{}, false
}

func ec2ActionByConfirmID(id string) (ec2Action, bool) {
	for _, a := range ec2Actions {
		if a.confirmID == id {
			return a, true
		}
	}
	return ec2Action{}, false
}

// ec2RefreshDelay gives AWS time to register a state change before the
// list loads again.
const ec2RefreshDelay = 2 * time.Second

// ec2Keys are the keys only the EC2 list has. An SSM session is an
// interactive shell on the instance, so ReadOnly mode blocks it.
var ec2Keys = struct {
	Connect ui.HintBinding
	Manage  ui.HintBinding
}{
	Connect: ui.HintBinding{Binding: key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "connect (SSM)")), Mode: ui.ModeReadWrite},
	Manage:  ui.HintBinding{Binding: key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "manage")), Mode: ui.ModeReadWrite},
}

// EC2List displays all EC2 instances.
type EC2List struct {
	listCore[aws.Instance]
	ec2       aws.EC2Service
	awsClient *aws.Client
	pending   pendingConfirm // instances targeted by a Manage action
}

// NewEC2List creates the EC2 instance list view.
func NewEC2List(ec2 aws.EC2Service, awsClient *aws.Client) *EC2List {
	e := &EC2List{ec2: ec2, awsClient: awsClient}
	e.listCore = newListCore(listConfig[aws.Instance]{
		Noun:         "instances",
		LoadingLabel: "Loading EC2 instances...",
		ErrContext:   "listing EC2 instances",
		Key:          func(i aws.Instance) string { return i.ID },
		Columns:      ec2Columns,
		Row:          ec2Row,
		Fetch:        fetchPages(ec2, aws.EC2Service.ListInstancesPage, splitInstancePage),
		FetchTimeout: callTimeout,
		Refresh:      refreshBy(ec2, aws.EC2Service.RefreshInstances, callTimeout),
		OpenDetail:   func(i aws.Instance) tea.Cmd { return e.fetchDetail(i.ID) },
		FocusMissing: e.fetchDetail, // a link opens the detail even for an unlisted ID
		MultiSelect:  true,
		Copy:         true,
		CopyNoun:     "instance ID",
	})
	return e
}

func (e *EC2List) ID() string     { return "ec2_list" }
func (e *EC2List) Title() string  { return "EC2 Instances" }
func (e *EC2List) Init() tea.Cmd  { return e.init() }
func (e *EC2List) Footer() string { return e.footer() }
func (e *EC2List) View() tea.View { return tea.NewView(e.render("", "")) }

func (e *EC2List) KeyMap() []ui.HintBinding {
	return e.hints(listKeys.Details, ec2Keys.Connect, ec2Keys.Manage, listKeys.CopyID,
		listKeys.Select, listKeys.Sort, listKeys.Filter, listKeys.Refresh)
}

// Update handles the Manage flow, SSM and the detail reply, and hands the
// rest to the core. It returns the view itself, so the navigator keeps its
// identity.
func (e *EC2List) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case ui.PickerResultMsg:
		if m.ID == "action" {
			return e, e.onActionPicked(m)
		}
	case ui.ConfirmResultMsg:
		return e, e.onConfirm(m)
	case ec2InstanceMutatedMsg:
		return e, e.onMutated(m)
	case ec2DelayedRefreshMsg:
		return e, e.backgroundReload("Refreshing instances...")
	case ec2SSMSessionFinishedMsg:
		return e, e.onSSMFinished(m)
	case ec2InstanceDetailMsg:
		return e, e.onDetail(m)
	case tea.KeyPressMsg:
		// The error screen hides the rows, so o and m must not act on them.
		if !e.CapturingInput() && e.err == nil {
			switch {
			case key.Matches(m, ec2Keys.Connect.Binding):
				return e, e.connect()
			case key.Matches(m, ec2Keys.Manage.Binding):
				return e, e.manage()
			}
		}
	}
	return e, e.update(m)
}

func splitInstancePage(p *aws.InstancePage) ([]aws.Instance, *string) {
	return p.Instances, nextToken(p.HasMorePages, p.Token)
}

func (e *EC2List) fetchDetail(instanceID string) tea.Cmd {
	svc := e.ec2
	ctx := e.viewContext()
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Fetching details for instance: %s", instanceID)
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		detail, err := svc.GetInstanceDetail(ctx, instanceID)
		return ec2InstanceDetailMsg{detail: detail, err: err}
	}
}

// onDetail opens the detail panel. An error gives a toast and the list stays.
func (e *EC2List) onDetail(m ec2InstanceDetailMsg) tea.Cmd {
	if m.err != nil {
		eventlog.Errorf(eventlog.CatAWS, "Instance details failed: %v", m.err)
		return detailErrorToast(m.err)
	}
	if m.detail == nil {
		return nil
	}
	d := m.detail
	title := d.InstanceID
	if d.Name != "" {
		title = d.Name + " (" + d.InstanceID + ")"
	}
	infoContent, infoLinks := buildEC2InfoContentWithLinks(d)
	tabs := []msg.TabContent{
		{Title: "Info", Content: infoContent, Format: "text", Links: infoLinks},
		{Title: "JSON", Content: d.DetailJSON(), Format: "json"},
	}
	if len(d.SecurityGroups) > 0 {
		sgContent, sgLinks := buildSGContentWithLinks(d.SecurityGroups)
		tabs = append(tabs, msg.TabContent{
			Title: "Security Groups", Content: sgContent, Format: "text", Links: sgLinks,
		})
	}
	if len(d.Tags) > 0 {
		tabs = append(tabs, msg.TabContent{
			Title: "Tags", Content: buildTagsContent(d.Tags), Format: "text",
		})
	}
	return func() tea.Msg {
		return msg.TabbedContentMsg{PanelTitle: title, Tabs: tabs}
	}
}

// connect starts an SSM session on the cursor instance.
func (e *EC2List) connect() tea.Cmd {
	if e.awsClient == nil {
		return func() tea.Msg {
			return msg.ToastError("AWS client not available — check the profile")
		}
	}
	inst := e.cursorItem()
	if inst == nil {
		return nil
	}
	if inst.State != "running" {
		state := inst.State
		return func() tea.Msg {
			return msg.ToastError("Instance is " + state + " — must be running for SSM")
		}
	}
	if !aws.AWSCLIAvailable() {
		return func() tea.Msg {
			return msg.ToastError("aws CLI not found — install it first")
		}
	}
	if !aws.SSMPluginAvailable() {
		return func() tea.Msg {
			return msg.ToastError("session-manager-plugin not found — install it first")
		}
	}
	label := inst.ID
	if inst.Name != "" {
		label = inst.Name + " (" + inst.ID + ")"
	}
	eventlog.Infof(eventlog.CatAWS, "Starting SSM session: %s", label)
	id := inst.ID
	name := inst.Name
	ssmCmd := e.awsClient.SSMSessionCmd(id, label)
	return tea.Exec(ssmCmd, func(err error) tea.Msg {
		return ec2SSMSessionFinishedMsg{instanceID: id, instanceName: name, err: err}
	})
}

// onSSMFinished reports the end of a session and reloads the list, because
// the state may have changed during the session.
func (e *EC2List) onSSMFinished(m ec2SSMSessionFinishedMsg) tea.Cmd {
	if m.err != nil {
		eventlog.Errorf(eventlog.CatAWS, "SSM session failed for %s: %v", m.instanceID, m.err)
		return func() tea.Msg {
			return msg.ToastError("SSM session failed: " + aws.ErrorText(m.err))
		}
	}
	label := m.instanceID
	if m.instanceName != "" {
		label = m.instanceName
	}
	eventlog.Infof(eventlog.CatAWS, "SSM session ended: %s", label)
	return tea.Batch(e.reload("Refreshing instances..."), func() tea.Msg {
		return msg.ToastSuccess("Session ended: " + label)
	})
}

// manage opens the action picker for the targets.
func (e *EC2List) manage() tea.Cmd {
	ids := e.targets()
	if len(ids) == 0 {
		return nil
	}
	actions := e.commonActionsForSelection(ids)
	if len(actions) == 0 {
		return func() tea.Msg {
			return msg.ToastError("No common actions for selected instances")
		}
	}
	e.pending.hold(ids)
	title := "Manage Instance"
	if len(ids) > 1 {
		title = fmt.Sprintf("Manage %d Instances", len(ids))
	}
	return func() tea.Msg {
		return msg.RequestActionPickerMsg{Title: title, Options: actions}
	}
}

// onActionPicked asks for confirmation of the picked action. A canceled
// picker forgets the targets.
func (e *EC2List) onActionPicked(m ui.PickerResultMsg) tea.Cmd {
	if a, ok := ec2ActionByLabel(m.Value); ok && m.Selected >= 0 {
		return e.pending.ask(e.confirmRequest(a))
	}
	e.pending.clear()
	return nil
}

// commonActionsForSelection returns the actions available for ALL selected instances.
// Returns nil if the selection contains instances in mixed states with no common actions.
func (e *EC2List) commonActionsForSelection(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	var common []string
	for i, id := range ids {
		inst := e.find(id)
		if inst == nil {
			return nil
		}
		actions := actionsForState(inst.State)
		if len(actions) == 0 {
			return nil
		}
		if i == 0 {
			common = actions
			continue
		}
		// Intersect
		set := make(map[string]bool, len(actions))
		for _, a := range actions {
			set[a] = true
		}
		filtered := common[:0]
		for _, a := range common {
			if set[a] {
				filtered = append(filtered, a)
			}
		}
		common = filtered
	}
	return common
}

// actionName returns the name of the action for a past-tense action of
// ec2InstanceMutatedMsg, for example "Terminate" for "terminated".
func actionName(done string) string {
	switch done {
	case "started":
		return "Start"
	case "stopped":
		return "Stop"
	case "rebooted":
		return "Reboot"
	case "terminated":
		return "Terminate"
	}
	return done
}

func actionsForState(state string) []string {
	switch state {
	case "stopped":
		return []string{"Start"}
	case "running":
		return []string{"Stop", "Reboot", "Terminate"}
	default:
		return nil
	}
}

// confirmRequest builds the confirm prompt for a on the pending targets.
// It lists the names from the rows the view holds, so it must run on the
// Update goroutine.
func (e *EC2List) confirmRequest(a ec2Action) msg.RequestConfirmMsg {
	ids := e.pending.keys
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		name := id
		if inst := e.find(id); inst != nil && inst.Name != "" {
			name = inst.Name + " (" + id + ")"
		}
		names = append(names, name)
	}
	return msg.RequestConfirmMsg{
		Message: confirmMessage(a.label, len(ids), "instance(s)", names, ""),
		Action:  a.confirmID,
		Quick:   a.quick,
	}
}

// onConfirm runs the pending action when the reply confirms it. Any other
// reply only clears the targets.
func (e *EC2List) onConfirm(m ui.ConfirmResultMsg) tea.Cmd {
	ids, ok := e.pending.take(m)
	if !ok {
		return nil
	}
	a, ok := ec2ActionByConfirmID(m.Action)
	if !ok {
		return nil
	}
	// The mode can change while the prompt is open.
	if ui.IsReadOnly() {
		return func() tea.Msg { return msg.ToastError(ec2ReadOnlyText) }
	}
	e.updateItems(ids, func(i *aws.Instance) { i.State = a.transitional })
	return tea.Batch(e.busy(fmt.Sprintf("%s %d instance(s)...", a.busy, len(ids))), e.mutate(a, ids))
}

// mutate runs a on the instances in one batch call. The call uses a
// detached context, so leaving the view does not stop it.
func (e *EC2List) mutate(a ec2Action, ids []string) tea.Cmd {
	svc := e.ec2
	ctx := e.viewContext()
	return func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "%s %d instances", a.busy, len(ids))
		ctx, cancel := detached(ctx)
		defer cancel()
		err := a.run(svc, ctx, ids)
		return ec2InstanceMutatedMsg{action: a.past, ids: ids, instanceID: strings.Join(ids, ", "), err: err}
	}
}

// onMutated handles the end of an action. After a failure it refreshes the
// instances: an action on several instances can partly succeed (a terminate
// stops at a protected instance in one zone and goes on in another), so the
// list must show their real state and not the state the view guessed. After
// a success it waits before the reload: without the delay, DescribeInstances
// may return the old state and overwrite the guess.
func (e *EC2List) onMutated(m ec2InstanceMutatedMsg) tea.Cmd {
	e.table.DeselectAll()
	if m.err != nil {
		eventlog.Errorf(eventlog.CatAWS, "Instance %s failed for %s: %v", m.action, m.instanceID, m.err)
		text := fmt.Sprintf("%s failed: %s. The list shows the current state of %d instance(s).",
			actionName(m.action), aws.ErrorCode(m.err), len(m.ids))
		return tea.Batch(e.refreshKeys(m.ids), func() tea.Msg { return msg.ToastError(text) })
	}
	eventlog.Infof(eventlog.CatAWS, "Instance %s completed: %s", m.action, m.instanceID)
	toast := m.Outcome()
	delayedRefresh := tea.Tick(ec2RefreshDelay, func(time.Time) tea.Msg {
		return ec2DelayedRefreshMsg{}
	})
	return tea.Batch(e.busy("Waiting for state change..."), delayedRefresh, func() tea.Msg { return toast })
}

func ec2Columns(tier ui.WidthTier) []ui.Column {
	if tier == ui.TierNarrow {
		return []ui.Column{
			{Title: "Instance ID", Width: 21, Weight: 1, MaxWidth: 35},
			{Title: "Name", Width: 24, Weight: 2, MaxWidth: 60},
			{Title: "State", Width: 16, Style: ui.StateColor},
			{Title: "Type", Width: 14},
		}
	}
	if tier == ui.TierMedium {
		return []ui.Column{
			{Title: "Instance ID", Width: 19, Weight: 1, MaxWidth: 35},
			{Title: "Name", Width: 15, Weight: 2, MaxWidth: 60},
			{Title: "State", Width: 10, Style: ui.StateColor},
			{Title: "Type", Width: 10},
			{Title: "Private IP", Width: 15},
		}
	}
	return []ui.Column{
		{Title: "Instance ID", Width: 21, Weight: 1, MaxWidth: 35},
		{Title: "Name", Width: 24, Weight: 2, MaxWidth: 60},
		{Title: "State", Width: 16, Style: ui.StateColor},
		{Title: "Type", Width: 14},
		{Title: "Private IP", Width: 16},
		{Title: "Public IP", Width: 16},
		{Title: "AZ", Width: 14},
		{Title: "Launched", Width: 12, Weight: 1, MaxWidth: 25},
	}
}

// ec2Row builds the row of one instance.
func ec2Row(inst aws.Instance, tier ui.WidthTier) ui.Row {
	var cells table.Row
	switch tier {
	case ui.TierNarrow:
		cells = table.Row{inst.ID, inst.Name, inst.State, inst.Type}
	case ui.TierMedium:
		cells = table.Row{inst.ID, inst.Name, inst.State, inst.Type, inst.PrivateIP}
	default:
		launched := ""
		if !inst.LaunchTime.IsZero() {
			launched = inst.LaunchTime.Format("2006-01-02")
		}
		cells = table.Row{
			inst.ID, inst.Name, inst.State, inst.Type,
			inst.PrivateIP, inst.PublicIP, inst.AvailabilityZone, launched,
		}
	}
	return ui.Row{Key: inst.ID, Cells: cells, Sort: cells}
}

func buildEC2InfoContentWithLinks(d *aws.InstanceDetail) (string, []msg.TabLink) {
	fields := []infoField{
		{K: "Instance ID", V: d.InstanceID},
		{K: "Name", V: d.Name},
		{K: "State", V: d.State},
		{K: "Type", V: d.InstanceType},
		{K: "Platform", V: d.Platform},
		{K: "Architecture", V: d.Architecture},
		{K: "Private IP", V: d.PrivateIP},
		{K: "Public IP", V: d.PublicIP},
		{K: "Private DNS", V: d.PrivateDNS},
		{K: "Public DNS", V: d.PublicDNS},
		{K: "VPC", V: d.VpcID, ViewID: "vpc_list", Params: map[string]string{"focus": d.VpcID}},
		{K: "Subnet", V: d.SubnetID, ViewID: "subnet_list", Params: map[string]string{"focus": d.SubnetID}},
		{K: "AZ", V: d.AvailabilityZone},
		{K: "Key Name", V: d.KeyName},
		{K: "AMI", V: d.AMI, ViewID: "ami_list", Params: map[string]string{"focus": d.AMI}},
		{K: "IAM Role", V: d.IAMRole},
		{K: "Launch Time", V: d.LaunchTime},
		{K: "Root Device", V: rootDevice(d)},
	}
	return buildInfo(fields)
}

// rootDevice returns "type (name)", or "" when the instance has neither.
func rootDevice(d *aws.InstanceDetail) string {
	if d.RootDeviceType == "" && d.RootDeviceName == "" {
		return ""
	}
	return d.RootDeviceType + " (" + d.RootDeviceName + ")"
}

func buildSGContentWithLinks(sgs []aws.SecurityGroupRef) (string, []msg.TabLink) {
	var b strings.Builder
	var links []msg.TabLink
	for i, sg := range sgs {
		fmt.Fprintf(&b, "%-22s %s\n", sg.ID, sg.Name)
		links = append(links, msg.TabLink{
			Line:   i,
			ViewID: "sg_list",
			Params: map[string]string{"focus": sg.ID},
		})
	}
	return b.String(), links
}
