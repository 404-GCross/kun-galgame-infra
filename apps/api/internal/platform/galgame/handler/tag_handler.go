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

// TagHandler handles tag HTTP requests
type TagHandler struct {
	tagRepo    *repository.TagRepository
	searchHook *search.Hook
}

// NewTagHandler creates a new TagHandler. Pass nil hook to skip search write-through.
func NewTagHandler(tagRepo *repository.TagRepository, hook *search.Hook) *TagHandler {
	return &TagHandler{tagRepo: tagRepo, searchHook: hook}
}

// List returns a paginated list of tags
func (h *TagHandler) List(c fiber.Ctx) error {
	var req dto.ListTagRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.Limit <= 0 {
		req.Limit = 50
	}

	items, total, err := h.tagRepo.List(c.Context(), req.Page, req.Limit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{"items": items, "total": total})
}

// GetByName returns a tag with its associated galgames
func (h *TagHandler) GetByName(c fiber.Ctx) error {
	var req dto.GetTagByNameRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.Limit <= 0 {
		req.Limit = 24
	}

	tag, err := h.tagRepo.FindByID(c.Context(), req.TagID)
	if err != nil {
		return response.NotFound(c, errors.ErrNotFound)
	}

	galgames, total, err := h.tagRepo.FindGalgamesByTagID(c.Context(), req.TagID, req.Page, req.Limit, req.SortField, req.SortOrder, req.ContentLimit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{"tag": tag, "galgames": galgames, "total": total})
}

// Search searches tags by name or alias
func (h *TagHandler) Search(c fiber.Ctx) error {
	var req dto.SearchTagRequest
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

	tags, err := h.tagRepo.Search(c.Context(), terms)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, tags)
}

// Multi returns galgames matching all specified tag IDs
func (h *TagHandler) Multi(c fiber.Ctx) error {
	var req dto.MultiTagRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.Limit <= 0 {
		req.Limit = 24
	}
	if len(req.TagIDs) == 0 {
		return response.Success(c, fiber.Map{"items": []any{}, "total": 0})
	}

	galgames, total, err := h.tagRepo.FindGalgamesByMultipleTags(c.Context(), req.TagIDs, req.Page, req.Limit, req.ContentLimit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{"items": galgames, "total": total})
}

// Update updates a tag (requires role > 2)
func (h *TagHandler) Update(c fiber.Ctx) error {
	roles, _ := c.Locals("user_roles").([]string)
	if !hasRole(roles, "admin", "moderator") {
		return response.Forbidden(c, errors.ErrForbidden)
	}

	var req dto.UpdateTagRequest
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
	if req.Category != nil {
		updates["category"] = *req.Category
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}

	if err := h.tagRepo.Update(c.Context(), req.TagID, updates, req.Alias); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	h.searchHook.Tag(req.TagID)

	return response.Success(c, nil)
}

// Create creates a new tag. Any logged-in user — this is what lets
// kungal/moyu users introduce a tag missing from the wiki for an
// original / doujin work with no VNDB entry (mirrors series Create).
func (h *TagHandler) Create(c fiber.Ctx) error {
	var req dto.CreateTagRequest
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
	exists, err := h.tagRepo.ExistsByName(c.Context(), name)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	if exists {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, "已存在同名 tag")
	}

	tag := &model.GalgameTag{
		Name:        name,
		Category:    req.Category,
		Description: req.Description,
	}
	if err := h.tagRepo.Create(c.Context(), tag, req.Alias); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	h.searchHook.Tag(tag.ID)
	return response.Success(c, tag)
}

// Delete removes a tag (requires admin/moderator). Cascades aliases +
// galgame_tag_relation rows so no dangling references remain.
func (h *TagHandler) Delete(c fiber.Ctx) error {
	roles, _ := c.Locals("user_roles").([]string)
	if !hasRole(roles, "admin", "moderator") {
		return response.Forbidden(c, errors.ErrForbidden)
	}

	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	if err := h.tagRepo.Delete(c.Context(), id); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	h.searchHook.TagDelete(id)
	return response.Success(c, nil)
}

// hasRole checks if the user has any of the specified roles
func hasRole(roles []string, allowed ...string) bool {
	for _, r := range roles {
		for _, a := range allowed {
			if r == a {
				return true
			}
		}
	}
	return false
}
