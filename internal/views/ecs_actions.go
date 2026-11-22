package views

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The ECS write actions: f force deploy and c desired count on a service,
// X stop tasks. See docs/design/ecs.md, "Write actions" (decision D8).
// Each one needs ReadWrite mode: the app blocks the key in ReadOnly, the
// view checks the mode again at the reply, and GuardECS refuses the call.

// ecsStopReason is the reason of every task that X stops. The confirm
// prompt has no input field for a reason of its own.
const ecsStopReason = "Stopped by lazycloud"

// ecsDesiredCountForm is the prefix of the ID of the desired count form.
// The service ARN follows it, so the reply needs no state in the view.
const ecsDesiredCountForm = "ecs_desired_count:"

// ecsActionKeys are the keys of the write actions.
var ecsActionKeys = struct {
	Force ui.HintBinding
	Count ui.HintBinding
	Stop  ui.HintBinding
}{
	Force: ui.HintBinding{Binding: key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "force deploy")), Mode: ui.ModeReadWrite},
	Count: ui.HintBinding{Binding: key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "desired count")), Mode: ui.ModeReadWrite},
	Stop:  ui.HintBinding{Binding: key.NewBinding(key.WithKeys("X"), key.WithHelp("X", "stop")), Mode: ui.ModeReadWrite},
}

// ecsForceTag marks the y/n prompt of f.
type ecsForceTag struct{ arn, name string }

// PromptTag marks ecsForceTag as a prompt tag.
func (*ecsForceTag) PromptTag() {}

// ecsStopTag marks the typed prompt of X. It holds the task ARNs, and the
// task IDs for the result text.
type ecsStopTag struct {
	arns  []string
	names map[string]string
}

// PromptTag marks ecsStopTag as a prompt tag.
func (*ecsStopTag) PromptTag() {}

// ecsActionDoneMsg is the result of a write action. The text names the
// service or the tasks, so the app can show it after the user left the
// view (msg.Outcome).
type ecsActionDoneMsg struct {
	text string
	ok   bool
}

// Outcome implements msg.Outcome.
func (m ecsActionDoneMsg) Outcome() msg.ToastMsg {
	if m.ok {
		return msg.ToastSuccess(m.text)
	}
	return msg.ToastError(m.text)
}

// ecsFixedController reports whether a service has a deployment controller
// that rejects a forced deployment from UpdateService (H9).
func ecsFixedController(s aws.ClusterService) bool {
	return s.Controller == "CODE_DEPLOY" || s.Controller == "EXTERNAL"
}

// ─── Services ───────────────────────────────────────────────────────────────

// forceDeploy asks to force a new deployment of the cursor service.
func (v *ECSServices) forceDeploy(s aws.ClusterService) tea.Cmd {
	switch {
	case s.Err != "":
		return toastCmd(msg.ToastError("AWS could not describe " + s.Name + ". Press r to load it again."))
	case ecsFixedController(s):
		return toastCmd(msg.ToastError(fmt.Sprintf("%s uses the %s deployment controller, so it does not take a forced deployment. Deploy it with %s.",
			s.Name, s.Controller, s.Controller)))
	}
	req := msg.RequestConfirmMsg{
		Message: fmt.Sprintf("Force a new deployment of %s?\nEvery task of the service is replaced with a new one.", ui.Sanitize(s.Name)),
		Quick:   true,
		Tag:     &ecsForceTag{arn: s.ARN, name: s.Name},
	}
	return func() tea.Msg { return req }
}

// onForceConfirm forces the deployment of the tag of m when m is a yes.
func (v *ECSServices) onForceConfirm(m msg.ConfirmResultMsg) tea.Cmd {
	t, ok := m.Tag.(*ecsForceTag)
	if !ok || t == nil || !m.Confirmed {
		return nil
	}
	if ui.IsReadOnly() {
		return refuseReadOnly()
	}
	svc, ctx, cluster := v.ecs, v.viewContext(), v.cluster
	return tea.Batch(v.startTask("Deploying "+t.name+"..."), func() tea.Msg {
		ctx, cancel := detached(ctx)
		defer cancel()
		eventlog.Infof(eventlog.CatAWS, "Forcing a new deployment of %s", t.arn)
		if err := ecsCall(svc, func(s aws.ECSService) error { return s.ForceDeployment(ctx, cluster, t.arn) }); err != nil {
			eventlog.Errorf(eventlog.CatAWS, "Force deploy of %s failed: %v", t.arn, err)
			return ecsActionDoneMsg{text: "Force deploy of " + t.name + " failed: " + aws.ErrorText(err)} //nolint:contextcheck // ErrorText reads only the local AWS config files.
		}
		return ecsActionDoneMsg{text: "Started a new deployment of " + t.name, ok: true}
	})
}

// desiredCount opens the form for the desired count of the cursor service.
func (v *ECSServices) desiredCount(s aws.ClusterService) tea.Cmd {
	if s.Err != "" {
		return toastCmd(msg.ToastError("AWS could not describe " + s.Name + ". Press r to load it again."))
	}
	desc := fmt.Sprintf("Running %d, desired %d. A whole number from 0 up.", s.Running, s.Desired)
	if s.Managed {
		desc += " Express Mode manages this service, and it can change the count."
	}
	req := msg.RequestFormMsg{
		ID:    ecsDesiredCountForm + s.ARN,
		Title: "Desired count",
		Fields: []msg.FormField{{
			Key: "count", Type: "input", Title: "Desired count of " + s.Name, Description: desc,
			Default: strconv.Itoa(int(s.Desired)), Required: true, Validate: validateDesiredCount,
		}},
	}
	return func() tea.Msg { return req }
}

