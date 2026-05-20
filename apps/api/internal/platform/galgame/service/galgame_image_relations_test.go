package service

import (
	"context"
	"testing"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sha-256-length hash literals for cover/screenshot tests. The wiki
// never inspects bytes — image_service does — so any 64-char value
// passes validation. Keeping them as named constants makes the test
// bodies readable.
const (
	hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	hashC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func TestApplySnapshot_RebuildsCoversAndScreenshots(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()
	gid := makeGalgame(t)

	cv := []dto.GalgameCoverInput{
		{ImageHash: hashA, SortOrder: 0, Sexual: 0, Source: "vndb", SourceKey: "cv1"},
		{ImageHash: hashB, SortOrder: 1, Sexual: 1, Source: "user"},
	}
	sh := []dto.GalgameScreenshotInput{
		{ImageHash: hashC, SortOrder: 0, Caption: "CG 01"},
	}

	_, err := testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{
		Covers: &cv, Screenshots: &sh,
	})
	require.NoError(t, err)

	// DB matches: two cover rows, one screenshot row.
	var cCount, sCount int64
	testDB.Model(&model.GalgameCover{}).Where("galgame_id = ?", gid).Count(&cCount)
	testDB.Model(&model.GalgameScreenshot{}).Where("galgame_id = ?", gid).Count(&sCount)
	assert.Equal(t, int64(2), cCount)
	assert.Equal(t, int64(1), sCount)

	// snapshot has both, canonical-sorted by hash (a < b < c).
	snap := latestRevSnapshot(t, gid)
	require.Len(t, snap.Covers, 2)
	require.Len(t, snap.Screenshots, 1)
	assert.Equal(t, hashA, snap.Covers[0].ImageHash)
	assert.Equal(t, hashB, snap.Covers[1].ImageHash)
	assert.Equal(t, "vndb", snap.Covers[0].Source)
	assert.Equal(t, "cv1", snap.Covers[0].SourceKey)
	assert.Equal(t, int16(1), snap.Covers[1].Sexual)
	assert.Equal(t, "CG 01", snap.Screenshots[0].Caption)
}

func TestUpdate_CoversPresenceSemantics(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()
	gid := makeGalgame(t)

	// Initial: set 2 covers.
	cv0 := []dto.GalgameCoverInput{
		{ImageHash: hashA, SortOrder: 0},
		{ImageHash: hashB, SortOrder: 1},
	}
	_, err := testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{Covers: &cv0})
	require.NoError(t, err)
	beforeRev := revCount(t, gid)

	// Omitting Covers must NOT touch them — only edit Name.
	nm := "新名"
	_, err = testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{NameZhCN: &nm})
	require.NoError(t, err)
	var cCount int64
	testDB.Model(&model.GalgameCover{}).Where("galgame_id = ?", gid).Count(&cCount)
	assert.Equal(t, int64(2), cCount, "covers preserved when Covers field omitted")
	assert.Equal(t, int64(1), revCount(t, gid)-beforeRev, "edit only produced one revision")

	// Empty slice must CLEAR all covers.
	empty := []dto.GalgameCoverInput{}
	_, err = testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{Covers: &empty})
	require.NoError(t, err)
	testDB.Model(&model.GalgameCover{}).Where("galgame_id = ?", gid).Count(&cCount)
	assert.Equal(t, int64(0), cCount, "empty []  wipes all covers")
}

func TestGalgameCover_PartialUniqueIndexEnforcesOnePinned(t *testing.T) {
	cleanTables(t)
	getRepos()
	gid := makeGalgame(t)

	// First sort_order=0 row is fine.
	require.NoError(t, testDB.Create(&model.GalgameCover{
		GalgameID: gid, ImageHash: hashA, SortOrder: 0,
	}).Error)

	// Second sort_order=0 row for SAME galgame must be rejected by the
	// partial unique index `idx_galgame_cover_pinned`.
	err := testDB.Create(&model.GalgameCover{
		GalgameID: gid, ImageHash: hashB, SortOrder: 0,
	}).Error
	require.Error(t, err, "two covers with sort_order=0 must violate the partial unique index")

	// A non-zero sort_order for the same galgame is fine (index is
	// partial — only sort_order=0 is constrained).
	require.NoError(t, testDB.Create(&model.GalgameCover{
		GalgameID: gid, ImageHash: hashB, SortOrder: 1,
	}).Error)
}

