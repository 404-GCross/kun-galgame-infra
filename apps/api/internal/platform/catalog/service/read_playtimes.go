package service

import (
	"context"
)

type WorkPlaytimeRow struct {
	SourceID  int16
	Minutes   int
	VoteCount int
}

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
