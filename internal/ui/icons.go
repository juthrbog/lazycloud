package ui

import (
	"image/color"
	"strconv"
	"strings"

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
	IconS3      = ServiceIcon{Nerd: "\U000f01bc", Fallback: "◇"} // nf-md-bucket
	IconEC2     = ServiceIcon{Nerd: "\U000f01c4", Fallback: "◈"} // nf-md-server
	IconCloud   = ServiceIcon{Nerd: "\U000f015f", Fallback: "☁"} // nf-md-cloud
	IconShield  = ServiceIcon{Nerd: "\U000f0498", Fallback: "⛨"} // nf-md-shield
	IconSQS     = ServiceIcon{Nerd: "\U000f01ee", Fallback: "≡"} // nf-md-message_text
	IconNetwork = ServiceIcon{Nerd: "\U000f0317", Fallback: "⊞"} // nf-md-lan
	IconLogs    = ServiceIcon{Nerd: "\U000f0219", Fallback: "¶"} // nf-md-file_document
	IconLambda  = ServiceIcon{Nerd: "\U000f0627", Fallback: "λ"} // nf-md-lambda
	IconECS     = ServiceIcon{Nerd: "\U000f0868", Fallback: "▣"} // nf-md-docker

	// IconLock marks a session that a rule locks to ReadOnly. Its fallback
	// makes the mode badge read RO*.
	IconLock = ServiceIcon{Nerd: " \U000f033e", Fallback: "*"} // nf-md-lock

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

// CountColor colors a count with the warning color when it is not 0. It
// takes the cell text, so that it can be the Style of a count column. Text
// that is not a number, for example "?", stays plain.
func CountColor(count string) string {
	if n, err := strconv.Atoi(count); err != nil || n == 0 {
		return count
	}
	return FgRender(ActiveTheme.Warning, count)
}

// RatioColor colors a "running/desired" count: green when the two numbers
// are equal, yellow when they differ. It takes the cell text, so that it can
// be the Style of a column. Text that does not parse stays plain.
func RatioColor(ratio string) string {
	a, b, ok := strings.Cut(ratio, "/")
	if !ok {
		return ratio
	}
	running, err1 := strconv.Atoi(a)
	desired, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil {
		return ratio
	}
	if running == desired {
		return FgRender(ActiveTheme.StateRunning, ratio)
	}
	return FgRender(ActiveTheme.StatePending, ratio)
}

// RolloutColor colors the rollout state of an ECS deployment: COMPLETED
// green, IN_PROGRESS yellow and FAILED red, with the icons of StateColor.
// Other text stays plain.
func RolloutColor(state string) string {
	t := ActiveTheme
	switch state {
	case "COMPLETED":
		return FgRender(t.StateRunning, IconRunning.Icon()+" "+state)
	case "IN_PROGRESS":
		return FgRender(t.StatePending, IconPending.Icon()+" "+state)
	case "FAILED":
		return FgRender(t.StateStopped, IconStopped.Icon()+" "+state)
	}
	return state
}

// HealthColor colors the health status of an ECS task or container:
// HEALTHY green, UNHEALTHY red and UNKNOWN muted, with the icons of
// StateColor. Other text stays plain.
func HealthColor(health string) string {
	t := ActiveTheme
	switch health {
	case "HEALTHY":
		return FgRender(t.StateRunning, IconRunning.Icon()+" "+health)
	case "UNHEALTHY":
		return FgRender(t.StateStopped, IconStopped.Icon()+" "+health)
	case "UNKNOWN":
		return FgRender(t.Muted, IconPending.Icon()+" "+health)
	}
	return health
}

// StateColor returns a styled state string with the appropriate color and
// icon. It compares without case, so the capitalized states of Lambda and
// of event source mappings ("Active", "Enabled") get a color too. The text
// keeps its case.
func StateColor(state string) string {
	t := ActiveTheme
	switch strings.ToLower(state) {
	case "running", "available", "active", "enabled", "successful":
		return FgRender(t.StateRunning, IconRunning.Icon()+" "+state)
	case "stopped", "terminated", "deleted", "failed", "unavailable", "disabled":
		return FgRender(t.StateStopped, IconStopped.Icon()+" "+state)
	case "pending", "starting", "stopping", "shutting-down", "creating",
		"inprogress", "enabling", "updating", "disabling",
		// The lifecycle states of ECS clusters, services and tasks.
		"provisioning", "activating", "draining", "deactivating", "deprovisioning":
		return FgRender(t.StatePending, IconPending.Icon()+" "+state)
	case "inactive":
		return FgRender(t.Muted, IconStopped.Icon()+" "+state)
	default:
		return state
	}
}
