package service

import (
	"context"
	"strings"
)

type WorkTagRow struct {
	Name        string
	Count       int
	SourceID    int16
	Spoiler     int16
	Sexual      bool
	CanonicalID *int64
	Tier        *int16
	Kind        *int16
}

func (s *ReadService) loadWorkTags(ctx context.Context, subjects []claimSubject, spoilerMax int16) (map[int64][]WorkTagRow, error) {
	out := make(map[int64][]WorkTagRow, len(subjects))
	if len(subjects) > 0 {
		workIDs := make([]int64, 0, len(subjects))
		for _, sub := range subjects {
			workIDs = append(workIDs, sub.WorkID)
		}
		if err := s.nativeWorkTags(ctx, workIDs, out, spoilerMax); err != nil {
			return nil, err
		}
	}
	if err := s.enrichCanonicalTags(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *ReadService) enrichCanonicalTags(ctx context.Context, out map[int64][]WorkTagRow) error {
	type key struct {
		src  int16
		name string
	}
	seen := map[key]struct{}{}
	args := make([]any, 0)
	var placeholders strings.Builder
	for _, rows := range out {
		for _, r := range rows {
			k := key{src: r.SourceID, name: r.Name}
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			if placeholders.Len() > 0 {
				placeholders.WriteByte(',')
			}
			placeholders.WriteString("(?,?)")
			args = append(args, r.SourceID, r.Name)
		}
	}
	if len(args) == 0 {
		return nil
	}
	var mapped []struct {
		SourceID int16  `gorm:"column:source_id"`
		Name     string `gorm:"column:source_name"`
		TagID    int64  `gorm:"column:id"`
		Tier     int16  `gorm:"column:tier"`
		Kind     int16  `gorm:"column:kind"`
		Sexual   bool   `gorm:"column:sexual"`
	}
	sql := `SELECT m.source_id, m.source_name, t.id, t.tier, t.kind, t.sexual
		FROM catalog_tag_source_map m
		JOIN catalog_tag t ON t.id = m.tag_id
		WHERE (m.source_id, m.source_name) IN (` + placeholders.String() + `)`
	if err := s.db.WithContext(ctx).Raw(sql, args...).Scan(&mapped).Error; err != nil {
		return err
	}
	if len(mapped) == 0 {
		return nil
	}
	type canonical struct {
		id         int64
		tier, kind int16
		sexual     bool
	}
	byKey := make(map[key]canonical, len(mapped))
	for _, m := range mapped {
		byKey[key{src: m.SourceID, name: m.Name}] = canonical{id: m.TagID, tier: m.Tier, kind: m.Kind, sexual: m.Sexual}
	}
	for workID, rows := range out {
		for i := range rows {
			if c, ok := byKey[key{src: rows[i].SourceID, name: rows[i].Name}]; ok {
				id, tier, kind := c.id, c.tier, c.kind
				rows[i].CanonicalID = &id
				rows[i].Tier = &tier
				rows[i].Kind = &kind
				rows[i].Sexual = rows[i].Sexual || c.sexual
			}
		}
		out[workID] = rows
	}
	return nil
}

// nativeWorkTags reads the catalog_work_tag rows for a set of works in ONE
// query, spoiler-ceilinged in SQL. The per-work order (count DESC, name ASC,
// source_id ASC) is the read contract: source_id breaks the (count, name) tie
// deterministically now that one query serves what used to be a two-lane merge.
//
// `name ASC` means BYTE order, which is why the COLLATE is spelled out. The order
// used to be produced by a Go sort — `rows[i].Name < rows[j].Name`, i.e. a byte
// comparison — and moving it into SQL would otherwise silently adopt the database
// collation, which is a different order for anything but plain lowercase ASCII
// ("iOS" sorts before "NS" under en_US, after it byte-wise). On a frozen face that
// is a wire drift, and one no SQL-to-SQL parity check can see; the flip's own A/B
// against the pre-flip binary is what caught it.
func (s *ReadService) nativeWorkTags(ctx context.Context, workIDs []int64, out map[int64][]WorkTagRow, spoilerMax int16) error {
	db := s.db.WithContext(ctx)
	var rows []struct {
		WorkID   int64  `gorm:"column:work_id"`
		Name     string `gorm:"column:name"`
		Count    int    `gorm:"column:count"`
		SourceID int16  `gorm:"column:source_id"`
		Spoiler  int16  `gorm:"column:spoiler"`
		Sexual   bool   `gorm:"column:sexual"`
	}
	if err := db.Raw(`SELECT work_id, name, count, source_id, spoiler, sexual FROM catalog_work_tag
		WHERE work_id IN ? AND spoiler <= ?
		ORDER BY work_id, count DESC, name COLLATE "C", source_id`, workIDs, spoilerMax).Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		out[r.WorkID] = append(out[r.WorkID], WorkTagRow{
			Name: r.Name, Count: r.Count, SourceID: r.SourceID,
			Spoiler: r.Spoiler, Sexual: r.Sexual,
		})
	}
	return nil
}
