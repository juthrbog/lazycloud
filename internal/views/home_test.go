package views

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/ui"
)

// P3: each frame of the current chain moves the animation in the area of
// the home view. A redraw without a frame moves nothing.
func TestHomeFramesMoveTheAnimation(t *testing.T) {
	h := NewHome()
	h.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	h.Init()
	first := h.View().Content
	require.NotEmpty(t, first)
	require.Equal(t, first, h.View().Content, "a redraw moves nothing")

	changed := false
	for range 60 {
		_, cmd := h.Update(ui.AnimationFrameMsg{Gen: h.tickGen})
		require.NotNil(t, cmd, "the chain goes on")
		if h.View().Content != first {
			changed = true
			break
		}
	}
	assert.True(t, changed, "the frames move the animation")
}
