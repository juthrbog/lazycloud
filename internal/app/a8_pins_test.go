package app

import (
	"fmt"
	"image/color"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The tests in this file pin the theme switch through A8
// (docs/design/a8-theme-switch.md). PR 0 pinned each hazard as a test named
// TestA8Hazard_.... PR 2 fixed all of them, flipped their tests and renamed
// them TestA8_<hazard>_<new behavior>. A test named TestA8Keep_... pins
// behavior that A8 keeps.

// a8Theme puts the default theme back after the test.
func a8Theme(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { ui.ActiveTheme = ui.DefaultTheme; ui.RebuildStyles() })
}

// fgParams returns the SGR parameters that set c as the foreground color.
func fgParams(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)
}

// ec2WithRows gives the EC2 list with one running instance and a panel.
func ec2WithRows(t *testing.T) Model {
	t.Helper()
	e := new(awstest.MockEC2Service)
	e.On("ListInstancesPage", mock.Anything, (*string)(nil)).Return(&aws.InstancePage{
		Instances: []aws.Instance{{ID: "i-abc", State: "running"}},
	}, nil)
	m := newTestModel(panelWide, 40)
	m.svc.EC2 = e
	m, cmd := update(t, m, msg.NavigateMsg{ViewID: "ec2_list"})
	m = pump(t, m, cmd)
	require.Contains(t, screen(m), "i-abc")
	openA6Panel(&m, "i-abc")
	return m
}

// --- Hazards ---

// H1 (L17). Fixed in PR 2: a theme switch below the home view keeps the
// views, the panel, the back history and the loaded rows. The rows take the
// new colors.
func TestA8_H1_SwitchBelowHomeKeepsTheViews(t *testing.T) {
	a8Theme(t)
	m := ec2WithRows(t)
	list := m.nav.Current()
	m = followLink(t, m, "eventlog", nil)
	require.Equal(t, 1, historyLenOf(m))
	require.Equal(t, 3, m.nav.Depth())

	m, _ = m.applyTheme("nord")

	assert.Equal(t, ui.Nord.Name, ui.ActiveTheme.Name)
	assert.Equal(t, 3, m.nav.Depth())
	assert.Equal(t, 1, historyLenOf(m))
	rows := list.View().Content
	assert.Contains(t, rows, fgParams(ui.Nord.StateRunning))
	assert.NotContains(t, rows, fgParams(ui.Catppuccin.StateRunning))

	m, _ = update(t, m, keyBackspace)
	assert.Same(t, list, m.nav.Current())
	assert.True(t, panelShown(m), "the saved panel comes back")
	assert.Contains(t, screen(m), "i-abc")
}

// H1 (L17). Fixed in PR 2: a theme switch keeps an open form.
func TestA8_H1_SwitchKeepsAForm(t *testing.T) {
	a8Theme(t)
	m := ec2WithRows(t)
	m = openA6Form(t, m)
	form := m.nav.Current()

	m, _ = m.applyTheme("nord")

	assert.Same(t, form, m.nav.Current())
	assert.Contains(t, form.View().Content, fgParams(ui.Nord.Primary))
}

