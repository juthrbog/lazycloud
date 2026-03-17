package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/config"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	appmsg "github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
	"github.com/juthrbog/lazycloud/internal/ui"
	"github.com/juthrbog/lazycloud/internal/views"
)

// Model is the root application model — message router and layout compositor.
type Model struct {
	config  config.Config
	nav     *nav.Navigator
	confirm ui.Confirm
	picker  ui.Picker
	width   int
	height  int
	err     string
	info    string
	isDark  bool
}

// New creates the root model with the home view as the starting screen.
func New(cfg config.Config) Model {
	home := views.NewHome()
	eventlog.Infof(eventlog.CatApp, "LazyCloud started (theme=%s, region=%s)", cfg.Display.Theme, cfg.AWS.Region)
	if cfg.AWS.Profile != "" {
		eventlog.Infof(eventlog.CatConfig, "AWS profile: %s", cfg.AWS.Profile)
	}
	if cfg.AWS.Endpoint != "" {
		eventlog.Infof(eventlog.CatConfig, "Endpoint override: %s (LocalStack)", cfg.AWS.Endpoint)
	}
	return Model{
		config:  cfg,
		nav:     nav.New(home),
		confirm: ui.NewConfirm(),
		picker:  ui.NewPicker(),
		isDark:  true,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.nav.Current().Init(),
		tea.RequestBackgroundColor,
	)
}

func (m Model) Update(teaMsg tea.Msg) (tea.Model, tea.Cmd) {
	// Picker intercepts all input when visible
	if m.picker.Visible() {
		var cmd tea.Cmd
		m.picker, cmd = m.picker.Update(teaMsg)
		return m, cmd
	}

	// Confirmation dialog intercepts all input when visible
	if m.confirm.Visible() {
		var cmd tea.Cmd
		m.confirm, cmd = m.confirm.Update(teaMsg)
		return m, cmd
	}

	switch msg := teaMsg.(type) {
	case tea.BackgroundColorMsg:
		m.isDark = msg.IsDark()
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		innerMsg := tea.WindowSizeMsg{Width: msg.Width - 2, Height: msg.Height - 5}
		cmd := m.nav.UpdateCurrent(innerMsg)
		return m, cmd

	case appmsg.NavigateMsg:
		view := m.resolveView(msg)
		if view != nil {
			eventlog.Infof(eventlog.CatNav, "Navigate → %s", msg.ViewID)
			cmd := m.pushView(view)
			return m, cmd
		}
		eventlog.Warnf(eventlog.CatNav, "Unknown view: %s", msg.ViewID)
		return m, nil

	case appmsg.NavigateBackMsg:
		if m.nav.Depth() > 1 {
			m.nav.Pop()
		}
		return m, nil

	case appmsg.ErrorMsg:
		m.err = msg.Context + ": " + msg.Err.Error()
		m.info = ""
		eventlog.Errorf(eventlog.CatApp, "%s: %v", msg.Context, msg.Err)
		return m, nil

	case appmsg.StatusMsg:
		m.info = msg.Text
		m.err = ""
		return m, nil

	case ui.PickerResultMsg:
		if msg.Selected < 0 {
			return m, nil
		}
		switch msg.ID {
		case "theme":
			return m.applyTheme(msg.Value)
		case "region":
			return m.applyRegion(msg.Value)
		case "profile":
			return m.applyProfile(msg.Value)
		}
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			if m.nav.Depth() <= 1 {
				return m, tea.Quit
			}
			m.nav.Pop()
			return m, nil
		case "esc":
			// Delegate esc to child first — it may need to dismiss a filter
			cmd := m.nav.UpdateCurrent(teaMsg)
			return m, cmd
		case "T":
			m.showThemePicker()
			return m, nil
		case "P":
			m.showProfilePicker()
			return m, nil
		case "R":
			m.showRegionPicker()
			return m, nil
		case "L":
			eventlog.Debug(eventlog.CatUI, "Event log opened")
			cmd := m.pushView(views.NewEventLog())
			return m, cmd
		}
	}

	cmd := m.nav.UpdateCurrent(teaMsg)
	return m, cmd
}

// pushView pushes a view onto the navigator and sends it the current window size.
func (m *Model) pushView(v nav.View) tea.Cmd {
	initCmd := m.nav.Push(v)
	if m.width > 0 && m.height > 0 {
		sizeCmd := m.nav.UpdateCurrent(tea.WindowSizeMsg{
			Width:  m.width - 2,
			Height: m.height - 5,
		})
		return tea.Batch(initCmd, sizeCmd)
	}
	return initCmd
}

func (m *Model) showThemePicker() {
	var options []ui.PickerOption
	currentIdx := 0
	for i, name := range ui.ThemeOrder {
		t := ui.Themes[name]
		label := t.Name
		if strings.EqualFold(t.Name, ui.ActiveTheme.Name) {
			label += " ●"
			currentIdx = i
		}
		options = append(options, ui.PickerOption{Label: label, Value: name})
	}
	m.picker.Show("theme", "Select Theme", options, currentIdx)
}

// Common AWS regions, ordered by popularity.
var awsRegions = []string{
	"us-east-1",
	"us-east-2",
	"us-west-1",
	"us-west-2",
	"eu-west-1",
	"eu-west-2",
	"eu-west-3",
	"eu-central-1",
	"eu-central-2",
	"eu-north-1",
	"eu-south-1",
	"ap-southeast-1",
	"ap-southeast-2",
	"ap-northeast-1",
	"ap-northeast-2",
	"ap-northeast-3",
	"ap-south-1",
	"ap-east-1",
	"ca-central-1",
	"sa-east-1",
	"me-south-1",
	"af-south-1",
}

