// claimstate_test.go — A2-1e / R7: the claim VISIBILITY projection the claim
// phase maintains on catalog_work.claim_state.
//
// Two properties matter and both are pinned here: the wiki→catalog vocabulary
// mapping is total and conservative, and the write is an IDEMPOTENT
// re-projection — the doc-123 job re-runs it nightly, so a converged population
// must write zero rows, and a status change must be picked up without any
// bookkeeping of what was written last time.
package catalogsync

import (
	"testing"

	"api/internal/platform/catalog/model"
	gmodel "api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedGameStatus inserts a wiki galgame with an explicit publication status.
func seedGameStatus(t *testing.T, id int64, status int) {
	t.Helper()
	require.NoError(t, testDB.Exec(`
		INSERT INTO galgame (id, vndb_id, bid, name_en_us, name_ja_jp, name_zh_cn, name_zh_tw,
		                     intro_en_us, intro_ja_jp, intro_zh_cn, intro_zh_tw,
		                     release_date, status, user_id)
		VALUES (?, '', NULL, '', ?, '', '', '', '', '', '', NULL, ?, 1)
	`, id, "作品"+itoa(id), status).Error)
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

// claimStateOfWork reads the projected column for one wiki galgame id.
func claimStateOfWork(t *testing.T, galgameID int64) *int16 {
	t.Helper()
	var got *int16
	require.NoError(t, testDB.Raw(
		`SELECT claim_state FROM catalog_work WHERE site = ? AND product_work_id = ?`,
		siteGalgame, galgameID).Scan(&got).Error)
	return got
}

// TestClaimStateProjectionIsTotalAndIdempotent walks the whole status
// vocabulary through a real claim run, then re-runs to prove convergence and
// flips a status to prove the projection follows it.
func TestClaimStateProjectionIsTotalAndIdempotent(t *testing.T) {
	clean(t)

	cases := []struct {
		galgameID int64
		status    int
		want      int16
	}{
		{11, gmodel.GalgameStatusPublished, model.ClaimStateLive},
		{12, gmodel.GalgameStatusVNDBDraft, model.ClaimStateDraft},
		// Wave 161 P5 / 162 §4 ruling ②: pending is its OWN state, not a
		// second spelling of draft. An unclaimed VNDB stub can be claimed;
		// somebody else's submission under review cannot, and before this
		// split the wire could not tell a wizard which it was looking at.
		{13, gmodel.GalgameStatusPending, model.ClaimStatePending},
		{14, gmodel.GalgameStatusBanned, model.ClaimStateHidden},
		// The second half of the same ruling: a turned-down submission and a
		// staff-removed entry are different facts about a work, and the
		// submitter is the person who most needs them told apart.
		{15, gmodel.GalgameStatusDeclined, model.ClaimStateDeclined},
		// A status outside the vocabulary must land on the CONSERVATIVE end —
		// publishing an entry we do not understand is the failure this guards.
		{16, 77, model.ClaimStateHidden},
	}
	for _, c := range cases {
		seedGameStatus(t, c.galgameID, c.status)
	}

	rc := New(testDB, testDB, nil, Options{Phase: PhaseClaim})
	stats, err := rc.Run(t.Context(), PhaseClaim)
	require.NoError(t, err)
	require.Equal(t, len(cases), stats.Claim.ClaimedNew, "every fixture must be claimed")
	assert.Equal(t, len(cases), stats.Claim.ClaimStateWritten,
		"the first pass stamps every claimed row (they all start NULL)")

	for _, c := range cases {
		got := claimStateOfWork(t, c.galgameID)
		require.NotNil(t, got, "galgame %d never got a claim_state", c.galgameID)
		assert.Equal(t, c.want, *got, "galgame %d (wiki status %d)", c.galgameID, c.status)
	}

	// Converged re-run: zero writes. This is what keeps the nightly job free.
	rc2 := New(testDB, testDB, nil, Options{Phase: PhaseClaim})
	stats2, err := rc2.Run(t.Context(), PhaseClaim)
	require.NoError(t, err)
	assert.Equal(t, 0, stats2.Claim.ClaimStateWritten, "a converged re-run must write nothing")

	// A status change is picked up by the very same full re-projection — there
	// is no separate backfill path to forget.
	require.NoError(t, testDB.Exec(`UPDATE galgame SET status = ? WHERE id = 11`,
		gmodel.GalgameStatusBanned).Error)
	rc3 := New(testDB, testDB, nil, Options{Phase: PhaseClaim})
	stats3, err := rc3.Run(t.Context(), PhaseClaim)
	require.NoError(t, err)
	assert.Equal(t, 1, stats3.Claim.ClaimStateWritten, "exactly the one changed row")
	got := claimStateOfWork(t, 11)
	require.NotNil(t, got)
	assert.Equal(t, model.ClaimStateHidden, *got, "a banned entry must go hidden")
}

// TestClaimStateDryRunWritesNothing: the default (dry-run) mode must report the
// pending work without touching the column.
func TestClaimStateDryRunWritesNothing(t *testing.T) {
	clean(t)
	seedGameStatus(t, 21, gmodel.GalgameStatusPublished)

	// Apply once so the work exists and is claimed...
	_, err := New(testDB, testDB, nil, Options{Phase: PhaseClaim}).Run(t.Context(), PhaseClaim)
	require.NoError(t, err)
	// ...then reset the column and confirm a dry run only COUNTS the repair.
	require.NoError(t, testDB.Exec(`UPDATE catalog_work SET claim_state = NULL`).Error)

	stats, err := New(testDB, testDB, nil, Options{Phase: PhaseClaim, DryRun: true}).
		Run(t.Context(), PhaseClaim)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Claim.ClaimStateWritten, "dry run reports what WOULD change")
	assert.Nil(t, claimStateOfWork(t, 21), "dry run must not write")
}

// TestClaimStateSkipsGovernedWorks pins wave 155 ruling 1: once the registry's
// own lifecycle endpoints have acted on a claim — the presence of any
// catalog_claim_event row is the whole signal — this projector stops recomputing
// its state from galgame.status. Without the skip, an approve/decline/ban made
// through the catalog face is reverted within the day.
//
// Since wave 161 P5 this projector CAN produce all five states, but only ever
// as a function of the wiki row. A governed claim's state came from a lifecycle
// action instead, and must survive regardless of what the wiki row now says —
// which is what this test pins.
func TestClaimStateSkipsGovernedWorks(t *testing.T) {
	clean(t)
	seedGameStatus(t, 31, gmodel.GalgameStatusPublished) // stays projected
	seedGameStatus(t, 32, gmodel.GalgameStatusPublished) // taken over below

	_, err := New(testDB, testDB, nil, Options{Phase: PhaseClaim}).Run(t.Context(), PhaseClaim)
	require.NoError(t, err)

	var governedWorkID int64
	require.NoError(t, testDB.Raw(
		`SELECT id FROM catalog_work WHERE site = ? AND product_work_id = 32`, siteGalgame).
		Scan(&governedWorkID).Error)

	// The catalog face moves it to `pending` and records the event — the same
	// pair of writes ClaimLifecycleService.Act performs in one transaction.
	require.NoError(t, testDB.Exec(
		`UPDATE catalog_work SET claim_state = ? WHERE id = ?`, model.ClaimStatePending, governedWorkID).Error)
	require.NoError(t, testDB.Exec(
		`INSERT INTO catalog_claim_event (work_id, from_state, to_state, actor_uid, site)
		 VALUES (?, ?, ?, 1, ?)`,
		governedWorkID, model.ClaimStateDraft, model.ClaimStatePending, siteGalgame).Error)

	// Both wiki rows now say "published", which would project to live.
	stats, err := New(testDB, testDB, nil, Options{Phase: PhaseClaim}).Run(t.Context(), PhaseClaim)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.Claim.ClaimStateWritten, "nothing left to project")
	assert.Equal(t, 1, stats.Claim.ClaimStateGoverned, "exactly the taken-over claim is skipped")

	got := claimStateOfWork(t, 32)
	require.NotNil(t, got)
	assert.Equal(t, model.ClaimStatePending, *got, "a governed claim keeps the state the catalog gave it")

	// The ungoverned neighbour still follows the wiki, so the handover is per
	// work and the existing population is untouched.
	require.NoError(t, testDB.Exec(`UPDATE galgame SET status = ? WHERE id = 31`,
		gmodel.GalgameStatusBanned).Error)
	stats2, err := New(testDB, testDB, nil, Options{Phase: PhaseClaim}).Run(t.Context(), PhaseClaim)
	require.NoError(t, err)
	assert.Equal(t, 1, stats2.Claim.ClaimStateWritten)
	untouched := claimStateOfWork(t, 31)
	require.NotNil(t, untouched)
	assert.Equal(t, model.ClaimStateHidden, *untouched)
}

