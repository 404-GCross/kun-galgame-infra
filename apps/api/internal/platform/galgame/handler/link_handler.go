package handler

import (
	"strconv"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"
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
	uid, _ := c.Locals("user_uid").(uint)
	if uid == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}

	gid, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	var req dto.CreateLinkRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	db := h.galgameRepo.DB()

	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&model.GalgameLink{
			GalgameID: gid,
			UserID:    int(uid),
			Name:      req.Name,
			Link:      req.Link,
		}).Error; err != nil {
			return err
		}

		return h.svc.CreateRevisionFromCurrentState(tx, gid, int(uid), "updated", "添加链接: "+req.Name, false)
	})

	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, nil)
}

// DeleteLink deletes a link and creates a revision
func (h *LinkHandler) DeleteLink(c fiber.Ctx) error {
	uid, _ := c.Locals("user_uid").(uint)
	if uid == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}

	gid, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	var req dto.DeleteByIDRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	db := h.galgameRepo.DB()

	err = db.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND galgame_id = ?", req.ID, gid).Delete(&model.GalgameLink{})
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}

		return h.svc.CreateRevisionFromCurrentState(tx, gid, int(uid), "updated", "删除链接", true)
	})

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return response.NotFound(c, errors.ErrNotFound)
		}
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
	uid, _ := c.Locals("user_uid").(uint)
	if uid == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}

	gid, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	var req dto.CreateAliasRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	db := h.galgameRepo.DB()

	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&model.GalgameAlias{
			GalgameID: gid,
			Name:      req.Name,
		}).Error; err != nil {
			return err
		}

		return h.svc.CreateRevisionFromCurrentState(tx, gid, int(uid), "updated", "添加别名: "+req.Name, false)
	})

	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, nil)
}

// DeleteAlias deletes an alias and creates a revision
func (h *LinkHandler) DeleteAlias(c fiber.Ctx) error {
	uid, _ := c.Locals("user_uid").(uint)
	if uid == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}

	gid, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	var req dto.DeleteByIDRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	db := h.galgameRepo.DB()

	err = db.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND galgame_id = ?", req.ID, gid).Delete(&model.GalgameAlias{})
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}

		return h.svc.CreateRevisionFromCurrentState(tx, gid, int(uid), "updated", "删除别名", true)
	})

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return response.NotFound(c, errors.ErrNotFound)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, nil)
}
