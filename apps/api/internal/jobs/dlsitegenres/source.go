package dlsitegenres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// registry holds the catalog registry ids this backfill needs, resolved by key
// (never hardcoded) so a rehearsal / prod DB with different auto-increment
// seeds still works — the worktags / workratings discipline.
type registry struct {
	galgameMedium int16
	dlsiteSource  int16
}

func resolveRegistry(ctx context.Context, db *gorm.DB) (registry, error) {
	var r registry
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_medium WHERE key = 'galgame'`).Scan(&r.galgameMedium).Error; err != nil {
		return r, fmt.Errorf("resolve galgame medium: %w", err)
	}
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'dlsite'`).Scan(&r.dlsiteSource).Error; err != nil {
		return r, fmt.Errorf("resolve dlsite source: %w", err)
	}
	if r.galgameMedium == 0 || r.dlsiteSource == 0 {
		return r, fmt.Errorf("registry not seeded (galgame medium=%d, dlsite source=%d)",
			r.galgameMedium, r.dlsiteSource)
	}
	return r, nil
}

// candidate is one galgame work with its representative DLsite workno. DISTINCT
// ON keeps ONE anchor per work (the lexicographically lowest workno — the
// workratings dlsite-lane precedent; surveyed there: zero galgame-medium works
// carry more than one distinct workno).
type candidate struct {
	WorkID int64  `gorm:"column:work_id"`
	Workno string `gorm:"column:workno"`
}

// loadCandidates resolves GALGAME-medium works carrying a DLsite EXACT release
// anchor — the workratings dlsite-lane candidate query verbatim (worknos anchor at
// RELEASE level, SKU-natured; medium pinned to galgame by the game-domain ruling).
// Limit/Offset window the distinct-work list in Go (the dlsitemedia chunking
// discipline).
func loadCandidates(ctx context.Context, db *gorm.DB, reg registry, limit, offset int) ([]candidate, error) {
	var out []candidate
	if err := db.WithContext(ctx).
		Raw(`SELECT DISTINCT ON (w.id) w.id AS work_id, r.external_id AS workno
			FROM catalog_work w
			JOIN catalog_release rel ON rel.work_id = w.id AND rel.deleted_at IS NULL
			JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = rel.id
				AND r.source_id = ? AND r.link_kind = ?
			WHERE w.medium_id = ? AND w.deleted_at IS NULL
			ORDER BY w.id, r.external_id`,
			model.EntityTypeRelease, reg.dlsiteSource, model.LinkKindExact, reg.galgameMedium).
		Scan(&out).Error; err != nil {
		return nil, err
	}
	return window(out, limit, offset), nil
}

// loadTaxonomy loads the FULL zh_CN vocabulary (wave 67's genre_taxonomy in
// the same dlsite mirror DB — 407 rows live, trivially memory-resident) as
// genre_id → current official zh name. The taxonomy holds the CURRENT name per
// id, so resolving by id auto-corrects works embedding a since-renamed genre.
func loadTaxonomy(ctx context.Context, dlsiteDB *gorm.DB) (map[int]string, error) {
	var rows []struct {
		GenreID int    `gorm:"column:genre_id"`
		Name    string `gorm:"column:name"`
	}
	if err := dlsiteDB.WithContext(ctx).Table("genre_taxonomy").
		Select("genre_id, name").Where("locale = ?", taxonomyLocale).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int]string, len(rows))
	for _, r := range rows {
		if name := strings.TrimSpace(r.Name); name != "" {
			out[r.GenreID] = name
		}
	}
	return out, nil
}

// loadMirrorGenres batch-loads the referenced mirror rows' raw genres jsonb
// (product_json.genres — surveyed: an array of {id number, name string} on
// 805k mirror rows, NULL on the rest). A workno absent from the map means the
// mirror has no row at all (missing_mirror); a present-but-empty raw value is
// the caller's NoGenres branch.
func loadMirrorGenres(ctx context.Context, dlsiteDB *gorm.DB, worknos []string) (map[string][]byte, error) {
	out := make(map[string][]byte, len(worknos))
	type row struct {
		Workno string `gorm:"column:workno"`
		Genres []byte `gorm:"column:genres"`
	}
	for start := 0; start < len(worknos); start += 1000 {
		end := min(start+1000, len(worknos))
		var batch []row
		if err := dlsiteDB.WithContext(ctx).Table("works").
			Select(`workno, product_json->'genres' AS genres`).
			Where("workno IN ?", worknos[start:end]).Scan(&batch).Error; err != nil {
			return nil, err
		}
		for _, r := range batch {
			out[r.Workno] = r.Genres
		}
	}
	return out, nil
}

// genreEl is one raw product_json.genres element (only the keys this backfill
// reads; name_base / search_val ignored).
type genreEl struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// resolvedGenre is one decided tag of a work after name resolution and
// in-work dedupe.
type resolvedGenre struct {
	ID           int
	Name         string
	FromTaxonomy bool
}

// resolveGenres defensively decodes a work's raw genres jsonb and resolves
// each element's display name (taxonomy zh_CN by id → embedded ja fallback for
// retired ids), bumping the stats counters (see the package doc for the branch
// taxonomy). Returns nil when there is nothing to write. The decided list
// keeps the mirror's element order (DLsite's own official ordering) minus
// collapsed duplicates — deterministic for writes and samples.
func resolveGenres(raw []byte, taxonomy map[int]string, st *Stats) []resolvedGenre {
	if len(raw) == 0 || string(raw) == "null" { // key absent or JSON null
		st.NoGenres++
		return nil
	}
	var els []genreEl
	if err := json.Unmarshal(raw, &els); err != nil {
		// Not an array of {id,name} objects — a malformed mirror row. Counted,
		// never fatal.
		st.NotArray++
		return nil
	}
	if len(els) == 0 {
		st.NoGenres++
		return nil
	}
	out := make([]resolvedGenre, 0, len(els))
	seen := map[string]bool{}
	for _, el := range els {
		name, fromTaxonomy := taxonomy[el.ID]
		if !fromTaxonomy { // retired id (no current vocabulary row) → embedded ja
			name = strings.TrimSpace(el.Name)
			if name == "" {
				st.NameBlank++
				continue
			}
		}
		if fromTaxonomy {
			st.ZhHit++
		} else {
			st.JaFallback++
		}
		if seen[name] { // two ids resolving to one name within the work
			st.DupCollapsed++
			continue
		}
		seen[name] = true
		out = append(out, resolvedGenre{ID: el.ID, Name: name, FromTaxonomy: fromTaxonomy})
	}
	if len(out) == 0 {
		return nil // every element was blank — already counted per element
	}
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
