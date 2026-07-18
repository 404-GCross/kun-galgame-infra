package service

import (
	"context"
	"testing"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/editspec"
	"api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The old-wire PR/revision adapter (SubmitPR / MergePR / DeclinePR / Revert /
// ListRevisions / GetRevision / GetRevisionDiff) retired at E3b along with
// apps/wiki. Its behavior — propose / merge / rebase / conflict / decline /
// revert — is the editing engine's, covered by internal/platform/editing
// (mergerebase_test.go, statemachine_test.go, policy_test.go) and the engine
// edit face (catalog/handler/edit_test.go + the forum edit BFF). What remains
// galgame-specific below is the changed_fields precision of the surviving
// direct-edit path (Update).

// createTestGalgame creates a galgame via the service (birth revision + row).
// Shared by several service tests.
func createTestGalgame(t *testing.T, vndbID, name string) *model.Galgame {
	t.Helper()
	g, err := testSvc.Create(context.Background(), 1, &dto.CreateGalgameRequest{
		VNDBID:   vndbID,
		NameZhCN: name,
	})
	require.NoError(t, err)
	return g
}

// TestUpdate_ChangedFieldsImmuneToStaleBaseline reproduces the kungal bug
// report: VNDB enrichment mutated the LIVE tables (added a screenshot, set bid)
// without minting a revision, leaving the previous revision's snapshot stale. A
// subsequent one-field user edit must record exactly that one field —
// overlayUpdate diffs against LIVE state, so the recorded changed_fields is
// immune to the stale baseline instead of over-reporting screenshots/bid. (The
// old-wire /diff read that first surfaced this retired at E3b; the
// changed_fields recording it relied on lives in Update, which this pins.)
func TestUpdate_ChangedFieldsImmuneToStaleBaseline(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g := createTestGalgame(t, "v30099", "原始名") // rev 1

	// Simulate post-creation enrichment straight into the live tables, with NO
	// revision (the historical root cause). rev 1's snapshot stays stale.
	require.NoError(t, testDB.Create(&model.GalgameScreenshot{
		GalgameID: g.ID, ImageHash: "deadbeefdeadbeef", SortOrder: 0, Source: "vndb",
	}).Error)
	require.NoError(t, testDB.Model(&model.Galgame{}).Where("id = ?", g.ID).
		Update("bid", 12345).Error)

	// User edits exactly one field. overlayUpdate diffs against live (which now
	// holds the screenshot + bid), so the recorded change set is {name_zh_cn}.
	name := "新名字"
	_, err := testSvc.Update(ctx, 1, g.ID, []string{"admin"}, &dto.UpdateGalgameRequest{NameZhCN: &name})
	require.NoError(t, err)

	// The edit revision recorded exactly the field the user changed — stale-
	// baseline drift (screenshots/bid) must NOT leak into changed_fields.
	rev2 := bridgeRevision(t, g.ID, 2)
	assert.True(t, rev2.HasChangedFields())
	assert.Equal(t, []string{"name_zh_cn"}, rev2.ChangedFieldsList())
}

// TestListRecentRevisions_MergedFeedAndCursor pins the graduated S2S feed
// (GET /galgame/revisions/recent, now served by editquery instead of the
// retired editbridge): only MERGED-action revisions feed downstream, the item
// carries the proposer's uid, and the since_id cursor is exclusive — the exact
// contract kungal/moyu crons depend on.
func TestListRecentRevisions_MergedFeedAndCursor(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g := createTestGalgame(t, "v40001", "feed-test")

	// A direct edit (engine action "updated") must NOT appear in the feed.
	name := "direct edit"
	_, err := testSvc.Update(ctx, 1, g.ID, []string{"admin"}, &dto.UpdateGalgameRequest{NameZhCN: &name})
	require.NoError(t, err)

	// A merged proposal (engine action "merged") DOES.
	prop := submitProposalForTest(t, 2, g.ID, map[string]any{editspec.FieldNameZhCN: "merged edit"}, "pr")
	mergeProposalForTest(t, 1, prop)

	feed, err := testSvc.ListRecentRevisions(ctx, 0, 100)
	require.NoError(t, err)
	require.Len(t, feed.Items, 1, "only the merged revision feeds; the direct 'updated' edit is excluded")
	item := feed.Items[0]
	assert.Equal(t, "merged", item.Action)
	assert.Equal(t, g.ID, item.GalgameID)
	assert.Equal(t, 2, item.UserID, "the proposer, not the merger")
	assert.False(t, feed.HasMore)

	// since_id is exclusive: cursoring past the last wire id returns nothing.
	after, err := testSvc.ListRecentRevisions(ctx, item.ID, 100)
	require.NoError(t, err)
	assert.Empty(t, after.Items)
}
