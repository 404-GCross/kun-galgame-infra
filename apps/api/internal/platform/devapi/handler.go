package devapi

import (
	"encoding/json"
	goerrors "errors"
	"strconv"

	apperr "api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// AdminHandler serves the developer-platform management surface (admin-only,
// mounted under the oauth console). It follows the site handler conventions:
// bind → validate → service → house response envelope.
type AdminHandler struct {
	svc *AdminService
}

// NewAdminHandler builds the management handler.
func NewAdminHandler(svc *AdminService) *AdminHandler {
	return &AdminHandler{svc: svc}
}

// Register mounts the management routes on r. The caller (cmd/oauth) applies the
// auth + devapi.manage permission gate on the group it passes in.
func (h *AdminHandler) Register(r fiber.Router) {
	r.Get("/apps", h.ListApps)
	r.Patch("/apps/:client_id", h.PatchApp)
	r.Post("/apps/:client_id/keys", h.MintKey)
	r.Get("/apps/:client_id/keys", h.ListKeys)
	r.Post("/apps/:client_id/keys/:id/rotate", h.RotateKey)
	r.Delete("/apps/:client_id/keys/:id", h.RevokeKey)
}

// --- Request / response DTOs ---

type patchAppRequest struct {
	OwnerUserID    *uint   `json:"owner_user_id"`
	DevEnabled     *bool   `json:"dev_enabled"`
	DevTier        *string `json:"dev_tier"`
	DevNSFWAllowed *bool   `json:"dev_nsfw_allowed"`
	DevRatePerMin  *int    `json:"dev_rate_per_min"`
	DevQuotaDaily  *int    `json:"dev_quota_daily"`
}

type mintKeyRequest struct {
	Name   string   `json:"name"`
	Test   bool     `json:"test"`
	Scopes []string `json:"scopes"`
}

type appView struct {
	ClientID       string `json:"client_id"`
	Name           string `json:"name"`
	OwnerUserID    *uint  `json:"owner_user_id,omitempty"`
	DevEnabled     bool   `json:"dev_enabled"`
	DevTier        string `json:"dev_tier"`
	DevNSFWAllowed bool   `json:"dev_nsfw_allowed"`
	DevRatePerMin  int    `json:"dev_rate_per_min"`
	DevQuotaDaily  int    `json:"dev_quota_daily"`
	KeyCount       int64  `json:"key_count"`
}

type keyView struct {
	ID          uint     `json:"id"`
	ClientID    string   `json:"client_id"`
	Name        string   `json:"name"`
	KeyPrefix   string   `json:"key_prefix"`
	Last4       string   `json:"last4"`
	Scopes      []string `json:"scopes"`
	NSFWAllowed bool     `json:"nsfw_allowed"`
	ExpiresAt   string   `json:"expires_at,omitempty"`
	RevokedAt   string   `json:"revoked_at,omitempty"`
	LastUsedAt  string   `json:"last_used_at,omitempty"`
	CreatedAt   string   `json:"created_at"`
}

// mintedKeyView is keyView plus the show-once plaintext.
type mintedKeyView struct {
	keyView
	Key string `json:"key"`
}

// --- Handlers ---

// ListApps returns the dev_enabled applications with their key counts.
func (h *AdminHandler) ListApps(c fiber.Ctx) error {
	apps, err := h.svc.ListApps(c.Context())
	if err != nil {
		return response.InternalError(c, apperr.ErrOperationFailed)
	}
	out := make([]appView, len(apps))
	for i, a := range apps {
		out[i] = appView{
			ClientID:       a.Client.ID,
			Name:           a.Client.Name,
			OwnerUserID:    a.Client.OwnerUserID,
			DevEnabled:     a.Client.DevEnabled,
			DevTier:        a.Client.DevTier,
			DevNSFWAllowed: a.Client.DevNSFWAllowed,
			DevRatePerMin:  a.Client.DevRatePerMin,
			DevQuotaDaily:  a.Client.DevQuotaDaily,
			KeyCount:       a.KeyCount,
		}
	}
	return response.Success(c, out)
}

// PatchApp updates an app's dev configuration.
func (h *AdminHandler) PatchApp(c fiber.Ctx) error {
	clientID := c.Params("client_id")
	if clientID == "" {
		return response.BadRequest(c, apperr.ErrMissingParam)
	}
	var req patchAppRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, apperr.ErrBadRequest)
	}
	app, err := h.svc.UpdateAppConfig(c.Context(), clientID, AppConfig{
		OwnerUserID:    req.OwnerUserID,
		DevEnabled:     req.DevEnabled,
		DevTier:        req.DevTier,
		DevNSFWAllowed: req.DevNSFWAllowed,
		DevRatePerMin:  req.DevRatePerMin,
		DevQuotaDaily:  req.DevQuotaDaily,
	})
	if goerrors.Is(err, ErrInvalidTier) {
		return response.BadRequestMsg(c, apperr.ErrValidationFailed, "invalid tier (want free|trusted|internal)")
	}
	if goerrors.Is(err, gorm.ErrRecordNotFound) {
		return response.NotFound(c, apperr.ErrNotFound)
	}
	if err != nil {
		return response.InternalError(c, apperr.ErrOperationFailed)
	}
	return response.Success(c, appView{
		ClientID:       app.ID,
		Name:           app.Name,
		OwnerUserID:    app.OwnerUserID,
		DevEnabled:     app.DevEnabled,
		DevTier:        app.DevTier,
		DevNSFWAllowed: app.DevNSFWAllowed,
		DevRatePerMin:  app.DevRatePerMin,
		DevQuotaDaily:  app.DevQuotaDaily,
	})
}

