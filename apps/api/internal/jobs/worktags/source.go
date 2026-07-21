package worktags

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// registry holds the catalog registry ids this backfill needs, resolved by key
// (never hardcoded) so a rehearsal / prod DB with different auto-increment
// seeds still works — the bgmsummaries / workratings discipline.
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
		return r, fmt.Errorf("registry not seeded (galgame medium=%d, bangumi source=%d)",
			r.galgameMedium, r.bangumiSource)
	}
	return r, nil
}

// candidate is one bodyless galgame work joined to its EXACT Bangumi anchor's
// subject tags blob. Site is carried (not filtered out) so the write-time XOR
// guard can re-assert bodylessness.
type candidate struct {
	WorkID    int64   `gorm:"column:work_id"`
	SubjectID int64   `gorm:"column:subject_id"`
	Site      *string `gorm:"column:site"`
	Tags      []byte  `gorm:"column:tags"`
}

// loadCandidates resolves bodyless galgame works carrying an EXACT step-56a
// Bangumi anchor, joined to the anchored subject's tags jsonb — the exact
// workratings bgm-lane join shape (src_bangumi is a schema inside the catalog
// DB, single DSN). DISTINCT ON keeps ONE anchor per work (the lowest
// external_id); the external_id→bigint cast is safe under the matched_by
// filter (56a writes numeric subject ids only). Limit/Offset window the
// distinct-work list in Go (the dlsitemedia chunking discipline).
func loadCandidates(ctx context.Context, db *gorm.DB, reg registry, limit, offset int) ([]candidate, error) {
	var out []candidate
	if err := db.WithContext(ctx).
		Raw(`SELECT DISTINCT ON (w.id) w.id AS work_id, r.external_id::bigint AS subject_id,
				w.site AS site, sub.tags AS tags
			FROM catalog_work w
			JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = w.id
				AND r.source_id = ? AND r.link_kind = ? AND r.matched_by = ?
			JOIN src_bangumi.subject sub ON sub.id = r.external_id::bigint
			WHERE w.medium_id = ? AND (w.site IS NULL OR w.site = '') AND w.deleted_at IS NULL
			ORDER BY w.id, r.external_id`,
			model.EntityTypeWork, reg.bangumiSource, model.LinkKindExact, ruleTitleYear, reg.galgameMedium).
		Scan(&out).Error; err != nil {
		return nil, err
	}
	return window(out, limit, offset), nil
}

// subjectTag is one decided tag of a subject after defensive parsing: trimmed
// name, highest count when the subject repeated the name.
type subjectTag struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// parseSubjectTags defensively decodes a subject's tags jsonb into the decided
// tag list, bumping the stats counters (see the package doc for the branch
// taxonomy). Returns nil when there is nothing to write (NULL/empty/malformed
// — the caller just moves on). The decided list is ordered (count DESC, name)
// for deterministic writes and samples.
func parseSubjectTags(raw []byte, st *Stats) []subjectTag {
	if len(raw) == 0 || string(raw) == "null" { // column NULL → no folksonomy
		st.NoTags++
		return nil
	}
	var els []subjectTag
	if err := json.Unmarshal(raw, &els); err != nil {
		// Not an array of {name,count} objects — a malformed dump row. Counted,
		// never fatal.
		st.NotArray++
		return nil
	}
	if len(els) == 0 {
		st.NoTags++
		return nil
	}
	// Trim + de-duplicate within the subject, keeping the highest count.
	best := map[string]int{}
	for _, el := range els {
		name := strings.TrimSpace(el.Name)
		if name == "" { // missing name key or whitespace-only name
			st.NameBlank++
			continue
		}
		if prev, seen := best[name]; seen {
			st.DupCollapsed++
			if el.Count > prev {
				best[name] = el.Count
			}
			continue
		}
		best[name] = el.Count
	}
	if len(best) == 0 {
		return nil // every element was blank — already counted per element
	}
	out := make([]subjectTag, 0, len(best))
	for name, count := range best {
		out = append(out, subjectTag{Name: name, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// window applies the offset/limit chunking to an already-distinct candidate
// list (slicing keeps it obviously correct — the bgmsummaries discipline).
func window[T any](in []T, limit, offset int) []T {
	if offset > 0 {
		if offset >= len(in) {
			return nil
		}
		in = in[offset:]
	}
	if limit > 0 && limit < len(in) {
		in = in[:limit]
	}
	return in
}
