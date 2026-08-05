package service

// Label attribution read face, batched (A2-1a). The per-work query already
// lives inline in loadWorkDetail (one work at a time); the works LIST needs the
// same rows for a whole page, so this is that query with an IN list and a
// work_id lead in the ORDER BY. Same projection, same ordering within a work
// (attribution kind, then label display name) — one 口径, two grains.
//
// Both grains gate on l.deleted_at IS NULL. The attribution edge is NOT the
// authority on whether a label still exists: an edge survives its label being
// merged away or soft-deleted (writers repoint lazily, some not at all), and
// projecting it anyway renders the merged-away twin beside the survivor — two
// identically named companies on one work page.

import "context"

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
		Lang        string `gorm:"column:lang"`
		LogoHash    string `gorm:"column:logo_hash"`
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT wl.work_id, wl.label_id, l.display_name, l.kind AS label_kind, wl.kind AS kind, l.lang, l.logo_hash
		FROM catalog_work_label wl JOIN catalog_label l ON l.id = wl.label_id AND l.deleted_at IS NULL
		WHERE wl.work_id IN ?
		ORDER BY wl.work_id, wl.kind, l.display_name`, workIDs).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.WorkID] = append(out[r.WorkID], LabelAttribution{
			LabelID: r.LabelID, DisplayName: r.DisplayName, LabelKind: r.LabelKind, Kind: r.Kind,
			Lang: r.Lang, LogoHash: r.LogoHash,
		})
	}
	return out, nil
}

// LabelLogoHashes reads catalog_label.logo_hash for a set of label ids in ONE
// query (wave 170). Labels without a logo — and ids that do not resolve to a
// live label — are absent from the map, so a caller that ranges over its own
// ids reads "" for them.
//
// This exists because the entity-search hits come from Meilisearch, whose label
// documents carry no logo hash: hydrating the handful of hits from Postgres by
// id keeps the index (settings AND documents) out of the wave entirely.
func (s *ReadService) LabelLogoHashes(ctx context.Context, labelIDs []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(labelIDs))
	if len(labelIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ID       int64  `gorm:"column:id"`
		LogoHash string `gorm:"column:logo_hash"`
	}
	// Raw SQL bypasses GORM soft-delete, so deleted_at is filtered explicitly.
	if err := s.db.WithContext(ctx).Raw(`
		SELECT id, logo_hash FROM catalog_label
		WHERE id IN ? AND deleted_at IS NULL AND logo_hash <> ''`, labelIDs).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ID] = r.LogoHash
	}
	return out, nil
}
