package app

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
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
	panelMinWidth     = 30
	panelMaxWidth     = 80
	panelMinMainWidth = 30 // minimum width for main view when panel is open
	panelResizeStep   = 5  // columns per resize keystroke
)

// Cross-navigation history for back traversal.
const crossNavHistoryMax = 10

type crossNavEntry struct {
	navDepth   int                 // nav stack depth when saved
	panelTitle string              // panel title to restore
	panelTabs  []appmsg.TabContent // panel content to restore
}

// Model is the root application model — message router and layout compositor.
type Model struct {
	config     config.Config
	svc        aws.Services // AWS services of the current session
	svcErr     error        // why no session exists; nil when svc is usable
	nav        *nav.Navigator
	confirm    ui.Confirm
	picker     ui.Picker
	help       ui.HelpOverlay
	commandBar ui.CommandBar
	toasts     ui.ToastManager
	width      int
	height     int
	err        string
	errView    nav.View // view that err belongs to; err shows only while it is on top
	isDark     bool

	keys ui.AppKeyMap

	// Side detail panel
	panel              *ui.TabbedPanel
	panelOpen          bool
	panelFocused       bool
	panelTitle         string
	panelWidthOverride int // 0 = use default 40% calculation

	// Stashed panel state for resize restoration
	stashedPanelTitle string
	stashedPanelTabs  []appmsg.TabContent

	// Cross-navigation history for back traversal
	crossNavHistory []crossNavEntry

	// Pending theme name awaiting confirmation when nav stack is deep
	pendingTheme string
}

// New creates the root model with the home view as the starting screen.
func New(cfg config.Config) Model {
	home := views.NewHome()
	if cfg.AWS.Profile != "" {
		eventlog.Infof(eventlog.CatConfig, "AWS profile: %s", cfg.AWS.Profile)
	}
	if cfg.AWS.Endpoint != "" {
		eventlog.Infof(eventlog.CatConfig, "Endpoint override: %s (LocalStack)", cfg.AWS.Endpoint)
	}

	m := Model{
		config:     cfg,
		nav:        nav.New(home),
		confirm:    ui.NewConfirm(),
		picker:     ui.NewPicker(),
		help:       ui.NewHelpOverlay(),
		commandBar: ui.NewCommandBar(),
		toasts:     ui.NewToastManager(),
		keys:       ui.DefaultAppKeyMap(),
		isDark:     true,
	}
	// Without a session, the app still starts. It shows the error on Home and
	// opens no AWS view until a profile or region switch succeeds.
	m.svc, m.svcErr = aws.NewServices(cfg.AWS.Profile, cfg.AWS.Region, cfg.AWS.Endpoint, ui.IsReadOnly)
	if m.svcErr != nil {
		eventlog.Errorf(eventlog.CatAWS, "Failed to create AWS client: %v", m.svcErr)
		m.err = noSessionText(m.svcErr)
		m.errView = home
	}
	eventlog.Infof(eventlog.CatApp, "LazyCloud started (theme=%s, region=%s)", cfg.Display.Theme, m.activeRegion())
	return m
}

// noSessionText explains why the app has no AWS session and how to fix it.
func noSessionText(err error) string {
	return "No AWS session (press P for a profile or R for a region): " + err.Error()
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.nav.InitCurrent(),
		tea.RequestBackgroundColor,
	)
}

// handleRouted delivers the result of a command that a view issued.
// See nav.Routed for why results carry their origin view.
func (m Model) handleRouted(r nav.Routed) (tea.Model, tea.Cmd) {
	switch inner := r.Msg.(type) {
	case appmsg.ToastMsg, appmsg.StatusMsg:
		// Feedback is useful even from a view the user has left.
		return m.Update(inner)

	case appmsg.NavigateMsg, appmsg.NavigateBackMsg, appmsg.TabbedContentMsg,
		appmsg.RequestFormMsg, appmsg.FormResultMsg, appmsg.RequestConfirmMsg,
		appmsg.RequestActionPickerMsg, appmsg.RequestSortPickerMsg,
		appmsg.RequestFeaturePickerMsg, ui.RequestFormDiscardConfirmMsg,
		ui.ContentLinkActivatedMsg:
		// Navigation, overlays, and the side panel act on what the user sees.
		// A late result from a view that is no longer on top must not move
		// the user or open a dialog or panel over an unrelated view.
		if !m.nav.IsCurrent(r.Origin) {
			eventlog.Debugf(eventlog.CatNav, "Dropped %T from %s: view is not on top", inner, r.Origin.ID())
			return m, nil
		}
		return m.Update(inner)

	case ui.AnimationFrameMsg:
		// Animate only while visible. The app restarts the tick when the
		// home view is shown again.
		if !m.nav.IsCurrent(r.Origin) {
			return m, nil
		}
	}

	if !m.nav.Contains(r.Origin) {
		eventlog.Debugf(eventlog.CatNav, "Dropped %T from %s: view no longer exists", r.Msg, r.Origin.ID())
		return m, nil
	}
	if e, ok := r.Msg.(appmsg.ErrorMsg); ok {
		eventlog.Errorf(eventlog.CatApp, "%s: %v", e.Context, e.Err)
		m.err = e.Context + ": " + e.Err.Error()
		m.errView = r.Origin
	}
	return m, m.nav.UpdateView(r.Origin, r.Msg)
}

