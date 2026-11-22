package views

import (
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// Tests of the ECS task definition lists and detail (docs/design/ecs.md,
// PR 5).

const (
	webRev7 = "arn:aws:ecs:us-east-1:1:task-definition/web:7"
	webRev6 = "arn:aws:ecs:us-east-1:1:task-definition/web:6"
	webRev2 = "arn:aws:ecs:us-east-1:1:task-definition/web:2"
)

var fullTaskDef = &aws.TaskDefinition{
	ARN: webRev7, Family: "web", Revision: 7, Status: "ACTIVE",
	RegisteredAt: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC), RegisteredBy: "arn:aws:iam::1:user/dev",
	CPU: "256", Memory: "512", NetworkMode: "awsvpc",
	Compatibilities: []string{"EC2", "FARGATE"}, RequiresCompat: []string{"FARGATE"},
	TaskRole: "arn:aws:iam::1:role/web-task", ExecutionRole: "arn:aws:iam::1:role/exec",
	CPUArchitecture: "X86_64", OSFamily: "LINUX", EphemeralGiB: 30, Volumes: []string{"data"},
	Containers: []aws.ContainerDefinition{
		{
			Name: "app", Image: "nginx:alpine", CPU: 128, Memory: 256, MemoryReservation: 128, Essential: true,
			Command: []string{"nginx", "-g", "daemon off;"}, EntryPoint: []string{"/docker-entrypoint.sh"},
			Ports:    []aws.PortMapping{{Name: "http", ContainerPort: 80, HostPort: 8080, Protocol: "tcp"}, {ContainerPort: 9000}, {ContainerPortRange: "8000-8010", Protocol: "udp"}},
			Env:      map[string]string{"DB_HOST": "web.db.internal", "LOG_LEVEL": "debug"},
			EnvFiles: []string{"arn:aws:s3:::cfg/web.env"},
			Secrets:  []aws.SecretRef{{Name: "DB_PASSWORD", From: "arn:aws:secretsmanager:us-east-1:1:secret:db"}},
			Log:      awslogs,
		},
		{Name: "sidecar", Image: "busybox"},
	},
	Tags: map[string]string{"team": "web"},
}

func TestECSTaskDefDetail(t *testing.T) {
	d := ecsTaskDefDetail(fullTaskDef, false)
	assert.Equal(t, "web:7", d.PanelTitle)
	assert.Equal(t, []string{"Info", "Containers", "Environment", "Tags"}, tabTitles(d))
	info := d.Tabs[0].Content
	for _, s := range []string{"web", webRev7, "ACTIVE", "2026-11-", "user/dev", "256", "512", "awsvpc",
		"FARGATE", "EC2, FARGATE", "LINUX X86_64", "30 GiB", "role/web-task", "role/exec", "data"} {
		assert.Contains(t, info, s)
	}
	assert.NotContains(t, info, "Deregistered", "an empty value leaves out its line")

	cs := d.Tabs[1].Content
	for _, s := range []string{"app", "nginx:alpine", "Yes", "128", "256", "/docker-entrypoint.sh", "nginx -g daemon off;",
		"cfg/web.env", "8080 -> 80/tcp (http)", "9000\n", "awslogs", "awslogs-group", "/ecs/web-app",
		"DB_PASSWORD from arn:aws:secretsmanager:us-east-1:1:secret:db", "sidecar", "busybox"} {
		assert.Contains(t, cs, s)
	}
	assert.NotContains(t, cs, "web.db.internal", "the Containers tab shows no environment values")
	assert.NotContains(t, cs, "0 -> 9000", "a port with no host port shows alone")
	assert.Contains(t, cs, "8000-8010/udp", "a port range shows as the range")
	assert.NotContains(t, cs, " 0/udp")

	env := d.Tabs[2].Content
	assert.Contains(t, env, "Values are hidden. Press $ in the list to show them.")
	assert.Contains(t, env, "DB_HOST")
	assert.Contains(t, env, lambdaHidden)
	assert.NotContains(t, env, "web.db.internal")
	assert.Contains(t, env, "sidecar\n  No environment variables")

	// $ shows the values, with the Environment tab first.
	r := ecsTaskDefDetail(fullTaskDef, true)
	assert.Equal(t, []string{"Environment", "Info", "Containers", "Tags"}, tabTitles(r))
	assert.Contains(t, r.Tabs[0].Content, "web.db.internal")
	assert.Contains(t, r.Tabs[0].Content, "debug")
	assert.NotContains(t, r.Tabs[0].Content, "hidden")
	assert.NotContains(t, r.Tabs[0].Content, lambdaHidden)

	bare := ecsTaskDefDetail(&aws.TaskDefinition{ARN: webRev2, Family: "web", Revision: 2}, false)
	assert.Equal(t, []string{"Info", "Containers", "Environment"}, tabTitles(bare), "no tags, no Tags tab")
	assert.Contains(t, bare.Tabs[1].Content, "No containers")
	for _, s := range []string{"Ephemeral", "Platform", "Deregistered", "Volumes", "Task role"} {
		assert.NotContains(t, bare.Tabs[0].Content, s, "an empty value leaves out its line")
	}
	assert.Contains(t, bare.Tabs[0].Content, "Revision")
	assert.Contains(t, bare.Tabs[2].Content, "No containers")
	assert.NotContains(t, bare.Tabs[2].Content, "hidden", "no note without values")
	plainC := ecsContainerDefsText([]aws.ContainerDefinition{{Name: "x"}})
	for _, s := range []string{"CPU", "Memory", "Port", "Log driver", "Secret", "Command"} {
		assert.NotContains(t, plainC, s)
	}
}

