package views

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// Characterization tests for ami_list (Phase 8, PR 0). They record what the
// view does today, through its public surface, so the port to the shared
// list core (PR 6) shows every change of behavior as a diff of this file.
// A line that records a bug names the divergence row that changes it.

var (
	charAMIA = aws.AMI{ID: "ami-aaa", Name: "alpha-image", OwnerID: "111122223333", Architecture: "x86_64", State: "available", CreationDate: "2026-03-01T08:00:00Z"}
	charAMIB = aws.AMI{ID: "ami-bbb", Name: "beta-image", OwnerID: "111122223333", OwnerAlias: "self", Architecture: "arm64", State: "pending", CreationDate: "2025-12-24T00:00:00Z"}
	charAMIP = aws.AMI{ID: "ami-pub", Name: "amzn-public", OwnerID: "137112412989", OwnerAlias: "amazon", Architecture: "x86_64", State: "available", CreationDate: "2024-05-05T05:05:05Z"}
)

// newCharAMI returns an AMI list that has not loaded yet, with a 160x24
// window. It turns Nerd Font icons off, so the state cells show the plain
// fallback icons.
func newCharAMI(t *testing.T) (*AMIList, *awstest.MockEC2Service) {
	t.Helper()
	old := ui.UseNerdFonts
	ui.UseNerdFonts = false
	t.Cleanup(func() { ui.UseNerdFonts = old })
	svc := new(awstest.MockEC2Service)
	v := NewAMIList(svc)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
	return v, svc
}

// newCharAMILoaded returns an AMI list that loaded amis as its owned AMIs
// through Init and the mock.
func newCharAMILoaded(t *testing.T, amis ...aws.AMI) (*AMIList, *awstest.MockEC2Service) {
	t.Helper()
	v, svc := newCharAMI(t)
	svc.On("ListOwnedAMIs", mock.Anything).Return(amis, nil).Once()
	pump(v, v.Init(), 5)
	require.Equal(t, fmt.Sprintf("%d/%d AMIs (owned)", len(amis), len(amis)), plain(v.Footer()))
	return v, svc
}

// charRun runs cmd and returns its messages without spinner ticks.
func charRun(cmd tea.Cmd) []tea.Msg {
	var out []tea.Msg
	for _, m := range collectMsgs(cmd) {
		if _, tick := m.(spinner.TickMsg); tick {
			continue
		}
		out = append(out, m)
	}
	return out
}

// charRows returns the AMI IDs of the table rows in the order they show,
// with a "*" in front of a selected row.
func charRows(v *AMIList) []string {
	var ids []string
	for line := range strings.SplitSeq(plain(v.View().Content), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		switch {
		case strings.HasPrefix(f[0], "ami-"):
			ids = append(ids, f[0])
		case f[0] == "✓" && len(f) > 1:
			ids = append(ids, "*"+f[1])
		}
	}
	return ids
}

// charHeader returns the header line of the table, trimmed.
func charHeader(v *AMIList) string {
	for line := range strings.SplitSeq(plain(v.View().Content), "\n") {
		if strings.Contains(line, "AMI ID") {
			return strings.Join(strings.Fields(line), " ")
		}
	}
	return ""
}

// charFilterChange runs cmd and returns the ui.FilterChangedMsg it produces.
// It runs the commands of a batch at the same time and does not wait for the
// others (cursor blink), so typing costs no blink delay.
func charFilterChange(t *testing.T, cmd tea.Cmd) ui.FilterChangedMsg {
	t.Helper()
	found := make(chan ui.FilterChangedMsg, 8)
	var run func(c tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch m := c().(type) {
		case ui.FilterChangedMsg:
			found <- m
		case tea.BatchMsg:
			for _, sub := range m {
				go run(sub)
			}
		}
	}
	go run(cmd)
	select {
	case f := <-found:
		return f
	case <-time.After(5 * time.Second):
		require.FailNow(t, "no FilterChangedMsg")
		return ui.FilterChangedMsg{}
	}
}

// charType sends each rune of s as a key press and delivers the filter
// change each one emits back to the view, as the app would.
func charType(t *testing.T, v *AMIList, s string) {
	t.Helper()
	for _, r := range s {
		_, cmd := v.Update(keyPress(r))
		v.Update(charFilterChange(t, cmd))
	}
}

// charSearch opens the search, types q and presses enter. It returns the
// command of enter without running it. It does not deliver the filter
// changes that typing in the search emits (Divergence #32, recorded in
// TestCharAMI_SearchLiveFiltersTable), so the table stays unfiltered.
func charSearch(v *AMIList, q string) tea.Cmd {
	v.Update(keyPress('p'))
	for _, r := range q {
		v.Update(keyPress(r))
	}
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return cmd
}

func charClipboard(msgs []tea.Msg) (string, bool) {
	for _, m := range msgs {
		if fmt.Sprintf("%T", m) == "tea.setClipboardMsg" {
			return fmt.Sprint(m), true
		}
	}
	return "", false
}

// charAMIJSON is the detail text of an AMI: its indented JSON.
func charAMIJSON(t *testing.T, ami aws.AMI) string {
	t.Helper()
	b, err := json.MarshalIndent(ami, "", "  ")
	require.NoError(t, err)
	return string(b)
}

func charKeyMap(v *AMIList) []string {
	var out []string
	for _, h := range v.KeyMap() {
		out = append(out, fmt.Sprintf("%s|%s|%s|mode=%d|cat=%s",
			strings.Join(h.Keys(), ","), h.Help().Key, h.Help().Desc, h.Mode, h.Category))
	}
	return out
}

// --- All views ---

func TestCharAMI_IDAndTitle(t *testing.T) {
	v, _ := newCharAMI(t)
	assert.Equal(t, "ami_list", v.ID())
	assert.Equal(t, "AMIs", v.Title())
}

func TestCharAMI_KeyMapOwned(t *testing.T) {
	v, _ := newCharAMILoaded(t, charAMIA)
	assert.Equal(t, []string{
		"enter|enter/d|details|mode=0|cat=",
		"y|y|copy ID|mode=0|cat=",
		"space|space|select|mode=0|cat=",
		"p|p|search public|mode=0|cat=",
		"s|s/S|sort|mode=0|cat=",
		"/|/|filter|mode=0|cat=",
		"r|r|refresh|mode=0|cat=",
	}, charKeyMap(v))
}

func TestCharAMI_KeyMapSearchResults(t *testing.T) {
	v, svc := newCharAMILoaded(t, charAMIA)
	svc.On("SearchAMIs", mock.Anything, "amzn").Return([]aws.AMI{charAMIP}, nil).Once()
	pump(v, charSearch(v, "amzn"), 5)
	require.Equal(t, `1/1 results for "amzn"`, plain(v.Footer()))

	assert.Equal(t, []string{
		"enter|enter/d|details|mode=0|cat=",
		"y|y|copy ID|mode=0|cat=",
		"space|space|select|mode=0|cat=",
		"p|p|search public|mode=0|cat=",
		"s|s/S|sort|mode=0|cat=",
		"/|/|filter|mode=0|cat=",
		"r|r|refresh|mode=0|cat=",
		"r|r|refresh|mode=0|cat=", // Divergence #33: changes in PR 1.
	}, charKeyMap(v))
}

