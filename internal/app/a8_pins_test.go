package app

import (
	"fmt"
	"image/color"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The tests in this file pin the theme switch through A8
// (docs/design/a8-theme-switch.md). PR 0 pins each hazard as a test named
// TestA8Hazard_.... The PR that fixes a hazard flips its test and renames it
// TestA8_<hazard>_<new behavior>. A test named TestA8Keep_... pins behavior
// that A8 keeps.

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

// H1 (L17). A theme switch below the home view closes the views, the panel
// and the back history. The loaded rows are gone.
func TestA8Hazard_H1_SwitchBelowHomeResetsTheViews(t *testing.T) {
	a8Theme(t)
	m := ec2WithRows(t)
	m = followLink(t, m, "eventlog", nil)
	require.Equal(t, 1, historyLenOf(m))
	require.Equal(t, 3, m.nav.Depth())

	m, _ = m.applyTheme("nord")

	assert.Equal(t, ui.Nord.Name, ui.ActiveTheme.Name)
	assert.Equal(t, 1, m.nav.Depth(), "only the home view stays")
	assert.False(t, panelShown(m))
	assert.Zero(t, historyLenOf(m))
	assert.NotContains(t, screen(m), "i-abc")
}

// H1 (L17). A theme switch closes an open form.
func TestA8Hazard_H1_SwitchClosesAForm(t *testing.T) {
	a8Theme(t)
	m := ec2WithRows(t)
	m = openA6Form(t, m)

	m, _ = m.applyTheme("nord")

	assert.Equal(t, "home", m.nav.Current().ID())
	assert.Empty(t, m.forms)
}

// H2. Below the home view, the theme picker asks first.
func TestA8Hazard_H2_PickerAsksBelowHome(t *testing.T) {
	a8Theme(t)
	m := ec2WithRows(t)
	before := ui.ActiveTheme.Name

	m, _ = update(t, m, press("T"))
	require.True(t, m.picker.Visible())
	m, _ = update(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = answer(t, m, keyEnter)

	assert.True(t, m.confirm.Visible(), "the switch asks first")
	assert.Equal(t, before, ui.ActiveTheme.Name)
}

// H3. A theme switch declines an open prompt and closes it.
func TestA8Hazard_H3_SwitchDeclinesAnOpenPrompt(t *testing.T) {
	a8Theme(t)
	m := newTestModel(140, 40)
	var replies []string
	m.askQuick("Sure?",
		func(m Model) (Model, tea.Cmd) { replies = append(replies, "yes"); return m, nil },
		func(m Model) (Model, tea.Cmd) { replies = append(replies, "no"); return m, nil })
	require.True(t, m.confirm.Visible())

	m, cmd := m.applyTheme("nord")
	pump(t, m, cmd)

	assert.False(t, m.confirm.Visible())
	assert.Equal(t, []string{"no"}, replies)
}

// H4 (L34). With the auto setting, a reply of the terminal that comes after
// the user opened views closes them.
func TestA8Hazard_H4_LateReplyResetsTheViews(t *testing.T) {
	a8Theme(t)
	m := ec2WithRows(t)
	m.autoTheme = true

	m, _ = update(t, m, lightBackground)

	assert.Equal(t, ui.CatppuccinLatte.Name, ui.ActiveTheme.Name)
	assert.Equal(t, 1, m.nav.Depth())
	assert.False(t, panelShown(m))
}

// H5 (L34). A reply of the terminal that comes after the user picked a
// theme replaces that theme.
func TestA8Hazard_H5_LateReplyReplacesAPickedTheme(t *testing.T) {
	a8Theme(t)
	m := newTestModel(140, 40)
	m.autoTheme = true
	m, _ = m.applyTheme("nord")
	require.Equal(t, ui.Nord.Name, ui.ActiveTheme.Name)

	update(t, m, lightBackground)

	assert.Equal(t, ui.CatppuccinLatte.Name, ui.ActiveTheme.Name)
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
