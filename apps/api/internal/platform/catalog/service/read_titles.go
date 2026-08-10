package service

import (
	"context"

	"api/internal/platform/catalog/model"
)

type WorkTitleRow struct {
	Lang  string
	Title string
	Latin string
	Kind  int16
}

func (s *ReadService) loadWorkTitles(ctx context.Context, subjects []claimSubject) (map[int64][]WorkTitleRow, error) {
	return s.loadTitles(ctx, subjects, false)
}

func (s *ReadService) loadWorkDetailTitles(ctx context.Context, subjects []claimSubject) (map[int64][]WorkTitleRow, error) {
	return s.loadTitles(ctx, subjects, true)
}

func (s *ReadService) loadTitles(ctx context.Context, subjects []claimSubject, withHints bool) (map[int64][]WorkTitleRow, error) {
	out := make(map[int64][]WorkTitleRow, len(subjects))
	if len(subjects) == 0 {
		return out, nil
	}
	workIDs := make([]int64, 0, len(subjects))
	for _, sub := range subjects {
		workIDs = append(workIDs, sub.WorkID)
	}
	return out, s.nativeWorkTitles(ctx, workIDs, out, withHints)
}

func (s *ReadService) nativeWorkTitles(ctx context.Context, workIDs []int64, out map[int64][]WorkTitleRow, withHints bool) error {
	q := `SELECT work_id, lang, title, coalesce(latin, '') AS latin, kind FROM catalog_work_title
		WHERE work_id IN ? AND kind <> ? ORDER BY work_id, kind, id`
	args := []any{workIDs, model.WorkTitleKindSearchHint}
	if withHints {
		q = `SELECT work_id, lang, title, coalesce(latin, '') AS latin, kind FROM catalog_work_title
			WHERE work_id IN ? ORDER BY work_id, kind, lang, id`
		args = []any{workIDs}
	}
	var rows []struct {
		WorkID int64  `gorm:"column:work_id"`
		Lang   string `gorm:"column:lang"`
		Title  string `gorm:"column:title"`
		Latin  string `gorm:"column:latin"`
		Kind   int16  `gorm:"column:kind"`
	}
	if err := s.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		out[r.WorkID] = append(out[r.WorkID], WorkTitleRow{
			Lang: r.Lang, Title: r.Title, Latin: r.Latin, Kind: r.Kind,
		})
	}
	return nil
}
