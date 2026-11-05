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
)

var publicAMI = aws.AMI{ID: "ami-pub", Name: "amzn2-public", OwnerID: "137112412989", OwnerAlias: "amazon", State: "available"}

// focusView returns an AMI list with one owned AMI and a mock service.
func focusView(t *testing.T) (*AMIList, *awstest.MockEC2Service) {
	t.Helper()
	svc := new(awstest.MockEC2Service)
	view := NewAMIList(svc)
	view.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
	loadAMIs(view, []aws.AMI{testAMI1})
	return view, svc
}

// focusAMI sends a focus message and returns the app-level messages that
// follow, after it gives the fetch result back to the view.
func focusAMI(t *testing.T, view *AMIList, id string) []any {
	t.Helper()
	_, cmd := view.Update(msg.FocusResourceMsg{ResourceID: id})
	require.NotNil(t, cmd)
	var out []any
	for _, m := range collectMsgs(cmd) {
		if loaded, ok := m.(amiFocusLoadedMsg); ok {
			_, next := view.Update(loaded)
			for _, n := range collectMsgs(next) {
				out = append(out, n)
			}
			continue
		}
		out = append(out, m)
	}
	return out
}

func panelTitle(msgs []any) string {
	for _, m := range msgs {
		if p, ok := m.(msg.TabbedContentMsg); ok {
			return p.PanelTitle
		}
	}
	return ""
}

func toastText(msgs []any) string {
	for _, m := range msgs {
		if p, ok := m.(msg.ToastMsg); ok {
			return p.Text
		}
	}
	return ""
}

// M10: an owned AMI needs no request.
func TestAMIFocus_OwnedAMIOpensWithoutRequest(t *testing.T) {
	view, svc := focusView(t)

	msgs := focusAMI(t, view, testAMI1.ID)

	assert.Equal(t, "my-linux-image (ami-111)", panelTitle(msgs))
	svc.AssertNotCalled(t, "RefreshAMIs", mock.Anything, mock.Anything)
}

// M10: an AMI of another account is fetched by ID, gets a row and the cursor,
// and opens its panel.
func TestAMIFocus_OtherAccountAMIGetsARow(t *testing.T) {
	view, svc := focusView(t)
	svc.On("RefreshAMIs", mock.Anything, []string{publicAMI.ID}).Return([]aws.AMI{publicAMI}, nil).Once()

	msgs := focusAMI(t, view, publicAMI.ID)

	assert.Equal(t, "amzn2-public (ami-pub)", panelTitle(msgs))
	assert.Equal(t, publicAMI.ID, view.table.CursorKey())
	assert.NotNil(t, view.findAMI(publicAMI.ID))
	assert.Contains(t, view.Footer(), "1 linked")
	svc.AssertExpectations(t)
}

// M10: the link also works when the list was still loading.
func TestAMIFocus_PendingFocusFetchesAfterLoad(t *testing.T) {
	svc := new(awstest.MockEC2Service)
	svc.On("RefreshAMIs", mock.Anything, []string{publicAMI.ID}).Return([]aws.AMI{publicAMI}, nil).Once()
	view := NewAMIList(svc)
	view.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
	require.True(t, view.loading)
	_, cmd := view.Update(msg.FocusResourceMsg{ResourceID: publicAMI.ID})
	require.Nil(t, cmd)

	_, cmd = view.Update(amiListLoadedMsg{amis: []aws.AMI{testAMI1}, owned: true})
	require.NotNil(t, cmd)
	var title string
	for _, m := range collectMsgs(cmd) {
		if loaded, ok := m.(amiFocusLoadedMsg); ok {
			_, next := view.Update(loaded)
			for _, n := range collectMsgs(next) {
				if p, ok := n.(msg.TabbedContentMsg); ok {
					title = p.PanelTitle
				}
			}
		}
	}

	assert.Equal(t, "amzn2-public (ami-pub)", title)
}

// M10: a reload shows the owned AMIs again, so the footer no longer counts a
// linked row.
func TestAMIFocus_ReloadDropsLinkedRow(t *testing.T) {
	view, svc := focusView(t)
	svc.On("RefreshAMIs", mock.Anything, []string{publicAMI.ID}).Return([]aws.AMI{publicAMI}, nil).Once()
	focusAMI(t, view, publicAMI.ID)

	loadAMIs(view, []aws.AMI{testAMI1})

	assert.Nil(t, view.findAMI(publicAMI.ID))
	assert.NotContains(t, view.Footer(), "linked")
}

// M10: an empty result gives a toast, and the list and cursor do not change.
func TestAMIFocus_NotFoundShowsToast(t *testing.T) {
	view, svc := focusView(t)
	svc.On("RefreshAMIs", mock.Anything, []string{"ami-gone"}).Return([]aws.AMI{}, nil).Once()

	msgs := focusAMI(t, view, "ami-gone")

	assert.Contains(t, toastText(msgs), "ami-gone")
	assert.Contains(t, toastText(msgs), "not found")
	assert.Empty(t, panelTitle(msgs))
	assert.Len(t, view.amis, 1)
	assert.Equal(t, testAMI1.ID, view.table.CursorKey())
	assert.False(t, view.spinner.Visible())
}

// M10: a failed request gives a toast with the error, not a silent list.
func TestAMIFocus_ErrorShowsToast(t *testing.T) {
	view, svc := focusView(t)
	svc.On("RefreshAMIs", mock.Anything, []string{"ami-x"}).Return([]aws.AMI(nil), errors.New("denied")).Once()

	msgs := focusAMI(t, view, "ami-x")

	assert.Contains(t, toastText(msgs), "ami-x")
	assert.Contains(t, toastText(msgs), "denied")
	assert.False(t, view.spinner.Visible())
}

// Leaving the view cancels the request. That is not an error to report.
func TestAMIFocus_CanceledIsSilent(t *testing.T) {
	view, svc := focusView(t)
	svc.On("RefreshAMIs", mock.Anything, []string{"ami-x"}).Return([]aws.AMI(nil), context.Canceled).Once()

	msgs := focusAMI(t, view, "ami-x")

	assert.Empty(t, toastText(msgs))
}
