package handler

import (
	"strings"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/service"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// MessageHandler serves message endpoints: /messages/mine, /messages/feed
// (cron), /admin/galgame/messages (admin queue).
type MessageHandler struct {
	messageSvc *service.MessageService
}

// NewMessageHandler creates a MessageHandler.
func NewMessageHandler(svc *service.MessageService) *MessageHandler {
	return &MessageHandler{messageSvc: svc}
}

// ListMine handles GET /galgame/messages/mine.
func (h *MessageHandler) ListMine(c fiber.Ctx) error {
	uid, _ := c.Locals("user_uid").(uint)
	if uid == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}

	var req dto.ListMineMessagesRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	items, total, err := h.messageSvc.ListMine(c.Context(), int(uid), req.SinceID, req.Limit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, fiber.Map{
		"items": items,
		"total": total,
	})
}

// ListFeed handles GET /galgame/messages/feed (OAuth Client Basic Auth).
// Returns has_more flag for cron cursor pagination.
func (h *MessageHandler) ListFeed(c fiber.Ctx) error {
	var req dto.FeedMessagesRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	resp, err := h.messageSvc.ListFeed(c.Context(), req.SinceID, req.Limit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, resp)
}

// ListAdminQueue handles GET /admin/galgame/messages.
func (h *MessageHandler) ListAdminQueue(c fiber.Ctx) error {
	var req dto.AdminQueueRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	var types []string
	if req.Type != "" {
		for _, t := range strings.Split(req.Type, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				types = append(types, t)
			}
		}
	}
	items, total, err := h.messageSvc.ListAdminQueue(c.Context(), types, req.Page, req.Limit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, fiber.Map{
		"items": items,
		"total": total,
	})
}
