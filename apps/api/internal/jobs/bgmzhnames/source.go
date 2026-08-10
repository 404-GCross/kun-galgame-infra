package bgmzhnames

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

const preloadChunk = 10000

func resolveBangumiSource(ctx context.Context, db *gorm.DB) (int16, error) {
	var id int16
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'bangumi'`).Scan(&id).Error; err != nil {
		return 0, fmt.Errorf("resolve bangumi source: %w", err)
	}
	if id == 0 {
		return 0, fmt.Errorf("registry not seeded (bangumi source missing)")
	}
	return id, nil
}

func characterLane() laneSpec {
	return laneSpec{
		load: loadAnchoredCharacters,
		preload: func(ctx context.Context, db *gorm.DB, ids []int64) (map[int64]*zhAliasState, error) {
			return preloadZhAliasesBy(ctx, db, "catalog_character_alias", "character_id", ids)
		},
		hostWorks: preloadHostWorks,
		insert:    insertCharacterAlias,
	}
}

func loadAnchoredCharacters(ctx context.Context, db *gorm.DB, sourceID int16, limit, offset int) ([]anchoredEntity, error) {
	const query = `SELECT DISTINCT ON (c.id) c.id AS entity_id, c.id AS owner_id,
			'' AS owner_name, r.external_id, sb.infobox_parsed
		FROM catalog_character c
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = c.id
			AND r.source_id = ? AND r.link_kind = ?
		JOIN src_bangumi.character sb ON sb.id = r.external_id::bigint
		WHERE c.deleted_at IS NULL
		ORDER BY c.id, r.external_id::bigint`
	var out []anchoredEntity
	if err := db.WithContext(ctx).
		Raw(query, model.EntityTypeCharacter, sourceID, model.LinkKindExact).Scan(&out).Error; err != nil {
		return nil, fmt.Errorf("load anchored characters: %w", err)
	}
	return window(out, limit, offset), nil
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

func window[T any](out []T, limit, offset int) []T {
	if offset > 0 {
		if offset >= len(out) {
			return nil
		}
		out = out[offset:]
	}
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out
}
