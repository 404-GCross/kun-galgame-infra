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

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Register(admin fiber.Router) {
	g := admin.Group("/permissions")
	g.Get("/matrix", h.Matrix)
	g.Get("/audit", h.Audit)
	g.Post("/overrides", h.Add)
	g.Delete("/overrides", h.Remove)

	slog.Info("permission console registered under /api/v1/admin/permissions/*")
}

func callerFrom(c fiber.Ctx) Caller {
	roles, _ := c.Locals("user_roles").([]string)
	id, _ := c.Locals("user_id").(uint)
	return Caller{UserID: id, Roles: roles}
}

func (h *Handler) Matrix(c fiber.Ctx) error {
	m, err := h.svc.Matrix(c.Context(), callerFrom(c))
	if err != nil {
		slog.Error("permissions: matrix build failed", "err", err)
		return response.InternalError(c, apperrors.ErrOperationFailed)
	}
	return response.Success(c, m)
}

func (h *Handler) Audit(c fiber.Ctx) error {
	limit, _ := strconv.Atoi(c.Query("limit"))
	entries, err := h.svc.Audit(c.Context(), limit)
	if err != nil {
		slog.Error("permissions: audit read failed", "err", err)
		return response.InternalError(c, apperrors.ErrOperationFailed)
	}
	return response.Success(c, entries)
}

type overrideRequest struct {
	Role       string `json:"role"`
	Permission string `json:"permission"`
	Effect     string `json:"effect"`
}

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
		return response.ForbiddenMsg(c, apperrors.ErrForbidden, err.Error())
	case errors.Is(err, ErrAlreadyGranted), errors.Is(err, ErrNotGranted):
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