// H2. Fixed in PR 2: below the home view, the theme picker switches at once.
func TestA8_H2_PickerSwitchesAtOnceBelowHome(t *testing.T) {
	a8Theme(t)
	m := ec2WithRows(t)
	before := ui.ActiveTheme.Name

	m, _ = update(t, m, press("T"))
	require.True(t, m.picker.Visible())
	m, _ = update(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = answer(t, m, keyEnter)

	assert.False(t, m.confirm.Visible(), "no question")
	assert.NotEqual(t, before, ui.ActiveTheme.Name)
	assert.Equal(t, "ec2_list", m.nav.Current().ID())
}

// H3. Fixed in PR 2: a theme switch keeps an open prompt and sends no
// decline.
func TestA8_H3_SwitchKeepsAnOpenPrompt(t *testing.T) {
	a8Theme(t)
	m := newTestModel(140, 40)
	var replies []string
	m.askQuick("Sure?",
		func(m Model) (Model, tea.Cmd) { replies = append(replies, "yes"); return m, nil },
		func(m Model) (Model, tea.Cmd) { replies = append(replies, "no"); return m, nil })
	require.True(t, m.confirm.Visible())

	m, cmd := m.applyTheme("nord")
	m = pump(t, m, cmd)

	assert.True(t, m.confirm.Visible())
	assert.Empty(t, replies)
}

// H4 (L34). Fixed in PR 2: with the auto setting, a reply of the terminal
// that comes after the user opened views switches the theme and keeps them.
func TestA8_H4_LateReplyKeepsTheViews(t *testing.T) {
	a8Theme(t)
	m := ec2WithRows(t)
	m.autoTheme = true

	m, _ = update(t, m, lightBackground)

	assert.Equal(t, ui.CatppuccinLatte.Name, ui.ActiveTheme.Name)
	assert.Equal(t, "ec2_list", m.nav.Current().ID())
	assert.True(t, panelShown(m))
	assert.Contains(t, m.nav.Current().View().Content, fgParams(ui.CatppuccinLatte.StateRunning))
}

// H5 (L34). Fixed in PR 2: a theme that the user picked ends the auto
// setting, so a later reply of the terminal does not replace it.
func TestA8_H5_LateReplyKeepsAPickedTheme(t *testing.T) {
	a8Theme(t)
	m := newTestModel(140, 40)
	m.autoTheme = true
	m, _ = m.applyTheme("nord")
	require.Equal(t, ui.Nord.Name, ui.ActiveTheme.Name)

	update(t, m, lightBackground)

	assert.Equal(t, ui.Nord.Name, ui.ActiveTheme.Name)
}

// Help that is open during a switch takes the new colors.
func TestA8_HelpTakesTheNewTheme(t *testing.T) {
	a8Theme(t)
	m := ec2WithRows(t)
	m.autoTheme = true
	m, _ = update(t, m, press("?"))
	require.Contains(t, m.help.View(), fgParams(ui.Catppuccin.Text))

	m, _ = update(t, m, lightBackground)

	assert.Contains(t, m.help.View(), fgParams(ui.CatppuccinLatte.Text))
	assert.NotContains(t, m.help.View(), fgParams(ui.Catppuccin.Text))
}

// A table made before the color profile arrives draws again without
// colors: its cursor row uses reverse video.
func TestA8_ColorProfileRestylesTheViews(t *testing.T) {
	a8Theme(t)
	t.Cleanup(func() { ui.SetNoColor(false) })
	m := ec2WithRows(t)
	const reverse = "\x1b[7;"
	require.NotContains(t, m.nav.Current().View().Content, reverse)

	m, _ = update(t, m, tea.ColorProfileMsg{Profile: colorprofile.ASCII})

	assert.True(t, ui.NoColor())
	assert.Contains(t, m.nav.Current().View().Content, reverse)
}

// --- Kept ---

// The header takes the colors of the new theme.
func TestA8Keep_HeaderTakesTheNewTheme(t *testing.T) {
	a8Theme(t)
	m := newTestModel(140, 40)
	require.NotContains(t, m.View().Content, fgParams(ui.Nord.Primary))

	m, _ = m.applyTheme("nord")

	assert.Contains(t, m.View().Content, fgParams(ui.Nord.Primary))
}

// A table made after the switch colors its cells with the new theme.
func TestA8Keep_NewTableTakesTheNewTheme(t *testing.T) {
	a8Theme(t)
	newTestModel(140, 40).applyTheme("nord")

	m := ec2WithRows(t)

	list := m.nav.Current().View().Content
	assert.Contains(t, list, fgParams(ui.Nord.StateRunning))
	assert.NotContains(t, list, fgParams(ui.Catppuccin.StateRunning))
}

// A form opened after the switch draws with the new theme.
func TestA8Keep_FormTakesTheNewTheme(t *testing.T) {
	a8Theme(t)
	newTestModel(140, 40).applyTheme("nord")
	m := ec2WithRows(t)

	m = openA6Form(t, m)

	assert.Contains(t, m.nav.Current().View().Content, fgParams(ui.Nord.Primary))
}

// ReadOnly does not change with the theme.
func TestA8Keep_ReadOnlySurvivesASwitch(t *testing.T) {
	a8Theme(t)
	ui.SetReadOnly(false)
	t.Cleanup(func() { ui.SetReadOnly(true) })

	newTestModel(140, 40).applyTheme("nord")

	assert.False(t, ui.IsReadOnly())
}
