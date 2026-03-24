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

	// Breadcrumbs — last segment is bold + bright to indicate current location
	crumbParts := make([]string, len(data.Breadcrumbs))
	for i, c := range data.Breadcrumbs {
		if i == len(data.Breadcrumbs)-1 {
			crumbParts[i] = lipgloss.NewStyle().
				Foreground(t.BrightText).
				Bold(true).
				Render(c)
		} else {
			crumbParts[i] = lipgloss.NewStyle().
				Foreground(t.SubText).
				Render(c)
		}
	}
	sep := lipgloss.NewStyle().Foreground(t.Muted).Render(" › ")
	crumbs := strings.Join(crumbParts, sep)

	bar := titleRendered + " " + profileBadge + " " + regionBadge + " " + modeBadge + "  " + crumbs

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
	gradLine := renderGradientLine(data.Width)

	return bar + "\n" + gradLine
}

// renderGradientLine creates a thin horizontal gradient line.
func renderGradientLine(width int) string {
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
