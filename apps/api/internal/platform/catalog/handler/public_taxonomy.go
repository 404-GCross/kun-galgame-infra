// public_taxonomy.go — the taxonomy read faces on the frozen /v1/catalog
// public projection (A2-1b): the labels / tags / engines browse lanes and the
// engine detail record.
//
// Every filter here is a CLOSED vocabulary — our own token set — so an unknown
// token is a 400 with the legal values spelled out, exactly like the works
// list's content_rating. The alternative (ignore it, serve the unfiltered page)
// is the worst failure class: a plausible-looking 200 whose rows do not match
// what the caller asked for. Contrast: an unknown SOURCE stays a miss, because
// sources are an OPEN registry.
package handler

import (
	stderrors "errors"
	"strconv"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/service"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

const (
	msgBadLabelKind = "kind must be one of game_brand, bunko, publisher, anime_studio, doujin_circle, group"
	msgBadTagTier   = "tier must be one of core, longtail, hidden"
	msgBadTagKind   = "kind must be one of content, meta"
	msgBadCursor    = "malformed cursor"
)

// LabelsList serves GET /v1/catalog/labels — the keyset label browse lane
// (id ASC). Filter by kind and/or has_works; work_count is nsfw-aware.
func (h *PublicHandler) LabelsList(c fiber.Ctx) error {
	f := service.LabelsListFilter{NSFW: nsfwQuery(c), HasWorks: boolQueryPub(c.Query("has_works"))}
	if raw := c.Query("kind"); raw != "" {
		v, ok := labelKindFromKey(raw)
		if !ok {
			return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadLabelKind)
		}
		f.Kind = &v
	}
	limit, ok := limitPub(c.Query("limit"), 20, 100)
	if !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadLimit)
	}
	data, err := h.svc.LabelsList(c.Context(), f, c.Query("cursor"), limit)
	if err != nil {
		return taxonomyListError(c, err)
	}
	c.Set("Cache-Control", cacheSearch)
	return response.Success(c, data)
}

// TagsList serves GET /v1/catalog/tags — the keyset canonical-tag browse lane
// (id ASC). Filter by tier, kind and/or has_works; work_count is nsfw-aware.
func (h *PublicHandler) TagsList(c fiber.Ctx) error {
	f := service.TagsListFilter{NSFW: nsfwQuery(c), HasWorks: boolQueryPub(c.Query("has_works"))}
	if raw := c.Query("tier"); raw != "" {
		v, ok := tagTierFromKey(raw)
		if !ok {
			return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadTagTier)
		}
		f.Tier = &v
	}
	if raw := c.Query("kind"); raw != "" {
		v, ok := tagKindFromKey(raw)
		if !ok {
			return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadTagKind)
		}
		f.Kind = &v
	}
	limit, ok := limitPub(c.Query("limit"), 20, 100)
	if !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadLimit)
	}
	data, err := h.svc.TagsList(c.Context(), f, c.Query("cursor"), limit)
	if err != nil {
		return taxonomyListError(c, err)
	}
	c.Set("Cache-Control", cacheSearch)
	return response.Success(c, data)
}

// EnginesList serves GET /v1/catalog/engines — the keyset engine browse lane
// (id ASC). No filters; work_count is nsfw-aware.
func (h *PublicHandler) EnginesList(c fiber.Ctx) error {
	limit, ok := limitPub(c.Query("limit"), 20, 100)
	if !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadLimit)
	}
	data, err := h.svc.EnginesList(c.Context(), service.EnginesListFilter{NSFW: nsfwQuery(c)}, c.Query("cursor"), limit)
	if err != nil {
		return taxonomyListError(c, err)
	}
	c.Set("Cache-Control", cacheSearch)
	return response.Success(c, data)
}

// EngineDetail serves GET /v1/catalog/engines/{id} — one engine's record with
// its exact-only identity anchors. 400 on a non-numeric id, 404 on an unknown
// one (the labels/{id} posture).
func (h *PublicHandler) EngineDetail(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	rec, found, err := h.svc.EngineDetail(c.Context(), id, nsfwQuery(c))
	if err != nil {
		return response.InternalError(c, errors.ErrInternalServer)
	}
	if !found {
		return response.NotFound(c, errors.ErrNotFound)
	}
	c.Set("Cache-Control", cacheDetail)
	return response.Success(c, rec)
}

// taxonomyListError maps a browse-lane failure: a malformed cursor is caller
// error (400), everything else is a 500.
func taxonomyListError(c fiber.Ctx, err error) error {
	if stderrors.Is(err, service.ErrBadCursor) {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadCursor)
	}
	return response.InternalError(c, errors.ErrInternalServer)
}

// labelKindFromKey is the input-validation inverse of the service's
// labelKindKey. "other" is deliberately NOT accepted: it is an output-only
// fallback for an int outside the vocabulary, not a filterable kind.
func labelKindFromKey(k string) (int16, bool) {
	switch k {
	case "game_brand":
		return model.LabelKindGameBrand, true
	case "bunko":
		return model.LabelKindBunko, true
	case "publisher":
		return model.LabelKindPublisher, true
	case "anime_studio":
		return model.LabelKindAnimeStudio, true
	case "doujin_circle":
		return model.LabelKindDoujinCircle, true
	case "group":
		return model.LabelKindGroup, true
	default:
		return 0, false
	}
}

// tagTierFromKey is the input-validation inverse of the service's tagTierKey.
func tagTierFromKey(k string) (int16, bool) {
	switch k {
	case "core":
		return model.TagTierCore, true
	case "longtail":
		return model.TagTierLongtail, true
	case "hidden":
		return model.TagTierHidden, true
	default:
		return 0, false
	}
}

// tagKindFromKey is the input-validation inverse of the service's tagKindKey.
func tagKindFromKey(k string) (int16, bool) {
	switch k {
	case "content":
		return model.TagKindContent, true
	case "meta":
		return model.TagKindMeta, true
	default:
		return 0, false
	}
}