func TestPinNewBanner_FlowAtomicallyDemotesOld(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()
	gid := makeGalgame(t)

	// Start with hashA pinned.
	cv0 := []dto.GalgameCoverInput{{ImageHash: hashA, SortOrder: 0}}
	_, err := testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{Covers: &cv0})
	require.NoError(t, err)

	// Admin "pins new banner" by submitting the full set with hashB
	// at sort_order=0 and hashA demoted to 1. Because ApplySnapshot
	// clears-and-rebuilds, the demotion and promotion happen together;
	// the partial unique index never observes a two-row collision.
	cv1 := []dto.GalgameCoverInput{
		{ImageHash: hashB, SortOrder: 0},
		{ImageHash: hashA, SortOrder: 1},
	}
	_, err = testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{Covers: &cv1})
	require.NoError(t, err)

	// Effective banner now points at hashB.
	got, _, err := testSvc.GetByID(ctx, gid)
	require.NoError(t, err)
	require.NotNil(t, got.EffectiveBannerHash)
	assert.Equal(t, hashB, *got.EffectiveBannerHash)
}

// TestPromoteCoverHash_Create_InjectsAsPinnedCover verifies that a
// multipart-uploaded banner on Create (handler sets req.PromoteCoverHash)
// becomes the sort_order=0 cover of the new galgame. Without this, the
// post-PR5 multipart UX path is invisible — buildCreateSnapshot's promote
// merge could silently regress and tests would still pass.
func TestPromoteCoverHash_Create_InjectsAsPinnedCover(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	const uploadedHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	g, err := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{
		VNDBID:           "v123456",
		NameZhCN:         "test",
		PromoteCoverHash: uploadedHash, // ← what handler sets after multipart upload
	})
	require.NoError(t, err)

	// Cover row exists at sort_order=0 with the uploaded hash.
	var cover model.GalgameCover
	require.NoError(t, testDB.Where("galgame_id = ? AND sort_order = 0", g.ID).
		First(&cover).Error)
	assert.Equal(t, uploadedHash, cover.ImageHash)

	// Effective banner derives correctly from it.
	got, _, err := testSvc.GetByID(ctx, g.ID)
	require.NoError(t, err)
	require.NotNil(t, got.EffectiveBannerHash)
	assert.Equal(t, uploadedHash, *got.EffectiveBannerHash)
}

// TestPromoteCoverHash_Update_PreservesExistingCovers verifies the
// "upload new banner WITHOUT wiping the gallery" contract. The user has
// an existing gallery (covers + screenshots). They upload a new banner
// via multipart on Update. Expected: the new hash becomes sort_order=0,
// the old pinned cover demotes to sort_order=1, all other covers
// untouched. This is the entire reason PromoteCoverHash exists as a
// transient field instead of forcing the user to round-trip the full
// covers[] in the JSON body.
func TestPromoteCoverHash_Update_PreservesExistingCovers(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()
	gid := makeGalgame(t)

	// Seed: gallery with 3 covers (pinned + two others).
	const (
		oldPinned = "1111111111111111111111111111111111111111111111111111111111111111"
		other1    = "2222222222222222222222222222222222222222222222222222222222222222"
		other2    = "3333333333333333333333333333333333333333333333333333333333333333"
		uploaded  = "4444444444444444444444444444444444444444444444444444444444444444"
	)
	seed := []dto.GalgameCoverInput{
		{ImageHash: oldPinned, SortOrder: 0},
		{ImageHash: other1, SortOrder: 1},
		{ImageHash: other2, SortOrder: 2},
	}
	_, err := testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{Covers: &seed})
	require.NoError(t, err)

	// Simulate the multipart-upload edit: handler sets PromoteCoverHash,
	// leaves req.Covers nil (= keep current set, just swap the pinned).
	_, err = testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{
		PromoteCoverHash: uploaded,
	})
	require.NoError(t, err)

	// Expected end state: 4 covers, uploaded is pinned, oldPinned demoted.
	var rows []model.GalgameCover
	require.NoError(t, testDB.Where("galgame_id = ?", gid).
		Order("image_hash").Find(&rows).Error)
	require.Len(t, rows, 4, "all existing covers must survive; uploaded gets added")

	bySort := map[int]string{}
	for _, r := range rows {
		bySort[r.SortOrder] = r.ImageHash
		if r.SortOrder == 0 {
			assert.Equal(t, uploaded, r.ImageHash, "uploaded hash MUST be pinned")
		}
	}
	// oldPinned was at sort_order=0; demote moves it to sort_order=1.
	// other1 was at 1; the demote DOESN'T renumber other rows (those are
	// caller-managed), so we end up with TWO rows at sort_order=1.
	// That's allowed (only sort_order=0 has a partial unique constraint);
	// effective_banner picks uploaded by virtue of being the unique 0.
	got, _, err := testSvc.GetByID(ctx, gid)
	require.NoError(t, err)
	require.NotNil(t, got.EffectiveBannerHash)
	assert.Equal(t, uploaded, *got.EffectiveBannerHash, "new uploaded banner wins")
}

