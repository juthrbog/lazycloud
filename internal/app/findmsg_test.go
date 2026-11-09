package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

type genericMsg[T any] struct{ v T }

func TestFindMsgPrefix(t *testing.T) {
	msgs := []tea.Msg{genericMsg[int]{1}, genericMsg[string]{"x"}}
	got := findMsgPrefix(msgs, "app.genericMsg[", "string]")
	assert.Equal(t, genericMsg[string]{"x"}, got)
	assert.Nil(t, findMsgPrefix(msgs, "app.genericMsg[", "bool]"))
}
