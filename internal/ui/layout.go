package ui

import (
	"charm.land/bubbles/v2/table"
)

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

// Column defines a table column with flex layout properties.
type Column struct {
	Title    string
	Width    int // base/minimum width
	MaxWidth int // max width cap (0 = no limit)
	Weight   int // flex weight for extra space distribution (0 = fixed)
}

// cellPadding is the horizontal padding the bubbles table adds per cell.
const cellPadding = 2

// ColumnsFit reports whether the given columns fit within the available width,
// accounting for the bubbles table's default cell padding (1 char each side).
func ColumnsFit(cols []Column, width int) bool {
	total := 0
	for _, c := range cols {
		total += c.Width + cellPadding
	}
	return total <= width
}

// DistributeWidths converts flex columns to fixed-width table columns,
// distributing extra terminal space proportionally by weight.
func DistributeWidths(cols []Column, totalWidth int) []table.Column {
	result := make([]table.Column, len(cols))
	widths := make([]int, len(cols))
	for i, c := range cols {
		result[i].Title = c.Title
		widths[i] = c.Width
	}

	used := 0
	for _, w := range widths {
		used += w + cellPadding
	}
	extra := totalWidth - used
	if extra <= 0 {
		for i := range result {
			result[i].Width = widths[i]
		}
		return result
	}

	// Distribute extra space by weight, respecting MaxWidth caps.
	// Loop handles redistribution when columns hit their cap.
	for extra > 0 {
		totalWeight := 0
		for i, c := range cols {
			if c.Weight > 0 && (c.MaxWidth == 0 || widths[i] < c.MaxWidth) {
				totalWeight += c.Weight
			}
		}
		if totalWeight == 0 {
			break
		}

		distributed := 0
		for i, c := range cols {
			if c.Weight <= 0 || (c.MaxWidth > 0 && widths[i] >= c.MaxWidth) {
				continue
			}
			share := extra * c.Weight / totalWeight
			if c.MaxWidth > 0 && widths[i]+share > c.MaxWidth {
				share = c.MaxWidth - widths[i]
			}
			widths[i] += share
			distributed += share
		}

		if distributed == 0 {
			break
		}
		extra -= distributed
	}

	// Give any remainder from integer rounding to the last flexible column.
	if extra > 0 {
		for i := len(cols) - 1; i >= 0; i-- {
			if cols[i].Weight > 0 && (cols[i].MaxWidth == 0 || widths[i] < cols[i].MaxWidth) {
				room := extra
				if cols[i].MaxWidth > 0 {
					room = cols[i].MaxWidth - widths[i]
					if room > extra {
						room = extra
					}
				}
				widths[i] += room
				extra -= room
				if extra == 0 {
					break
				}
			}
		}
	}

	for i := range result {
		result[i].Width = widths[i]
	}
	return result
}