// TestPromoteCoverHash_Update_IdempotentWhenAlreadyPinned: if the user
// re-uploads (somehow) the SAME hash that's already the pinned cover,
// nothing should change — and importantly, no second cover row should
// be added.
func TestPromoteCoverHash_Update_IdempotentWhenAlreadyPinned(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()
	gid := makeGalgame(t)

	const pinned = "5555555555555555555555555555555555555555555555555555555555555555"
	seed := []dto.GalgameCoverInput{{ImageHash: pinned, SortOrder: 0}}
	_, err := testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{Covers: &seed})
	require.NoError(t, err)

	// Re-upload same hash.
	_, err = testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{
		PromoteCoverHash: pinned,
	})
	require.NoError(t, err)

	var cnt int64
	testDB.Model(&model.GalgameCover{}).Where("galgame_id = ?", gid).Count(&cnt)
	assert.Equal(t, int64(1), cnt, "no duplicate row for the same pinned hash")
}

// TestEffectiveBannerHash_NilWhenNoCovers verifies the post-PR5 contract:
// galgame_cover is the SOLE source for the pinned banner. A galgame
// with no cover rows has no effective banner — no legacy fallback.
// (Pre-PR5 this test asserted a fallback to banner_image_hash; that
// column was retired together with PR5's migrate-drop-banner-image-hash.)
func TestEffectiveBannerHash_NilWhenNoCovers(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()
	gid := makeGalgame(t)

	got, _, err := testSvc.GetByID(ctx, gid)
	require.NoError(t, err)
	assert.Nil(t, got.EffectiveBannerHash, "no covers → no effective banner; frontend falls back to legacy Banner URL")
}

// TestRevert_GracefulDegradeOnMissingImage verifies the defence-in-depth
// pre-check on Revert: when the configured image-existence probe reports
// a hash from the target snapshot is missing in image_service, the
// snapshot is stripped (cover row not resurrected) and the new revision's
// Note explains why. Without this, revert would resurrect a galgame
// pointing at a now-deleted image and the UI would render a broken
// thumbnail with no audit trail.
func TestRevert_GracefulDegradeOnMissingImage(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()
	gid := makeGalgame(t)

	// rev 1: set two covers.
	cv0 := []dto.GalgameCoverInput{
		{ImageHash: hashA, SortOrder: 0},
		{ImageHash: hashB, SortOrder: 1},
	}
	_, err := testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{Covers: &cv0})
	require.NoError(t, err)
	// The "rev 1" referenced in this test is the most recent revision
	// id after the update above.
	rev1 := latestRevisionID(t, gid)

	// rev 2: drop both covers. hashA / hashB are now orphan from the
	// "live" perspective — only kept alive in image_service by refping
	// reading the rev 1 snapshot.
	empty := []dto.GalgameCoverInput{}
	_, err = testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{Covers: &empty})
	require.NoError(t, err)

	// Simulate: image_service has TTL-deleted hashA (refping must have
	// missed it). hashB is still alive. We do not actually call
	// image_service — the probe is a function field on the service.
	testSvc.WithImageProbe(func(_ context.Context, hashes []string) ([]string, error) {
		missing := []string{}
		for _, h := range hashes {
			if h == hashA {
				missing = append(missing, h)
			}
		}
		return missing, nil
	})
	defer testSvc.WithImageProbe(nil) // reset for other tests

	// Revert to rev 1 — expect hashA stripped, hashB restored.
	require.NoError(t, testSvc.Revert(ctx, 1, gid, rev1, []string{"admin"}))

	var coverCount int64
	testDB.Model(&model.GalgameCover{}).Where("galgame_id = ?", gid).Count(&coverCount)
	assert.Equal(t, int64(1), coverCount, "missing hashA should be stripped; hashB survives")

	var keptHash string
	require.NoError(t, testDB.Model(&model.GalgameCover{}).Where("galgame_id = ?", gid).
		Select("image_hash").Take(&keptHash).Error)
	assert.Equal(t, hashB, keptHash)

	// The new "reverted" revision's Note must mention the degrade so
	// admins find their way to re-uploading. Substring match — exact
	// wording lives in revision_service.scrubMissingImages.
	var note string
	require.NoError(t, testDB.Model(&model.GalgameRevision{}).
		Where("galgame_id = ?", gid).
		Order("revision DESC").Select("note").Take(&note).Error)
	assert.Contains(t, note, "partial revert")
	assert.Contains(t, note, "image_service")
}

// latestRevisionID returns the most recent revision number for gid,
// or 0 when the galgame has no revisions yet. The COALESCE wrap is
// required because MAX(revision) on an empty set is SQL NULL which
// Go's `int` scan refuses; tests that probe "rev before vs rev after"
// pre-revision rely on the 0 sentinel meaning "no revisions yet".
func latestRevisionID(t *testing.T, gid int) int {
	t.Helper()
	var rev int
	require.NoError(t, testDB.Model(&model.GalgameRevision{}).
		Where("galgame_id = ?", gid).
		Select("COALESCE(MAX(revision), 0)").Scan(&rev).Error)
	return rev
}