func (m *Model) showProfilePicker() {
	profiles := aws.ListProfiles()
	if len(profiles) == 0 {
		m.info = "No profiles found in ~/.aws/config"
		return
	}

	current := m.config.AWS.Profile
	var options []ui.PickerOption
	currentIdx := 0
	for i, p := range profiles {
		label := p
		if p == current {
			label += " ●"
			currentIdx = i
		}
		options = append(options, ui.PickerOption{Label: label, Value: p})
	}
	m.picker.Show("profile", "Select Profile", options, currentIdx)
}

func (m Model) applyProfile(profile string) (Model, tea.Cmd) {
	eventlog.Infof(eventlog.CatConfig, "Profile changed → %s", profile)
	m.config.AWS.Profile = profile

	home := views.NewHome()
	m.nav = nav.New(home)
	m.err = ""
	m.info = "Profile: " + profile

	var cmd tea.Cmd
	if m.width > 0 && m.height > 0 {
		cmd = func() tea.Msg {
			return tea.WindowSizeMsg{Width: m.width, Height: m.height}
		}
	}
	return m, cmd
}

func (m *Model) showRegionPicker() {
	current := m.config.AWS.Region
	if current == "" {
		current = "us-east-1"
	}

	var options []ui.PickerOption
	currentIdx := 0
	for i, r := range awsRegions {
		label := r
		if r == current {
			label += " ●"
			currentIdx = i
		}
		options = append(options, ui.PickerOption{Label: label, Value: r})
	}
	m.picker.Show("region", "Select Region", options, currentIdx)
}

func (m Model) applyRegion(region string) (Model, tea.Cmd) {
	eventlog.Infof(eventlog.CatConfig, "Region changed → %s", region)
	m.config.AWS.Region = region

	home := views.NewHome()
	m.nav = nav.New(home)
	m.err = ""
	m.info = "Region: " + region

	var cmd tea.Cmd
	if m.width > 0 && m.height > 0 {
		cmd = func() tea.Msg {
			return tea.WindowSizeMsg{Width: m.width, Height: m.height}
		}
	}
	return m, cmd
}

func (m Model) applyTheme(name string) (Model, tea.Cmd) {
	eventlog.Infof(eventlog.CatUI, "Theme changed → %s", name)
	if t, ok := ui.Themes[name]; ok {
		ui.ActiveTheme = t
		ui.RebuildStyles()
	}

	// Rebuild navigator with fresh views using new theme
	home := views.NewHome()
	m.nav = nav.New(home)
	m.confirm = ui.NewConfirm()
	m.picker = ui.NewPicker()
	m.err = ""
	m.info = "Theme: " + ui.ActiveTheme.Name

	// Re-send window size so the new home view sizes correctly
	var cmd tea.Cmd
	if m.width > 0 && m.height > 0 {
		cmd = func() tea.Msg {
			return tea.WindowSizeMsg{Width: m.width, Height: m.height}
		}
	}
	return m, cmd
}

func (m Model) View() tea.View {
	t := ui.ActiveTheme

	header := ui.RenderHeader(ui.HeaderData{
		Profile:     m.config.AWS.Profile,
		Region:      m.config.AWS.Region,
		Breadcrumbs: m.nav.Breadcrumbs(),
		Width:       m.width,
	})

	keys := m.currentKeyHints()
	statusBar := ui.RenderStatusBar(ui.StatusBarData{
		Keys:  keys,
		Error: m.err,
		Info:  m.info,
		Width: m.width,
	})

	headerHeight := lipgloss.Height(header)
	statusHeight := lipgloss.Height(statusBar)
	contentHeight := m.height - headerHeight - statusHeight
	if contentHeight < 0 {
		contentHeight = 0
	}

	childView := m.nav.Current().View()
	contentStr := childView.Content

	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Secondary).
		Width(m.width - 2).
		Height(contentHeight - 2)

	contentStr = borderStyle.Render(contentStr)

	body := lipgloss.JoinVertical(lipgloss.Left, header, contentStr, statusBar)

	// Overlay picker or confirm dialog
	if m.picker.Visible() {
		dialog := m.picker.View()
		body = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, dialog)
	} else if m.confirm.Visible() {
		dialog := m.confirm.View()
		body = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, dialog)
	}

	v := tea.NewView(body)
	v.AltScreen = true
	return v
}

func (m Model) resolveView(n appmsg.NavigateMsg) nav.View {
	switch n.ViewID {
	case "ec2_list":
		return views.NewEC2List()
	case "eventlog":
		return views.NewEventLog()
	case "content":
		title := n.Params["title"]
		content := n.Params["content"]
		format := ui.ContentFormat(n.Params["format"])
		if format == "" {
			format = ui.FormatAuto
		}
		return views.NewContentViewer("content:"+title, title, content, format)
	default:
		return nil
	}
}

func (m Model) currentKeyHints() []ui.KeyHint {
	hints := []ui.KeyHint{
		{Key: "enter", Desc: "select"},
		{Key: "/", Desc: "filter"},
		{Key: "r", Desc: "refresh"},
	}
	if m.nav.Depth() > 1 {
		hints = append(hints, ui.KeyHint{Key: "esc", Desc: "back"})
	}
	hints = append(hints, ui.KeyHint{Key: "L", Desc: "logs"})
	hints = append(hints, ui.KeyHint{Key: "P", Desc: "profile"})
	hints = append(hints, ui.KeyHint{Key: "R", Desc: "region"})
	hints = append(hints, ui.KeyHint{Key: "T", Desc: "theme"})
	hints = append(hints, ui.KeyHint{Key: "q", Desc: "quit"})
	return hints
}
