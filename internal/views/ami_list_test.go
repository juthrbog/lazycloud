package views

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

func newTestAMIList() *AMIList {
	m := new(awstest.MockEC2Service)
	view := NewAMIList(m)
	view.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
	return view
}

// loadAMIs delivers amis as the result of the current load (owned, unless
// a search was started).
func loadAMIs(view *AMIList, amis []aws.AMI) {
	deliverItems(view, &view.listCore, amis)
}

var testAMI1 = aws.AMI{ID: "ami-111", Name: "my-linux-image", OwnerID: "123456789012", Architecture: "x86_64", State: "available", CreationDate: "2026-01-15T10:00:00Z"}
var testAMI2 = aws.AMI{ID: "ami-222", Name: "my-arm-image", OwnerID: "123456789012", Architecture: "arm64", State: "available", CreationDate: "2026-02-01T10:00:00Z"}

// --- Load ---

func TestAMIList_LoadedAMIsPopulateTable(t *testing.T) {
	view := newTestAMIList()
	loadAMIs(view, []aws.AMI{testAMI1, testAMI2})

	assert.False(t, view.loading)
	assert.Len(t, view.items, 2)
	_, total := view.table.RowCount()
	assert.Equal(t, 2, total)
}

func TestAMIList_OwnedAfterLoad(t *testing.T) {
	view := newTestAMIList()
	loadAMIs(view, []aws.AMI{testAMI1})

	assert.Empty(t, view.shown)
	assert.Equal(t, "1/1 AMIs (owned)", plain(view.Footer()))
}

func TestAMIList_SearchResultSetsShown(t *testing.T) {
	view := newTestAMIList()
	view.searchAMIs("linux")
	loadAMIs(view, []aws.AMI{testAMI1})

	assert.Equal(t, "linux", view.shown)
	assert.Equal(t, `1/1 results for "linux"`, plain(view.Footer()))
}

// --- Search mode ---

func TestAMIList_PActivatesSearch(t *testing.T) {
	view := newTestAMIList()
	loadAMIs(view, []aws.AMI{testAMI1})

	view.Update(keyPress('p'))

	assert.True(t, view.searchInput.Active())
}

func TestAMIList_EscCancelsSearch(t *testing.T) {
	view := newTestAMIList()
	loadAMIs(view, []aws.AMI{testAMI1})

	view.Update(keyPress('p'))
	assert.True(t, view.searchInput.Active())

	view.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.False(t, view.searchInput.Active())
}

// The search and the owned load share one load generation, so the one
// started last wins, whichever reply comes first.
func TestAMIList_SearchThenOwned_LastStartedWins(t *testing.T) {
	t.Run("owned started last", func(t *testing.T) {
		view, svc := focusView(t)
		svc.On("SearchAMIs", mock.Anything, "amzn").Return([]aws.AMI{publicAMI}, nil).Once()
		svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI{testAMI1, testAMI2}, nil).Once()

		search := collectMsgs(view.searchAMIs("amzn"))
		_, owned := view.Update(keyPress('r'))
		ownedMsgs := collectMsgs(owned)

		deliverAll(view, ownedMsgs...)
		deliverAll(view, search...) // the older search ends last
		assert.Equal(t, "2/2 AMIs (owned)", plain(view.Footer()))
		assert.Nil(t, view.find(publicAMI.ID))
	})
	t.Run("search started last", func(t *testing.T) {
		view, svc := focusView(t)
		svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI{testAMI1, testAMI2}, nil).Once()
		svc.On("SearchAMIs", mock.Anything, "amzn").Return([]aws.AMI{publicAMI}, nil).Once()

		_, owned := view.Update(keyPress('r'))
		ownedMsgs := collectMsgs(owned)
		search := collectMsgs(view.searchAMIs("amzn"))

		deliverAll(view, search...)
		deliverAll(view, ownedMsgs...) // the older owned load ends last
		assert.Equal(t, `1/1 results for "amzn"`, plain(view.Footer()))
		assert.Equal(t, []string{publicAMI.ID}, view.table.VisibleKeys())
	})
}

