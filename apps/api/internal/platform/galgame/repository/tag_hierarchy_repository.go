package repository

import (
	"context"
	"strings"

	"api/internal/platform/galgame/model"
)

// Tag hierarchy faces (tags/multi expand=descendants + the tag-detail children
// block). The edges live in galgame_tag_edge — wiki tag ids on both ends,
// projected from the VNDB tag DAG by cmd/backfill-tag-edges.

// DescendantTagIDs expands each seed tag id to {itself + every descendant} over
// the galgame_tag_edge closure (recursive CTE; the DAG is shallow and small, so
// the closure is computed per request). A seed with no out-edges expands to
// just itself; an unknown seed also expands to itself (it then matches nothing,
// exactly like the non-expanded face). Diamonds collapse via UNION dedup.
func (r *TagRepository) DescendantTagIDs(ctx context.Context, seedIDs []int) (map[int][]int, error) {
	out := make(map[int][]int, len(seedIDs))
	if len(seedIDs) == 0 {
		return out, nil
	}

	// VALUES (?::bigint), (?::bigint) … feeds the CTE anchor so ONE
	// round-trip expands every seed. The explicit cast matters twice: a bare
	// VALUES placeholder resolves to text (pgx sends unknown-typed params) and
	// text = bigint has no operator (42883); and the cast must be bigint —
	// GORM maps the model int columns to bigint, so an int anchor would clash
	// with the recursive term's child_id type (42804).
	holders := make([]string, len(seedIDs))
	args := make([]any, len(seedIDs))
	for i, id := range seedIDs {
		holders[i] = "(?::bigint)"
		args[i] = id
	}

	type row struct {
		Seed int `gorm:"column:seed"`
		Tag  int `gorm:"column:tag"`
	}
	var rows []row
	err := r.db.WithContext(ctx).Raw(`
		WITH RECURSIVE seeds(seed) AS (
			VALUES `+strings.Join(holders, ", ")+`
		), closure(seed, tag) AS (
			SELECT seed, seed FROM seeds
			UNION
			SELECT c.seed, e.child_id
			FROM closure c
			JOIN galgame_tag_edge e ON e.parent_id = c.tag
		)
		SELECT seed, tag FROM closure
	`, args...).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, rw := range rows {
		out[rw.Seed] = append(out[rw.Seed], rw.Tag)
	}
	return out, nil
}

// FindGalgamesByTagGroups returns published galgames carrying at least one tag
// from EVERY group (AND across groups, OR inside each group) — the grouped
// generalization of FindGalgamesByMultipleTags that tags/multi
// expand=descendants runs on. total/pagination reflect the same filtered set;
// contentLimit gates rows exactly like the flat face. An empty groups slice
// matches nothing (guarded by the service).
func (r *TagRepository) FindGalgamesByTagGroups(ctx context.Context, groups [][]int, page, limit int, contentLimit string) ([]model.Galgame, int64, error) {
	var galgames []model.Galgame
	var total int64

	query := r.db.WithContext(ctx).
		Model(&model.Galgame{}).
		Where("status = 0")
	if contentLimit != "" {
		query = query.Where("content_limit = ?", contentLimit)
	}

	// One EXISTS per group: each probes idx_galgame_tag_relation_tag
	// (tag_id, galgame_id), so the plan stays index-driven per group — no
	// GROUP BY / HAVING over the whole relation table.
	for _, group := range groups {
		query = query.Where(
			"EXISTS (SELECT 1 FROM galgame_tag_relation gtr WHERE gtr.galgame_id = galgame.id AND gtr.tag_id IN ?)",
			group,
		)
	}

	query.Count(&total)

	err := query.
		Order("resource_update_time DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&galgames).Error

	return galgames, total, err
}

// ChildTags returns a tag's DIRECT children (one edge hop), each with its
// published galgame count — the tag-detail children block, letting consumers
// surface "expand includes: 硬科幻, 科幻奇幻" without shipping the whole DAG.
// Ordered by count DESC then name for a stable, relevance-ish listing.
func (r *TagRepository) ChildTags(ctx context.Context, parentID int) ([]model.GalgameTag, error) {
	var tags []model.GalgameTag
	err := r.db.WithContext(ctx).
		Select("galgame_tag.*, COALESCE(tc.cnt, 0) AS cnt").
		Joins("JOIN galgame_tag_edge e ON e.child_id = galgame_tag.id AND e.parent_id = ?", parentID).
		Joins("LEFT JOIN (SELECT r.tag_id, COUNT(*) AS cnt FROM galgame_tag_relation r JOIN galgame g ON g.id = r.galgame_id AND g.status = 0 GROUP BY r.tag_id) tc ON tc.tag_id = galgame_tag.id").
		Order("cnt DESC, galgame_tag.name ASC").
		Find(&tags).Error
	return tags, err
}
