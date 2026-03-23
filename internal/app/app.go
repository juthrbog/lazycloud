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
	{Label: "ec2            EC2 instances", Value: "ec2"},
	{Label: "amis           EC2 AMIs", Value: "amis"},
	{Label: "s3             S3 buckets", Value: "s3"},
	{Label: "logs           Event log", Value: "logs"},
	{Label: "mode           Toggle ReadOnly/ReadWrite", Value: "mode"},
	{Label: "theme          Switch theme", Value: "theme"},
	{Label: "region         Switch region", Value: "region"},
	{Label: "profile        Switch profile", Value: "profile"},
}

// Side panel constants.
const (
	panelMinWidth  = 40
	panelMaxWidth  = 80
	panelThreshold = 120 // minimum terminal width to show side panel
)

// Model is the root application model — message router and layout compositor.
type Model struct {
	config    config.Config
	awsClient *aws.Client
	s3        aws.S3Service
	ec2       aws.EC2Service
	nav       *nav.Navigator
	confirm   ui.Confirm
	picker    ui.Picker
	toasts    ui.ToastManager
	width     int
	height    int
	err       string
	isDark    bool

	// Side detail panel
	panel        *ui.ContentView
	panelOpen    bool
	panelFocused bool
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
		s3:        aws.NewS3Service(awsClient),
		ec2:       aws.NewEC2Service(awsClient),
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
	// Remove expired toasts as a fallback in case dismiss commands were dropped.
	m.toasts.Cleanup()

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
		if m.panelOpen && !m.canShowPanel() {
			m.closePanel()
		}
		m.resizeViews()
		return m, nil

	case appmsg.NavigateMsg:
		// Show content in side panel when space permits
		if msg.ViewID == "content" && m.canShowPanel() {
			format := ui.ContentFormat(msg.Params["format"])
			m.openPanel(msg.Params["title"], msg.Params["content"], format)
			return m, nil
		}
		// Non-content navigation closes the panel
		if m.panelOpen {
			m.closePanel()
		}
		view := m.resolveView(msg)
		if view != nil {
			eventlog.Infof(eventlog.CatNav, "Navigate → %s", msg.ViewID)
			cmd := m.pushView(view)
			return m, cmd
		}
		eventlog.Warnf(eventlog.CatNav, "Unknown view: %s", msg.ViewID)
		return m, nil

	case appmsg.NavigateBackMsg:
		if m.panelOpen {
			m.closePanel()
		}
		if m.nav.Depth() > 1 {
			m.nav.Pop()
			m.resizeViews()
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

	case appmsg.RequestActionPickerMsg:
		var options []ui.PickerOption
		for _, opt := range msg.Options {
			options = append(options, ui.PickerOption{Label: opt, Value: opt})
		}
		title := msg.Title
		if title == "" {
			title = "Action"
		}
		m.picker.Show("action", title, options, 0)
		return m, nil

	case appmsg.RequestSortPickerMsg:
		var options []ui.PickerOption
		for _, col := range msg.Columns {
			options = append(options, ui.PickerOption{Label: col, Value: col})
		}
		initialIdx := 0
		if msg.CurrentCol >= 0 {
			initialIdx = msg.CurrentCol
		}
		m.picker.Show("sort", "Sort by Column", options, initialIdx)
		return m, nil

	case appmsg.RequestFeaturePickerMsg:
		var options []ui.PickerOption
		for i, label := range msg.Labels {
			options = append(options, ui.PickerOption{Label: label, Value: msg.ViewIDs[i]})
		}
		m.picker.Show("feature", msg.Service, options, 0)
		return m, nil

	case ui.ConfirmResultMsg:
		// Route to current view
		cmd := m.nav.UpdateCurrent(msg)
		return m, cmd

	case ui.PickerResultMsg:
		// Feature picker: navigate to the selected view
		if msg.ID == "feature" {
			if msg.Selected < 0 {
				return m, nil
			}
			viewID := msg.Value
			return m, func() tea.Msg {
				return appmsg.NavigateMsg{ViewID: viewID}
			}
		}
		// Sort and action pickers: route to current view
		if msg.ID == "sort" || msg.ID == "action" {
			if msg.Selected == -1 {
				return m, nil // esc cancel — do nothing
			}
			cmd := m.nav.UpdateCurrent(msg)
			return m, cmd
		}
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
		case "mode":
			return m.applyMode(msg.Value)
		}
		return m, nil

	case tea.KeyPressMsg:
		// Tab toggles focus between main view and panel
		if m.panelOpen && msg.String() == "tab" {
			m.panelFocused = !m.panelFocused
			return m, nil
		}

		// Panel-focused key handling
		if m.panelOpen && m.panelFocused && m.panel != nil {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "esc":
				m.closePanel()
				return m, nil
			case "e":
				return m, m.panel.OpenInEditorCmd()
			default:
				// Forward scroll/visual/yank keys to panel
				updated, cmd := m.panel.Update(msg)
				m.panel = &updated
				return m, cmd
			}
		}

		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			if m.panelOpen {
				m.closePanel()
				return m, nil
			}
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
		case "W":
			m.showModePicker()
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
	case "mode":
		m.showModePicker()
		return m, nil
	case "theme":
		m.showThemePicker()
		return m, nil
	case "region":
		m.showRegionPicker()
		return m, nil
	case "profile":
		m.showProfilePicker()
		return m, nil
	case "ec2":
		return m, func() tea.Msg {
			return appmsg.NavigateMsg{ViewID: "ec2_list"}
		}
	case "amis":
		return m, func() tea.Msg {
			return appmsg.NavigateMsg{ViewID: "ami_list"}
		}
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

