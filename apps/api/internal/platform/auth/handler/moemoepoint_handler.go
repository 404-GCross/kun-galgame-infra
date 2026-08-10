package handler

import (
	"strconv"

	"api/internal/middleware"
	"api/internal/platform/auth/model"
	"api/internal/platform/auth/service"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

type MoemoepointHandler struct {
	svc *service.MoemoepointService
}

func NewMoemoepointHandler(svc *service.MoemoepointService) *MoemoepointHandler {
	return &MoemoepointHandler{svc: svc}
}

type adjustRequest struct {
	Delta          int    `json:"delta"`
	Reason         string `json:"reason"`
	Ref            string `json:"ref"`
	ActorUserID    uint   `json:"actor_user_id"`
	IdempotencyKey string `json:"idempotency_key"`
	Note           string `json:"note"`
}

func (h *MoemoepointHandler) Adjust(c fiber.Ctx) error {
	userID, err := parseUintParam(c, "id")
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	client := middleware.OAuthClientFromCtx(c)
	if client == nil {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	if !client.MoemoepointAwarder {
		return response.Forbidden(c, errors.ErrMoemoepointNotAwarder)
	}

	var req adjustRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	switch req.Reason {
	case model.MoemoepointReasonAdminGrant, model.MoemoepointReasonAdminDeduct,
		model.MoemoepointReasonMigration, model.MoemoepointReasonRegisterGift:
		return response.BadRequest(c, errors.ErrMoemoepointInvalidReason)
	}

	res, err := h.svc.Adjust(c.Context(), service.AdjustParams{
		UserID:         userID,
		Delta:          req.Delta,
		Reason:         req.Reason,
		SourceApp:      client.ID,
		Ref:            req.Ref,
		ActorUserID:    req.ActorUserID,
		IdempotencyKey: req.IdempotencyKey,
		Note:           req.Note,
	})
	if err != nil {
		return respondAdjustErr(c, err)
	}
	return response.Success(c, fiber.Map{
		"user_id": userID, "balance": res.Balance, "applied": res.Applied,
	})
}

func (h *MoemoepointHandler) GetBalance(c fiber.Ctx) error {
	userID, err := parseUintParam(c, "id")
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	balance, err := h.svc.GetBalance(c.Context(), userID)
	if err != nil {
		return respondAdjustErr(c, err)
	}
	return response.Success(c, fiber.Map{"user_id": userID, "balance": balance})
}

func (h *MoemoepointHandler) GetLog(c fiber.Ctx) error {
	userID, err := parseUintParam(c, "id")
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	return h.respondLog(c, userID, false)
}

func (h *MoemoepointHandler) MyLog(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	return h.respondLog(c, userID, false)
}

type adminAdjustRequest struct {
	Delta          int    `json:"delta"`
	Note           string `json:"note"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (h *MoemoepointHandler) AdminAdjust(c fiber.Ctx) error {
	userID, err := h.svc.UserIDByUUID(c.Context(), c.Params("uuid"))
	if err != nil {
		return response.NotFound(c, errors.ErrAuthUserNotFound)
	}
	adminID, _ := c.Locals("user_id").(uint)

	var req adminAdjustRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	reason := model.MoemoepointReasonAdminGrant
	if req.Delta < 0 {
		reason = model.MoemoepointReasonAdminDeduct
	}

	res, err := h.svc.Adjust(c.Context(), service.AdjustParams{
		UserID:         userID,
		Delta:          req.Delta,
		Reason:         reason,
		SourceApp:      "oauth",
		Ref:            "admin:" + strconv.FormatUint(uint64(adminID), 10),
		ActorUserID:    adminID,
		IdempotencyKey: req.IdempotencyKey,
		Note:           req.Note,
	})
	if err != nil {
		return respondAdjustErr(c, err)
	}
	return response.Success(c, fiber.Map{
		"user_id": userID, "balance": res.Balance, "applied": res.Applied,
	})
}

func (h *MoemoepointHandler) AdminGetLog(c fiber.Ctx) error {
	userID, err := h.svc.UserIDByUUID(c.Context(), c.Params("uuid"))
	if err != nil {
		return response.NotFound(c, errors.ErrAuthUserNotFound)
	}
	return h.respondLog(c, userID, true)
}

func (h *MoemoepointHandler) respondLog(c fiber.Ctx, userID uint, full bool) error {
	limit, _ := strconv.Atoi(c.Query("limit"))
	beforeID, _ := strconv.ParseInt(c.Query("before_id"), 10, 64)
	rows, hasMore, err := h.svc.GetLog(c.Context(), userID, limit, beforeID, c.Query("reason"))
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	apps := make([]string, len(rows))
	for i, r := range rows {
		apps[i] = r.SourceApp
	}
	names := h.svc.SourceNames(c.Context(), apps)
	items := make([]fiber.Map, len(rows))
	for i, r := range rows {
		m := fiber.Map{
			"id": r.ID, "delta": r.Delta, "reason": r.Reason,
			"source_app": r.SourceApp, "source_name": names[r.SourceApp],
			"ref": r.Ref, "created_at": r.CreatedAt,
		}
		if full {
			m["note"] = r.Note
			m["actor_user_id"] = r.ActorUserID
		}
		items[i] = m
	}
	return response.Success(c, fiber.Map{"items": items, "has_more": hasMore})
}

func parseUintParam(c fiber.Ctx, name string) (uint, error) {
	n, err := strconv.ParseUint(c.Params(name), 10, 64)
	if err != nil {
		return 0, err
	}
	return uint(n), nil
}

func respondAdjustErr(c fiber.Ctx, err error) error {
	if appErr, ok := err.(*errors.AppError); ok {
		switch appErr.Code {
		case errors.ErrAuthUserNotFound:
			return response.NotFound(c, appErr.Code)
		case errors.ErrMoemoepointInvalidDelta, errors.ErrMoemoepointInvalidReason,
			errors.ErrMoemoepointIdemConflict, errors.ErrMissingParam:
			return response.BadRequest(c, appErr.Code)
		}
	}
	return response.InternalError(c, errors.ErrOperationFailed)
}
