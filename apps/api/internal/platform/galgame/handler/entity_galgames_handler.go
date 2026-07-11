package handler

import (
	"strconv"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/repository"
	"api/internal/platform/galgame/service"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// EntityGalgamesHandler serves the entity reverse-lookup reads (step 20):
// GET /galgame/officials/{id}/galgames and GET /galgame/tags/{id}/galgames.
// These sit in the galgame READ namespace (part of read-openapi) so a downstream
// entity page ("what else did this circle make / what else carries this tag") is
// self-sufficient on direct navigation: the response carries the entity's own
// identity + a page of enriched galgame briefs + the total. Read-only, no audit.
type EntityGalgamesHandler struct {
	officialRepo *repository.OfficialRepository
	tagRepo      *repository.TagRepository
	galgameSvc   *service.GalgameService
}

// NewEntityGalgamesHandler wires the reverse-lookup handler.
func NewEntityGalgamesHandler(officialRepo *repository.OfficialRepository, tagRepo *repository.TagRepository, galgameSvc *service.GalgameService) *EntityGalgamesHandler {
	return &EntityGalgamesHandler{officialRepo: officialRepo, tagRepo: tagRepo, galgameSvc: galgameSvc}
}

// entityPageParams reads page/limit (1-based page, default limit 24, cap 50) +
// the sort + content_limit query params, mirroring the wiki's other list faces.
// content_limit defaults to sfw (safe-by-default); "all" → "" = no filter.
func entityPageParams(c fiber.Ctx) (page, limit int, sortField, sortOrder, contentLimit string) {
	page = 1
	if p, err := strconv.Atoi(c.Query("page")); err == nil && p > 1 {
		page = p
	}
	limit = 24
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		limit = l
	}
	if limit > 50 {
		limit = 50
	}
	return page, limit, c.Query("sort_field"), c.Query("sort_order"), utils.ParseContentLimit(c.Query("content_limit"), "sfw")
}

// OfficialGalgames returns an official's self-description + a page of its
// published galgames. 404 on a missing id.
func (h *EntityGalgamesHandler) OfficialGalgames(c fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil || id <= 0 {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	official, err := h.officialRepo.FindByID(c.Context(), id)
	if err != nil {
		return response.NotFound(c, errors.ErrNotFound)
	}
	page, limit, sortField, sortOrder, contentLimit := entityPageParams(c)
	galgames, total, err := h.officialRepo.FindGalgamesByOfficialID(c.Context(), id, page, limit, sortField, sortOrder, contentLimit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	aliases := make([]string, 0, len(official.Alias))
	for _, a := range official.Alias {
		aliases = append(aliases, a.Name)
	}
	return response.Success(c, dto.OfficialGalgamesResponse{
		Official: dto.EntityHead{ID: official.ID, Name: official.Name, Category: official.Category, Aliases: aliases},
		Galgames: h.galgameSvc.EnrichBriefs(c.Context(), galgames),
		Total:    total,
	})
}

// TagGalgames returns a tag's self-description + a page of the galgames that
// carry it. 404 on a missing id.
func (h *EntityGalgamesHandler) TagGalgames(c fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil || id <= 0 {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	tag, err := h.tagRepo.FindByID(c.Context(), id)
	if err != nil {
		return response.NotFound(c, errors.ErrNotFound)
	}
	page, limit, sortField, sortOrder, contentLimit := entityPageParams(c)
	galgames, total, err := h.tagRepo.FindGalgamesByTagID(c.Context(), id, page, limit, sortField, sortOrder, contentLimit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	aliases := make([]string, 0, len(tag.Alias))
	for _, a := range tag.Alias {
		aliases = append(aliases, a.Name)
	}
	return response.Success(c, dto.TagGalgamesResponse{
		Tag:      dto.EntityHead{ID: tag.ID, Name: tag.Name, Category: tag.Category, Aliases: aliases},
		Galgames: h.galgameSvc.EnrichBriefs(c.Context(), galgames),
		Total:    total,
	})
}
