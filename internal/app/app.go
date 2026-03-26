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
	"github.com/juthrbog/lazycloud/internal/registry"
	"github.com/juthrbog/lazycloud/internal/ui"
	"github.com/juthrbog/lazycloud/internal/views"
)


// Side panel constants.
const (
	panelMinWidth = 40
	panelMaxWidth = 80
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
	help       ui.HelpOverlay
	commandBar ui.CommandBar
	toasts     ui.ToastManager
	width     int
	height    int
	err       string
	isDark    bool

	// Side detail panel
	panel        *ui.TabbedPanel
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
		help:       ui.NewHelpOverlay(),
		commandBar: ui.NewCommandBar(),
		toasts:     ui.NewToastManager(),
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

	// Help overlay intercepts all input when visible
	if m.help.Visible() {
		var cmd tea.Cmd
		m.help, cmd = m.help.Update(teaMsg)
		return m, cmd
	}

	// Command bar intercepts all input when visible
	if m.commandBar.Visible() {
		var cmd tea.Cmd
		m.commandBar, cmd = m.commandBar.Update(teaMsg)
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

	case appmsg.TabbedContentMsg:
		if m.canShowPanel() {
			m.openTabbedPanel(msg.PanelTitle, msg.Tabs)
			return m, nil
		}
		// Narrow terminal fallback: push first tab as full-screen content
		if len(msg.Tabs) > 0 {
			first := msg.Tabs[0]
			return m, func() tea.Msg {
				return appmsg.NavigateMsg{
					ViewID: "content",
					Params: map[string]string{
						"title":   first.Title,
						"content": first.Content,
						"format":  first.Format,
					},
				}
			}
		}
		return m, nil

	case appmsg.ErrorMsg:
		m.err = msg.Context + ": " + msg.Err.Error()
		eventlog.Errorf(eventlog.CatApp, "%s: %v", msg.Context, msg.Err)
		return m, nil

	case ui.ContentLinkActivatedMsg:
		// Cross-resource navigation from the detail panel
		m.closePanel()
		viewID := msg.ViewID
		params := msg.Params
		return m, func() tea.Msg {
			return appmsg.NavigateMsg{ViewID: viewID, Params: params}
		}

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

	case ui.CommandBarResultMsg:
		if msg.Cancelled {
			return m, nil
		}
		m.commandBar.AddHistory(msg.Value)
		return m.executeCommand(msg.Value)

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
			m.commandBar.Show(registry.CommandBarEntries(), m.width)
			return m, nil
		case "?":
			hints := m.collectAllKeyHints()
			m.help.Show(hints, m.width, m.height)
			return m, nil
		}
	}

	cmd := m.nav.UpdateCurrent(teaMsg)
	return m, cmd
}

