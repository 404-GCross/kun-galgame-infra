// claimone_test.go — wave 146: the write-path claim lane.
//
// What these cases are actually defending: a galgame published on the wiki used
// to have NO catalog identity until 03:20 the next morning, so /v1/catalog
// answered 404 for a work the wiki was already serving. The lane must therefore
// be (a) immediate, (b) idempotent — it runs beside a nightly job that keeps
// doing the same work, (c) harmless when it fails, and (d) narrow: it must not
// mint identities for rows the nightly job would not have minted yet.
package catalogsync

import (
	"testing"

	cmodel "api/internal/platform/catalog/model"
	gmodel "api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedGameVNDB inserts a wiki galgame with an explicit status and vndb anchor.
func seedGameVNDB(t *testing.T, id int64, status int, vndbID string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`
		INSERT INTO galgame (id, vndb_id, bid, name_en_us, name_ja_jp, name_zh_cn, name_zh_tw,
		                     intro_en_us, intro_ja_jp, intro_zh_cn, intro_zh_tw,
		                     release_date, status, user_id)
		VALUES (?, ?, NULL, '', ?, '', '', '', '', '', '', NULL, ?, 1)
	`, id, vndbID, "作品"+itoa(id), status).Error)
}

// pointerOf returns the wiki cross-face pointer galgame.catalog_work_id.
func pointerOf(t *testing.T, galgameID int64) *int64 {
	t.Helper()
	var id *int64
	require.NoError(t, testDB.Raw(`SELECT catalog_work_id FROM galgame WHERE id = ?`, galgameID).
		Scan(&id).Error)
	return id
}

// TestClaimOneMintsOnPublish is the wave's core case: the anchor exists the
// moment the publication returns, with no nightly run in between — and the wiki
// pointer + the claim_state projection land with it.
func TestClaimOneMintsOnPublish(t *testing.T) {
	clean(t)
	seedGameVNDB(t, 31, gmodel.GalgameStatusPublished, "v3100")

	stats, err := ClaimOne(t.Context(), testDB, testDB, 31)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Processed)
	assert.Equal(t, 1, stats.ClaimedNew)
	assert.Equal(t, 1, stats.VNDBExact, "the wiki vndb_id is the exact anchor")

	workID := workIDOf(t, 31)
	require.NotZero(t, workID, "a published galgame must be registered immediately")

	ptr := pointerOf(t, 31)
	require.NotNil(t, ptr, "the wiki cross-face pointer must be backfilled by the same pass")
	assert.Equal(t, workID, *ptr)

	state := claimStateOfWork(t, 31)
	require.NotNil(t, state)
	assert.Equal(t, cmodel.ClaimStateLive, *state)

	// The graded anchor is what makes /v1/catalog/works/by-anchor answer.
	assert.Equal(t, int64(1), refCount(t, workID, sourceVNDB, "v3100"))
}

// TestClaimOneIsIdempotent: the lane runs beside a nightly job doing the same
// work, and a single publication can fire it more than once (a create followed
// by an immediate status edit), so a repeat must write nothing at all.
func TestClaimOneIsIdempotent(t *testing.T) {
	clean(t)
	seedGameVNDB(t, 32, gmodel.GalgameStatusPublished, "v3200")

	first, err := ClaimOne(t.Context(), testDB, testDB, 32)
	require.NoError(t, err)
	require.Equal(t, 1, first.ClaimedNew)
	workID := workIDOf(t, 32)

	second, err := ClaimOne(t.Context(), testDB, testDB, 32)
	require.NoError(t, err)
	assert.Zero(t, second.ClaimedNew, "a repeat must mint nothing")
	assert.Equal(t, 1, second.AlreadyClaimed)
	assert.Zero(t, second.WorkIDBackfilled, "a converged pointer is not rewritten")
	assert.Zero(t, second.ClaimStateWritten, "a converged projection writes nothing")
	assert.Equal(t, workID, workIDOf(t, 32), "the identity must not move")

	var works, revisions int64
	require.NoError(t, testDB.Raw(`SELECT count(*) FROM catalog_work`).Scan(&works).Error)
	require.NoError(t, testDB.Raw(`SELECT count(*) FROM catalog_revision`).Scan(&revisions).Error)
	assert.Equal(t, int64(1), works)
	assert.Equal(t, int64(1), revisions, "a repeat must not append a second birth revision")

	// And the nightly job over the same population agrees: nothing left to do.
	nightly, err := New(testDB, testDB, nil, Options{Phase: PhaseClaim}).Run(t.Context(), PhaseClaim)
	require.NoError(t, err)
	assert.Zero(t, nightly.Claim.ClaimedNew, "the safety net finds the backlog already converged")
	assert.Zero(t, nightly.Claim.ClaimStateWritten)
}

