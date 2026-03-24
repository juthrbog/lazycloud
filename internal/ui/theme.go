package ui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Theme holds all colors for the application. Themes adapt to dark/light terminals.
type Theme struct {
	Name string

	// Core palette
	Primary   color.Color
	Secondary color.Color
	Accent    color.Color
	Error     color.Color
	Warning   color.Color
	Success   color.Color
	Info      color.Color

	// Surfaces
	Base       color.Color // main background-ish tone
	Surface    color.Color // elevated surface (panels)
	Overlay    color.Color // overlays, dialogs
	Muted      color.Color // subtle text
	Text       color.Color // primary text
	SubText    color.Color // secondary text
	BrightText color.Color // emphasized text

	// Gradient stops for header
	GradientFrom color.Color
	GradientTo   color.Color

	// Syntax highlighting
	ChromaStyle string // Chroma library style name (e.g., "catppuccin-mocha", "dracula")

	// State colors
	StateRunning color.Color
	StateStopped color.Color
	StatePending color.Color
}

// --- Built-in themes ---

var Catppuccin = Theme{
	Name:         "Catppuccin Mocha",
	Primary:      lipgloss.Color("#cba6f7"), // mauve
	Secondary:    lipgloss.Color("#585b70"), // surface2
	Accent:       lipgloss.Color("#a6e3a1"), // green
	Error:        lipgloss.Color("#f38ba8"), // red
	Warning:      lipgloss.Color("#f9e2af"), // yellow
	Success:      lipgloss.Color("#a6e3a1"), // green
	Info:         lipgloss.Color("#89b4fa"), // blue
	Base:         lipgloss.Color("#1e1e2e"), // base
	Surface:      lipgloss.Color("#313244"), // surface0
	Overlay:      lipgloss.Color("#45475a"), // surface1
	Muted:        lipgloss.Color("#6c7086"), // overlay0
	Text:         lipgloss.Color("#cdd6f4"), // text
	SubText:      lipgloss.Color("#a6adc8"), // subtext0
	BrightText:   lipgloss.Color("#ffffff"),
	GradientFrom: lipgloss.Color("#cba6f7"), // mauve
	GradientTo:   lipgloss.Color("#89b4fa"), // blue
	ChromaStyle:  "catppuccin-mocha",
	StateRunning: lipgloss.Color("#a6e3a1"), // green
	StateStopped: lipgloss.Color("#f38ba8"), // red
	StatePending: lipgloss.Color("#f9e2af"), // yellow
}

var Dracula = Theme{
	Name:         "Dracula",
	Primary:      lipgloss.Color("#bd93f9"), // purple
	Secondary:    lipgloss.Color("#6272a4"), // comment
	Accent:       lipgloss.Color("#50fa7b"), // green
	Error:        lipgloss.Color("#ff5555"), // red
	Warning:      lipgloss.Color("#f1fa8c"), // yellow
	Success:      lipgloss.Color("#50fa7b"), // green
	Info:         lipgloss.Color("#8be9fd"), // cyan
	Base:         lipgloss.Color("#282a36"), // background
	Surface:      lipgloss.Color("#44475a"), // current line
	Overlay:      lipgloss.Color("#6272a4"), // comment
	Muted:        lipgloss.Color("#6272a4"), // comment
	Text:         lipgloss.Color("#f8f8f2"), // foreground
	SubText:      lipgloss.Color("#bfbfbf"),
	BrightText:   lipgloss.Color("#ffffff"),
	GradientFrom: lipgloss.Color("#bd93f9"), // purple
	GradientTo:   lipgloss.Color("#ff79c6"), // pink
	ChromaStyle:  "dracula",
	StateRunning: lipgloss.Color("#50fa7b"), // green
	StateStopped: lipgloss.Color("#ff5555"), // red
	StatePending: lipgloss.Color("#f1fa8c"), // yellow
}

var Nord = Theme{
	Name:         "Nord",
	Primary:      lipgloss.Color("#88c0d0"), // frost
	Secondary:    lipgloss.Color("#4c566a"), // polar night
	Accent:       lipgloss.Color("#a3be8c"), // aurora green
	Error:        lipgloss.Color("#bf616a"), // aurora red
	Warning:      lipgloss.Color("#ebcb8b"), // aurora yellow
	Success:      lipgloss.Color("#a3be8c"), // aurora green
	Info:         lipgloss.Color("#81a1c1"), // frost blue
	Base:         lipgloss.Color("#2e3440"), // polar night
	Surface:      lipgloss.Color("#3b4252"), // polar night
	Overlay:      lipgloss.Color("#434c5e"), // polar night
	Muted:        lipgloss.Color("#4c566a"), // polar night
	Text:         lipgloss.Color("#eceff4"), // snow storm
	SubText:      lipgloss.Color("#d8dee9"), // snow storm
	BrightText:   lipgloss.Color("#ffffff"),
	GradientFrom: lipgloss.Color("#88c0d0"), // frost
	GradientTo:   lipgloss.Color("#5e81ac"), // frost
	ChromaStyle:  "nord",
	StateRunning: lipgloss.Color("#a3be8c"),
	StateStopped: lipgloss.Color("#bf616a"),
	StatePending: lipgloss.Color("#ebcb8b"),
}

var TokyoNight = Theme{
	Name:         "Tokyo Night",
	Primary:      lipgloss.Color("#7aa2f7"), // blue
	Secondary:    lipgloss.Color("#565f89"), // comment
	Accent:       lipgloss.Color("#9ece6a"), // green
	Error:        lipgloss.Color("#f7768e"), // red
	Warning:      lipgloss.Color("#e0af68"), // yellow
	Success:      lipgloss.Color("#9ece6a"), // green
	Info:         lipgloss.Color("#7aa2f7"), // blue
	Base:         lipgloss.Color("#1a1b26"), // bg
	Surface:      lipgloss.Color("#24283b"), // bg_dark
	Overlay:      lipgloss.Color("#414868"), // bg_highlight
	Muted:        lipgloss.Color("#565f89"), // comment
	Text:         lipgloss.Color("#c0caf5"), // fg
	SubText:      lipgloss.Color("#a9b1d6"), // fg_dark
	BrightText:   lipgloss.Color("#ffffff"),
	GradientFrom: lipgloss.Color("#7aa2f7"), // blue
	GradientTo:   lipgloss.Color("#bb9af7"), // purple
	ChromaStyle:  "tokyonight-night",
	StateRunning: lipgloss.Color("#9ece6a"),
	StateStopped: lipgloss.Color("#f7768e"),
	StatePending: lipgloss.Color("#e0af68"),
}

// ThemeOrder defines the cycle order for theme switching.
var ThemeOrder = []string{"catppuccin", "dracula", "nord", "tokyonight"}

// Themes maps theme names to themes.
var Themes = map[string]Theme{
	"catppuccin": Catppuccin,
	"dracula":    Dracula,
	"nord":       Nord,
	"tokyonight": TokyoNight,
}

// DefaultTheme is the default theme.
var DefaultTheme = Catppuccin

// ActiveTheme is the currently active theme. Set at startup.
var ActiveTheme = DefaultTheme

// CycleTheme advances to the next theme and rebuilds styles.
// Returns the new theme name.
func CycleTheme() string {
	current := ActiveTheme.Name
	nextIdx := 0
	for i, name := range ThemeOrder {
		if Themes[name].Name == current {
			nextIdx = (i + 1) % len(ThemeOrder)
			break
		}
	}
	next := ThemeOrder[nextIdx]
	ActiveTheme = Themes[next]
	RebuildStyles()
	return next
}