// visibleErr returns the status bar error for the current view. An error
// stays with the view that caused it, so navigating away hides it.
func (m Model) visibleErr() string {
	if m.err == "" || m.errView == nil || !m.nav.IsCurrent(m.errView) {
		return ""
	}
	return m.err
}

func (m Model) Update(teaMsg tea.Msg) (tea.Model, tea.Cmd) {
	// Remove expired toasts as a fallback in case dismiss commands were dropped.
	m.toasts.Cleanup()

	// Overlays intercept key input when visible, but let async data messages
	// (page loads, spinner ticks, etc.) fall through to normal routing.
	if _, isKey := teaMsg.(tea.KeyPressMsg); isKey {
		if m.picker.Visible() {
			var cmd tea.Cmd
			m.picker, cmd = m.picker.Update(teaMsg)
			return m, cmd
		}
		if m.confirm.Visible() {
			var cmd tea.Cmd
			m.confirm, cmd = m.confirm.Update(teaMsg)
			return m, cmd
		}
		if m.help.Visible() {
			var cmd tea.Cmd
			m.help, cmd = m.help.Update(teaMsg)
			return m, cmd
		}
		if m.commandBar.Visible() {
			var cmd tea.Cmd
			m.commandBar, cmd = m.commandBar.Update(teaMsg)
			return m, cmd
		}
	}

	switch msg := teaMsg.(type) {
	case nav.Routed:
		return m.handleRouted(msg)

	case tea.BackgroundColorMsg:
		m.isDark = msg.IsDark()
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.panelOpen && !m.canShowPanel() {
			m.stashPanel()
			m.closePanel()
		}
		if !m.panelOpen && m.canShowPanel() && m.stashedPanelTitle != "" {
			m.openTabbedPanel(m.stashedPanelTitle, m.stashedPanelTabs)
			m.clearStashedPanel()
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
			m.clearStashedPanel()
			m.closePanel()
		}
		if focusID := msg.Params["focus"]; focusID != "" && m.nav.Current().ID() == msg.ViewID {
			// A link to another row of the view on top only moves its
			// cursor. Pushing would put the same view on the stack twice.
			return m, func() tea.Msg { return appmsg.FocusResourceMsg{ResourceID: focusID} }
		}
		if m.svcErr != nil && needsSession(msg.ViewID) {
			_, cmd := m.toasts.Add(noSessionText(m.svcErr), ui.ToastError, 0)
			return m, cmd
		}
		view := m.resolveView(msg)
		if view != nil {
			eventlog.Infof(eventlog.CatNav, "Navigate → %s", msg.ViewID)
			cmd := m.pushView(view)
			if focusID := msg.Params["focus"]; focusID != "" {
				focusCmd := func() tea.Msg {
					return appmsg.FocusResourceMsg{ResourceID: focusID}
				}
				return m, tea.Batch(cmd, focusCmd)
			}
			return m, cmd
		}
		eventlog.Warnf(eventlog.CatNav, "Unknown view: %s", msg.ViewID)
		return m, nil

	case appmsg.NavigateBackMsg:
		if m.panelOpen {
			m.clearStashedPanel()
			m.closePanel()
		}
		m.clearStashedPanel()
		if m.nav.Depth() > 1 {
			m.nav.Pop()
			m.trimCrossNavHistory()
			m.resizeViews()
		}
		if m.nav.Depth() == 1 {
			return m, m.nav.InitCurrent()
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
		m.errView = m.nav.Current()
		eventlog.Errorf(eventlog.CatApp, "%s: %v", msg.Context, msg.Err)
		cmd := m.nav.UpdateCurrent(teaMsg)
		return m, cmd

	case ui.ContentLinkActivatedMsg:
		// Save panel state before cross-navigation for back traversal
		if m.panelOpen && m.panel != nil {
			m.pushCrossNavHistory()
		}
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

	case appmsg.RequestFormMsg:
		formView := ui.NewFormView(msg)
		if m.panelOpen {
			m.clearStashedPanel()
			m.closePanel()
		}
		cmd := m.pushView(formView)
		return m, cmd

	case appmsg.FormResultMsg:
		// Pop the form view, then route the result to the revealed parent view.
		if m.nav.Depth() > 1 {
			m.nav.Pop()
			m.resizeViews()
		}
		cmd := m.nav.UpdateCurrent(msg)
		return m, cmd

	case ui.RequestFormDiscardConfirmMsg:
		m.confirm.ShowQuick("Discard unsaved changes?", "form_discard")
		return m, nil

	case appmsg.RequestConfirmMsg:
		m.confirm.Show(msg.Message, msg.Action, msg.Phrase)
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
		if msg.Action == "theme_switch" {
			if msg.Confirmed && m.pendingTheme != "" {
				name := m.pendingTheme
				m.pendingTheme = ""
				return m.applyTheme(name)
			}
			m.pendingTheme = ""
			return m, nil
		}
		if msg.Action == "form_discard" {
			if msg.Confirmed {
				if fv, ok := m.nav.Current().(*ui.FormView); ok {
					formID := fv.FormID()
					return m, func() tea.Msg {
						return appmsg.FormResultMsg{ID: formID, Aborted: true}
					}
				}
			}
			return m, nil
		}
		// Route to current view
		cmd := m.nav.UpdateCurrent(msg)
		return m, cmd

	case ui.PickerResultMsg:
		// Service picker: navigate to selected service (or show feature picker for multi-feature)
		if msg.ID == "service" {
			if msg.Selected < 0 {
				return m, nil
			}
			for _, svc := range registry.Services {
				if svc.Name == msg.Value {
					if len(svc.Features) == 1 {
						viewID := svc.Features[0].ViewID
						return m, func() tea.Msg {
							return appmsg.NavigateMsg{ViewID: viewID}
						}
					}
					labels := make([]string, len(svc.Features))
					viewIDs := make([]string, len(svc.Features))
					for i, f := range svc.Features {
						labels[i] = f.Name
						viewIDs[i] = f.ViewID
					}
					return m, func() tea.Msg {
						return appmsg.RequestFeaturePickerMsg{
							Service: svc.Name,
							Labels:  labels,
							ViewIDs: viewIDs,
						}
					}
				}
			}
			return m, nil
		}
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
			if m.nav.Depth() > 1 {
				m.pendingTheme = msg.Value
				m.confirm.ShowQuick("Switching themes resets the current view. Continue?", "theme_switch")
				return m, nil
			}
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
		// A view that takes text input, such as a filter or a form, gets every
		// key except ctrl+c. Typed letters such as q, W, or tab must not run
		// app-wide keys.
		if m.capturingInput() {
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			cmd := m.nav.UpdateCurrent(teaMsg)
			return m, cmd
		}

		// Tab toggles focus between main view and panel
		if m.panelOpen && key.Matches(msg, m.keys.TabToggle.Binding) {
			m.panelFocused = !m.panelFocused
			return m, nil
		}

		// Global keys — work regardless of focus state
		switch {
		case msg.String() == "ctrl+c":
			return m, tea.Quit
		case key.Matches(msg, m.keys.Quit.Binding):
			if m.panelOpen {
				m.clearStashedPanel()
				m.closePanel()
				return m, nil
			}
			if m.nav.Depth() <= 1 {
				return m, tea.Quit
			}
			m.nav.Pop()
			m.trimCrossNavHistory()
			m.resizeViews()
			// Restart animation tick when returning to home screen
			if m.nav.Depth() == 1 {
				return m, m.nav.InitCurrent()
			}
			return m, nil
		case key.Matches(msg, m.keys.ThemePicker.Binding):
			m.showThemePicker()
			return m, nil
		case key.Matches(msg, m.keys.ProfilePicker.Binding):
			return m, m.showProfilePicker()
		case key.Matches(msg, m.keys.RegionPicker.Binding):
			m.showRegionPicker()
			return m, nil
		case key.Matches(msg, m.keys.ModePicker.Binding):
			m.showModePicker()
			return m, nil
		case key.Matches(msg, m.keys.EventLog.Binding):
			eventlog.Debug(eventlog.CatUI, "Event log opened")
			cmd := m.pushView(views.NewEventLog())
			return m, cmd
		case key.Matches(msg, m.keys.CommandBar.Binding):
			m.commandBar.Show(registry.CommandBarEntries(), m.width)
			return m, nil
		case key.Matches(msg, m.keys.ServicePicker.Binding):
			if m.nav.Depth() == 1 {
				m.showServicePicker()
				return m, nil
			}
		case key.Matches(msg, m.keys.Help.Binding):
			hints := m.collectAllKeyHints()
			m.help.Show(hints, m.width, m.height)
			return m, nil
		case key.Matches(msg, m.keys.CrossNavBack.Binding):
			if len(m.crossNavHistory) > 0 {
				m.popCrossNavBack()
				return m, nil
			}
			return m, nil
		}

		// Panel-focused key handling
		if m.panelOpen && m.panelFocused && m.panel != nil {
			switch {
			case key.Matches(msg, m.keys.PanelClose.Binding):
				m.clearStashedPanel()
				m.closePanel()
				return m, nil
			case key.Matches(msg, m.keys.PanelEditor.Binding):
				return m, m.panel.OpenInEditorCmd()
			case key.Matches(msg, m.keys.PanelGrow.Binding):
				m.panelWidthOverride = m.panelWidth() + panelResizeStep
				m.resizeViews()
				return m, nil
			case key.Matches(msg, m.keys.PanelShrink.Binding):
				m.panelWidthOverride = m.panelWidth() - panelResizeStep
				m.resizeViews()
				return m, nil
			case key.Matches(msg, m.keys.PanelReset.Binding):
				m.panelWidthOverride = 0
				m.resizeViews()
				return m, nil
			default:
				// Forward scroll/visual/yank keys to panel
				updated, cmd := m.panel.Update(msg)
				m.panel = &updated
				return m, cmd
			}
		}

		// Context-specific esc when panel is not focused
		if msg.String() == "esc" {
			// Delegate to child — it may need to dismiss a filter
			cmd := m.nav.UpdateCurrent(teaMsg)
			return m, cmd
		}

		// Enter on home screen opens the service picker
		if msg.String() == "enter" && m.nav.Depth() == 1 {
			m.showServicePicker()
			return m, nil
		}

		// ReadOnly mode blocks the ReadWrite keys of the view here, before the
		// view gets the key. The guarded AWS services check again when the
		// action runs.
		if ui.IsReadOnly() && readWriteKey(m.nav.Current().KeyMap(), msg) {
			_, cmd := m.toasts.Add("ReadOnly mode — press W to switch", ui.ToastError, 0)
			return m, cmd
		}
	}

	cmd := m.nav.UpdateCurrent(teaMsg)
	return m, cmd
}

// capturingInput reports whether the view on top takes text input.
func (m Model) capturingInput() bool {
	c, ok := m.nav.Current().(nav.InputCapturer)
	return ok && c.CapturingInput()
}

// readWriteKey reports whether k is a key of a ReadWrite binding in hints. It
// compares the keys and ignores the enabled flag, because ApplyModeAll
// disables the ReadWrite hints in ReadOnly mode.
func readWriteKey(hints []ui.HintBinding, k tea.KeyPressMsg) bool {
	s := k.String()
	for _, h := range hints {
		if h.Mode == ui.ModeReadWrite && slices.Contains(h.Keys(), s) {
			return true
		}
	}
	return false
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
		return m, m.resetNav()
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
		return m, m.showProfilePicker()
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

// activeRegion returns the region the AWS client actually uses. The config
// value is only an override and is empty when the profile decides the region.
func (m Model) activeRegion() string {
	if m.svc.Client != nil {
		return m.svc.Client.Region
	}
	if m.config.AWS.Region != "" {
		return m.config.AWS.Region
	}
	return aws.FallbackRegion
}

// --- Side panel helpers ---

func (m Model) canShowPanel() bool {
	return ui.GetWidthTier(m.width) == ui.TierWide
}

func (m Model) panelWidth() int {
	if m.panelWidthOverride > 0 {
		maxW := m.width - panelMinMainWidth - 3 // gap + main borders
		w := m.panelWidthOverride
		if w < panelMinWidth {
			w = panelMinWidth
		}
		if w > maxW {
			w = maxW
		}
		return w
	}
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
	m.panelTitle = title
	m.resizeViews()
	eventlog.Infof(eventlog.CatUI, "Panel opened: %s (%d tabs)", title, len(tabs))
}

func (m *Model) closePanel() {
	m.panel = nil
	m.panelOpen = false
	m.panelFocused = false
	m.panelTitle = ""
	m.resizeViews()
}

// savePanelState extracts the current panel's title and tabs for saving.
func (m *Model) savePanelState() (string, []appmsg.TabContent) {
	if m.panel == nil {
		return "", nil
	}
	tabData := m.panel.TabData()
	tabs := make([]appmsg.TabContent, len(tabData))
	for i, t := range tabData {
		var links []appmsg.TabLink
		for line, link := range t.Links {
			links = append(links, appmsg.TabLink{
				Line:   line,
				ViewID: link.ViewID,
				Params: link.Params,
			})
		}
		tabs[i] = appmsg.TabContent{
			Title:   t.Title,
			Content: t.Content,
			Format:  t.Format,
			Links:   links,
		}
	}
	return m.panelTitle, tabs
}

// stashPanel saves the current panel state for resize restoration.
func (m *Model) stashPanel() {
	m.stashedPanelTitle, m.stashedPanelTabs = m.savePanelState()
}

// clearStashedPanel discards any stashed panel state.
func (m *Model) clearStashedPanel() {
	m.stashedPanelTitle = ""
	m.stashedPanelTabs = nil
}

// pushCrossNavHistory saves the current panel state for later restoration.
func (m *Model) pushCrossNavHistory() {
	title, tabs := m.savePanelState()
	if title == "" {
		return
	}
	entry := crossNavEntry{
		navDepth:   m.nav.Depth(),
		panelTitle: title,
		panelTabs:  tabs,
	}
	m.crossNavHistory = append(m.crossNavHistory, entry)
	if len(m.crossNavHistory) > crossNavHistoryMax {
		m.crossNavHistory = m.crossNavHistory[1:]
	}
}

// popCrossNavBack pops the cross-nav history, goes back to the view that
// showed the panel, and restores the panel. The user can open more views
// after a link, so it pops to the saved depth, not one level.
func (m *Model) popCrossNavBack() {
	n := len(m.crossNavHistory)
	entry := m.crossNavHistory[n-1]
	m.crossNavHistory = m.crossNavHistory[:n-1]

	if m.panelOpen {
		m.closePanel()
	}
	m.nav.PopTo(entry.navDepth)
	m.resizeViews()

	m.openTabbedPanel(entry.panelTitle, entry.panelTabs)
}

// trimCrossNavHistory removes stale entries whose source depth is no longer on the stack.
func (m *Model) trimCrossNavHistory() {
	depth := m.nav.Depth()
	n := 0
	for _, e := range m.crossNavHistory {
		if e.navDepth < depth {
			m.crossNavHistory[n] = e
			n++
		}
	}
	m.crossNavHistory = m.crossNavHistory[:n]
}

// chromeHeight returns the vertical lines consumed by fixed layout chrome:
// header (2: title bar + gradient) + status bar (1) + content border (2: top + bottom)
// + footer (0 or 2: gradient line + text when present).
func (m Model) chromeHeight() int {
	h := 5 // header (2) + status bar (1) + content border (2)
	if m.nav.Current().Footer() != "" {
		h += 2 // gradient line + footer text
	}
	return h
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

func (m *Model) showServicePicker() {
	var options []ui.PickerOption
	for _, svc := range registry.Services {
		label := svc.Icon.Icon() + "  " + svc.Name
		options = append(options, ui.PickerOption{Label: label, Value: svc.Name})
	}
	m.picker.Show("service", "Select Service", options, 0)
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

func (m *Model) showProfilePicker() tea.Cmd {
	profiles := aws.ListProfiles()
	if len(profiles) == 0 {
		_, cmd := m.toasts.Add("No profiles found in ~/.aws/config", ui.ToastError, 0)
		return cmd
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
	return nil
}

func (m Model) applyProfile(profile string) (Model, tea.Cmd) {
	eventlog.Infof(eventlog.CatConfig, "Profile changed → %s", profile)
	return m.switchSession(profile, m.config.AWS.Region, "Profile", profile)
}

// switchSession builds the AWS services for profile and region. kind and
// value name the change for the toast, for example "Region" and "eu-west-1".
//
// If the build fails, nothing changes: the current session, the views, and
// the configuration stay, and the user sees the error. If it succeeds, the
// app resets the navigation and opens the service view that was on top
// again, now with the new session.
func (m Model) switchSession(profile, region, kind, value string) (Model, tea.Cmd) {
	svc, err := aws.NewServices(profile, region, m.config.AWS.Endpoint, ui.IsReadOnly)
	if err != nil {
		eventlog.Errorf(eventlog.CatAWS, "Cannot switch %s to %s: %v", strings.ToLower(kind), value, err)
		_, cmd := m.toasts.Add(fmt.Sprintf("Cannot switch %s to %s: %v", strings.ToLower(kind), value, err), ui.ToastError, 0)
		return m, cmd
	}
	m.config.AWS.Profile = profile
	m.config.AWS.Region = region
	m.svc, m.svcErr = svc, nil

	returnTo := m.topLevelViewID()
	cmds := []tea.Cmd{m.resetNav()}
	_, toastCmd := m.toasts.Add(kind+": "+value, ui.ToastSuccess, 0)
	cmds = append(cmds, toastCmd)
	if returnTo != "" {
		cmds = append(cmds, func() tea.Msg {
			return appmsg.NavigateMsg{ViewID: returnTo}
		})
	}
	return m, tea.Batch(cmds...)
}

// resetNav replaces the navigation with a new home view. It closes the panel
// and clears the stashed panel, the cross-navigation history, and the error,
// so nothing of the old views comes back. It returns the commands that start
// the home animation and size the new view.
func (m *Model) resetNav() tea.Cmd {
	m.clearStashedPanel()
	m.closePanel()
	m.crossNavHistory = nil
	m.err, m.errView = "", nil
	m.nav = nav.New(views.NewHome())
	if m.svcErr != nil {
		m.err, m.errView = noSessionText(m.svcErr), m.nav.Current()
	}
	return tea.Batch(m.nav.InitCurrent(), m.resizeCmd())
}

func (m *Model) showModePicker() {
	options := []ui.PickerOption{
		{Label: "ReadOnly    Browse only, no mutations", Value: "readonly"},
		{Label: "ReadWrite   Allow create/delete/copy/move", Value: "readwrite"},
	}
	currentIdx := 0
	if !ui.IsReadOnly() {
		currentIdx = 1
	}
	m.picker.Show("mode", "Access Mode", options, currentIdx)
}

func (m Model) applyMode(mode string) (Model, tea.Cmd) {
	switch mode {
	case "readonly":
		ui.SetReadOnly(true)
		eventlog.Infof(eventlog.CatApp, "Mode changed → ReadOnly")
		_, cmd := m.toasts.Add("Mode: ReadOnly", ui.ToastInfo, 0)
		return m, cmd
	case "readwrite":
		ui.SetReadOnly(false)
		eventlog.Warnf(eventlog.CatApp, "Mode changed → ReadWrite")
		_, cmd := m.toasts.Add("Mode: ReadWrite", ui.ToastSuccess, 0)
		return m, cmd
	}
	return m, nil
}

func (m *Model) showRegionPicker() {
	current := m.activeRegion()

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
	return m.switchSession(m.config.AWS.Profile, region, "Region", region)
}

func (m Model) applyTheme(name string) (Model, tea.Cmd) {
	eventlog.Infof(eventlog.CatUI, "Theme changed → %s", name)
	if t, ok := ui.Themes[name]; ok {
		ui.ActiveTheme = t
		ui.RebuildStyles()
	}

	// The views keep theme colors in their rows (L17), so the app builds
	// the navigation and the overlays again with the new theme.
	cmd := m.resetNav()
	m.confirm = ui.NewConfirm()
	m.picker = ui.NewPicker()
	_, toastCmd := m.toasts.Add("Theme: "+ui.ActiveTheme.Name, ui.ToastSuccess, 0)
	return m, tea.Batch(cmd, toastCmd)
}

func (m Model) View() tea.View {
	t := ui.ActiveTheme

	mode := "RO"
	if !ui.IsReadOnly() {
		mode = "RW"
	}
	crumbs := m.nav.Breadcrumbs()
	if m.panelOpen && m.panelTitle != "" {
		crumbs = append(crumbs, m.panelTitle)
	}
	var crossNavLabel string
	if n := len(m.crossNavHistory); n > 0 {
		crossNavLabel = m.crossNavHistory[n-1].panelTitle
	}
	header := ui.RenderHeader(ui.HeaderData{
		Profile:           m.config.AWS.Profile,
		Region:            m.activeRegion(),
		Mode:              mode,
		Breadcrumbs:       crumbs,
		CrossNavBackLabel: crossNavLabel,
		Width:             m.width,
	})

	var statusBar string
	if m.commandBar.Visible() {
		statusBar = m.commandBar.ViewInput(m.width)
	} else {
		keys := m.currentKeyHints()
		statusBar = ui.RenderStatusBar(ui.StatusBarData{
			Keys:  keys,
			Error: m.visibleErr(),
			Width: m.width,
		})
	}

	// Build footer line (gradient + resource count)
	footerText := m.nav.Current().Footer()
	var footer string
	if footerText != "" {
		gradLine := ui.RenderGradientLine(m.width)
		footer = gradLine + "\n" + ui.S.StatusDesc.Render(" "+footerText)
	}

	headerHeight := lipgloss.Height(header)
	statusHeight := lipgloss.Height(statusBar)
	footerHeight := lipgloss.Height(footer)
	contentHeight := m.height - headerHeight - statusHeight - footerHeight

	// Ensure minimum table height by hiding chrome
	minContent := ui.MinTableRows + 2 // +2 for border
	if contentHeight < minContent && headerHeight > 0 {
		header = ""
		contentHeight = m.height - statusHeight - footerHeight
	}
	if contentHeight < minContent && statusHeight > 0 {
		statusBar = ""
		contentHeight = m.height - footerHeight
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

	body := lipgloss.JoinVertical(lipgloss.Left, header, contentStr, footer, statusBar)

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
		y := m.height - toastH - footerHeight - statusHeight - 1
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
	// The view on top can be a detail view such as s3_objects, or a view
	// without AWS data such as the event log. The nearest service view of
	// the registry under it is the one to open again.
	views := m.nav.Views()
	for i := len(views) - 1; i >= 0; i-- {
		id, _, _ := strings.Cut(views[i].ID(), ":")
		if registry.IsServiceView(id) {
			return id
		}
	}
	return ""
}

// needsSession reports whether the view needs an AWS session.
func needsSession(viewID string) bool {
	switch viewID {
	case "eventlog", "content":
		return false
	}
	return true
}

func (m Model) resolveView(n appmsg.NavigateMsg) nav.View {
	switch n.ViewID {
	case "ec2_list":
		return views.NewEC2List(m.svc.EC2, m.svc.Client)
	case "ami_list":
		return views.NewAMIList(m.svc.EC2)
	case "sg_list":
		return views.NewSGList(m.svc.EC2)
	case "vpc_list":
		return views.NewVPCList(m.svc.EC2)
	case "subnet_list":
		return views.NewSubnetList(m.svc.EC2, n.Params["vpc_id"])
	case "s3_list":
		return views.NewS3List(m.svc.S3, m.activeRegion())
	case "s3_objects":
		return views.NewS3Objects(m.svc.S3, n.Params["bucket"], n.Params["prefix"])
	case "s3_versions":
		return views.NewS3Versions(m.svc.S3, n.Params["bucket"], n.Params["key"])
	case "sqs_queues":
		return views.NewSQSQueues(m.svc.SQS)
	case "sqs_messages":
		return views.NewSQSMessages(m.svc.SQS, n.Params["queue_url"], n.Params["queue_name"], n.Params["fifo"] == "true")
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

func (m Model) currentKeyHints() []ui.HintBinding {
	if m.panelOpen && m.panelFocused {
		var hints []ui.HintBinding
		if m.panel != nil && m.panel.TabCount() > 1 {
			hints = append(hints, ui.NewHintBinding(
				nil, fmt.Sprintf("1-%d", m.panel.TabCount()), "switch tab",
			))
		}
		cvk := ui.DefaultContentViewKeyMap()
		hints = append(hints,
			ui.HintBinding{Binding: cvk.CursorDown},
			ui.HintBinding{Binding: cvk.GotoTop},
			ui.HintBinding{Binding: cvk.ScrollLeft},
			ui.HintBinding{Binding: cvk.VisualToggle},
			ui.HintBinding{Binding: cvk.Yank},
			ui.HintBinding{Binding: cvk.WrapToggle},
			m.keys.PanelEditor,
			ui.NewHintBinding([]string{"<", ">", "="}, "</>/=", "resize/reset"),
			ui.NewHintBinding([]string{"tab"}, "tab", "focus main"),
			m.keys.PanelClose,
		)
		return hints
	}

	// View-specific hints first
	hints := m.nav.Current().KeyMap()

	// While the view takes text input, app-wide keys do not work, so their
	// hints are not shown.
	if m.capturingInput() {
		return hints
	}

	if m.panelOpen {
		hints = append(hints, ui.NewHintBinding([]string{"tab"}, "tab", "focus panel"))
	}

	// Service picker hints on home screen
	if m.nav.Depth() == 1 {
		hints = append(hints,
			ui.NewHintBinding([]string{"enter", "s"}, "enter/s", "services"),
		)
	}

	// Global hints
	if len(m.crossNavHistory) > 0 {
		hints = append(hints, m.keys.CrossNavBack)
	}
	if m.nav.Depth() > 1 {
		hints = append(hints, ui.NewHintBinding([]string{"esc", "q"}, "esc/q", "back"))
	} else {
		hints = append(hints, m.keys.Quit)
	}
	hints = append(hints,
		m.keys.ModePicker,
		m.keys.EventLog,
		m.keys.ProfilePicker,
		m.keys.RegionPicker,
		m.keys.ThemePicker,
		m.keys.CommandBar,
		m.keys.Help,
	)
	return hints
}

// collectAllKeyHints returns all keybindings with categories set for the help overlay.
func (m Model) collectAllKeyHints() []ui.HintBinding {
	var hints []ui.HintBinding

	// View-specific hints (Category empty — rendered as "Current View")
	hints = append(hints, m.nav.Current().KeyMap()...)
	if m.nav.Depth() == 1 {
		hints = append(hints, ui.NewHintBinding([]string{"enter", "s"}, "enter/s", "services"))
	}

	// Navigation hints
	if m.panelOpen {
		hints = append(hints, m.keys.TabToggle.WithCategory("Navigation"))
	}
	if len(m.crossNavHistory) > 0 {
		hints = append(hints, m.keys.CrossNavBack.WithCategory("Navigation"))
	}
	if m.nav.Depth() > 1 {
		hints = append(hints, ui.NewHintBinding([]string{"esc", "q"}, "esc/q", "go back").WithCategory("Navigation"))
	}

	// Panel hints (when panel is open)
	if m.panelOpen {
		cvk := ui.DefaultContentViewKeyMap()
		panelHints := []ui.HintBinding{
			{Binding: cvk.CursorDown, Category: "Panel"},
			{Binding: cvk.GotoTop, Category: "Panel"},
			{Binding: cvk.ScrollLeft, Category: "Panel"},
			{Binding: cvk.VisualToggle, Category: "Panel"},
			{Binding: cvk.Yank, Category: "Panel"},
			{Binding: cvk.WrapToggle, Category: "Panel"},
			m.keys.PanelEditor.WithCategory("Panel"),
			ui.NewHintBinding([]string{"<", ">", "="}, "</>/=", "resize/reset panel").WithCategory("Panel"),
			m.keys.PanelClose.WithCategory("Panel"),
		}
		hints = append(hints, panelHints...)
	}

	// Global hints
	globalHints := []ui.HintBinding{
		m.keys.ModePicker.WithCategory("Global"),
		m.keys.EventLog.WithCategory("Global"),
		m.keys.ProfilePicker.WithCategory("Global"),
		m.keys.RegionPicker.WithCategory("Global"),
		m.keys.ThemePicker.WithCategory("Global"),
		m.keys.CommandBar.WithCategory("Global"),
		m.keys.Help.WithCategory("Global"),
	}
	if m.nav.Depth() <= 1 {
		globalHints = append(globalHints, m.keys.Quit.WithCategory("Global"))
	}
	hints = append(hints, globalHints...)

	return hints
}
