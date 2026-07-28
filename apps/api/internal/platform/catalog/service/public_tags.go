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
