package handler

import (
	"strconv"
	"strings"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/repository"
	"api/internal/platform/galgame/search"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// OfficialHandler handles official HTTP requests
type OfficialHandler struct {
	officialRepo *repository.OfficialRepository
	searchHook   *search.Hook
}

// NewOfficialHandler creates a new OfficialHandler. Pass nil hook to skip search write-through.
func NewOfficialHandler(officialRepo *repository.OfficialRepository, hook *search.Hook) *OfficialHandler {
	return &OfficialHandler{officialRepo: officialRepo, searchHook: hook}
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

// Update updates an official (requires role > 2)
func (h *OfficialHandler) Update(c fiber.Ctx) error {
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

	updates := map[string]any{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Link != nil {
		updates["link"] = *req.Link
	}
	if req.Category != nil {
		updates["category"] = *req.Category
	}
	if req.Lang != nil {
		updates["lang"] = *req.Lang
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}

	if err := h.officialRepo.Update(c.Context(), req.OfficialID, updates, req.Alias); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	h.searchHook.Official(req.OfficialID)

	return response.Success(c, nil)
}

// Create creates a new official (any logged-in user — lets kungal/moyu
// users introduce a company/circle missing from the wiki).
func (h *OfficialHandler) Create(c fiber.Ctx) error {
	var req dto.CreateOfficialRequest
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
	exists, err := h.officialRepo.ExistsByName(c.Context(), name)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	if exists {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, "已存在同名 official")
	}

	official := &model.GalgameOfficial{
		Name:        name,
		Category:    req.Category,
		Original:    req.Original,
		Link:        req.Link,
		Lang:        req.Lang,
		Description: req.Description,
	}
	if err := h.officialRepo.Create(c.Context(), official, req.Alias); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	h.searchHook.Official(official.ID)
	return response.Success(c, official)
}

// Delete removes an official (requires admin/moderator). Cascades
// aliases + galgame_official_relation rows.
func (h *OfficialHandler) Delete(c fiber.Ctx) error {
	roles, _ := c.Locals("user_roles").([]string)
	if !hasRole(roles, "admin", "moderator") {
		return response.Forbidden(c, errors.ErrForbidden)
	}

	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	if err := h.officialRepo.Delete(c.Context(), id); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	h.searchHook.OfficialDelete(id)
	return response.Success(c, nil)
}
