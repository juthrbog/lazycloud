package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// AccountState is what the app knows about the account of the session.
type AccountState int

const (
	AccountChecking    AccountState = iota // STS has not answered
	AccountKnown                           // STS named the account
	AccountLoginNeeded                     // the credentials have expired, or the user must sign in
	AccountUnknown                         // the check failed, for example on a network error
)

// HeaderData holds the values needed to render the header bar.
type HeaderData struct {
	Profile           string
	Region            string
	Mode              string // "RO" or "RW"
	Locked            bool   // a rule locks the session to ReadOnly
	Label             string // the label of a rule, "" for none
	LabelLocks        bool   // the rule of the label locks the session
	Account           string // the account from STS, or from the config while checking
	AccountState      AccountState
	Principal         string // the principal from STS, short for display
	Endpoint          bool   // an endpoint override is set, for example LocalStack
	Breadcrumbs       []string
	CrossNavBackLabel string // non-empty: show right-aligned back indicator with this label
	Width             int
}

// The longest profile and principal that the header shows before it cuts
// them with an ellipsis. A narrow terminal cuts them more.
const (
	maxHeaderProfile   = 24
	maxHeaderPrincipal = 24
	// minHeaderCrumbs is the width that the header keeps for the
	// breadcrumbs before it cuts the profile below its maximum.
	minHeaderCrumbs = 12
	minHeaderText   = 4
)

// RenderHeader renders the top bar with gradient branding and the badges
// of the session. The badges that it shows depend on the width:
//
//   - 120 and up: title, label, profile, account, principal, region, mode.
//   - 80 to 119: label, profile, account, region, mode.
//   - 60 to 79: label, profile, mode.
//   - below 60: label and mode.
//
// The breadcrumbs take the rest of the line.
func RenderHeader(data HeaderData) string {
	t := ActiveTheme
	s := S

	// A breadcrumb can be an S3 key or a prefix.
	crumbs := make([]string, len(data.Breadcrumbs))
	for i, c := range data.Breadcrumbs {
		crumbs[i] = Sanitize(c)
	}
	data.Breadcrumbs = crumbs
	data.CrossNavBackLabel = Sanitize(data.CrossNavBackLabel)

	profile := Sanitize(data.Profile)
	if profile == "" {
		profile = "default"
	}
	region := Sanitize(data.Region)
	if region == "" {
		region = "us-east-1"
	}

	sep := lipgloss.NewStyle().Foreground(t.Muted).Render(" › ")

	// Right-aligned cross-nav back indicator
	var backIndicator string
	var backWidth int
	if data.CrossNavBackLabel != "" {
		backIndicator = lipgloss.NewStyle().Foreground(t.Muted).Render(" ← bksp ") +
			lipgloss.NewStyle().Foreground(t.Accent).Render(data.CrossNavBackLabel) +
			" "
		backWidth = lipgloss.Width(backIndicator)
	}

	modeBadge := ModeBadge(data.Mode, data.Locked)
	// The label badge comes first, with its own space after it.
	labelBadge := LabelBadge(data.Label, data.LabelLocks)
	if labelBadge != "" {
		labelBadge += " "
	}

	// The width tier picks the parts. When they do not fit with the
	// shortest profile, the header drops parts, the least important
	// first, so that the label and the mode always fit.
	var p headerParts
	switch {
	case data.Width < 60:
	case data.Width < 80:
		p.profile = true
	case data.Width < 120:
		p = headerParts{profile: true, account: true, endpoint: data.Endpoint, region: true}
	default:
		p = headerParts{title: true, profile: true, account: true, endpoint: data.Endpoint,
			principal: data.Principal != "", region: true}
	}
	var before, after string
	for _, drop := range []func(){
		func() { p.principal = false },
		func() { p.endpoint = false },
		func() { p.title = false },
		func() { p.account = false },
		func() { p.region = false },
		func() { p.profile = false },
		func() { backIndicator, backWidth = "", 0 },
	} {
		before, after = p.render(data, labelBadge, modeBadge, region)
		need := lipgloss.Width(before) + lipgloss.Width(after) + backWidth
		if p.profile {
			need += minHeaderText
		}
		if need <= data.Width {
			break
		}
		drop()
	}
	before, after = p.render(data, labelBadge, modeBadge, region)
	fixed := before + after
	if p.profile {
		room := data.Width - lipgloss.Width(fixed) - backWidth
		if len(data.Breadcrumbs) > 0 {
			room -= minHeaderCrumbs
		}
		fixed = before + s.HeaderProfile.Render(cut(profile, min(maxHeaderProfile, max(room, minHeaderText)))) + after
	}
	remaining := max(data.Width-lipgloss.Width(fixed)-backWidth, 0)
	crumbBar := truncateBreadcrumbs(data.Breadcrumbs, sep, t, remaining)
	if lipgloss.Width(crumbBar) > remaining {
		crumbBar = ansi.Truncate(crumbBar, remaining, "…")
	}
	bar := fixed + crumbBar

	// Fill remaining space and append right-aligned back indicator
	barWidth := lipgloss.Width(bar)
	remainingWidth := data.Width - barWidth - backWidth
	if remainingWidth > 0 {
		padding := lipgloss.NewStyle().
			Width(remainingWidth).
			Background(t.Base).
			Render("")
		bar += padding
	}
	bar += backIndicator

	// Add a thin gradient line below the header
	gradLine := RenderGradientLine(data.Width)

	return bar + "\n" + gradLine
}

