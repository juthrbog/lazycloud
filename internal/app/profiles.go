package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/charmbracelet/x/ansi"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// profilesMsg carries the info of each profile, read in a command, for the
// profile picker.
type profilesMsg struct {
	infos []aws.ProfileInfo
	now   time.Time
}

// showProfilePicker reads the profiles in a command, because it reads the
// AWS files and the token caches. The picker opens when profilesMsg comes.
func (m *Model) showProfilePicker() tea.Cmd {
	return func() tea.Msg {
		now := time.Now()
		return profilesMsg{infos: aws.DescribeProfiles(now), now: now}
	}
}

// openProfilePicker opens the picker with one row for each profile. A
// prompt that opened while the profiles loaded wins, and the picker does
// not open.
func (m Model) openProfilePicker(msg profilesMsg) (Model, tea.Cmd) {
	if len(msg.infos) == 0 {
		_, cmd := m.toasts.Add("No profiles found in ~/.aws/config", ui.ToastError, 0)
		return m, cmd
	}
	if m.promptOpen() {
		eventlog.Debugf(eventlog.CatUI, "Profile picker not opened: another prompt is open")
		return m, nil
	}
	byName := make(map[string]aws.ProfileInfo, len(msg.infos))
	currentIdx := 0
	for i, info := range msg.infos {
		byName[info.Name] = info
		if info.Name == m.config.AWS.Profile {
			currentIdx = i
		}
	}
	options := m.profileRows(msg.infos, msg.now)
	cmd := m.pick("Select Profile", options, currentIdx, onPicked(func(m Model, name string) (Model, tea.Cmd) {
		return m.pickProfile(byName[name])
	}))
	return m, cmd
}

// pickProfile switches to the profile that the user picked. A profile that
// does not load, or that needs an MFA code, cannot give a session, so the
// app says why and keeps the session. For a profile that is not signed in,
// the app offers a sign-in first.
func (m Model) pickProfile(info aws.ProfileInfo) (Model, tea.Cmd) {
	var text string
	switch {
	case info.Kind == aws.KindInvalid:
		text = fmt.Sprintf("Cannot use profile %s: %s", info.Name, aws.ErrorText(info.Err))
	case info.MFA:
		text = fmt.Sprintf("Cannot use profile %s: %s", info.Name, aws.MFANotSupported)
	case needsSignIn(info):
		return m.offerSignIn(info)
	default:
		return m.applyProfile(info.Name)
	}
	eventlog.Warnf(eventlog.CatConfig, "%s", text)
	_, cmd := m.toasts.Add(text, ui.ToastError, 0)
	return m, cmd
}

// Column widths of the profile picker.
const (
	maxProfileCol = 24
	kindCol       = 12
	accountCol    = 13
	tokenCol      = 20
)

// profileRows builds one row for each profile: the name, the kind of
// credential, the account from the config, the token state, and the label
// and lock of the rules. All AWS text goes through ui.Sanitize. Below 80
// columns, the account column goes.
func (m Model) profileRows(infos []aws.ProfileInfo, now time.Time) []ui.PickerOption {
	nameCol := 0
	for _, info := range infos {
		nameCol = max(nameCol, lipgloss.Width(ui.Sanitize(info.Name))+2) // room for " ●"
	}
	nameCol = min(nameCol, maxProfileCol)
	withAccount := m.width >= 80

	options := make([]ui.PickerOption, 0, len(infos))
	for _, info := range infos {
		name := ui.Sanitize(info.Name)
		if info.Name == m.config.AWS.Profile {
			name += " ●"
		}
		row := pad(name, nameCol) + " "
		if info.Kind == aws.KindInvalid {
			row += "invalid: " + loadErrorClass(info.Err)
			options = append(options, ui.PickerOption{Label: row, Value: info.Name})
			continue
		}
		kind := info.Kind.String()
		if info.Role {
			kind = "role>" + kind
		}
		row += pad(kind, kindCol) + " "
		if withAccount {
			account := ui.Sanitize(info.Account)
			if account == "" {
				account = "-"
			}
			row += pad(account, accountCol) + " "
		}
		token := info.TokenText(now)
		if info.MFA {
			token = "mfa (not supported)"
		}
		row += pad(token, tokenCol)
		match := m.config.MatchSession(info.Name, info.Account)
		if match.Label != "" {
			row += " " + ui.Sanitize(match.Label)
		}
		if match.Lock != "" {
			row += " RO" + ui.IconLock.Icon()
		}
		options = append(options, ui.PickerOption{Label: strings.TrimRight(row, " "), Value: info.Name})
	}
	return options
}

// pad truncates s to w cells with an ellipsis, or fills it with spaces to
// w cells.
func pad(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

// loadErrorClass names the kind of a load error of a profile in a few
// words for a picker row. The toast of a pick shows the full error.
func loadErrorClass(err error) string {
	var roleErr config.SharedConfigAssumeRoleError
	switch {
	case errors.As(err, &roleErr):
		return "source_profile"
	case err != nil && strings.Contains(err.Error(), "SSO session"):
		return "sso-session"
	case err != nil && strings.Contains(err.Error(), "role_arn"):
		return "role_arn"
	}
	return "config"
}
