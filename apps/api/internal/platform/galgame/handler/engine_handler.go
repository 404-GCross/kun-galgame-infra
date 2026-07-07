package handler

import (
	"strconv"
	"strings"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/perm"
	"api/internal/platform/galgame/repository"
	"api/internal/platform/galgame/service"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// EngineHandler — read paths through repo, mutations through
// TaxonomyService so every change writes a taxonomy_revision row.
type EngineHandler struct {
	engineRepo *repository.EngineRepository
	taxSvc     *service.TaxonomyService
}

// NewEngineHandler creates a new EngineHandler
func NewEngineHandler(engineRepo *repository.EngineRepository, taxSvc *service.TaxonomyService) *EngineHandler {
	return &EngineHandler{engineRepo: engineRepo, taxSvc: taxSvc}
}

// GalgameIDs returns every published galgame id on this engine (id array only).
// See TagHandler.GalgameIDs.
func (h *EngineHandler) GalgameIDs(c fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil || id <= 0 {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	ids, err := h.engineRepo.GalgameIDsByEngineID(c.Context(), id)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	if ids == nil {
		ids = []int{}
	}
	return response.Success(c, fiber.Map{"ids": ids})
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
	if req.Limit > 50 { // honor the DTO max=50 (the validate tag never runs)
		req.Limit = 50
	}

	engine, err := h.engineRepo.FindByID(c.Context(), req.EngineID)
	if err != nil {
		return response.NotFound(c, errors.ErrNotFound)
	}

	// Default safe-by-default (sfw) when caller omits content_limit.
	// Pass "all" to opt INTO including NSFW. See handbook §NSFW.
	contentLimit := utils.ParseContentLimit(req.ContentLimit, "sfw")
	galgames, total, err := h.engineRepo.FindGalgamesByEngineID(c.Context(), req.EngineID, req.Page, req.Limit, contentLimit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{"engine": engine, "galgames": galgames, "total": total})
}

// Update — delegates to TaxonomyService (revision-aware).
func (h *EngineHandler) Update(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	roles, _ := c.Locals("user_roles").([]string)
	if !perm.Resolver.Can(roles, perm.TaxonomyEditAny) {
		return response.Forbidden(c, errors.ErrForbidden)
	}

	var req dto.UpdateEngineRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	if err := h.taxSvc.UpdateEngine(c.Context(), int(userID), roleLevel(roles), &req); err != nil {
		return mapAppErrOrInternal(c, err)
	}
	return response.Success(c, nil)
}

// Create — delegates to TaxonomyService. Engine is not Meilisearch-
// indexed, so no search hook.
func (h *EngineHandler) Create(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	roles, _ := c.Locals("user_roles").([]string)

	var req dto.CreateEngineRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	e, err := h.taxSvc.CreateEngine(c.Context(), int(userID), roleLevel(roles), &req)
	if err != nil {
		return mapAppErrOrInternal(c, err)
	}
	return response.Success(c, e)
}

// Delete — same UX as TagHandler.Delete (preflight gate + force-purge
// via service which writes audit + per-galgame revisions).
func (h *EngineHandler) Delete(c fiber.Ctx) error {
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

	rel, err := h.engineRepo.CountReferences(c.Context(), id)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	force := c.Query("force") == "true"
	if rel > 0 && !force {
		return response.BadRequestMsg(c, errors.ErrValidationFailed,
			"该 engine 仍被 "+strconv.FormatInt(rel, 10)+" 个 galgame 引用；如确认要一键清除全部引用并硬删除，请带 ?force=true（仅 role>1）")
	}

	relations, affected, err := h.taxSvc.DeleteEngine(c.Context(), int(userID), roleLevel(roles), id)
	if err != nil {
		return mapAppErrOrInternal(c, err)
	}
	return response.Success(c, fiber.Map{
		"deleted":              true,
		"forced":               force && rel > 0,
		"purged_relations":     relations,
		"affected_galgame_ids": affected,
	})
}
