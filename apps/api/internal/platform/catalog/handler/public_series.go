// public_series.go — the series detail face on the frozen /v1/catalog public
// projection (wave 149c).
package handler

import (
	"strconv"

	"api/internal/platform/catalog/service"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// SeriesList serves GET /v1/catalog/series — the keyset series browse lane
// (id ASC), the fourth member of the labels / tags / engines family.
// work_count is nsfw-aware, exactly like its three siblings. source= selects
// lanes (curated / derived / dlsite) by the same key the rows print.
func (h *PublicHandler) SeriesList(c fiber.Ctx) error {
	limit, ok := limitPub(c.Query("limit"), 20, 100)
	if !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadLimit)
	}
	data, err := h.svc.SeriesList(c.Context(), nsfwQuery(c), c.Query("cursor"), c.Query("source"), limit)
	if err != nil {
		return taxonomyListError(c, err)
	}
	c.Set("Cache-Control", cacheSearch)
	return response.Success(c, data)
}

// Series serves GET /v1/catalog/series/{id} — the series record (identity +
// source anchor + intros); include=works attaches its member works with the
// tags/{id} paging posture (limit 1-50 default 50, clamp-high / 400-low).
//
// 400 on a non-numeric id, 404 on an unknown one. Series have no merge or
// soft-delete machinery, so a miss is a plain miss — never a redirect.
func (h *PublicHandler) Series(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	limit, offset, ok := pagePub(c)
	if !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadLimit)
	}
	rec, found, err := h.svc.SeriesDetail(c.Context(), id, service.ParsePublicInclude(c.Query("include")).Works, nsfwQuery(c), limit, offset)
	if err != nil {
		return response.InternalError(c, errors.ErrInternalServer)
	}
	if !found {
		return response.NotFound(c, errors.ErrNotFound)
	}
	c.Set("Cache-Control", cacheDetail)
	return response.Success(c, rec)
}
