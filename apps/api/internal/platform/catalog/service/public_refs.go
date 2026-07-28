// public_refs.go — the generic per-entity refs[] loader for the public face
// (doc 106 G4: refs are isomorphic across every entity). EXACT tier only —
// probable is a review-lane hypothesis and related is a non-identity link;
// neither ever crosses the public face (硬红线). FROZEN after W1.
package service

import (
	"context"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"
)

// entityRefsFor batch-loads the EXACT identity anchors for a set of entities
// of one type, keyed by entity id, projected to the public wire shape
// (source = public key spelling; deterministic (source_id, external_id)
// order). Missing entities simply have no map entry; callers render [].
func (s *PublicService) entityRefsFor(ctx context.Context, entityType int16, ids []int64) (map[int64][]dto.PublicCatalogRef, error) {
	if len(ids) == 0 {
		return map[int64][]dto.PublicCatalogRef{}, nil
	}
	var rows []struct {
		EntityID   int64  `gorm:"column:entity_id"`
		Source     string `gorm:"column:source"`
		ExternalID string `gorm:"column:external_id"`
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT r.entity_id, src.key AS source, r.external_id
		FROM catalog_external_ref r JOIN catalog_source src ON src.id = r.source_id
		WHERE r.entity_type = ? AND r.entity_id IN ? AND r.link_kind = ?
		ORDER BY r.entity_id, r.source_id, r.external_id`,
		entityType, ids, model.LinkKindExact).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64][]dto.PublicCatalogRef, len(ids))
	for _, r := range rows {
		out[r.EntityID] = append(out[r.EntityID], dto.PublicCatalogRef{
			Source: publicSourceKey(r.Source), ExternalID: r.ExternalID,
		})
	}
	return out, nil
}

// entityRefs is the single-entity convenience over entityRefsFor; always
// returns a non-nil slice (the public face serializes [] never null).
func (s *PublicService) entityRefs(ctx context.Context, entityType int16, id int64) ([]dto.PublicCatalogRef, error) {
	m, err := s.entityRefsFor(ctx, entityType, []int64{id})
	if err != nil {
		return nil, err
	}
	if refs := m[id]; refs != nil {
		return refs, nil
	}
	return []dto.PublicCatalogRef{}, nil
}
