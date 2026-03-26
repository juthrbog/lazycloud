package ui

import "charm.land/bubbles/v2/table"

// WidthTier represents a terminal width category for responsive layout.
type WidthTier int

const (
	TierNarrow WidthTier = iota // < 80 cols: table only, no detail panel
	TierMedium                  // 80-119 cols: full table, no detail panel
	TierWide                    // >= 120 cols: table + side detail panel
)

// GetWidthTier returns the layout tier for a given terminal width.
func GetWidthTier(width int) WidthTier {
	if width < 80 {
		return TierNarrow
	}
	if width < 120 {
		return TierMedium
	}
	return TierWide
}

// MinTableRows is the minimum number of rows a table should display.
// If the terminal is too short, hide the header or status bar before
// shrinking the table below this threshold.
const MinTableRows = 5

// ColumnsFit reports whether the given columns fit within the available width,
// accounting for the bubbles table's default cell padding (1 char each side).
func ColumnsFit(cols []table.Column, width int) bool {
	total := 0
	for _, c := range cols {
		total += c.Width + 2 // +2 for default Padding(0,1) left+right
	}
	return total <= width
}
