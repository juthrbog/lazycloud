package views

import (
	"fmt"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/aws/smithy-go"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
)

// The tag search of the jump (docs/design/jump.md, "Tag search (PR 6)").

var testTagged = []aws.TaggedResource{
	{ARN: "arn:aws:lambda:us-east-1:1:function:orders-api", Tags: map[string]string{"team": "orders", "env": "dev"}},
	{ARN: "arn:aws:s3:::logs-bucket", Tags: map[string]string{"team": "orders"}},
}

func newJumpTags(t *testing.T, filter string, filters []aws.TagFilter) (*JumpTags, *awstest.MockTaggingService) {
	t.Helper()
	svc := new(awstest.MockTaggingService)
	svc.Test(t)
	svc.On("GetResourcesPage", mock.Anything, filters, mock.Anything).Return(&aws.TaggedPage{Resources: testTagged}, nil)
	v := NewJumpTags(svc, filter)
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	pump(v, v.Init(), 20)
	return v, svc
}

func TestParseTagFilter(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []aws.TagFilter
	}{
		{"", nil},
		{"  ", nil},
		{"team=orders", []aws.TagFilter{{Key: "team", Values: []string{"orders"}}}},
		{"tag:team=orders tag:env", []aws.TagFilter{{Key: "team", Values: []string{"orders"}}, {Key: "env"}}},
		{"env=dev env=prod", []aws.TagFilter{{Key: "env", Values: []string{"dev", "prod"}}}},
		{"env=dev env", []aws.TagFilter{{Key: "env"}}},
		{"env=dev env=dev", []aws.TagFilter{{Key: "env", Values: []string{"dev"}}}},
		{"team=", []aws.TagFilter{{Key: "team", Values: []string{""}}}},
	} {
		got, err := ParseTagFilter(tc.in)
		require.NoError(t, err, tc.in)
		assert.Equal(t, tc.want, got, tc.in)
	}
	_, err := ParseTagFilter("=orders")
	require.Error(t, err)
}

func TestJumpTags_ListsTheResources(t *testing.T) {
	v, svc := newJumpTags(t, "team=orders", []aws.TagFilter{{Key: "team", Values: []string{"orders"}}})
	svc.AssertExpectations(t)
	assert.Equal(t, "jump_tags:team=orders", v.ID())
	assert.Equal(t, "Tags team=orders", v.Title())
	out := ansi.Strip(v.View().Content)
	for _, s := range []string{"Service", "Type", "Name", "Region", "lambda", "function", "orders-api", "us-east-1",
		"env=dev team=orders", "s3", "bucket", "logs-bucket", "global"} {
		assert.Contains(t, out, s)
	}
	assert.Equal(t, "2/2 resources", v.Footer())
}

// With no filter, the view lists every tagged resource and says how to
// filter.
func TestJumpTags_NoFilter(t *testing.T) {
	v, _ := newJumpTags(t, "", nil)
	assert.Equal(t, "jump_tags:", v.ID())
	assert.Equal(t, "Tag Search", v.Title())
	assert.Equal(t, "2/2 resources · every tagged resource of the Region · t to filter by tag", v.Footer())
}

// The hints show enter once, as the jump, and d as the detail.
func TestJumpTags_KeyMap(t *testing.T) {
	v, _ := newJumpTags(t, "", nil)
	var enter []string
	for _, h := range v.KeyMap() {
		if slices.Contains(h.Keys(), "enter") {
			enter = append(enter, h.Help().Desc)
		}
	}
	assert.Equal(t, []string{"jump"}, enter)
}

// enter jumps to the resource of the row, as :go ARN does.
func TestJumpTags_EnterJumps(t *testing.T) {
	v, _ := newJumpTags(t, "", nil)
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, []tea.Msg{msg.JumpMsg{Text: testTagged[0].ARN}}, collectMsgs(cmd))
}

// d opens the ARN and the tags from the row, with no call.
func TestJumpTags_Detail(t *testing.T) {
	v, _ := newJumpTags(t, "", nil)
	_, cmd := v.Update(keyPress('d'))
	panel := detailPanel(t, v, cmd)
	assert.Equal(t, "orders-api", panel.PanelTitle)
	require.Len(t, panel.Tabs, 2)
	assert.Contains(t, panel.Tabs[0].Content, testTagged[0].ARN)
	assert.Contains(t, panel.Tabs[1].Content, "orders")
}

// t asks for the tag filter with the current one filled in. The answer
// opens a new search, so Back returns to this one.
func TestJumpTags_FilterForm(t *testing.T) {
	v, _ := newJumpTags(t, "team=orders", []aws.TagFilter{{Key: "team", Values: []string{"orders"}}})
	_, cmd := v.Update(keyPress('t'))
	req, ok := cmd().(msg.RequestFormMsg)
	require.True(t, ok)
	require.Len(t, req.Fields, 1)
	assert.Equal(t, "team=orders", req.Fields[0].Default)
	require.NoError(t, req.Fields[0].Validate("env=prod tag:team"))
	require.Error(t, req.Fields[0].Validate("=x"))

	_, cmd = v.Update(msg.FormResultMsg{ID: jumpTagsForm, Values: map[string]string{"tags": " env=prod "}})
	assert.Equal(t, []msg.NavigateMsg{{ViewID: "jump_tags", Params: map[string]string{"filter": "env=prod"}}}, navigations(collectMsgs(cmd)))

	_, cmd = v.Update(msg.FormResultMsg{ID: jumpTagsForm, Aborted: true})
	assert.Nil(t, cmd)
	_, cmd = v.Update(msg.FormResultMsg{ID: "other", Values: map[string]string{"tags": "x"}})
	assert.Nil(t, cmd)
}

// A missing permission names the action, also when the error holds the
// AWS error, as GetResourcesPage builds it.
func TestJumpTags_Denied(t *testing.T) {
	svc := new(awstest.MockTaggingService)
	apiErr := &smithy.OperationError{ServiceID: "Resource Groups Tagging API", OperationName: "GetResources",
		Err: &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not allowed"}}
	svc.On("GetResourcesPage", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("%w: %w", aws.ErrTagSearchDenied, apiErr))
	v := NewJumpTags(svc, "")
	v.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	pump(v, v.Init(), 20)
	assert.Contains(t, ansi.Strip(v.View().Content), "tag:GetResources is not allowed for this identity")
}
