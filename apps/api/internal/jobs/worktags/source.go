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
	Tags      []byte `gorm:"column:tags"`
}

func loadCandidates(ctx context.Context, db *gorm.DB, reg registry, limit, offset int) ([]candidate, error) {
	var out []candidate
	if err := db.WithContext(ctx).
		Raw(`SELECT DISTINCT ON (w.id) w.id AS work_id, r.external_id::bigint AS subject_id,
				sub.tags AS tags
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

type subjectTag struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func parseSubjectTags(raw []byte, st *Stats) []subjectTag {
	if len(raw) == 0 || string(raw) == "null" {
		st.NoTags++
		return nil
	}
	var els []subjectTag
	if err := json.Unmarshal(raw, &els); err != nil {
		st.NotArray++
		return nil
	}
	if len(els) == 0 {
		st.NoTags++
		return nil
	}
	best := map[string]int{}
	for _, el := range els {
		name := strings.TrimSpace(el.Name)
		if name == "" {
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
		return nil
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
