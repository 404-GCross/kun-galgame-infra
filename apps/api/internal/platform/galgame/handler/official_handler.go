package handler

import (
	"strconv"
	"strings"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/repository"
	"api/internal/platform/galgame/search"
	"api/internal/platform/galgame/service"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// OfficialHandler — read paths through repo (plain queries, no audit
// footprint); mutating paths through TaxonomyService so every change
// writes a taxonomy_revision row + per-affected-galgame galgame_revision
// rows on force-delete. Same pattern as TagHandler.
type OfficialHandler struct {
	officialRepo *repository.OfficialRepository
	taxSvc       *service.TaxonomyService
	searchHook   *search.Hook
}

// NewOfficialHandler creates a new OfficialHandler. Pass nil hook to skip search write-through.
func NewOfficialHandler(officialRepo *repository.OfficialRepository, taxSvc *service.TaxonomyService, hook *search.Hook) *OfficialHandler {
	return &OfficialHandler{officialRepo: officialRepo, taxSvc: taxSvc, searchHook: hook}
}

// List returns a paginated list of officials
func (h *OfficialHandler) List(c fiber.Ctx) error {
	var req dto.ListOfficialRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.Limit <= 0 {
		req.Limit = 50
	}

	items, total, err := h.officialRepo.List(c.Context(), req.Page, req.Limit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{"items": items, "total": total})
}

// GetByName returns an official with its galgames
func (h *OfficialHandler) GetByName(c fiber.Ctx) error {
	var req dto.GetOfficialByNameRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.Limit <= 0 {
		req.Limit = 24
	}

	official, err := h.officialRepo.FindByID(c.Context(), req.OfficialID)
	if err != nil {
		return response.NotFound(c, errors.ErrNotFound)
	}

	galgames, total, err := h.officialRepo.FindGalgamesByOfficialID(c.Context(), req.OfficialID, req.Page, req.Limit, req.SortField, req.SortOrder, req.ContentLimit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{"official": official, "galgames": galgames, "total": total})
}

// Search searches officials by name or alias
func (h *OfficialHandler) Search(c fiber.Ctx) error {
	var req dto.SearchOfficialRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	parts := strings.Split(req.Q, ",")
	var terms []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			terms = append(terms, p)
		}
	}
	if len(terms) == 0 {
		return response.Success(c, []any{})
	}

	officials, err := h.officialRepo.Search(c.Context(), terms)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, officials)
}

// Update — delegates to TaxonomyService (revision-aware).
func (h *OfficialHandler) Update(c fiber.Ctx) error {
	uid, _ := c.Locals("user_uid").(uint)
	if uid == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	roles, _ := c.Locals("user_roles").([]string)
	if !hasRole(roles, "admin", "moderator") {
		return response.Forbidden(c, errors.ErrForbidden)
	}

	var req dto.UpdateOfficialRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	if err := h.taxSvc.UpdateOfficial(c.Context(), int(uid), roleLevel(roles), &req); err != nil {
		return mapAppErrOrInternal(c, err)
	}
	h.searchHook.Official(req.OfficialID)
	return response.Success(c, nil)
}

// Create — delegates to TaxonomyService.
func (h *OfficialHandler) Create(c fiber.Ctx) error {
	uid, _ := c.Locals("user_uid").(uint)
	if uid == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	roles, _ := c.Locals("user_roles").([]string)

	var req dto.CreateOfficialRequest
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

	o, err := h.taxSvc.CreateOfficial(c.Context(), int(uid), roleLevel(roles), &req)
	if err != nil {
		return mapAppErrOrInternal(c, err)
	}
	h.searchHook.Official(o.ID)
	return response.Success(c, o)
}

// Delete — same UX as TagHandler.Delete (preflight gate + force-purge
// via service which writes audit + per-galgame revisions).
func (h *OfficialHandler) Delete(c fiber.Ctx) error {
	uid, _ := c.Locals("user_uid").(uint)
	if uid == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	roles, _ := c.Locals("user_roles").([]string)
	if !hasRole(roles, "admin", "moderator") {
		return response.Forbidden(c, errors.ErrForbidden)
	}

	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	rel, _, err := h.officialRepo.CountReferences(c.Context(), id)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	force := c.Query("force") == "true"
	if rel > 0 && !force {
		return response.BadRequestMsg(c, errors.ErrValidationFailed,
			"该 official 仍被 "+strconv.FormatInt(rel, 10)+" 个 galgame 引用；如确认要一键清除全部引用并硬删除，请带 ?force=true（仅 role>1）")
	}

	relations, aliases, affected, err := h.taxSvc.DeleteOfficial(c.Context(), int(uid), roleLevel(roles), id)
	if err != nil {
		return mapAppErrOrInternal(c, err)
	}
	h.searchHook.OfficialDelete(id)
	return response.Success(c, fiber.Map{
		"deleted":              true,
		"forced":               force && rel > 0,
		"purged_relations":     relations,
		"purged_aliases":       aliases,
		"affected_galgame_ids": affected,
	})
}