// No RW keys: every hint is ModeAny, and read-only mode changes nothing.
func TestCharAMI_NoReadWriteKeys(t *testing.T) {
	t.Cleanup(func() { ui.SetReadOnly(false) })
	v, _ := newCharAMILoaded(t, charAMIA)

	ui.SetReadOnly(false)
	rw := charKeyMap(v)
	ui.SetReadOnly(true)
	ro := charKeyMap(v)

	assert.Equal(t, rw, ro)
	for _, h := range v.KeyMap() {
		assert.Equal(t, ui.ModeAny, h.Mode, h.Help().Desc)
	}
}

func TestCharAMI_CapturingInput(t *testing.T) {
	v, _ := newCharAMILoaded(t, charAMIA)
	assert.False(t, v.CapturingInput())

	v.Update(keyPress('/'))
	assert.True(t, v.CapturingInput(), "filter open")
	v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.False(t, v.CapturingInput())

	v.Update(keyPress('p'))
	assert.True(t, v.CapturingInput(), "search open")
	v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.False(t, v.CapturingInput())
}

// Init loads only while the view is loading; a cached view does not reload.
func TestCharAMI_InitLoadsOnlyWhileLoading(t *testing.T) {
	v, svc := newCharAMILoaded(t, charAMIA)
	assert.Nil(t, v.Init(), "a loaded view does not reload")
	svc.AssertNumberOfCalls(t, "ListOwnedAMIs", 1)
	svc.AssertNotCalled(t, "SearchAMIs", mock.Anything, mock.Anything)
}

// Divergence #2: Init deselects (kept: a no-op without a selection after the port).
func TestCharAMI_InitDeselects(t *testing.T) {
	v, _ := newCharAMILoaded(t, charAMIA, charAMIB)
	v.Update(keyPress(' '))
	require.Equal(t, "2/2 AMIs (owned)  (1 selected)", plain(v.Footer()))

	assert.Nil(t, v.Init())
	assert.Equal(t, "2/2 AMIs (owned)", plain(v.Footer()))
}

// A fresh view accepts the first load result (AMI has no gen today).
func TestCharAMI_FreshViewAcceptsFirstLoad(t *testing.T) {
	v, svc := newCharAMI(t)
	svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI{charAMIA, charAMIB}, nil).Once()
	cmd := v.Init()
	require.NotNil(t, cmd)
	deliverAll(v, charRun(cmd)...)
	assert.Equal(t, []string{"ami-aaa", "ami-bbb"}, charRows(v))
	assert.Equal(t, "2/2 AMIs (owned)", plain(v.Footer()))
}

// Divergence #35: AMI has no gen, so the reply that arrives last wins, even
// when it is from the older load. Changes in PR 6.
func TestCharAMI_StaleReplyLastWins(t *testing.T) {
	v, svc := newCharAMILoaded(t, charAMIA)
	svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI{charAMIA, charAMIB}, nil).Once()
	svc.On("SearchAMIs", mock.Anything, "amzn").Return([]aws.AMI{charAMIP}, nil).Once()

	_, owned := v.Update(keyPress('r'))
	search := charSearch(v, "amzn")
	ownedMsgs, searchMsgs := charRun(owned), charRun(search)

	deliverAll(v, searchMsgs...) // the newer load ends first
	assert.Equal(t, `1/1 results for "amzn"`, plain(v.Footer()))
	deliverAll(v, ownedMsgs...) // then the older load
	assert.Equal(t, "2/2 AMIs (owned)", plain(v.Footer()), "Divergence #35: changes in PR 6.")
	assert.Equal(t, []string{"ami-aaa", "ami-bbb"}, charRows(v))
}

// Divergence #9: a stale ErrorMsg from a superseded load is applied. Changes in PR 6.
func TestCharAMI_StaleErrorApplied(t *testing.T) {
	v, svc := newCharAMILoaded(t, charAMIA)
	svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI(nil), errors.New("old failure")).Once()
	svc.On("SearchAMIs", mock.Anything, "amzn").Return([]aws.AMI{charAMIP}, nil).Once()

	_, owned := v.Update(keyPress('r'))
	search := charSearch(v, "amzn")
	ownedMsgs, searchMsgs := charRun(owned), charRun(search)
	deliverAll(v, searchMsgs...)
	deliverAll(v, ownedMsgs...)

	assert.Equal(t, "\n  Error: old failure", plain(v.View().Content), "Divergence #9: changes in PR 6.")
}

func TestCharAMI_ErrorContexts(t *testing.T) {
	t.Run("owned", func(t *testing.T) {
		v, svc := newCharAMI(t)
		svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI(nil), errors.New("boom")).Once()
		e, ok := firstOfType[msg.ErrorMsg](charRun(v.Init()))
		require.True(t, ok)
		assert.Equal(t, "listing owned AMIs", e.Context)
		assert.EqualError(t, e.Err, "boom")
	})
	t.Run("search", func(t *testing.T) {
		v, svc := newCharAMILoaded(t, charAMIA)
		svc.On("SearchAMIs", mock.Anything, "amzn").Return([]aws.AMI(nil), errors.New("boom")).Once()
		e, ok := firstOfType[msg.ErrorMsg](charRun(charSearch(v, "amzn")))
		require.True(t, ok)
		assert.Equal(t, "searching AMIs", e.Context)
	})
	t.Run("nil client owned", func(t *testing.T) {
		v := NewAMIList(nil)
		v.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
		e, ok := firstOfType[msg.ErrorMsg](charRun(v.Init()))
		require.True(t, ok)
		assert.Equal(t, "EC2", e.Context)
		assert.EqualError(t, e.Err, "AWS client not initialized")
	})
	t.Run("nil client search", func(t *testing.T) {
		v := NewAMIList(nil)
		v.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
		cmd := charSearch(v, "amzn")
		require.NotNil(t, cmd)
		// Divergence #47 (and #38): search has no nil-service guard and
		// panics. Changes in PR 3.
		assert.Panics(t, func() { collectMsgs(cmd) })
	})
}

// The error view shows "Error: " + the sanitized aws.ErrorText in theme red.
func TestCharAMI_ErrorView(t *testing.T) {
	v, svc := newCharAMI(t)
	svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI(nil), errors.New("denied\x1b[2J")).Once()
	pump(v, v.Init(), 5)

	out := v.View().Content
	assert.Equal(t, "\n  Error: denied␛[2J", plain(out))
	assert.Contains(t, out, fgSGR(ui.ActiveTheme.Error))
	assert.Equal(t, "0/0 AMIs (owned)", plain(v.Footer()))
}

// The error view uses aws.ErrorText, not err.Error().
func TestCharAMI_ErrorViewUsesErrorText(t *testing.T) {
	v, svc := newCharAMI(t)
	apiErr := &smithy.GenericAPIError{Code: "InvalidAMIID.Malformed", Message: "bad id\x1b[2J"}
	svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI(nil), apiErr).Once()
	pump(v, v.Init(), 5)
	assert.Equal(t, "\n  Error: bad id␛[2J (InvalidAMIID.Malformed)", plain(v.View().Content))
}

