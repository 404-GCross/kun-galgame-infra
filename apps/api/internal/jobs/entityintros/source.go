package entityintros

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

type registry struct {
	vndbSource    int16
	bangumiSource int16
	egSource      int16
}

func resolveRegistry(ctx context.Context, db *gorm.DB) (registry, error) {
	var r registry
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'vndb'`).Scan(&r.vndbSource).Error; err != nil {
		return r, fmt.Errorf("resolve vndb source: %w", err)
	}
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'bangumi'`).Scan(&r.bangumiSource).Error; err != nil {
		return r, fmt.Errorf("resolve bangumi source: %w", err)
	}
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'erogamescape'`).Scan(&r.egSource).Error; err != nil {
		return r, fmt.Errorf("resolve erogamescape source: %w", err)
	}
	if r.vndbSource == 0 || r.bangumiSource == 0 || r.egSource == 0 {
		return r, fmt.Errorf("registry not seeded (vndb source=%d, bangumi source=%d, erogamescape source=%d)",
			r.vndbSource, r.bangumiSource, r.egSource)
	}
	return r, nil
}

type candidate struct {
	EntityID   int64  `gorm:"column:entity_id"`
	ExternalID string `gorm:"column:external_id"`
	Text       string `gorm:"column:text"`
}

func loadCandidates(ctx context.Context, db *gorm.DB, lane string, reg registry, limit, offset int) ([]candidate, error) {
	var query string
	var args []any
	switch lane {
	case LaneCharBangumi:
		query = `SELECT DISTINCT ON (c.id) c.id AS entity_id, r.external_id, sb.summary AS text
			FROM catalog_character c
			JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = c.id
				AND r.source_id = ? AND r.link_kind = ?
			JOIN src_bangumi.character sb ON sb.id = r.external_id::bigint
			WHERE c.deleted_at IS NULL
			ORDER BY c.id, r.external_id::bigint`
		args = []any{model.EntityTypeCharacter, reg.bangumiSource, model.LinkKindExact}
	case LaneCharVNDB:
		query = `SELECT DISTINCT ON (c.id) c.id AS entity_id, r.external_id, sv.description AS text
			FROM catalog_character c
			JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = c.id
				AND r.source_id = ? AND r.link_kind = ?
			JOIN src_vndb.chars sv ON sv.id = r.external_id
			WHERE c.deleted_at IS NULL
			ORDER BY c.id, r.external_id`
		args = []any{model.EntityTypeCharacter, reg.vndbSource, model.LinkKindExact}
	case LanePersonBangumi:
		query = `SELECT DISTINCT ON (p.id) p.id AS entity_id, r.external_id, sp.summary AS text
			FROM catalog_person p
			JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = p.id
				AND r.source_id = ? AND r.link_kind = ?
			JOIN src_bangumi.person sp ON sp.id = r.external_id::bigint
			WHERE p.deleted_at IS NULL
			ORDER BY p.id, r.external_id::bigint`
		args = []any{model.EntityTypePerson, reg.bangumiSource, model.LinkKindExact}
	default:
		return nil, fmt.Errorf("unknown lane %q", lane)
	}
	var out []candidate
	if err := db.WithContext(ctx).Raw(query, args...).Scan(&out).Error; err != nil {
		return nil, fmt.Errorf("load %s candidates: %w", lane, err)
	}
	if offset > 0 {
		if offset >= len(out) {
			return nil, nil
		}
		out = out[offset:]
	}
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}

const preloadChunk = 10000

func preloadExistingLangs(ctx context.Context, db *gorm.DB, table, idCol string, ids []int64) (map[int64]map[string]bool, error) {
	out := map[int64]map[string]bool{}
	for start := 0; start < len(ids); start += preloadChunk {
		end := min(start+preloadChunk, len(ids))
		var rows []struct {
			EntityID int64  `gorm:"column:entity_id"`
			Lang     string `gorm:"column:lang"`
		}
		if err := db.WithContext(ctx).
			Raw(`SELECT `+idCol+` AS entity_id, lang FROM `+table+` WHERE provenance = 0 AND `+idCol+` IN ?`, ids[start:end]).
			Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, r := range rows {
			set := out[r.EntityID]
			if set == nil {
				set = map[string]bool{}
				out[r.EntityID] = set
			}
			set[r.Lang] = true
		}
	}
	return out, nil
}

func preloadHostWorks(ctx context.Context, db *gorm.DB, ids []int64) (map[int64][]int64, error) {
	out := map[int64][]int64{}
	for start := 0; start < len(ids); start += preloadChunk {
		end := min(start+preloadChunk, len(ids))
		var rows []struct {
			CharacterID int64 `gorm:"column:character_id"`
			WorkID      int64 `gorm:"column:work_id"`
		}
		if err := db.WithContext(ctx).Raw(`SELECT wc.character_id, wc.work_id
			FROM catalog_work_character wc
			JOIN catalog_work w ON w.id = wc.work_id AND w.deleted_at IS NULL
			WHERE wc.character_id IN ?`, ids[start:end]).Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("preload character host works: %w", err)
		}
		for _, r := range rows {
			out[r.CharacterID] = append(out[r.CharacterID], r.WorkID)
		}
	}
	return out, nil
}
