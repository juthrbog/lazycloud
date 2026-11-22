package views

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
)

// Pin tests for the jump design (docs/design/jump.md). PR 2 flipped H2, H3
// and H5 here, and H4 in TestLambdaFunctions_FocusMissing. H1, H6 and H10
// are pinned in internal/app, H7 and H8 in internal/ui, and H11 in
// internal/aws. H9 comes with the Tagging API module in PR 6.

// H2, fixed in PR 2: a list whose FocusKey is a name missed a focus by ARN,
// also when the row key was that ARN. findFocus now tries Key after
// FocusKey, so the ARN finds the row and opens its detail.
func TestJumpFocusByARNFindsTheRow(t *testing.T) {
	f := &fakeLambda{pages: [][]aws.Function{{testFunction}}, detail: testDetail()}
	v := newFunctions(t, f, 160)
	require.Len(t, v.items, 1)
	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: testFunction.ARN})
	assert.NotEmpty(t, detailPanel(t, v, cmd).Tabs)
	assert.Equal(t, testFunction.ARN, v.table.CursorKey())

	// The name still works.
	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: testFunction.Name})
	_, isDetail := cmd().(listDetailMsg)
	assert.True(t, isDetail)
}

// H3, fixed in PR 2: a focus on an ID that is not in a list that loads every
// row did nothing and showed nothing. Now a toast names it.
func TestJumpMissingIDGivesAToast(t *testing.T) {
	v, _ := newCharSG(t, aws.SecurityGroup{ID: "sg-111", Name: "web"})
	require.Len(t, v.items, 1)
	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "sg-0123456789abcdef0"})
	assert.Equal(t, []tea.Msg{msg.ToastInfo("No security group sg-0123456789abcdef0 in the list")}, collectMsgs(cmd))
}

// H5, fixed in PR 2: s3_objects dropped a focus. Now a focus on a loaded key
// moves the cursor, a focus on a key that is not there gives a toast, and a
// focus that comes during the load waits for the last page.
func TestJumpObjectListTakesAFocus(t *testing.T) {
	v, _ := newTestS3Objects()
	v.Update(s3PageLoadedMsg{gen: v.loadGen, bucket: "test-bucket",
		objects: []aws.S3Object{{Key: "a.csv"}, {Key: "q3.csv"}}, pageNum: 1})
	require.False(t, v.loading)

	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "q3.csv"})
	assert.Empty(t, collectMsgs(cmd))
	assert.Equal(t, "q3.csv", v.table.CursorKey())

	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: "not-there.csv"})
	assert.Equal(t, []tea.Msg{msg.ToastInfo("No object not-there.csv in the list")}, collectMsgs(cmd))
	assert.Equal(t, "q3.csv", v.table.CursorKey(), "the cursor stays")

	// During the load, the focus waits for the last page.
	w, _ := newTestS3Objects()
	require.True(t, w.loading)
	_, cmd = w.Update(msg.FocusResourceMsg{ResourceID: "z.csv"})
	assert.Nil(t, cmd)
	w.Update(s3PageLoadedMsg{gen: w.loadGen, bucket: "test-bucket",
		objects: []aws.S3Object{{Key: "a.csv"}}, hasMorePages: true, token: new("t"), pageNum: 1})
	assert.Equal(t, "a.csv", w.table.CursorKey(), "not yet")
	_, cmd = w.Update(s3PageLoadedMsg{gen: w.loadGen, bucket: "test-bucket",
		objects: []aws.S3Object{{Key: "z.csv"}}, pageNum: 2})
	assert.Empty(t, collectMsgs(cmd))
	assert.Equal(t, "z.csv", w.table.CursorKey())

	// An error of another call (a head or a read) while the pages load
	// keeps the focus that waits.
	e, _ := newTestS3Objects()
	e.Update(msg.FocusResourceMsg{ResourceID: "z.csv"})
	e.Update(s3PageLoadedMsg{gen: e.loadGen, bucket: "test-bucket",
		objects: []aws.S3Object{{Key: "a.csv"}}, hasMorePages: true, token: new("t"), pageNum: 1})
	e.Update(msg.ErrorMsg{Err: errors.New("head s3://test-bucket/a.csv: 403")})
	e.Update(s3PageLoadedMsg{gen: e.loadGen, bucket: "test-bucket",
		objects: []aws.S3Object{{Key: "z.csv"}}, pageNum: 2})
	assert.Equal(t, "z.csv", e.table.CursorKey())
}