// A search clears the error too. While the error shows, p opens the search
// but the View shows only the error, not the search prompt.
func TestCharAMI_SearchClearsError(t *testing.T) {
	v, svc := newCharAMILoaded(t, charAMIA)
	v.Update(msg.ErrorMsg{Err: errors.New("denied"), Context: "listing owned AMIs"})
	require.Equal(t, "\n  Error: denied", plain(v.View().Content))

	v.Update(keyPress('p'))
	assert.True(t, v.CapturingInput())
	assert.Equal(t, "\n  Error: denied", plain(v.View().Content))

	svc.On("SearchAMIs", mock.Anything, "amzn").Return([]aws.AMI{charAMIP}, nil).Once()
	for _, r := range "amzn" {
		v.Update(keyPress(r))
	}
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	assert.Equal(t, []string{"ami-aaa"}, charRows(v), "the error is gone at once, the old rows show")
	pump(v, cmd, 5)
	assert.Equal(t, []string{"ami-pub"}, charRows(v))
	assert.Equal(t, `1/1 results for "amzn"`, plain(v.Footer()))
}

// Divergence #8: err is cleared only by r (and by a search), not by a load
// result that arrives on its own. Changes in PR 6.
func TestCharAMI_ErrorClearedOnlyByKey(t *testing.T) {
	v, svc := newCharAMI(t)
	svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI{charAMIA}, nil).Once()
	loaded := charRun(v.Init())
	v.Update(msg.ErrorMsg{Err: errors.New("denied"), Context: "x"})
	deliverAll(v, loaded...)

	assert.Equal(t, "\n  Error: denied", plain(v.View().Content), "Divergence #8: changes in PR 6.")
	assert.Equal(t, "1/1 AMIs (owned)", plain(v.Footer()))

	// r clears it.
	svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI{charAMIA}, nil).Once()
	_, cmd := v.Update(keyPress('r'))
	pump(v, cmd, 5)
	assert.Equal(t, []string{"ami-aaa"}, charRows(v))
}

// The table keeps cursor, selection and sort by key across SetRows (C3).
func TestCharAMI_TableKeepsStateAcrossSetRows(t *testing.T) {
	v, svc := newCharAMILoaded(t, charAMIB, charAMIA)
	v.Update(ui.PickerResultMsg{ID: "sort", Selected: 0, Value: "AMI ID"})
	require.Equal(t, []string{"ami-aaa", "ami-bbb"}, charRows(v))
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // cursor on ami-bbb
	v.Update(keyPress(' '))
	require.Equal(t, []string{"ami-aaa", "*ami-bbb"}, charRows(v))

	// Selective refresh calls SetRows.
	newer := charAMIB
	newer.State = "available"
	svc.On("RefreshAMIs", mock.Anything, []string{"ami-bbb"}).Return([]aws.AMI{newer}, nil).Once()
	_, cmd := v.Update(keyPress('r'))
	pump(v, cmd, 5)
	assert.Equal(t, []string{"ami-aaa", "*ami-bbb"}, charRows(v))

	// A tier change rebuilds the rows.
	v.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	assert.Equal(t, []string{"ami-aaa", "*ami-bbb"}, charRows(v), "sort survives a tier change")
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
	assert.Equal(t, []string{"ami-aaa", "*ami-bbb"}, charRows(v))

	// The cursor stayed on ami-bbb: enter opens it.
	nav, ok := firstOfType[msg.NavigateMsg](charRun(func() tea.Cmd {
		_, c := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		return c
	}()))
	require.True(t, ok)
	// M8: the select marker never leaks into the key, so the AMI is found.
	assert.Equal(t, "beta-image (ami-bbb)", nav.Params["title"])
}

// C4: the filter deselects rows it hides.
func TestCharAMI_FilterDeselectsHiddenRows(t *testing.T) {
	v, _ := newCharAMILoaded(t, charAMIA, charAMIB)
	v.Update(keyPress(' ')) // select ami-aaa
	require.Equal(t, "2/2 AMIs (owned)  (1 selected)", plain(v.Footer()))

	v.Update(keyPress('/'))
	charType(t, v, "be")
	assert.Equal(t, "1/2 AMIs (owned)", plain(v.Footer()))
	assert.Equal(t, []string{"ami-bbb"}, charRows(v))
	assert.Contains(t, plain(v.View().Content), "/ be")

	// esc closes the filter and its command clears the table filter; the
	// hidden row does not get its selection back.
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.NotNil(t, cmd)
	pump(v, cmd, 1)
	assert.False(t, v.CapturingInput())
	assert.Equal(t, "2/2 AMIs (owned)", plain(v.Footer()))
	assert.Equal(t, []string{"ami-aaa", "ami-bbb"}, charRows(v))
}

// enter in the filter closes it and keeps the filter applied.
func TestCharAMI_FilterEnterKeepsFilter(t *testing.T) {
	v, _ := newCharAMILoaded(t, charAMIA, charAMIB)
	v.Update(keyPress('/'))
	charType(t, v, "al")
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Nil(t, cmd)
	assert.False(t, v.CapturingInput())
	assert.Equal(t, "1/2 AMIs (owned)", plain(v.Footer()))
	assert.NotContains(t, plain(v.View().Content), "/ al")
	assert.Equal(t, []string{"ami-aaa"}, charRows(v))
}

func TestCharAMI_SortPicker(t *testing.T) {
	v, _ := newCharAMILoaded(t, charAMIB, charAMIA)

	_, cmd := v.Update(keyPress('s'))
	req, ok := firstOfType[msg.RequestSortPickerMsg](charRun(cmd))
	require.True(t, ok)
	assert.Equal(t, []string{"AMI ID", "Name", "Owner", "Architecture", "State", "Created"}, req.Columns)
	assert.Equal(t, -1, req.CurrentCol)

	v.Update(ui.PickerResultMsg{ID: "other", Selected: 0})
	assert.Equal(t, []string{"ami-bbb", "ami-aaa"}, charRows(v), "a non-sort picker is ignored")

	_, cmd = v.Update(ui.PickerResultMsg{ID: "sort", Selected: 0, Value: "AMI ID"})
	assert.Nil(t, cmd)
	assert.Equal(t, []string{"ami-aaa", "ami-bbb"}, charRows(v))
	assert.Contains(t, charHeader(v), "AMI ID ▲")

	v.Update(keyPress('S'))
	assert.Equal(t, []string{"ami-bbb", "ami-aaa"}, charRows(v))
	assert.Contains(t, charHeader(v), "AMI ID ▼")

	_, cmd = v.Update(keyPress('s'))
	req, _ = firstOfType[msg.RequestSortPickerMsg](charRun(cmd))
	assert.Equal(t, 0, req.CurrentCol)

	v.Update(ui.PickerResultMsg{ID: "sort", Selected: -1, Value: ""})
	assert.Equal(t, []string{"ami-bbb", "ami-aaa"}, charRows(v), "a cancelled picker keeps the sort")

	v.Update(ui.PickerResultMsg{ID: "sort", Selected: 6, Value: "_clear"})
	assert.Equal(t, []string{"ami-bbb", "ami-aaa"}, charRows(v), "load order")
	assert.NotContains(t, charHeader(v), "▲")
	assert.NotContains(t, charHeader(v), "▼")
}

