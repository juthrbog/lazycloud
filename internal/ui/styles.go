package ui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// Styles holds all computed lipgloss styles derived from the active theme.
type Styles struct {
	// Header
	HeaderStyle    lipgloss.Style
	HeaderAccent   lipgloss.Style
	Breadcrumb     lipgloss.Style
	BreadcrumbSep  lipgloss.Style
	HeaderGradient []color.Color

	// Status bar
	StatusBar     lipgloss.Style
	StatusKey     lipgloss.Style
	StatusDesc    lipgloss.Style
	StatusBarBase lipgloss.Style

	// Table
	TableHeader   lipgloss.Style
	TableSelected lipgloss.Style
	TableCell     lipgloss.Style

	// Detail
	DetailKey   lipgloss.Style
	DetailValue lipgloss.Style

	// Messages
	Error   lipgloss.Style
	Warning lipgloss.Style
	Success lipgloss.Style

	// Filter
	FilterPrompt lipgloss.Style

	// Content border
	ContentBorder lipgloss.Style

	// General
	Muted lipgloss.Style
	Title lipgloss.Style
}

// NewStyles builds all styles from the given theme.
func NewStyles(t Theme) Styles {
	return Styles{
		// Header — uses gradient background
		HeaderStyle: lipgloss.NewStyle().
			Foreground(t.BrightText).
			Background(t.Primary).
			Bold(true).
			Padding(0, 1),

		HeaderAccent: lipgloss.NewStyle().
			Foreground(t.Base).
			Background(t.Accent).
			Bold(true).
			Padding(0, 1),

		Breadcrumb: lipgloss.NewStyle().
			Foreground(t.BrightText).
			Background(t.Surface),

		BreadcrumbSep: lipgloss.NewStyle().
			Foreground(t.Muted).
			Background(t.Surface),

		HeaderGradient: lipgloss.Blend1D(60, t.GradientFrom, t.GradientTo),

		// Status bar
		StatusBar: lipgloss.NewStyle().
			Background(t.Surface).
			Foreground(t.SubText).
			Padding(0, 1),

		StatusKey: lipgloss.NewStyle().
			Bold(true).
			Foreground(t.Accent).
			Background(t.Surface),

		StatusDesc: lipgloss.NewStyle().
			Foreground(t.SubText).
			Background(t.Surface),

		StatusBarBase: lipgloss.NewStyle().
			Background(t.Surface),

		// Table
		TableHeader: lipgloss.NewStyle().
			Bold(true).
			Foreground(t.Primary).
			Padding(0, 1),

		TableSelected: lipgloss.NewStyle().
			Background(t.Overlay).
			Foreground(t.BrightText).
			Bold(false),

		TableCell: lipgloss.NewStyle().
			Foreground(t.Text).
			Padding(0, 1),

		// Detail
		DetailKey: lipgloss.NewStyle().
			Bold(true).
			Foreground(t.Primary).
			Width(20),

		DetailValue: lipgloss.NewStyle().
			Foreground(t.Text),

		// Messages
		Error:   lipgloss.NewStyle().Foreground(t.Error),
		Warning: lipgloss.NewStyle().Foreground(t.Warning),
		Success: lipgloss.NewStyle().Foreground(t.Success),

		// Filter
		FilterPrompt: lipgloss.NewStyle().Foreground(t.Accent).Bold(true),

		// Content area border
		ContentBorder: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(t.Secondary),

		// General
		Muted: lipgloss.NewStyle().Foreground(t.Muted),
		Title: lipgloss.NewStyle().Bold(true).Foreground(t.Primary),
	}
}

// S is the global computed styles instance. Initialized from ActiveTheme.
var S = NewStyles(ActiveTheme)

// RebuildStyles rebuilds the global styles from the active theme.
func RebuildStyles() {
	S = NewStyles(ActiveTheme)
}

// GradientText renders text with a horizontal gradient using the theme's gradient colors.
func GradientText(text string, colors []color.Color) string {
	if len(colors) == 0 || len(text) == 0 {
		return text
	}
	runes := []rune(text)
	gradient := lipgloss.Blend1D(len(runes), colors[0], colors[len(colors)-1])

	var b strings.Builder
	for i, r := range runes {
		idx := i
		if idx >= len(gradient) {
			idx = len(gradient) - 1
		}
		b.WriteString(lipgloss.NewStyle().Foreground(gradient[idx]).Render(string(r)))
	}
	return b.String()
}

// --- Legacy exports (kept for backward compat during migration) ---

var (
	ErrorStyle   = lipgloss.NewStyle().Foreground(ActiveTheme.Error)
	WarningStyle = lipgloss.NewStyle().Foreground(ActiveTheme.Warning)
	SuccessStyle = lipgloss.NewStyle().Foreground(ActiveTheme.Success)
)