// --- Side panel helpers ---

func (m Model) canShowPanel() bool {
	return m.width >= panelThreshold
}

func (m Model) panelWidth() int {
	w := m.width * 40 / 100
	if w < panelMinWidth {
		w = panelMinWidth
	}
	if w > panelMaxWidth {
		w = panelMaxWidth
	}
	return w
}

func (m *Model) openPanel(title, content string, format ui.ContentFormat) {
	if format == "" {
		format = ui.FormatAuto
	}
	cv := ui.NewContentView(title, content, format)
	m.panel = &cv
	m.panelOpen = true
	m.panelFocused = true
	m.resizeViews()
	eventlog.Infof(eventlog.CatUI, "Panel opened: %s", title)
}

func (m *Model) closePanel() {
	m.panel = nil
	m.panelOpen = false
	m.panelFocused = false
	m.resizeViews()
}

func (m *Model) resizeViews() {
	if m.width == 0 || m.height == 0 {
		return
	}
	innerH := m.height - 5
	if m.panelOpen && m.panel != nil {
		pw := m.panelWidth()
		mainW := m.width - pw - 3 // borders (2 each pane = 4) + gap, but JoinHorizontal handles it
		m.nav.UpdateCurrent(tea.WindowSizeMsg{Width: mainW, Height: innerH})
		m.panel.SetSize(pw-2, innerH-2) // subtract border from panel dimensions
	} else {
		m.nav.UpdateCurrent(tea.WindowSizeMsg{Width: m.width - 2, Height: innerH})
	}
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
	m.closePanel()
	m.config.AWS.Profile = profile

	// Recreate AWS client with new profile
	awsClient, err := aws.NewClient(profile, m.config.AWS.Region, m.config.AWS.Endpoint)
	if err != nil {
		eventlog.Errorf(eventlog.CatAWS, "Failed to create AWS client: %v", err)
	}
	m.awsClient = awsClient
	m.s3 = aws.NewS3Service(awsClient)
	m.ec2 = aws.NewEC2Service(awsClient)

	// Remember the active service view before resetting navigation
	returnTo := m.topLevelViewID()

	home := views.NewHome()
	m.nav = nav.New(home)
	m.err = ""
	_, toastCmd := m.toasts.Add("Profile: "+profile, ui.ToastSuccess, 0)

	var cmds []tea.Cmd
	if toastCmd != nil {
		cmds = append(cmds, toastCmd)
	}
	if returnTo != "" {
		viewID := returnTo
		cmds = append(cmds, func() tea.Msg {
			return appmsg.NavigateMsg{ViewID: viewID}
		})
	}
	if m.width > 0 && m.height > 0 {
		cmds = append(cmds, func() tea.Msg {
			return tea.WindowSizeMsg{Width: m.width, Height: m.height}
		})
	}
	return m, tea.Batch(cmds...)
}

func (m *Model) showModePicker() {
	options := []ui.PickerOption{
		{Label: "ReadOnly    Browse only, no mutations", Value: "readonly"},
		{Label: "ReadWrite   Allow create/delete/copy/move", Value: "readwrite"},
	}
	currentIdx := 0
	if !ui.ReadOnly {
		currentIdx = 1
	}
	m.picker.Show("mode", "Access Mode", options, currentIdx)
}

func (m Model) applyMode(mode string) (Model, tea.Cmd) {
	switch mode {
	case "readonly":
		ui.ReadOnly = true
		eventlog.Infof(eventlog.CatApp, "Mode changed → ReadOnly")
		_, cmd := m.toasts.Add("Mode: ReadOnly", ui.ToastInfo, 0)
		return m, cmd
	case "readwrite":
		ui.ReadOnly = false
		eventlog.Warnf(eventlog.CatApp, "Mode changed → ReadWrite")
		_, cmd := m.toasts.Add("Mode: ReadWrite", ui.ToastSuccess, 0)
		return m, cmd
	}
	return m, nil
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
	m.closePanel()
	m.config.AWS.Region = region

	// Recreate AWS client with new region
	awsClient, err := aws.NewClient(m.config.AWS.Profile, region, m.config.AWS.Endpoint)
	if err != nil {
		eventlog.Errorf(eventlog.CatAWS, "Failed to create AWS client: %v", err)
	}
	m.awsClient = awsClient
	m.s3 = aws.NewS3Service(awsClient)
	m.ec2 = aws.NewEC2Service(awsClient)

	// Remember the active service view before resetting navigation
	returnTo := m.topLevelViewID()

	home := views.NewHome()
	m.nav = nav.New(home)
	m.err = ""
	_, toastCmd := m.toasts.Add("Region: "+region, ui.ToastSuccess, 0)

	var cmds []tea.Cmd
	if toastCmd != nil {
		cmds = append(cmds, toastCmd)
	}
	if returnTo != "" {
		viewID := returnTo
		cmds = append(cmds, func() tea.Msg {
			return appmsg.NavigateMsg{ViewID: viewID}
		})
	}
	if m.width > 0 && m.height > 0 {
		cmds = append(cmds, func() tea.Msg {
			return tea.WindowSizeMsg{Width: m.width, Height: m.height}
		})
	}
	return m, tea.Batch(cmds...)
}

