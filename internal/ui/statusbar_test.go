package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func init() {
	RebuildStyles()
}

func TestRenderStatusBarFiltersReadWriteInReadOnly(t *testing.T) {
	ReadOnly = true
	defer func() { ReadOnly = true }()

	data := StatusBarData{
		Keys: []HintBinding{
			NewHintBinding([]string{"enter"}, "enter", "select"),
			NewHintBinding([]string{"m"}, "m", "manage").WithMode(ModeReadWrite),
			NewHintBinding([]string{"/"}, "/", "filter"),
		},
		Width: 80,
	}

	bar := RenderStatusBar(data)
	assert.Contains(t, bar, "select")
	assert.Contains(t, bar, "filter")
	assert.NotContains(t, bar, "manage")
}

func TestRenderStatusBarShowsReadWriteInReadWrite(t *testing.T) {
	ReadOnly = false
	defer func() { ReadOnly = true }()

	data := StatusBarData{
		Keys: []HintBinding{
			NewHintBinding([]string{"enter"}, "enter", "select"),
			NewHintBinding([]string{"m"}, "m", "manage").WithMode(ModeReadWrite),
		},
		Width: 80,
	}

	bar := RenderStatusBar(data)
	assert.Contains(t, bar, "select")
	assert.Contains(t, bar, "manage")
}

func TestRenderStatusBarFiltersReadOnlyInReadWrite(t *testing.T) {
	ReadOnly = false
	defer func() { ReadOnly = true }()

	data := StatusBarData{
		Keys: []HintBinding{
			NewHintBinding([]string{"enter"}, "enter", "select"),
			NewHintBinding([]string{"x"}, "x", "ro-only").WithMode(ModeReadOnly),
		},
		Width: 80,
	}

	bar := RenderStatusBar(data)
	assert.Contains(t, bar, "select")
	assert.NotContains(t, bar, "ro-only")
}

func TestRenderStatusBarModeAnyAlwaysShown(t *testing.T) {
	for _, ro := range []bool{true, false} {
		ReadOnly = ro
		data := StatusBarData{
			Keys:  []HintBinding{NewHintBinding([]string{"q"}, "q", "quit")},
			Width: 80,
		}
		bar := RenderStatusBar(data)
		assert.Contains(t, bar, "quit")
	}
	ReadOnly = true
}
