package entityintros

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// The char-eg lane (refs/proj/120) differs from the other three in one physical
// way: erogamespace is a SEPARATE DATABASE, not a schema inside the catalog DB,
// so the anchor→text join cannot be a SQL join. Both sides are small (25,586
// anchors, 30,606 appearance rows), so each is loaded whole and joined in Go —
// the same shape the roster importer uses for the very same table.

// egAnchor is one live character pinned to an erogamespace character id.
// ExternalID stays the raw text (it is what the samples/logs report), EGID is
// the numeric form the appearance rows are keyed on.
type egAnchor struct {
	EntityID   int64  `gorm:"column:entity_id"`
	ExternalID string `gorm:"column:external_id"`
	EGID       int64  `gorm:"column:eg_id"`
}

// loadEGAnchors returns one anchor per live character (lowest external id,
// numerically — EG character ids are all-numeric), mirroring the DISTINCT ON
// discipline of the in-DB lanes.
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

// loadEGTexts picks ONE appearance text per erogamespace character.
//
// An EG character id is global (not per-game), so a character reappearing in a
// series carries several formal_explanation texts — and the pk is
// game/character/stage, so even one game can contribute several rows. The
// ruling (120 §4) is: among the appearances with a non-empty, non-spoiler
// formal_explanation take the LONGEST text, ties broken by the smallest game
// id; pk closes the last theoretical tie so the pick is fully deterministic.
//
// netabare = true marks a spoiler write-up. Exactly one such row exists today
// (and its text is empty anyway), but the exclusion is written into the query
// rather than left to the data: the mirror re-syncs, and this table has no
// spoiler-aware read face to hide behind (catalog_character_intro carries no
// spoiler flag — the same reasoning that makes the vndb lane drop spoiler spans
// outright). Compared as text so a re-typed field cannot raise a cast error.
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

// loadEGCandidates joins the anchors to their chosen text and reports how many
// anchored characters EG has nothing usable for.
//
// noSupply is counted over the WHOLE anchor set, not the window: it is a
// coverage fact about the source, and making it depend on --limit/--offset
// would turn a chunked run's summary into noise. The window applies to the
// candidates (the supplied set), exactly as the in-DB lanes window their
// post-join set, so chunks stay evenly sized.
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

// preloadHostWorks maps each candidate character to the live works that list it
// on their roster. Those works are what a new character intro changes for a
// reader, so they are the set the lane bumps through the public changes feed
// (repository.TouchWorks, wired in step 117). Soft-deleted works are dropped
// here rather than at touch time — a deleted work has no read face to refresh.
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