// TestClaimOneLeavesUnclaimedDraftsToTheNightlyJob pins the lane's narrowness.
// An unclaimed draft may still be withdrawn (DeleteDraft hard-deletes the row),
// and a registry identity minted for it would outlive the galgame as an orphan.
// The nightly job still claims it once it has survived to 03:20.
func TestClaimOneLeavesUnclaimedDraftsToTheNightlyJob(t *testing.T) {
	clean(t)
	seedGameVNDB(t, 33, gmodel.GalgameStatusPending, "v3300")
	seedGameVNDB(t, 34, gmodel.GalgameStatusVNDBDraft, "v3400")

	for _, id := range []int64{33, 34} {
		stats, err := ClaimOne(t.Context(), testDB, testDB, id)
		require.NoError(t, err)
		assert.Zero(t, stats.Processed, "galgame %d is an unclaimed draft — not this lane's row", id)
		assert.Zero(t, workIDOf(t, id))
	}

	// The safety net still owns them.
	nightly, err := New(testDB, testDB, nil, Options{Phase: PhaseClaim}).Run(t.Context(), PhaseClaim)
	require.NoError(t, err)
	assert.Equal(t, 2, nightly.Claim.ClaimedNew)
}

// TestClaimOneRefreshesClaimStateOfAClaimedRow: once a row IS claimed, every
// later status transition re-projects immediately — a ban stops being publicly
// listed the moment it lands, not the next morning.
func TestClaimOneRefreshesClaimStateOfAClaimedRow(t *testing.T) {
	clean(t)
	seedGameVNDB(t, 35, gmodel.GalgameStatusPublished, "v3500")
	_, err := ClaimOne(t.Context(), testDB, testDB, 35)
	require.NoError(t, err)

	require.NoError(t, testDB.Exec(`UPDATE galgame SET status = ? WHERE id = 35`,
		gmodel.GalgameStatusBanned).Error)
	stats, err := ClaimOne(t.Context(), testDB, testDB, 35)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.ClaimStateWritten)
	state := claimStateOfWork(t, 35)
	require.NotNil(t, state)
	assert.Equal(t, cmodel.ClaimStateHidden, *state)
}

// TestClaimOneTouchesOnlyItsOwnRow: the lane is a scope, not a full run wearing
// a filter — a sibling galgame published in the same minute must not be dragged
// into another request's transaction.
func TestClaimOneTouchesOnlyItsOwnRow(t *testing.T) {
	clean(t)
	seedGameVNDB(t, 36, gmodel.GalgameStatusPublished, "v3600")
	seedGameVNDB(t, 37, gmodel.GalgameStatusPublished, "v3700")

	stats, err := ClaimOne(t.Context(), testDB, testDB, 36)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Processed)
	assert.NotZero(t, workIDOf(t, 36))
	assert.Zero(t, workIDOf(t, 37), "the sibling belongs to its own call")
}

// TestHookSwallowsClaimFailure is the failure semantics the whole design rests
// on: a claim that genuinely errors must not surface to the write path. The
// injected failure is the real one the Reconciler documents — an anchor already
// owned by another product, which aborts the claim phase.
func TestHookSwallowsClaimFailure(t *testing.T) {
	clean(t)
	// Another product already owns the vndb anchor this publication carries.
	require.NoError(t, testDB.Exec(`
		INSERT INTO catalog_work (medium_id, olang, display_name, content_rating, status,
		                          site, product_work_id, display_nsfw)
		VALUES (?, 'ja', 'Owned elsewhere', 0, ?, 'moyu', 999, false)`,
		mediumGalgame, cmodel.WorkStatusLive).Error)
	var otherID int64
	require.NoError(t, testDB.Raw(
		`SELECT id FROM catalog_work WHERE site = 'moyu' AND product_work_id = 999`).Scan(&otherID).Error)
	require.NoError(t, testDB.Exec(`
		INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (?, ?, ?, 'v3800', ?, 'rule:test')`,
		cmodel.EntityTypeWork, otherID, sourceVNDB, cmodel.LinkKindExact).Error)

	seedGameVNDB(t, 38, gmodel.GalgameStatusPublished, "v3800")

	_, err := ClaimOne(t.Context(), testDB, testDB, 38)
	require.Error(t, err, "the fixture must actually make the claim fail")

	// The hook is what the write paths hold, and it never propagates that error.
	assert.NotPanics(t, func() { Hook(testDB, testDB)(t.Context(), 38) })
	assert.Zero(t, workIDOf(t, 38), "a conflict leaves the row for a human, not a second identity")

	// The publication itself is untouched — that is the point.
	var status int
	require.NoError(t, testDB.Raw(`SELECT status FROM galgame WHERE id = 38`).Scan(&status).Error)
	assert.Equal(t, gmodel.GalgameStatusPublished, status)
}

// TestWritePathLaneRefusesOtherPhases: the lane loads only what the claim phase
// needs, so eg / bangumi would compare against a truncated right-hand side and
// silently "find" nothing. Fail loudly instead.
func TestWritePathLaneRefusesOtherPhases(t *testing.T) {
	clean(t)
	rc := New(testDB, testDB, nil, Options{Phase: PhaseClaim, WritePathIDs: []int64{39}})
	_, err := rc.Run(t.Context(), PhaseAll)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write-path lane")
}
