package handler

import (
	"strconv"
	"strings"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/perm"
	"api/internal/platform/galgame/repository"
	"api/internal/platform/galgame/service"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// SeriesHandler — read paths through repo, mutations through
// TaxonomyService. Note: series is special — Name/Description go to
// taxonomy_revision, but the GalgameIDs list (membership) becomes
// per-affected-galgame galgame_revision rows. Service handles both.
type SeriesHandler struct {
	seriesRepo *repository.SeriesRepository
	taxSvc     *service.TaxonomyService
}

// NewSeriesHandler creates a new SeriesHandler
func NewSeriesHandler(seriesRepo *repository.SeriesRepository, taxSvc *service.TaxonomyService) *SeriesHandler {
	return &SeriesHandler{seriesRepo: seriesRepo, taxSvc: taxSvc}
}

// List returns a paginated list of series
func (h *SeriesHandler) List(c fiber.Ctx) error {
	var req dto.ListSeriesRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.Limit <= 0 {
		req.Limit = 24
	}

	// Default safe-by-default (sfw) when caller omits content_limit.
	// Affects both the preview galgames embedded under each series AND
	// the cnt total returned per series row (kept in sync).
	contentLimit := utils.ParseContentLimit(req.ContentLimit, "sfw")
	items, total, err := h.seriesRepo.List(c.Context(), req.Page, req.Limit, contentLimit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{"items": items, "total": total})
}

// Get returns a series by ID with all galgames.
//
// content_limit filters which associated galgames are embedded in the
// response. Default safe-by-default ("sfw") — pass `?content_limit=all`
// to include NSFW. The series row itself is always returned regardless.
func (h *SeriesHandler) Get(c fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	contentLimit := utils.ParseContentLimit(c.Query("content_limit"), "sfw")
	series, err := h.seriesRepo.FindByID(c.Context(), id, contentLimit)
	if err != nil {
		return response.NotFound(c, errors.ErrNotFound)
	}

	return response.Success(c, series)
}

// Create — delegates to TaxonomyService. Series taxonomy_revision
// records Name+Description; each gid in GalgameIDs additionally gets
// its own galgame_revision (changed_fields=['series_id']) since
// membership is owned by the galgame side, not by series.
func (h *SeriesHandler) Create(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	roles, _ := c.Locals("user_roles").([]string)
	// Creating a series re-homes arbitrary galgames (writes their series_id +
	// a galgame_revision), so it must be staff-only — same gate as series
	// Update/Delete/Revert and the sibling tag/official/engine mutations.
	if !perm.Resolver.Can(roles, perm.TaxonomyEditAny) {
		return response.Forbidden(c, errors.ErrForbidden)
	}

	var req dto.CreateSeriesRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	sr, err := h.taxSvc.CreateSeries(c.Context(), int(userID), roleLevel(roles), &req, req.GalgameIDs)
	if err != nil {
		return mapAppErrOrInternal(c, err)
	}
	return response.Success(c, sr)
}

// Update — delegates to TaxonomyService. Path id is bound to the DTO
// (UpdateSeriesRequest.SeriesID) so the service signature matches the
// other entity flows.
func (h *SeriesHandler) Update(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	roles, _ := c.Locals("user_roles").([]string)
	// Mirror Delete/Revert and the sibling tag/official/engine Update gates:
	// editing a series renames it and can re-home arbitrary galgames, so it
	// must be staff-only (the route only attaches jwtAuth, not RequireRole).
	if !perm.Resolver.Can(roles, perm.TaxonomyEditAny) {
		return response.Forbidden(c, errors.ErrForbidden)
	}

	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	var req dto.UpdateSeriesRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}
	req.SeriesID = id

	if err := h.taxSvc.UpdateSeries(c.Context(), int(userID), roleLevel(roles), &req); err != nil {
		return mapAppErrOrInternal(c, err)
	}
	return response.Success(c, nil)
}

// Delete deletes a series (requires admin/moderator). Force-purge:
// unbinds galgame.series_id for every member, writes the deletion
// revision + one galgame_revision per affected gid.
func (h *SeriesHandler) Delete(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	roles, _ := c.Locals("user_roles").([]string)
	if !perm.Resolver.Can(roles, perm.TaxonomyEditAny) {
		return response.Forbidden(c, errors.ErrForbidden)
	}

	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	relations, affected, err := h.taxSvc.DeleteSeries(c.Context(), int(userID), roleLevel(roles), id)
	if err != nil {
		return mapAppErrOrInternal(c, err)
	}
	return response.Success(c, fiber.Map{
		"deleted":              true,
		"purged_relations":     relations,
		"affected_galgame_ids": affected,
	})
}

// Search searches galgames by keywords for series assignment
func (h *SeriesHandler) Search(c fiber.Ctx) error {
	var req dto.SearchSeriesRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	kw := strings.TrimSpace(req.Keywords)
	if len(kw) > 107 {
		kw = kw[:107]
	}

	parts := strings.Fields(kw)
	if len(parts) == 0 {
		return response.Success(c, []any{})
	}

	galgames, err := h.seriesRepo.SearchGalgames(c.Context(), parts)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, galgames)
}

// Modal returns galgames by IDs (for modal display)
func (h *SeriesHandler) Modal(c fiber.Ctx) error {
	var req dto.ModalRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if len(req.IDs) == 0 {
		return response.Success(c, []any{})
	}

	galgames, err := h.seriesRepo.FindGalgamesByIDs(c.Context(), req.IDs)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	// Sort results to match input IDs order
	idOrder := make(map[int]int, len(req.IDs))
	for i, id := range req.IDs {
		idOrder[id] = i
	}
	sorted := make([]model.Galgame, len(galgames))
	copy(sorted, galgames)
	for i := range sorted {
		for j := range sorted {
			if idOrder[sorted[i].ID] < idOrder[sorted[j].ID] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	return response.Success(c, sorted)
}
