package views

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/eventlog"
	msg_pkg "github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

type contentViewerKeyMap struct {
	Esc    key.Binding
	Editor key.Binding
}

var defaultContentViewerKeyMap = contentViewerKeyMap{
	Esc:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Editor: key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "editor")),
}

// ContentViewer is a navigable view that displays syntax-highlighted content.
type ContentViewer struct {
	keys   contentViewerKeyMap
	viewer ui.ContentView
	id     string
	name   string
}

func (c *ContentViewer) ID() string    { return c.id }
func (c *ContentViewer) Title() string { return c.name }
func (c *ContentViewer) Footer() string    { return "" }
func (c *ContentViewer) KeyMap() []ui.HintBinding {
	cvk := ui.DefaultContentViewKeyMap()
	return []ui.HintBinding{
		{Binding: cvk.CursorDown},
		{Binding: cvk.VisualToggle},
		{Binding: cvk.Yank},
		{Binding: c.keys.Editor},
		{Binding: cvk.LineNumbers},
		{Binding: cvk.GotoTop},
	}
}

// NewContentViewer creates a content viewer view.
func NewContentViewer(id, title, content string, format ui.ContentFormat) *ContentViewer {
	return &ContentViewer{
		keys:   defaultContentViewerKeyMap,
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
		switch {
		case key.Matches(msg, c.keys.Esc):
			// If in visual mode, cancel it; otherwise navigate back
			if c.viewer.InVisualMode() {
				c.viewer.CancelVisual()
				return c, nil
			}
			return c, func() tea.Msg { return msg_pkg.NavigateBackMsg{} }
		case key.Matches(msg, c.keys.Editor):
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
