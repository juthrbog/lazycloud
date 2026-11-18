package views

import (
	"fmt"
	"image/color"
	"testing"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// useTheme makes th active and puts the default theme back after the test.
func useTheme(t *testing.T, th ui.Theme) {
	t.Helper()
	t.Cleanup(func() { ui.ActiveTheme = ui.DefaultTheme; ui.RebuildStyles() })
	ui.ActiveTheme = th
	ui.RebuildStyles()
}

// fg returns the SGR parameters that set c as the foreground color.
func fg(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)
}

// A8: each view with a table draws its table again with the new theme on
// Restyle. The header shows the primary color of the theme.
func TestViewsWithATableRestyle(t *testing.T) {
	for name, build := range map[string]func() (ui.Restyler, *ui.Table){
		"list core": func() (ui.Restyler, *ui.Table) {
			v := NewEC2List(new(awstest.MockEC2Service), nil)
			return v, &v.table
		},
		"s3 objects": func() (ui.Restyler, *ui.Table) {
			v := NewS3Objects(new(awstest.MockS3Service), "b", "")
			return v, &v.table
		},
		"s3 versions": func() (ui.Restyler, *ui.Table) {
			v := NewS3Versions(new(awstest.MockS3Service), "b", "k")
			return v, &v.table
		},
		"sqs messages": func() (ui.Restyler, *ui.Table) {
			v := NewSQSMessages(new(awstest.MockSQSService), "url", "q", false)
			return v, &v.table
		},
	} {
		t.Run(name, func(t *testing.T) {
			useTheme(t, ui.Catppuccin)
			v, tbl := build()
			tbl.SetSize(100, 10)
			cells := make(table.Row, len(tbl.Columns()))
			cells[0] = "row"
			tbl.SetRows([]ui.Row{{Key: "row", Cells: cells}})
			require.Contains(t, tbl.View(), fg(ui.Catppuccin.Primary))

			useTheme(t, ui.Nord)
			v.Restyle()

			assert.Contains(t, tbl.View(), fg(ui.Nord.Primary))
			assert.NotContains(t, tbl.View(), fg(ui.Catppuccin.Primary))
			assert.Equal(t, "row", tbl.CursorKey())
		})
	}
}

// A8: the event log writes its events again with the new theme on Restyle.
func TestEventLogRestyle(t *testing.T) {
	useTheme(t, ui.Catppuccin)
	eventlog.Warnf(eventlog.CatUI, "A8 restyle")
	e := NewEventLog()
	e.Init()
	e.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	require.Contains(t, e.View().Content, fg(ui.Catppuccin.Warning))

	useTheme(t, ui.Nord)
	require.Contains(t, e.View().Content, fg(ui.Catppuccin.Warning), "the lines keep the old colors until Restyle")
	e.Restyle()

	assert.Contains(t, e.View().Content, fg(ui.Nord.Warning))
	assert.NotContains(t, e.View().Content, fg(ui.Catppuccin.Warning))
}