func (m Model) applyTheme(name string) (Model, tea.Cmd) {
	eventlog.Infof(eventlog.CatUI, "Theme changed → %s", name)
	m.closePanel()
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
	_, toastCmd := m.toasts.Add("Theme: "+ui.ActiveTheme.Name, ui.ToastSuccess, 0)

	// Re-send window size so the new home view sizes correctly
	var cmds []tea.Cmd
	if toastCmd != nil {
		cmds = append(cmds, toastCmd)
	}
	if m.width > 0 && m.height > 0 {
		cmds = append(cmds, func() tea.Msg {
			return tea.WindowSizeMsg{Width: m.width, Height: m.height}
		})
	}
	return m, tea.Batch(cmds...)
}

func (m Model) View() tea.View {
	t := ui.ActiveTheme

	mode := "RO"
	if !ui.ReadOnly {
		mode = "RW"
	}
	header := ui.RenderHeader(ui.HeaderData{
		Profile:     m.config.AWS.Profile,
		Region:      m.config.AWS.Region,
		Mode:        mode,
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

	var rendered string
	if m.panelOpen && m.panel != nil {
		pw := m.panelWidth()
		mainW := m.width - pw - 1 // 1 char gap between borders

		mainBorderColor := t.Secondary
		panelBorderColor := t.Secondary
		if m.panelFocused {
			panelBorderColor = t.Accent
		} else {
			mainBorderColor = t.Accent
		}

		mainBorder := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(mainBorderColor).
			Width(mainW - 2).
			Height(contentHeight - 2)

		panelBorder := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(panelBorderColor).
			Width(pw - 2).
			Height(contentHeight - 2)

		rendered = lipgloss.JoinHorizontal(lipgloss.Top,
			mainBorder.Render(contentStr),
			panelBorder.Render(m.panel.View()),
		)
	} else {
		borderStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(t.Secondary).
			Width(m.width - 2).
			Height(contentHeight - 2)

		rendered = borderStyle.Render(contentStr)
	}
	contentStr = rendered

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

// topLevelViewID returns the view ID of the top-level service view the user
// is currently in (e.g. "ec2_list", "s3_list"), or "" if on the home screen.
// Used to restore navigation after region/profile changes.
func (m Model) topLevelViewID() string {
	id := m.nav.Current().ID()
	switch {
	case strings.HasPrefix(id, "ec2"):
		return "ec2_list"
	case strings.HasPrefix(id, "ami"):
		return "ami_list"
	case strings.HasPrefix(id, "s3"):
		return "s3_list"
	default:
		return ""
	}
}

func (m Model) resolveView(n appmsg.NavigateMsg) nav.View {
	switch n.ViewID {
	case "ec2_list":
		return views.NewEC2List(m.ec2, m.awsClient)
	case "ami_list":
		return views.NewAMIList(m.ec2)
	case "s3_list":
		return views.NewS3List(m.s3, m.config.AWS.Region)
	case "s3_objects":
		return views.NewS3Objects(m.s3, n.Params["bucket"], n.Params["prefix"])
	case "s3_versions":
		return views.NewS3Versions(m.s3, n.Params["bucket"], n.Params["key"])
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
	if m.panelOpen && m.panelFocused {
		return []ui.KeyHint{
			{Key: "j/k", Desc: "scroll"},
			{Key: "g/G", Desc: "top/bottom"},
			{Key: "V", Desc: "visual"},
			{Key: "y", Desc: "yank"},
			{Key: "e", Desc: "editor"},
			{Key: "tab", Desc: "focus main"},
			{Key: "esc", Desc: "close panel"},
		}
	}

	// View-specific hints first
	hints := m.nav.Current().KeyMap()

	if m.panelOpen {
		hints = append(hints, ui.KeyHint{Key: "tab", Desc: "focus panel"})
	}

	// Global hints
	if m.nav.Depth() > 1 {
		hints = append(hints, ui.KeyHint{Key: "esc", Desc: "back"})
	}
	hints = append(hints,
		ui.KeyHint{Key: "W", Desc: "mode"},
		ui.KeyHint{Key: "L", Desc: "logs"},
		ui.KeyHint{Key: "P", Desc: "profile"},
		ui.KeyHint{Key: "R", Desc: "region"},
		ui.KeyHint{Key: "T", Desc: "theme"},
		ui.KeyHint{Key: ":", Desc: "command"},
		ui.KeyHint{Key: "q", Desc: "quit"},
	)
	return hints
}
