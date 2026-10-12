package views

import (
	"math/rand/v2"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/ui"
)

// Home is the landing screen — displays an idle animation and provides
// access to services via the `s` / Enter picker (handled in app.go).
type Home struct {
	animation ui.Animation
	tickGen   int // generation of the running tick chain
	width     int
	height    int
}

func (h *Home) ID() string     { return "home" }
func (h *Home) Title() string  { return "Services" }
func (h *Home) Footer() string { return "" }
func (h *Home) KeyMap() []ui.HintBinding {
	return []ui.HintBinding{
		ui.NewHintBinding([]string{"+"}, "+/-", "cloud speed"),
	}
}

// NewHome creates the home landing view.
// There's a 5% chance of a storm animation on launch.
func NewHome() *Home {
	seed := time.Now().UnixNano()
	var anim ui.Animation
	if rand.IntN(20) == 0 { //nolint:gosec // cosmetic randomness, not security
		anim = ui.NewStormAnimation(seed)
	} else {
		anim = ui.NewCloudAnimation(seed)
	}
	return &Home{animation: anim}
}

// Init starts a new chain of animation ticks. The app calls it each time the
// home view is shown again. Frames of an older chain still in flight are
// ignored, so the animation never runs at double speed.
func (h *Home) Init() tea.Cmd {
	h.tickGen++
	return ui.AnimationTick(h.tickGen)
}

func (h *Home) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case tea.WindowSizeMsg:
		h.width = m.Width
		h.height = m.Height
		return h, nil
	case ui.AnimationFrameMsg:
		if m.Gen != h.tickGen {
			return h, nil // frame of an older chain
		}
		h.animation.Update()
		return h, ui.AnimationTick(h.tickGen)
	case tea.KeyPressMsg:
		switch m.String() {
		case "+", "=":
			h.animation.SpeedUp()
			return h, nil
		case "-", "_":
			h.animation.SlowDown()
			return h, nil
		}
	}
	return h, nil
}

func (h *Home) View() tea.View {
	content := h.animation.View(h.width, h.height)
	return tea.NewView(content)
}
