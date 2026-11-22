package views

import (
	"context"
	"errors"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/jump"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The tag search of the jump (docs/design/jump.md, "Tag search (PR 6)"): the
// tagged resources of the session Region from the Resource Groups Tagging
// API. With no filter it lists every tagged resource. t sets a filter, and
// enter jumps to the resource of the row.

// jumpTagsPageBudget is 10 pages of 100: 1,000 resources.
const jumpTagsPageBudget = 10

// jumpTagsForm is the ID of the form that asks for the tag filter.
const jumpTagsForm = "jump_tags_filter"

var jumpTagsKeys = struct {
	Open   ui.HintBinding
	Filter ui.HintBinding
}{
	Open:   ui.NewHintBinding([]string{"enter"}, "enter", "jump"),
	Filter: ui.NewHintBinding([]string{"t"}, "t", "tag filter"),
}

// JumpTags lists the tagged resources of the session Region.
type JumpTags struct {
	listCore[aws.TaggedResource]
	tagText string // the tag terms, as typed: "team=orders env"
}

// NewJumpTags creates the tag search for filter, a list of KEY=VALUE or
// KEY terms with or without "tag:". An empty filter lists every tagged
// resource. The row key is the ARN, so y copies it.
func NewJumpTags(svc aws.TaggingService, filter string) *JumpTags {
	filters, _ := ParseTagFilter(filter)
	v := &JumpTags{tagText: strings.TrimSpace(filter)}
	v.listCore = newListCore(listConfig[aws.TaggedResource]{
		Noun:         "resources",
		LoadingLabel: "Loading tagged resources...",
		ErrContext:   "searching tagged resources",
		Key:          func(r aws.TaggedResource) string { return r.ARN },
		Columns:      jumpTagsColumns,
		Row:          jumpTagsRow,
		Fetch: fetchPages(svc, func(s aws.TaggingService, ctx context.Context, tok *string) (*aws.TaggedPage, error) {
			return s.GetResourcesPage(ctx, filters, tok)
		}, func(p *aws.TaggedPage) ([]aws.TaggedResource, *string) {
			if p == nil {
				return nil, nil
			}
			return p.Resources, p.Next
		}),
		FetchTimeout: callTimeout,
		OpenDetail:   jumpTagsDetail,
		PageBudget:   jumpTagsPageBudget,
		MoreKey:      true,
		Copy:         true,
		CopyNoun:     "ARN",
	})
	return v
}

// ParseTagFilter reads tag terms: KEY=VALUE or KEY, with or without "tag:",
// with spaces between them. The terms of one key OR their values, as the
// Tagging API does, and the keys are ANDed. KEY alone matches any value of
// the key. It returns an error for a term with no key.
func ParseTagFilter(text string) ([]aws.TagFilter, error) {
	var terms []string
	for f := range strings.FieldsSeq(text) {
		if !strings.HasPrefix(f, "tag:") {
			f = "tag:" + f
		}
		terms = append(terms, f)
	}
	if len(terms) == 0 {
		return nil, nil
	}
	t := jump.Parse(strings.Join(terms, " "))
	if t.Kind != jump.Tags {
		return nil, errors.New("each term must be KEY=VALUE or KEY")
	}
	var out []aws.TagFilter
	anyValue := map[string]bool{}
	for _, tag := range t.Tags {
		i := slices.IndexFunc(out, func(f aws.TagFilter) bool { return f.Key == tag.Key })
		if i < 0 {
			out = append(out, aws.TagFilter{Key: tag.Key})
			i = len(out) - 1
		}
		switch {
		case !tag.HasValue:
			anyValue[tag.Key] = true
		case !slices.Contains(out[i].Values, tag.Value):
			out[i].Values = append(out[i].Values, tag.Value)
		}
	}
	// A key alone matches any value, so its values do not narrow it.
	for i := range out {
		if anyValue[out[i].Key] {
			out[i].Values = nil
		}
	}
	return out, nil
}

func jumpTagsColumns(tier ui.WidthTier) []ui.Column {
	if tier == ui.TierNarrow {
		return []ui.Column{
			{Title: "Service", Width: 8},
			{Title: "Name", Width: 20, Weight: 2, MaxWidth: 60},
			{Title: "Tags", Width: 20, Weight: 3},
		}
	}
	return []ui.Column{
		{Title: "Service", Width: 10},
		{Title: "Type", Width: 16},
		{Title: "Name", Width: 24, Weight: 2, MaxWidth: 80},
		{Title: "Region", Width: 14},
		{Title: "Tags", Width: 24, Weight: 3},
	}
}

func jumpTagsRow(r aws.TaggedResource, tier ui.WidthTier) ui.Row {
	region := r.Region()
	if region == "" {
		region = "global"
	}
	if tier == ui.TierNarrow {
		return ui.Row{Key: r.ARN, Cells: table.Row{r.Service(), r.Name(), r.TagText()}}
	}
	return ui.Row{Key: r.ARN, Cells: table.Row{r.Service(), r.Type(), r.Name(), region, r.TagText()}}
}

// jumpTagsDetail shows the ARN and the tags of a resource, from the row.
func jumpTagsDetail(r aws.TaggedResource) tea.Cmd {
	info, links := buildInfo([]infoField{
		{K: "ARN", V: r.ARN},
		{K: "Service", V: r.Service()},
		{K: "Type", V: r.Type()},
		{K: "Region", V: r.Region()},
	})
	tabs := []msg.TabContent{{Title: "Info", Content: info, Format: "text", Links: links}}
	if len(r.Tags) > 0 {
		tabs = append(tabs, msg.TabContent{Title: "Tags", Content: buildTagsContent(r.Tags), Format: "text"})
	}
	d := msg.TabbedContentMsg{PanelTitle: r.Name(), Tabs: tabs}
	return func() tea.Msg { return d }
}

func (v *JumpTags) ID() string { return "jump_tags:" + v.tagText }

func (v *JumpTags) Title() string {
	if v.tagText == "" {
		return "Tag Search"
	}
	return "Tags " + v.tagText
}

func (v *JumpTags) Init() tea.Cmd { return v.init() }

func (v *JumpTags) Footer() string {
	if v.tagText == "" {
		return v.footer() + " · every tagged resource of the Region · t to filter by tag"
	}
	return v.footer()
}

func (v *JumpTags) View() tea.View { return tea.NewView(v.render("", "")) }

func (v *JumpTags) KeyMap() []ui.HintBinding {
	// enter jumps, so d alone opens the detail.
	return v.hints(jumpTagsKeys.Open, listKeys.Describe, jumpTagsKeys.Filter, listKeys.CopyID,
		listKeys.More, listKeys.Sort, listKeys.Filter, listKeys.Refresh)
}

// filterForm asks for the tag terms, with the current filter filled in.
func (v *JumpTags) filterForm() tea.Cmd {
	req := msg.RequestFormMsg{
		ID:    jumpTagsForm,
		Title: "Tag filter",
		Fields: []msg.FormField{{
			Key: "tags", Type: "input", Title: "Tags",
			Description: "KEY=VALUE terms with spaces between them, for example team=orders env=prod. KEY alone matches any value.",
			Default:     v.tagText, Required: true,
			Validate: func(s string) error { _, err := ParseTagFilter(s); return err },
		}},
	}
	return func() tea.Msg { return req }
}

// Update returns the view itself. enter jumps to the resource of the row,
// and t asks for a tag filter, which opens a new search.
func (v *JumpTags) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case msg.FormResultMsg:
		if m.ID != jumpTagsForm || m.Aborted {
			return v, nil
		}
		text := strings.TrimSpace(m.Values["tags"])
		return v, navigate("jump_tags", map[string]string{"filter": text})
	case tea.KeyPressMsg:
		if v.filter.Active() {
			break
		}
		if key.Matches(m, jumpTagsKeys.Filter.Binding) {
			return v, v.filterForm()
		}
		if it := v.cursorItem(); it != nil && v.err == nil && key.Matches(m, jumpTagsKeys.Open.Binding) {
			arn := it.ARN
			return v, func() tea.Msg { return msg.JumpMsg{Text: arn} }
		}
	}
	return v, v.update(m)
}
