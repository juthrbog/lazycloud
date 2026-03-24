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
