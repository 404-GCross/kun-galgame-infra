package entityintros

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// registry holds the catalog source ids this backfill needs, resolved BY KEY
// (never hardcoded) so a rehearsal / prod DB with different auto-increment
// seeds still works — the bgmsummaries / dlsitemedia discipline.
type registry struct {
	vndbSource    int16
	bangumiSource int16
}

func resolveRegistry(ctx context.Context, db *gorm.DB) (registry, error) {
	var r registry
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'vndb'`).Scan(&r.vndbSource).Error; err != nil {
		return r, fmt.Errorf("resolve vndb source: %w", err)
	}
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'bangumi'`).Scan(&r.bangumiSource).Error; err != nil {
		return r, fmt.Errorf("resolve bangumi source: %w", err)
	}
	if r.vndbSource == 0 || r.bangumiSource == 0 {
		return r, fmt.Errorf("registry not seeded (vndb source=%d, bangumi source=%d)", r.vndbSource, r.bangumiSource)
	}
	return r, nil
}

// candidate is one live entity joined to its anchored staging row. Text is
// verbatim from staging ('' when the source has none — counted as no_text,
// not filtered, so the run reports it honestly).
type candidate struct {
	EntityID   int64  `gorm:"column:entity_id"`
	ExternalID string `gorm:"column:external_id"`
	Text       string `gorm:"column:text"`
}

// loadCandidates resolves one lane's candidates: live entities carrying an
// EXACT anchor (link_kind=0) for the lane's source, joined to the staging text.
// Unlike bgmsummaries no matched_by pin is needed — every character/person
// anchor tier under exact link_kind is an import/attach rule (the vndb
// probable tier, rule:same-work-character-name kind=1, is excluded by the
// link_kind filter alone). DISTINCT ON keeps ONE anchor per entity (lowest
// external_id, numerically for bangumi's all-numeric ids and lexically for
// vndb's "c<N>" ids — deterministic either way).
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
	// Window in Go after DISTINCT ON so offset/limit apply to distinct entities
	// (the dlsitemedia chunking discipline — slicing keeps it obviously correct).
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

// preloadChunk keeps each preload IN-list under the wire protocol's 65,535
// parameter cap (the candidate sets here are ~150k characters — the first
// intro job big enough to hit it).
const preloadChunk = 10000

// preloadExistingLangs loads every (entity_id → set of intro langs) pair
// already present for the candidate entities — across ALL sources, because the
// fill-missing-language rule asks "does the entity have this language at
// all?", not "did this source already write it?". This preload is the primary
// skip; the (entity_id,lang,source_id) ON CONFLICT is only the backstop.
// table is catalog_character_intro or catalog_person_intro with its matching
// id column.
func preloadExistingLangs(ctx context.Context, db *gorm.DB, table, idCol string, ids []int64) (map[int64]map[string]bool, error) {
	out := map[int64]map[string]bool{}
	for start := 0; start < len(ids); start += preloadChunk {
		end := min(start+preloadChunk, len(ids))
		var rows []struct {
			EntityID int64  `gorm:"column:entity_id"`
			Lang     string `gorm:"column:lang"`
		}
		if err := db.WithContext(ctx).
			Raw(`SELECT `+idCol+` AS entity_id, lang FROM `+table+` WHERE `+idCol+` IN ?`, ids[start:end]).
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
