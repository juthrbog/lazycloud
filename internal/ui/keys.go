package ui

import "charm.land/bubbles/v2/key"

// HintBinding wraps key.Binding with metadata for the help overlay and
// status bar. It carries HintMode (ReadOnly/ReadWrite filtering) and Category
// (help overlay grouping) that key.Binding alone does not provide.
type HintBinding struct {
	key.Binding
	Mode     HintMode // ModeAny, ModeReadOnly, ModeReadWrite
	Category string   // for help overlay grouping; empty = "Current View"
}

// NewHintBinding creates a HintBinding with ModeAny and no category.
func NewHintBinding(keys []string, helpKey, helpDesc string) HintBinding {
	return HintBinding{
		Binding: key.NewBinding(
			key.WithKeys(keys...),
			key.WithHelp(helpKey, helpDesc),
		),
	}
}

// WithMode returns a copy with the given HintMode.
func (h HintBinding) WithMode(m HintMode) HintBinding {
	h.Mode = m
	return h
}

// WithCategory returns a copy with the given category.
func (h HintBinding) WithCategory(cat string) HintBinding {
	h.Category = cat
	return h
}

// ApplyMode enables or disables this binding based on the current ReadOnly
// state. ModeReadWrite bindings are disabled when ReadOnly is true;
// ModeReadOnly bindings are disabled when ReadOnly is false.
func (h *HintBinding) ApplyMode() {
	switch h.Mode {
	case ModeReadWrite:
		h.SetEnabled(!ReadOnly)
	case ModeReadOnly:
		h.SetEnabled(ReadOnly)
	}
}

// ApplyModeAll applies mode logic to a slice of HintBindings.
func ApplyModeAll(bindings []HintBinding) {
	for i := range bindings {
		bindings[i].ApplyMode()
	}
}

// AppKeyMap holds all app-level keybindings (global, navigation, panel).
type AppKeyMap struct {
	Quit          HintBinding
	ThemePicker   HintBinding
	ProfilePicker HintBinding
	RegionPicker  HintBinding
	ModePicker    HintBinding
	EventLog      HintBinding
	CommandBar    HintBinding
	Help          HintBinding
	TabToggle     HintBinding
	PanelClose    HintBinding
	PanelEditor   HintBinding
	PanelGrow     HintBinding
	PanelShrink   HintBinding
	PanelReset    HintBinding
}

// DefaultAppKeyMap returns the default app-level keybindings.
func DefaultAppKeyMap() AppKeyMap {
	return AppKeyMap{
		Quit:          NewHintBinding([]string{"q"}, "q", "quit"),
		ThemePicker:   NewHintBinding([]string{"T"}, "T", "theme"),
		ProfilePicker: NewHintBinding([]string{"P"}, "P", "profile"),
		RegionPicker:  NewHintBinding([]string{"R"}, "R", "region"),
		ModePicker:    NewHintBinding([]string{"W"}, "W", "mode"),
		EventLog:      NewHintBinding([]string{"L"}, "L", "logs"),
		CommandBar:    NewHintBinding([]string{":"}, ":", "command"),
		Help:          NewHintBinding([]string{"?"}, "?", "help"),
		TabToggle:     NewHintBinding([]string{"tab"}, "tab", "toggle panel focus"),
		PanelClose:    NewHintBinding([]string{"esc"}, "esc", "close panel"),
		PanelEditor:   NewHintBinding([]string{"e"}, "e", "editor"),
		PanelGrow:     NewHintBinding([]string{"<"}, "<", "grow panel"),
		PanelShrink:   NewHintBinding([]string{">"}, ">", "shrink panel"),
		PanelReset:    NewHintBinding([]string{"="}, "=", "reset panel"),
	}
}

// ContentViewKeyMap holds keybindings for the content viewer (panel).
type ContentViewKeyMap struct {
	CursorDown   key.Binding
	CursorUp     key.Binding
	GotoTop      key.Binding
	GotoBottom   key.Binding
	HalfPageDown key.Binding
	HalfPageUp   key.Binding
	VisualToggle key.Binding
	Yank         key.Binding
	Enter        key.Binding
	LineNumbers  key.Binding
	WrapToggle   key.Binding
	ScrollLeft   key.Binding
	ScrollRight  key.Binding
}

// DefaultContentViewKeyMap returns the default content viewer keybindings.
func DefaultContentViewKeyMap() ContentViewKeyMap {
	return ContentViewKeyMap{
		CursorDown:   key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j/k", "scroll")),
		CursorUp:     key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k", "up")),
		GotoTop:      key.NewBinding(key.WithKeys("g"), key.WithHelp("g/G", "top/bottom")),
		GotoBottom:   key.NewBinding(key.WithKeys("G"), key.WithHelp("G", "bottom")),
		HalfPageDown: key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "page down")),
		HalfPageUp:   key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "page up")),
		VisualToggle: key.NewBinding(key.WithKeys("V"), key.WithHelp("V", "visual")),
		Yank:         key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "yank")),
		Enter:        key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "follow link")),
		LineNumbers:  key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "lines")),
		WrapToggle:   key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "wrap")),
		ScrollLeft:   key.NewBinding(key.WithKeys("h", "left"), key.WithHelp("h/l", "scroll horiz")),
		ScrollRight:  key.NewBinding(key.WithKeys("l", "right"), key.WithHelp("l", "right")),
	}
}
