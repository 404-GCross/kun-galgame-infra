package permissions

import (
	"errors"
	"log/slog"
	"strconv"

	"api/internal/platform/authz"
	apperrors "api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// Handler serves the permission console under /api/v1/admin/permissions.
type Handler struct {
	svc *Service
}

// NewHandler wires the console handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the console on an admin router that ALREADY carries
// middleware.Auth + oauth.admin_access — the same page gate as the rest of the
// console. The write endpoints are deliberately NOT gated on
// oauth.permissions.manage at the route: an ordinary admin may legitimately
// delegate downward, and which cells they may touch is a per-cell decision the
// service makes. Gating the route on the ren key would have made the delegation
// rule unreachable.
func (h *Handler) Register(admin fiber.Router) {
	g := admin.Group("/permissions")
	g.Get("/matrix", h.Matrix)
	g.Get("/audit", h.Audit)
	g.Post("/overrides", h.Add)
	g.Delete("/overrides", h.Remove)

	slog.Info("permission console registered under /api/v1/admin/permissions/*")
}

// callerFrom reads the authenticated identity the JWT middleware stored.
func callerFrom(c fiber.Ctx) Caller {
	roles, _ := c.Locals("user_roles").([]string)
	id, _ := c.Locals("user_id").(uint)
	return Caller{UserID: id, Roles: roles}
}

// Matrix returns every live domain's keys with the per-role effective grant and
// this caller's editable cells.
func (h *Handler) Matrix(c fiber.Ctx) error {
	m, err := h.svc.Matrix(c.Context(), callerFrom(c))
	if err != nil {
		slog.Error("permissions: matrix build failed", "err", err)
		return response.InternalError(c, apperrors.ErrOperationFailed)
	}
	return response.Success(c, m)
}

// Audit returns the newest permission-change rows.
func (h *Handler) Audit(c fiber.Ctx) error {
	limit, _ := strconv.Atoi(c.Query("limit"))
	entries, err := h.svc.Audit(c.Context(), limit)
	if err != nil {
		slog.Error("permissions: audit read failed", "err", err)
		return response.InternalError(c, apperrors.ErrOperationFailed)
	}
	return response.Success(c, entries)
}

// overrideRequest is the body of the insert endpoint — one overlay cell plus
// the effect being written.
type overrideRequest struct {
	Role       string `json:"role"`
	Permission string `json:"permission"`
	// Effect is "grant" or "deny". Empty means "grant", which keeps the body
	// this endpoint accepted before deny rows existed working unchanged.
	Effect string `json:"effect"`
}

// Add writes an overlay row — a grant or a deny — from a JSON body.
func (h *Handler) Add(c fiber.Ctx) error {
	var req overrideRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, apperrors.ErrBadRequest)
	}
	if req.Effect == "" {
		req.Effect = EffectGrant
	}
	if req.Effect != EffectGrant && req.Effect != EffectDeny {
		return response.BadRequest(c, apperrors.ErrBadRequest)
	}
	return h.write(c, WriteRequest{
		Add:        true,
		Effect:     req.Effect,
		Role:       req.Role,
		Permission: authz.Permission(req.Permission),
	})
}

// Remove deletes whichever overlay row the cell has — a grant (back to the code
// floor) or a deny (restoring the code floor). It names the cell in the QUERY
// rather than a body: a DELETE with a request body is unevenly supported across
// the stack (the admin console's own HTTP client sends none), and the pair
// (role, permission) is short enough to be a URL. It deliberately does NOT take
// an effect: the row that is there is the row being removed, and letting the
// client assert which one invites deleting a deny while believing it revoked a
// grant.
func (h *Handler) Remove(c fiber.Ctx) error {
	return h.write(c, WriteRequest{
		Role:       c.Query("role"),
		Permission: authz.Permission(c.Query("permission")),
	})
}

func (h *Handler) write(c fiber.Ctx, req WriteRequest) error {
	if req.Role == "" || req.Permission == "" {
		return response.BadRequest(c, apperrors.ErrMissingParam)
	}

	err := h.svc.Apply(c.Context(), callerFrom(c), req)
	switch {
	case err == nil:
		return response.Success(c, nil)
	case isRejection(err):
		// A rule said no. 403 rather than 400: the request was well-formed and
		// names real things — the caller simply may not make this change.
		return response.ForbiddenMsg(c, apperrors.ErrForbidden, err.Error())
	case errors.Is(err, ErrAlreadyGranted), errors.Is(err, ErrNotGranted):
		// Lost a race with a concurrent write; the matrix the caller acted on
		// is stale.
		return response.BadRequestMsg(c, apperrors.ErrOperationFailed, "权限矩阵已被其他人修改,请刷新后重试")
	default:
		slog.Error("permissions: overlay write failed", "role", req.Role, "permission", req.Permission, "err", err)
		return response.InternalError(c, apperrors.ErrOperationFailed)
	}
}

func isRejection(err error) bool {
	var rej *RejectionError
	return errors.As(err, &rej)
}
