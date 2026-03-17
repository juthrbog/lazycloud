package views

import (
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/eventlog"
	msg_pkg "github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// ContentViewer is a navigable view that displays syntax-highlighted content.
type ContentViewer struct {
	viewer ui.ContentView
	id     string
	name   string
}

func (c *ContentViewer) ID() string    { return c.id }
func (c *ContentViewer) Title() string { return c.name }

// NewContentViewer creates a content viewer view.
func NewContentViewer(id, title, content string, format ui.ContentFormat) *ContentViewer {
	return &ContentViewer{
		viewer: ui.NewContentView(title, content, format),
		id:     id,
		name:   title,
	}
}

func (c *ContentViewer) Init() tea.Cmd {
	return nil
}

func (c *ContentViewer) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		c.viewer.SetSize(msg.Width, msg.Height)
		return c, nil

	case ui.EditorFinishedMsg:
		return c, nil

	case ui.YankedMsg:
		eventlog.Infof(eventlog.CatUI, "Yanked %d line(s) to clipboard", msg.Lines)
		return c, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc":
			// If in visual mode, cancel it; otherwise navigate back
			if c.viewer.InVisualMode() {
				c.viewer.CancelVisual()
				return c, nil
			}
			return c, func() tea.Msg { return msg_pkg.NavigateBackMsg{} }
		case "e":
			eventlog.Info(eventlog.CatUI, "Opening content in $EDITOR")
			return c, c.viewer.OpenInEditorCmd()
		}
	}

	var cmd tea.Cmd
	c.viewer, cmd = c.viewer.Update(msg)
	return c, cmd
}

func (c *ContentViewer) View() tea.View {
	return tea.NewView(c.viewer.View())
}
