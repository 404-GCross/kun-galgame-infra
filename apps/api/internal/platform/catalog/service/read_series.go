package service

import (
	"context"
)

type WorkSeriesRow struct {
	ID          int64  `gorm:"column:id"`
	Name        string `gorm:"column:name"`
	SourceID    int16  `gorm:"column:source_id"`
	MemberCount int    `gorm:"column:member_count"`
}

func (s *ReadService) loadWorkSeries(ctx context.Context, subjects []claimSubject) (map[int64][]WorkSeriesRow, error) {
	out := make(map[int64][]WorkSeriesRow, len(subjects))
	if len(subjects) == 0 {
		return out, nil
	}
	workIDs := make([]int64, 0, len(subjects))
	for _, sub := range subjects {
		workIDs = append(workIDs, sub.WorkID)
	}
	var rows []struct {
		WorkID      int64  `gorm:"column:work_id"`
		ID          int64  `gorm:"column:id"`
		Name        string `gorm:"column:name"`
		SourceID    int16  `gorm:"column:source_id"`
		MemberCount int    `gorm:"column:member_count"`
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT m.work_id, se.id, se.display_name AS name, se.source_id,
		       (SELECT count(*) FROM catalog_series_member mm WHERE mm.series_id = se.id) AS member_count
		FROM catalog_series_member m
		JOIN catalog_series se ON se.id = m.series_id
		WHERE m.work_id IN ?
		ORDER BY m.work_id, se.id`, workIDs).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.WorkID] = append(out[r.WorkID], WorkSeriesRow{
			ID: r.ID, Name: r.Name, SourceID: r.SourceID, MemberCount: r.MemberCount,
		})
	}
	return out, nil
}
