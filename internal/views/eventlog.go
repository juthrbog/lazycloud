package views

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/juthrbog/lazycloud/internal/eventlog"
	msg_pkg "github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// severity filter levels (indexes into levelFilters)
var levelFilters = []struct {
	Label string
	Index int
}{
	{"ALL", 0},
	{"INF+", 1},
	{"WRN+", 2},
	{"ERR", 3},
}

type eventLogKeyMap struct {
	Esc        key.Binding
	Filter     key.Binding
	AutoScroll key.Binding
	Refresh    key.Binding
	Level1     key.Binding
	Level2     key.Binding
	Level3     key.Binding
	Level4     key.Binding
	CycleUp    key.Binding
	CycleDown  key.Binding
}

var defaultEventLogKeyMap = eventLogKeyMap{
	Esc:        key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Filter:     key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
	AutoScroll: key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("ctrl+s", "auto-scroll")),
	Refresh:    key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
	Level1:     key.NewBinding(key.WithKeys("1"), key.WithHelp("1-4", "severity")),
	Level2:     key.NewBinding(key.WithKeys("2"), key.WithHelp("2", "inf+")),
	Level3:     key.NewBinding(key.WithKeys("3"), key.WithHelp("3", "wrn+")),
	Level4:     key.NewBinding(key.WithKeys("4"), key.WithHelp("4", "err")),
	CycleUp:    key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "cycle")),
	CycleDown:  key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "cycle back")),
}

// EventLog displays the in-app event log with scrolling and filtering.
type EventLog struct {
	keys         eventLogKeyMap
	viewport     viewport.Model
	filter       ui.Filter
	autoScroll   bool
	levelIdx     int // index into levelFilters, 0 = ALL
	width        int
	height       int
	lastLen      int
}

func (e *EventLog) ID() string    { return "eventlog" }
func (e *EventLog) Title() string { return "Event Log" }
func (e *EventLog) Footer() string    { return "" }
func (e *EventLog) KeyMap() []ui.HintBinding {
	return []ui.HintBinding{
		{Binding: e.keys.Level1},
		{Binding: e.keys.CycleUp},
		{Binding: e.keys.AutoScroll},
		{Binding: e.keys.Filter},
		{Binding: e.keys.Refresh},
	}
}

// NewEventLog creates the event log view.
func NewEventLog() *EventLog {
	vp := viewport.New()
	return &EventLog{
		keys:       defaultEventLogKeyMap,
		viewport:   vp,
		filter:     ui.NewFilter(),
		autoScroll: true,
		levelIdx:   0, // ALL
	}
}

func (e *EventLog) Init() tea.Cmd {
	e.refreshContent()
	return nil
}

func (e *EventLog) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		e.width = msg.Width
		e.height = msg.Height
		e.viewport.SetWidth(msg.Width)
		e.viewport.SetHeight(msg.Height - 3)
		e.filter.SetWidth(msg.Width)
		e.refreshContent()
		return e, nil

	case ui.FilterChangedMsg:
		e.refreshContent()
		return e, nil

	case tea.KeyPressMsg:
		if e.filter.Active() {
			var cmd tea.Cmd
			e.filter, cmd = e.filter.Update(msg)
			return e, cmd
		}

		switch {
		case key.Matches(msg, e.keys.Esc):
			return e, func() tea.Msg { return msg_pkg.NavigateBackMsg{} }
		case key.Matches(msg, e.keys.Filter):
			e.filter.Activate()
			return e, nil
		case key.Matches(msg, e.keys.AutoScroll):
			e.autoScroll = !e.autoScroll
			return e, nil
		case key.Matches(msg, e.keys.Refresh):
			e.refreshContent()
			return e, nil
		case key.Matches(msg, e.keys.Level1):
			e.levelIdx = 0
			e.refreshContent()
			return e, nil
		case key.Matches(msg, e.keys.Level2):
			e.levelIdx = 1
			e.refreshContent()
			return e, nil
		case key.Matches(msg, e.keys.Level3):
			e.levelIdx = 2
			e.refreshContent()
			return e, nil
		case key.Matches(msg, e.keys.Level4):
			e.levelIdx = 3
			e.refreshContent()
			return e, nil
		case key.Matches(msg, e.keys.CycleUp):
			e.levelIdx = (e.levelIdx + 1) % len(levelFilters)
			e.refreshContent()
			return e, nil
		case key.Matches(msg, e.keys.CycleDown):
			e.levelIdx = (e.levelIdx - 1 + len(levelFilters)) % len(levelFilters)
			e.refreshContent()
			return e, nil
		}
	}

	if eventlog.Len() != e.lastLen {
		e.refreshContent()
	}

	var cmd tea.Cmd
	e.viewport, cmd = e.viewport.Update(msg)
	if cmd != nil {
		e.autoScroll = false
	}
	return e, cmd
}

