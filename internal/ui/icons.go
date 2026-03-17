package ui

import "charm.land/lipgloss/v2"

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
	IconEC2        = ServiceIcon{Nerd: "\U000f035b", Fallback: "◆"} // nf-md-server
	IconS3         = ServiceIcon{Nerd: "\U000f01bc", Fallback: "◇"} // nf-md-bucket
	IconECS        = ServiceIcon{Nerd: "\U000f0868", Fallback: "▣"} // nf-md-docker
	IconLambda     = ServiceIcon{Nerd: "\U000f0627", Fallback: "λ"} // nf-md-lambda
	IconIAM        = ServiceIcon{Nerd: "\U000f0f17", Fallback: "⊕"} // nf-md-shield_account
	IconRDS        = ServiceIcon{Nerd: "\U000f01bc", Fallback: "⊞"} // nf-md-database
	IconCloudWatch = ServiceIcon{Nerd: "\U000f0209", Fallback: "◈"} // nf-md-chart_line
	IconCloud      = ServiceIcon{Nerd: "\U000f015f", Fallback: "☁"} // nf-md-cloud

	// State indicators
	IconRunning = ServiceIcon{Nerd: "\U000f012c", Fallback: "●"} // nf-md-check_circle
	IconStopped = ServiceIcon{Nerd: "\U000f0156", Fallback: "○"} // nf-md-close_circle
	IconPending = ServiceIcon{Nerd: "\U000f0e4e", Fallback: "◌"} // nf-md-clock
)

// ServiceIcons maps service view IDs to their icons.
var ServiceIcons = map[string]ServiceIcon{
	"ec2_list":       IconEC2,
	"s3_list":        IconS3,
	"ecs_clusters":   IconECS,
	"lambda_list":    IconLambda,
	"iam_roles":      IconIAM,
	"rds_list":       IconRDS,
	"cloudwatch_logs": IconCloudWatch,
}

// StateColor returns a styled state string with the appropriate color and icon.
func StateColor(state string) string {
	t := ActiveTheme
	switch state {
	case "running", "available", "active":
		return lipgloss.NewStyle().Foreground(t.StateRunning).Render(IconRunning.Icon() + " " + state)
	case "stopped", "terminated", "deleted":
		return lipgloss.NewStyle().Foreground(t.StateStopped).Render(IconStopped.Icon() + " " + state)
	case "pending", "starting", "stopping", "creating":
		return lipgloss.NewStyle().Foreground(t.StatePending).Render(IconPending.Icon() + " " + state)
	default:
		return state
	}
}
