package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetWidthTier(t *testing.T) {
	tests := []struct {
		width int
		want  WidthTier
	}{
		{0, TierNarrow},
		{40, TierNarrow},
		{79, TierNarrow},
		{80, TierMedium},
		{100, TierMedium},
		{119, TierMedium},
		{120, TierWide},
		{200, TierWide},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, GetWidthTier(tt.width), "width=%d", tt.width)
	}
}

func TestMinTableRowsConstant(t *testing.T) {
	assert.Equal(t, 5, MinTableRows)
}

func TestDistributeWidths_AllFixed(t *testing.T) {
	cols := []Column{
		{Title: "A", Width: 10, Weight: 0},
		{Title: "B", Width: 20, Weight: 0},
	}
	result := DistributeWidths(cols, 200)
	assert.Equal(t, 10, result[0].Width)
	assert.Equal(t, 20, result[1].Width)
}

func TestDistributeWidths_MixedWeights(t *testing.T) {
	cols := []Column{
		{Title: "ID", Width: 10, Weight: 0},    // fixed
		{Title: "Name", Width: 20, Weight: 2},   // flex
		{Title: "Count", Width: 10, Weight: 0},  // fixed
		{Title: "Desc", Width: 20, Weight: 1},   // flex
	}
	// Total base: (10+2)+(20+2)+(10+2)+(20+2) = 68, extra = 100-68 = 32
	// Name gets 2/3 of 32 ≈ 21, Desc gets 1/3 of 32 ≈ 10
	result := DistributeWidths(cols, 100)
	assert.Equal(t, 10, result[0].Width, "fixed column unchanged")
	assert.Equal(t, 10, result[2].Width, "fixed column unchanged")
	assert.Greater(t, result[1].Width, 20, "Name should grow")
	assert.Greater(t, result[3].Width, 20, "Desc should grow")
	// Total consumed should equal totalWidth
	total := 0
	for _, c := range result {
		total += c.Width + cellPadding
	}
	assert.Equal(t, 100, total, "all space should be used")
}

func TestDistributeWidths_MaxWidthCap(t *testing.T) {
	cols := []Column{
		{Title: "A", Width: 10, Weight: 1, MaxWidth: 15},
		{Title: "B", Width: 10, Weight: 1},
	}
	// Total base: (10+2)+(10+2) = 24, extra = 60-24 = 36
	// A capped at 15 (gets 5), remaining 31 goes to B
	result := DistributeWidths(cols, 60)
	assert.Equal(t, 15, result[0].Width, "A capped at MaxWidth")
	assert.Equal(t, 41, result[1].Width, "B gets remaining space")
}

func TestDistributeWidths_NarrowTerminal(t *testing.T) {
	cols := []Column{
		{Title: "A", Width: 20, Weight: 1},
		{Title: "B", Width: 20, Weight: 1},
	}
	// Total base: (20+2)+(20+2) = 44, terminal only 30 → shrinks proportionally
	result := DistributeWidths(cols, 30)
	assert.Less(t, result[0].Width, 20, "A should shrink")
	assert.Equal(t, result[0].Width, result[1].Width, "equal base widths shrink equally")
}

func TestDistributeWidths_SingleFlexColumn(t *testing.T) {
	cols := []Column{
		{Title: "Name", Width: 20, Weight: 1},
	}
	// Total base: 20+2 = 22, extra = 80-22 = 58
	result := DistributeWidths(cols, 80)
	assert.Equal(t, 78, result[0].Width, "single column gets all extra")
}

func TestDistributeWidths_Shrink(t *testing.T) {
	cols := []Column{
		{Title: "A", Width: 20},
		{Title: "B", Width: 10},
	}
	// Total base+pad: (20+2)+(10+2) = 34, terminal only 24 → deficit 10
	result := DistributeWidths(cols, 24)
	// A (wider) should shrink more than B
	assert.Less(t, result[0].Width, 20, "A should shrink")
	assert.Less(t, result[1].Width, 10, "B should shrink")
	assert.Greater(t, result[0].Width, result[1].Width, "A still wider than B")
	assert.GreaterOrEqual(t, result[0].Width, 2, "minimum width preserved")
	assert.GreaterOrEqual(t, result[1].Width, 2, "minimum width preserved")
}

func TestBestFitTier(t *testing.T) {
	colsFn := func(tier WidthTier) []Column {
		switch tier {
		case TierNarrow:
			return []Column{{Title: "A", Width: 20}, {Title: "B", Width: 20}} // base+pad=44
		case TierMedium:
			return []Column{{Title: "A", Width: 20}, {Title: "B", Width: 20}, {Title: "C", Width: 20}} // base+pad=66
		default:
			return []Column{{Title: "A", Width: 20}, {Title: "B", Width: 20}, {Title: "C", Width: 20}, {Title: "D", Width: 20}} // base+pad=88
		}
	}

	// Wide terminal: gets TierWide columns
	cols, tier := BestFitTier(200, colsFn)
	assert.Equal(t, TierWide, tier)
	assert.Equal(t, 4, len(cols))

	// Medium terminal where Wide doesn't fit: cascades to TierMedium
	cols, tier = BestFitTier(80, colsFn)
	assert.Equal(t, TierMedium, tier)
	assert.Equal(t, 3, len(cols))

	// Narrow terminal where Medium doesn't fit: cascades to TierNarrow
	cols, tier = BestFitTier(50, colsFn)
	assert.Equal(t, TierNarrow, tier)
	assert.Equal(t, 2, len(cols))
}

func TestColumnsFit(t *testing.T) {
	cols := []Column{
		{Title: "A", Width: 10},
		{Title: "B", Width: 20},
	}
	assert.True(t, ColumnsFit(cols, 34))   // exact fit: (10+2)+(20+2) = 34
	assert.True(t, ColumnsFit(cols, 100))  // plenty of room
	assert.False(t, ColumnsFit(cols, 33)) // too narrow
}
