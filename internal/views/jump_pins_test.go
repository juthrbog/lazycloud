package views

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
)

// Pin tests for the jump design (docs/design/jump.md). They pin the hazards
// as they are today (TestJumpHazard_*). The PR that fixes a hazard flips its
// test. H1, H6 and H10 are pinned in internal/app, H7 and H8 in
// internal/ui, and H11 in internal/aws. H4 is pinned by
// TestLambdaFunctions_FocusMissing. H9 comes with the Tagging API module in
// PR 6.

// H2, fixed in PR 2: a list whose FocusKey is a name misses a focus by ARN,
// also when the row key is that ARN. The Lambda list then says that no
// function has the ARN as its name.
func TestJumpHazard_FocusByARNMisses(t *testing.T) {
	v := newFunctions(t, &fakeLambda{pages: [][]aws.Function{{testFunction}}, detail: testDetail()}, 160)
	require.Len(t, v.items, 1)
	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: testFunction.ARN})
	msgs := collectMsgs(cmd)
	ts := toasts(msgs)
	require.Len(t, ts, 1)
	assert.Equal(t, "No function named "+testFunction.ARN+" in the loaded functions", ts[0].Text)
	for _, m := range msgs {
		_, isDetail := m.(listDetailMsg)
		assert.False(t, isDetail, "no detail opens")
	}

	// The name still works.
	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: testFunction.Name})
	_, isDetail := cmd().(listDetailMsg)
	assert.True(t, isDetail)
}

// H3, fixed in PR 2: a focus on an ID that is not in a list that loads every
// row does nothing and shows nothing.
func TestJumpHazard_MissingIDShowsNothing(t *testing.T) {
	v, _ := newCharSG(t, aws.SecurityGroup{ID: "sg-111", Name: "web"})
	require.Len(t, v.items, 1)
	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "sg-0123456789abcdef0"})
	assert.Empty(t, collectMsgs(cmd))
}

// H5, fixed in PR 2: s3_objects has no list core. After its first page, a
// focus on a loaded key does not move the cursor, and a focus on a key that
// is not loaded shows no message.
func TestJumpHazard_ObjectListDropsAFocus(t *testing.T) {
	v, _ := newTestS3Objects()
	v.Update(s3PageLoadedMsg{gen: v.loadGen, bucket: "test-bucket",
		objects: []aws.S3Object{{Key: "a.csv"}, {Key: "q3.csv"}}, pageNum: 1})
	require.False(t, v.loading)
	first := v.table.CursorKey()
	require.NotEmpty(t, first)

	_, cmd := v.Update(msg.FocusResourceMsg{ResourceID: "q3.csv"})
	assert.Empty(t, collectMsgs(cmd))
	assert.Equal(t, first, v.table.CursorKey(), "the cursor stays")

	_, cmd = v.Update(msg.FocusResourceMsg{ResourceID: "not-loaded.csv"})
	assert.Empty(t, collectMsgs(cmd), "no message")
}
