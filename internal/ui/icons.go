package ui

import (
	"fmt"
	"image/color"

	"github.com/charmbracelet/x/ansi"
)

// ServiceIcon holds a Nerd Font icon and a Unicode fallback.
type ServiceIcon struct {
	Nerd     string // Nerd Font glyph
	Fallback string // plain Unicode fallback
}

// Icon returns the Nerd Font icon if enabled, otherwise the fallback.
func (i ServiceIcon) Icon() string {
	if UseNerdFonts {
		return i.Nerd
	}
	return i.Fallback
}

// UseNerdFonts controls whether Nerd Font icons are used.
// Set to true if the user's terminal has a Nerd Font patched font.
var UseNerdFonts = true

// Service icons
var (
	IconS3    = ServiceIcon{Nerd: "\U000f01bc", Fallback: "◇"} // nf-md-bucket
	IconEC2   = ServiceIcon{Nerd: "\U000f01c4", Fallback: "◈"} // nf-md-server
	IconCloud  = ServiceIcon{Nerd: "\U000f015f", Fallback: "☁"} // nf-md-cloud
	IconShield = ServiceIcon{Nerd: "\U000f0498", Fallback: "🛡"} // nf-md-shield
	IconSQS     = ServiceIcon{Nerd: "\U000f01ee", Fallback: "≡"} // nf-md-message_text
	IconNetwork = ServiceIcon{Nerd: "\U000f0317", Fallback: "⊞"} // nf-md-lan

	// State indicators
	IconRunning = ServiceIcon{Nerd: "\U000f012c", Fallback: "●"} // nf-md-check_circle
	IconStopped = ServiceIcon{Nerd: "\U000f0156", Fallback: "○"} // nf-md-close_circle
	IconPending = ServiceIcon{Nerd: "\U000f0e4e", Fallback: "◌"} // nf-md-clock
)

// FgRender applies a foreground color without a full ANSI reset.
// It uses \x1b[39m (default foreground) instead of \x1b[m (reset all),
// so any background set by an outer style (e.g., table row selection)
// is preserved. Use this instead of lipgloss.Render for table cell values.
func FgRender(c color.Color, s string) string {
	return ansi.Style{}.ForegroundColor(c).String() + s + "\x1b[39m"
}

// CountColor returns a styled count string — warning-colored when non-zero, plain when zero.
func CountColor(count int) string {
	s := fmt.Sprintf("%d", count)
	if count == 0 {
		return s
	}
	return FgRender(ActiveTheme.Warning, s)
}

// StateColor returns a styled state string with the appropriate color and icon.
func StateColor(state string) string {
	t := ActiveTheme
	switch state {
	case "running", "available", "active":
		return FgRender(t.StateRunning, IconRunning.Icon()+" "+state)
	case "stopped", "terminated", "deleted", "failed", "unavailable":
		return FgRender(t.StateStopped, IconStopped.Icon()+" "+state)
	case "pending", "starting", "stopping", "shutting-down", "creating":
		return FgRender(t.StatePending, IconPending.Icon()+" "+state)
	default:
		return state
	}
}
