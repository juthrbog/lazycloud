package ui

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

// DetailField represents a key-value pair in a detail pane.
type DetailField struct {
	Key   string
	Value string
}

// Detail renders a scrollable key-value detail pane.
type Detail struct {
	viewport viewport.Model
	fields   []DetailField
	title    string
	width    int
	height   int
}

// NewDetail creates a Detail pane with the given title and fields.
func NewDetail(title string, fields []DetailField) Detail {
	vp := viewport.New()
	d := Detail{
		viewport: vp,
		fields:   fields,
		title:    title,
	}
	d.renderContent()
	return d
}

// SetFields updates the displayed fields.
func (d *Detail) SetFields(fields []DetailField) {
	d.fields = fields
	d.renderContent()
}

// SetSize sets the detail pane dimensions.
func (d *Detail) SetSize(w, h int) {
	d.width = w
	d.height = h
	d.viewport.SetWidth(w)
	d.viewport.SetHeight(h)
	d.renderContent()
}

// Update handles viewport scrolling messages.
func (d Detail) Update(msg tea.Msg) (Detail, tea.Cmd) {
	var cmd tea.Cmd
	d.viewport, cmd = d.viewport.Update(msg)
	return d, cmd
}

// View renders the detail pane.
func (d Detail) View() string {
	titleBar := S.Title.Render(d.title)
	return titleBar + "\n" + d.viewport.View()
}

func (d *Detail) renderContent() {
	var b strings.Builder
	for _, f := range d.fields {
		key := S.DetailKey.Render(f.Key)
		val := S.DetailValue.Render(f.Value)
		b.WriteString(key + "  " + val + "\n")
	}
	d.viewport.SetContent(b.String())
}