func TestCharAMI_Footer(t *testing.T) {
	t.Run("loading", func(t *testing.T) {
		v, _ := newCharAMI(t)
		assert.Equal(t, "0/0 AMIs (owned)  ⣾  Loading AMIs...", plain(v.Footer()))
	})
	t.Run("after load", func(t *testing.T) {
		v, _ := newCharAMILoaded(t, charAMIA, charAMIB)
		assert.Equal(t, "2/2 AMIs (owned)", plain(v.Footer()))
	})
	t.Run("one selected", func(t *testing.T) {
		v, _ := newCharAMILoaded(t, charAMIA, charAMIB)
		v.Update(keyPress(' '))
		assert.Equal(t, "2/2 AMIs (owned)  (1 selected)", plain(v.Footer()))
	})
	t.Run("search results", func(t *testing.T) {
		v, svc := newCharAMILoaded(t, charAMIA)
		svc.On("SearchAMIs", mock.Anything, `a "b"`).Return([]aws.AMI{charAMIP, charAMIA}, nil).Once()
		pump(v, charSearch(v, `a "b"`), 5)
		assert.Equal(t, `2/2 results for "a \"b\""`, plain(v.Footer()))
	})
	t.Run("searching", func(t *testing.T) {
		v, _ := newCharAMILoaded(t, charAMIA)
		require.NotNil(t, charSearch(v, "amzn"))
		assert.Equal(t, "1/1 AMIs (owned)  ⣾  Searching AMIs...", plain(v.Footer()))
	})
	t.Run("linked", func(t *testing.T) {
		v, svc := newCharAMILoaded(t, charAMIA)
		svc.On("RefreshAMIs", mock.Anything, []string{"ami-pub"}).Return([]aws.AMI{charAMIP}, nil).Once()
		_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "ami-pub"})
		assert.Equal(t, "1/1 AMIs (owned)  ⣾  Loading AMI...", plain(v.Footer()))
		pump(v, cmd, 5)
		assert.Equal(t, "2/2 AMIs (owned + 1 linked)", plain(v.Footer()))
	})
}

// State cells hold plain text; the column Style adds the icon and color.
func TestCharAMI_StateCellStyledByColumn(t *testing.T) {
	v, _ := newCharAMILoaded(t, charAMIA, charAMIB)
	// The cells of the row hold the plain state. No public method returns
	// the cells, and the View shows them only after the column Style ran.
	assert.Equal(t, table.Row{"ami-aaa", "alpha-image", "111122223333", "x86_64", "available", "2026-03-01"},
		v.table.SelectedRow()) // private
	out := v.View().Content
	assert.Contains(t, plain(out), ui.IconRunning.Fallback+" available")
	assert.Contains(t, plain(out), ui.IconPending.Fallback+" pending")
	assert.Contains(t, out, fgSGR(ui.ActiveTheme.StateRunning))
}

// Focus while loading is stored and returns nil; it opens after the load.
func TestCharAMI_PendingFocus(t *testing.T) {
	v, svc := newCharAMI(t)
	svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI{charAMIA, charAMIB}, nil).Once()
	init := v.Init()

	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "ami-bbb"})
	assert.Nil(t, cmd)

	seen := pump(v, init, 5)
	p, ok := firstOfType[msg.TabbedContentMsg](seen)
	require.True(t, ok)
	assert.Equal(t, "beta-image (ami-bbb)", p.PanelTitle)
	svc.AssertNotCalled(t, "RefreshAMIs", mock.Anything, mock.Anything)

	// The cursor moved to the focused AMI: y copies it.
	_, cmd = v.Update(keyPress('y'))
	text, ok := charClipboard(charRun(cmd))
	require.True(t, ok)
	assert.Equal(t, "ami-bbb", text)

	// The focus was used up: the next load opens nothing.
	svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI{charAMIA, charAMIB}, nil).Once()
	_, cmd = v.Update(keyPress('r'))
	_, again := firstOfType[msg.TabbedContentMsg](pump(v, cmd, 5))
	assert.False(t, again)
}

// Divergence #51 (kept): a pending focus survives an ErrorMsg.
func TestCharAMI_PendingFocusSurvivesError(t *testing.T) {
	v, svc := newCharAMI(t)
	svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI{charAMIA}, nil).Once()
	v.Update(msg.FocusResourceMsg{ResourceID: "ami-aaa"})
	v.Update(msg.ErrorMsg{Err: errors.New("x"), Context: "y"})

	_, cmd := v.Update(keyPress('r'))
	seen := pump(v, cmd, 5)
	p, ok := firstOfType[msg.TabbedContentMsg](seen)
	require.True(t, ok)
	assert.Equal(t, "alpha-image (ami-aaa)", p.PanelTitle)
}

// Focus on a listed AMI moves the cursor and opens a panel with one tab.
func TestCharAMI_FocusListedAMI(t *testing.T) {
	v, svc := newCharAMILoaded(t, charAMIA, charAMIB)
	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "ami-bbb"})
	p, ok := firstOfType[msg.TabbedContentMsg](charRun(cmd))
	require.True(t, ok)
	// Divergence #34: one tab named like the panel. Changes in PR 6.
	assert.Equal(t, "beta-image (ami-bbb)", p.PanelTitle)
	require.Len(t, p.Tabs, 1)
	assert.Equal(t, "beta-image (ami-bbb)", p.Tabs[0].Title)
	assert.Equal(t, "json", p.Tabs[0].Format)
	assert.Equal(t, charAMIJSON(t, charAMIB), p.Tabs[0].Content)
	svc.AssertNotCalled(t, "RefreshAMIs", mock.Anything, mock.Anything)

	// The cursor moved: y copies ami-bbb.
	_, cmd = v.Update(keyPress('y'))
	text, ok := charClipboard(charRun(cmd))
	require.True(t, ok)
	assert.Equal(t, "ami-bbb", text)
}

// Divergence #22 (kept): focus on a row the filter hides opens the panel and
// the cursor stays.
func TestCharAMI_FocusFilteredRow(t *testing.T) {
	v, _ := newCharAMILoaded(t, charAMIA, charAMIB)
	v.Update(ui.FilterChangedMsg{Text: "alpha"})
	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "ami-bbb"})
	p, ok := firstOfType[msg.TabbedContentMsg](charRun(cmd))
	require.True(t, ok)
	assert.Equal(t, "beta-image (ami-bbb)", p.PanelTitle)
	assert.Equal(t, []string{"ami-aaa"}, charRows(v))
	assert.Equal(t, "1/2 AMIs (owned)", plain(v.Footer()))
}

