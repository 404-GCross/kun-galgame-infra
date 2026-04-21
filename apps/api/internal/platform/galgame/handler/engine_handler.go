package handler

import (
	"encoding/json"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/repository"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// EngineHandler handles engine HTTP requests
type EngineHandler struct {
	engineRepo *repository.EngineRepository
}

// NewEngineHandler creates a new EngineHandler
func NewEngineHandler(engineRepo *repository.EngineRepository) *EngineHandler {
	return &EngineHandler{engineRepo: engineRepo}
}

// List returns all engines (small dataset, no pagination)
func (h *EngineHandler) List(c fiber.Ctx) error {
	items, err := h.engineRepo.ListAll(c.Context())
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, items)
}

// GetByName returns an engine with its galgames
func (h *EngineHandler) GetByName(c fiber.Ctx) error {
	var req dto.GetEngineByNameRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.Limit <= 0 {
		req.Limit = 24
	}

	engine, err := h.engineRepo.FindByID(c.Context(), req.EngineID)
	if err != nil {
		return response.NotFound(c, errors.ErrNotFound)
	}

	galgames, total, err := h.engineRepo.FindGalgamesByEngineID(c.Context(), req.EngineID, req.Page, req.Limit, req.ContentLimit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{"engine": engine, "galgames": galgames, "total": total})
}

// Update updates an engine (requires role > 2)
func (h *EngineHandler) Update(c fiber.Ctx) error {
	roles, _ := c.Locals("user_roles").([]string)
	if !hasRole(roles, "admin", "moderator") {
		return response.Forbidden(c, errors.ErrForbidden)
	}

	var req dto.UpdateEngineRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	updates := map[string]any{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.Alias != nil {
		aliasJSON, _ := json.Marshal(req.Alias)
		updates["alias"] = aliasJSON
	}

	if err := h.engineRepo.Update(c.Context(), req.EngineID, updates); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, nil)
}
