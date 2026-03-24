package ui

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
