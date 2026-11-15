package app

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
)

// pump delivers the messages of cmd, and then the messages that they
// produce, as the runtime would. It stops after a few rounds, so a chain
// of commands cannot run forever.
func pump(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	pending := collectMsgs(cmd)
	for round := 0; round < 10 && len(pending) > 0; round++ {
		var next []tea.Msg
		for _, in := range pending {
			var c tea.Cmd
			m, c = update(t, m, in)
			next = append(next, collectMsgs(c)...)
		}
		pending = next
	}
	return m
}

// L30: a good load of a list clears the status bar error of its failed load.
func TestStatusBarErrorClearsAfterAGoodLoad(t *testing.T) {
	e := new(awstest.MockEC2Service)
	e.On("ListInstancesPage", mock.Anything, (*string)(nil)).
		Return((*aws.InstancePage)(nil), errors.New("Throttled")).Once()
	e.On("ListInstancesPage", mock.Anything, (*string)(nil)).Return(&aws.InstancePage{
		Instances: []aws.Instance{{ID: "i-abc", State: "running"}},
	}, nil)
	m := newTestModel(140, 40)
	m.svc.EC2 = e
	home := m.nav.Current()
	m, cmd := update(t, m, msg.NavigateMsg{ViewID: "ec2_list"})
	m = pump(t, m, cmd)
	require.Contains(t, m.visibleErr(), "Throttled", "the failed load shows in the status bar")
	list := m.nav.Current()

	// A good load of another view leaves the error alone.
	m, _ = update(t, m, nav.Routed{Origin: home, Msg: msg.LoadedMsg{}})
	assert.Contains(t, m.visibleErr(), "Throttled")

	m, cmd = update(t, m, press("r"))
	m = pump(t, m, cmd)
	assert.Same(t, list, m.nav.Current())
	assert.Empty(t, m.visibleErr(), "the good load clears the error")
	assert.Contains(t, screen(m), "i-abc")
	e.AssertNumberOfCalls(t, "ListInstancesPage", 2)
}

// L31: a list keeps its selection when the user comes back to it from a
// view below it. A list opened again from the home view starts with no
// selection.
func TestSelectionStaysAfterASubView(t *testing.T) {
	s3m := new(awstest.MockS3Service)
	s3m.On("ListBuckets", mock.Anything).Return([]aws.Bucket{{Name: "a"}, {Name: "b"}}, nil)
	s3m.On("ListObjectsPage", mock.Anything, mock.Anything, "", (*string)(nil)).
		Return(&aws.ObjectPage{Objects: []aws.S3Object{{Key: "x.txt"}}}, nil)
	m := newTestModel(140, 40)
	m.svc.S3 = s3m
	m, cmd := update(t, m, msg.NavigateMsg{ViewID: "s3_list"})
	m = pump(t, m, cmd)
	list := m.nav.Current()
	m, _ = update(t, m, press(" "))
	require.Contains(t, list.Footer(), "(1 selected)")

	m, cmd = update(t, m, keyEnter)
	m = pump(t, m, cmd)
	require.Equal(t, "s3_objects:a:", m.nav.Current().ID(), "enter opens the objects of bucket a")
	m, cmd = update(t, m, press("esc"))
	m = pump(t, m, cmd)
	require.Same(t, list, m.nav.Current(), "esc goes back to the bucket list")
	assert.Contains(t, list.Footer(), "(1 selected)", "the selection stays")

	m, cmd = update(t, m, msg.NavigateBackMsg{})
	m = pump(t, m, cmd)
	require.Equal(t, "home", m.nav.Current().ID())
	m, cmd = update(t, m, msg.NavigateMsg{ViewID: "s3_list"})
	m = pump(t, m, cmd)
	assert.NotSame(t, list, m.nav.Current(), "the home view opens a new list")
	assert.NotContains(t, m.nav.Current().Footer(), "selected", "the new list has no selection")
}
