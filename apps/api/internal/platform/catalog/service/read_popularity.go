package service

import (
	"context"
)

// Popularity read face (step 62, the ratings facet's companion) — the sixth
// media-aggregation facet. ONE native lane for every work since the W1-pre
// nativization (refs/proj/140): the claimed dlsite counter trio the face used
// to pivot out of galgame_dlsite_meta at read time was adopted into
// catalog_work_popularity by the one-shot step t and is owned by the
// workratings importer since its claim guard came off (the Bangumi favorite
// shelves, metrics 10-14, were already native for claimed and bodyless works
// alike — T2b, refs/proj/102). The bridge and its dlsite source exclusion are
// gone.
//
// Counter semantics, preserved from mirror time: a NULL counter contributed NO
// row (DLsite genuinely does not publish dl_count for commercial works —
// absent ≠ 0), while a published 0 did (a real value the refresh loop keeps
// current). Values are VERBATIM per (source, metric) — never summed across
// sources (a DLsite sale and a Bangumi favorite are different units; consumers
// render per source_id + metric, exactly the ratings-scale rule).

// WorkPopularityRow is one (source, metric) counter on a work's read face,
// projected from catalog_work_popularity.
type WorkPopularityRow struct {
	SourceID int16
	Metric   int16
	Value    int64
}

// loadWorkPopularity assembles the popularity set for a set of works from
// catalog_work_popularity. Batched (§9.1): ONE query for the whole set — never
// per-work. Each work's rows are ordered (source_id, metric) ascending.
// Returns a map keyed by work id; a work with no popularity is absent (the
// caller renders []).
func (s *ReadService) loadWorkPopularity(ctx context.Context, subjects []claimSubject) (map[int64][]WorkPopularityRow, error) {
	out := make(map[int64][]WorkPopularityRow, len(subjects))
	if len(subjects) == 0 {
		return out, nil
	}
	workIDs := make([]int64, 0, len(subjects))
	for _, sub := range subjects {
		workIDs = append(workIDs, sub.WorkID)
	}
	return out, s.nativeWorkPopularity(ctx, workIDs, out)
}

// nativeWorkPopularity reads works' catalog_work_popularity rows in ONE query,
// ordered so each work's rows are (source_id, metric)-ascending.
func (s *ReadService) nativeWorkPopularity(ctx context.Context, workIDs []int64, out map[int64][]WorkPopularityRow) error {
	var rows []struct {
		WorkID   int64 `gorm:"column:work_id"`
		SourceID int16 `gorm:"column:source_id"`
		Metric   int16 `gorm:"column:metric"`
		Value    int64 `gorm:"column:value"`
	}
	if err := s.db.WithContext(ctx).Raw(`SELECT work_id, source_id, metric, value FROM catalog_work_popularity
		WHERE work_id IN ? ORDER BY work_id, source_id, metric`, workIDs).Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		out[r.WorkID] = append(out[r.WorkID], WorkPopularityRow{
			SourceID: r.SourceID, Metric: r.Metric, Value: r.Value,
		})
	}
	return nil
}
