// public_tags.go — the canonical-tag detail endpoint (GET /v1/catalog/tags/{id},
// doc 106 G5) over the step-74/87/90 vocabulary (catalog_tag +
// catalog_tag_source_map).
//
// OWNERSHIP (doc 106 W2 file-ownership table): TagDetail is the W2C wave's to
// implement — W1 ships it as a compiling stub (not-found) so the route/spec
// land first. The tier/kind key mappers are W1-owned and FROZEN (the works
// facet overlay in public_service.go shares them).
package service

import (
	"context"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"
)

// TagDetail projects one canonical tag. found=false → 404 on an unknown id.
// include=works attaches the works carrying any source tag mapped to this
// canonical tag (DISTINCT works via catalog_tag_source_map ⋈ catalog_work_tag
// riding idx_catalog_work_tag_name_source; offset paging over the unfiltered
// query, r18 briefs dropped unless nsfw — the labels/{id} works convention).
//
// W2C TODO: implement (head row from catalog_tag; works page = DISTINCT
// work_id JOIN through the map, ordered by work id; loadWorkBriefs for the
// brief projection; nextOffset for the paging pointer).
func (s *PublicService) TagDetail(ctx context.Context, id int64, withWorks, nsfw bool, limit, offset int) (dto.PublicTagDetail, bool, error) {
	_ = ctx
	_ = withWorks
	_ = nsfw
	_ = limit
	_ = offset
	_ = id
	return dto.PublicTagDetail{}, false, nil
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
