package service

import (
	"context"
)

// Ratings read face (step 58a, refs/proj/58 Facet A) — the fourth media-
// aggregation facet, structurally identical to intros/covers/screenshots:
// CLAIMED works bridge, BODYLESS works read native rows, strict XOR, source_id
// on every row. Unlike the byte-bearing facets the claimed bridge reads THREE
// narrow meta tables (galgame_vndb_meta + galgame_bangumi_meta +
// galgame_eg_meta — the step-10/42/60 products co-located in kun_catalog),
// each contributing at most one row.
//
// Bridge column mapping (surveyed live, 2026-07-21):
//
//	galgame_vndb_meta: galgame_id | vndb_id | rating numeric NULLABLE
//	  (kana-API wire scale 10-100 = VNDB's displayed 1-10 score × 10;
//	  NULL = VNDB publishes no rating: zero votes, or below the public-
//	  rating vote threshold — the vndbscores sync NEVER stores a fake 0,
//	  and 0 is impossible on the wire scale anyway) | vote_count | synced_at
//	  → {source: vndb, score: rating / 10, vote_count, rank: NULL}
//	galgame_bangumi_meta: galgame_id | bid | score numeric (0-10 mean) |
//	  rank bigint (0 = unranked) | total bigint (vote count = summed
//	  score_details buckets) | nsfw | synced_at
//	  → {source: bangumi, score, vote_count: total, rank: rank when > 0}
//	galgame_eg_meta: galgame_id | eg_game_id | median bigint NULLABLE
//	  (0-100 median; NULL = EG has no median) | vote_count | synced_at
//	  → {source: erogamespace, score: median, vote_count, rank: NULL}
//
// Scores stay SOURCE-NATIVE (58 拍板): 1-10 mean vs 0-10 mean vs 0-100 median
// are different semantics — normalizing would fake precision; consumers render
// per source. The vndb ÷10 is NOT normalization: it decodes the kana wire
// encoding (82.34) to the scale VNDB itself displays (8.23) — same source, its
// own native presentation. Rows without a real score never surface: a vndb
// meta row with NULL rating, a bangumi meta row with score<=0 (an unrated
// subject) and an EG meta row with NULL median are all skipped — a claimed
// work whose metas are all unscored reads [] and, per the strict XOR, never
// falls back to native rows.

// WorkRatingRow is one source's rating on a work's read face — the unified
// shape the claimed bridge (galgame_bangumi_meta ∪ galgame_eg_meta) and the
// bodyless native table (catalog_work_rating) both project into. Score is on
// the source-native scale; Rank is nil when the source has none.
type WorkRatingRow struct {
	SourceID  int16
	Score     float64
	VoteCount int
	Rank      *int
}