// Update returns the same pointer for every message.
func TestCharAMI_UpdateReturnsSamePointer(t *testing.T) {
	v, svc := newCharAMILoaded(t, charAMIA, charAMIB)
	svc.On("RefreshAMIs", mock.Anything, mock.Anything).Return([]aws.AMI{charAMIP}, nil).Maybe()
	msgs := []tea.Msg{
		tea.WindowSizeMsg{Width: 100, Height: 20},
		ui.PickerResultMsg{ID: "sort", Selected: 1},
		ui.FilterChangedMsg{Text: ""},
		msg.FocusResourceMsg{ResourceID: "ami-aaa"},
		msg.ErrorMsg{Err: errors.New("x")},
		spinner.TickMsg{},
		keyPress(' '),
		keyPress('s'),
		keyPress('S'),
		keyPress('y'),
		tea.KeyPressMsg{Code: tea.KeyDown},
		tea.KeyPressMsg{Code: tea.KeyEnter},
		keyPress('d'),
		keyPress('/'),
		keyPress('x'),
		tea.KeyPressMsg{Code: tea.KeyEscape},
		keyPress('p'),
		keyPress('x'),
		tea.KeyPressMsg{Code: tea.KeyEscape},
		tea.KeyPressMsg{Code: tea.KeyEscape},
		tea.KeyPressMsg{Code: tea.KeyEscape},
		keyPress('r'),
	}
	for _, m := range msgs {
		got, _ := v.Update(m)
		require.Same(t, v, got, "%T", m)
	}
}

// Update returns the same pointer for the view's own replies: owned load,
// search load, refresh (ok and failed), focus fetch (ok and failed).
func TestCharAMI_UpdateReturnsSamePointerForReplies(t *testing.T) {
	v, svc := newCharAMI(t)
	svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI{charAMIA}, nil).Once()
	svc.On("SearchAMIs", mock.Anything, "amzn").Return([]aws.AMI{charAMIA}, nil).Once()
	svc.On("RefreshAMIs", mock.Anything, []string{"ami-aaa"}).Return([]aws.AMI{charAMIA}, nil).Once()
	svc.On("RefreshAMIs", mock.Anything, []string{"ami-aaa"}).Return([]aws.AMI(nil), errors.New("x")).Once()
	svc.On("RefreshAMIs", mock.Anything, []string{"ami-pub"}).Return([]aws.AMI{charAMIP}, nil).Once()
	svc.On("RefreshAMIs", mock.Anything, []string{"ami-zzz"}).Return([]aws.AMI(nil), errors.New("x")).Once()

	var replies []tea.Msg
	run := func(cmd tea.Cmd) {
		t.Helper()
		got := charRun(cmd)
		require.NotEmpty(t, got)
		for _, m := range got {
			out, _ := v.Update(m)
			require.Same(t, v, out, "%T", m)
		}
		replies = append(replies, got...)
	}
	run(v.Init())
	run(charSearch(v, "amzn"))
	v.Update(keyPress(' '))
	_, cmd := v.Update(keyPress('r'))
	run(cmd)
	_, cmd = v.Update(keyPress('r'))
	run(cmd)
	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: "ami-pub"})
	run(cmd)
	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: "ami-zzz"})
	run(cmd)
	assert.Len(t, replies, 6)
	svc.AssertExpectations(t)
}

// SetContext reaches every fetch: owned, search, refresh and focus.
func TestCharAMI_SetContextReachesFetch(t *testing.T) {
	type key struct{}
	v, svc := newCharAMI(t)
	v.SetContext(context.WithValue(context.Background(), key{}, "nav"))
	hasValue := mock.MatchedBy(func(ctx context.Context) bool { return ctx.Value(key{}) == "nav" })
	svc.On("ListOwnedAMIs", hasValue).Return([]aws.AMI{charAMIA}, nil).Once()
	svc.On("SearchAMIs", hasValue, "amzn").Return([]aws.AMI{charAMIA}, nil).Once()
	svc.On("RefreshAMIs", hasValue, []string{"ami-aaa"}).Return([]aws.AMI{charAMIA}, nil).Once()
	svc.On("RefreshAMIs", hasValue, []string{"ami-pub"}).Return([]aws.AMI{charAMIP}, nil).Once()

	pump(v, v.Init(), 5)
	pump(v, charSearch(v, "amzn"), 5)
	v.Update(keyPress(' '))
	_, cmd := v.Update(keyPress('r'))
	pump(v, cmd, 5)
	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: "ami-pub"})
	pump(v, cmd, 5)

	svc.AssertExpectations(t)
}

// --- ami_list ---

func TestCharAMI_ColumnsPerWidth(t *testing.T) {
	cases := []struct {
		width  int
		header string
	}{
		{60, "AMI ID Name State"},
		{100, "AMI ID Name Architecture State"},
		{160, "AMI ID Name Owner Architecture State Created"},
		{200, "AMI ID Name Owner Architecture State Created"},
	}
	for _, c := range cases {
		t.Run(fmt.Sprint(c.width), func(t *testing.T) {
			v, _ := newCharAMILoaded(t, charAMIA)
			v.Update(tea.WindowSizeMsg{Width: c.width, Height: 24})
			assert.Equal(t, c.header, charHeader(v))
		})
	}
}

// Wide rows: owner alias before owner ID, and a 10-character date.
func TestCharAMI_WideRowCells(t *testing.T) {
	v, _ := newCharAMILoaded(t, charAMIA, charAMIB)
	var lines []string
	for line := range strings.SplitSeq(plain(v.View().Content), "\n") {
		if f := strings.Fields(line); len(f) > 0 && strings.HasPrefix(f[0], "ami-") {
			lines = append(lines, strings.Join(f, " "))
		}
	}
	assert.Equal(t, []string{
		"ami-aaa alpha-image 111122223333 x86_64 ● available 2026-03-01",
		"ami-bbb beta-image self arm64 ◌ pending 2025-12-24",
	}, lines)
}

// The owned load uses listTimeout (2 minutes); the search has no timeout.
func TestCharAMI_Timeouts(t *testing.T) {
	v, svc := newCharAMI(t)
	var ownedLeft time.Duration
	var ownedHas, searchHas bool
	svc.On("ListOwnedAMIs", mock.Anything).Run(func(args mock.Arguments) {
		dl, ok := args.Get(0).(context.Context).Deadline()
		ownedHas, ownedLeft = ok, time.Until(dl)
	}).Return([]aws.AMI{charAMIA}, nil).Once()
	svc.On("SearchAMIs", mock.Anything, "amzn").Run(func(args mock.Arguments) {
		_, searchHas = args.Get(0).(context.Context).Deadline()
	}).Return([]aws.AMI{charAMIA}, nil).Once()

	pump(v, v.Init(), 5)
	pump(v, charSearch(v, "amzn"), 5)

	require.True(t, ownedHas)
	assert.InDelta(t, (2 * time.Minute).Seconds(), ownedLeft.Seconds(), 10)
	// Divergence #38 (kept): no timeout on the search.
	assert.False(t, searchHas)
}

