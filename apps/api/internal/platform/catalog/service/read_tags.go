package service

import (
	"context"
	"strings"
)

// Tags read face (step 58b + T2, refs/proj/58 Facet B, refs/proj/70 §3/§8) —
// the fifth media-aggregation facet. ONE native lane for every work since the
// W1-pre nativization (refs/proj/140): the wiki tag layer a CLAIMED work used
// to bridge at read time (galgame_tag_relation ⋈ galgame_tag) was materialized
// into catalog_work_tag by wikirescue step r — name = the wiki's localized
// display name verbatim, source_id = the edge's own source (galgame_wiki for a
// user-curated edge, vndb for a synced one), count = 0 (the wiki layer has no
// votes — the DTO omits count via omitempty), plus the safety axis — and the
// bridge was deleted. Rows are per-source attributable exactly as the two-lane
// union was; the per-work order is (count DESC, name ASC, source_id ASC):
// voted bgm rows lead, the count-0 rows trail by name, and the source id
// breaks the (count, name) tie deterministically (the old two-lane merge left
// that tie to an unstable sort — 4 groups corpus-wide).
//
// Safety axis (A2-1e / R8): Spoiler is per-EDGE (0 none, 1 minor, 2 major),
// Sexual flags the TAG as sexual-category; both live on the row now. The
// caller supplies a spoiler CEILING, applied in the query: 0 (every face's
// default) means no spoiler-flagged tag at all, and only an explicit
// spoilers=1|2 ever sees more. Coverage is asymmetric and that asymmetry is
// real, not an implementation gap: the axis exists only in the VNDB-derived
// vocabulary; Bangumi/DLsite folksonomy publishes neither concept, so those
// rows carry the explicit 0/false the importers write — the absence of the
// axis, which the public DTO documents so a consumer cannot mistake it for an
// assertion of safety.
//
// Tags are VERBATIM folksonomy (58 拍板): no vocabulary mapping, no
// normalization — and content tags NEVER touch catalog_label (the attribution
// vocabulary red line).

// WorkTagRow is one tag on a work's read face, projected from catalog_work_tag.
// Count is the source's vote count; 0 = the source has no votes (the whole
// mirrored wiki layer) and the DTO omits it.
//
// Canonical layer (step 74, additive): when this tag's (source_id, name) is
// mapped into the cross-source canonical vocabulary (catalog_tag_source_map),
// CanonicalID/Tier/Kind carry the canonical row's identity + display tier +
// content/meta kind; an UNMAPPED tag leaves them nil (fields omitted). The
// original name/count/source_id are never mutated — the canonical layer is a
// pure overlay, never a replacement.
type WorkTagRow struct {
	Name     string
	Count    int
	SourceID int16
	// Safety axis (A2-1e / R8). Spoiler is the per-EDGE level (0 none, 1 minor,
	// 2 major) and Sexual flags the TAG as sexual-category. Stored on the row;
	// folksonomy rows (bangumi/dlsite) carry the explicit 0/false their sources'
	// missing axis honestly is.
	Spoiler int16
	Sexual  bool
	// Canonical overlay — nil when the tag is not (yet) in the canonical
	// vocabulary. Tier: 0=core 1=longtail 2=hidden; Kind: 0=content 1=meta.
	CanonicalID *int64
	Tier        *int16
	Kind        *int16
}

// loadWorkTags assembles the tag set for a set of works from catalog_work_tag —
// one native lane for every work (see the file doc for how the wiki tag layer
// got here).
//
// Batched (§9.1): ONE catalog_work_tag query for the whole set — never
// per-work. Returns a map keyed by work id; a work with no tag is absent (the
// caller renders []). spoilerMax is the per-edge spoiler CEILING, applied in
// the query: rows above it are not loaded, and 0 (every face's default)
// reproduces the pre-A2-1e behavior exactly.
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
	// Canonical overlay (step 74): stamp canonical_id/tier/kind onto every
	// mapped tag — the map is keyed (source_id, name), which a mirrored claimed
	// vndb tag (source_id=vndb, name=the wiki display name) hits exactly like a
	// native bangumi/dlsite tag does. Additive: unmapped tags keep nil overlay
	// fields.
	if err := s.enrichCanonicalTags(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// enrichCanonicalTags batch-resolves the canonical overlay for every tag in out.
// It collects the distinct (source_id, name) pairs, looks them up in ONE query
// against catalog_tag_source_map ⋈ catalog_tag, and stamps CanonicalID/Tier/Kind
// on the matching rows. A tag with no map row is left untouched (nil overlay).
func (s *ReadService) enrichCanonicalTags(ctx context.Context, out map[int64][]WorkTagRow) error {
	// Distinct (source_id, name) pairs across all works — the lookup keys.
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
	}
	sql := `SELECT m.source_id, m.source_name, t.id, t.tier, t.kind
		FROM catalog_tag_source_map m
		JOIN catalog_tag t ON t.id = m.tag_id
		WHERE (m.source_id, m.source_name) IN (` + placeholders.String() + `)`
	if err := s.db.WithContext(ctx).Raw(sql, args...).Scan(&mapped).Error; err != nil {
		return err
	}
	if len(mapped) == 0 {
		return nil
	}
	byKey := make(map[key]struct {
		id         int64
		tier, kind int16
	}, len(mapped))
	for _, m := range mapped {
		byKey[key{src: m.SourceID, name: m.Name}] = struct {
			id         int64
			tier, kind int16
		}{id: m.TagID, tier: m.Tier, kind: m.Kind}
	}
	for workID, rows := range out {
		for i := range rows {
			if c, ok := byKey[key{src: rows[i].SourceID, name: rows[i].Name}]; ok {
				id, tier, kind := c.id, c.tier, c.kind
				rows[i].CanonicalID = &id
				rows[i].Tier = &tier
				rows[i].Kind = &kind
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
