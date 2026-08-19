package devapi

import (
	goerrors "errors"

	apperr "api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

type scopeApplicationRequest struct {
	Scope   string `json:"scope"`
	Message string `json:"message"`
}

type scopeApplicationView struct {
	ID            uint   `json:"id"`
	Scope         string `json:"scope"`
	Message       string `json:"message"`
	Status        string `json:"status"`
	DeclineReason string `json:"decline_reason"`
	CreatedAt     string `json:"created_at"`
	ReviewedAt    string `json:"reviewed_at,omitempty"`
}

func (h *SelfServiceHandler) ApplyForScope(c fiber.Ctx) error {
	ownerID, ok := ownerFromCtx(c)
	if !ok {
		return response.Unauthorized(c, apperr.ErrAuthUnauthorized)
	}
	var req scopeApplicationRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, apperr.ErrBadRequest)
	}
	app, err := h.svc.ApplyForScope(c.Context(), ownerID, req.Scope, req.Message)
	if resp, handled := selfServicePolicyError(c, err); handled {
		return resp
	}
	if msg, conflict := scopeApplicationConflict(err); conflict {
		return response.Error(c, fiber.StatusConflict, apperr.ErrValidationFailed, msg)
	}
	if msg, bad := selfServiceBadRequest(err); bad {
		return response.BadRequestMsg(c, apperr.ErrValidationFailed, msg)
	}
	if err != nil {
		return response.InternalError(c, apperr.ErrOperationFailed)
	}
	return response.Success(c, toScopeApplicationView(app))
}

func (h *SelfServiceHandler) ListScopeApplications(c fiber.Ctx) error {
	ownerID, ok := ownerFromCtx(c)
	if !ok {
		return response.Unauthorized(c, apperr.ErrAuthUnauthorized)
	}
	apps, err := h.svc.ListScopeApplications(c.Context(), ownerID)
	if err != nil {
		return response.InternalError(c, apperr.ErrOperationFailed)
	}
	out := make([]scopeApplicationView, len(apps))
	for i := range apps {
		out[i] = toScopeApplicationView(&apps[i])
	}
	return response.Success(c, out)
}

func toScopeApplicationView(app *ScopeApplication) scopeApplicationView {
	v := scopeApplicationView{
		ID:            app.ID,
		Scope:         app.Scope,
		Message:       app.Message,
		Status:        app.Status,
		DeclineReason: app.DeclineReason,
		CreatedAt:     app.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if app.ReviewedAt != nil {
		v.ReviewedAt = app.ReviewedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return v
}

func scopeApplicationConflict(err error) (string, bool) {
	switch {
	case goerrors.Is(err, ErrScopeAppPending):
		return "an application for this scope is already awaiting review", true
	case goerrors.Is(err, ErrScopeAppApproved):
		return "this scope is already granted — tick it when you mint a key", true
	default:
		return "", false
	}
}
