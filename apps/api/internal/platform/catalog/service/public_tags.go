// public_tags.go — the canonical-tag detail endpoint (GET /v1/catalog/tags/{id},
// doc 106 G5) over the step-74/87/90 vocabulary (catalog_tag +
// catalog_tag_source_map).
package service

import (
	"context"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"
)

// TagDetail projects one canonical tag. found=false → 404 on an unknown id.
// include=works attaches the works carrying any source tag mapped to this
// canonical tag (DISTINCT works via catalog_tag_source_map ⋈ catalog_work_tag,
// scoped to the LIVE galgame fetchable set; offset paging, r18 briefs dropped
// unless nsfw — the labels/{id} works convention).
func (s *PublicService) TagDetail(ctx context.Context, id int64, withWorks, nsfw bool, limit, offset int) (dto.PublicTagDetail, bool, error) {
	var head struct {
		ID   int64
		Name string
		Tier int16
		Kind int16
	}
	if err := s.db.WithContext(ctx).Raw(
		`SELECT id, name, tier, kind FROM catalog_tag WHERE id = ?`, id).Scan(&head).Error; err != nil {
		return dto.PublicTagDetail{}, false, err
	}
	if head.ID == 0 {
		return dto.PublicTagDetail{}, false, nil // identity PK starts at 1 → 0 = miss
	}
	rec := dto.PublicTagDetail{
		ID: head.ID, Name: head.Name, Tier: tagTierKey(head.Tier), Kind: tagKindKey(head.Kind),
	}
	// work_count is the browse lane's number for this one row (A2-1e) — the
	// SAME nsfw-aware aggregate, so a detail page and the list it was reached
	// from can never disagree.
	counts, err := s.workCountsFor(ctx, tagWorkEdge, []int64{id}, nsfw)
	if err != nil {
		return dto.PublicTagDetail{}, false, err
	}
	rec.WorkCount = counts[id]
	// sexual is the TAG-level safety flag (A2-1f) — the same derivation and the
	// same coverage caveat the browse lane's rows carry.
	sexual, err := s.tagSexualFor(ctx, []int64{id})
	if err != nil {
		return dto.PublicTagDetail{}, false, err
	}
	rec.Sexual = sexual[id]
	// intros are part of the base record (not include-gated), like a label's.
	intros, err := s.tagIntros(ctx, id)
	if err != nil {
		return dto.PublicTagDetail{}, false, err
	}
	rec.Intros = intros
	if withWorks {
		var wrows []struct {
			WorkID int64 `gorm:"column:work_id"`
		}
		if err := s.db.WithContext(ctx).Raw(`
			SELECT DISTINCT wt.work_id FROM catalog_work_tag wt
			JOIN catalog_tag_source_map m ON m.source_id = wt.source_id AND m.source_name = wt.name
			JOIN catalog_work w ON w.id = wt.work_id AND w.deleted_at IS NULL AND w.status = ? AND w.medium_id = ?
			WHERE m.tag_id = ?
			ORDER BY wt.work_id
			LIMIT ? OFFSET ?`,
			model.WorkStatusLive, galgameMediumID, id, limit, offset).Scan(&wrows).Error; err != nil {
			return dto.PublicTagDetail{}, false, err
		}
		ids := make([]int64, len(wrows))
		for i, r := range wrows {
			ids[i] = r.WorkID
		}
		briefs, err := s.loadWorkBriefs(ctx, ids, nsfw)
		if err != nil {
			return dto.PublicTagDetail{}, false, err
		}
		rec.Works = make([]dto.PublicWorkBrief, 0, len(ids))
		for _, wid := range ids {
			if b := briefs[wid]; b != nil {
				rec.Works = append(rec.Works, *b)
			}
		}
		rec.NextOffset = nextOffset(len(wrows), limit, offset)
	}
	return rec, true, nil
}

// tagIntros loads a canonical tag's multilingual intros, merged to one element
// per language (lowest source_id wins — the step-65 intro merge), lang ASC.
// The labelIntros projection one table over: the W0 data-layer-retirement wave
// rescued the wiki's hand-written galgame_tag.description rows into
// catalog_tag_intro, and this is the read face that makes them reachable.
// source goes through sourceKey so the wire always carries the PUBLIC source
// spelling (the character/name intro convention). Empty → [].
func (s *PublicService) tagIntros(ctx context.Context, tagID int64) ([]dto.PublicTagIntro, error) {
	var rows []struct {
		Lang     string `gorm:"column:lang"`
		Intro    string `gorm:"column:intro"`
		SourceID int16  `gorm:"column:source_id"`
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT lang, intro, source_id FROM catalog_tag_intro
		WHERE tag_id = ? ORDER BY lang, source_id`, tagID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]dto.PublicTagIntro, 0, len(rows))
	seenLang := map[string]bool{}
	for _, r := range rows {
		if seenLang[r.Lang] {
			continue // a lower source_id already claimed this language
		}
		seenLang[r.Lang] = true
		out = append(out, dto.PublicTagIntro{Lang: r.Lang, Intro: r.Intro, Source: s.sourceKey(r.SourceID)})
	}
	return out, nil
}

// tagTierKey projects the TagTier* vocabulary to the public string keys.
func tagTierKey(t int16) string {
	switch t {
	case model.TagTierLongtail:
		return "longtail"
	case model.TagTierHidden:
		return "hidden"
	default:
		return "core"
	}
}

// tagKindKey projects the TagKind* vocabulary to the public string keys.
func tagKindKey(k int16) string {
	if k == model.TagKindMeta {
		return "meta"
	}
	return "content"
}