func TestECSTaskDefLink(t *testing.T) {
	f := ecsTaskDefLink(webRev7)
	assert.Equal(t, infoField{K: "Task def", V: "web:7", ViewID: "ecs_task_definitions",
		Params: map[string]string{"family": "web", "focus": webRev7}}, f)
	assert.Equal(t, infoField{K: "Task def"}, ecsTaskDefLink(""), "no task definition, no link")
}

// The service and task details link their task definition to its
// revision list, with the cursor on that revision.
func TestECSDetailsLinkTheTaskDefinition(t *testing.T) {
	want := msg.TabLink{ViewID: "ecs_task_definitions", Params: map[string]string{"family": "web", "focus": webRev7}}
	task := testTask
	task.TaskDefinition = webRev7
	links := ecsTaskDetail(task).Tabs[0].Links
	require.Len(t, links, 1)
	want.Line = links[0].Line
	assert.Equal(t, want, links[0])

	plainService := testService
	plainService.CurrentDeployment = "" // no DescribeServiceDeployment call
	v := newServices(t, plainService)
	_, cmd := v.Update(keyPress('d'))
	require.NotNil(t, cmd)
	d, ok := cmd().(listDetailMsg)
	require.True(t, ok)
	require.NoError(t, d.err)
	links = d.panel.Tabs[0].Links
	require.Len(t, links, 1)
	assert.Equal(t, "ecs_task_definitions", links[0].ViewID)
	assert.Equal(t, testService.TaskDefinition, links[0].Params["focus"])
}

func newFamilies(t *testing.T) (*ECSTaskFamilies, *awstest.MockECSService) {
	t.Helper()
	svc := new(awstest.MockECSService)
	svc.Test(t)
	svc.On("ListTaskFamiliesPage", mock.Anything, mock.Anything).Return(&aws.FamilyPage{Families: []string{"web", "worker"}}, nil)
	v := NewECSTaskFamilies(svc)
	v.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	pump(v, v.Init(), 20)
	return v, svc
}

