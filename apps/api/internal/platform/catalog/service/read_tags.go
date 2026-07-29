package service

import (
	"context"
	"sort"
	"strings"
)

// Tags read face (step 58b + T2, refs/proj/58 Facet B, refs/proj/70 §3/§8) —
// the fifth media-aggregation facet, and the FIRST with the (facet, source) XOR
// refinement (T2). Unlike the sibling facets (intros/covers/screenshots/ratings,
// which keep the whole-facet XOR — claimed bridge XOR bodyless native), tags
// reads the UNION of two per-source lanes:
//
//   - the WIKI bridge lane (galgame_wiki / vndb): read-time bridge from the
//     galgame family's tag layer, never materialized (bridge-not-copy) — runs
//     for CLAIMED works only (a bodyless work has no wiki body);
//   - the CATALOG-native lane (bangumi / dlsite): catalog_work_tag rows — runs
//     for ALL works, claimed and bodyless alike.
//
// A claimed work's read face is therefore bridge ∪ native (its bgm/dlsite tags
// no longer hide behind the claim); a bodyless work degenerates to the native
// lane (empty bridge). Every row carries source_id, so the two lanes stay
// attributable. Merged rows are re-sorted (count DESC, name ASC) per work.
//
// Galgame tag storage (surveyed live, 2026-07-21):
//
//	galgame_tag: id | name text UNIQUE (the display name kungal renders — the
//	  VNDB sync resolves through docs/tagMap.ts english→chinese, so a mapped
//	  tag's name IS the localized zh name; an unmapped tag keeps its English
//	  VNDB name; user-created tags are whatever the wiki editor typed) |
//	  category (content/sexual/technical — VNDB's cont/ero/tech, mapped at sync
//	  time; this is the `sexual` flag's ONLY source) | description
//	galgame_tag_relation: (galgame_id, tag_id) PK | spoiler_level
//	  (0=none 1=mild 2=severe) | source ('' = user-curated, 'vndb' = synced)
//	  — NO vote field anywhere in the layer.
//
// Bridge mapping: relation ⋈ tag → {name: galgame_tag.name (the localized
// display name, verbatim), count: 0 (the galgame layer has no votes — the DTO
// omits count via omitempty), source_id: relation.source mapped through
// galgameMediaSourceKey ('' → galgame_wiki, 'vndb' → vndb, unknown → the
// galgame_wiki fallback — a claimed tag is always part of the wiki body)}.
//
// Spoiler discipline (A2-1e / R8 — the shape this file's older comment said
// was missing): the unified row now CARRIES the axis (Spoiler per edge, Sexual
// per tag), so a bridged spoiler tag can no longer surface unlabeled. The
// bridge therefore filters to a CEILING supplied by the caller instead of the
// hard spoiler_level=0 it used to apply: 0 (the default on every face) is the
// old behavior byte for byte, and only a caller that explicitly asks for
// spoilers=1|2 ever sees more.
//
// Coverage is asymmetric and that asymmetry is real, not an implementation
// gap: the spoiler level and the sexual category exist ONLY in the VNDB-derived
// vocabulary the wiki layer carries. Bangumi/DLsite folksonomy has neither
// concept, so those rows are spoiler 0 / sexual false — the absence of the
// axis, which the public DTO documents so a consumer cannot mistake it for an
// assertion of safety.
//
// Tags are VERBATIM folksonomy (58 拍板): no vocabulary mapping, no
// normalization — and content tags NEVER touch catalog_label (the attribution
// vocabulary red line).

// galgameTagCategorySexual is the wiki tag category that VNDB's `ero` maps to
// (the sync writes cont→content, ero→sexual, tech→technical). It is the sole
// input of the public tag `sexual` flag.
const galgameTagCategorySexual = "sexual"

