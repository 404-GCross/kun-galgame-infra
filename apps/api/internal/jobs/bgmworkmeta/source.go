package bgmworkmeta

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

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

type candidate struct {
	WorkID    int64  `gorm:"column:work_id"`
	SubjectID int64  `gorm:"column:subject_id"`
	MetaTags  []byte `gorm:"column:meta_tags"`
	Favorite  []byte `gorm:"column:favorite"`
}

func loadCandidates(ctx context.Context, db *gorm.DB, reg registry, limit, offset int) ([]candidate, error) {
	var out []candidate
	if err := db.WithContext(ctx).
		Raw(`SELECT DISTINCT ON (w.id) w.id AS work_id, r.external_id::bigint AS subject_id,
				sub.meta_tags AS meta_tags, sub.favorite AS favorite
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

func parseMetaTags(raw []byte, st *Stats) []string {
	if len(raw) == 0 || string(raw) == "null" {
		st.MetaNoTags++
		return nil
	}
	var els []string
	if err := json.Unmarshal(raw, &els); err != nil {
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
		if name == "" {
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
		return nil
	}
	return out
}

type bucketValue struct {
	Bucket string
	Metric int16
	Value  int64
}

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
			continue
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