func (m Model) executeCommand(input string) (Model, tea.Cmd) {
	cmd := registry.LookupCommand(input)
	if cmd == nil {
		_, toastCmd := m.toasts.Add("Unknown command: "+input, ui.ToastError, 0)
		return m, toastCmd
	}

	// Navigation commands emit a NavigateMsg.
	if cmd.IsNav() {
		viewID := cmd.ViewID
		return m, func() tea.Msg {
			return appmsg.NavigateMsg{ViewID: viewID}
		}
	}

	// Action commands handled individually.
	switch cmd.Name {
	case "quit":
		return m, tea.Quit
	case "home":
		home := views.NewHome()
		m.nav = nav.New(home)
		m.err = ""
		return m, m.resizeCmd()
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
	default:
		_, toastCmd := m.toasts.Add("Unknown command: "+input, ui.ToastError, 0)
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
	return ui.GetWidthTier(m.width) == ui.TierWide
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
	m.openTabbedPanel(title, []appmsg.TabContent{
		{Title: title, Content: content, Format: string(format)},
	})
}

func (m *Model) openTabbedPanel(title string, tabs []appmsg.TabContent) {
	tabInputs := make([]ui.TabInput, len(tabs))
	for i, t := range tabs {
		var links map[int]ui.ContentLink
		if len(t.Links) > 0 {
			links = make(map[int]ui.ContentLink)
			for _, l := range t.Links {
				links[l.Line] = ui.ContentLink{ViewID: l.ViewID, Params: l.Params}
			}
		}
		tabInputs[i] = ui.TabInput{
			Title:   t.Title,
			Content: t.Content,
			Format:  t.Format,
			Links:   links,
		}
	}
	tp := ui.NewTabbedPanel(title, tabInputs)
	m.panel = &tp
	m.panelOpen = true
	m.panelFocused = true
	m.resizeViews()
	eventlog.Infof(eventlog.CatUI, "Panel opened: %s (%d tabs)", title, len(tabs))
}

func (m *Model) closePanel() {
	m.panel = nil
	m.panelOpen = false
	m.panelFocused = false
	m.resizeViews()
}

// chromeHeight returns the vertical lines consumed by fixed layout chrome:
// header (2: title bar + gradient) + status bar (1) + content border (2: top + bottom).
func (m Model) chromeHeight() int {
	return 5
}

func (m *Model) resizeViews() {
	if m.width == 0 || m.height == 0 {
		return
	}
	innerH := m.height - m.chromeHeight()
	if m.panelOpen && m.panel != nil {
		pw := m.panelWidth()
		mainW := m.width - pw - 3 // inner content width: total(m.width) - panel(pw) - gap(1) - main borders(2)
		m.nav.UpdateCurrent(tea.WindowSizeMsg{Width: mainW, Height: innerH})
		m.panel.SetSize(pw-2, innerH)
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

	var statusBar string
	if m.commandBar.Visible() {
		statusBar = m.commandBar.ViewInput(m.width)
	} else {
		keys := m.currentKeyHints()
		statusBar = ui.RenderStatusBar(ui.StatusBarData{
			Keys:  keys,
			Error: m.err,
			Width: m.width,
		})
	}

	headerHeight := lipgloss.Height(header)
	statusHeight := lipgloss.Height(statusBar)
	contentHeight := m.height - headerHeight - statusHeight

	// Ensure minimum table height by hiding chrome
	minContent := ui.MinTableRows + 2 // +2 for border
	if contentHeight < minContent && headerHeight > 0 {
		header = ""
		headerHeight = 0
		contentHeight = m.height - statusHeight
	}
	if contentHeight < minContent && statusHeight > 0 {
		statusBar = ""
		statusHeight = 0
		contentHeight = m.height
	}
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
			Width(mainW).
			Height(contentHeight).
			MaxHeight(contentHeight)

		panelBorder := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(panelBorderColor).
			Width(pw).
			Height(contentHeight).
			MaxHeight(contentHeight)

		mainContent := contentStr
		panelContent := m.panel.View()
		faint := lipgloss.NewStyle().Faint(true)
		if m.panelFocused {
			mainContent = faint.Render(mainContent)
		} else {
			panelContent = faint.Render(panelContent)
		}

		rendered = lipgloss.JoinHorizontal(lipgloss.Top,
			mainBorder.Render(mainContent),
			panelBorder.Render(panelContent),
		)
	} else {
		borderStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(t.Secondary).
			Width(m.width).
			Height(contentHeight).
			MaxHeight(contentHeight)

		rendered = borderStyle.Render(contentStr)
	}
	contentStr = rendered

	body := lipgloss.JoinVertical(lipgloss.Left, header, contentStr, statusBar)

	// Help overlay
	if m.help.Visible() {
		body = composeOverlay(body, m.help.View(), m.width, m.height)
	}

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

	// Command bar suggestions above the input line
	if m.commandBar.Visible() {
		if suggestions := m.commandBar.ViewSuggestions(); suggestions != "" {
			sugH := lipgloss.Height(suggestions)
			y := m.height - sugH - 1
			if y < 0 {
				y = 0
			}
			comp := lipgloss.NewCompositor(
				lipgloss.NewLayer(body).Z(0),
				lipgloss.NewLayer(suggestions).X(1).Y(y).Z(1),
			)
			body = comp.Render()
		}
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
		hints := []ui.KeyHint{}
		if m.panel != nil && m.panel.TabCount() > 1 {
			hints = append(hints, ui.KeyHint{
				Key:  fmt.Sprintf("1-%d", m.panel.TabCount()),
				Desc: "switch tab",
			})
		}
		hints = append(hints,
			ui.KeyHint{Key: "j/k", Desc: "scroll"},
			ui.KeyHint{Key: "g/G", Desc: "top/bottom"},
			ui.KeyHint{Key: "V", Desc: "visual"},
			ui.KeyHint{Key: "y", Desc: "yank"},
			ui.KeyHint{Key: "e", Desc: "editor"},
			ui.KeyHint{Key: "tab", Desc: "focus main"},
			ui.KeyHint{Key: "esc", Desc: "close panel"},
		)
		return hints
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
		ui.KeyHint{Key: "?", Desc: "help"},
		ui.KeyHint{Key: "q", Desc: "quit"},
	)
	return hints
}

// collectAllKeyHints returns all keybindings with categories set for the help overlay.
func (m Model) collectAllKeyHints() []ui.KeyHint {
	var hints []ui.KeyHint

	// View-specific hints
	for _, h := range m.nav.Current().KeyMap() {
		// Leave Category empty — rendered as "Current View"
		hints = append(hints, h)
	}

	// Navigation hints
	if m.panelOpen {
		hints = append(hints, ui.KeyHint{Key: "tab", Desc: "toggle panel focus", Category: "Navigation"})
	}
	if m.nav.Depth() > 1 {
		hints = append(hints, ui.KeyHint{Key: "esc", Desc: "go back", Category: "Navigation"})
	}

	// Panel hints (when panel is open)
	if m.panelOpen {
		panelHints := []ui.KeyHint{
			{Key: "j/k", Desc: "scroll", Category: "Panel"},
			{Key: "g/G", Desc: "top/bottom", Category: "Panel"},
			{Key: "V", Desc: "visual select", Category: "Panel"},
			{Key: "y", Desc: "yank to clipboard", Category: "Panel"},
			{Key: "e", Desc: "open in editor", Category: "Panel"},
			{Key: "esc", Desc: "close panel", Category: "Panel"},
		}
		hints = append(hints, panelHints...)
	}

	// Global hints
	globalHints := []ui.KeyHint{
		{Key: "W", Desc: "toggle ReadOnly/ReadWrite", Category: "Global"},
		{Key: "L", Desc: "event log", Category: "Global"},
		{Key: "P", Desc: "switch AWS profile", Category: "Global"},
		{Key: "R", Desc: "switch AWS region", Category: "Global"},
		{Key: "T", Desc: "switch theme", Category: "Global"},
		{Key: ":", Desc: "command palette", Category: "Global"},
		{Key: "?", Desc: "this help", Category: "Global"},
		{Key: "q", Desc: "quit", Category: "Global"},
	}
	hints = append(hints, globalHints...)

	return hints
}