func TestCharAMI_SearchOpenClose(t *testing.T) {
	v, svc := newCharAMILoaded(t, charAMIA)

	_, cmd := v.Update(keyPress('p'))
	assert.Nil(t, cmd)
	lines := strings.Split(plain(v.View().Content), "\n")
	assert.Equal(t, "┃ ? search public AMIs...", strings.TrimRight(lines[0], " "))

	_, cmd = v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Nil(t, cmd, "esc closes the search and does not go back")
	assert.False(t, v.CapturingInput())
	assert.NotContains(t, plain(v.View().Content), "search public")

	// Enter with an empty query only closes the search.
	v.Update(keyPress('p'))
	_, cmd = v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Nil(t, cmd)
	assert.False(t, v.CapturingInput())
	assert.Equal(t, "1/1 AMIs (owned)", plain(v.Footer()))
	svc.AssertNotCalled(t, "SearchAMIs", mock.Anything, mock.Anything)
}

// Divergence #32: typing in the search filters the table, and esc leaves the
// filter applied with no filter input shown. Changes in PR 1.
func TestCharAMI_SearchLiveFiltersTable(t *testing.T) {
	v, _ := newCharAMILoaded(t, charAMIA, charAMIB)
	v.Update(keyPress('p'))
	charType(t, v, "be")
	assert.Equal(t, "1/2 AMIs (owned)", plain(v.Footer()), "Divergence #32: changes in PR 1.")

	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Nil(t, cmd, "esc in the search sends no FilterChangedMsg")
	assert.Equal(t, "1/2 AMIs (owned)", plain(v.Footer()), "Divergence #32: changes in PR 1.")
	assert.Equal(t, []string{"ami-bbb"}, charRows(v))
}

// A search: enter shows the spinner, calls SearchAMIs, and shows the results.
func TestCharAMI_SearchLoads(t *testing.T) {
	v, svc := newCharAMILoaded(t, charAMIA)
	svc.On("SearchAMIs", mock.Anything, "amzn").Return([]aws.AMI{charAMIP}, nil).Once()

	cmd := charSearch(v, "amzn")
	require.NotNil(t, cmd)
	assert.False(t, v.CapturingInput())
	// Divergence #7 (kept): the rows stay during the load.
	assert.Equal(t, []string{"ami-aaa"}, charRows(v))
	pump(v, cmd, 5)

	assert.Equal(t, []string{"ami-pub"}, charRows(v))
	svc.AssertNumberOfCalls(t, "ListOwnedAMIs", 1)
}

// r with no selection returns to owned mode.
func TestCharAMI_RefreshAfterSearchLoadsOwned(t *testing.T) {
	v, svc := newCharAMILoaded(t, charAMIA)
	svc.On("SearchAMIs", mock.Anything, "amzn").Return([]aws.AMI{charAMIP}, nil).Once()
	pump(v, charSearch(v, "amzn"), 5)
	require.Equal(t, `1/1 results for "amzn"`, plain(v.Footer()))

	svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI{charAMIA, charAMIB}, nil).Once()
	_, cmd := v.Update(keyPress('r'))
	require.NotNil(t, cmd)
	// Divergence #36: the footer says "(owned)" before the owned data arrives.
	// Changes in PR 6.
	assert.Equal(t, "1/1 AMIs (owned)  ⣾  Loading AMIs...", plain(v.Footer()))
	assert.Equal(t, []string{"ami-pub"}, charRows(v), "Divergence #7 (kept): rows stay during the reload")

	pump(v, cmd, 5)
	assert.Equal(t, "2/2 AMIs (owned)", plain(v.Footer()))
	assert.Equal(t, []string{"ami-aaa", "ami-bbb"}, charRows(v))
	svc.AssertNumberOfCalls(t, "ListOwnedAMIs", 2)
	svc.AssertNumberOfCalls(t, "SearchAMIs", 1)
}

// View snapshots: first load, reload, error.
func TestCharAMI_ViewSnapshots(t *testing.T) {
	t.Run("first load", func(t *testing.T) {
		v, _ := newCharAMI(t)
		assert.Equal(t, "\n  ⣾  Loading AMIs...", plain(v.View().Content))
	})
	t.Run("reload", func(t *testing.T) {
		v, svc := newCharAMILoaded(t, charAMIA)
		svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI{charAMIA}, nil).Maybe()
		v.Update(keyPress('r'))
		lines := strings.Split(plain(v.View().Content), "\n")
		require.GreaterOrEqual(t, len(lines), 3)
		assert.Equal(t, "AMI ID Name Owner Architecture State Created", strings.Join(strings.Fields(lines[0]), " "))
		assert.Equal(t, "ami-aaa alpha-image 111122223333 x86_64 ● available 2026-03-01", strings.Join(strings.Fields(lines[2]), " "))
		assert.Equal(t, "1/1 AMIs (owned)  ⣾  Loading AMIs...", plain(v.Footer()))
	})
	t.Run("error", func(t *testing.T) {
		v, _ := newCharAMI(t)
		v.Update(msg.ErrorMsg{Err: errors.New("access denied"), Context: "listing owned AMIs"})
		assert.Equal(t, "\n  Error: access denied", plain(v.View().Content))
	})
	t.Run("error wins over rows", func(t *testing.T) {
		v, _ := newCharAMILoaded(t, charAMIA)
		v.Update(msg.ErrorMsg{Err: errors.New("access denied")})
		assert.Equal(t, "\n  Error: access denied", plain(v.View().Content))
	})
}

// Divergence #6: the table is frozen during a load; nav keys go to the
// spinner. Changes in PR 6.
func TestCharAMI_NavKeysDuringLoad(t *testing.T) {
	v, svc := newCharAMILoaded(t, charAMIA, charAMIB)
	svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI{charAMIA, charAMIB}, nil).Maybe()
	v.Update(keyPress('r'))

	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd := v.Update(keyPress('y'))
	text, ok := charClipboard(charRun(cmd))
	require.True(t, ok)
	assert.Equal(t, "ami-aaa", text, "Divergence #6: the cursor did not move. Changes in PR 6.")
}

// Without a load running, nav keys move the cursor.
func TestCharAMI_NavKeysAfterLoad(t *testing.T) {
	v, _ := newCharAMILoaded(t, charAMIA, charAMIB)
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd := v.Update(keyPress('y'))
	text, ok := charClipboard(charRun(cmd))
	require.True(t, ok)
	assert.Equal(t, "ami-bbb", text)
}

// Divergence #11: the spinner ticks only while loading; during a selective
// refresh its tick is dropped and the spinner freezes. Changes in PR 6.
func TestCharAMI_SpinnerTicks(t *testing.T) {
	v, svc := newCharAMI(t)
	svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI{charAMIA}, nil).Once()
	init := v.Init()
	tick, ok := firstOfType[spinner.TickMsg](collectMsgs(init))
	require.True(t, ok)
	_, cmd := v.Update(tick)
	assert.NotNil(t, cmd, "loading: the spinner asks for the next tick")

	v2, svc2 := newCharAMILoaded(t, charAMIA)
	svc2.On("RefreshAMIs", mock.Anything, []string{"ami-aaa"}).Return([]aws.AMI{charAMIA}, nil).Maybe()
	v2.Update(keyPress(' '))
	_, cmd = v2.Update(keyPress('r'))
	tick, ok = firstOfType[spinner.TickMsg](collectMsgs(cmd))
	require.True(t, ok)
	require.Contains(t, plain(v2.Footer()), "Refreshing 1 AMIs...")
	_, cmd = v2.Update(tick)
	assert.Nil(t, cmd, "Divergence #11: changes in PR 6.")
}

