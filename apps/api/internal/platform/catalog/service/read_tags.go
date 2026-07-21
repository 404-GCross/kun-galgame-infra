package service

import (
	"context"
)

// Tags read face (step 58b, refs/proj/58 Facet B) — the fifth media-aggregation
// facet, structurally identical to intros/covers/screenshots/ratings: CLAIMED
// works bridge, BODYLESS works read native rows, strict XOR, source_id on every
// row. The claimed bridge reads the galgame family's tag layer.
//
// Galgame tag storage (surveyed live, 2026-07-21):
//
//	galgame_tag: id | name text UNIQUE (the display name kungal renders — the
//	  VNDB sync resolves through docs/tagMap.ts english→chinese, so a mapped
//	  tag's name IS the localized zh name; an unmapped tag keeps its English
//	  VNDB name; user-created tags are whatever the wiki editor typed) |
//	  category (content/sexual/technical) | description
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
// Spoiler discipline: the bridge emits NON-SPOILER tags only
// (spoiler_level=0). The unified tags[] shape carries no spoiler flag (the
// bodyless side is Bangumi folksonomy, which has no spoiler concept), so a
// bridged spoiler tag would surface unlabeled to every consumer — the galgame
// read face can gate them (it exposes spoiler_level), this face cannot.
// Filtering to the non-spoiler subset is the honest bridge; it parallels the
// ratings bridge's score>0 filter.
//
// Tags are VERBATIM folksonomy (58 拍板): no vocabulary mapping, no
// normalization — and content tags NEVER touch catalog_label (the attribution
// vocabulary red line).

// WorkTagRow is one tag on a work's read face — the unified shape the claimed
// bridge (galgame_tag_relation ⋈ galgame_tag) and the bodyless native table
// (catalog_work_tag) both project into. Count is the source's vote count; 0 =
// the source has no votes (the whole galgame layer) and the DTO omits it.
type WorkTagRow struct {
	Name     string
	Count    int
	SourceID int16
}

// loadWorkTags assembles the tag set for a set of works, honoring the
// media-aggregation contract (refs/proj/51 §2/§3/§8, step 58b):
//
//   - CLAIMED (site='galgame_wiki'): bridge from galgame_tag_relation ⋈
//     galgame_tag (see the file doc for the mapping; non-spoiler only).
//     Bridge-not-copy (§2): bridged tags are never materialized into
//     catalog_work_tag.
//   - BODYLESS (site=”/NULL): the work's catalog_work_tag rows.
//   - Strict XOR (§8.D): a claimed work reads ONLY the bridge; it never falls
//     back to native rows even if it still has shadowed ones (shadow-never-delete).
//
// Batched (§9.1): claimed works bridge in one join query, bodyless works read
// in one catalog_work_tag query — never per-work. Each work's tags are ordered
// (count DESC, name ASC) — high-vote first for the bodyless folksonomy; the
// bridged rows all carry count 0, so the order degenerates to name ASC there.
// Returns a map keyed by work id; a work with no tag is absent (the caller
// renders []).
func (s *ReadService) loadWorkTags(ctx context.Context, subjects []claimSubject) (map[int64][]WorkTagRow, error) {
	out := make(map[int64][]WorkTagRow, len(subjects))
	galgameIDs, galgameToWork, bodylessIDs := partitionClaimSubjects(subjects)
	if len(galgameIDs) > 0 {
		if err := s.bridgeGalgameTags(ctx, galgameIDs, galgameToWork, out); err != nil {
			return nil, err
		}
	}
	if len(bodylessIDs) > 0 {
		if err := s.nativeWorkTags(ctx, bodylessIDs, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// bridgeGalgameTags reads the claimed works' tag layer in ONE join query and
// maps it to the unified shape. galgame_tag.name is UNIQUE and (galgame_id,
// tag_id) is the relation PK, so a work never sees a duplicate name; the SQL
// orders by name, which IS the (count DESC, name) contract order because every
// bridged row carries count 0.
func (s *ReadService) bridgeGalgameTags(ctx context.Context, galgameIDs []int64, galgameToWork map[int64]int64, out map[int64][]WorkTagRow) error {
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
	}
	// COALESCE guards the two nullable-with-default columns against legacy NULLs
	// (both semantically equal their defaults: no spoiler / user-curated).
	if err := db.Raw(`SELECT r.galgame_id, t.name, COALESCE(r.source, '') AS source
		FROM galgame_tag_relation r
		JOIN galgame_tag t ON t.id = r.tag_id
		WHERE r.galgame_id IN ? AND COALESCE(r.spoiler_level, 0) = 0
		ORDER BY r.galgame_id, t.name`, galgameIDs).Scan(&rows).Error; err != nil {
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
		out[workID] = append(out[workID], WorkTagRow{Name: r.Name, SourceID: srcID})
	}
	return nil
}

// nativeWorkTags reads the bodyless works' catalog_work_tag rows in ONE query,
// ordered so each work's tags are (count DESC, name ASC) — high-vote first.
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
