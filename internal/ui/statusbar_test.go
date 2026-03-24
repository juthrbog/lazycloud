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
		Keys: []KeyHint{
			{Key: "enter", Desc: "select"},
			{Key: "m", Desc: "manage", Mode: ModeReadWrite},
			{Key: "/", Desc: "filter"},
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
		Keys: []KeyHint{
			{Key: "enter", Desc: "select"},
			{Key: "m", Desc: "manage", Mode: ModeReadWrite},
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
		Keys: []KeyHint{
			{Key: "enter", Desc: "select"},
			{Key: "x", Desc: "ro-only", Mode: ModeReadOnly},
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
			Keys:  []KeyHint{{Key: "q", Desc: "quit", Mode: ModeAny}},
			Width: 80,
		}
		bar := RenderStatusBar(data)
		assert.Contains(t, bar, "quit")
	}
	ReadOnly = true
}
