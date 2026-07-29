package service

// Label attribution read face, batched (A2-1a). The per-work query already
// lives inline in loadWorkDetail (one work at a time); the works LIST needs the
// same rows for a whole page, so this is that query with an IN list and a
// work_id lead in the ORDER BY. Same projection, same ordering within a work
// (attribution kind, then label display name) — one 口径, two grains.

import (
	"context"

	"api/internal/platform/catalog/model"
)

// loadWorkLabels reads the label attributions for a set of works in ONE query.
// A work with no attribution is absent from the map (the caller renders []).
func (s *ReadService) loadWorkLabels(ctx context.Context, workIDs []int64) (map[int64][]LabelAttribution, error) {
	out := make(map[int64][]LabelAttribution, len(workIDs))
	if len(workIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		WorkID      int64  `gorm:"column:work_id"`
		LabelID     int64  `gorm:"column:label_id"`
		DisplayName string `gorm:"column:display_name"`
		LabelKind   int16  `gorm:"column:label_kind"`
		Kind        int16  `gorm:"column:kind"`
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT wl.work_id, wl.label_id, l.display_name, l.kind AS label_kind, wl.kind AS kind
		FROM catalog_work_label wl JOIN catalog_label l ON l.id = wl.label_id
		WHERE wl.work_id IN ?
		ORDER BY wl.work_id, wl.kind, l.display_name`, workIDs).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.WorkID] = append(out[r.WorkID], LabelAttribution{
			LabelID: r.LabelID, DisplayName: r.DisplayName, LabelKind: r.LabelKind, Kind: r.Kind,
		})
	}
	return out, nil
}

// loadWorkTitles reads the display titles for a set of works in ONE query,
// search_hint rows excluded at the query level (they are findability-only and
// never leave the internal face). Ordered (work, kind, id): kind ASC is the
// detail face's own ordering, and the id tie-break makes "the first row for a
// language" a stable choice for the LIST face's per-key pivot.
func (s *ReadService) loadWorkTitles(ctx context.Context, workIDs []int64) (map[int64][]WorkTitleRow, error) {
	out := make(map[int64][]WorkTitleRow, len(workIDs))
	if len(workIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		WorkID int64  `gorm:"column:work_id"`
		Lang   string `gorm:"column:lang"`
		Title  string `gorm:"column:title"`
		Kind   int16  `gorm:"column:kind"`
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT work_id, lang, title, kind FROM catalog_work_title
		WHERE work_id IN ? AND kind <> ?
		ORDER BY work_id, kind, id`, workIDs, model.WorkTitleKindSearchHint).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.WorkID] = append(out[r.WorkID], WorkTitleRow{Lang: r.Lang, Title: r.Title, Kind: r.Kind})
	}
	return out, nil
}

// WorkTitleRow is one display title on a work's read face (search_hint rows
// never appear here).
type WorkTitleRow struct {
	Lang  string
	Title string
	Kind  int16
}
