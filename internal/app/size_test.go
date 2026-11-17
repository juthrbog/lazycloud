package app

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/views"
)

// rowView records the sizes that it gets and draws one numbered line for
// each row, so a test can see whether the last row is on the screen.
type rowView struct {
	*views.EventLog
	footer string
	sizes  []tea.WindowSizeMsg
}

func newRowView(footer string) *rowView {
	return &rowView{EventLog: views.NewEventLog(), footer: footer}
}

func (v *rowView) Footer() string { return v.footer }

func (v *rowView) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	if s, ok := m.(tea.WindowSizeMsg); ok {
		v.sizes = append(v.sizes, s)
	}
	return v, nil
}

func (v *rowView) View() tea.View {
	if len(v.sizes) == 0 {
		return tea.NewView("")
	}
	h := v.sizes[len(v.sizes)-1].Height
	lines := make([]string, h)
	for i := range lines {
		lines[i] = fmt.Sprintf("row-%02d", i+1)
	}
	return tea.NewView(strings.Join(lines, "\n"))
}

// L1: pushView gave a new view the window height minus 5. It left out the
// footer and the operations line, so the view drew more rows than fit and
// lost its last rows until the next resize. A view that opens now gets the
// size that a resize gives it.
func TestPushedViewGetsTheResizeSize(t *testing.T) {
	for _, tc := range []struct {
		name   string
		width  int
		footer string
		setup  func(t *testing.T, m Model) Model
		wantH  int
	}{
		{"plain", 140, "", nil, 40 - 5},
		{"footer", 140, "3 items", nil, 40 - 7},
		{"operation running", 140, "3 items", startTestOp, 40 - 8},
		{"panel open", panelWide, "3 items", func(t *testing.T, m Model) Model {
			m.openPanel("detail", "{}", "json")
			require.True(t, m.panels.IsOpen())
			return m
		}, 40 - 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(tc.width, 40)
			if tc.setup != nil {
				m = tc.setup(t, m)
			}
			v := newRowView(tc.footer)
			m.pushView(v)
			require.Len(t, v.sizes, 1)
			pushed := v.sizes[0]
			assert.Equal(t, tc.wantH, pushed.Height)
			wantW := tc.width - 2 // the borders of the main box
			if m.panels.IsOpen() {
				wantW = tc.width - m.panelWidth() - 3 // panel, gap, borders
				assert.Less(t, wantW, tc.width-2)
			}
			assert.Equal(t, wantW, pushed.Width)

			m.resizeViews()
			require.Len(t, v.sizes, 2)
			assert.Equal(t, v.sizes[1], pushed, "a resize changes nothing")

			last := fmt.Sprintf("row-%02d", pushed.Height)
			assert.Contains(t, rendered(m), last, "the last row is on the screen")
			m.ops.CancelAll()
		})
	}
}