func (e *EventLog) View() tea.View {
	t := ui.ActiveTheme

	// Title
	title := lipgloss.NewStyle().Bold(true).Foreground(t.Primary).Render("Event Log")
	count := lipgloss.NewStyle().Foreground(t.Muted).Render(fmt.Sprintf(" (%d events)", e.lastLen))

	// Severity tabs
	tabs := e.renderLevelTabs()

	// Auto-scroll indicator
	scrollIndicator := ""
	if e.autoScroll {
		scrollIndicator = lipgloss.NewStyle().Foreground(t.Accent).Render("  ● auto-scroll")
	}

	header := title + count + "  " + tabs + scrollIndicator

	// Filter
	filterView := ""
	if e.filter.Active() {
		filterView = e.filter.View() + "\n"
	}

	// Footer hints
	hints := lipgloss.NewStyle().Foreground(t.Muted).Render(
		"↑↓ scroll  1 all  2 inf+  3 wrn+  4 err  tab cycle  ctrl+s auto-scroll  / filter  esc back",
	)

	content := header + "\n" + filterView + e.viewport.View() + "\n" + hints
	return tea.NewView(content)
}

func (e *EventLog) renderLevelTabs() string {
	t := ui.ActiveTheme
	active := lipgloss.NewStyle().Bold(true).Foreground(t.BrightText).Background(t.Overlay).Padding(0, 1)
	inactive := lipgloss.NewStyle().Foreground(t.Muted).Padding(0, 1)

	var tabs []string
	for i, lf := range levelFilters {
		style := inactive
		if i == e.levelIdx {
			style = active
		}
		tabs = append(tabs, style.Render(lf.Label))
	}
	return strings.Join(tabs, " ")
}

func (e *EventLog) refreshContent() {
	entries := eventlog.Entries()
	e.lastLen = len(entries)
	t := ui.ActiveTheme

	infoStyle := lipgloss.NewStyle().Foreground(t.Accent)
	warnStyle := lipgloss.NewStyle().Foreground(t.Warning)
	errStyle := lipgloss.NewStyle().Foreground(t.Error)
	debugStyle := lipgloss.NewStyle().Foreground(t.Muted)
	tsStyle := lipgloss.NewStyle().Foreground(t.SubText)
	catStyle := lipgloss.NewStyle().Foreground(t.Secondary)

	filterText := strings.ToLower(e.filter.Value())
	var b strings.Builder
	shown := 0
	for _, entry := range entries {
		// Severity filter
		if !passesLevelFilter(entry.Level, e.levelIdx) {
			continue
		}

		// Text filter
		if filterText != "" {
			line := entry.Format()
			if !strings.Contains(strings.ToLower(line), filterText) {
				continue
			}
		}

		ts := tsStyle.Render(entry.Time.Format("15:04:05"))
		cat := catStyle.Render(fmt.Sprintf("[%s]", entry.Category))

		var lvl string
		switch entry.Level {
		case eventlog.LevelInfo:
			lvl = infoStyle.Render("INF")
		case eventlog.LevelWarn:
			lvl = warnStyle.Render("WRN")
		case eventlog.LevelError:
			lvl = errStyle.Render("ERR")
		case eventlog.LevelDebug:
			lvl = debugStyle.Render("DBG")
		}

		fmt.Fprintf(&b, "%s  %s  %s  %s\n", ts, lvl, cat, entry.Message)
		shown++
	}

	if shown == 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(t.Muted).Render("  No matching events."))
	}

	e.viewport.SetContent(b.String())

	if e.autoScroll {
		e.viewport.GotoBottom()
	}
}

func passesLevelFilter(level eventlog.Level, idx int) bool {
	switch idx {
	case 0: // ALL
		return true
	case 1: // INF+
		return level == eventlog.LevelInfo || level == eventlog.LevelWarn || level == eventlog.LevelError
	case 2: // WRN+
		return level == eventlog.LevelWarn || level == eventlog.LevelError
	case 3: // ERR
		return level == eventlog.LevelError
	default:
		return true
	}
}
