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
		{13, gmodel.GalgameStatusPending, model.ClaimStateDraft},
		{14, gmodel.GalgameStatusBanned, model.ClaimStateHidden},
		{15, gmodel.GalgameStatusDeclined, model.ClaimStateHidden},
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
