package handler

import (
	"strconv"
	"strings"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/repository"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// SeriesHandler handles series HTTP requests
type SeriesHandler struct {
	seriesRepo *repository.SeriesRepository
}

// NewSeriesHandler creates a new SeriesHandler
func NewSeriesHandler(seriesRepo *repository.SeriesRepository) *SeriesHandler {
	return &SeriesHandler{seriesRepo: seriesRepo}
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

	items, total, err := h.seriesRepo.List(c.Context(), req.Page, req.Limit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{"items": items, "total": total})
}

// Get returns a series by ID with all galgames
func (h *SeriesHandler) Get(c fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	series, err := h.seriesRepo.FindByID(c.Context(), id)
	if err != nil {
		return response.NotFound(c, errors.ErrNotFound)
	}

	return response.Success(c, series)
}

// Create creates a new series
func (h *SeriesHandler) Create(c fiber.Ctx) error {
	var req dto.CreateSeriesRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	series := &model.GalgameSeries{
		Name:        req.Name,
		Description: req.Description,
	}

	if err := h.seriesRepo.Create(c.Context(), series, req.GalgameIDs); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, series)
}

// Update updates a series
func (h *SeriesHandler) Update(c fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	var req dto.UpdateSeriesRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	updates := map[string]any{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}

	if err := h.seriesRepo.Update(c.Context(), id, updates, req.GalgameIDs); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, nil)
}

// Delete deletes a series (requires admin/moderator)
func (h *SeriesHandler) Delete(c fiber.Ctx) error {
	roles, _ := c.Locals("user_roles").([]string)
	if !hasRole(roles, "admin", "moderator") {
		return response.Forbidden(c, errors.ErrForbidden)
	}

	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	if err := h.seriesRepo.Delete(c.Context(), id); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, nil)
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