// MintKey issues a new key and returns the plaintext ONCE.
func (h *AdminHandler) MintKey(c fiber.Ctx) error {
	clientID := c.Params("client_id")
	if clientID == "" {
		return response.BadRequest(c, apperr.ErrMissingParam)
	}
	var req mintKeyRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, apperr.ErrBadRequest)
	}
	if req.Name == "" {
		return response.BadRequestMsg(c, apperr.ErrValidationFailed, "name is required")
	}
	createdBy, _ := c.Locals("user_id").(uint)
	key, plaintext, err := h.svc.MintKey(c.Context(), clientID, MintKeyInput{
		Name:   req.Name,
		Test:   req.Test,
		Scopes: req.Scopes,
	}, createdBy)
	if goerrors.Is(err, gorm.ErrRecordNotFound) {
		return response.NotFound(c, apperr.ErrNotFound)
	}
	if err != nil {
		return response.InternalError(c, apperr.ErrOperationFailed)
	}
	return response.Success(c, mintedKeyView{keyView: toKeyView(key), Key: plaintext})
}

// ListKeys returns an app's keys (no secret material).
func (h *AdminHandler) ListKeys(c fiber.Ctx) error {
	clientID := c.Params("client_id")
	if clientID == "" {
		return response.BadRequest(c, apperr.ErrMissingParam)
	}
	keys, err := h.svc.ListKeys(c.Context(), clientID)
	if err != nil {
		return response.InternalError(c, apperr.ErrOperationFailed)
	}
	out := make([]keyView, len(keys))
	for i := range keys {
		out[i] = toKeyView(&keys[i])
	}
	return response.Success(c, out)
}

// RotateKey mints a replacement key (old key enters a grace window).
func (h *AdminHandler) RotateKey(c fiber.Ctx) error {
	clientID := c.Params("client_id")
	keyID, ok := parseKeyID(c)
	if !ok {
		return response.BadRequest(c, apperr.ErrInvalidID)
	}
	if _, err := h.requireKeyOfClient(c, clientID, keyID); err != nil {
		return err
	}
	createdBy, _ := c.Locals("user_id").(uint)
	key, plaintext, err := h.svc.RotateKey(c.Context(), keyID, createdBy)
	if err != nil {
		return response.InternalError(c, apperr.ErrOperationFailed)
	}
	return response.Success(c, mintedKeyView{keyView: toKeyView(key), Key: plaintext})
}

// RevokeKey revokes a key immediately.
func (h *AdminHandler) RevokeKey(c fiber.Ctx) error {
	clientID := c.Params("client_id")
	keyID, ok := parseKeyID(c)
	if !ok {
		return response.BadRequest(c, apperr.ErrInvalidID)
	}
	if _, err := h.requireKeyOfClient(c, clientID, keyID); err != nil {
		return err
	}
	if err := h.svc.RevokeKey(c.Context(), keyID); err != nil {
		return response.InternalError(c, apperr.ErrOperationFailed)
	}
	return response.Success(c, nil)
}

// requireKeyOfClient resolves a key and enforces route nesting (key belongs to
// client_id). It writes the error response itself and returns a non-nil error
// the caller returns as-is.
func (h *AdminHandler) requireKeyOfClient(c fiber.Ctx, clientID string, keyID uint) (*DeveloperAPIKey, error) {
	key, err := h.svc.GetKeyForClient(c.Context(), clientID, keyID)
	if goerrors.Is(err, gorm.ErrRecordNotFound) || key == nil {
		return nil, response.NotFound(c, apperr.ErrNotFound)
	}
	if err != nil {
		return nil, response.InternalError(c, apperr.ErrOperationFailed)
	}
	return key, nil
}

func parseKeyID(c fiber.Ctx) (uint, bool) {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return 0, false
	}
	return uint(id), true
}

func toKeyView(k *DeveloperAPIKey) keyView {
	var scopes []string
	if len(k.Scopes) > 0 {
		_ = json.Unmarshal(k.Scopes, &scopes)
	}
	v := keyView{
		ID:          k.ID,
		ClientID:    k.ClientID,
		Name:        k.Name,
		KeyPrefix:   k.KeyPrefix,
		Last4:       k.Last4,
		Scopes:      scopes,
		NSFWAllowed: k.NSFWAllowed,
		CreatedAt:   k.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if k.ExpiresAt != nil {
		v.ExpiresAt = k.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	if k.RevokedAt != nil {
		v.RevokedAt = k.RevokedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	if k.LastUsedAt != nil {
		v.LastUsedAt = k.LastUsedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return v
}
