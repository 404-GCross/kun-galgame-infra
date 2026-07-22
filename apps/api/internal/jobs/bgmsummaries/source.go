package bgmsummaries

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// registry holds the two catalog registry ids this enrichment needs, resolved
// by key (never hardcoded) so a rehearsal / prod DB with different
// auto-increment seeds still works. In practice both are stable (galgame
// medium=1, bangumi source=3) but resolving keeps the tool honest — the same
// discipline internal/jobs/dlsitemedia and doujinbangumi follow.
type registry struct {
	galgameMedium int16
	bangumiSource int16
}

func resolveRegistry(ctx context.Context, db *gorm.DB) (registry, error) {
	var r registry
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_medium WHERE key = 'galgame'`).Scan(&r.galgameMedium).Error; err != nil {
		return r, fmt.Errorf("resolve galgame medium: %w", err)
	}
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'bangumi'`).Scan(&r.bangumiSource).Error; err != nil {
		return r, fmt.Errorf("resolve bangumi source: %w", err)
	}
	if r.galgameMedium == 0 || r.bangumiSource == 0 {
		return r, fmt.Errorf("registry not seeded (galgame medium=%d, bangumi source=%d)", r.galgameMedium, r.bangumiSource)
	}
	return r, nil
}

// candidate is one bodyless galgame work joined to its EXACT Bangumi anchor's
// subject. Summary is verbatim from the dump ('' when the subject has none —
// counted, not filtered, so the run reports skip_no_summary). Site is carried
// (not filtered out) so the write-time XOR guard can re-assert bodylessness
// even though loadCandidates already selects only bodyless rows.
type candidate struct {
	WorkID    int64   `gorm:"column:work_id"`
	SubjectID int64   `gorm:"column:subject_id"`
	Site      *string `gorm:"column:site"`
	Summary   string  `gorm:"column:summary"`
}

// loadCandidates resolves bodyless galgame works carrying an EXACT Bangumi work
// anchor — matched_by UNRESTRICTED (every exact tier asserts identity, the
// 66/69/71 ruling; the bgmworkmeta precedent) — joined to the anchored
// subject's summary:
//
//	catalog_work(bodyless galgame)
//	  → catalog_external_ref(entity_type=work, source=bangumi, link_kind=exact)
//	  → src_bangumi.subject (same DB — src_bangumi is a schema, single DSN)
//
// Probable anchors (link_kind=probable) never appear (the exact gate excludes
// them). The external_id→bigint cast is safe (surveyed: zero non-numeric exact
// bgm work anchors — the 69 verification). DISTINCT ON keeps ONE anchor per
// work (the lowest external_id), same as dlsitemedia.
func loadCandidates(ctx context.Context, db *gorm.DB, reg registry, limit, offset int) ([]candidate, error) {
	q := db.WithContext(ctx).
		Raw(`SELECT DISTINCT ON (w.id) w.id AS work_id, r.external_id::bigint AS subject_id,
				w.site AS site, sub.summary AS summary
			FROM catalog_work w
			JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = w.id
				AND r.source_id = ? AND r.link_kind = ?
			JOIN src_bangumi.subject sub ON sub.id = r.external_id::bigint
			WHERE w.medium_id = ? AND (w.site IS NULL OR w.site = '') AND w.deleted_at IS NULL
			ORDER BY w.id, r.external_id`,
			model.EntityTypeWork, reg.bangumiSource, model.LinkKindExact, reg.galgameMedium)
	var out []candidate
	if err := q.Scan(&out).Error; err != nil {
		return nil, err
	}
	// Window in Go after DISTINCT ON so offset/limit apply to distinct works
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

// preloadExistingLangs loads every (work_id → set of intro langs) pair already
// present for the candidate works — across ALL sources, because the
// fill-missing-language rule asks "does the work have this language at all?",
// not "did bangumi already write it?". This preload is the primary skip; the
// (work_id,lang,source_id) ON CONFLICT is only the backstop.
func preloadExistingLangs(ctx context.Context, db *gorm.DB, workIDs []int64) (map[int64]map[string]bool, error) {
	out := map[int64]map[string]bool{}
	if len(workIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		WorkID int64  `gorm:"column:work_id"`
		Lang   string `gorm:"column:lang"`
	}
	if err := db.WithContext(ctx).
		Raw(`SELECT work_id, lang FROM catalog_work_intro WHERE work_id IN ?`, workIDs).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		set := out[r.WorkID]
		if set == nil {
			set = map[string]bool{}
			out[r.WorkID] = set
		}
		set[r.Lang] = true
	}
	return out, nil
}
