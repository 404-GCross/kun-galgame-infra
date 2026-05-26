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

// TagHandler handles tag HTTP requests. Read paths (List / Search /
// GetByName / Multi) talk directly to the repo because they're plain
// queries with no audit footprint. Mutating paths (Create / Update /
// Delete) go through TaxonomyService so every change writes a
// taxonomy_revision row + (for force-delete) per-affected-galgame
// galgame_revision rows.
type TagHandler struct {
	tagRepo    *repository.TagRepository
	taxSvc     *service.TaxonomyService
	searchHook *search.Hook
}

// NewTagHandler creates a new TagHandler. Pass nil hook to skip search
// write-through. taxSvc is required (mutations go through it).
func NewTagHandler(tagRepo *repository.TagRepository, taxSvc *service.TaxonomyService, hook *search.Hook) *TagHandler {
	return &TagHandler{tagRepo: tagRepo, taxSvc: taxSvc, searchHook: hook}
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

	// Default safe-by-default (sfw) when caller omits content_limit.
	// Pass "all" to opt INTO including NSFW. See handbook §NSFW.
	contentLimit := utils.ParseContentLimit(req.ContentLimit, "sfw")
	galgames, total, err := h.tagRepo.FindGalgamesByTagID(c.Context(), req.TagID, req.Page, req.Limit, req.SortField, req.SortOrder, contentLimit)
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

	// Default safe-by-default (sfw) when caller omits content_limit.
	// Pass "all" to opt INTO including NSFW. See handbook §NSFW.
	contentLimit := utils.ParseContentLimit(req.ContentLimit, "sfw")
	galgames, total, err := h.tagRepo.FindGalgamesByMultipleTags(c.Context(), req.TagIDs, req.Page, req.Limit, contentLimit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{"items": galgames, "total": total})
}

// Update updates a tag (requires role > 2). Delegates to TaxonomyService
// which handles the snapshot-overlay → ApplyTagSnapshot → revision
// write sequence; this handler only binds, validates, and dispatches.
func (h *TagHandler) Update(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
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

	if err := h.taxSvc.UpdateTag(c.Context(), int(userID), roleLevel(roles), &req); err != nil {
		return mapAppErrOrInternal(c, err)
	}
	h.searchHook.Tag(req.TagID)
	return response.Success(c, nil)
}

// Create creates a new tag. Any logged-in user — lets kungal/moyu
// users introduce a tag missing from the wiki for an original / doujin
// work. Service writes the initial taxonomy_revision (action=created).
func (h *TagHandler) Create(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	roles, _ := c.Locals("user_roles").([]string)

	var req dto.CreateTagRequest
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

	tag, err := h.taxSvc.CreateTag(c.Context(), int(userID), roleLevel(roles), &req)
	if err != nil {
		return mapAppErrOrInternal(c, err)
	}
	h.searchHook.Tag(tag.ID)
	return response.Success(c, tag)
}

// Delete removes a tag. Two-step UX preserved: caller without
// ?force=true sees the ref count and must re-confirm. Service handles
// the cascade + records taxonomy_revision (action=deleted) +
// per-galgame galgame_revisions (changed_fields=['tag_ids']).
func (h *TagHandler) Delete(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
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

	rel, _, err := h.tagRepo.CountReferences(c.Context(), id)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	force := c.Query("force") == "true"
	if rel > 0 && !force {
		return response.BadRequestMsg(c, errors.ErrValidationFailed,
			"该 tag 仍被 "+strconv.FormatInt(rel, 10)+" 个 galgame 引用；如确认要一键清除全部引用并硬删除，请带 ?force=true（仅 role>1）")
	}

	relations, aliases, affected, err := h.taxSvc.DeleteTag(c.Context(), int(userID), roleLevel(roles), id)
	if err != nil {
		return mapAppErrOrInternal(c, err)
	}

	h.searchHook.TagDelete(id)
	return response.Success(c, fiber.Map{
		"deleted":          true,
		"forced":           force && rel > 0,
		"purged_relations": relations,
		"purged_aliases":   aliases,
		"affected_galgame_ids": affected,
	})
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
