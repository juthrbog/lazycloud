package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func init() {
	RebuildStyles()
}

func testTabs() []TabInput {
	return []TabInput{
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
	tabs := []TabInput{
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

func TestTabbedPanelPassesLinksToViewer(t *testing.T) {
	tabs := []TabInput{
		{
			Title:   "Info",
			Content: "line0\nline1\nline2",
			Format:  "text",
			Links: map[int]ContentLink{
				1: {ViewID: "ami_list", Params: map[string]string{"id": "ami-123"}},
			},
		},
	}
	tp := NewTabbedPanel("test", tabs)
	tp.SetSize(80, 24)

	assert.NotNil(t, tp.tabs[0].viewer)
	assert.True(t, tp.tabs[0].viewer.HasLinkAtCursor() == false) // cursor at line 0

	// Move cursor to linked line
	tp, _ = tp.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	assert.True(t, tp.tabs[0].viewer.HasLinkAtCursor())
}

func TestContentViewEnterOnLinkedLineEmitsMsg(t *testing.T) {
	cv := NewContentView("test", "line0\nlinked\nline2", FormatText)
	cv.SetSize(80, 24)
	cv.SetLinks(map[int]ContentLink{
		1: {ViewID: "ami_list", Params: map[string]string{"id": "ami-123"}},
	})

	// Move to linked line
	cv, _ = cv.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})

	// Enter should emit ContentLinkActivatedMsg
	cv, cmd := cv.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.NotNil(t, cmd)

	result := cmd()
	linkMsg, ok := result.(ContentLinkActivatedMsg)
	assert.True(t, ok, "expected ContentLinkActivatedMsg, got %T", result)
	assert.Equal(t, "ami_list", linkMsg.ViewID)
	assert.Equal(t, "ami-123", linkMsg.Params["id"])
}

func TestContentViewEnterOnNonLinkedLineDoesNothing(t *testing.T) {
	cv := NewContentView("test", "line0\nlinked\nline2", FormatText)
	cv.SetSize(80, 24)
	cv.SetLinks(map[int]ContentLink{
		1: {ViewID: "ami_list"},
	})

	// Cursor at line 0 (not linked)
	cv, cmd := cv.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Nil(t, cmd)
}
