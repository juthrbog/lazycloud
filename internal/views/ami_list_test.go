package views

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

func newTestAMIList() (*AMIList, *awstest.MockEC2Service) {
	m := new(awstest.MockEC2Service)
	view := NewAMIList(m)
	view.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
	return view, m
}

func loadAMIs(view *AMIList, amis []aws.AMI) {
	view.Update(amiListLoadedMsg{amis: amis, owned: true})
}

var testAMI1 = aws.AMI{ID: "ami-111", Name: "my-linux-image", OwnerID: "123456789012", Architecture: "x86_64", State: "available", CreationDate: "2026-01-15T10:00:00Z"}
var testAMI2 = aws.AMI{ID: "ami-222", Name: "my-arm-image", OwnerID: "123456789012", Architecture: "arm64", State: "available", CreationDate: "2026-02-01T10:00:00Z"}

// --- Load ---

func TestAMIList_LoadedAMIsPopulateTable(t *testing.T) {
	view, _ := newTestAMIList()
	loadAMIs(view, []aws.AMI{testAMI1, testAMI2})

	assert.False(t, view.loading)
	assert.Len(t, view.amis, 2)
	_, total := view.table.RowCount()
	assert.Equal(t, 2, total)
}

func TestAMIList_OwnedModeAfterLoad(t *testing.T) {
	view, _ := newTestAMIList()
	loadAMIs(view, []aws.AMI{testAMI1})

	assert.True(t, view.ownedMode)
	assert.Empty(t, view.lastQuery)
}

func TestAMIList_SearchResultSetsOwnedModeFalse(t *testing.T) {
	view, _ := newTestAMIList()
	view.Update(amiListLoadedMsg{amis: []aws.AMI{testAMI1}, owned: false, query: "linux"})

	assert.False(t, view.ownedMode)
	assert.Equal(t, "linux", view.lastQuery)
}

// --- Search mode ---

func TestAMIList_PActivatesSearch(t *testing.T) {
	view, _ := newTestAMIList()
	loadAMIs(view, []aws.AMI{testAMI1})

	view.Update(keyPress('p'))

	assert.True(t, view.searchActive)
}

func TestAMIList_EscCancelsSearch(t *testing.T) {
	view, _ := newTestAMIList()
	loadAMIs(view, []aws.AMI{testAMI1})

	view.Update(keyPress('p'))
	assert.True(t, view.searchActive)

	view.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.False(t, view.searchActive)
}

// --- Refresh ---

func TestAMIList_RefreshReloadsOwned(t *testing.T) {
	view, _ := newTestAMIList()
	// Put view into search-result mode first
	view.Update(amiListLoadedMsg{amis: []aws.AMI{testAMI1}, owned: false, query: "linux"})
	assert.False(t, view.ownedMode)

	_, cmd := view.Update(keyPress('r'))
	assert.NotNil(t, cmd)
	assert.True(t, view.loading)
	assert.True(t, view.ownedMode)
	assert.Empty(t, view.lastQuery)
}

// --- Detail navigation ---

func TestAMIList_EnterEmitsNavigateToContent(t *testing.T) {
	view, _ := newTestAMIList()
	loadAMIs(view, []aws.AMI{testAMI1})

	_, cmd := view.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	result := cmd()
	nav, ok := result.(msg.NavigateMsg)
	require.True(t, ok, "expected NavigateMsg, got %T", result)
	assert.Equal(t, "content", nav.ViewID)
	assert.Equal(t, "json", nav.Params["format"])
	assert.Contains(t, nav.Params["content"], "ami-111")
}

// --- Responsive columns ---

func TestAMIList_NarrowTierHidesColumns(t *testing.T) {
	cols := amiColumns(ui.TierNarrow)
	assert.Equal(t, 3, len(cols), "narrow tier should show 3 columns")
	assert.Equal(t, "AMI ID", cols[0].Title)
	assert.Equal(t, "State", cols[2].Title)
}

func TestAMIList_WideTierShowsAllColumns(t *testing.T) {
	cols := amiColumns(ui.TierWide)
	assert.Equal(t, 6, len(cols), "wide tier should show 6 columns")
}

// --- findAMI ---

func TestAMIList_FindAMI(t *testing.T) {
	view, _ := newTestAMIList()
	loadAMIs(view, []aws.AMI{testAMI1, testAMI2})

	found := view.findAMI("ami-222")
	require.NotNil(t, found)
	assert.Equal(t, "my-arm-image", found.Name)

	assert.Nil(t, view.findAMI("ami-nope"))
}
