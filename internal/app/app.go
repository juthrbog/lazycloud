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

// panelResizeStep is the number of columns per resize keystroke.
const panelResizeStep = 5

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
	// blurred is true while the terminal does not have the focus. The home
	// animation then pauses (P3).
	blurred bool

	keys ui.AppKeyMap

	// panels holds the side detail panel, the panel that a narrow terminal
	// hides, and the history for back traversal.
	panels panelController

	// ops holds the long operations that run in the background (see
	// appmsg.StartOpMsg). The pointer is shared by every copy of Model.
	ops *ops.Registry
	// opsTicking is true while a tick redraws the operations line.
	opsTicking bool
	// opsShown is true while the layout has the operations line. It
	// changes only in syncOpsLine, which also resizes the views.
	opsShown bool
	// match is the result of the rules of the config for the current
	// session: its lock and its label (see config.Rule).
	match config.Match
	// startWarnings are the problems at start that main prints on stderr,
	// for example a --read-write that a rule refused.
	startWarnings []string
	// resizeCmds holds the commands that views returned for a resize in
	// this update. Update sends them with its own command (L18).
	resizeCmds []tea.Cmd
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
	// The first session gets its own mode. main asks for ReadWrite with
	// --read-write through the mode that exists before any session.
	startRW := !ui.IsReadOnly()
	var mode *ui.SessionMode
	mode, m.match = m.newSessionMode(cfg.AWS.Profile)
	if startRW {
		if err := mode.SetReadWrite(); err != nil {
			w := fmt.Sprintf("--read-write ignored: profile %s is %s", displayProfile(cfg.AWS.Profile), err)
			eventlog.Warnf(eventlog.CatApp, "%s", w)
			m.startWarnings = append(m.startWarnings, w)
			m.toasts.Add(w, ui.ToastError, 0)
		}
	}
	ui.SetCurrentMode(mode)
	// Without a session, the app still starts. It shows the error on Home and
	// opens no AWS view until a profile or region switch succeeds.
	m.svc, m.svcErr = aws.NewServices(cfg.AWS.Profile, cfg.AWS.Region, cfg.AWS.Endpoint, mode.ReadOnly)
	if m.svcErr != nil {
		eventlog.Errorf(eventlog.CatAWS, "Failed to create AWS client: %v", m.svcErr)
		m.err = noSessionText(m.svcErr)
		m.errView = home
	}
	eventlog.Infof(eventlog.CatApp, "LazyCloud started (theme=%s, region=%s)", cfg.Display.Theme, m.activeRegion())
	return m
}

// StartWarnings returns the problems at start that main prints on stderr
// before the screen of the app covers it.
func (m Model) StartWarnings() []string {
	return m.startWarnings
}

// newSessionMode returns a ReadOnly mode for a new session of profile,
// locked when a rule of the config locks the profile, and the result of
// the rules.
func (m Model) newSessionMode(profile string) (*ui.SessionMode, config.Match) {
	mode := ui.NewSessionMode()
	// The account from the config lets a rule by account lock the session
	// before any call to AWS.
	match := m.config.MatchSession(profile, aws.ClassifyProfile(profile, time.Now()).Account)
	if match.Lock != "" {
		mode.Lock(match.Lock)
		eventlog.Infof(eventlog.CatConfig, "Profile %s is locked to ReadOnly by rule %s", displayProfile(profile), match.Lock)
	}
	return mode, match
}

// displayProfile names a profile for the user. The empty profile is the
// default profile.
func displayProfile(profile string) string {
	if profile == "" {
		return "default"
	}
	return profile
}

// Ops returns the registry of background operations. main cancels them and
// waits for them after the program ends, so a cancelled download can remove
// its partial file.
func (m Model) Ops() *ops.Registry {
	return m.ops
}