func TestCharAMI_Esc(t *testing.T) {
	t.Run("without selection goes back", func(t *testing.T) {
		v, _ := newCharAMILoaded(t, charAMIA)
		_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		_, ok := firstOfType[msg.NavigateBackMsg](charRun(cmd))
		assert.True(t, ok)
	})
	t.Run("with selection deselects", func(t *testing.T) {
		v, _ := newCharAMILoaded(t, charAMIA)
		v.Update(keyPress(' '))
		_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		// Divergence #1 (kept for multi-select views).
		assert.Nil(t, cmd)
		assert.Equal(t, "1/1 AMIs (owned)", plain(v.Footer()))
	})
}

func TestCharAMI_Copy(t *testing.T) {
	t.Run("cursor", func(t *testing.T) {
		v, _ := newCharAMILoaded(t, charAMIA, charAMIB)
		msgs := charRun(func() tea.Cmd { _, c := v.Update(keyPress('y')); return c }())
		text, ok := charClipboard(msgs)
		require.True(t, ok)
		assert.Equal(t, "ami-aaa", text)
		require.Len(t, toasts(msgs), 1)
		assert.Equal(t, msg.ToastSuccess("Copied 1 AMI ID(s)"), toasts(msgs)[0])
	})
	t.Run("selection", func(t *testing.T) {
		v, _ := newCharAMILoaded(t, charAMIA, charAMIB)
		v.Update(keyPress(' '))
		v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		v.Update(keyPress(' '))
		msgs := charRun(func() tea.Cmd { _, c := v.Update(keyPress('y')); return c }())
		text, _ := charClipboard(msgs)
		assert.Equal(t, "ami-aaa\nami-bbb", text)
		require.Len(t, toasts(msgs), 1)
		assert.Equal(t, "Copied 2 AMI ID(s)", toasts(msgs)[0].Text)
	})
	t.Run("empty list", func(t *testing.T) {
		v, _ := newCharAMILoaded(t)
		_, cmd := v.Update(keyPress('y'))
		assert.Nil(t, cmd)
	})
}

// Divergence #34: enter and d emit NavigateMsg to the content view. Changes in PR 6.
func TestCharAMI_DetailsKeys(t *testing.T) {
	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, keyPress('d')} {
		t.Run(k.String(), func(t *testing.T) {
			v, _ := newCharAMILoaded(t, charAMIA)
			_, cmd := v.Update(k)
			nav, ok := firstOfType[msg.NavigateMsg](charRun(cmd))
			require.True(t, ok)
			assert.Equal(t, "content", nav.ViewID)
			assert.Equal(t, map[string]string{
				"title":   "alpha-image (ami-aaa)",
				"format":  "json",
				"content": charAMIJSON(t, charAMIA),
			}, nav.Params)
		})
	}
	t.Run("no name", func(t *testing.T) {
		v, _ := newCharAMILoaded(t, aws.AMI{ID: "ami-noname", State: "available"})
		_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		nav, ok := firstOfType[msg.NavigateMsg](charRun(cmd))
		require.True(t, ok)
		assert.Equal(t, "ami-noname", nav.Params["title"])
	})
	// Divergence #48 (kept): with no cursor the key falls through to the table.
	t.Run("no cursor", func(t *testing.T) {
		v, _ := newCharAMILoaded(t)
		_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		_, ok := firstOfType[msg.NavigateMsg](charRun(cmd))
		assert.False(t, ok)
	})
}

// Selective refresh: r with a selection calls RefreshAMIs once with the
// selected IDs, shows a spinner, and merges the result.
func TestCharAMI_SelectiveRefresh(t *testing.T) {
	v, svc := newCharAMILoaded(t, charAMIA, charAMIB)
	newer := charAMIB
	newer.State = "available"
	newer.Name = "beta-renamed"
	// Divergence #50 (kept): an extra ID in the reply is ignored.
	svc.On("RefreshAMIs", mock.Anything, []string{"ami-aaa", "ami-bbb"}).
		Return([]aws.AMI{newer, charAMIP}, nil).Once()
	v.Update(keyPress(' '))
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	v.Update(keyPress(' '))

	_, cmd := v.Update(keyPress('r'))
	require.NotNil(t, cmd)
	assert.Equal(t, "2/2 AMIs (owned)  (2 selected)  ⣾  Refreshing 2 AMIs...", plain(v.Footer()))
	assert.Equal(t, []string{"*ami-aaa", "*ami-bbb"}, charRows(v))

	seen := pump(v, cmd, 5)
	assert.Empty(t, toasts(seen))
	assert.Equal(t, "2/2 AMIs (owned)  (2 selected)", plain(v.Footer()))
	assert.Contains(t, plain(v.View().Content), "beta-renamed")
	// Divergence #50 (kept): ami-aaa was not in the reply and stays.
	assert.Equal(t, []string{"*ami-aaa", "*ami-bbb"}, charRows(v))
	svc.AssertNumberOfCalls(t, "ListOwnedAMIs", 1)
}

func TestCharAMI_SelectiveRefreshUsesCallTimeout(t *testing.T) {
	v, svc := newCharAMILoaded(t, charAMIA)
	var left time.Duration
	svc.On("RefreshAMIs", mock.Anything, []string{"ami-aaa"}).Run(func(args mock.Arguments) {
		dl, _ := args.Get(0).(context.Context).Deadline()
		left = time.Until(dl)
	}).Return([]aws.AMI{charAMIA}, nil).Once()
	v.Update(keyPress(' '))
	_, cmd := v.Update(keyPress('r'))
	pump(v, cmd, 5)
	assert.InDelta(t, (30 * time.Second).Seconds(), left.Seconds(), 5)
}

func TestCharAMI_SelectiveRefreshErrors(t *testing.T) {
	t.Run("error toast", func(t *testing.T) {
		v, svc := newCharAMILoaded(t, charAMIA)
		svc.On("RefreshAMIs", mock.Anything, []string{"ami-aaa"}).Return([]aws.AMI(nil), errors.New("throttled")).Once()
		v.Update(keyPress(' '))
		_, cmd := v.Update(keyPress('r'))
		seen := pump(v, cmd, 5)
		require.Len(t, toasts(seen), 1)
		assert.Equal(t, msg.ToastError("Refresh failed: throttled"), toasts(seen)[0])
		assert.Equal(t, "1/1 AMIs (owned)  (1 selected)", plain(v.Footer()), "spinner hidden")
		assert.Equal(t, []string{"*ami-aaa"}, charRows(v))
	})
	t.Run("canceled toasts", func(t *testing.T) {
		v, svc := newCharAMILoaded(t, charAMIA)
		svc.On("RefreshAMIs", mock.Anything, []string{"ami-aaa"}).Return([]aws.AMI(nil), context.Canceled).Once()
		v.Update(keyPress(' '))
		_, cmd := v.Update(keyPress('r'))
		seen := pump(v, cmd, 5)
		// Divergence #37: changes in PR 6.
		require.Len(t, toasts(seen), 1)
		assert.Equal(t, "Refresh failed: cancelled", toasts(seen)[0].Text)
	})
}

