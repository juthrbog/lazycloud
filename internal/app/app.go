package app

import (
	"fmt"
	"strings"
	"time"

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

var appCommands = []ui.PickerOption{
	{Label: "quit           Exit LazyCloud", Value: "quit"},
	{Label: "home           Go to home screen", Value: "home"},
	{Label: "s3             S3 buckets", Value: "s3"},
{Label: "logs           Event log", Value: "logs"},
	{Label: "theme          Switch theme", Value: "theme"},
	{Label: "region         Switch region", Value: "region"},
	{Label: "profile        Switch profile", Value: "profile"},
}

// Model is the root application model — message router and layout compositor.
type Model struct {
	config    config.Config
	awsClient *aws.Client
	nav       *nav.Navigator
	confirm   ui.Confirm
	picker    ui.Picker
	toasts    ui.ToastManager
	width     int
	height    int
	err       string
	isDark    bool
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

	awsClient, err := aws.NewClient(cfg.AWS.Profile, cfg.AWS.Region, cfg.AWS.Endpoint)
	if err != nil {
		eventlog.Errorf(eventlog.CatAWS, "Failed to create AWS client: %v", err)
	}

	return Model{
		config:    cfg,
		awsClient: awsClient,
		nav:       nav.New(home),
		confirm:   ui.NewConfirm(),
		picker:    ui.NewPicker(),
		toasts:    ui.NewToastManager(),
		isDark:    true,
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
		eventlog.Errorf(eventlog.CatApp, "%s: %v", msg.Context, msg.Err)
		return m, nil

	case appmsg.StatusMsg:
		// Legacy — convert to toast
		_, cmd := m.toasts.Add(msg.Text, ui.ToastInfo, 0)
		m.err = ""
		return m, cmd

	case appmsg.ToastMsg:
		_, cmd := m.toasts.Add(msg.Text, ui.ToastLevel(msg.Level), 0)
		return m, cmd

	case ui.ToastDismissMsg:
		m.toasts.Dismiss(msg.ID)
		return m, nil

	case appmsg.RequestConfirmMsg:
		m.confirm.Show(msg.Message, msg.Action)
		return m, nil

	case ui.ConfirmResultMsg:
		// Route to current view
		cmd := m.nav.UpdateCurrent(msg)
		return m, cmd

	case ui.PickerResultMsg:
		if msg.Selected < 0 {
			return m, nil
		}
		switch msg.ID {
		case "command":
			return m.executeCommand(msg.Value)
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
		case ":":
			m.picker.Show("command", "Command", appCommands, 0)
			return m, nil
		}
	}

	cmd := m.nav.UpdateCurrent(teaMsg)
	return m, cmd
}

func (m Model) executeCommand(cmd string) (Model, tea.Cmd) {
	switch cmd {
	case "q", "quit":
		return m, tea.Quit
	case "qa", "qall":
		return m, tea.Quit
	case "home":
		home := views.NewHome()
		m.nav = nav.New(home)
		m.err = ""
		return m, m.resizeCmd()
	case "logs", "log", "events":
		return m, func() tea.Msg {
			return appmsg.NavigateMsg{ViewID: "eventlog"}
		}
	case "theme":
		m.showThemePicker()
		return m, nil
	case "region":
		m.showRegionPicker()
		return m, nil
	case "profile":
		m.showProfilePicker()
		return m, nil
	case "s3":
		return m, func() tea.Msg {
			return appmsg.NavigateMsg{ViewID: "s3_list"}
		}
	default:
		_, toastCmd := m.toasts.Add("Unknown command: "+cmd, ui.ToastError, 0)
		return m, toastCmd
	}
}

func (m Model) resizeCmd() tea.Cmd {
	if m.width > 0 && m.height > 0 {
		return func() tea.Msg {
			return tea.WindowSizeMsg{Width: m.width, Height: m.height}
		}
	}
	return nil
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
		m.toasts.Add("No profiles found in ~/.aws/config", ui.ToastError, 0)
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

	// Recreate AWS client with new profile
	awsClient, err := aws.NewClient(profile, m.config.AWS.Region, m.config.AWS.Endpoint)
	if err != nil {
		eventlog.Errorf(eventlog.CatAWS, "Failed to create AWS client: %v", err)
	}
	m.awsClient = awsClient

	home := views.NewHome()
	m.nav = nav.New(home)
	m.err = ""
	m.toasts.Add("Profile: "+profile, ui.ToastSuccess, 0)

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

	// Recreate AWS client with new region
	awsClient, err := aws.NewClient(m.config.AWS.Profile, region, m.config.AWS.Endpoint)
	if err != nil {
		eventlog.Errorf(eventlog.CatAWS, "Failed to create AWS client: %v", err)
	}
	m.awsClient = awsClient

	home := views.NewHome()
	m.nav = nav.New(home)
	m.err = ""
	m.toasts.Add("Region: "+region, ui.ToastSuccess, 0)

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
	m.toasts.Add("Theme: "+ui.ActiveTheme.Name, ui.ToastSuccess, 0)

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

	// Overlay popup centered on the full screen
	if m.picker.Visible() || m.confirm.Visible() {
		var dialog string
		if m.picker.Visible() {
			dialog = m.picker.View()
		} else {
			dialog = m.confirm.View()
		}
		body = composeOverlay(body, dialog, m.width, m.height)
	}

	// Overlay toasts in bottom-right
	if m.toasts.HasActive() {
		toastView := m.toasts.View(m.width)
		toastW := lipgloss.Width(toastView)
		toastH := lipgloss.Height(toastView)
		x := m.width - toastW - 2
		y := m.height - toastH - 2
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
		comp := lipgloss.NewCompositor(
			lipgloss.NewLayer(body).Z(0),
			lipgloss.NewLayer(toastView).X(x).Y(y).Z(1),
		)
		body = comp.Render()
	}

	v := tea.NewView(body)
	v.AltScreen = true
	return v
}

// composeOverlay renders a dialog centered on top of a background using
// lipgloss Compositor for proper layer compositing.
func composeOverlay(bg, dialog string, bgWidth, bgHeight int) string {
	dlgW := lipgloss.Width(dialog)
	dlgH := lipgloss.Height(dialog)
	x := (bgWidth - dlgW) / 2
	y := (bgHeight - dlgH) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}

	comp := lipgloss.NewCompositor(
		lipgloss.NewLayer(bg).Z(0),
		lipgloss.NewLayer(dialog).X(x).Y(y).Z(1),
	)
	return comp.Render()
}

