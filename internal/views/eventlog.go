package views

import (
	"fmt"
	"strings"

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

// EventLog displays the in-app event log with scrolling and filtering.
type EventLog struct {
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
func (e *EventLog) KeyMap() []ui.KeyHint {
	return []ui.KeyHint{
		{Key: "1-4", Desc: "severity"},
		{Key: "tab", Desc: "cycle"},
		{Key: "ctrl+s", Desc: "auto-scroll"},
		{Key: "/", Desc: "filter"},
		{Key: "r", Desc: "refresh"},
	}
}

// NewEventLog creates the event log view.
func NewEventLog() *EventLog {
	vp := viewport.New()
	return &EventLog{
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

		switch msg.String() {
		case "esc":
			return e, func() tea.Msg { return msg_pkg.NavigateBackMsg{} }
		case "/":
			e.filter.Activate()
			return e, nil
		case "ctrl+s":
			e.autoScroll = !e.autoScroll
			return e, nil
		case "r":
			e.refreshContent()
			return e, nil
		case "1":
			e.levelIdx = 0
			e.refreshContent()
			return e, nil
		case "2":
			e.levelIdx = 1
			e.refreshContent()
			return e, nil
		case "3":
			e.levelIdx = 2
			e.refreshContent()
			return e, nil
		case "4":
			e.levelIdx = 3
			e.refreshContent()
			return e, nil
		case "tab":
			e.levelIdx = (e.levelIdx + 1) % len(levelFilters)
			e.refreshContent()
			return e, nil
		case "shift+tab":
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
