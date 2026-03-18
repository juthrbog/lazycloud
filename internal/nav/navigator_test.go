package nav

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"

	"github.com/juthrbog/lazycloud/internal/ui"
)

// stubView is a minimal View implementation for testing.
type stubView struct {
	id    string
	title string
}

func (v stubView) ID() string              { return v.id }
func (v stubView) Title() string           { return v.title }
func (v stubView) KeyMap() []ui.KeyHint    { return nil }
func (v stubView) Init() tea.Cmd           { return nil }
func (v stubView) View() tea.View          { return tea.NewView("") }
func (v stubView) Update(tea.Msg) (tea.Model, tea.Cmd) {
	return v, nil
}

func TestNew(t *testing.T) {
	root := stubView{id: "root", title: "Root"}
	n := New(root)

	assert.Equal(t, 1, n.Depth())
	assert.Equal(t, "root", n.Current().ID())
}

func TestPushPop(t *testing.T) {
	root := stubView{id: "root", title: "Root"}
	child := stubView{id: "child", title: "Child"}
	n := New(root)

	n.Push(child)
	assert.Equal(t, 2, n.Depth())
	assert.Equal(t, "child", n.Current().ID())

	n.Pop()
	assert.Equal(t, 1, n.Depth())
	assert.Equal(t, "root", n.Current().ID())
}

func TestPopAtRootIsNoop(t *testing.T) {
	root := stubView{id: "root", title: "Root"}
	n := New(root)

	n.Pop()
	assert.Equal(t, 1, n.Depth())
	assert.Equal(t, "root", n.Current().ID())
}

func TestCacheReuse(t *testing.T) {
	root := stubView{id: "root", title: "Root"}
	original := stubView{id: "page", title: "Original"}
	n := New(root)

	n.Push(original)
	n.Pop()

	// Push a new view with the same ID — should get the cached version
	replacement := stubView{id: "page", title: "Replacement"}
	n.Push(replacement)

	assert.Equal(t, "Original", n.Current().Title())
}

func TestBreadcrumbs(t *testing.T) {
	root := stubView{id: "root", title: "Home"}
	child1 := stubView{id: "c1", title: "Buckets"}
	child2 := stubView{id: "c2", title: "Objects"}
	n := New(root)
	n.Push(child1)
	n.Push(child2)

	assert.Equal(t, []string{"Home", "Buckets", "Objects"}, n.Breadcrumbs())
}

func TestClearCache(t *testing.T) {
	root := stubView{id: "root", title: "Root"}
	original := stubView{id: "page", title: "Original"}
	n := New(root)

	n.Push(original)
	n.Pop()
	n.ClearCache()

	// Push a new view with the same ID — cache was cleared, so fresh view is used
	replacement := stubView{id: "page", title: "Replacement"}
	n.Push(replacement)

	assert.Equal(t, "Replacement", n.Current().Title())
}

func TestUpdateCurrent(t *testing.T) {
	updated := false
	root := &trackingView{id: "root", title: "Root", onUpdate: func() { updated = true }}
	n := New(root)

	n.UpdateCurrent(tea.KeyPressMsg{})
	assert.True(t, updated)
}

// trackingView tracks whether Update was called.
type trackingView struct {
	id       string
	title    string
	onUpdate func()
}

func (v *trackingView) ID() string              { return v.id }
func (v *trackingView) Title() string           { return v.title }
func (v *trackingView) KeyMap() []ui.KeyHint    { return nil }
func (v *trackingView) Init() tea.Cmd           { return nil }
func (v *trackingView) View() tea.View          { return tea.NewView("") }
func (v *trackingView) Update(tea.Msg) (tea.Model, tea.Cmd) {
	if v.onUpdate != nil {
		v.onUpdate()
	}
	return v, nil
}
