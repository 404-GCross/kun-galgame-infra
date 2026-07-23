package devapi

import (
	goerrors "errors"
	"strconv"

	apperr "api/pkg/errors"
	"api/pkg/response"

	siteModel "api/internal/platform/site/model"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// SelfServiceHandler serves the developer self-service face (/api/v1/dev/*):
// the developer's own view of their applications and keys. The caller mounts it
// under a group carrying the user-JWT auth middleware (no admin permission); the
// per-request owner guard lives in the service (a non-owned app is 404, never
// distinguishable from a nonexistent one — no existence leak).
type SelfServiceHandler struct {
	svc *SelfServiceService
}

// NewSelfServiceHandler builds the self-service handler.
func NewSelfServiceHandler(svc *SelfServiceService) *SelfServiceHandler {
	return &SelfServiceHandler{svc: svc}
}

// Register mounts the self-service routes on r (a group already gated by the
// user-JWT auth middleware).
func (h *SelfServiceHandler) Register(r fiber.Router) {
	r.Post("/apps", h.CreateApp)
	r.Get("/apps", h.ListApps)
	r.Get("/apps/:client_id", h.GetApp)
	r.Patch("/apps/:client_id", h.UpdateApp)
	r.Delete("/apps/:client_id", h.DeactivateApp)
	r.Post("/apps/:client_id/keys", h.MintKey)
	r.Get("/apps/:client_id/keys", h.ListKeys)
	r.Post("/apps/:client_id/keys/:id/rotate", h.RotateKey)
	r.Delete("/apps/:client_id/keys/:id", h.RevokeKey)
	r.Get("/apps/:client_id/usage", h.Usage)
	r.Get("/usage", h.OwnerUsage)
}

// --- Request / response DTOs ---

type createAppRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type updateAppRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

type selfMintKeyRequest struct {
	Name   string   `json:"name"`
	Test   bool     `json:"test"`
	Scopes []string `json:"scopes"`
}

// selfAppView is the developer's view of one of their apps. Tier / rate / quota
// are read-only here (informational — set by admins); nsfw is omitted (never
// self-service). rate_per_min / quota_daily are the EFFECTIVE limits (tier
// default with any admin override applied); 0 = unlimited (internal tier).
type selfAppView struct {
	ClientID    string `json:"client_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	DevEnabled  bool   `json:"dev_enabled"`
	Tier        string `json:"tier"`
	RatePerMin  int    `json:"rate_per_min"`
	QuotaDaily  int    `json:"quota_daily"`
	KeyCount    int64  `json:"key_count"`
	CreatedAt   string `json:"created_at"`
}

// --- Handlers ---

// CreateApp registers a new developer application for the caller.
func (h *SelfServiceHandler) CreateApp(c fiber.Ctx) error {
	ownerID, ok := ownerFromCtx(c)
	if !ok {
		return response.Unauthorized(c, apperr.ErrAuthUnauthorized)
	}
	var req createAppRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, apperr.ErrBadRequest)
	}
	app, err := h.svc.CreateApp(c.Context(), ownerID, req.Name, req.Description)
	if msg, bad := selfServiceBadRequest(err); bad {
		return response.BadRequestMsg(c, apperr.ErrValidationFailed, msg)
	}
	if err != nil {
		return response.InternalError(c, apperr.ErrOperationFailed)
	}
	return response.Success(c, toSelfAppView(app, 0))
}

// ListApps returns the caller's applications with their key counts.
func (h *SelfServiceHandler) ListApps(c fiber.Ctx) error {
	ownerID, ok := ownerFromCtx(c)
	if !ok {
		return response.Unauthorized(c, apperr.ErrAuthUnauthorized)
	}
	apps, err := h.svc.ListApps(c.Context(), ownerID)
	if err != nil {
		return response.InternalError(c, apperr.ErrOperationFailed)
	}
	out := make([]selfAppView, len(apps))
	for i, a := range apps {
		out[i] = toSelfAppView(a.Client, a.KeyCount)
	}
	return response.Success(c, out)
}

// GetApp returns one of the caller's applications.
func (h *SelfServiceHandler) GetApp(c fiber.Ctx) error {
	ownerID, ok := ownerFromCtx(c)
	if !ok {
		return response.Unauthorized(c, apperr.ErrAuthUnauthorized)
	}
	view, err := h.svc.GetApp(c.Context(), ownerID, c.Params("client_id"))
	if goerrors.Is(err, gorm.ErrRecordNotFound) {
		return response.NotFound(c, apperr.ErrNotFound)
	}
	if err != nil {
		return response.InternalError(c, apperr.ErrOperationFailed)
	}
	return response.Success(c, toSelfAppView(view.Client, view.KeyCount))
}

// UpdateApp patches an owned app's name and/or description.
func (h *SelfServiceHandler) UpdateApp(c fiber.Ctx) error {
	ownerID, ok := ownerFromCtx(c)
	if !ok {
		return response.Unauthorized(c, apperr.ErrAuthUnauthorized)
	}
	var req updateAppRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, apperr.ErrBadRequest)
	}
	app, err := h.svc.UpdateApp(c.Context(), ownerID, c.Params("client_id"), req.Name, req.Description)
	if goerrors.Is(err, gorm.ErrRecordNotFound) {
		return response.NotFound(c, apperr.ErrNotFound)
	}
	if msg, bad := selfServiceBadRequest(err); bad {
		return response.BadRequestMsg(c, apperr.ErrValidationFailed, msg)
	}
	if err != nil {
		return response.InternalError(c, apperr.ErrOperationFailed)
	}
	// Re-count keys for a consistent view shape.
	n, _ := h.svc.repo.CountKeysByClient(c.Context(), app.ID)
	return response.Success(c, toSelfAppView(app, n))
}

// DeactivateApp disables an owned app and revokes all its keys.
func (h *SelfServiceHandler) DeactivateApp(c fiber.Ctx) error {
	ownerID, ok := ownerFromCtx(c)
	if !ok {
		return response.Unauthorized(c, apperr.ErrAuthUnauthorized)
	}
	err := h.svc.DeactivateApp(c.Context(), ownerID, c.Params("client_id"))
	if goerrors.Is(err, gorm.ErrRecordNotFound) {
		return response.NotFound(c, apperr.ErrNotFound)
	}
	if err != nil {
		return response.InternalError(c, apperr.ErrOperationFailed)
	}
	return response.Success(c, nil)
}

// MintKey issues a new key for an owned app and returns the plaintext ONCE.
func (h *SelfServiceHandler) MintKey(c fiber.Ctx) error {
	ownerID, ok := ownerFromCtx(c)
	if !ok {
		return response.Unauthorized(c, apperr.ErrAuthUnauthorized)
	}
	var req selfMintKeyRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, apperr.ErrBadRequest)
	}
	key, plaintext, err := h.svc.MintKey(c.Context(), ownerID, c.Params("client_id"), MintKeyInput{
		Name:   req.Name,
		Test:   req.Test,
		Scopes: req.Scopes,
	})
	if goerrors.Is(err, gorm.ErrRecordNotFound) {
		return response.NotFound(c, apperr.ErrNotFound)
	}
	if msg, bad := selfServiceBadRequest(err); bad {
		return response.BadRequestMsg(c, apperr.ErrValidationFailed, msg)
	}
	if err != nil {
		return response.InternalError(c, apperr.ErrOperationFailed)
	}
	return response.Success(c, mintedKeyView{keyView: toKeyView(key), Key: plaintext})
}

// ListKeys returns an owned app's keys (no secret material).
func (h *SelfServiceHandler) ListKeys(c fiber.Ctx) error {
	ownerID, ok := ownerFromCtx(c)
	if !ok {
		return response.Unauthorized(c, apperr.ErrAuthUnauthorized)
	}
	keys, err := h.svc.ListKeys(c.Context(), ownerID, c.Params("client_id"))
	if goerrors.Is(err, gorm.ErrRecordNotFound) {
		return response.NotFound(c, apperr.ErrNotFound)
	}
	if err != nil {
		return response.InternalError(c, apperr.ErrOperationFailed)
	}
	out := make([]keyView, len(keys))
	for i := range keys {
		out[i] = toKeyView(&keys[i])
	}
	return response.Success(c, out)
}

// RotateKey mints a replacement key for an owned app (old key enters grace).
func (h *SelfServiceHandler) RotateKey(c fiber.Ctx) error {
	ownerID, ok := ownerFromCtx(c)
	if !ok {
		return response.Unauthorized(c, apperr.ErrAuthUnauthorized)
	}
	keyID, ok := parseKeyID(c)
	if !ok {
		return response.BadRequest(c, apperr.ErrInvalidID)
	}
	key, plaintext, err := h.svc.RotateKey(c.Context(), ownerID, c.Params("client_id"), keyID)
	if goerrors.Is(err, gorm.ErrRecordNotFound) {
		return response.NotFound(c, apperr.ErrNotFound)
	}
	if err != nil {
		return response.InternalError(c, apperr.ErrOperationFailed)
	}
	if key == nil { // owned app, but the key is not one of its keys
		return response.NotFound(c, apperr.ErrNotFound)
	}
	return response.Success(c, mintedKeyView{keyView: toKeyView(key), Key: plaintext})
}

// RevokeKey revokes a key of an owned app immediately.
func (h *SelfServiceHandler) RevokeKey(c fiber.Ctx) error {
	ownerID, ok := ownerFromCtx(c)
	if !ok {
		return response.Unauthorized(c, apperr.ErrAuthUnauthorized)
	}
	keyID, ok := parseKeyID(c)
	if !ok {
		return response.BadRequest(c, apperr.ErrInvalidID)
	}
	found, err := h.svc.RevokeKey(c.Context(), ownerID, c.Params("client_id"), keyID)
	if goerrors.Is(err, gorm.ErrRecordNotFound) {
		return response.NotFound(c, apperr.ErrNotFound)
	}
	if err != nil {
		return response.InternalError(c, apperr.ErrOperationFailed)
	}
	if !found { // owned app, but the key is not one of its keys
		return response.NotFound(c, apperr.ErrNotFound)
	}
	return response.Success(c, nil)
}

// Usage returns the caller's app usage aggregated by (day, face). ?days=N,
// clamped to [1, 30], default 7.
func (h *SelfServiceHandler) Usage(c fiber.Ctx) error {
	ownerID, ok := ownerFromCtx(c)
	if !ok {
		return response.Unauthorized(c, apperr.ErrAuthUnauthorized)
	}
	days := clampDays(c.Query("days"))
	rows, err := h.svc.Usage(c.Context(), ownerID, c.Params("client_id"), days)
	if goerrors.Is(err, gorm.ErrRecordNotFound) {
		return response.NotFound(c, apperr.ErrNotFound)
	}
	if err != nil {
		return response.InternalError(c, apperr.ErrOperationFailed)
	}
	if rows == nil {
		rows = []UsageDayFace{}
	}
	return response.Success(c, rows)
}

// OwnerUsage returns the caller's usage aggregated across ALL their apps: a
// dense daily volume series + a per-app breakdown + window totals. ?days=N,
// clamped to [1, 30], default 7.
func (h *SelfServiceHandler) OwnerUsage(c fiber.Ctx) error {
	ownerID, ok := ownerFromCtx(c)
	if !ok {
		return response.Unauthorized(c, apperr.ErrAuthUnauthorized)
	}
	summary, err := h.svc.OwnerUsage(c.Context(), ownerID, clampDays(c.Query("days")))
	if err != nil {
		return response.InternalError(c, apperr.ErrOperationFailed)
	}
	return response.Success(c, summary)
}

// --- helpers ---

// ownerFromCtx reads the authenticated user id the auth middleware set as a
// local. A missing/zero id (no auth) is treated as unauthenticated.
func ownerFromCtx(c fiber.Ctx) (uint, bool) {
	id, ok := c.Locals("user_id").(uint)
	if !ok || id == 0 {
		return 0, false
	}
	return id, true
}

// clampDays parses the usage window: default 7, min 1, max 30.
func clampDays(raw string) int {
	days, err := strconv.Atoi(raw)
	if err != nil || days <= 0 {
		return 7
	}
	if days > 30 {
		return 30
	}
	return days
}

// selfServiceBadRequest maps a self-service validation/limit sentinel to its
// client-facing 400 message; (_, false) means it is not such an error.
func selfServiceBadRequest(err error) (string, bool) {
	switch {
	case err == nil:
		return "", false
	case goerrors.Is(err, ErrAppLimitReached):
		return "application limit reached (max 5 per account)", true
	case goerrors.Is(err, ErrKeyLimitReached):
		return "active key limit reached (max 5 per application)", true
	case goerrors.Is(err, ErrScopeNotAllowed):
		return "scope not permitted (want catalog:read and/or galgame:read)", true
	case goerrors.Is(err, ErrNameRequired):
		return "name is required", true
	case goerrors.Is(err, ErrNameTooLong):
		return "name too long (max 100)", true
	case goerrors.Is(err, ErrDescTooLong):
		return "description too long (max 100)", true
	default:
		return "", false
	}
}

// toSelfAppView renders an app row + key count into the developer's view, with
// the effective (tier default + override) rate/quota resolved.
func toSelfAppView(app *siteModel.OAuthClient, keyCount int64) selfAppView {
	rate, quota := effectiveAppLimits(app)
	return selfAppView{
		ClientID:    app.ID,
		Name:        app.Name,
		Description: app.Tagline,
		DevEnabled:  app.DevEnabled,
		Tier:        app.DevTier,
		RatePerMin:  rate,
		QuotaDaily:  quota,
		KeyCount:    keyCount,
		CreatedAt:   app.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

// effectiveAppLimits resolves an app's effective per-minute rate and daily quota
// (tier default, with a positive override winning; 0 = unlimited for internal).
// Mirrors Credential.EffectiveRate/Quota on the app row.
func effectiveAppLimits(app *siteModel.OAuthClient) (rate, quota int) {
	defRate, defQuota, unlimited := TierLimits(app.DevTier)
	if unlimited {
		return 0, 0
	}
	rate, quota = defRate, defQuota
	if app.DevRatePerMin > 0 {
		rate = app.DevRatePerMin
	}
	if app.DevQuotaDaily > 0 {
		quota = app.DevQuotaDaily
	}
	return rate, quota
}
