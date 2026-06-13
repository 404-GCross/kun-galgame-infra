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

// MoemoepointHandler exposes the unified moemoepoint balance: a service-to-
// service adjust/read surface for downstream apps (OAuth Client Basic Auth,
// by numeric user id) and an admin grant/deduct + log surface (admin JWT, by
// uuid). All changes flow through MoemoepointService.Adjust (idempotent).
type MoemoepointHandler struct {
	svc *service.MoemoepointService
}

// NewMoemoepointHandler creates a MoemoepointHandler.
func NewMoemoepointHandler(svc *service.MoemoepointService) *MoemoepointHandler {
	return &MoemoepointHandler{svc: svc}
}

// ── service-to-service (Basic Auth, /users/:id/moemoepoint) ──────────────

type adjustRequest struct {
	Delta          int    `json:"delta"`
	Reason         string `json:"reason"`
	Ref            string `json:"ref"`
	ActorUserID    uint   `json:"actor_user_id"`
	IdempotencyKey string `json:"idempotency_key"`
	Note           string `json:"note"`
}

// Adjust handles POST /users/:id/moemoepoint. source_app is derived from the
// authenticated OAuth client (never trusted from the body).
func (h *MoemoepointHandler) Adjust(c fiber.Ctx) error {
	userID, err := parseUintParam(c, "id")
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	client := middleware.OAuthClientFromCtx(c)
	if client == nil {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	// Minting into the shared moemoepoint wallet is allow-listed: only clients
	// flagged MoemoepointAwarder may award. Reading the balance (GetBalance)
	// stays open to any registered client, and admin grants go through the
	// admin-JWT path — neither hits this gate. Fail-closed by default so a
	// new client (e.g. a content site that only seeds a local economy from
	// the balance) can't write to the shared ledger. See
	// docs/integration/oauth/06-moemoepoint.md §awarder allow-list.
	if !client.MoemoepointAwarder {
		return response.Forbidden(c, errors.ErrMoemoepointNotAwarder)
	}

	var req adjustRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	// admin_grant / admin_deduct / migration are OAuth-internal — a
	// downstream client must not self-label points with them (audit clarity;
	// can't fake a manual/admin grant). Downstream awards use the
	// content_/daily_checkin/liked reasons.
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

// GetBalance handles GET /users/:id/moemoepoint.
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

// GetLog handles GET /users/:id/moemoepoint/log (service-to-service). Returns
// the REDUCED view (no note / actor) — downstream may render this to end
// users, so admin notes (e.g. moderation reasons) must not leak.
func (h *MoemoepointHandler) GetLog(c fiber.Ctx) error {
	userID, err := parseUintParam(c, "id")
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	return h.respondLog(c, userID, false)
}

// ── self-service (user JWT, /auth/me/moemoepoint/log) ────────────────────

// MyLog handles GET /api/v1/auth/me/moemoepoint/log — the authenticated user
// reading their OWN ledger. Returns the REDUCED view (no note / actor): a user
// must not see admin notes (e.g. moderation reasons) attached to their rows —
// only the admin full-view endpoint exposes those. The user id comes from the
// JWT, never a path param, so one user can't read another's log.
func (h *MoemoepointHandler) MyLog(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	return h.respondLog(c, userID, false)
}

// ── admin (JWT, /admin/users/:uuid/moemoepoint) ──────────────────────────

type adminAdjustRequest struct {
	// Delta is signed: positive = grant, negative = deduct. The reason is
	// derived from the sign so the admin UI only sends a number + note.
	Delta          int    `json:"delta"`
	Note           string `json:"note"`
	IdempotencyKey string `json:"idempotency_key"`
}

// AdminAdjust handles POST /admin/users/:uuid/moemoepoint (admin grant/deduct).
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

// AdminGetLog handles GET /admin/users/:uuid/moemoepoint/log (admin). Returns
// the FULL view including note + actor (admins are trusted with it).
func (h *MoemoepointHandler) AdminGetLog(c fiber.Ctx) error {
	userID, err := h.svc.UserIDByUUID(c.Context(), c.Params("uuid"))
	if err != nil {
		return response.NotFound(c, errors.ErrAuthUserNotFound)
	}
	return h.respondLog(c, userID, true)
}

// ── shared ───────────────────────────────────────────────────────────────

// respondLog renders a user's ledger. full=true (admin) includes note +
// actor_user_id; full=false (service-to-service / end-user facing) omits them
// so moderation notes never leak downstream.
func (h *MoemoepointHandler) respondLog(c fiber.Ctx, userID uint, full bool) error {
	limit, _ := strconv.Atoi(c.Query("limit"))
	beforeID, _ := strconv.ParseInt(c.Query("before_id"), 10, 64)
	rows, hasMore, err := h.svc.GetLog(c.Context(), userID, limit, beforeID, c.Query("reason"))
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	if full {
		return response.Success(c, fiber.Map{"items": rows, "has_more": hasMore})
	}
	items := make([]fiber.Map, len(rows))
	for i, r := range rows {
		items[i] = fiber.Map{
			"id": r.ID, "delta": r.Delta, "reason": r.Reason,
			"source_app": r.SourceApp, "ref": r.Ref, "created_at": r.CreatedAt,
		}
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