// validateDesiredCount accepts a whole number from 0 up.
func validateDesiredCount(s string) error {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
	switch {
	case err != nil && errors.Is(err, strconv.ErrRange):
		return fmt.Errorf("must be at most %d", math.MaxInt32)
	case err != nil:
		return errors.New("must be a whole number")
	case n < 0:
		return errors.New("must be 0 or more")
	}
	return nil
}

// onCountForm sets the desired count of the form reply m.
func (v *ECSServices) onCountForm(m msg.FormResultMsg) tea.Cmd {
	arn, ok := strings.CutPrefix(m.ID, ecsDesiredCountForm)
	if !ok || arn == "" || m.Aborted {
		return nil
	}
	if ui.IsReadOnly() {
		return refuseReadOnly()
	}
	if validateDesiredCount(m.Values["count"]) != nil {
		return nil // the form validated it
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(m.Values["count"]), 10, 32)
	count := int32(n)
	name := aws.ARNName(arn) // a service ARN ends with the service name
	svc, ctx, cluster := v.ecs, v.viewContext(), v.cluster
	return tea.Batch(v.startTask(fmt.Sprintf("Setting the desired count of %s...", name)), func() tea.Msg {
		ctx, cancel := detached(ctx)
		defer cancel()
		eventlog.Infof(eventlog.CatAWS, "Setting the desired count of %s to %d", arn, count)
		if err := ecsCall(svc, func(s aws.ECSService) error { return s.SetDesiredCount(ctx, cluster, arn, count) }); err != nil {
			eventlog.Errorf(eventlog.CatAWS, "Desired count of %s failed: %v", arn, err)
			return ecsActionDoneMsg{text: "Desired count of " + name + " failed: " + aws.ErrorText(err)} //nolint:contextcheck // ErrorText reads only the local AWS config files.
		}
		return ecsActionDoneMsg{text: fmt.Sprintf("Set the desired count of %s to %d", name, count), ok: true}
	})
}

// ─── Tasks ──────────────────────────────────────────────────────────────────

// stopTasks asks to stop the selected tasks, or the cursor task, with a
// typed phrase.
func (v *ECSTasks) stopTasks() tea.Cmd {
	if v.stopped {
		return toastCmd(msg.ToastError("These tasks are stopped already. Press x for the running tasks."))
	}
	arns := v.targets()
	if len(arns) == 0 {
		return nil
	}
	names := make(map[string]string, len(arns))
	list := make([]string, 0, len(arns))
	for _, arn := range arns {
		name := aws.ARNName(arn) // the task ID
		names[arn] = name
		list = append(list, name)
	}
	phrase := fmt.Sprintf("stop %d tasks", len(arns))
	if len(arns) == 1 {
		phrase = "stop 1 task"
	}
	req := msg.RequestConfirmMsg{
		Message: confirmMessage("Stop", len(arns), "task(s)", list,
			"The reason is \""+ecsStopReason+"\". A service starts new tasks in their place."),
		Phrase: phrase,
		Tag:    &ecsStopTag{arns: arns, names: names},
	}
	return func() tea.Msg { return req }
}

// onStopConfirm stops the tasks of the tag of m when m is a yes.
func (v *ECSTasks) onStopConfirm(m msg.ConfirmResultMsg) tea.Cmd {
	t, ok := m.Tag.(*ecsStopTag)
	if !ok || t == nil || !m.Confirmed || len(t.arns) == 0 {
		return nil
	}
	if ui.IsReadOnly() {
		return refuseReadOnly()
	}
	svc, ctx, cluster := v.ecs, v.viewContext(), v.cluster
	return tea.Batch(v.startTask(fmt.Sprintf("Stopping %d task(s)...", len(t.arns))), func() tea.Msg {
		eventlog.Infof(eventlog.CatAWS, "Stopping %d tasks", len(t.arns))
		_, failed := runEach(ctx, t.arns, t.names, func(ctx context.Context, arn string) error {
			return ecsCall(svc, func(s aws.ECSService) error { return s.StopTask(ctx, cluster, arn, ecsStopReason) })
		})
		for _, f := range failed {
			eventlog.Errorf(eventlog.CatAWS, "Stop of task %s failed: %v", f.name, f.err)
		}
		text, ok := resultText("Stopped", len(t.arns), "task(s)", failed)
		return ecsActionDoneMsg{text: text, ok: ok}
	})
}

// ─── Shared ─────────────────────────────────────────────────────────────────

// ecsCall runs fn on svc, or gives errNoClient without a service.
func ecsCall(svc aws.ECSService, fn func(aws.ECSService) error) error {
	if svc == nil {
		return errNoClient
	}
	return fn(svc)
}

// ecsActionResult shows the result of an action and loads the list again,
// so the rows show what AWS has now.
func ecsActionResult(m ecsActionDoneMsg, endTask func(), reload func(string) tea.Cmd, label string) tea.Cmd {
	endTask()
	return tea.Batch(toastCmd(m.Outcome()), reload(label))
}
