// public_tag_counts_test.go — the tag rollup.
//
// The load-bearing test here is the drift guard: a rollup is only worth having
// if the stored number is the number the live aggregate would have produced.
// Staleness is the tradeoff the table was built to make; disagreement is not,
// and it is the failure a rollup fails by — silently, because both halves keep
// answering with confidence.
package service

import (
	"testing"
	"time"

	"api/internal/platform/catalog/model"
)

const tagCountSrc int16 = 3

// tagRollupFixture builds one canonical tag over a population that exercises
// every axis the three stored columns separate: an all-ages work, an r18 work,
// an editorially-nsfw work, plus rows that must NOT count (an unclaimed work, a
// stub, and a second tag that shares nothing).
func tagRollupFixture(t *testing.T) (hot, cold int64) {
	t.Helper()
	cleanTables(t)
	cleanTagTables(t)

	hot = createCanonicalTag(t, "romance", model.TagTierCore, model.TagKindContent)
	cold = createCanonicalTag(t, "orphan", model.TagTierCore, model.TagKindContent)
	for _, name := range []string{"恋愛", "ロマンス"} {
		// TWO source names onto ONE canonical tag — the reason the aggregate
		// counts DISTINCT works and the reason a naive rollup would double.
		if err := testDB.Create(&model.CatalogTagSourceMap{
			SourceID: tagCountSrc, SourceName: name, TagID: hot,
		}).Error; err != nil {
			t.Fatalf("map source tag %s: %v", name, err)
		}
	}

	safe := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "RollupSafe")
	r18 := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "RollupR18")
	stub := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusStub, "RollupStub")
	loose := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "RollupUnclaimed")
	for i, id := range []int64{safe.ID, r18.ID, stub.ID} {
		claimLive(t, id, int64(9500+i))
	}
	// loose stays unclaimed: claim_state=live is the gate, so it must not count.

	for _, w := range []int64{safe.ID, r18.ID, stub.ID, loose.ID} {
		for _, name := range []string{"恋愛", "ロマンス"} {
			if err := testDB.Create(&model.CatalogWorkTag{
				WorkID: w, Name: name, Count: 1, SourceID: tagCountSrc,
			}).Error; err != nil {
				t.Fatalf("work tag on %d: %v", w, err)
			}
		}
	}
	// The r18 work is also the editorially-nsfw one, so n_nsfw has something to
	// find that is not simply "every r18 row".
	if err := testDB.Model(&model.CatalogWork{}).Where("id = ?", r18.ID).
		Update("display_nsfw", true).Error; err != nil {
		t.Fatalf("set display_nsfw: %v", err)
	}
	return hot, cold
}

// TestTagRollupMatchesTheLiveAggregate is the drift guard. Whatever the gates
// come to mean, the two halves must keep meaning the same thing — and the only
// way to hold that is to compute both and compare.
func TestTagRollupMatchesTheLiveAggregate(t *testing.T) {
	hot, cold := tagRollupFixture(t)
	ctx := t.Context()
	svc := newPublicSvc()

	st, err := svc.RefreshTagWorkCounts(ctx, time.Now())
	if err != nil {
		t.Fatalf("RefreshTagWorkCounts: %v", err)
	}
	if st.Rows != 1 {
		t.Fatalf("refresh wrote %d rows, want 1 (only the mapped tag reaches a work)", st.Rows)
	}

	ids := []int64{hot, cold}
	for _, nsfw := range []bool{false, true} {
		liveN, liveNSFW, err := svc.workCountsLive(ctx, tagWorkEdge, ids, nsfw)
		if err != nil {
			t.Fatalf("nsfw=%v: live: %v", nsfw, err)
		}
		rollN, rollNSFW, err := svc.workCountsFromRollup(ctx, tagWorkEdge, ids, nsfw)
		if err != nil {
			t.Fatalf("nsfw=%v: rollup: %v", nsfw, err)
		}
		for _, id := range ids {
			if liveN[id] != rollN[id] {
				t.Fatalf("nsfw=%v tag %d: rollup count %d != live %d", nsfw, id, rollN[id], liveN[id])
			}
			if liveNSFW[id] != rollNSFW[id] {
				t.Fatalf("nsfw=%v tag %d: rollup nsfw tally %d != live %d", nsfw, id, rollNSFW[id], liveNSFW[id])
			}
		}
	}

	// And the numbers are the ones the fixture was built to produce, so a
	// mutual regression in both halves cannot pass the comparison above.
	sfw, _, err := svc.workCountsFromRollup(ctx, tagWorkEdge, ids, false)
	if err != nil {
		t.Fatalf("rollup sfw: %v", err)
	}
	nsfwCounts, nsfwWorks, err := svc.workCountsFromRollup(ctx, tagWorkEdge, ids, true)
	if err != nil {
		t.Fatalf("rollup nsfw: %v", err)
	}
	if sfw[hot] != 1 {
		t.Fatalf("sfw count = %d, want 1 (the all-ages claimed work only)", sfw[hot])
	}
	if nsfwCounts[hot] != 2 {
		t.Fatalf("nsfw count = %d, want 2 (all-ages + r18, still not the stub or the unclaimed)", nsfwCounts[hot])
	}
	if nsfwWorks[hot] != 1 {
		t.Fatalf("display-nsfw tally = %d, want 1", nsfwWorks[hot])
	}
	if _, ok := sfw[cold]; ok {
		t.Fatalf("a tag no work carries is present in the map, want absent (renders 0)")
	}
}

