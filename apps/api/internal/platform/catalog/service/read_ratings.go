package service

import (
	"context"
)

// Ratings read face (step 58a, refs/proj/58 Facet A) — the fourth media-
// aggregation facet. ONE native lane for every work since the W1-pre
// nativization (refs/proj/140): the four wiki meta tables a CLAIMED work used
// to bridge at read time (galgame_vndb_meta / galgame_bangumi_meta /
// galgame_dlsite_meta / galgame_eg_meta) were materialized into
// catalog_work_rating — the vndb rows by the daily mirror step s (score =
// rating/10, computed in float64 and stored shortest-round-trip so the read
// face reproduces the bridge's exact double), the bangumi/dlsite/eg rows by
// the one-shot adoption step t, owned by the workratings importer since its
// claim guard came off — and the bridge was deleted.
//
// Scores stay SOURCE-NATIVE (58 拍板): 1-10 mean vs 0-10 mean vs 0-5 star mean
// vs 0-100 median are different semantics — normalizing would fake precision;
// consumers render per source. Rows without a real score were never written:
// a NULL vndb rating, a bangumi score<=0 (an unrated subject), a NULL dlsite
// star / rate_count 0 and a NULL EG median all yielded no row, at mirror time
// exactly as they yielded none at bridge time — the sources' "no rating" is
// absence, never a fake 0.

// WorkRatingRow is one source's rating on a work's read face. Score is on the
// source-native scale; Rank is nil when the source has none.
type WorkRatingRow struct {
	SourceID  int16
	Score     float64
	VoteCount int
	Rank      *int
}

// loadWorkRatings assembles the rating set for a set of works from
// catalog_work_rating. Batched (§9.1): ONE query for the whole set — never
// per-work. Each work's ratings are ordered by source_id ascending (vndb=2
// before bangumi=3 before dlsite=4 before erogamespace=5 — the order the old
// bridge appended its lanes in). Returns a map keyed by work id; a work with
// no rating is absent (the caller renders []).
func (s *ReadService) loadWorkRatings(ctx context.Context, subjects []claimSubject) (map[int64][]WorkRatingRow, error) {
	out := make(map[int64][]WorkRatingRow, len(subjects))
	if len(subjects) == 0 {
		return out, nil
	}
	workIDs := make([]int64, 0, len(subjects))
	for _, sub := range subjects {
		workIDs = append(workIDs, sub.WorkID)
	}
	return out, s.nativeWorkRatings(ctx, workIDs, out)
}

// nativeWorkRatings reads the works' catalog_work_rating rows in ONE query,
// ordered so each work's ratings are source_id-ascending.
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
