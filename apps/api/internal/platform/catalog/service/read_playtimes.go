package service

// Playtimes read face (step 91, refs/proj/91) — the playtime facet. Unlike
// intros/covers/screenshots/ratings/tags/popularity this facet has NO claimed
// bridge lane: the wiki galgame family carries no playtime field, so EVERY
// work (claimed and bodyless alike) reads its catalog_work_playtime rows —
// the (facet,source) XOR's degenerate case (step-88 semantics: the bridge set
// is empty by construction, only the native lane exists).

import (
	"context"
)

// WorkPlaytimeRow is one source's playtime estimate on a work's read face.
// Minutes is unit-normalized to minutes on ingest (EG hours ×60; vndb
// c_length verbatim); the estimate semantics stay source-native (vndb =
// vote-backed median, EG = community median), so consumers render per source.
type WorkPlaytimeRow struct {
	SourceID  int16
	Minutes   int
	VoteCount int
}

// loadWorkPlaytimes reads the playtime rows for a set of works in ONE query,
// ordered so each work's estimates are source_id-ascending. A work with no
// estimate is absent from the map (the caller renders []).
func (s *ReadService) loadWorkPlaytimes(ctx context.Context, subjects []claimSubject) (map[int64][]WorkPlaytimeRow, error) {
	out := make(map[int64][]WorkPlaytimeRow, len(subjects))
	if len(subjects) == 0 {
		return out, nil
	}
	workIDs := make([]int64, 0, len(subjects))
	for _, sub := range subjects {
		workIDs = append(workIDs, sub.WorkID)
	}
	var rows []struct {
		WorkID    int64 `gorm:"column:work_id"`
		SourceID  int16 `gorm:"column:source_id"`
		Minutes   int   `gorm:"column:minutes"`
		VoteCount int   `gorm:"column:vote_count"`
	}
	if err := s.db.WithContext(ctx).Raw(`SELECT work_id, source_id, minutes, vote_count
		FROM catalog_work_playtime WHERE work_id IN ? ORDER BY work_id, source_id`, workIDs).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.WorkID] = append(out[r.WorkID], WorkPlaytimeRow{
			SourceID: r.SourceID, Minutes: r.Minutes, VoteCount: r.VoteCount,
		})
	}
	return out, nil
}
