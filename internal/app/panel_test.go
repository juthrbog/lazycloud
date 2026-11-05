package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
)

const (
	panelWide   = 140
	panelNarrow = 100
)

var keyEventLog = tea.KeyPressMsg{Code: 'L', Text: "L"}

// stashedOnEC2 returns a model on the EC2 list with a panel that a narrow
// terminal stashed.
func stashedOnEC2(t *testing.T) Model {
	t.Helper()
	m := panelOnEC2(t)
	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelNarrow, Height: 40})
	require.False(t, m.panelOpen)
	require.Equal(t, "web-server-1", m.stashedPanelTitle)
	return m
}

// panelOnEC2 returns a wide model on the EC2 list with an open panel.
func panelOnEC2(t *testing.T) Model {
	t.Helper()
	m := newTestModel(panelWide, 40)
	m.svc.EC2 = new(awstest.MockEC2Service)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "ec2_list"})
	m.openPanel("web-server-1", "{}", "json")
	return m
}

// M12: a resize gives a stashed panel back to the view that showed it.
func TestWideningRestoresStashedPanel(t *testing.T) {
	m := stashedOnEC2(t)

	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelWide, Height: 40})

	assert.Equal(t, "ec2_list", m.nav.Current().ID())
	assert.True(t, m.panelOpen)
	assert.Equal(t, "web-server-1", m.panelTitle)
	assert.Empty(t, m.stashedPanelTitle)
}

// M12: what closes an open panel also drops a stashed one, so the old panel
// does not open over another view when the terminal gets wide again.
func TestLeavingTheViewDropsStashedPanel(t *testing.T) {
	for name, tc := range map[string]struct {
		leave func(t *testing.T, m Model) Model
		top   string
	}{
		"navigate": {
			leave: func(t *testing.T, m Model) Model {
				m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
				return m
			},
			top: "eventlog",
		},
		"event log key": {
			leave: func(t *testing.T, m Model) Model {
				m, _ = update(t, m, keyEventLog)
				return m
			},
			top: "eventlog",
		},
		"form": {
			leave: func(t *testing.T, m Model) Model {
				m, _ = update(t, m, msg.RequestFormMsg{ID: "test", Title: "Test", Fields: []msg.FormField{{Key: "name", Title: "Name"}}})
				return m
			},
			top: "form:test",
		},
		"navigate back": {
			leave: func(t *testing.T, m Model) Model {
				m, _ = update(t, m, msg.NavigateBackMsg{})
				return m
			},
			top: "home",
		},
		"q": {
			leave: func(t *testing.T, m Model) Model {
				m, _ = update(t, m, tea.KeyPressMsg{Code: 'q', Text: "q"})
				return m
			},
			top: "home",
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := stashedOnEC2(t)

			m = tc.leave(t, m)
			require.Contains(t, m.nav.Current().ID(), tc.top)
			m, _ = update(t, m, tea.WindowSizeMsg{Width: panelWide, Height: 40})

			assert.False(t, m.panelOpen, "the old panel stays closed")
			assert.Empty(t, m.stashedPanelTitle)
		})
	}
}

// M12: the event log closes the panel, as other navigation does. The panel
// does not come back when the user leaves the log.
func TestEventLogKeyClosesPanel(t *testing.T) {
	m := panelOnEC2(t)

	m, _ = update(t, m, keyEventLog)

	assert.Equal(t, "eventlog", m.nav.Current().ID())
	assert.False(t, m.panelOpen)

	m, _ = update(t, m, msg.NavigateBackMsg{})
	assert.Equal(t, "ec2_list", m.nav.Current().ID())
	assert.False(t, m.panelOpen)
}

// M12: back traversal replaces a stashed panel. The stash of the view that the
// user left must not win over the panel that back traversal restores.
func TestCrossNavBackReplacesStashedPanel(t *testing.T) {
	m := panelOnEC2(t)
	m.pushCrossNavHistory()
	m.closePanel()
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
	m.openPanel("newer", "{}", "json")
	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelNarrow, Height: 40})
	require.Equal(t, "newer", m.stashedPanelTitle)

	m, _ = update(t, m, keyBackspace)
	assert.Equal(t, "ec2_list", m.nav.Current().ID())
	assert.False(t, m.panelOpen, "a narrow terminal cannot show a panel")

	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelWide, Height: 40})
	assert.True(t, m.panelOpen)
	assert.Equal(t, "web-server-1", m.panelTitle)
}
