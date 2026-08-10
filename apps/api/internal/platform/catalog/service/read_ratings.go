package service

import (
	"context"
)

type WorkRatingRow struct {
	SourceID  int16
	Score     float64
	VoteCount int
	Rank      *int
}

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
