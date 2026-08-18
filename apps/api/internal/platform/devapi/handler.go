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

type AdminHandler struct {
	svc *AdminService
}

func NewAdminHandler(svc *AdminService) *AdminHandler {
	return &AdminHandler{svc: svc}
}

func (h *AdminHandler) Register(r fiber.Router) {
	r.Get("/apps", h.ListApps)
	r.Patch("/apps/:client_id", h.PatchApp)
	r.Post("/apps/:client_id/keys", h.MintKey)
	r.Get("/apps/:client_id/keys", h.ListKeys)
	r.Post("/apps/:client_id/keys/:id/rotate", h.RotateKey)
	r.Delete("/apps/:client_id/keys/:id", h.RevokeKey)
	r.Get("/scope-applications", h.ListScopeApplications)
	r.Post("/scope-applications/:id/approve", h.ApproveScopeApplication)
	r.Post("/scope-applications/:id/decline", h.DeclineScopeApplication)
}

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

type mintedKeyView struct {
	keyView
	Key string `json:"key"`
}

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

func (h *AdminHandler) RotateKey(c fiber.Ctx) error {
	clientID := c.Params("client_id")
	keyID, ok := parseIDParam(c)
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

func (h *AdminHandler) RevokeKey(c fiber.Ctx) error {
	clientID := c.Params("client_id")
	keyID, ok := parseIDParam(c)
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

type declineScopeApplicationRequest struct {
	Reason string `json:"reason"`
}

type adminScopeApplicationView struct {
	ID            uint   `json:"id"`
	UserID        uint   `json:"user_id"`
	Scope         string `json:"scope"`
	Message       string `json:"message"`
	Status        string `json:"status"`
	ReviewerID    *uint  `json:"reviewer_id,omitempty"`
	DeclineReason string `json:"decline_reason"`
	CreatedAt     string `json:"created_at"`
	ReviewedAt    string `json:"reviewed_at,omitempty"`
}

func (h *AdminHandler) ListScopeApplications(c fiber.Ctx) error {
	status := c.Query("status", ScopeAppPending)
	if status == "all" {
		status = ""
	}
	apps, err := h.svc.ListScopeApplications(c.Context(), status)
	if err != nil {
		return response.InternalError(c, apperr.ErrOperationFailed)
	}
	out := make([]adminScopeApplicationView, len(apps))
	for i := range apps {
		out[i] = toAdminScopeApplicationView(&apps[i])
	}
	return response.Success(c, out)
}

func (h *AdminHandler) ApproveScopeApplication(c fiber.Ctx) error {
	id, ok := parseIDParam(c)
	if !ok {
		return response.BadRequest(c, apperr.ErrInvalidID)
	}
	reviewer, _ := c.Locals("user_id").(uint)
	app, err := h.svc.ApproveScopeApplication(c.Context(), id, reviewer)
	return h.respondScopeApplication(c, app, err)
}

func (h *AdminHandler) DeclineScopeApplication(c fiber.Ctx) error {
	id, ok := parseIDParam(c)
	if !ok {
		return response.BadRequest(c, apperr.ErrInvalidID)
	}
	var req declineScopeApplicationRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, apperr.ErrBadRequest)
	}
	reviewer, _ := c.Locals("user_id").(uint)
	app, err := h.svc.DeclineScopeApplication(c.Context(), id, reviewer, req.Reason)
	return h.respondScopeApplication(c, app, err)
}

func (h *AdminHandler) respondScopeApplication(c fiber.Ctx, app *ScopeApplication, err error) error {
	switch {
	case goerrors.Is(err, gorm.ErrRecordNotFound):
		return response.NotFound(c, apperr.ErrNotFound)
	case goerrors.Is(err, ErrScopeAppNotPending):
		return response.Error(c, fiber.StatusConflict, apperr.ErrValidationFailed,
			"only a pending application can be reviewed")
	case goerrors.Is(err, ErrScopeAppNeedsReason):
		return response.BadRequestMsg(c, apperr.ErrValidationFailed, "a decline needs a reason")
	case goerrors.Is(err, ErrScopeAppMsgTooLong):
		return response.BadRequestMsg(c, apperr.ErrValidationFailed, "reason too long (max 2000)")
	case err != nil:
		return response.InternalError(c, apperr.ErrOperationFailed)
	}
	return response.Success(c, toAdminScopeApplicationView(app))
}

func toAdminScopeApplicationView(app *ScopeApplication) adminScopeApplicationView {
	v := adminScopeApplicationView{
		ID:            app.ID,
		UserID:        app.UserID,
		Scope:         app.Scope,
		Message:       app.Message,
		Status:        app.Status,
		ReviewerID:    app.ReviewerID,
		DeclineReason: app.DeclineReason,
		CreatedAt:     app.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if app.ReviewedAt != nil {
		v.ReviewedAt = app.ReviewedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return v
}

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

func parseIDParam(c fiber.Ctx) (uint, bool) {
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
