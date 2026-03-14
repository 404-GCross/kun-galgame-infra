package handler

import (
	"strconv"

	"api/internal/platform/moderation/model"
	"api/internal/platform/moderation/service"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// ModerationHandler handles moderation requests
type ModerationHandler struct {
	moderationService *service.ModerationService
}

// NewModerationHandler creates a new ModerationHandler
func NewModerationHandler(moderationService *service.ModerationService) *ModerationHandler {
	return &ModerationHandler{moderationService: moderationService}
}

// ListJobs lists moderation jobs
func (h *ModerationHandler) ListJobs(c fiber.Ctx) error {
	var p utils.Pagination
	if err := c.Bind().Query(&p); err != nil {
		p = utils.DefaultPagination()
	}

	result, err := h.moderationService.ListJobs(c.Context(), p)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, result)
}

// GetJob gets a job by ID
func (h *ModerationHandler) GetJob(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	job, err := h.moderationService.GetJob(c.Context(), uint(id))
	if err != nil {
		return response.NotFound(c, errors.ErrModerationNotFound)
	}

	return response.Success(c, job)
}

// ManualReview performs manual review
func (h *ModerationHandler) ManualReview(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	var req struct {
		Decision string `json:"decision" validate:"required,oneof=approved rejected"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	reviewerUUID := c.Locals("user_uuid").(string)

	if err := h.moderationService.ManualReview(c.Context(), uint(id), model.Decision(req.Decision), reviewerUUID); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, nil)
}

// ListPolicies lists moderation policies
func (h *ModerationHandler) ListPolicies(c fiber.Ctx) error {
	// TODO: implement
	return response.Success(c, nil)
}