// r after a search loads the owned AMIs (not the search again). The footer
// says what is on screen: the search results until the owned load ends.
func TestAMIList_RefreshAfterSearchLoadsOwned(t *testing.T) {
	view, svc := focusView(t)
	svc.On("SearchAMIs", mock.Anything, "amzn").Return([]aws.AMI{publicAMI}, nil).Once()
	pump(view, view.searchAMIs("amzn"), 5)
	require.Equal(t, `1/1 results for "amzn"`, plain(view.Footer()))

	svc.On("ListOwnedAMIs", mock.Anything).Return([]aws.AMI{testAMI1}, nil).Once()
	_, cmd := view.Update(keyPress('r'))
	require.NotNil(t, cmd)
	assert.True(t, view.loading)
	assert.Contains(t, plain(view.Footer()), `results for "amzn"`)

	pump(view, cmd, 5)
	assert.Equal(t, "1/1 AMIs (owned)", plain(view.Footer()))
	svc.AssertNumberOfCalls(t, "SearchAMIs", 1)
	svc.AssertNumberOfCalls(t, "ListOwnedAMIs", 1)
}

func TestAMIList_SearchFailureContext(t *testing.T) {
	view, svc := focusView(t)
	svc.On("SearchAMIs", mock.Anything, "amzn").Return([]aws.AMI(nil), errors.New("throttled")).Once()

	seen := pump(view, view.searchAMIs("amzn"), 5)

	e, ok := firstOfType[msg.ErrorMsg](seen)
	require.True(t, ok)
	assert.Equal(t, "searching AMIs", e.Context)
	assert.Equal(t, "\n  Error: throttled", plain(view.View().Content))
}

// --- Refresh ---

func TestAMIList_RefreshCanceledIsSilent(t *testing.T) {
	view, svc := focusView(t)
	svc.On("RefreshAMIs", mock.Anything, []string{testAMI1.ID}).Return([]aws.AMI(nil), context.Canceled).Once()
	view.Update(keyPress(' '))

	_, cmd := view.Update(keyPress('r'))
	seen := pump(view, cmd, 5)

	assert.Empty(t, toasts(seen))
	assert.False(t, view.spinner.Visible())
}

// --- Detail ---

func TestAMIList_EnterOpensJSONTab(t *testing.T) {
	view := newTestAMIList()
	loadAMIs(view, []aws.AMI{testAMI1})

	_, cmd := view.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	result := cmd()
	panel, ok := result.(msg.TabbedContentMsg)
	require.True(t, ok, "expected TabbedContentMsg, got %T", result)
	assert.Equal(t, "my-linux-image (ami-111)", panel.PanelTitle)
	require.Len(t, panel.Tabs, 1)
	assert.Equal(t, "JSON", panel.Tabs[0].Title)
	assert.Equal(t, "json", panel.Tabs[0].Format)
	assert.Contains(t, panel.Tabs[0].Content, "ami-111")
}

// --- Responsive columns ---

func TestAMIList_NarrowTierHidesColumns(t *testing.T) {
	cols := amiColumns(ui.TierNarrow)
	assert.Len(t, cols, 3, "narrow tier should show 3 columns")
	assert.Equal(t, "AMI ID", cols[0].Title)
	assert.Equal(t, "State", cols[2].Title)
}

func TestAMIList_MediumTierColumns(t *testing.T) {
	cols := amiColumns(ui.TierMedium)
	assert.Len(t, cols, 4, "medium tier should show 4 columns")
	assert.Equal(t, "State", cols[3].Title)
}

func TestAMIList_WideTierShowsAllColumns(t *testing.T) {
	cols := amiColumns(ui.TierWide)
	assert.Len(t, cols, 6, "wide tier should show 6 columns")
}

// --- find ---

func TestAMIList_FindAMI(t *testing.T) {
	view := newTestAMIList()
	loadAMIs(view, []aws.AMI{testAMI1, testAMI2})

	found := view.find("ami-222")
	require.NotNil(t, found)
	assert.Equal(t, "my-arm-image", found.Name)

	assert.Nil(t, view.find("ami-nope"))
}