func TestECSTaskFamilies(t *testing.T) {
	v, svc := newFamilies(t)
	assert.Equal(t, "ECS Task Definitions", v.Title())
	assert.Equal(t, "ecs_task_families", v.ID())
	assert.Equal(t, "2/2 families", v.Footer())
	out := ansi.Strip(v.View().Content)
	assert.Contains(t, out, "web")
	assert.Contains(t, out, "worker")
	assert.Equal(t, ecsFamilyPageBudget, v.cfg.PageBudget)
	assert.Equal(t, 10, ecsFamilyPageBudget)
	assert.True(t, v.cfg.MoreKey)
	assert.Equal(t, []string{"enter", "d", "$", "y", "l", "s/S", "/", "r"}, hintKeys(v.KeyMap()))

	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	assert.Equal(t, msg.NavigateMsg{ViewID: "ecs_task_definitions", Params: map[string]string{"family": "web"}}, cmd())

	// d opens the latest revision: DescribeTaskDefinition takes the family.
	svc.On("DescribeTaskDefinition", mock.Anything, "web").Return(fullTaskDef, nil)
	_, cmd = v.Update(keyPress('d'))
	require.NotNil(t, cmd)
	d, ok := cmd().(listDetailMsg)
	require.True(t, ok)
	require.NoError(t, d.err)
	assert.Equal(t, "web:7", d.panel.PanelTitle)
	assert.Equal(t, "Info", d.panel.Tabs[0].Title)

	_, cmd = v.Update(keyPress('$'))
	require.NotNil(t, cmd)
	r, ok := cmd().(msg.TabbedContentMsg)
	require.True(t, ok)
	assert.Equal(t, "Environment", r.Tabs[0].Title)
	assert.Contains(t, r.Tabs[0].Content, "web.db.internal")

	// A failed $ gives a toast.
	svc.On("DescribeTaskDefinition", mock.Anything, "worker").Return(nil, errors.New("AccessDeniedException"))
	v.Update(keyPress('j'))
	_, cmd = v.Update(keyPress('$'))
	ts := toasts(collectMsgs(cmd))
	require.Len(t, ts, 1)
	assert.Contains(t, ts[0].Text, "AccessDeniedException")

	// The filter takes enter and $.
	v.Update(keyPress('k'))
	v.Update(keyPress('/'))
	for _, k := range []tea.KeyPressMsg{keyPress('$'), {Code: tea.KeyEnter}} {
		_, cmd = v.Update(k)
		for _, m := range collectMsgs(cmd) {
			switch m.(type) {
			case msg.TabbedContentMsg, msg.NavigateMsg, msg.ToastMsg:
				t.Errorf("the filter did not take %s: %T", k.String(), m)
			}
		}
	}
}

func TestECSTaskFamilies_ErrorScreenBlocksTheKeys(t *testing.T) {
	svc := new(awstest.MockECSService)
	svc.On("ListTaskFamiliesPage", mock.Anything, mock.Anything).Return(nil, errors.New("boom"))
	v := NewECSTaskFamilies(svc)
	pump(v, v.Init(), 20)
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, keyPress('$')} {
		_, cmd := v.Update(k)
		assert.Nil(t, cmd)
	}
	w := NewECSTaskDefinitions(nil, "web")
	pump(w, w.Init(), 20)
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, keyPress('$')} {
		_, cmd := w.Update(k)
		assert.Nil(t, cmd)
	}
}

func newRevisions(t *testing.T) (*ECSTaskDefinitions, *awstest.MockECSService) {
	t.Helper()
	svc := new(awstest.MockECSService)
	svc.Test(t)
	svc.On("ListTaskDefinitionsPage", mock.Anything, "web", mock.Anything).Return(&aws.TaskDefinitionPage{Revisions: []aws.TaskDefinitionRef{
		{ARN: webRev7, Family: "web", Revision: 7}, {ARN: webRev6, Family: "web", Revision: 6},
	}}, nil)
	v := NewECSTaskDefinitions(svc, "web")
	v.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	pump(v, v.Init(), 20)
	return v, svc
}

