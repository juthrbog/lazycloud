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
	Info    lipgloss.Style

	// Filter
	FilterPrompt lipgloss.Style

	// Content border
	ContentBorder lipgloss.Style

	// Tabs
	TabActive   lipgloss.Style
	TabInactive lipgloss.Style

	// Content view
	FormatBadge   lipgloss.Style
	LineNumber    lipgloss.Style
	LinkIndicator lipgloss.Style
	PositionInfo  lipgloss.Style
	ModeIndicator lipgloss.Style

	// Dialogs
	DialogBorder      lipgloss.Style
	DialogErrorBorder lipgloss.Style

	// Picker / suggestions
	PickerOption         lipgloss.Style
	PickerOptionSelected lipgloss.Style
	SuggestionName       lipgloss.Style
	SuggestionDesc       lipgloss.Style
	SuggestionSelected   lipgloss.Style

	// Help overlay
	HelpGroupHeader lipgloss.Style
	HelpKeyColumn   lipgloss.Style
	ReadWriteBadge  lipgloss.Style

	// Command bar
	CommandCursor lipgloss.Style

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
			Foreground(t.Accent),

		StatusDesc: lipgloss.NewStyle().
			Foreground(t.SubText),

		StatusBarBase: lipgloss.NewStyle(),

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
		Info:    lipgloss.NewStyle().Foreground(t.Info),

		// Filter
		FilterPrompt: lipgloss.NewStyle().Foreground(t.Accent).Bold(true),

		// Content area border
		ContentBorder: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(t.Secondary),

		// Tabs
		TabActive:   lipgloss.NewStyle().Foreground(t.Accent).Bold(true),
		TabInactive: lipgloss.NewStyle().Foreground(t.Muted),

		// Content view
		FormatBadge: lipgloss.NewStyle().
			Foreground(t.BrightText).
			Background(t.Secondary).
			Padding(0, 1),
		LineNumber:    lipgloss.NewStyle().Foreground(t.Muted),
		LinkIndicator: lipgloss.NewStyle().Foreground(t.Info),
		PositionInfo:  lipgloss.NewStyle().Foreground(t.Muted),
		ModeIndicator: lipgloss.NewStyle().Foreground(t.Warning).Bold(true),

		// Dialogs
		DialogBorder: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(t.Primary).
			Padding(0, 1),
		DialogErrorBorder: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(t.Error).
			Padding(0, 1),

		// Picker / suggestions
		PickerOption: lipgloss.NewStyle().
			Foreground(t.Text).Padding(0, 2),
		PickerOptionSelected: lipgloss.NewStyle().
			Foreground(t.BrightText).Background(t.Overlay).Bold(true).Padding(0, 2),
		SuggestionName: lipgloss.NewStyle().
			Foreground(t.Text).Width(16),
		SuggestionDesc: lipgloss.NewStyle().
			Foreground(t.Muted),
		SuggestionSelected: lipgloss.NewStyle().
			Foreground(t.BrightText).Bold(true).Width(16),

		// Help overlay
		HelpGroupHeader: lipgloss.NewStyle().Bold(true).Foreground(t.Accent),
		HelpKeyColumn:   lipgloss.NewStyle().Foreground(t.Primary).Width(14),
		ReadWriteBadge:  lipgloss.NewStyle().Foreground(t.Warning).Bold(true),

		// Command bar
		CommandCursor: lipgloss.NewStyle().Foreground(t.Accent),

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
