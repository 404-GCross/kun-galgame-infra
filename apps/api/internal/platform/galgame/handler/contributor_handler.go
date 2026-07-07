package handler

import (
	"strconv"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/perm"
	"api/internal/platform/galgame/repository"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// ContributorHandler handles contributor HTTP requests
type ContributorHandler struct {
	galgameRepo *repository.GalgameRepository
	userRepo    *repository.UserReadonlyRepository
}

// NewContributorHandler creates a new ContributorHandler
func NewContributorHandler(galgameRepo *repository.GalgameRepository, userRepo *repository.UserReadonlyRepository) *ContributorHandler {
	return &ContributorHandler{galgameRepo: galgameRepo, userRepo: userRepo}
}

// List returns contributors for a galgame with user info
func (h *ContributorHandler) List(c fiber.Ctx) error {
	gid, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	var contributors []model.GalgameContributor
	if err := h.galgameRepo.DB().Where("galgame_id = ?", gid).Order("created ASC").Find(&contributors).Error; err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	// Batch lookup user info
	userIDs := make([]int, len(contributors))
	for i, c := range contributors {
		userIDs[i] = c.UserID
	}

	users, err := h.userRepo.FindByIDs(c.Context(), userIDs)
	if err != nil {
		users = make(map[int]*dto.UserBrief)
	}

	type ContributorWithUser struct {
		model.GalgameContributor
		User *dto.UserBrief `json:"user,omitempty"`
	}

	result := make([]ContributorWithUser, len(contributors))
	for i, contrib := range contributors {
		result[i] = ContributorWithUser{
			GalgameContributor: contrib,
			User:               users[contrib.UserID],
		}
	}

	return response.Success(c, result)
}

// Delete removes a contributor (requires galgame creator or admin)
func (h *ContributorHandler) Delete(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	roles, _ := c.Locals("user_roles").([]string)

	gid, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	targetUserID, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	// Check permission: galgame creator or admin
	var galgame model.Galgame
	if err := h.galgameRepo.DB().Select("user_id").First(&galgame, gid).Error; err != nil {
		return response.NotFound(c, errors.ErrGalgameNotFound)
	}
	if galgame.UserID != int(userID) && !perm.Resolver.Can(roles, perm.OwnerOverride) {
		return response.Forbidden(c, errors.ErrGalgameForbidden)
	}

	result := h.galgameRepo.DB().
		Where("galgame_id = ? AND user_id = ?", gid, targetUserID).
		Delete(&model.GalgameContributor{})
	if result.RowsAffected == 0 {
		return response.NotFound(c, errors.ErrNotFound)
	}

	return response.Success(c, nil)
}