// TestTagRollupServesTheChipAndAgreesWithTheList closes the loop the whole
// invariant is about: the number on the chip a reader sees equals the length of
// the list following it gives them.
func TestTagRollupServesTheChipAndAgreesWithTheList(t *testing.T) {
	hot, _ := tagRollupFixture(t)
	ctx := t.Context()
	svc := newPublicSvc()
	if _, err := svc.RefreshTagWorkCounts(ctx, time.Now()); err != nil {
		t.Fatalf("RefreshTagWorkCounts: %v", err)
	}

	var safeID int64
	if err := testDB.Raw(`SELECT id FROM catalog_work WHERE display_name = 'RollupSafe'`).
		Scan(&safeID).Error; err != nil || safeID == 0 {
		t.Fatalf("fixture work lookup: id=%d err=%v", safeID, err)
	}

	for _, nsfw := range []bool{false, true} {
		rec, found, err := svc.WorkDetail(ctx, safeID, PublicInclude{}, nsfw, 0)
		if err != nil || !found {
			t.Fatalf("nsfw=%v: WorkDetail = %v, %v", nsfw, found, err)
		}
		var got int
		var seen bool
		for _, tag := range rec.Tags {
			if tag.CanonicalID != hot {
				continue
			}
			if tag.WorkCount == nil {
				t.Fatalf("nsfw=%v: mapped tag chip carries no work_count", nsfw)
			}
			// Both source names map to the same tag, so the chip appears twice
			// on the record; every appearance must carry the same number.
			if seen && *tag.WorkCount != got {
				t.Fatalf("nsfw=%v: the same tag reports %d and %d on one record", nsfw, got, *tag.WorkCount)
			}
			got, seen = *tag.WorkCount, true
		}
		if !seen {
			t.Fatalf("nsfw=%v: the mapped tag is missing from the record", nsfw)
		}
		assertCountMatchesWorksList(t, svc, WorksListFilter{Sort: "id", TagIDs: []int64{hot}, NSFW: nsfw}, got)
	}
}

// TestTagRollupFallsBackBeforeItIsEverFilled pins the deploy window: the read
// half can ship before anything has run the write half, and until then it must
// answer from the live aggregate rather than tell every reader zero.
func TestTagRollupFallsBackBeforeItIsEverFilled(t *testing.T) {
	hot, _ := tagRollupFixture(t)
	ctx := t.Context()
	svc := newPublicSvc()

	// No refresh has run — the table is empty.
	counts, _, err := svc.workCountsFromRollup(ctx, tagWorkEdge, []int64{hot}, true)
	if err != nil {
		t.Fatalf("rollup on an empty table: %v", err)
	}
	if counts[hot] != 2 {
		t.Fatalf("unfilled rollup returned %d, want the live 2 — a fresh deploy must not zero every chip", counts[hot])
	}

	// Once it holds anything, a tag that is absent from it means zero and is
	// believed: the fallback is a bootstrap, not a permanent second opinion.
	if _, err := svc.RefreshTagWorkCounts(ctx, time.Now()); err != nil {
		t.Fatalf("RefreshTagWorkCounts: %v", err)
	}
	if err := testDB.Exec(`DELETE FROM catalog_tag_work_count WHERE tag_id = ?`, hot).Error; err != nil {
		t.Fatalf("delete rollup row: %v", err)
	}
	if err := testDB.Create(&model.CatalogTagWorkCount{
		TagID: 999001, NAll: 7, NSfw: 7, NNsfw: 0, ComputedAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed an unrelated row: %v", err)
	}
	counts, _, err = svc.workCountsFromRollup(ctx, tagWorkEdge, []int64{hot}, true)
	if err != nil {
		t.Fatalf("rollup on a filled table: %v", err)
	}
	if _, ok := counts[hot]; ok {
		t.Fatalf("a filled rollup fell back to live for a missing key, want the stored zero")
	}
}

// TestTagRollupPrunesWhatNoLongerCounts: a tag that loses its last work must
// lose its row, not keep a stale positive number forever.
func TestTagRollupPrunesWhatNoLongerCounts(t *testing.T) {
	hot, _ := tagRollupFixture(t)
	ctx := t.Context()
	svc := newPublicSvc()
	if _, err := svc.RefreshTagWorkCounts(ctx, time.Now()); err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	if err := testDB.Exec(`DELETE FROM catalog_work_tag`).Error; err != nil {
		t.Fatalf("drop the edges: %v", err)
	}
	st, err := svc.RefreshTagWorkCounts(ctx, time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if st.Rows != 0 || st.Pruned != 1 {
		t.Fatalf("refresh = %d rows / %d pruned, want 0 / 1", st.Rows, st.Pruned)
	}
	var left int64
	if err := testDB.Raw(`SELECT count(*) FROM catalog_tag_work_count WHERE tag_id = ?`, hot).
		Scan(&left).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if left != 0 {
		t.Fatalf("the tag kept %d rollup rows after losing every work", left)
	}
}
