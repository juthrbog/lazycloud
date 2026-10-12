package app

import (
	"bytes"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/stretchr/testify/mock"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
)

// TestRuntime_RoutedMessagesEndToEnd runs the full app in the real Bubble Tea
// runtime. Every view command is tagged (see nav.Tag), so this checks that the
// runtime still executes tagged batches, delivers routed results, handles
// clipboard writes, and quits.
func TestRuntime_RoutedMessagesEndToEnd(t *testing.T) {
	ec2 := new(awstest.MockEC2Service)
	ec2.On("ListInstancesPage", mock.Anything, (*string)(nil)).Return(&aws.InstancePage{
		Instances: []aws.Instance{{ID: "i-runtime1", Name: "web-1", State: "running"}},
	}, nil)

	m := newTestModel(0, 0)
	m.svc.EC2 = ec2
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(140, 40))
	wait := func(want string) {
		t.Helper()
		teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
			return bytes.Contains(b, []byte(want))
		}, teatest.WithDuration(5*time.Second))
	}

	// Init + first page come back as routed messages inside a tagged batch.
	tm.Send(msg.NavigateMsg{ViewID: "ec2_list"})
	wait("i-runtime1")

	// y = tea.SetClipboard (runtime message) + a routed success toast.
	tm.Send(tea.KeyPressMsg{Code: 'y', Text: "y"})
	wait("Copied 1 instance ID(s)")

	// q twice: back to home, then quit.
	tm.Send(tea.KeyPressMsg{Code: 'q', Text: "q"})
	tm.Send(tea.KeyPressMsg{Code: 'q', Text: "q"})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
	ec2.AssertExpectations(t)
}