func TestECSTaskDefinitions(t *testing.T) {
	v, svc := newRevisions(t)
	assert.Equal(t, "web revisions", v.Title())
	assert.Equal(t, "ecs_task_definitions:web", v.ID())
	assert.Equal(t, "2/2 revisions", v.Footer())
	out := ansi.Strip(v.View().Content)
	assert.Contains(t, out, webRev7)
	assert.Contains(t, out, webRev6)
	assert.Equal(t, ecsRevisionPageBudget, v.cfg.PageBudget)
	assert.Equal(t, 4, ecsRevisionPageBudget)
	assert.Equal(t, []string{"enter", "d", "$", "y", "l", "s/S", "/", "r"}, hintKeys(v.KeyMap()))

	row := ecsRevisionRow(aws.TaskDefinitionRef{ARN: webRev7, Revision: 7}, ui.TierMedium)
	assert.Equal(t, []string{"7", webRev7}, []string(row.Cells))
	assert.Equal(t, ui.SortKeyInt(7), row.Sort[0], "the revision sorts as a number")
	assert.Equal(t, webRev7, row.Sort[1])

	svc.On("DescribeTaskDefinition", mock.Anything, webRev7).Return(fullTaskDef, nil)
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, keyPress('d')} {
		_, cmd := v.Update(k)
		require.NotNil(t, cmd)
		d, ok := cmd().(listDetailMsg)
		require.True(t, ok)
		require.NoError(t, d.err)
		assert.Equal(t, "web:7", d.panel.PanelTitle)
	}
	_, cmd := v.Update(keyPress('$'))
	require.NotNil(t, cmd)
	r, ok := cmd().(msg.TabbedContentMsg)
	require.True(t, ok)
	assert.Contains(t, r.Tabs[0].Content, "web.db.internal")

	_, cmd = v.Update(keyPress('y'))
	ts := toasts(collectMsgs(cmd))
	require.NotEmpty(t, ts)
	assert.Equal(t, "Copied: "+webRev7, ts[0].Text)

	v.Update(keyPress('/'))
	_, cmd = v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	for _, m := range collectMsgs(cmd) {
		_, isDetail := m.(listDetailMsg)
		assert.False(t, isDetail, "enter closes the filter")
	}
}

// A link moves the cursor to its revision and opens its detail.
func TestECSTaskDefinitions_FocusOnALoadedRevision(t *testing.T) {
	v, svc := newRevisions(t)
	svc.On("DescribeTaskDefinition", mock.Anything, webRev6).Return(&aws.TaskDefinition{ARN: webRev6, Family: "web", Revision: 6}, nil)
	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: webRev6})
	require.NotNil(t, cmd)
	d, ok := cmd().(listDetailMsg)
	require.True(t, ok)
	assert.Equal(t, "web:6", d.panel.PanelTitle)
	assert.Equal(t, webRev6, v.table.CursorKey())
}

// H13: a link to a revision that is not in the list, because it is
// inactive or past the page budget, still opens its detail. A failed call
// gives a toast.
func TestECSTaskDefinitions_FocusOnAMissingRevision(t *testing.T) {
	v, svc := newRevisions(t)
	svc.On("DescribeTaskDefinition", mock.Anything, webRev2).Return(&aws.TaskDefinition{ARN: webRev2, Family: "web", Revision: 2, Status: "INACTIVE"}, nil)
	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: webRev2})
	require.NotNil(t, cmd)
	d, ok := cmd().(listDetailMsg)
	require.True(t, ok)
	require.NoError(t, d.err)
	assert.Equal(t, "web:2", d.panel.PanelTitle)
	assert.Contains(t, d.panel.Tabs[0].Content, "INACTIVE")
	assert.Equal(t, webRev7, v.table.CursorKey(), "the cursor stays")

	gone := "arn:aws:ecs:us-east-1:1:task-definition/web:1"
	svc.On("DescribeTaskDefinition", mock.Anything, gone).Return(nil, errors.New("ClientException: Unable to describe task definition."))
	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: gone})
	ts := toasts(pump(v, cmd, 5))
	require.Len(t, ts, 1)
	assert.Contains(t, ts[0].Text, "Unable to describe task definition")
}
