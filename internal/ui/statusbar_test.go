package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func init() {
	RebuildStyles()
}

func TestRenderStatusBarFiltersReadWriteInReadOnly(t *testing.T) {
	SetReadOnly(true)
	defer func() { SetReadOnly(true) }()

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
	SetReadOnly(false)
	defer func() { SetReadOnly(true) }()

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
	SetReadOnly(false)
	defer func() { SetReadOnly(true) }()

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
		SetReadOnly(ro)
		data := StatusBarData{
			Keys:  []HintBinding{NewHintBinding([]string{"q"}, "q", "quit")},
			Width: 80,
		}
		bar := RenderStatusBar(data)
		assert.Contains(t, bar, "quit")
	}
	SetReadOnly(true)
}
