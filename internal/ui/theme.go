package ui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Theme holds all colors for the application. A dark theme is made for a
// dark terminal background, and a light theme for a light one.
type Theme struct {
	Name string

	// Light is true for a theme made for a light terminal background. On
	// such a theme, Base is light and the text colors are dark.
	Light bool

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

var CatppuccinLatte = Theme{
	Name:         "Catppuccin Latte",
	Light:        true,
	Primary:      lipgloss.Color("#8839ef"), // mauve
	Secondary:    lipgloss.Color("#acb0be"), // surface2
	Accent:       lipgloss.Color("#3a9626"), // green #40a02b, darker for text on base
	Error:        lipgloss.Color("#c8102f"), // red #d20f39, which is gray with 256 colors
	Warning:      lipgloss.Color("#b8730c"), // yellow #df8e1d, darker for text on base
	Success:      lipgloss.Color("#3a9626"), // green, darker
	Info:         lipgloss.Color("#1e66f5"), // blue
	Base:         lipgloss.Color("#eff1f5"), // base
	Surface:      lipgloss.Color("#ccd0da"), // surface0
	Overlay:      lipgloss.Color("#bcc0cc"), // surface1
	Muted:        lipgloss.Color("#7c7f93"), // overlay2
	Text:         lipgloss.Color("#4c4f69"), // text
	SubText:      lipgloss.Color("#6c6f85"), // subtext0
	BrightText:   lipgloss.Color("#11111b"),
	GradientFrom: lipgloss.Color("#8839ef"), // mauve
	GradientTo:   lipgloss.Color("#1e66f5"), // blue
	ChromaStyle:  "catppuccin-latte",
	StateRunning: lipgloss.Color("#3a9626"), // green, darker
	StateStopped: lipgloss.Color("#c8102f"), // red, see Error
	StatePending: lipgloss.Color("#b8730c"), // yellow, darker
}

var TokyoNightDay = Theme{
	Name:         "Tokyo Night Day",
	Light:        true,
	Primary:      lipgloss.Color("#2e7de9"), // blue
	Secondary:    lipgloss.Color("#a8aecb"), // fg_gutter
	Accent:       lipgloss.Color("#587539"), // green
	Error:        lipgloss.Color("#f52a65"), // red
	Warning:      lipgloss.Color("#8c6c3e"), // yellow
	Success:      lipgloss.Color("#587539"), // green
	Info:         lipgloss.Color("#2e7de9"), // blue
	Base:         lipgloss.Color("#e1e2e7"), // bg
	Surface:      lipgloss.Color("#d0d5e3"), // bg_dark
	Overlay:      lipgloss.Color("#c4c8da"), // bg_highlight
	Muted:        lipgloss.Color("#68709a"), // dark5
	Text:         lipgloss.Color("#3760bf"), // fg
	SubText:      lipgloss.Color("#6172b0"), // fg_dark
	BrightText:   lipgloss.Color("#1a1b26"),
	GradientFrom: lipgloss.Color("#2e7de9"), // blue
	GradientTo:   lipgloss.Color("#9854f1"), // purple
	ChromaStyle:  "tokyonight-day",
	StateRunning: lipgloss.Color("#587539"),
	StateStopped: lipgloss.Color("#f52a65"),
	StatePending: lipgloss.Color("#8c6c3e"),
}

// ThemeOrder defines the cycle order for theme switching.
var ThemeOrder = []string{"catppuccin", "catppuccin-latte", "dracula", "nord", "tokyonight", "tokyonight-day"}

// Themes maps theme names to themes.
var Themes = map[string]Theme{
	"catppuccin":       Catppuccin,
	"catppuccin-latte": CatppuccinLatte,
	"dracula":          Dracula,
	"nord":             Nord,
	"tokyonight":       TokyoNight,
	"tokyonight-day":   TokyoNightDay,
}

// AutoTheme is the theme setting that picks a theme for the background of
// the terminal: AutoThemeName(true) on a dark one, AutoThemeName(false) on a
// light one.
const AutoTheme = "auto"

// AutoThemeName returns the name of the theme that the auto setting uses for
// a dark or a light terminal background.
func AutoThemeName(dark bool) string {
	if dark {
		return "catppuccin"
	}
	return "catppuccin-latte"
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