// WorkTagRow is one tag on a work's read face — the unified shape the wiki
// bridge (galgame_tag_relation ⋈ galgame_tag) and the catalog-native table
// (catalog_work_tag) both project into. Count is the source's vote count; 0 =
// the source has no votes (the whole galgame layer) and the DTO omits it.
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
	// 2 major) and Sexual flags the TAG as sexual-category. Both come from the
	// wiki/VNDB lane only; the catalog-native lane leaves them zero because
	// Bangumi/DLsite publish no such axis.
	Spoiler int16
	Sexual  bool
	// Canonical overlay — nil when the tag is not (yet) in the canonical
	// vocabulary. Tier: 0=core 1=longtail 2=hidden; Kind: 0=content 1=meta.
	CanonicalID *int64
	Tier        *int16
	Kind        *int16
}

// loadWorkTags assembles the tag set for a set of works, honoring the
// media-aggregation contract with the T2 (facet, source) XOR (refs/proj/51
// §2/§3/§8, refs/proj/70 §3/§8, step 58b + T2):
//
//   - WIKI bridge lane (CLAIMED works, site='galgame_wiki'): bridge from
//     galgame_tag_relation ⋈ galgame_tag (see the file doc for the mapping;
//     non-spoiler only). Bridge-not-copy (§2): bridged tags are never
//     materialized into catalog_work_tag.
//   - CATALOG-native lane (ALL works): the work's catalog_work_tag rows
//     (bangumi/dlsite). Claimed works read this lane too — the (facet, source)
//     XOR (T2) narrows the old whole-facet XOR to per-source: the catalog
//     sources always read native, the wiki sources always bridge.
//
// A claimed work's read face is bridge ∪ native; a bodyless work degenerates to
// the native lane. (The pre-T2 "claimed never reads native" rule survives only
// as the per-source fact that a claimed work has no wiki rows in the native
// table — bridge-not-copy still holds.)
//
// Batched (§9.1): claimed works bridge in one join query, every work reads the
// native table in one catalog_work_tag query — never per-work. Each work's
// merged rows are re-sorted (count DESC, name ASC): voted bgm rows lead, the
// count-0 rows (bridged wiki tags + bgm meta tags) trail by name. Returns a map
// keyed by work id; a work with no tag is absent (the caller renders []).
// spoilerMax is the per-edge spoiler CEILING: rows above it are not loaded.
// 0 (every face's default) reproduces the pre-A2-1e behavior exactly.
func (s *ReadService) loadWorkTags(ctx context.Context, subjects []claimSubject, spoilerMax int16) (map[int64][]WorkTagRow, error) {
	out := make(map[int64][]WorkTagRow, len(subjects))
	galgameIDs, galgameToWork, _ := partitionClaimSubjects(subjects)
	// Wiki bridge lane — CLAIMED works only.
	if len(galgameIDs) > 0 {
		if err := s.bridgeGalgameTags(ctx, galgameIDs, galgameToWork, out, spoilerMax); err != nil {
			return nil, err
		}
	}
	// Catalog-native lane — ALL works (claimed + bodyless). partitionClaimSubjects
	// is shared with the whole-facet-XOR facets, so we build the full work-id list
	// here rather than reusing its bodyless-only split.
	allIDs := make([]int64, 0, len(subjects))
	for _, sub := range subjects {
		allIDs = append(allIDs, sub.WorkID)
	}
	if len(allIDs) > 0 {
		if err := s.nativeWorkTags(ctx, allIDs, out); err != nil {
			return nil, err
		}
	}
	// Merge re-sort: a claimed work's bridge (count 0) and native rows arrive
	// from two queries, so re-order each work by the (count DESC, name ASC)
	// contract before the overlay.
	for workID, rows := range out {
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].Count != rows[j].Count {
				return rows[i].Count > rows[j].Count
			}
			return rows[i].Name < rows[j].Name
		})
		out[workID] = rows
	}
	// Canonical overlay (step 74): stamp canonical_id/tier/kind onto every
	// mapped tag, across BOTH lanes — the map is keyed (source_id, name), which
	// a bridged claimed vndb tag (source_id=vndb, name=galgame_tag.name) hits
	// exactly like a native bangumi/dlsite tag does. Additive: unmapped tags
	// keep nil overlay fields.
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

