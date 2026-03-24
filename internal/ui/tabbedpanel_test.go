package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func init() {
	RebuildStyles()
}

func testTabs() []struct {
	Title   string
	Content string
	Format  string
} {
	return []struct {
		Title   string
		Content string
		Format  string
	}{
		{Title: "Info", Content: "key: value", Format: "text"},
		{Title: "JSON", Content: `{"a": 1}`, Format: "json"},
		{Title: "Tags", Content: "Name  my-server", Format: "text"},
	}
}

func TestNewTabbedPanel(t *testing.T) {
	tp := NewTabbedPanel("test", testTabs())
	assert.Equal(t, 3, tp.TabCount())
	assert.Equal(t, 0, tp.ActiveTab())
}

func TestTabbedPanelSwitchTab(t *testing.T) {
	tp := NewTabbedPanel("test", testTabs())
	tp.SetSize(80, 24)

	// Switch to tab 2
	tp, _ = tp.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	assert.Equal(t, 1, tp.ActiveTab())

	// Switch to tab 3
	tp, _ = tp.Update(tea.KeyPressMsg{Code: '3', Text: "3"})
	assert.Equal(t, 2, tp.ActiveTab())

	// Switch back to tab 1
	tp, _ = tp.Update(tea.KeyPressMsg{Code: '1', Text: "1"})
	assert.Equal(t, 0, tp.ActiveTab())
}

func TestTabbedPanelOutOfRangeKey(t *testing.T) {
	tp := NewTabbedPanel("test", testTabs())

	// Tab 5 doesn't exist (only 3 tabs)
	tp, _ = tp.Update(tea.KeyPressMsg{Code: '5', Text: "5"})
	assert.Equal(t, 0, tp.ActiveTab(), "out of range key should not change tab")
}

func TestTabbedPanelSingleTab(t *testing.T) {
	tabs := []struct {
		Title   string
		Content string
		Format  string
	}{
		{Title: "Info", Content: "hello", Format: "text"},
	}
	tp := NewTabbedPanel("test", tabs)
	assert.Equal(t, 1, tp.TabCount())

	// Number key does nothing with single tab
	tp, _ = tp.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	assert.Equal(t, 0, tp.ActiveTab())
}

func TestTabbedPanelSetSize(t *testing.T) {
	tp := NewTabbedPanel("test", testTabs())
	tp.SetSize(80, 24)

	assert.Equal(t, 80, tp.width)
	assert.Equal(t, 24, tp.height)
}

func TestTabbedPanelViewRendersTabBar(t *testing.T) {
	tp := NewTabbedPanel("test", testTabs())
	tp.SetSize(80, 24)

	view := tp.View()
	assert.Contains(t, view, "1:Info")
	assert.Contains(t, view, "2:JSON")
	assert.Contains(t, view, "3:Tags")
}

func TestTabbedPanelViewEmpty(t *testing.T) {
	tp := NewTabbedPanel("test", nil)
	assert.Equal(t, "", tp.View())
}

func TestTabbedPanelLazyCreation(t *testing.T) {
	tp := NewTabbedPanel("test", testTabs())

	// First tab viewer created eagerly
	assert.NotNil(t, tp.tabs[0].viewer)

	// Other tabs are nil until accessed
	assert.Nil(t, tp.tabs[1].viewer)
	assert.Nil(t, tp.tabs[2].viewer)

	// Switch to tab 2 — viewer created
	tp.SetSize(80, 24)
	tp, _ = tp.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	assert.NotNil(t, tp.tabs[1].viewer)

	// Tab 3 still nil
	assert.Nil(t, tp.tabs[2].viewer)
}