// sessionLabel names the current session, for example "default/us-east-1".
func (m Model) sessionLabel() string {
	return displayProfile(m.config.AWS.Profile) + "/" + m.activeRegion()
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
			cmd := m.refuseBelowTop(r.Origin, inner.Tag, inner.Message, appmsg.ConfirmResultMsg{Confirmed: false})
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
				cmd := m.refuseBelowTop(r.Origin, req.Tag, actionTitle(req), pickDecline())
				return m, cmd
			}
			return m.showActionPicker(req, r.Origin)
		}
		req := inner.(appmsg.RequestSortPickerMsg)
		if !m.nav.IsCurrent(r.Origin) {
			cmd := m.refuseBelowTop(r.Origin, req.Tag, sortTitle, pickDecline())
			return m, cmd
		}
		return m.showSortPicker(req, r.Origin)

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
		// Animate only while visible: the home view is on top and the
		// terminal has the focus. The app restarts the tick when the home
		// view is shown again or the terminal gets the focus back.
		if !m.nav.IsCurrent(r.Origin) || m.blurred {
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
		cmd := m.refuseOpen(req.Message, replyRoute{replyTo: asker, tag: req.Tag}, appmsg.ConfirmResultMsg{Confirmed: false})
		return m, cmd
	}
	if req.Quick {
		m.confirm.ShowQuick(req.Message)
	} else {
		m.confirm.Show(req.Message, req.Phrase)
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

// pickDecline is the reply of a picker that was not shown.
func pickDecline() appmsg.PickerResultMsg {
	return appmsg.PickerResultMsg{Outcome: appmsg.PickCancelled}
}

// showActionPicker shows the action picker that req asks for. Its reply
// goes to asker.
func (m Model) showActionPicker(req appmsg.RequestActionPickerMsg, asker nav.View) (Model, tea.Cmd) {
	if m.promptOpen() {
		cmd := m.refuseOpen(actionTitle(req), replyRoute{replyTo: asker, tag: req.Tag}, pickDecline())
		return m, cmd
	}
	options := make([]ui.PickerOption, 0, len(req.Options))
	for _, opt := range req.Options {
		options = append(options, ui.PickerOption{Label: opt, Value: opt})
	}
	m.picker.Show(actionTitle(req), options, 0)
	m.openOverlayFor(ovPicker, asker, req.Tag)
	return m, nil
}

// showSortPicker shows the sort picker that req asks for. Its reply goes
// to asker.
func (m Model) showSortPicker(req appmsg.RequestSortPickerMsg, asker nav.View) (Model, tea.Cmd) {
	if m.promptOpen() {
		cmd := m.refuseOpen(sortTitle, replyRoute{replyTo: asker, tag: req.Tag}, pickDecline())
		return m, cmd
	}
	options := make([]ui.PickerOption, 0, len(req.Columns))
	for _, col := range req.Columns {
		options = append(options, ui.PickerOption{Label: col, Value: col})
	}
	m.picker.Show(sortTitle, options, max(req.CurrentCol, 0))
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

// Update handles one message. A resize can happen in many places in an
// update, so resizeViews keeps the commands of the views, and Update sends
// them with the command of the update (L18).
func (m Model) Update(teaMsg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(teaMsg)
	nm, ok := next.(Model)
	if !ok || len(nm.resizeCmds) == 0 {
		return next, cmd
	}
	cmd = tea.Batch(append(slices.Clip(nm.resizeCmds), cmd)...)
	nm.resizeCmds = nil
	return nm, cmd
}

func (m Model) update(teaMsg tea.Msg) (tea.Model, tea.Cmd) {
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
		// The terminal answers once, at start. The answer can come after
		// the user opened views. The switch keeps them (L34).
		m.autoTheme = false
		name := ui.AutoThemeName(m.isDark)
		if ui.Themes[name].Name == ui.ActiveTheme.Name {
			return m, nil
		}
		eventlog.Infof(eventlog.CatUI, "Theme auto → %s (dark background: %t)", name, m.isDark)
		return m.setTheme(name), nil

	case tea.BlurMsg:
		m.blurred = true
		return m, nil

	case tea.FocusMsg:
		wasBlurred := m.blurred
		m.blurred = false
		if wasBlurred && m.nav.Depth() == 1 {
			return m, m.nav.InitCurrent()
		}
		return m, nil

	case tea.ColorProfileMsg:
		// Bubble Tea sends this once at start. NO_COLOR gives the ASCII
		// profile. A view made before this message draws again.
		ui.SetNoColor(msg.Profile == colorprofile.NoTTY || msg.Profile == colorprofile.ASCII)
		m.restyle()
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeOverlays()
		if m.panels.IsOpen() && !m.canShowPanel() {
			m.panels.Hide()
			m.resizeViews()
		}
		if !m.panels.IsOpen() && m.canShowPanel() && m.panels.Unhide(m.nav.Current(), !m.capturingInput()) {
			eventlog.Infof(eventlog.CatUI, "Panel opened: %s (%d tabs)", m.panels.Title(), m.panels.Panel().TabCount())
		}
		m.resizeViews()
		m.refreshHelp()
		return m, nil

	case panelRouted:
		// A result of a panel command. A panel that closed, or an older
		// panel, may not act on what the user sees now (H4).
		if !m.panels.IsCurrent(msg.gen) {
			eventlog.Debugf(eventlog.CatUI, "Dropped %T from panel %d: panel is not on screen", msg.msg, msg.gen)
			return m, nil
		}
		return m.Update(msg.msg)

	case appmsg.NavigateMsg:
		return m.navigate(msg)

	case profilesMsg:
		return m.openProfilePicker(msg)

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
			return m.navigate(appmsg.NavigateMsg{
				ViewID: "content",
				Params: map[string]string{
					"title":   first.Title,
					"content": first.Content,
					"format":  first.Format,
				},
			})
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
		// Save the panel for back traversal. A link to a row of the view on
		// top only moves its cursor, so it leaves no entry (decision D3).
		sameView := msg.Params["focus"] != "" && m.nav.Current().ID() == msg.ViewID
		if m.panels.IsOpen() && !sameView {
			m.panels.Push(m.nav.Depth())
		}
		m.closePanel()
		return m.navigate(appmsg.NavigateMsg{ViewID: msg.ViewID, Params: msg.Params})

	case appmsg.ToastMsg:
		_, cmd := m.toasts.Add(msg.Text, ui.ToastLevel(msg.Level), 0)
		return m, cmd

	case ui.ToastDismissMsg:
		m.toasts.Dismiss(msg.ID)
		return m, nil

	case appmsg.RequestFormMsg, appmsg.FormResultMsg,
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
		if m.panels.IsOpen() && key.Matches(msg, m.keys.TabToggle.Binding) {
			m.panels.SetFocus(!m.panels.Focused())
			return m, nil
		}

		// Global keys — work regardless of focus state
		switch {
		case msg.String() == "ctrl+c":
			return m.requestQuit()
		case key.Matches(msg, m.keys.CancelOp.Binding):
			return m.showCancelPicker()
		case key.Matches(msg, m.keys.Quit.Binding):
			if m.panels.IsOpen() {
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
			if m.panels.HasHistory() {
				m.popCrossNavBack()
				return m, nil
			}
			return m, nil
		}

		// Panel-focused key handling
		if m.panels.Focused() {
			switch {
			case key.Matches(msg, m.keys.PanelClose.Binding):
				m.dismissPanel()
				return m, nil
			case key.Matches(msg, m.keys.PanelEditor.Binding):
				return m, m.panels.EditorCmd()
			case key.Matches(msg, m.keys.PanelGrow.Binding):
				m.panels.SetWidth(m.panelWidth() + panelResizeStep)
				m.resizeViews()
				return m, nil
			case key.Matches(msg, m.keys.PanelShrink.Binding):
				m.panels.SetWidth(m.panelWidth() - panelResizeStep)
				m.resizeViews()
				return m, nil
			case key.Matches(msg, m.keys.PanelReset.Binding):
				m.panels.SetWidth(0)
				m.resizeViews()
				return m, nil
			default:
				// Forward scroll/visual/yank keys to panel
				return m, m.panels.Update(msg)
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
			text := "ReadOnly mode — press W to switch"
			if locked, rule := ui.CurrentMode().Locked(); locked {
				text = "ReadOnly mode (locked by rule " + rule + ")"
			}
			_, cmd := m.toasts.Add(text, ui.ToastError, 0)
			return m, cmd
		}
	}

	cmd := m.nav.UpdateCurrent(teaMsg)
	return m, cmd
}

// navigate handles a NavigateMsg. The link of a panel and the narrow
// fallback of TabbedContentMsg call it directly, so no key can change the
// stack between them and the navigation (H4).
func (m Model) navigate(msg appmsg.NavigateMsg) (Model, tea.Cmd) {
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
	// Open help lists the keys of the view on top, so it follows the push.
	m.refreshHelp()
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
	return m.panels.Width(m.width)
}

func (m *Model) openPanel(title, content string, format ui.ContentFormat) {
	m.openTabbedPanel(title, []appmsg.TabContent{
		{Title: title, Content: content, Format: string(format)},
	})
}

func (m *Model) openTabbedPanel(title string, tabs []appmsg.TabContent) {
	m.panels.Open(title, tabs, m.nav.Current(), !m.capturingInput())
	m.resizeViews()
	m.refreshHelp()
	eventlog.Infof(eventlog.CatUI, "Panel opened: %s (%d tabs)", title, len(tabs))
}

func (m *Model) closePanel() {
	m.panels.Close()
	m.resizeViews()
	m.refreshHelp()
}

// dismissPanel closes the panel for good: it closes an open panel and drops a
// stashed one. Use it when the panel no longer belongs on screen. A stashed
// panel is not open, so do not guard this call with panels.IsOpen.
func (m *Model) dismissPanel() {
	wasOpen := m.panels.IsOpen()
	m.panels.Dismiss()
	if wasOpen {
		m.resizeViews()
		m.refreshHelp()
	}
}

// refreshHelp builds the keys of the open help overlay again, after the panel
// opened, closed, hid or came back (H5). The filter and the scroll position
// stay.
func (m *Model) refreshHelp() {
	if m.help.Visible() {
		m.help.SetHints(m.collectAllKeyHints())
	}
}

// popCrossNavBack pops the cross-nav history, goes back to the view that
// showed the panel, and restores the panel as the user left it. The user can
// open more views after a link, so it pops to the saved depth, not one level.
// An entry whose view is gone is dropped.
func (m *Model) popCrossNavBack() {
	entry, ok := m.panels.Back(m.nav.Views())
	if !ok {
		return
	}

	m.dismissPanel()
	m.nav.PopTo(entry.depth)
	m.resizeViews()

	if !m.canShowPanel() {
		// The terminal got narrow after the link. The panel opens when the
		// terminal is wide again.
		m.panels.StashState(entry.state)
		return
	}
	m.panels.Restore(entry.state, !m.capturingInput())
	m.resizeViews()
	m.refreshHelp()
	eventlog.Infof(eventlog.CatUI, "Panel restored: %s", entry.state.title)
}

// trimCrossNavHistory drops the history entries whose view is no longer at
// its depth, and the entries of the view on top.
func (m *Model) trimCrossNavHistory() {
	m.panels.Trim(m.nav.Views())
	m.refreshHelp()
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
	if m.panels.IsOpen() {
		width = m.width - m.panelWidth() - 3 // panel, gap (1), main borders (2)
	}
	return width, height
}

func (m *Model) resizeViews() {
	if m.width == 0 || m.height == 0 {
		return
	}
	w, h := m.contentSize()
	if cmd := m.nav.UpdateCurrent(tea.WindowSizeMsg{Width: w, Height: h}); cmd != nil {
		// Clip makes append copy, so copies of the model do not share it.
		m.resizeCmds = append(slices.Clip(m.resizeCmds), cmd)
	}
	if p := m.panels.Panel(); p != nil {
		p.SetSize(m.panelWidth()-2, h)
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
	return m.pick("Select Theme", options, currentIdx, onPicked(Model.applyTheme))
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
// If the build fails, nothing changes: the current session, the views, the
// mode, and the configuration stay, and the user sees the error. If it
// succeeds, the app resets the navigation and opens the service view that
// was on top again, now with the new session.
//
// The new session gets its own mode, so the guards of the old session keep
// the old mode for the operations that it still runs. A new profile starts
// in ReadOnly. A new region of the same profile keeps the mode, because the
// account is the same.
func (m Model) switchSession(profile, region, kind, value string) (Model, tea.Cmd) {
	old := ui.CurrentMode()
	mode, match := m.newSessionMode(profile)
	if profile == m.config.AWS.Profile && !old.ReadOnly() {
		_ = mode.SetReadWrite()
	}
	svc, err := aws.NewServices(profile, region, m.config.AWS.Endpoint, mode.ReadOnly)
	if err != nil {
		eventlog.Errorf(eventlog.CatAWS, "Cannot switch %s to %s: %v", strings.ToLower(kind), value, err)
		_, cmd := m.toasts.Add(fmt.Sprintf("Cannot switch %s to %s: %s", strings.ToLower(kind), value, aws.ErrorText(err)), ui.ToastError, 0)
		return m, cmd
	}
	eventlog.Infof(eventlog.CatConfig, "%s changed → %s", kind, value)
	ui.SetCurrentMode(mode)
	m.match = match
	toast := kind + ": " + value
	if locked, rule := mode.Locked(); locked {
		toast += " · ReadOnly (locked by rule " + rule + ")"
	} else if !old.ReadOnly() && mode.ReadOnly() {
		eventlog.Infof(eventlog.CatApp, "Mode changed → ReadOnly (new profile)")
		toast += " · ReadOnly"
	}
	m.config.AWS.Profile = profile
	m.config.AWS.Region = region
	m.svc, m.svcErr = svc, nil

	returnTo := m.topLevelViewID()
	cmds := []tea.Cmd{m.resetNav()}
	_, toastCmd := m.toasts.Add(toast, ui.ToastSuccess, 0)
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
	m.panels.Reset()
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
	if locked, rule := ui.CurrentMode().Locked(); locked {
		options[1].Label = "ReadWrite   (locked by rule " + ui.Sanitize(rule) + ")"
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
		if err := ui.CurrentMode().SetReadWrite(); err != nil {
			text := fmt.Sprintf("%s is %s", displayProfile(m.config.AWS.Profile), err)
			eventlog.Warnf(eventlog.CatApp, "Mode not changed: %s", text)
			_, cmd := m.toasts.Add(text, ui.ToastError, 0)
			return m, cmd
		}
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

// applyTheme makes the theme that the user picked active. The pick ends the
// auto setting, so a late answer of the terminal does not replace it (L34).
func (m Model) applyTheme(name string) (Model, tea.Cmd) {
	eventlog.Infof(eventlog.CatUI, "Theme changed → %s", name)
	m.autoTheme = false
	m = m.setTheme(name)
	_, cmd := m.toasts.Add("Theme: "+ui.ActiveTheme.Name, ui.ToastSuccess, 0)
	return m, cmd
}

// setTheme makes a theme active and draws the open views again with it.
// The views, the panel, the back history and the open prompts stay (L17).
func (m Model) setTheme(name string) Model {
	t, ok := ui.Themes[name]
	if !ok {
		return m
	}
	ui.ActiveTheme = t
	ui.RebuildStyles()
	m.restyle()
	return m
}

// restyle draws the views on the stack and help again with the active
// styles. The other parts of the screen read the styles each time they
// draw. The panel needs no call: each ContentView draws again when the
// theme changes, and the saved panels are the same panel values.
func (m *Model) restyle() {
	for _, v := range m.nav.Views() {
		if r, ok := v.(ui.Restyler); ok {
			r.Restyle()
		}
	}
	m.refreshHelp()
}

func (m Model) View() tea.View {
	t := ui.ActiveTheme

	mode := "RO"
	if !ui.IsReadOnly() {
		mode = "RW"
	}
	crumbs := m.nav.Breadcrumbs()
	if m.panels.IsOpen() && m.panels.Title() != "" {
		crumbs = append(crumbs, m.panels.Title())
	}
	// backspace goes back only while the view on top does not take input,
	// such as a filter or a form (H6).
	crossNavLabel := ""
	if !m.capturingInput() {
		crossNavLabel = m.panels.BackTitle()
	}
	locked, _ := ui.CurrentMode().Locked()
	header := ui.RenderHeader(ui.HeaderData{
		Profile:           m.config.AWS.Profile,
		Region:            m.activeRegion(),
		Mode:              mode,
		Locked:            locked,
		Label:             m.match.Label,
		LabelLocks:        m.match.LabelLocks,
		Breadcrumbs:       crumbs,
		CrossNavBackLabel: crossNavLabel,
		Width:             m.width,
	})

	// statusFor draws the status bar. With session, it starts with the
	// label and the mode, for a terminal too short for the header.
	statusFor := func(session bool) string {
		if m.commandBar.Visible() {
			return m.commandBar.ViewInput(m.width)
		}
		data := ui.StatusBarData{Keys: m.currentKeyHints(), Error: m.visibleErr(), Width: m.width}
		if session {
			data.Session = ui.LabelBadge(m.match.Label, m.match.LabelLocks)
			if data.Session != "" {
				data.Session += " "
			}
			data.Session += ui.ModeBadge(mode, locked)
		}
		return ui.RenderStatusBar(data)
	}
	statusBar := statusFor(false)

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
		// The status bar shows the mode in place of the header.
		statusBar = statusFor(true)
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
	if m.panels.IsOpen() {
		pw := m.panelWidth()
		mainW := m.width - pw - 1 // 1 char gap between borders

		mainBorderColor := t.Secondary
		panelBorderColor := t.Secondary
		if m.panels.Focused() {
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
		panelContent := m.panels.Panel().View()
		// ui.Dim keeps the text faint after each styled token, not only up
		// to the first reset.
		if m.panels.Focused() {
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
	// The home animation pauses while the terminal does not have the focus.
	v.ReportFocus = true
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
	if m.panels.Focused() {
		var hints []ui.HintBinding
		if n := m.panels.Panel().TabCount(); n > 1 {
			hints = append(hints, ui.NewHintBinding(
				nil, fmt.Sprintf("1-%d", n), "switch tab",
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

	if m.panels.IsOpen() {
		hints = append(hints, ui.NewHintBinding([]string{"tab"}, "tab", "focus panel"))
	}

	// Service picker hints on home screen
	if m.nav.Depth() == 1 {
		hints = append(hints,
			ui.NewHintBinding([]string{"enter", "s"}, "enter/s", "services"),
		)
	}

	// Global hints
	if m.panels.HasHistory() {
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
	if m.panels.IsOpen() {
		hints = append(hints, m.keys.TabToggle.WithCategory("Navigation"))
	}
	if m.panels.HasHistory() {
		hints = append(hints, m.keys.CrossNavBack.WithCategory("Navigation"))
	}
	if m.nav.Depth() > 1 {
		hints = append(hints, ui.NewHintBinding([]string{"esc", "q"}, "esc/q", "go back").WithCategory("Navigation"))
	}

	// Panel hints (when panel is open)
	if m.panels.IsOpen() {
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
