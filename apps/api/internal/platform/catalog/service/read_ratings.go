package service

import (
	"context"
)

// Ratings read face (step 58a, refs/proj/58 Facet A) — the fourth media-
// aggregation facet, structurally identical to intros/covers/screenshots:
// CLAIMED works bridge, BODYLESS works read native rows, strict XOR, source_id
// on every row. Unlike the byte-bearing facets the claimed bridge reads TWO
// narrow meta tables (galgame_bangumi_meta + galgame_eg_meta — the step-10/42
// products co-located in kun_catalog), each contributing at most one row.
//
// Bridge column mapping (surveyed live, 2026-07-21):
//
//	galgame_bangumi_meta: galgame_id | bid | score numeric (0-10 mean) |
//	  rank bigint (0 = unranked) | total bigint (vote count = summed
//	  score_details buckets) | nsfw | synced_at
//	  → {source: bangumi, score, vote_count: total, rank: rank when > 0}
//	galgame_eg_meta: galgame_id | eg_game_id | median bigint NULLABLE
//	  (0-100 median; NULL = EG has no median) | vote_count | synced_at
//	  → {source: erogamespace, score: median, vote_count, rank: NULL}
//
// Scores stay SOURCE-NATIVE (58 拍板): 0-10 vs 0-100 are different semantics
// (mean vs median) — normalizing would fake precision; consumers render per
// source. Rows without a real score never surface: a bangumi meta row with
// score<=0 (an unrated subject) and an EG meta row with NULL median are both
// skipped — a claimed work whose metas are all unscored reads [] and, per the
// strict XOR, never falls back to native rows.

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
//   - CLAIMED (site='galgame_wiki'): bridge from galgame_bangumi_meta and
//     galgame_eg_meta (see the file doc for the column mapping). Bridge-not-copy
//     (§2): meta rows are never materialized into catalog_work_rating.
//   - BODYLESS (site=”/NULL): the work's catalog_work_rating rows.
//   - Strict XOR (§8.D): a claimed work reads ONLY the bridge; it never falls
//     back to native rows even if it still has shadowed ones (shadow-never-delete).
//
// Batched (§9.1): claimed works bridge in one query per meta table, bodyless
// works read in one catalog_work_rating query — never per-work. Each work's
// ratings are ordered by source_id ascending (bangumi=3 before erogamespace=5).
// Returns a map keyed by work id; a work with no rating is absent (the caller
// renders []).
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

// bridgeGalgameRatings reads the claimed works' two rating meta tables in ONE
// query each and maps them to the unified shape. The bangumi row appends before
// the eg row per work (bangumi source id 3 < erogamespace 5 in the seed), so
// the per-work slice is source_id-ascending without a sort.
func (s *ReadService) bridgeGalgameRatings(ctx context.Context, galgameIDs []int64, galgameToWork map[int64]int64, out map[int64][]WorkRatingRow) error {
	db := s.db.WithContext(ctx)

	srcIDByKey, err := s.sourceIDsByKey(ctx, []string{sourceKeyBangumi, sourceKeyErogamespace})
	if err != nil {
		return err
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
