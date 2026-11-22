package views

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
)

// Jump PR 2 (docs/design/jump.md, D5 and H4): a focus on a row that a list
// with a page budget did not load reads that one resource and opens its
// detail. The row is not added.

func TestLambdaVersions_FocusMissingReadsTheVersion(t *testing.T) {
	f := &childFake{versions: [][]aws.Function{{{Name: "orders-api", Version: "$LATEST"}}}}
	v := load(t, NewLambdaVersions(f, "orders-api", "/aws/lambda/orders-api"), 5)
	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "3"})
	panel := detailPanel(t, v, cmd)
	assert.Equal(t, "orders-api:3", panel.PanelTitle)
	assert.Equal(t, []string{"orders-api:3"}, f.gets)
	assert.Len(t, v.items, 1, "no row is added")
}

func TestLogGroups_FocusMissingReadsTheGroup(t *testing.T) {
	f := &fakeLogs{groups: map[string]aws.LogGroup{testGroupOrders.Name: testGroupOrders}}
	v := testLogGroups(t, f, testGroupWeb)
	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: testGroupOrders.Name})
	panel := detailPanel(t, v, cmd)
	assert.Equal(t, "/aws/lambda/orders", panel.PanelTitle)
	assert.Contains(t, panel.Tabs[0].Content, "14 days", "the detail uses the group that GetLogGroup read")
	assert.Equal(t, testGroupOrders.ARN, f.tagARN)
	assert.Len(t, v.items, 1, "no row is added")

	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: "/nope"})
	ts := toasts(pump(v, cmd, 3))
	require.Len(t, ts, 1)
	assert.Contains(t, ts[0].Text, "Details failed:")
	assert.Contains(t, ts[0].Text, "/nope")
}

func TestECSServices_FocusMissingDescribesTheService(t *testing.T) {
	v := newServices(t)
	svc := v.ecs.(*awstest.MockECSService)
	svc.On("DescribeService", mock.Anything, testClusterARN, testServiceARN).Return(&testService, nil).Once()
	svc.On("DescribeServiceDeployment", mock.Anything, testService.CurrentDeployment).Return(testDeployment, nil)
	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: testServiceARN})
	panel := detailPanel(t, v, cmd)
	assert.Equal(t, "web", panel.PanelTitle)
	assert.Empty(t, v.items, "no row is added")

	svc.On("DescribeService", mock.Anything, testClusterARN, "gone").
		Return(nil, errors.New("service not found: gone")).Once()
	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: "gone"})
	assert.Equal(t, []msg.ToastMsg{msg.ToastError("Details failed: service not found: gone")}, toasts(pump(v, cmd, 3)))
}

func TestECSTasks_FocusMissingDescribesTheTask(t *testing.T) {
	v, svc := newTasks(t)
	svc.On("DescribeTask", mock.Anything, testClusterARN, stoppedTask.ARN).Return(&stoppedTask, nil).Once()
	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: stoppedTask.ARN})
	panel := detailPanel(t, v, cmd)
	assert.Contains(t, panel.Tabs[0].Content, "EssentialContainerExited", "a stopped task opens from the running list")
	assert.Len(t, v.items, 1, "no row is added")

	svc.On("DescribeTask", mock.Anything, testClusterARN, "gone").
		Return(nil, errors.New("task not found: gone")).Once()
	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: "gone"})
	assert.Equal(t, []msg.ToastMsg{msg.ToastError("Details failed: task not found: gone")}, toasts(pump(v, cmd, 3)))
}

// With no client, the read fails with errNoClient and no call is made.
func TestFocusMissingWithoutAClient(t *testing.T) {
	tasks := NewECSTasks(nil, nil, testClusterARN, "web")
	assert.Equal(t, listDetailMsg{err: errNoClient}, tasks.cfg.FocusMissing("x")())
	services := NewECSServices(nil, testClusterARN)
	assert.Equal(t, listDetailMsg{err: errNoClient}, services.cfg.FocusMissing("x")())
	groups := NewLogGroups(nil)
	assert.Equal(t, listDetailMsg{err: errNoClient}, groups.cfg.FocusMissing("x")())
}
