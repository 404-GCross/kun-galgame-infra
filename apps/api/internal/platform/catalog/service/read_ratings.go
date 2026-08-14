package service

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"

	"api/internal/platform/catalog/dto"
)

type WorkRatingRow struct {
	SourceID     int16
	Score        float64
	VoteCount    int
	Rank         *int
	Distribution []dto.RatingBucket
	Stats        *dto.RatingStats
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
		WorkID       int64   `gorm:"column:work_id"`
		SourceID     int16   `gorm:"column:source_id"`
		Score        float64 `gorm:"column:score"`
		VoteCount    int     `gorm:"column:vote_count"`
		Rank         *int    `gorm:"column:rank"`
		Distribution []byte  `gorm:"column:distribution"`
		Stats        []byte  `gorm:"column:stats"`
	}
	if err := db.Raw(`SELECT work_id, source_id, score, vote_count, rank, distribution, stats
		FROM catalog_work_rating
		WHERE work_id IN ? ORDER BY work_id, source_id`, workIDs).Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		out[r.WorkID] = append(out[r.WorkID], WorkRatingRow{
			SourceID: r.SourceID, Score: r.Score, VoteCount: r.VoteCount, Rank: r.Rank,
			Distribution: decodeRatingBuckets(r.Distribution), Stats: decodeRatingStats(r.Stats),
		})
	}
	return nil
}

// Stored sparsely as {"<score>": count}; served as an array sorted by score so
// no consumer has to sort the object's keys — "10" sorts before "9" as text,
// which would put the top bucket of a 1-10 scale in the middle of the chart.
func decodeRatingBuckets(raw []byte) []dto.RatingBucket {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]int
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	out := make([]dto.RatingBucket, 0, len(m))
	for k, n := range m {
		score, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		out = append(out, dto.RatingBucket{Score: score, Count: n})
	}
	slices.SortFunc(out, func(a, b dto.RatingBucket) int { return a.Score - b.Score })
	if len(out) == 0 {
		return nil
	}
	return out
}

func decodeRatingStats(raw []byte) *dto.RatingStats {
	if len(raw) == 0 {
		return nil
	}
	var st dto.RatingStats
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil
	}
	if st.Average == nil && st.Stdev == nil && st.Min == nil && st.Max == nil {
		return nil
	}
	return &st
}
