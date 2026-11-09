package views

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

func TestPendingConfirm_TakeConfirmedReply(t *testing.T) {
	var p pendingConfirm
	p.hold([]string{"a", "b"})
	cmd := p.ask(msg.RequestConfirmMsg{Action: "x_stop", Message: "Stop?"})
	assert.Equal(t, msg.RequestConfirmMsg{Action: "x_stop", Message: "Stop?"}, cmd())

	keys, ok := p.take(ui.ConfirmResultMsg{Confirmed: true, Action: "x_stop"})
	require.True(t, ok)
	assert.Equal(t, []string{"a", "b"}, keys)

	// A second reply finds nothing.
	_, ok = p.take(ui.ConfirmResultMsg{Confirmed: true, Action: "x_stop"})
	assert.False(t, ok)
}

func TestPendingConfirm_OtherRepliesClear(t *testing.T) {
	replies := map[string]ui.ConfirmResultMsg{
		"decline":        {Confirmed: false, Action: "x_stop"},
		"skipped prompt": {},
		"other action":   {Confirmed: true, Action: "x_delete"},
	}
	for name, reply := range replies {
		t.Run(name, func(t *testing.T) {
			var p pendingConfirm
			p.hold([]string{"a"})
			p.ask(msg.RequestConfirmMsg{Action: "x_stop"})
			_, ok := p.take(reply)
			assert.False(t, ok)
			_, ok = p.take(ui.ConfirmResultMsg{Confirmed: true, Action: "x_stop"})
			assert.False(t, ok, "the targets are gone")
		})
	}
}

func TestPendingConfirm_NoAskNoTake(t *testing.T) {
	var p pendingConfirm
	p.hold([]string{"a"})
	_, ok := p.take(ui.ConfirmResultMsg{Confirmed: true, Action: ""})
	assert.False(t, ok, "a held target with no request is not taken")

	p.hold(nil)
	p.ask(msg.RequestConfirmMsg{Action: "x_stop"})
	_, ok = p.take(ui.ConfirmResultMsg{Confirmed: true, Action: "x_stop"})
	assert.False(t, ok, "no targets")
}

func TestPendingConfirm_HoldResetsAction(t *testing.T) {
	var p pendingConfirm
	p.hold([]string{"a"})
	p.ask(msg.RequestConfirmMsg{Action: "x_stop"})
	p.hold([]string{"b"})
	_, ok := p.take(ui.ConfirmResultMsg{Confirmed: true, Action: "x_stop"})
	assert.False(t, ok, "a new key press drops the old request")
}

func TestConfirmMessage(t *testing.T) {
	assert.Equal(t, "Stop 2 instance(s)?\n  web (i-1)\n  db (i-2)\n",
		confirmMessage("Stop", 2, "instance(s)", []string{"web (i-1)", "db (i-2)"}, ""))
	assert.Equal(t, "Purge 1 queue(s)?\n  orders\nThis cannot be undone.",
		confirmMessage("Purge", 1, "queue(s)", []string{"orders"}, "This cannot be undone."))
}

func TestConfirmMessage_NamesCappedAndSanitized(t *testing.T) {
	names := make([]string, 12)
	for i := range names {
		names[i] = fmt.Sprintf("q%d", i)
	}
	names[0] = "evil\x1b[2Jname\nline"
	got := confirmMessage("Delete", 12, "queue(s)", names, "")
	assert.NotContains(t, got, "\x1b")
	assert.Contains(t, got, "  and 2 more\n")
	assert.NotContains(t, got, "q10")
	assert.Len(t, strings.Split(strings.TrimSuffix(got, "\n"), "\n"), 1+maxPromptNames+1, "title, 10 names, the rest line")
}

func TestRunEach_GoesOnAfterFailure(t *testing.T) {
	var calls []string
	done, failed := runEach(context.Background(), []string{"a", "b", "c"},
		map[string]string{"b": "bee"},
		func(_ context.Context, key string) error {
			calls = append(calls, key)
			if key == "b" || key == "c" {
				return errors.New("AccessDenied")
			}
			return nil
		})
	assert.Equal(t, []string{"a", "b", "c"}, calls)
	assert.Equal(t, []string{"a"}, done)
	require.Len(t, failed, 2)
	assert.Equal(t, "bee", failed[0].name)
	assert.Equal(t, "c", failed[1].name, "no name falls back to the key")
}

func TestRunEach_DetachedContextPerKey(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel() // the view is gone
	var deadlines []time.Time
	done, failed := runEach(parent, []string{"a", "b"}, nil, func(ctx context.Context, _ string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		d, ok := ctx.Deadline()
		require.True(t, ok)
		deadlines = append(deadlines, d)
		return nil
	})
	assert.Equal(t, []string{"a", "b"}, done)
	assert.Empty(t, failed)
	require.Len(t, deadlines, 2)
	assert.False(t, deadlines[1].Before(deadlines[0]), "each key gets its own limit")
}
