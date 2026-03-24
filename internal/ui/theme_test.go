package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAllThemesHaveRequiredFields(t *testing.T) {
	for name, theme := range Themes {
		assert.NotNil(t, theme.Info, "theme %q missing Info color", name)
		assert.NotEmpty(t, theme.ChromaStyle, "theme %q missing ChromaStyle", name)
		assert.NotNil(t, theme.Error, "theme %q missing Error color", name)
		assert.NotNil(t, theme.Warning, "theme %q missing Warning color", name)
		assert.NotNil(t, theme.Success, "theme %q missing Success color", name)
	}
}