// bridgeGalgameTags reads the claimed works' wiki tag layer in ONE join query
// and maps it to the unified shape. galgame_tag.name is UNIQUE and (galgame_id,
// tag_id) is the relation PK, so a work never sees a duplicate name. The SQL
// orders by name for a stable append; the final per-work order is (count DESC,
// name ASC), applied by loadWorkTags after these count-0 bridge rows merge with
// the native lane's voted rows.
func (s *ReadService) bridgeGalgameTags(ctx context.Context, galgameIDs []int64, galgameToWork map[int64]int64, out map[int64][]WorkTagRow, spoilerMax int16) error {
	db := s.db.WithContext(ctx)

	// The tag relation's source domain is only ”/vndb (surveyed), but resolving
	// the full galgameMediaSourceKey range keeps the mapping total — an
	// unexpected value falls back to galgame_wiki like the cover bridge.
	srcIDByKey, err := s.sourceIDsByKey(ctx, []string{sourceKeyGalgameWiki, sourceKeyVNDB, sourceKeyBangumi, sourceKeyUpscale})
	if err != nil {
		return err
	}
	fallbackSrc := srcIDByKey[sourceKeyGalgameWiki]

	var rows []struct {
		GalgameID int64  `gorm:"column:galgame_id"`
		Name      string `gorm:"column:name"`
		Source    string `gorm:"column:source"`
		Spoiler   int16  `gorm:"column:spoiler"`
		Category  string `gorm:"column:category"`
	}
	// COALESCE guards the two nullable-with-default columns against legacy NULLs
	// (both semantically equal their defaults: no spoiler / user-curated).
	// spoiler_level is clamped into 0-2 on the way out: the wiki column is a
	// bigint with no CHECK, and the public contract promises a three-value
	// vocabulary.
	if err := db.Raw(`SELECT r.galgame_id, t.name, COALESCE(r.source, '') AS source,
			LEAST(GREATEST(COALESCE(r.spoiler_level, 0), 0), 2)::smallint AS spoiler,
			t.category
		FROM galgame_tag_relation r
		JOIN galgame_tag t ON t.id = r.tag_id
		WHERE r.galgame_id IN ? AND COALESCE(r.spoiler_level, 0) <= ?
		ORDER BY r.galgame_id, t.name`, galgameIDs, spoilerMax).Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		workID, ok := galgameToWork[r.GalgameID]
		if !ok {
			continue
		}
		srcID := fallbackSrc
		if key, known := galgameMediaSourceKey[r.Source]; known {
			srcID = srcIDByKey[key]
		}
		out[workID] = append(out[workID], WorkTagRow{
			Name: r.Name, SourceID: srcID,
			Spoiler: r.Spoiler, Sexual: r.Category == galgameTagCategorySexual,
		})
	}
	return nil
}

// nativeWorkTags reads the catalog-native catalog_work_tag rows for a set of
// works (claimed AND bodyless, T2) in ONE query. The SQL pre-orders (count DESC,
// name) for a stable append; the authoritative per-work order is applied by
// loadWorkTags after the bridge merge.
func (s *ReadService) nativeWorkTags(ctx context.Context, workIDs []int64, out map[int64][]WorkTagRow) error {
	db := s.db.WithContext(ctx)
	var rows []struct {
		WorkID   int64  `gorm:"column:work_id"`
		Name     string `gorm:"column:name"`
		Count    int    `gorm:"column:count"`
		SourceID int16  `gorm:"column:source_id"`
	}
	if err := db.Raw(`SELECT work_id, name, count, source_id FROM catalog_work_tag
		WHERE work_id IN ? ORDER BY work_id, count DESC, name`, workIDs).Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		out[r.WorkID] = append(out[r.WorkID], WorkTagRow{
			Name: r.Name, Count: r.Count, SourceID: r.SourceID,
		})
	}
	return nil
}
