package bgmworkmeta

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

// candidate is one galgame work (claimed OR bodyless — T2 admits both) joined
// to its EXACT Bangumi anchor's subject meta_tags + favorite blobs. Site is
// carried because Field B (favorite → popularity) STILL refuses claimed works
// (its XOR is not in the T2 tags decision domain) — see write.go.
type candidate struct {
	WorkID    int64   `gorm:"column:work_id"`
	SubjectID int64   `gorm:"column:subject_id"`
	Site      *string `gorm:"column:site"`
	MetaTags  []byte  `gorm:"column:meta_tags"`
	Favorite  []byte  `gorm:"column:favorite"`
}

// loadCandidates resolves galgame works carrying an EXACT Bangumi work anchor —
// matched_by UNRESTRICTED (every exact tier asserts identity, the 66/69 ruling;
// the releasemeta bgm lane precedent) — joined to the anchored subject's
// meta_tags + favorite jsonb (src_bangumi is a schema inside the catalog DB,
// single DSN). T2 (refs/proj/70 §3/§8, 88): NO claim filter — Field A (meta_tags
// → catalog_work_tag) is a catalog-native SOURCE lane that materializes for
// claimed and bodyless works alike; Field B keeps its per-field guard at write
// time. DISTINCT ON keeps ONE anchor per work (the lowest external_id); the
// external_id→bigint cast is safe (surveyed on rehearsal AND live: zero
// non-numeric exact bgm work anchors — the 69 verification). Limit/Offset window
// the distinct-work list in Go (the bgmsummaries chunking discipline).
func loadCandidates(ctx context.Context, db *gorm.DB, reg registry, limit, offset int) ([]candidate, error) {
	var out []candidate
	if err := db.WithContext(ctx).
		Raw(`SELECT DISTINCT ON (w.id) w.id AS work_id, r.external_id::bigint AS subject_id,
				w.site AS site, sub.meta_tags AS meta_tags, sub.favorite AS favorite
			FROM catalog_work w
			JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = w.id
				AND r.source_id = ? AND r.link_kind = ?
			JOIN src_bangumi.subject sub ON sub.id = r.external_id::bigint
			WHERE w.medium_id = ? AND w.deleted_at IS NULL
			ORDER BY w.id, r.external_id`,
			model.EntityTypeWork, reg.bangumiSource, model.LinkKindExact, reg.galgameMedium).
		Scan(&out).Error; err != nil {
		return nil, err
	}
	return window(out, limit, offset), nil
}

// parseMetaTags defensively decodes a subject's meta_tags jsonb (surveyed: an
// array of strings on every dump row) into the decided name list: trimmed,
// blank-skipped, in-subject duplicates collapsed keeping first occurrence
// (dump order — meta tags carry no votes to rank by). Counters are bumped on
// st (see the package doc); nil means nothing to write.
func parseMetaTags(raw []byte, st *Stats) []string {
	if len(raw) == 0 || string(raw) == "null" { // column NULL → no meta tags
		st.MetaNoTags++
		return nil
	}
	var els []string
	if err := json.Unmarshal(raw, &els); err != nil {
		// Not an array of strings — a malformed dump row. Counted, never fatal.
		st.MetaNotArray++
		return nil
	}
	if len(els) == 0 {
		st.MetaNoTags++
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(els))
	for _, el := range els {
		name := strings.TrimSpace(el)
		if name == "" { // whitespace-only element
			st.MetaNameBlank++
			continue
		}
		if seen[name] {
			st.MetaDup++
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil // every element was blank — already counted per element
	}
	return out
}

// bucketValue is one decided favorite shelf after defensive parsing.
type bucketValue struct {
	Bucket string
	Metric int16
	Value  int64
}

// favoriteShelves maps the dump's shelf keys to the PopularityMetricBgm*
// vocabulary in metric order. Note the dump serializes Bangumi's "collect"
// shelf as "done" (surveyed: exactly these five keys on every game row).
var favoriteShelves = []struct {
	Key    string
	Metric int16
}{
	{"wish", model.PopularityMetricBgmWish},
	{"done", model.PopularityMetricBgmCollect},
	{"doing", model.PopularityMetricBgmDoing},
	{"on_hold", model.PopularityMetricBgmOnHold},
	{"dropped", model.PopularityMetricBgmDropped},
}

// parseFavorite defensively decodes a subject's favorite jsonb into the
// decided shelf rows, in fixed shelf order for determinism. A present 0 is a
// real row; an absent shelf yields no row (the 62 semantics); a non-integer or
// negative value is skipped (surveyed: none exist — pure defense); a key
// outside the five-shelf vocabulary is counted, never guessed into a metric.
func parseFavorite(raw []byte, st *Stats) []bucketValue {
	if len(raw) == 0 || string(raw) == "null" {
		st.FavNoObject++
		return nil
	}
	var shelves map[string]json.Number
	if err := json.Unmarshal(raw, &shelves); err != nil {
		st.FavNoObject++
		return nil
	}
	if len(shelves) == 0 {
		st.FavNoObject++
		return nil
	}
	known := map[string]bool{}
	out := make([]bucketValue, 0, len(favoriteShelves))
	for _, shelf := range favoriteShelves {
		known[shelf.Key] = true
		num, ok := shelves[shelf.Key]
		if !ok {
			continue // absent shelf = no row, never a fake 0
		}
		v, err := num.Int64()
		if err != nil || v < 0 {
			st.FavBadValue++
			continue
		}
		out = append(out, bucketValue{Bucket: shelf.Key, Metric: shelf.Metric, Value: v})
	}
	for key := range shelves {
		if !known[key] {
			st.FavUnknownKey++
		}
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
