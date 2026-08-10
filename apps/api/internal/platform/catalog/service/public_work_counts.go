// public_work_counts.go — work_count on the work-record CHIPS (A2-R1 区 B,
// refs/proj/136).
//
// A2-1b gave the taxonomy BROWSE lanes and their detail records an nsfw-aware
// work_count; the chips that actually get rendered — the labels / tags / engines
// on a work record, and the labels on a list row — never got one, so every
// downstream chip showed a literal "+ 0" (or nothing) beside a real
// attribution. That is the same failure mode the deprecated galgame face
// shipped, and the invariant public_taxonomy.go was written to hold: the count
// beside a chip MUST equal the total the caller gets by following that chip.
//
// So the numbers here are not a new aggregate — they are workCountsFor, the
// taxonomy lane's own query, over the same three edge fragments. One batched
// GROUP BY per facet for the whole record (or the whole list page), never one
// count per chip. The member call each ≡ names below is the one an entity page
// actually makes, i.e. carrying claim_state=live — see taxonomyLiveClaim:
//
//	labels[]  → labelWorkEdge  ≡ works?label_id=  ≡ labels/{id}.work_count
//	tags[]    → tagWorkEdge    ≡ works?tag_id=    ≡ tags/{id}.work_count
//	engines[] → engineWorkEdge ≡ works?engine_id= ≡ engines/{id}.work_count
//
// The tag caliber deserves one explicit word, because it is the one place the
// count can look surprising: works?tag_id= reaches works through
// catalog_tag_source_map ⋈ catalog_work_tag, so a CLAIMED work's BRIDGED wiki
// tag can report a count that does not include the work you are looking at.
// That is not a bug in the count — it is the honest answer to "what will I get
// if I click this", which is the only thing the number promises.
package service

import (
	"context"

	"api/internal/platform/catalog/dto"
)

// attachWorkChipCounts stamps work_count onto a work record's labels[], tags[]
// and engines[]. Three batched aggregates for the whole record.
func (s *PublicService) attachWorkChipCounts(ctx context.Context, rec *dto.PublicCatalogWork, nsfw bool) error {
	// The work's own chips AND every edition's (wave 200) in ONE aggregate: the
	// same company routinely appears at both grains, and two calls would ask the
	// database the same question twice to print the same number.
	labelBlocks := make([][]dto.PublicWorkLabel, 0, 1+len(rec.Releases))
	labelBlocks = append(labelBlocks, rec.Labels)
	for i := range rec.Releases {
		labelBlocks = append(labelBlocks, rec.Releases[i].Labels)
	}
	if err := s.fillWorkLabelCounts(ctx, labelBlocks, nsfw); err != nil {
		return err
	}

	// tags: only the rows that MAP into the canonical vocabulary have a landing
	// page, so only they get the key (the unmapped ones keep the three-key
	// omission rule canonical_id / tier / kind already follow).
	tagIDs := make([]int64, 0, len(rec.Tags))
	for _, t := range rec.Tags {
		if t.CanonicalID != 0 {
			tagIDs = append(tagIDs, t.CanonicalID)
		}
	}
	tagCounts, err := s.workCountsFor(ctx, tagWorkEdge, tagIDs, nsfw)
	if err != nil {
		return err
	}
	for i := range rec.Tags {
		if rec.Tags[i].CanonicalID == 0 {
			continue
		}
		n := tagCounts[rec.Tags[i].CanonicalID]
		rec.Tags[i].WorkCount = &n
	}

	engineIDs := make([]int64, 0, len(rec.Engines))
	for _, e := range rec.Engines {
		engineIDs = append(engineIDs, e.ID)
	}
	engineCounts, err := s.workCountsFor(ctx, engineWorkEdge, engineIDs, nsfw)
	if err != nil {
		return err
	}
	for i := range rec.Engines {
		rec.Engines[i].WorkCount = engineCounts[rec.Engines[i].ID]
	}
	return nil
}

// fillWorkLabelCounts stamps work_count onto every label chip in the given
// blocks, in ONE aggregate for all of them — so a whole list page costs the
// same single query one detail record does.
//
// Both faces call THIS function rather than each computing its own number:
// "detail and list include=labels agree" is then true by construction, not by
// two implementations happening to match. The blocks share their backing arrays
// with the caller's items, so writing through them is the fill.
func (s *PublicService) fillWorkLabelCounts(ctx context.Context, blocks [][]dto.PublicWorkLabel, nsfw bool) error {
	var ids []int64
	for _, b := range blocks {
		for _, l := range b {
			ids = append(ids, l.ID)
		}
	}
	counts, err := s.workCountsFor(ctx, labelWorkEdge, ids, nsfw)
	if err != nil {
		return err
	}
	for _, b := range blocks {
		for i := range b {
			b[i].WorkCount = counts[b[i].ID]
		}
	}
	return nil
}
