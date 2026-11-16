package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/config"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	appmsg "github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
	"github.com/juthrbog/lazycloud/internal/ops"
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
	overlays   overlayStack // the open overlays, bottom first
	forms      []formRoute  // the open forms and the views that asked for them
	toasts     ui.ToastManager
	width      int
	height     int
	err        string
	errView    nav.View // view that err belongs to; err shows only while it is on top
	isDark     bool
	// autoTheme is true until the terminal reports its background, when
	// the theme setting is auto. Then the app picks the dark or light theme.
	autoTheme bool

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

	// ops holds the long operations that run in the background (see
	// appmsg.StartOpMsg). The pointer is shared by every copy of Model.
	ops *ops.Registry
	// opsTicking is true while a tick redraws the operations line.
	opsTicking bool
	// opsShown is true while the layout has the operations line. It
	// changes only in syncOpsLine, which also resizes the views.
	opsShown bool
}

// sessionChange is a profile or region switch. kind and value name the
// change for the toast, for example "Region" and "eu-west-1".
type sessionChange struct {
	profile, region, kind, value string
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
		autoTheme:  strings.EqualFold(cfg.Display.Theme, ui.AutoTheme),
		ops:        ops.NewRegistry(),
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

// Ops returns the registry of background operations. main cancels them and
// waits for them after the program ends, so a cancelled download can remove
// its partial file.
func (m Model) Ops() *ops.Registry {
	return m.ops
}

// sessionLabel names the current session, for example "default/us-east-1".
func (m Model) sessionLabel() string {
	profile := m.config.AWS.Profile
	if profile == "" {
		profile = "default"
	}
	return profile + "/" + m.activeRegion()
}

// noSessionText explains why the app has no AWS session and how to fix it.
func noSessionText(err error) string {
	return "No AWS session (press P for a profile or R for a region): " + aws.ErrorText(err)
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
	// The result of an operation comes after the operation ended, so the
	// operations line can go away now.
	m.syncOpsLine()
	switch inner := r.Msg.(type) {
	case appmsg.ToastMsg:
		// Feedback is useful even from a view the user has left.
		return m.Update(inner)

	case appmsg.StartOpMsg:
		// The user started the operation, so it runs also when its view
		// is gone by now.
		cmd := m.startOp(r.Origin, inner)
		m.syncOpsLine()
		if !m.opsTicking {
			m.opsTicking = true
			cmd = tea.Batch(cmd, opsTick())
		}
		return m, cmd

	case appmsg.RequestConfirmMsg:
		if !m.nav.Contains(r.Origin) {
			eventlog.Debugf(eventlog.CatNav, "Dropped %T from %s: view no longer exists", inner, r.Origin.ID())
			return m, nil
		}
		if !m.nav.IsCurrent(r.Origin) {
			cmd := m.refuseBelowTop(r.Origin, inner.Tag, inner.Message, appmsg.ConfirmResultMsg{Confirmed: false, Action: inner.Action})
			return m, cmd
		}
		return m.askConfirm(inner, r.Origin)

	case appmsg.RequestActionPickerMsg, appmsg.RequestSortPickerMsg:
		// A picker acts on what the user sees, like navigation below. Its
		// reply goes to the view that asked.
		if !m.nav.Contains(r.Origin) {
			eventlog.Debugf(eventlog.CatNav, "Dropped %T from %s: view no longer exists", inner, r.Origin.ID())
			return m, nil
		}
		if req, ok := inner.(appmsg.RequestActionPickerMsg); ok {
			if !m.nav.IsCurrent(r.Origin) {
				cmd := m.refuseBelowTop(r.Origin, req.Tag, actionTitle(req), pickDecline("action"))
				return m, cmd
			}
			return m.showActionPicker(req, r.Origin)
		}
		req := inner.(appmsg.RequestSortPickerMsg)
		if !m.nav.IsCurrent(r.Origin) {
			cmd := m.refuseBelowTop(r.Origin, req.Tag, sortTitle, pickDecline("sort"))
			return m, cmd
		}
		return m.showSortPicker(req, r.Origin)

	case appmsg.RequestFormDiscardConfirmMsg:
		if !m.nav.Contains(r.Origin) {
			eventlog.Debugf(eventlog.CatNav, "Dropped %T from %s: view no longer exists", inner, r.Origin.ID())
			return m, nil
		}
		if !m.nav.IsCurrent(r.Origin) {
			cmd := m.refuseBelowTop(r.Origin, nil, discardText, appmsg.ConfirmResultMsg{Confirmed: false, Action: appmsg.FormDiscardAction})
			return m, cmd
		}
		return m.askDiscard(r.Origin)

	case appmsg.RequestFormMsg, appmsg.FormResultMsg:
		// A form acts on what the user sees, like navigation below. The
		// app records the view that asked, and the result goes to it.
		if !m.nav.IsCurrent(r.Origin) {
			eventlog.Debugf(eventlog.CatNav, "Dropped %T from %s: view is not on top", inner, r.Origin.ID())
			return m, nil
		}
		switch inner := inner.(type) {
		case appmsg.RequestFormMsg:
			return m.openForm(inner, r.Origin)
		case appmsg.FormResultMsg:
			if fv, ok := r.Origin.(*ui.FormView); ok {
				return m.finishForm(fv, inner)
			}
		}
		eventlog.Debugf(eventlog.CatNav, "Dropped %T from %s: not a form", inner, r.Origin.ID())
		return m, nil

	case appmsg.NavigateMsg, appmsg.NavigateBackMsg, appmsg.TabbedContentMsg,
		appmsg.RequestFeaturePickerMsg, ui.ContentLinkActivatedMsg:
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
		// The user left the view or switched the session. The result of
		// a mutation or an operation still matters, so it shows as a toast.
		if o, ok := r.Msg.(appmsg.Outcome); ok {
			return m, outcomeCmd(r.Origin, o.Outcome())
		}
		eventlog.Debugf(eventlog.CatNav, "Dropped %T from %s: view no longer exists", r.Msg, r.Origin.ID())
		return m, nil
	}
	if _, ok := r.Msg.(appmsg.LoadedMsg); ok {
		// A good load replaces the error of an earlier load of the view.
		if m.errView == r.Origin {
			m.err, m.errView = "", nil
		}
		return m, nil
	}
	if e, ok := r.Msg.(appmsg.ErrorMsg); ok {
		if errors.Is(e.Err, context.Canceled) {
			// Only a view that left the stack is cancelled, so this
			// cannot reach a live view today. Keep it out of the status
			// bar if it ever does.
			eventlog.Debugf(eventlog.CatNav, "Dropped cancelled %s from %s", e.Context, r.Origin.ID())
			return m, nil
		}
		eventlog.Errorf(eventlog.CatApp, "%s: %v", e.Context, e.Err)
		m.err = errorText(e)
		m.errView = r.Origin
	}
	return m, m.nav.UpdateView(r.Origin, r.Msg)
}

// errorText is the status bar text of an error.
func errorText(e appmsg.ErrorMsg) string {
	return e.Context + ": " + aws.ErrorText(e.Err)
}

// outcomeCmd returns a command that shows the outcome of a result whose
// view is gone. It also writes the outcome to the event log, as the view
// would have done.
func outcomeCmd(origin nav.View, t appmsg.ToastMsg) tea.Cmd {
	switch ui.ToastLevel(t.Level) {
	case ui.ToastError:
		eventlog.Errorf(eventlog.CatApp, "%s: %s", origin.Title(), t.Text)
	default:
		eventlog.Infof(eventlog.CatApp, "%s: %s", origin.Title(), t.Text)
	}
	return func() tea.Msg { return t }
}

// startOp registers a background operation and returns the command that
// runs it. The registration happens here, on the event loop, so the quit
// and session-switch prompts see the operation at once.
func (m Model) startOp(origin nav.View, s appmsg.StartOpMsg) tea.Cmd {
	op := m.ops.Begin(s.Label, m.sessionLabel())
	eventlog.Infof(eventlog.CatAWS, "Started in the background: %s (%s)", s.Label, op.Session)
	run := s.Run
	return nav.Tag(origin, func() tea.Msg {
		defer op.Done()
		return run(op.Ctx)
	})
}

// askConfirm shows the confirm dialog that req asks for. Its reply goes to
// asker.
func (m Model) askConfirm(req appmsg.RequestConfirmMsg, asker nav.View) (Model, tea.Cmd) {
	if m.promptOpen() {
		cmd := m.refuseOpen(req.Message, replyRoute{replyTo: asker, tag: req.Tag}, appmsg.ConfirmResultMsg{Confirmed: false, Action: req.Action})
		return m, cmd
	}
	if req.Quick {
		m.confirm.ShowQuick(req.Message, req.Action)
	} else {
		m.confirm.Show(req.Message, req.Action, req.Phrase)
	}
	m.openOverlayFor(ovConfirm, asker, req.Tag)
	return m, nil
}

// sortTitle is the title of the sort picker.
const sortTitle = "Sort by Column"

// actionTitle is the title of the action picker that req asks for.
func actionTitle(req appmsg.RequestActionPickerMsg) string {
	if req.Title == "" {
		return "Action"
	}
	return req.Title
}

// pickDecline is the reply of a picker with id that was not shown.
func pickDecline(id string) appmsg.PickerResultMsg {
	return appmsg.PickerResultMsg{ID: id, Outcome: appmsg.PickCancelled}
}

// showActionPicker shows the action picker that req asks for. Its reply
// goes to asker.
func (m Model) showActionPicker(req appmsg.RequestActionPickerMsg, asker nav.View) (Model, tea.Cmd) {
	if m.promptOpen() {
		cmd := m.refuseOpen(actionTitle(req), replyRoute{replyTo: asker, tag: req.Tag}, pickDecline("action"))
		return m, cmd
	}
	options := make([]ui.PickerOption, 0, len(req.Options))
	for _, opt := range req.Options {
		options = append(options, ui.PickerOption{Label: opt, Value: opt})
	}
	m.picker.Show("action", actionTitle(req), options, 0)
	m.openOverlayFor(ovPicker, asker, req.Tag)
	return m, nil
}

// showSortPicker shows the sort picker that req asks for. Its reply goes
// to asker.
func (m Model) showSortPicker(req appmsg.RequestSortPickerMsg, asker nav.View) (Model, tea.Cmd) {
	if m.promptOpen() {
		cmd := m.refuseOpen(sortTitle, replyRoute{replyTo: asker, tag: req.Tag}, pickDecline("sort"))
		return m, cmd
	}
	options := make([]ui.PickerOption, 0, len(req.Columns))
	for _, col := range req.Columns {
		options = append(options, ui.PickerOption{Label: col, Value: col})
	}
	m.picker.Show("sort", sortTitle, options, max(req.CurrentCol, 0))
	m.picker.SetClearable(true) // ctrl+x removes the sort
	m.openOverlayFor(ovPicker, asker, req.Tag)
	return m, nil
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

	// The overlay on top of the stack gets each key press and each paste,
	// so a paste does not go into the view under a picker. Other messages,
	// such as page loads and spinner ticks, go on to the normal routing.
	switch input := teaMsg.(type) {
	case tea.KeyPressMsg, tea.PasteMsg:
		if k, isKey := input.(tea.KeyPressMsg); isKey && m.quitAsked() && k.String() == "ctrl+c" {
			// A second ctrl+c quits without the answer, also when a late
			// picker opened over the quit confirm.
			return m.quitNow()
		}
		m.pruneOverlays()
		if top, ok := m.overlays.top(); ok {
			return m.sendOverlayInput(top, input)
		}
	}

	switch msg := teaMsg.(type) {
	case nav.Routed:
		return m.handleRouted(msg)

	case overlayReplyMsg:
		return m.deliverReply(msg)

	case tea.BackgroundColorMsg:
		m.isDark = msg.IsDark()
		if !m.autoTheme {
			return m, nil
		}
		// The terminal answers once, at start. Pick the theme only then: a
		// switch later would rebuild the views that the user opened.
		m.autoTheme = false
		name := ui.AutoThemeName(m.isDark)
		if ui.Themes[name].Name == ui.ActiveTheme.Name {
			return m, nil
		}
		eventlog.Infof(eventlog.CatUI, "Theme auto → %s (dark background: %t)", name, m.isDark)
		return m.setTheme(name)

	case tea.ColorProfileMsg:
		// Bubble Tea sends this once at start, before any table exists.
		// NO_COLOR gives the ASCII profile.
		ui.SetNoColor(msg.Profile == colorprofile.NoTTY || msg.Profile == colorprofile.ASCII)
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeOverlays()
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
		m.dismissPanel()
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
		m.dismissPanel()
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
		if errors.Is(msg.Err, context.Canceled) {
			eventlog.Debugf(eventlog.CatNav, "Dropped cancelled %s", msg.Context)
			return m, nil
		}
		m.err = errorText(msg)
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

	case appmsg.ToastMsg:
		_, cmd := m.toasts.Add(msg.Text, ui.ToastLevel(msg.Level), 0)
		return m, cmd

	case ui.ToastDismissMsg:
		m.toasts.Dismiss(msg.ID)
		return m, nil

	case appmsg.RequestFormMsg, appmsg.FormResultMsg, appmsg.RequestFormDiscardConfirmMsg,
		appmsg.RequestConfirmMsg, appmsg.RequestActionPickerMsg, appmsg.RequestSortPickerMsg,
		appmsg.ConfirmResultMsg, appmsg.PickerResultMsg:
		// A view sends its requests through the navigator, so they arrive
		// as nav.Routed with the view that asked. A reply goes out wrapped
		// with its route. Without a view to answer, the app drops them.
		eventlog.Debugf(eventlog.CatNav, "Dropped %T: no origin", msg)
		return m, nil

	case appmsg.RequestFeaturePickerMsg:
		cmd := m.showFeaturePicker(msg)
		return m, cmd

	case opsTickMsg:
		// The tick stops when no operation runs, and the next
		// operation starts it again.
		m.syncOpsLine()
		if m.ops.Count() == 0 {
			m.opsTicking = false
			return m, nil
		}
		return m, opsTick()

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
				return m.requestQuit()
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
			return m.requestQuit()
		case key.Matches(msg, m.keys.CancelOp.Binding):
			return m.showCancelPicker()
		case key.Matches(msg, m.keys.Quit.Binding):
			if m.panelOpen {
				m.dismissPanel()
				return m, nil
			}
			if m.nav.Depth() <= 1 {
				return m.requestQuit()
			}
			m.dismissPanel()
			m.nav.Pop()
			m.trimCrossNavHistory()
			m.resizeViews()
			// Restart animation tick when returning to home screen
			if m.nav.Depth() == 1 {
				return m, m.nav.InitCurrent()
			}
			return m, nil
		case key.Matches(msg, m.keys.ThemePicker.Binding):
			cmd := m.showThemePicker()
			return m, cmd
		case key.Matches(msg, m.keys.ProfilePicker.Binding):
			cmd := m.showProfilePicker()
			return m, cmd
		case key.Matches(msg, m.keys.RegionPicker.Binding):
			cmd := m.showRegionPicker()
			return m, cmd
		case key.Matches(msg, m.keys.ModePicker.Binding):
			cmd := m.showModePicker()
			return m, cmd
		case key.Matches(msg, m.keys.EventLog.Binding):
			eventlog.Debug(eventlog.CatUI, "Event log opened")
			m.dismissPanel()
			cmd := m.pushView(views.NewEventLog())
			return m, cmd
		case key.Matches(msg, m.keys.CommandBar.Binding):
			m.commandBar.Show(registry.CommandBarEntries(), m.width)
			m.openOverlay(ovCommandBar)
			return m, nil
		case key.Matches(msg, m.keys.ServicePicker.Binding):
			if m.nav.Depth() == 1 {
				cmd := m.showServicePicker()
				return m, cmd
			}
		case key.Matches(msg, m.keys.Help.Binding):
			hints := m.collectAllKeyHints()
			m.help.Show(hints, m.width, m.height)
			m.openOverlay(ovHelp)
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
				m.dismissPanel()
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
			cmd := m.showServicePicker()
			return m, cmd
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
		return m.requestQuit()
	case "home":
		cmd := m.resetNav()
		return m, cmd
	case "mode":
		cmd := m.showModePicker()
		return m, cmd
	case "theme":
		cmd := m.showThemePicker()
		return m, cmd
	case "region":
		cmd := m.showRegionPicker()
		return m, cmd
	case "profile":
		cmd := m.showProfilePicker()
		return m, cmd
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

// pushView pushes a view onto the navigator and sends it the size that
// resizeViews would give it (see contentSize).
func (m *Model) pushView(v nav.View) tea.Cmd {
	initCmd := m.nav.Push(v)
	if m.width > 0 && m.height > 0 {
		w, h := m.contentSize()
		sizeCmd := m.nav.UpdateCurrent(tea.WindowSizeMsg{Width: w, Height: h})
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
		return min(max(m.panelWidthOverride, panelMinWidth), maxW)
	}
	return min(max(m.width*40/100, panelMinWidth), panelMaxWidth)
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

// dismissPanel closes the panel for good: it closes an open panel and drops a
// stashed one. Use it when the panel no longer belongs on screen. A stashed
// panel has panelOpen false, so do not guard this call with panelOpen.
func (m *Model) dismissPanel() {
	m.clearStashedPanel()
	if m.panelOpen {
		m.closePanel()
	}
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

	m.dismissPanel()
	m.nav.PopTo(entry.navDepth)
	m.resizeViews()

	if !m.canShowPanel() {
		// The terminal got narrow after the link. The panel opens when the
		// terminal is wide again.
		m.stashedPanelTitle, m.stashedPanelTabs = entry.panelTitle, entry.panelTabs
		return
	}
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
	if m.opsShown {
		h++ // operations line
	}
	return h
}

// contentSize returns the size of the view on top: the window without the
// header, the status bar, the border, the footer of the view, and the
// operations line. When the side panel is open, the view gets the width
// next to it. pushView and resizeViews both use it, so a view that opens
// gets the same size as after a resize.
func (m Model) contentSize() (width, height int) {
	height = m.height - m.chromeHeight()
	width = m.width - 2 // the borders of the main box
	if m.panelOpen && m.panel != nil {
		width = m.width - m.panelWidth() - 3 // panel, gap (1), main borders (2)
	}
	return width, height
}

func (m *Model) resizeViews() {
	if m.width == 0 || m.height == 0 {
		return
	}
	w, h := m.contentSize()
	m.nav.UpdateCurrent(tea.WindowSizeMsg{Width: w, Height: h})
	if m.panelOpen && m.panel != nil {
		m.panel.SetSize(m.panelWidth()-2, h)
	}
}

func (m *Model) showThemePicker() tea.Cmd {
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
	return m.pick("Select Theme", options, currentIdx, onPicked(func(m Model, name string) (Model, tea.Cmd) {
		if m.nav.Depth() == 1 {
			return m.applyTheme(name)
		}
		// Below the home view, ask first: the switch resets the views.
		cmd := m.askQuick("Switching themes resets the current view. Continue?",
			func(m Model) (Model, tea.Cmd) { return m.applyTheme(name) }, nil)
		return m, cmd
	}))
}

func (m *Model) showServicePicker() tea.Cmd {
	var options []ui.PickerOption
	for _, svc := range registry.Services {
		label := svc.Icon.Icon() + "  " + svc.Name
		options = append(options, ui.PickerOption{Label: label, Value: svc.Name})
	}
	return m.pick("Select Service", options, 0, onPicked(Model.openService))
}

// openService opens the service that the user picked. A service with more
// than one feature asks which feature to open.
func (m Model) openService(name string) (Model, tea.Cmd) {
	for _, svc := range registry.Services {
		if svc.Name != name {
			continue
		}
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
			return appmsg.RequestFeaturePickerMsg{Service: svc.Name, Labels: labels, ViewIDs: viewIDs}
		}
	}
	return m, nil
}

// showFeaturePicker asks which feature of a service to open.
func (m *Model) showFeaturePicker(req appmsg.RequestFeaturePickerMsg) tea.Cmd {
	options := make([]ui.PickerOption, 0, len(req.Labels))
	for i, label := range req.Labels {
		options = append(options, ui.PickerOption{Label: label, Value: req.ViewIDs[i]})
	}
	return m.pick(req.Service, options, 0, onPicked(func(m Model, viewID string) (Model, tea.Cmd) {
		return m, func() tea.Msg { return appmsg.NavigateMsg{ViewID: viewID} }
	}))
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
	return m.pick("Select Profile", options, currentIdx, onPicked(Model.applyProfile))
}

func (m Model) applyProfile(profile string) (Model, tea.Cmd) {
	return m.requestSessionSwitch(sessionChange{profile, m.config.AWS.Region, "Profile", profile})
}

// requestSessionSwitch switches the session at once when no operation runs.
// Otherwise it asks first: the operations keep running in the background
// with the old session, and the user should know that.
func (m Model) requestSessionSwitch(c sessionChange) (Model, tea.Cmd) {
	n := m.ops.Count()
	if n == 0 {
		return m.switchSession(c.profile, c.region, c.kind, c.value)
	}
	cmd := m.askQuick(fmt.Sprintf(
		"%s running on %s. %s in the background. Switch the %s to %s?",
		countOps(n), m.opsSessions(), keepWord(n), strings.ToLower(c.kind), c.value),
		func(m Model) (Model, tea.Cmd) { return m.switchSession(c.profile, c.region, c.kind, c.value) },
		func(m Model) (Model, tea.Cmd) {
			eventlog.Infof(eventlog.CatConfig, "%s switch to %s cancelled", c.kind, c.value)
			return m, nil
		})
	return m, cmd
}

// opsSessions names the sessions that the running operations use. After
// an earlier switch, they can differ from the current session.
func (m Model) opsSessions() string {
	var names []string
	for _, op := range m.ops.Running() {
		if !slices.Contains(names, op.Session) {
			names = append(names, op.Session)
		}
	}
	return strings.Join(names, ", ")
}

// requestQuit quits at once when no operation runs. Otherwise it asks
// first, because quitting cancels the operations.
func (m Model) requestQuit() (Model, tea.Cmd) {
	n := m.ops.Count()
	if n == 0 {
		return m.quitNow()
	}
	cmd := m.askQuit(fmt.Sprintf("%s running. Quit and cancel %s?", countOps(n), itOrThem(n)))
	return m, cmd
}

// quitNow cancels the operations and quits. main waits a short time for
// them to end, so a cancelled download can remove its partial file.
func (m Model) quitNow() (Model, tea.Cmd) {
	if n := m.ops.Count(); n > 0 {
		eventlog.Warnf(eventlog.CatApp, "Quit: cancelling %d operation(s)", n)
	}
	m.ops.CancelAll()
	return m, tea.Quit
}

func countOps(n int) string {
	if n == 1 {
		return "1 operation is"
	}
	return fmt.Sprintf("%d operations are", n)
}

func keepWord(n int) string {
	if n == 1 {
		return "It keeps running"
	}
	return "They keep running"
}

func itOrThem(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
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
		_, cmd := m.toasts.Add(fmt.Sprintf("Cannot switch %s to %s: %s", strings.ToLower(kind), value, aws.ErrorText(err)), ui.ToastError, 0)
		return m, cmd
	}
	eventlog.Infof(eventlog.CatConfig, "%s changed → %s", kind, value)
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
	m.dismissPanel()
	m.crossNavHistory = nil
	m.err, m.errView = "", nil
	// Stop the loads of the old views. Background operations keep running.
	m.nav.Close()
	m.nav = nav.New(views.NewHome())
	m.forms = nil
	if m.svcErr != nil {
		m.err, m.errView = noSessionText(m.svcErr), m.nav.Current()
	}
	return tea.Batch(m.nav.InitCurrent(), m.resizeCmd())
}

func (m *Model) showModePicker() tea.Cmd {
	options := []ui.PickerOption{
		{Label: "ReadOnly    Browse only, no mutations", Value: "readonly"},
		{Label: "ReadWrite   Allow create/delete/copy/move", Value: "readwrite"},
	}
	currentIdx := 0
	if !ui.IsReadOnly() {
		currentIdx = 1
	}
	return m.pick("Access Mode", options, currentIdx, onPicked(Model.applyMode))
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

func (m *Model) showRegionPicker() tea.Cmd {
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
	return m.pick("Select Region", options, currentIdx, onPicked(Model.applyRegion))
}

func (m Model) applyRegion(region string) (Model, tea.Cmd) {
	return m.requestSessionSwitch(sessionChange{m.config.AWS.Profile, region, "Region", region})
}

func (m Model) applyTheme(name string) (Model, tea.Cmd) {
	eventlog.Infof(eventlog.CatUI, "Theme changed → %s", name)
	m, cmd := m.setTheme(name)
	_, toastCmd := m.toasts.Add("Theme: "+ui.ActiveTheme.Name, ui.ToastSuccess, 0)
	return m, tea.Batch(cmd, toastCmd)
}

// setTheme makes a theme active. The views keep theme colors in their rows
// (L17), so it builds the navigation and the overlays again.
func (m Model) setTheme(name string) (Model, tea.Cmd) {
	if t, ok := ui.Themes[name]; ok {
		ui.ActiveTheme = t
		ui.RebuildStyles()
	}
	// Every open prompt gets a decline before its component goes away.
	declines := m.closePrompts()
	cmd := m.resetNav()
	m.confirm = ui.NewConfirm()
	m.picker = ui.NewPicker()
	m.pruneOverlays()
	m.resizeOverlays()
	return m, tea.Batch(declines, cmd)
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

	// The running operations show on their own line above the status bar.
	// The line follows opsShown, not the registry, so it matches the size
	// that the views got (see syncOpsLine).
	var opsLine string
	if m.opsShown {
		opsLine = renderOpsLine(m.ops.Running(), m.sessionLabel(), m.width, time.Now())
		if opsLine == "" {
			opsLine = ui.S.StatusBarBase.Width(m.width).Render("")
		}
	}

	headerHeight := lipgloss.Height(header)
	statusHeight := lipgloss.Height(statusBar)
	footerHeight := lipgloss.Height(footer)
	opsHeight := 0
	if opsLine != "" {
		opsHeight = lipgloss.Height(opsLine)
	}
	contentHeight := m.height - headerHeight - statusHeight - footerHeight - opsHeight

	// Ensure minimum table height by hiding chrome
	minContent := ui.MinTableRows + 2 // +2 for border
	if contentHeight < minContent && headerHeight > 0 {
		header = ""
		contentHeight = m.height - statusHeight - footerHeight - opsHeight
	}
	if contentHeight < minContent && opsHeight > 0 {
		opsLine = ""
		opsHeight = 0
		contentHeight = m.height - statusHeight - footerHeight
	}
	if contentHeight < minContent && statusHeight > 0 {
		statusBar = ""
		statusHeight = 0
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
		// ui.Dim keeps the text faint after each styled token, not only up
		// to the first reset.
		if m.panelFocused {
			mainContent = ui.Dim(mainContent)
		} else {
			panelContent = ui.Dim(panelContent)
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

	parts := []string{header, contentStr, footer}
	if opsLine != "" {
		parts = append(parts, opsLine)
	}
	body := lipgloss.JoinVertical(lipgloss.Left, append(parts, statusBar)...)

	body = m.drawOverlays(body)

	// Overlay toasts in bottom-right
	if m.toasts.HasActive() {
		toastView := m.toasts.View(m.width)
		toastW := lipgloss.Width(toastView)
		toastH := lipgloss.Height(toastView)
		x := m.width - toastW - 2
		y := m.height - toastH - footerHeight - statusHeight - opsHeight - 1
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

	// Views show AWS text through ui.Sanitize. This second guard removes what
	// the renderer would pass on to the terminal, if a view missed it.
	v := tea.NewView(ui.StripUnsafe(body))
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
	// without AWS data such as the event log. The root of the nearest view
	// that has one is the service view to open again.
	for _, view := range slices.Backward(m.nav.Views()) {
		id, _, _ := strings.Cut(view.ID(), ":")
		if root := routes[id].root; root != "" {
			return root
		}
	}
	return ""
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
		m.keys.CancelOp.WithCategory("Global"),
	}
	if m.nav.Depth() <= 1 {
		globalHints = append(globalHints, m.keys.Quit.WithCategory("Global"))
	}
	hints = append(hints, globalHints...)

	return hints
}
