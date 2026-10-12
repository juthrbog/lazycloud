package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

// filterChanges runs cmd and returns the FilterChangedMsg values it produces.
// Commands that wait, such as the cursor blink, are skipped.
func filterChanges(cmd tea.Cmd) []FilterChangedMsg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(50 * time.Millisecond):
		return nil
	}
	var out []FilterChangedMsg
	switch m := msg.(type) {
	case FilterChangedMsg:
		out = append(out, m)
	case tea.BatchMsg:
		for _, c := range m {
			out = append(out, filterChanges(c)...)
		}
	}
	return out
}

// P2: only a change of the text makes the view filter again.
func TestFilterReportsOnlyTextChanges(t *testing.T) {
	f := NewFilter()
	f.Activate()

	f, cmd := f.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	assert.Equal(t, []FilterChangedMsg{{Text: "a"}}, filterChanges(cmd))

	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyLeft}, {Code: tea.KeyRight}, {Code: tea.KeyHome}} {
		f, cmd = f.Update(k)
		assert.Empty(t, filterChanges(cmd), "key %v does not change the text", k)
	}

	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	_, cmd = f.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	assert.Equal(t, []FilterChangedMsg{{Text: ""}}, filterChanges(cmd))
}
