package handler

import (
	"strconv"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/editspec"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/perm"
	"api/internal/platform/galgame/repository"
	"api/internal/platform/galgame/service"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// LinkHandler handles link and alias HTTP requests
type LinkHandler struct {
	svc         *service.GalgameService
	galgameRepo *repository.GalgameRepository
}

// NewLinkHandler creates a new LinkHandler
func NewLinkHandler(svc *service.GalgameService, galgameRepo *repository.GalgameRepository) *LinkHandler {
	return &LinkHandler{svc: svc, galgameRepo: galgameRepo}
}

// authorizeGalgameEdit gates link/alias mutations to the galgame's owner or
// an admin — mirroring GalgameService.Update / contributor-delete. Returns a
// non-nil response to short-circuit (404 when the galgame doesn't exist,
// 403 when the caller isn't owner/admin) or nil to proceed. This also closes
// the "create on a non-existent gid → 500" gap (the FK violation never fires
// because the missing gid 404s here first).
func (h *LinkHandler) authorizeGalgameEdit(c fiber.Ctx, gid int, userID uint) error {
	roles, _ := c.Locals("user_roles").([]string)
	var g model.Galgame
	if err := h.galgameRepo.DB().Select("user_id").First(&g, gid).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return response.NotFound(c, errors.ErrGalgameNotFound)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	if g.UserID != int(userID) && !perm.Resolver.Can(roles, perm.OwnerOverride) {
		return response.Forbidden(c, errors.ErrGalgameForbidden)
	}
	return nil
}

// ListLinks returns links for a galgame
func (h *LinkHandler) ListLinks(c fiber.Ctx) error {
	gid, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	var links []model.GalgameLink
	if err := h.galgameRepo.DB().Where("galgame_id = ?", gid).Order("created ASC").Find(&links).Error; err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, links)
}

// CreateLink adds a new link and creates a revision
func (h *LinkHandler) CreateLink(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}

	gid, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	if resp := h.authorizeGalgameEdit(c, gid, userID); resp != nil {
		return resp
	}

	var req dto.CreateLinkRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	// E2a: the desired links list goes through the engine as a direct edit
	// ({links: current + new}); the registered Apply rebuilds the rows (the
	// new row's owner = the acting user via the engine's actor context).
	current, err := h.currentLinks(gid)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	desired := append(current, model.SnapshotLink{Name: req.Name, Link: req.Link})
	if err := h.svc.DirectFieldEdit(c.Context(), gid, int(userID),
		editspec.FieldLinks, desired, "添加链接: "+req.Name); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, nil)
}

// currentLinks loads the live link rows in snapshot form (insertion order,
// matching LoadSnapshot's ORDER BY id ASC).
func (h *LinkHandler) currentLinks(gid int) ([]model.SnapshotLink, error) {
	var rows []model.GalgameLink
	if err := h.galgameRepo.DB().Where("galgame_id = ?", gid).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.SnapshotLink, 0, len(rows))
	for _, l := range rows {
		out = append(out, model.SnapshotLink{Name: l.Name, Link: l.Link, Source: l.Source, SourceKey: l.SourceKey})
	}
	return out, nil
}

// DeleteLink deletes a link and creates a revision
func (h *LinkHandler) DeleteLink(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}

	gid, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	if resp := h.authorizeGalgameEdit(c, gid, userID); resp != nil {
		return resp
	}

	var req dto.DeleteByIDRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	// Resolve the target row first (404 contract preserved), then land the
	// shrunken list through the engine.
	var target model.GalgameLink
	if err := h.galgameRepo.DB().Where("id = ? AND galgame_id = ?", req.ID, gid).
		First(&target).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return response.NotFound(c, errors.ErrNotFound)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	var rows []model.GalgameLink
	if err := h.galgameRepo.DB().Where("galgame_id = ?", gid).Order("id ASC").Find(&rows).Error; err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	desired := make([]model.SnapshotLink, 0, len(rows))
	for _, l := range rows {
		if l.ID == target.ID {
			continue
		}
		desired = append(desired, model.SnapshotLink{Name: l.Name, Link: l.Link, Source: l.Source, SourceKey: l.SourceKey})
	}
	if err := h.svc.DirectFieldEdit(c.Context(), gid, int(userID),
		editspec.FieldLinks, desired, "删除链接"); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, nil)
}

// ListAliases returns aliases for a galgame
func (h *LinkHandler) ListAliases(c fiber.Ctx) error {
	gid, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	var aliases []model.GalgameAlias
	if err := h.galgameRepo.DB().Where("galgame_id = ?", gid).Order("created ASC").Find(&aliases).Error; err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, aliases)
}

// CreateAlias adds a new alias and creates a revision
func (h *LinkHandler) CreateAlias(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}

	gid, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	if resp := h.authorizeGalgameEdit(c, gid, userID); resp != nil {
		return resp
	}

	var req dto.CreateAliasRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	current, err := h.currentAliases(gid)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	desired := append(current, req.Name)
	if err := h.svc.DirectFieldEdit(c.Context(), gid, int(userID),
		editspec.FieldAliases, desired, "添加别名: "+req.Name); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, nil)
}

// currentAliases loads the live alias names.
func (h *LinkHandler) currentAliases(gid int) ([]string, error) {
	var rows []model.GalgameAlias
	if err := h.galgameRepo.DB().Where("galgame_id = ?", gid).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, a := range rows {
		out = append(out, a.Name)
	}
	return out, nil
}

// DeleteAlias deletes an alias and creates a revision
func (h *LinkHandler) DeleteAlias(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}

	gid, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	if resp := h.authorizeGalgameEdit(c, gid, userID); resp != nil {
		return resp
	}

	var req dto.DeleteByIDRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	var target model.GalgameAlias
	if err := h.galgameRepo.DB().Where("id = ? AND galgame_id = ?", req.ID, gid).
		First(&target).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return response.NotFound(c, errors.ErrNotFound)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	var rows []model.GalgameAlias
	if err := h.galgameRepo.DB().Where("galgame_id = ?", gid).Order("id ASC").Find(&rows).Error; err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	desired := make([]string, 0, len(rows))
	for _, a := range rows {
		if a.ID == target.ID {
			continue
		}
		desired = append(desired, a.Name)
	}
	if err := h.svc.DirectFieldEdit(c.Context(), gid, int(userID),
		editspec.FieldAliases, desired, "删除别名"); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, nil)
}