// headerParts are the parts of the header around the label, the profile
// and the mode.
type headerParts struct {
	title, profile, account, endpoint, principal, region bool
}

// render returns the parts before the profile and the parts after it.
//
// Color in the header shows a state: the label, the mode, a sign-in that
// is needed and an endpoint override. The profile, the account, the
// principal and the region are plain text with a dot between them.
func (p headerParts) render(data HeaderData, labelBadge, modeBadge, region string) (before, after string) {
	if p.title {
		before = GradientText(" "+IconCloud.Icon()+" LazyCloud ", S.HeaderGradient) + " "
	}
	before += labelBadge
	if !p.profile {
		return before, modeBadge + "  "
	}
	if before == "" {
		before = " "
	}
	dot := S.HeaderDot.Render(" · ")
	if p.account {
		after += dot + accountText(data)
		if p.endpoint {
			after += " " + lipgloss.NewStyle().Foreground(ActiveTheme.Warning).Render("endpoint")
		}
	}
	if p.principal {
		if text := principalText(data.Principal); text != "" {
			after += dot + text
		}
	}
	if p.region {
		after += dot + S.HeaderText.Render(region)
	}
	return before, after + " " + modeBadge + "  "
}

// cut shortens s to at most width cells, with an ellipsis at the end.
func cut(s string, width int) string {
	return ansi.Truncate(s, width, "…")
}

// accountText renders the account of the session and the state of its
// check. Only "login needed" has a color, because it changes what the
// user can do.
func accountText(data HeaderData) string {
	t := ActiveTheme
	account := Sanitize(data.Account)
	switch data.AccountState {
	case AccountKnown:
		return S.HeaderText.Render(account)
	case AccountLoginNeeded:
		return lipgloss.NewStyle().Foreground(t.Base).Background(t.Error).Bold(true).Padding(0, 1).Render("login needed")
	case AccountUnknown:
		return S.HeaderText.Render("account unknown")
	default:
		if account == "" {
			account = "account"
		}
		return S.HeaderText.Render(account + "?")
	}
}

// principalText renders the principal, cut to its maximum. It is "" for no
// principal.
func principalText(p string) string {
	p = Sanitize(p)
	if p == "" {
		return ""
	}
	return S.HeaderText.Render(cut(p, maxHeaderPrincipal))
}

// ModeBadge renders the mode of a session: RW, RO, or RO with a lock when a
// rule locks the session.
func ModeBadge(mode string, locked bool) string {
	if mode == "RW" {
		return lipgloss.NewStyle().
			Foreground(ActiveTheme.Base).
			Background(ActiveTheme.Warning).
			Bold(true).
			Padding(0, 1).
			Render("RW")
	}
	text := "RO"
	if locked {
		text += IconLock.Icon()
	}
	return S.HeaderStyle.Render(" " + text + " ")
}

// LabelBadge renders the label of a rule, on the error color when its rule
// locks the session and on the accent color otherwise. It returns "" for
// no label.
func LabelBadge(label string, locks bool) string {
	label = Sanitize(label)
	if label == "" {
		return ""
	}
	bg := ActiveTheme.Accent
	if locks {
		bg = ActiveTheme.Error
	}
	return lipgloss.NewStyle().
		Foreground(ActiveTheme.Base).
		Background(bg).
		Bold(true).
		Padding(0, 1).
		Render(label)
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
		b.WriteString(lipgloss.NewStyle().Foreground(c).Background(ActiveTheme.Base).Render("▄"))
	}
	return b.String()
}