// Divergence #10: a refresh reply has no gen and hides the spinner of a full
// load that started after it. Changes in PR 6.
func TestCharAMI_RefreshReplyHidesLoadSpinner(t *testing.T) {
	v, svc := newCharAMILoaded(t, charAMIA)
	svc.On("RefreshAMIs", mock.Anything, []string{"ami-aaa"}).Return([]aws.AMI{charAMIA}, nil).Once()
	svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI{charAMIA}, nil).Maybe()
	v.Update(keyPress(' '))
	_, refresh := v.Update(keyPress('r'))
	refreshMsgs := charRun(refresh)
	v.Update(tea.KeyPressMsg{Code: tea.KeyEscape}) // deselect
	v.Update(keyPress('r'))                        // full load
	require.Equal(t, "1/1 AMIs (owned)  ⣾  Loading AMIs...", plain(v.Footer()))

	deliverAll(v, refreshMsgs...)
	assert.Equal(t, "1/1 AMIs (owned)", plain(v.Footer()), "Divergence #10: changes in PR 6.")
}

// Focus on a missing AMI fetches it by ID, adds a row, counts it as linked
// and opens the panel; the spinner is hidden afterwards.
func TestCharAMI_FocusMissingAMI(t *testing.T) {
	v, svc := newCharAMILoaded(t, charAMIA)
	var left time.Duration
	svc.On("RefreshAMIs", mock.Anything, []string{"ami-pub"}).Run(func(args mock.Arguments) {
		dl, _ := args.Get(0).(context.Context).Deadline()
		left = time.Until(dl)
	}).Return([]aws.AMI{charAMIP}, nil).Once()

	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "ami-pub"})
	require.NotNil(t, cmd)
	seen := pump(v, cmd, 5)

	p, ok := firstOfType[msg.TabbedContentMsg](seen)
	require.True(t, ok)
	assert.Equal(t, "amzn-public (ami-pub)", p.PanelTitle)
	assert.Equal(t, []string{"ami-aaa", "ami-pub"}, charRows(v))
	assert.Equal(t, "2/2 AMIs (owned + 1 linked)", plain(v.Footer()))
	assert.InDelta(t, (30 * time.Second).Seconds(), left.Seconds(), 5)

	// The cursor is on the new row.
	_, cmd = v.Update(keyPress('y'))
	text, _ := charClipboard(charRun(cmd))
	assert.Equal(t, "ami-pub", text)

	// A reload drops the linked row.
	svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI{charAMIA}, nil).Once()
	_, cmd = v.Update(keyPress('r'))
	pump(v, cmd, 5)
	assert.Equal(t, []string{"ami-aaa"}, charRows(v))
	assert.Equal(t, "1/1 AMIs (owned)", plain(v.Footer()))
}

func TestCharAMI_FocusMissingAMIFailures(t *testing.T) {
	cases := []struct {
		name  string
		amis  []aws.AMI
		err   error
		toast string
	}{
		{"not found", []aws.AMI{}, nil, "AMI ami-x not found or not visible to this account"},
		{"other id only", []aws.AMI{charAMIP}, nil, "AMI ami-x not found or not visible to this account"},
		{"error", nil, errors.New("denied"), "Could not load AMI ami-x: denied"},
		{"canceled", nil, context.Canceled, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, svc := newCharAMILoaded(t, charAMIA)
			svc.On("RefreshAMIs", mock.Anything, []string{"ami-x"}).Return(c.amis, c.err).Once()
			_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "ami-x"})
			seen := pump(v, cmd, 5)

			if c.toast == "" {
				assert.Empty(t, toasts(seen))
			} else {
				require.Len(t, toasts(seen), 1)
				assert.Equal(t, msg.ToastError(c.toast), toasts(seen)[0])
			}
			_, panel := firstOfType[msg.TabbedContentMsg](seen)
			assert.False(t, panel)
			assert.Equal(t, []string{"ami-aaa"}, charRows(v))
			assert.Equal(t, "1/1 AMIs (owned)", plain(v.Footer()), "spinner hidden, no linked row")
		})
	}
}

// Focus on a missing AMI while loading waits for the load, then fetches.
func TestCharAMI_PendingFocusOnMissingAMI(t *testing.T) {
	v, svc := newCharAMI(t)
	svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI{charAMIA}, nil).Once()
	svc.On("RefreshAMIs", mock.Anything, []string{"ami-pub"}).Return([]aws.AMI{charAMIP}, nil).Once()
	init := v.Init()
	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "ami-pub"})
	assert.Nil(t, cmd)
	svc.AssertNotCalled(t, "RefreshAMIs", mock.Anything, mock.Anything)

	seen := pump(v, init, 6)
	p, ok := firstOfType[msg.TabbedContentMsg](seen)
	require.True(t, ok)
	assert.Equal(t, "amzn-public (ami-pub)", p.PanelTitle)
	assert.Equal(t, "2/2 AMIs (owned + 1 linked)", plain(v.Footer()))
}

// A search result replaces the linked row count too.
func TestCharAMI_SearchDropsLinkedRow(t *testing.T) {
	v, svc := newCharAMILoaded(t, charAMIA)
	svc.On("RefreshAMIs", mock.Anything, []string{"ami-pub"}).Return([]aws.AMI{charAMIP}, nil).Once()
	svc.On("SearchAMIs", mock.Anything, "alpha").Return([]aws.AMI{charAMIA}, nil).Once()
	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "ami-pub"})
	pump(v, cmd, 5)
	require.Equal(t, "2/2 AMIs (owned + 1 linked)", plain(v.Footer()))

	pump(v, charSearch(v, "alpha"), 5)
	assert.Equal(t, `1/1 results for "alpha"`, plain(v.Footer()))
	assert.Equal(t, []string{"ami-aaa"}, charRows(v))
}

// C3: a full reload that returns the rows in a new order keeps the cursor
// on the same AMI.
func TestCharAMI_FullReloadKeepsCursorByKey(t *testing.T) {
	v, svc := newCharAMILoaded(t, charAMIA, charAMIB)
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // cursor on ami-bbb
	svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI{charAMIB, charAMIP, charAMIA}, nil).Once()
	_, cmd := v.Update(keyPress('r'))
	pump(v, cmd, 5)
	require.Equal(t, []string{"ami-bbb", "ami-pub", "ami-aaa"}, charRows(v))

	_, cmd = v.Update(keyPress('y'))
	text, ok := charClipboard(charRun(cmd))
	require.True(t, ok)
	assert.Equal(t, "ami-bbb", text)
}
