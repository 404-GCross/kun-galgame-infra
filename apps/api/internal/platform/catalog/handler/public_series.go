package handler

import (
	"strconv"

	"api/internal/platform/catalog/service"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

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
