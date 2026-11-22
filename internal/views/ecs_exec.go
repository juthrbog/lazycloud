package views

import (
	"slices"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// ECS Exec on o: a shell in a container through the AWS CLI. See
// docs/design/ecs.md, "Exec" (decision D6).

// ecsExecKey opens a shell. A session can change the container, so the
// app blocks it in ReadOnly mode, as the SSM session of EC2.
var ecsExecKey = ui.HintBinding{
	Binding: key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "exec (/bin/sh)")),
	Mode:    ui.ModeReadWrite,
}

// The checks for the AWS CLI and the plugin, and the command of the
// session, so tests can set them.
var (
	ecsCLIAvailable    = aws.AWSCLIAvailable
	ecsPluginAvailable = aws.SSMPluginAvailable
	ecsExecCommand     = (*aws.Client).ECSExecCmd
)

// ecsExecAgent is the managed agent that ECS Exec needs in the container.
const ecsExecAgent = "ExecuteCommandAgent"

// ecsExecTag marks the container picker of o on a task with more than one
// container.
type ecsExecTag struct {
	cluster string
	task    aws.Task
}

// PromptTag marks ecsExecTag as a prompt tag.
func (*ecsExecTag) PromptTag() {}

// ecsExecFinishedMsg comes when the session ends.
type ecsExecFinishedMsg struct {
	label string
	err   error
}

// ecsTaskExecProblem returns why exec cannot start on the task, or "".
func ecsTaskExecProblem(client *aws.Client, t aws.Task) string {
	switch {
	case client == nil:
		return "AWS client not available — check the profile"
	case t.LastStatus != "RUNNING":
		status := t.LastStatus
		if status == "" {
			status = "not running"
		}
		return "The task is " + status + ". Exec needs a running task."
	case !t.ExecEnabled:
		return "Exec is off for this task. Turn it on for the service, then start new tasks."
	case !ecsCLIAvailable():
		return "aws CLI not found — install it first"
	case !ecsPluginAvailable():
		return "session-manager-plugin not found — install it first"
	}
	return ""
}

// ecsContainerExecProblem returns why exec cannot start in the container,
// or "". The agent check runs only when the task reports agents.
func ecsContainerExecProblem(c aws.Container) string {
	if c.LastStatus != "" && c.LastStatus != "RUNNING" {
		return "The container " + c.Name + " is " + c.LastStatus + ". Exec needs a running container."
	}
	if len(c.Agents) == 0 {
		return ""
	}
	i := slices.IndexFunc(c.Agents, func(a aws.ManagedAgent) bool { return a.Name == ecsExecAgent })
	if i < 0 {
		return "The container " + c.Name + " has no exec agent."
	}
	a := c.Agents[i]
	if a.Status == "RUNNING" {
		return ""
	}
	text := "The exec agent of " + c.Name + " is " + a.Status
	if a.Status == "" {
		text = "The exec agent of " + c.Name + " is not running"
	}
	if a.Reason != "" {
		text += ": " + a.Reason
	}
	return text + "."
}

// ecsExecTask opens a shell in the task. A task with more than one
// container asks for the container first.
func ecsExecTask(client *aws.Client, cluster string, t aws.Task) tea.Cmd {
	if p := ecsTaskExecProblem(client, t); p != "" {
		return toastCmd(msg.ToastError(p))
	}
	switch len(t.Containers) {
	case 0:
		return toastCmd(msg.ToastError("The task has no containers."))
	case 1:
		return ecsExecContainer(client, cluster, t, t.Containers[0])
	}
	names := make([]string, 0, len(t.Containers))
	for _, c := range t.Containers {
		names = append(names, c.Name)
	}
	req := msg.RequestActionPickerMsg{Title: "Exec into container", Options: names, Tag: &ecsExecTag{cluster: cluster, task: t}}
	return func() tea.Msg { return req }
}

// onExecPicked opens a shell in the container that the picker chose. A
// cancel has no value, so it matches no container.
func onExecPicked(client *aws.Client, tag *ecsExecTag, r msg.PickerResultMsg) tea.Cmd {
	i := slices.IndexFunc(tag.task.Containers, func(c aws.Container) bool { return c.Name == r.Value })
	if i < 0 {
		return nil
	}
	return ecsExecContainer(client, tag.cluster, tag.task, tag.task.Containers[i])
}

// ecsExecContainer checks the task and the container, and opens the shell.
func ecsExecContainer(client *aws.Client, cluster string, t aws.Task, c aws.Container) tea.Cmd {
	if p := ecsTaskExecProblem(client, t); p != "" {
		return toastCmd(msg.ToastError(p))
	}
	if p := ecsContainerExecProblem(c); p != "" {
		return toastCmd(msg.ToastError(p))
	}
	label := c.Name + " (" + t.ID + ")"
	eventlog.Infof(eventlog.CatAWS, "Starting ECS Exec: %s", label)
	return tea.Exec(ecsExecCommand(client, cluster, t.ARN, c.Name, label), func(err error) tea.Msg {
		return ecsExecFinishedMsg{label: label, err: err}
	})
}

// onExecFinished reports the end of a session. A Ctrl+C that cancels the
// CLI before the session starts is not a failure. In the session, Ctrl+C
// goes to the shell in the container. After a failure, the CLI error
// stayed on the screen until Enter, so the toast shows only the exit code.
func onExecFinished(m ecsExecFinishedMsg) tea.Cmd {
	if m.err != nil && !aws.Cancelled(m.err) {
		eventlog.Errorf(eventlog.CatAWS, "ECS Exec failed for %s: %v", m.label, m.err)
		return toastCmd(msg.ToastError("Exec session failed: " + aws.ErrorText(m.err)))
	}
	eventlog.Infof(eventlog.CatAWS, "ECS Exec ended: %s", m.label)
	return toastCmd(msg.ToastInfo("Exec session ended: " + m.label))
}
