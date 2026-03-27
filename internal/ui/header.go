package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// HeaderData holds the values needed to render the header bar.
type HeaderData struct {
	Profile     string
	Region      string
	Mode        string // "RO" or "RW"
	Breadcrumbs []string
	Width       int
}

// RenderHeader renders the top bar with gradient branding and profile/region badges.
func RenderHeader(data HeaderData) string {
	t := ActiveTheme
	s := S

	profile := data.Profile
	if profile == "" {
		profile = "default"
	}
	region := data.Region
	if region == "" {
		region = "us-east-1"
	}

	// App title with gradient
	title := " " + IconCloud.Icon() + " LazyCloud "
	titleRendered := GradientText(title, s.HeaderGradient)

	// Profile, region, and mode badges
	profileBadge := s.HeaderStyle.Render(" " + profile + " ")
	regionBadge := s.HeaderAccent.Render(" " + region + " ")

	var modeBadge string
	if data.Mode == "RW" {
		modeBadge = lipgloss.NewStyle().
			Foreground(t.Base).
			Background(t.Warning).
			Bold(true).
			Padding(0, 1).
			Render("RW")
	} else {
		modeBadge = s.HeaderStyle.Render(" RO ")
	}

	// Build the fixed portion (title + badges) and measure remaining space
	// for breadcrumbs. At very narrow widths, progressively hide badges.
	sep := lipgloss.NewStyle().Foreground(t.Muted).Render(" › ")

	var bar string
	if data.Width < 60 {
		// Minimal: title + breadcrumbs only
		crumbs := renderBreadcrumbs(data.Breadcrumbs, sep, t)
		bar = titleRendered + "  " + crumbs
	} else if data.Width < 80 {
		// Compact: drop region badge
		fixed := titleRendered + " " + profileBadge + " " + modeBadge + "  "
		remaining := data.Width - lipgloss.Width(fixed)
		crumbs := truncateBreadcrumbs(data.Breadcrumbs, sep, t, remaining)
		bar = fixed + crumbs
	} else {
		// Full: all badges + breadcrumbs
		fixed := titleRendered + " " + profileBadge + " " + regionBadge + " " + modeBadge + "  "
		remaining := data.Width - lipgloss.Width(fixed)
		crumbs := truncateBreadcrumbs(data.Breadcrumbs, sep, t, remaining)
		bar = fixed + crumbs
	}

	// Fill to full width with surface background
	barWidth := lipgloss.Width(bar)
	if data.Width > barWidth {
		padding := lipgloss.NewStyle().
			Width(data.Width - barWidth).
			Background(t.Base).
			Render("")
		bar += padding
	}

	// Add a thin gradient line below the header
	gradLine := RenderGradientLine(data.Width)

	return bar + "\n\n" + gradLine
}

// renderBreadcrumbs renders the full breadcrumb trail.
func renderBreadcrumbs(crumbs []string, sep string, t Theme) string {
	if len(crumbs) == 0 {
		return ""
	}
	parts := make([]string, len(crumbs))
	for i, c := range crumbs {
		if i == len(crumbs)-1 {
			parts[i] = lipgloss.NewStyle().Foreground(t.BrightText).Bold(true).Render(c)
		} else {
			parts[i] = lipgloss.NewStyle().Foreground(t.SubText).Render(c)
		}
	}
	return strings.Join(parts, sep)
}

// truncateBreadcrumbs renders breadcrumbs, progressively dropping from the left
// (keeping the current location) if they exceed maxWidth.
func truncateBreadcrumbs(crumbs []string, sep string, t Theme, maxWidth int) string {
	full := renderBreadcrumbs(crumbs, sep, t)
	if lipgloss.Width(full) <= maxWidth || len(crumbs) <= 1 {
		return full
	}

	ellipsis := lipgloss.NewStyle().Foreground(t.SubText).Render("…")
	for start := 1; start < len(crumbs); start++ {
		truncated := ellipsis + sep + renderBreadcrumbs(crumbs[start:], sep, t)
		if lipgloss.Width(truncated) <= maxWidth {
			return truncated
		}
	}

	// Only the current crumb
	return lipgloss.NewStyle().Foreground(t.BrightText).Bold(true).Render(crumbs[len(crumbs)-1])
}

// RenderGradientLine creates a thin horizontal gradient line.
func RenderGradientLine(width int) string {
	if width <= 0 {
		return ""
	}
	colors := lipgloss.Blend1D(width, ActiveTheme.GradientFrom, ActiveTheme.GradientTo)
	var b strings.Builder
	for _, c := range colors {
		b.WriteString(lipgloss.NewStyle().Foreground(c).Render("▀"))
	}
	return b.String()
}
