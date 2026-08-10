package service

import (
	"context"
)

type WorkPopularityRow struct {
	SourceID int16
	Metric   int16
	Value    int64
}

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
