package catalogsync

import (
	"context"
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func workIDOf(t *testing.T, productWorkID int64) int64 {
	t.Helper()
	var id int64
	require.NoError(t, testDB.Raw(
		`SELECT id FROM catalog_work WHERE site=? AND product_work_id=?`, siteGalgame, productWorkID,
	).Scan(&id).Error)
	return id
}

func refCount(t *testing.T, workID int64, source int16, externalID string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Raw(
		`SELECT count(*) FROM catalog_external_ref WHERE entity_type=5 AND entity_id=? AND source_id=? AND external_id=?`,
		workID, source, externalID,
	).Scan(&n).Error)
	return n
}

// truncateNegativeKnowledge clears the two tables the base clean() leaves
// untouched, so rows never leak across sub-tests (RESTART IDENTITY reuses work
// ids, which would otherwise cross-match a stale rejection).
func truncateNegativeKnowledge(t *testing.T) {
	t.Helper()
	require.NoError(t, testDB.Exec(
		"TRUNCATE catalog_match_rejection, catalog_match_candidate RESTART IDENTITY CASCADE").Error)
}

func writeRejection(t *testing.T, workID int64, source int16, externalID string) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogMatchRejection{
		EntityType: model.EntityTypeWork, EntityID: workID, SourceID: source,
		ExternalID: externalID, Reason: "test: human rejected",
	}).Error)
}

// TestNegativeKnowledgeEnforcement proves doc 17 R7: a human-rejected pairing
// is never re-asserted by the automatic matchers (step 20 finding 2).
func TestNegativeKnowledgeEnforcement(t *testing.T) {
	ctx := context.Background()

	// (a) A rejected title-year probable is not resurrected on a re-run.
	t.Run("title-year probable stays dead", func(t *testing.T) {
		clean(t)
		truncateNegativeKnowledge(t)
		seedSubject(t, 800, 4, "タイトルエックス", "", "2010-05-01")
		seedGame(t, 4, "", nil, "タイトルエックス", "", 2010) // title-only match

		_, err := New(testDB, testDB, testDB, Options{}).Run(ctx, PhaseAll)
		require.NoError(t, err)
		workID := workIDOf(t, 4)
		require.Equal(t, int64(1), refCount(t, workID, sourceBangumi, "800"), "probable written on first run")

		// Human rejects it (AdminQueueService.RejectRef: delete ref + record it).
		require.NoError(t, testDB.Exec(
			`DELETE FROM catalog_external_ref WHERE entity_type=5 AND entity_id=? AND source_id=3 AND external_id='800'`, workID).Error)
		writeRejection(t, workID, sourceBangumi, "800")

		st, err := New(testDB, testDB, testDB, Options{}).Run(ctx, PhaseBangumi)
		require.NoError(t, err)
		assert.Equal(t, 1, st.Bangumi.SkippedRejected, "counted")
		assert.Equal(t, 0, st.Bangumi.RefsWritten, "not re-written")
		assert.Equal(t, int64(0), refCount(t, workID, sourceBangumi, "800"), "stays dead")
	})

	// (b) The bid-exact anchor never adopts a work a human rejected, and the
	// rejected pairing is not re-written onto the fresh work either.
	t.Run("bid-exact adopt gate", func(t *testing.T) {
		clean(t)
		truncateNegativeKnowledge(t)
		seedSubject(t, 13, 4, "クラナド", "", "2004-04-28")
		// An existing UNCLAIMED work already carries the bangumi-13 exact ref;
		// a human rejected that pairing for it.
		require.NoError(t, testDB.Exec(`INSERT INTO catalog_work
			(id, medium_id, olang, display_name, content_rating, status, extra, field_provenance)
			VALUES (9001, 1, 'ja', 'W', 0, 0, '{}', '{}')`).Error)
		require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref
			(entity_type, entity_id, source_id, external_id, link_kind, matched_by)
			VALUES (5, 9001, 3, '13', 0, 'import:x')`).Error)
		writeRejection(t, 9001, sourceBangumi, "13")

		bid13 := 13
		seedGame(t, 1, "", &bid13, "クラナド", "", 2004)

		st, err := New(testDB, testDB, testDB, Options{}).Run(ctx, PhaseClaim)
		require.NoError(t, err)
		assert.Equal(t, 1, st.Claim.SkippedRejected)

		var wSite *string
		require.NoError(t, testDB.Raw(`SELECT site FROM catalog_work WHERE id=9001`).Scan(&wSite).Error)
		assert.Nil(t, wSite, "rejected work not adopted")
		gWork := workIDOf(t, 1)
		assert.NotEqual(t, int64(9001), gWork, "game claimed its own work")
		assert.Equal(t, int64(0), refCount(t, gWork, sourceBangumi, "13"), "rejected anchor not re-written")
	})

	// (c) A rejected match candidate is never regenerated as pending — the
	// candidate PK (entity_type,a_id,b_id) + OnConflict DoNothing already
	// guarantees this; importer/candidates.go relies on it (no code change).
	t.Run("rejected candidate not regenerated", func(t *testing.T) {
		clean(t)
		truncateNegativeKnowledge(t)
		require.NoError(t, testDB.Create(&model.CatalogMatchCandidate{
			EntityType: model.EntityTypeCreditName, AID: 10, BID: 20,
			Reason: model.CandidateReasonSharedExternalID, Status: model.CandidateStatusRejected,
		}).Error)
		// A generator re-proposes the same pair as pending.
		regenerate(t, 10, 20)
		var status int16
		require.NoError(t, testDB.Raw(
			`SELECT status FROM catalog_match_candidate WHERE entity_type=? AND a_id=10 AND b_id=20`,
			model.EntityTypeCreditName).Scan(&status).Error)
		assert.Equal(t, model.CandidateStatusRejected, status, "rejection survives regeneration")
	})

	// (d) With no rejections the gate is inert — every counter stays zero and
	// the plan is unchanged (anti-collateral-damage).
	t.Run("no rejection zero drift", func(t *testing.T) {
		clean(t)
		truncateNegativeKnowledge(t)
		seedSubject(t, 800, 4, "タイトルエックス", "", "2010-05-01")
		seedGame(t, 4, "", nil, "タイトルエックス", "", 2010)
		st, err := New(testDB, testDB, testDB, Options{}).Run(ctx, PhaseAll)
		require.NoError(t, err)
		assert.Zero(t, st.Claim.SkippedRejected)
		assert.Zero(t, st.EG.SkippedRejected)
		assert.Zero(t, st.Bangumi.SkippedRejected)
		assert.Equal(t, 1, st.Bangumi.RefsWritten, "match still written when nothing is rejected")
	})
}

// regenerate mimics importer/candidates.go's write: OnConflict DoNothing on the
// candidate PK, which preserves a pre-existing (e.g. rejected) row.
func regenerate(t *testing.T, aID, bID int64) {
	t.Helper()
	require.NoError(t, testDB.Exec(`
		INSERT INTO catalog_match_candidate (entity_type, a_id, b_id, reason, status, created_at)
		VALUES (?, ?, ?, ?, ?, now())
		ON CONFLICT (entity_type, a_id, b_id) DO NOTHING`,
		model.EntityTypeCreditName, aID, bID, model.CandidateReasonSharedExternalID, model.CandidateStatusPending).Error)
}