// TestClaimStateSeparatesPendingFromDraft pins wave 161 P5 (162 §4 ruling ②,
// extended to declined by the P4 verdict)
// on its own, because the property it protects is not "the table has the right
// values" but "the two populations are DISTINGUISHABLE on the wire".
//
// Before the split, a publish wizard reading claim_state=draft got both the
// unclaimed VNDB stubs it may offer to claim and the submissions already under
// somebody else's review, where claiming is guaranteed to be refused. That is
// the 160 §7-1 gap, and this is the only place the distinction is produced —
// so a regression here is silent everywhere else.
func TestClaimStateSeparatesPendingFromDraft(t *testing.T) {
	clean(t)
	seedGameStatus(t, 41, gmodel.GalgameStatusVNDBDraft) // claimable stub
	seedGameStatus(t, 42, gmodel.GalgameStatusPending)   // under review

	_, err := New(testDB, testDB, nil, Options{Phase: PhaseClaim}).Run(t.Context(), PhaseClaim)
	require.NoError(t, err)

	stub, review := claimStateOfWork(t, 41), claimStateOfWork(t, 42)
	require.NotNil(t, stub)
	require.NotNil(t, review)
	assert.Equal(t, model.ClaimStateDraft, *stub)
	assert.Equal(t, model.ClaimStatePending, *review)
	assert.NotEqual(t, *stub, *review, "the two must never collapse back onto one value")

	// The declined/hidden half of the same ruling.
	seedGameStatus(t, 43, gmodel.GalgameStatusDeclined)
	seedGameStatus(t, 44, gmodel.GalgameStatusBanned)
	_, err = New(testDB, testDB, nil, Options{Phase: PhaseClaim}).Run(t.Context(), PhaseClaim)
	require.NoError(t, err)
	turnedDown, removed := claimStateOfWork(t, 43), claimStateOfWork(t, 44)
	require.NotNil(t, turnedDown)
	require.NotNil(t, removed)
	assert.Equal(t, model.ClaimStateDeclined, *turnedDown)
	assert.Equal(t, model.ClaimStateHidden, *removed)
	assert.NotEqual(t, *turnedDown, *removed, "a turned-down submission is not a staff removal")

	// A wizard's actual query: claim_state=draft must no longer hand back the
	// row it cannot claim.
	var draftIDs []int64
	require.NoError(t, testDB.Raw(
		`SELECT product_work_id FROM catalog_work WHERE site = ? AND claim_state = ? ORDER BY product_work_id`,
		siteGalgame, model.ClaimStateDraft).Scan(&draftIDs).Error)
	assert.Equal(t, []int64{41}, draftIDs)
}
