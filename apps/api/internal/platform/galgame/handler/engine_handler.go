package handler

import (
	"encoding/json"
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

// Create creates a new engine (any logged-in user — lets kungal/moyu
// users introduce an engine missing from the wiki). Engine is not
// Meilisearch-indexed, so there is no search hook.
func (h *EngineHandler) Create(c fiber.Ctx) error {
	var req dto.CreateEngineRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	exists, err := h.engineRepo.ExistsByName(c.Context(), name)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	if exists {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, "已存在同名 engine")
	}

	engine := &model.GalgameEngine{
		Name:        name,
		Description: req.Description,
	}
	if err := h.engineRepo.Create(c.Context(), engine, req.Alias); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, engine)
}

// Delete removes an engine. Requires role > 1 (admin/moderator). Safe by
// default: refused while still referenced; `?force=true` is the
// deliberate one-click purge-all-references-then-hard-delete (same
// convention as DELETE /admin/image/:hash?force=true). See
// TagHandler.Delete for the rationale.
func (h *EngineHandler) Delete(c fiber.Ctx) error {
	roles, _ := c.Locals("user_roles").([]string)
	if !hasRole(roles, "admin", "moderator") {
		return response.Forbidden(c, errors.ErrForbidden)
	}

	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	rel, err := h.engineRepo.CountReferences(c.Context(), id)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	force := c.Query("force") == "true"
	if rel > 0 && !force {
		return response.BadRequestMsg(c, errors.ErrValidationFailed,
			"该 engine 仍被 "+strconv.FormatInt(rel, 10)+" 个 galgame 引用；如确认要一键清除全部引用并硬删除，请带 ?force=true（仅 role>1）")
	}

	if err := h.engineRepo.Delete(c.Context(), id); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{
		"deleted":          true,
		"forced":           force && rel > 0,
		"purged_relations": rel,
	})
}
