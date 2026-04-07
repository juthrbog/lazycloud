package ui

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/huh/v2"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/msg"
)

// FormView wraps a huh.Form as a nav.View, providing a reusable multi-field
// form that integrates with the app's message routing and theme system.
type FormView struct {
	id     string
	title  string
	formID string // echoed back in FormResultMsg
	form   *huh.Form
	keys   []msg.FormField // field specs for value extraction
	done   bool
	width  int
	height int
}

// NewFormView creates a FormView from a RequestFormMsg.
func NewFormView(req msg.RequestFormMsg) *FormView {
	fields := make([]huh.Field, 0, len(req.Fields))
	for _, f := range req.Fields {
		fields = append(fields, buildHuhField(f))
	}

	form := huh.NewForm(huh.NewGroup(fields...)).
		WithTheme(HuhTheme()).
		WithShowHelp(false)

	return &FormView{
		id:     fmt.Sprintf("form:%s:%d", req.ID, time.Now().UnixNano()),
		title:  req.Title,
		formID: req.ID,
		form:   form,
		keys:   req.Fields,
	}
}

func (f *FormView) ID() string    { return f.id }
func (f *FormView) Title() string { return f.title }
func (f *FormView) KeyMap() []HintBinding {
	return []HintBinding{
		{Binding: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "next"))},
		{Binding: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "prev"))},
		{Binding: key.NewBinding(key.WithKeys("alt+enter"), key.WithHelp("alt+enter", "new line"))},
		{Binding: key.NewBinding(key.WithKeys("ctrl+e"), key.WithHelp("ctrl+e", "editor"))},
		{Binding: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))},
	}
}
func (f *FormView) Footer() string { return f.title }

func (f *FormView) Init() tea.Cmd {
	return f.form.Init()
}

func (f *FormView) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	if f.done {
		return f, nil
	}

	switch m := m.(type) {
	case tea.WindowSizeMsg:
		f.width = m.Width
		f.height = m.Height
		f.form = f.form.WithWidth(m.Width).WithHeight(m.Height - 3)
		return f, nil
	case tea.KeyPressMsg:
		if m.String() == "esc" {
			f.done = true
			formID := f.formID
			return f, func() tea.Msg {
				return msg.FormResultMsg{ID: formID, Aborted: true}
			}
		}
	}

	model, cmd := f.form.Update(m)
	if updated, ok := model.(*huh.Form); ok {
		f.form = updated
	}

	switch f.form.State {
	case huh.StateCompleted:
		f.done = true
		values := make(map[string]string, len(f.keys))
		for _, k := range f.keys {
			values[k.Key] = f.form.GetString(k.Key)
		}
		formID := f.formID
		return f, func() tea.Msg {
			return msg.FormResultMsg{ID: formID, Values: values}
		}
	case huh.StateAborted:
		f.done = true
		formID := f.formID
		return f, func() tea.Msg {
			return msg.FormResultMsg{ID: formID, Aborted: true}
		}
	}

	return f, cmd
}

func (f *FormView) View() tea.View {
	return tea.NewView("\n" + f.form.View())
}

// chainValidators combines Required and custom Validate into a single validator.
func chainValidators(f msg.FormField) func(string) error {
	var validators []func(string) error
	if f.Required {
		validators = append(validators, huh.ValidateNotEmpty())
	}
	if f.Validate != nil {
		validators = append(validators, f.Validate)
	}
	if len(validators) == 0 {
		return nil
	}
	if len(validators) == 1 {
		return validators[0]
	}
	return func(s string) error {
		for _, v := range validators {
			if err := v(s); err != nil {
				return err
			}
		}
		return nil
	}
}

// buildHuhField translates a declarative FormField into a huh Field.
func buildHuhField(f msg.FormField) huh.Field {
	switch f.Type {
	case "text":
		field := huh.NewText().
			Key(f.Key).
			Title(f.Title).
			Description(f.Description).
			Placeholder(f.Placeholder).
			Lines(6)
		if f.Default != "" {
			v := f.Default
			field = field.Value(&v)
		}
		if v := chainValidators(f); v != nil {
			field = field.Validate(v)
		}
		return field

	case "select":
		options := make([]huh.Option[string], len(f.Options))
		for i, opt := range f.Options {
			options[i] = huh.NewOption(opt, opt)
		}
		field := huh.NewSelect[string]().
			Key(f.Key).
			Title(f.Title).
			Description(f.Description).
			Options(options...)
		if f.Default != "" {
			v := f.Default
			field = field.Value(&v)
		}
		return field

	case "confirm":
		field := huh.NewConfirm().
			Key(f.Key).
			Title(f.Title).
			Description(f.Description)
		return field

	default: // "input" or unrecognized
		field := huh.NewInput().
			Key(f.Key).
			Title(f.Title).
			Description(f.Description).
			Placeholder(f.Placeholder)
		if f.Default != "" {
			v := f.Default
			field = field.Value(&v)
		}
		if v := chainValidators(f); v != nil {
			field = field.Validate(v)
		}
		return field
	}
}