// loadWorkRatings assembles the rating set for a set of works, honoring the
// media-aggregation contract (refs/proj/51 §2/§3/§8, step 58a):
//
//   - CLAIMED (site='galgame_wiki'): bridge from galgame_vndb_meta,
//     galgame_bangumi_meta and galgame_eg_meta (see the file doc for the column
//     mapping). Bridge-not-copy (§2): meta rows are never materialized into
//     catalog_work_rating.
//   - BODYLESS (site=”/NULL): the work's catalog_work_rating rows.
//   - Strict XOR (§8.D): a claimed work reads ONLY the bridge; it never falls
//     back to native rows even if it still has shadowed ones (shadow-never-delete).
//
// Batched (§9.1): claimed works bridge in one query per meta table, bodyless
// works read in one catalog_work_rating query — never per-work. Each work's
// ratings are ordered by source_id ascending (vndb=2 before bangumi=3 before
// erogamespace=5). Returns a map keyed by work id; a work with no rating is
// absent (the caller renders []).
func (s *ReadService) loadWorkRatings(ctx context.Context, subjects []claimSubject) (map[int64][]WorkRatingRow, error) {
	out := make(map[int64][]WorkRatingRow, len(subjects))
	galgameIDs, galgameToWork, bodylessIDs := partitionClaimSubjects(subjects)
	if len(galgameIDs) > 0 {
		if err := s.bridgeGalgameRatings(ctx, galgameIDs, galgameToWork, out); err != nil {
			return nil, err
		}
	}
	if len(bodylessIDs) > 0 {
		if err := s.nativeWorkRatings(ctx, bodylessIDs, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// bridgeGalgameRatings reads the claimed works' three rating meta tables in ONE
// query each and maps them to the unified shape. Lanes append in seed order —
// vndb (source id 2) before bangumi (3) before erogamespace (5) — so the
// per-work slice is source_id-ascending without a sort.
func (s *ReadService) bridgeGalgameRatings(ctx context.Context, galgameIDs []int64, galgameToWork map[int64]int64, out map[int64][]WorkRatingRow) error {
	db := s.db.WithContext(ctx)

	srcIDByKey, err := s.sourceIDsByKey(ctx, []string{sourceKeyVNDB, sourceKeyBangumi, sourceKeyErogamespace})
	if err != nil {
		return err
	}

	// VNDB lane (step 60 V1): rating IS NOT NULL filters VNs without a public
	// rating (surveyed live: the vndbscores sync stores NULL — never a fake 0 —
	// for zero-vote and below-threshold VNs; 0 of 62,327 rows carry rating=0,
	// so the NULL filter is the EG-median style, not bangumi's score>0). The
	// stored value is the kana wire scale (10-100); ÷10 projects VNDB's own
	// displayed 1-10 scale. Rank is always nil (the meta table carries none).
	var vndbRows []struct {
		GalgameID int64   `gorm:"column:galgame_id"`
		Rating    float64 `gorm:"column:rating"`
		VoteCount int     `gorm:"column:vote_count"`
	}
	if err := db.Raw(`SELECT galgame_id, rating, vote_count FROM galgame_vndb_meta
		WHERE galgame_id IN ? AND rating IS NOT NULL ORDER BY galgame_id`, galgameIDs).Scan(&vndbRows).Error; err != nil {
		return err
	}
	for _, r := range vndbRows {
		workID, ok := galgameToWork[r.GalgameID]
		if !ok {
			continue
		}
		out[workID] = append(out[workID], WorkRatingRow{
			SourceID: srcIDByKey[sourceKeyVNDB], Score: r.Rating / 10, VoteCount: r.VoteCount,
		})
	}

	// Bangumi lane: score>0 filters unrated subjects (Bangumi's mean is >0 as
	// soon as one vote exists, so score>0 ≡ has ratings — the same filter the
	// bodyless backfill applies, keeping the two lanes symmetric).
	var bgmRows []struct {
		GalgameID int64   `gorm:"column:galgame_id"`
		Score     float64 `gorm:"column:score"`
		Rank      int     `gorm:"column:rank"`
		Total     int     `gorm:"column:total"`
	}
	if err := db.Raw(`SELECT galgame_id, score, rank, total FROM galgame_bangumi_meta
		WHERE galgame_id IN ? AND score > 0 ORDER BY galgame_id`, galgameIDs).Scan(&bgmRows).Error; err != nil {
		return err
	}
	for _, r := range bgmRows {
		workID, ok := galgameToWork[r.GalgameID]
		if !ok {
			continue
		}
		row := WorkRatingRow{SourceID: srcIDByKey[sourceKeyBangumi], Score: r.Score, VoteCount: r.Total}
		if r.Rank > 0 { // rank 0 = unranked on Bangumi (ranks are 1-based)
			rank := r.Rank
			row.Rank = &rank
		}
		out[workID] = append(out[workID], row)
	}

	// EG lane: median IS NOT NULL filters games EG has no median for (NULL vs a
	// real low 0 must stay distinct — the eg_meta model doc); rank is always
	// nil (EG has no rank facet).
	var egRows []struct {
		GalgameID int64 `gorm:"column:galgame_id"`
		Median    int   `gorm:"column:median"`
		VoteCount int   `gorm:"column:vote_count"`
	}
	if err := db.Raw(`SELECT galgame_id, median, vote_count FROM galgame_eg_meta
		WHERE galgame_id IN ? AND median IS NOT NULL ORDER BY galgame_id`, galgameIDs).Scan(&egRows).Error; err != nil {
		return err
	}
	for _, r := range egRows {
		workID, ok := galgameToWork[r.GalgameID]
		if !ok {
			continue
		}
		out[workID] = append(out[workID], WorkRatingRow{
			SourceID: srcIDByKey[sourceKeyErogamespace], Score: float64(r.Median), VoteCount: r.VoteCount,
		})
	}
	return nil
}

// nativeWorkRatings reads the bodyless works' catalog_work_rating rows in ONE
// query, ordered so each work's ratings are source_id-ascending.
func (s *ReadService) nativeWorkRatings(ctx context.Context, workIDs []int64, out map[int64][]WorkRatingRow) error {
	db := s.db.WithContext(ctx)
	var rows []struct {
		WorkID    int64   `gorm:"column:work_id"`
		SourceID  int16   `gorm:"column:source_id"`
		Score     float64 `gorm:"column:score"`
		VoteCount int     `gorm:"column:vote_count"`
		Rank      *int    `gorm:"column:rank"`
	}
	if err := db.Raw(`SELECT work_id, source_id, score, vote_count, rank FROM catalog_work_rating
		WHERE work_id IN ? ORDER BY work_id, source_id`, workIDs).Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		out[r.WorkID] = append(out[r.WorkID], WorkRatingRow{
			SourceID: r.SourceID, Score: r.Score, VoteCount: r.VoteCount, Rank: r.Rank,
		})
	}
	return nil
}
