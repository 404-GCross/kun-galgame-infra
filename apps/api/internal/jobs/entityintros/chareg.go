package entityintros

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

type egAnchor struct {
	EntityID   int64  `gorm:"column:entity_id"`
	ExternalID string `gorm:"column:external_id"`
	EGID       int64  `gorm:"column:eg_id"`
}

func loadEGAnchors(ctx context.Context, db *gorm.DB, reg registry) ([]egAnchor, error) {
	const query = `SELECT DISTINCT ON (c.id) c.id AS entity_id, r.external_id, r.external_id::bigint AS eg_id
		FROM catalog_character c
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = c.id
			AND r.source_id = ? AND r.link_kind = ?
		WHERE c.deleted_at IS NULL
		ORDER BY c.id, r.external_id::bigint`
	var out []egAnchor
	if err := db.WithContext(ctx).
		Raw(query, model.EntityTypeCharacter, reg.egSource, model.LinkKindExact).
		Scan(&out).Error; err != nil {
		return nil, fmt.Errorf("load char-eg anchors: %w", err)
	}
	return out, nil
}

func loadEGTexts(ctx context.Context, egDB *gorm.DB) (map[int64]string, error) {
	const query = `SELECT DISTINCT ON (character_id) character_id, raw->>'formal_explanation' AS text
		FROM appearances
		WHERE character_id IS NOT NULL
			AND btrim(coalesce(raw->>'formal_explanation', '')) <> ''
			AND coalesce(raw->>'netabare', '') <> 'true'
		ORDER BY character_id, char_length(raw->>'formal_explanation') DESC, game ASC NULLS LAST, pk`
	var rows []struct {
		CharacterID int64  `gorm:"column:character_id"`
		Text        string `gorm:"column:text"`
	}
	if err := egDB.WithContext(ctx).Raw(query).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load erogamespace appearance texts: %w", err)
	}
	out := make(map[int64]string, len(rows))
	for _, r := range rows {
		out[r.CharacterID] = r.Text
	}
	return out, nil
}

func loadEGCandidates(ctx context.Context, catalogDB, egDB *gorm.DB, reg registry, limit, offset int) ([]candidate, int, error) {
	anchors, err := loadEGAnchors(ctx, catalogDB, reg)
	if err != nil {
		return nil, 0, err
	}
	texts, err := loadEGTexts(ctx, egDB)
	if err != nil {
		return nil, 0, err
	}
	out := make([]candidate, 0, len(anchors))
	noSupply := 0
	for _, a := range anchors {
		text, ok := texts[a.EGID]
		if !ok {
			noSupply++
			continue
		}
		out = append(out, candidate{EntityID: a.EntityID, ExternalID: a.ExternalID, Text: text})
	}
	if offset > 0 {
		if offset >= len(out) {
			return nil, noSupply, nil
		}
		out = out[offset:]
	}
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out, noSupply, nil
}
