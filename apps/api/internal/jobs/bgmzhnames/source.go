package bgmzhnames

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// preloadChunk keeps each preload IN-list under the wire protocol's 65,535
// parameter cap (the anchored set is ~74k characters).
const preloadChunk = 10000

// resolveBangumiSource looks the source up BY KEY (never hardcoded), so a
// rehearsal / prod DB with a different auto-increment seed still works — the
// charattrs / entityintros discipline.
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

// anchoredChar is one live character joined to its Bangumi staging row.
type anchoredChar struct {
	EntityID   int64          `gorm:"column:entity_id"`
	ExternalID string         `gorm:"column:external_id"`
	Infobox    datatypes.JSON `gorm:"column:infobox_parsed"`
}

// loadAnchored resolves every live character carrying an EXACT bangumi anchor,
// with its parsed infobox. DISTINCT ON keeps ONE anchor per character (lowest
// external id, numerically — bangumi ids are all-numeric), so a character with
// two anchors is projected once and deterministically.
//
// The infobox guard deliberately does NOT live in this query: loading the dirty
// rows too is what lets the run report how many it refused (skipped_guard)
// rather than silently narrowing its own universe.
func loadAnchored(ctx context.Context, db *gorm.DB, sourceID int16, limit, offset int) ([]anchoredChar, error) {
	const query = `SELECT DISTINCT ON (c.id) c.id AS entity_id, r.external_id, sb.infobox_parsed
		FROM catalog_character c
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = c.id
			AND r.source_id = ? AND r.link_kind = ?
		JOIN src_bangumi.character sb ON sb.id = r.external_id::bigint
		WHERE c.deleted_at IS NULL
		ORDER BY c.id, r.external_id::bigint`
	var out []anchoredChar
	if err := db.WithContext(ctx).
		Raw(query, model.EntityTypeCharacter, sourceID, model.LinkKindExact).Scan(&out).Error; err != nil {
		return nil, fmt.Errorf("load anchored characters: %w", err)
	}
	return window(out, limit, offset), nil
}

// zhAliasState is what a character already carries in zh-Hans: the exact names
// (the uniqueness key's third component is the language, so only zh-Hans rows
// can collide) and whether one of them is already the locale primary.
type zhAliasState struct {
	names      map[string]bool
	hasPrimary bool
}

// preloadZhAliases loads that state for the candidate characters. It is the
// PRIMARY skip — the ON CONFLICT clause on the write is only the backstop —
// and the source of the "never steal an existing primary" judgement.
func preloadZhAliases(ctx context.Context, db *gorm.DB, ids []int64) (map[int64]*zhAliasState, error) {
	out := make(map[int64]*zhAliasState, len(ids))
	for start := 0; start < len(ids); start += preloadChunk {
		end := min(start+preloadChunk, len(ids))
		var rows []struct {
			CharacterID int64  `gorm:"column:character_id"`
			Name        string `gorm:"column:name"`
			IsPrimary   bool   `gorm:"column:is_primary_for_locale"`
		}
		if err := db.WithContext(ctx).Raw(`SELECT character_id, name, is_primary_for_locale
			FROM catalog_character_alias WHERE lang = ? AND character_id IN ?`,
			LangZhHans, ids[start:end]).Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("preload zh-Hans aliases: %w", err)
		}
		for _, r := range rows {
			st := out[r.CharacterID]
			if st == nil {
				st = &zhAliasState{names: map[string]bool{}}
				out[r.CharacterID] = st
			}
			st.names[r.Name] = true
			st.hasPrimary = st.hasPrimary || r.IsPrimary
		}
	}
	return out, nil
}

// preloadHostWorks maps each candidate character to the live works whose roster
// lists it. Those works render the character's names, so they are the set a
// real write bumps through the public changes feed (repository.TouchWorks, the
// step-117/120 discipline). Soft-deleted works are dropped here: a deleted work
// has no read face to refresh.
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

// window applies offset/limit in Go after DISTINCT ON, so they slice distinct
// characters (the dlsitemedia / entityintros chunking discipline).
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