func (m Model) resolveView(n appmsg.NavigateMsg) nav.View {
	switch n.ViewID {
	case "service_menu":
		name := n.Params["service"]
		features := views.ServiceFeatures(name)
		if features == nil {
			return nil
		}
		return views.NewServiceMenu(name, features)
	case "s3_list":
		return views.NewS3List(m.awsClient)
	case "s3_objects":
		return views.NewS3Objects(m.awsClient, n.Params["bucket"], n.Params["prefix"])
	case "s3_versions":
		return views.NewS3Versions(m.awsClient, n.Params["bucket"], n.Params["key"])
	case "eventlog":
		return views.NewEventLog()
	case "content":
		title := n.Params["title"]
		content := n.Params["content"]
		format := ui.ContentFormat(n.Params["format"])
		if format == "" {
			format = ui.FormatAuto
		}
		// Use a unique ID so content viewers are never cached
		id := fmt.Sprintf("content:%s:%d", title, time.Now().UnixNano())
		return views.NewContentViewer(id, title, content, format)
	default:
		return nil
	}
}

func (m Model) currentKeyHints() []ui.KeyHint {
	// View-specific hints first
	hints := m.nav.Current().KeyMap()

	// Global hints
	if m.nav.Depth() > 1 {
		hints = append(hints, ui.KeyHint{Key: "esc", Desc: "back"})
	}
	hints = append(hints,
		ui.KeyHint{Key: "L", Desc: "logs"},
		ui.KeyHint{Key: "P", Desc: "profile"},
		ui.KeyHint{Key: "R", Desc: "region"},
		ui.KeyHint{Key: "T", Desc: "theme"},
		ui.KeyHint{Key: ":", Desc: "command"},
		ui.KeyHint{Key: "q", Desc: "quit"},
	)
	return hints
}
