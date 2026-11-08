package ui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

// L13: THEMING.md says that a fallback is one plain character, not an emoji.
// Emoji render with different widths in different terminals, so a row with
// one moves its columns.
func TestIconFallbacksArePlainCharacters(t *testing.T) {
	icons := map[string]ServiceIcon{
		"S3": IconS3, "EC2": IconEC2, "Cloud": IconCloud, "Shield": IconShield,
		"SQS": IconSQS, "Network": IconNetwork,
		"Running": IconRunning, "Stopped": IconStopped, "Pending": IconPending,
	}
	for name, icon := range icons {
		assert.Equal(t, 1, ansi.StringWidth(icon.Fallback), "%s fallback %q", name, icon.Fallback)
		for _, r := range icon.Fallback {
			// U+1F000 and up holds the emoji blocks, and U+FE0F asks for
			// the emoji form of the character before it.
			assert.Less(t, r, rune(0x1F000), "%s fallback %q is an emoji", name, icon.Fallback)
			assert.NotEqual(t, rune(0xFE0F), r, "%s fallback %q asks for an emoji", name, icon.Fallback)
		}
	}
}
