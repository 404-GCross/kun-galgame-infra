package handler

import (
	"encoding/json"
	"slices"
	"strconv"

	"api/internal/middleware"
	"api/internal/platform/site/dto"
	siteModel "api/internal/platform/site/model"
	"api/internal/platform/site/service"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// renOnlyScopes are the sensitive OAuth scopes only the ren（莲）role may grant
// to a client. Granting `artifact:upload` turns on the entire artifact
// capability for that client (large-file upload/download), and `image:upload`
// the image service — both are default-off and ordinary admins cannot add
// them, like enabling auto_consent. Keep in sync with the frontend
// REN_ONLY_SCOPES (apps/web shared/types/oauth-client.ts). See the ren-gate in
// CreateClient / UpdateClient and middleware.HasRole.
var renOnlyScopes = []string{"image:upload", "artifact:upload"}

// addsRenOnlyScope reports whether the requested scope set contains any
// ren-only scope (used by the create-time gate).
func addsRenOnlyScope(scopes []string) bool {
	for _, s := range scopes {
		if slices.Contains(renOnlyScopes, s) {
			return true
		}
	}
	return false
}

// addsNewRenOnlyScope reports whether the requested scope set ADDS a ren-only
// scope that the current client doesn't already have (used by the update-time
// no-escalation gate — keeping or removing an existing one is fine).
func addsNewRenOnlyScope(reqScopes, curScopes []string) bool {
	for _, s := range reqScopes {
		if slices.Contains(renOnlyScopes, s) && !slices.Contains(curScopes, s) {
			return true
		}
	}
	return false
}

// renSensitiveFieldMsg is the 403 message when a non-ren caller tries to grant
// a ren-only scope (image:upload / artifact:upload) or enable auto_consent.
const renSensitiveFieldMsg = "仅 ren（莲）可授予 image:upload / artifact:upload scope 或开启自动同意"

// SiteHandler handles site requests
type SiteHandler struct {
	siteService *service.SiteService
}

// NewSiteHandler creates a new SiteHandler
func NewSiteHandler(siteService *service.SiteService) *SiteHandler {
	return &SiteHandler{siteService: siteService}
}

// List lists all sites
func (h *SiteHandler) List(c fiber.Ctx) error {
	sites, err := h.siteService.List(c.Context())
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	// Convert to response DTOs
	result := make([]dto.SiteResponse, len(sites))
	for i, s := range sites {
		result[i] = dto.SiteResponse{
			ID:          s.ID,
			Name:        s.Name,
			Domain:      s.Domain,
			Description: s.Description,
			CreatedAt:   s.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
	}

	return response.Success(c, result)
}

// Get gets a site by ID
func (h *SiteHandler) Get(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	site, err := h.siteService.GetByID(c.Context(), uint(id))
	if err != nil {
		return response.NotFound(c, errors.ErrSiteNotFound)
	}

	return response.Success(c, dto.SiteResponse{
		ID:          site.ID,
		Name:        site.Name,
		Domain:      site.Domain,
		Description: site.Description,
		CreatedAt:   site.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	})
}

// Create creates a new site
func (h *SiteHandler) Create(c fiber.Ctx) error {
	var req dto.CreateSiteRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	// Check domain uniqueness
	if h.siteService.DomainExists(c.Context(), req.Domain) {
		return response.BadRequest(c, errors.ErrSiteAlreadyExists)
	}

	site := &siteModel.Site{
		Name:        req.Name,
		Domain:      req.Domain,
		Description: req.Description,
	}

	if err := h.siteService.Create(c.Context(), site); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, dto.SiteResponse{
		ID:          site.ID,
		Name:        site.Name,
		Domain:      site.Domain,
		Description: site.Description,
		CreatedAt:   site.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	})
}

// Update updates a site
func (h *SiteHandler) Update(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	var req dto.UpdateSiteRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	site, err := h.siteService.GetByID(c.Context(), uint(id))
	if err != nil {
		return response.NotFound(c, errors.ErrSiteNotFound)
	}

	if req.Name != nil {
		site.Name = *req.Name
	}
	if req.Description != nil {
		site.Description = *req.Description
	}

	if err := h.siteService.Update(c.Context(), site); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, dto.SiteResponse{
		ID:          site.ID,
		Name:        site.Name,
		Domain:      site.Domain,
		Description: site.Description,
		CreatedAt:   site.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	})
}

// Delete deletes a site
func (h *SiteHandler) Delete(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	// Precheck attached OAuth clients: the FK (sites ← oauth_clients) is
	// NO ACTION, so deleting a site that still has clients raises an opaque
	// FK-violation 500. Surface an actionable message instead. We must NOT
	// cascade — that would silently delete live SSO integrations.
	clients, err := h.siteService.GetOAuthClientsBySiteID(c.Context(), uint(id))
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	if len(clients) > 0 {
		return response.BadRequestMsg(c, errors.ErrOperationFailed, "站点下仍有 OAuth 客户端，请先删除")
	}

	if err := h.siteService.Delete(c.Context(), uint(id)); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, nil)
}

// ListClients lists all OAuth clients
func (h *SiteHandler) ListClients(c fiber.Ctx) error {
	clients, err := h.siteService.ListOAuthClients(c.Context())
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	result := make([]dto.OAuthClientResponse, len(clients))
	for i, cl := range clients {
		result[i] = toOAuthClientResponse(&cl)
	}

	return response.Success(c, result)
}

// GetSiteClients lists OAuth clients for a specific site
func (h *SiteHandler) GetSiteClients(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	clients, err := h.siteService.GetOAuthClientsBySiteID(c.Context(), uint(id))
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	result := make([]dto.OAuthClientResponse, len(clients))
	for i, cl := range clients {
		result[i] = toOAuthClientResponse(&cl)
	}

	return response.Success(c, result)
}

// CreateClient creates an OAuth client
func (h *SiteHandler) CreateClient(c fiber.Ctx) error {
	var req dto.CreateOAuthClientRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	// ren-gate: only ren（莲）may grant a ren-only scope (image:upload /
	// artifact:upload) or enable auto_consent. All are default-off, so an
	// ordinary admin simply creates a normal login client; a non-ren who
	// explicitly asks for any is refused. (Backstops the frontend, which hides
	// these controls for non-ren.)
	if !middleware.HasRole(c, "ren") &&
		(addsRenOnlyScope(req.AllowedScopes) || req.AutoConsent) {
		return response.ForbiddenMsg(c, errors.ErrForbidden, renSensitiveFieldMsg)
	}

	// display_order is ren-only: it controls the cross-site ordering of the
	// public app directory (a central decision), unlike the per-client display
	// fields (listed/logo/tagline) any admin may set. Silently pin a non-ren's
	// value to the default so their save still succeeds (the frontend hides it).
	if !middleware.HasRole(c, "ren") {
		req.DisplayOrder = 0
	}

	// Verify site exists
	if _, err := h.siteService.GetByID(c.Context(), req.SiteID); err != nil {
		return response.NotFound(c, errors.ErrSiteNotFound)
	}

	// Default grants include BOTH authorization_code and refresh_token —
	// real-world clients almost always need both, and shipping
	// authorization_code only causes silent re-login storms 15 minutes
	// after every login (the JWT TTL) because the OAuth server's
	// refresh-grant check rejects them.
	grants := req.Grants
	if grants == nil {
		grants = []string{"authorization_code", "refresh_token"}
	}

	client, secret, err := h.siteService.CreateOAuthClient(
		c.Context(),
		req.SiteID,
		req.Name,
		req.RedirectURIs,
		grants,
		req.AllowedScopes,
		req.IsPublic,
		req.AutoConsent,
		req.RefreshTokenTTLSeconds,
		req.Listed,
		req.LogoURL,
		req.Tagline,
		req.DisplayOrder,
	)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, dto.OAuthClientCreatedResponse{
		OAuthClientResponse: toOAuthClientResponse(client),
		Secret:              secret,
	})
}

// UpdateClient updates an OAuth client
func (h *SiteHandler) UpdateClient(c fiber.Ctx) error {
	clientID := c.Params("id")
	if clientID == "" {
		return response.BadRequest(c, errors.ErrMissingParam)
	}

	var req dto.UpdateOAuthClientRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	// The edit form re-sends every field, so an empty optional string arrives as
	// a non-nil pointer to "". Normalize "" → nil (= leave unchanged) so it skips
	// the validators — go-validator's omitempty only skips a NIL pointer, not a
	// pointer to "", so "" would otherwise trip the url tag. (To swap a logo set a
	// new one; emptying the field leaves the current value as-is.)
	if req.LogoURL != nil && *req.LogoURL == "" {
		req.LogoURL = nil
	}
	if req.Tagline != nil && *req.Tagline == "" {
		req.Tagline = nil
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	// ren-gate (no-escalation): a non-ren admin may edit a client, but may not
	// ADD a ren-only scope (image:upload / artifact:upload) or turn ON
	// auto_consent. Leaving a ren-provisioned client's existing sensitive fields
	// untouched (the form re-submits them) and de-escalating are both fine —
	// only escalation is blocked, compared against the current row.
	if !middleware.HasRole(c, "ren") {
		cur, err := h.siteService.GetOAuthClient(c.Context(), clientID)
		if err != nil {
			return response.NotFound(c, errors.ErrOperationFailed)
		}
		var curScopes []string
		_ = json.Unmarshal(cur.AllowedScopes, &curScopes)
		addsScope := req.AllowedScopes != nil && addsNewRenOnlyScope(req.AllowedScopes, curScopes)
		enablesAutoConsent := req.AutoConsent != nil && *req.AutoConsent && !cur.AutoConsent
		if addsScope || enablesAutoConsent {
			return response.ForbiddenMsg(c, errors.ErrForbidden, renSensitiveFieldMsg)
		}
		// display_order is ren-only — leave it unchanged on a non-ren edit.
		req.DisplayOrder = nil
	}

	client, err := h.siteService.UpdateOAuthClient(
		c.Context(),
		clientID,
		req.Name,
		req.RedirectURIs,
		req.Grants,
		req.AllowedScopes,
		req.AutoConsent,
		req.RefreshTokenTTLSeconds,
		req.Listed,
		req.LogoURL,
		req.Tagline,
		req.DisplayOrder,
	)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, toOAuthClientResponse(client))
}

// UpdateClientStorage sets a client's object-storage capability config (the
// artifact_*/image_* columns). Admin-only; a non-ren admin may not ENABLE a
// capability that is currently off (mirrors the upload-scope ren-gate).
func (h *SiteHandler) UpdateClientStorage(c fiber.Ctx) error {
	clientID := c.Params("id")
	if clientID == "" {
		return response.BadRequest(c, errors.ErrMissingParam)
	}

	var req dto.UpdateClientStorageRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	if !middleware.HasRole(c, "ren") {
		cur, err := h.siteService.GetOAuthClient(c.Context(), clientID)
		if err != nil {
			return response.NotFound(c, errors.ErrOperationFailed)
		}
		if (req.ArtifactEnabled && !cur.ArtifactEnabled) || (req.ImageEnabled && !cur.ImageEnabled) {
			return response.ForbiddenMsg(c, errors.ErrForbidden, renSensitiveFieldMsg)
		}
	}

	client, err := h.siteService.UpdateOAuthClientStorage(c.Context(), clientID, service.StorageConfig{
		ArtifactEnabled:         req.ArtifactEnabled,
		ArtifactSiteKey:         req.ArtifactSiteKey,
		ArtifactCDNBase:         req.ArtifactCDNBase,
		ArtifactAllowedMime:     req.ArtifactAllowedMime,
		ArtifactMaxFileSize:     req.ArtifactMaxFileSize,
		ArtifactQuotaDaily:      req.ArtifactQuotaDaily,
		ArtifactQuotaBytesDaily: req.ArtifactQuotaBytesDaily,
		ImageEnabled:            req.ImageEnabled,
		ImageSiteKey:            req.ImageSiteKey,
		ImageCDNBase:            req.ImageCDNBase,
		ImageAllowedPresets:     req.ImageAllowedPresets,
		ImageMaxFileSize:        req.ImageMaxFileSize,
		ImageQuotaDaily:         req.ImageQuotaDaily,
		ImageQuotaBytesDaily:    req.ImageQuotaBytesDaily,
	})
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, toOAuthClientResponse(client))
}

// DeleteClient deletes an OAuth client
func (h *SiteHandler) DeleteClient(c fiber.Ctx) error {
	clientID := c.Params("id")
	if clientID == "" {
		return response.BadRequest(c, errors.ErrMissingParam)
	}

	if err := h.siteService.DeleteOAuthClient(c.Context(), clientID); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, nil)
}

// toOAuthClientResponse converts model to response DTO
func toOAuthClientResponse(cl *siteModel.OAuthClient) dto.OAuthClientResponse {
	var redirectURIs []string
	_ = json.Unmarshal(cl.RedirectURIs, &redirectURIs)

	var grants []string
	_ = json.Unmarshal(cl.Grants, &grants)

	var allowedScopes []string
	if len(cl.AllowedScopes) > 0 {
		_ = json.Unmarshal(cl.AllowedScopes, &allowedScopes)
	}

	var artifactMime []string
	if len(cl.ArtifactAllowedMime) > 0 {
		_ = json.Unmarshal(cl.ArtifactAllowedMime, &artifactMime)
	}
	var imagePresets []string
	if len(cl.ImageAllowedPresets) > 0 {
		_ = json.Unmarshal(cl.ImageAllowedPresets, &imagePresets)
	}

	return dto.OAuthClientResponse{
		ID:                     cl.ID,
		SiteID:                 cl.SiteID,
		Name:                   cl.Name,
		RedirectURIs:           redirectURIs,
		Grants:                 grants,
		AllowedScopes:          allowedScopes,
		IsPublic:               cl.IsPublic,
		AutoConsent:            cl.AutoConsent,
		RefreshTokenTTLSeconds: cl.RefreshTokenTTLSeconds,
		Listed:                 cl.Listed,
		LogoURL:                cl.LogoURL,
		Tagline:                cl.Tagline,
		DisplayOrder:           cl.DisplayOrder,
		CreatedAt:              cl.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		Storage: dto.OAuthClientStorageConfig{
			ArtifactEnabled:         cl.ArtifactEnabled,
			ArtifactSiteKey:         cl.ArtifactSiteKey,
			ArtifactCDNBase:         cl.ArtifactCDNBase,
			ArtifactAllowedMime:     artifactMime,
			ArtifactMaxFileSize:     cl.ArtifactMaxFileSize,
			ArtifactQuotaDaily:      cl.ArtifactQuotaDaily,
			ArtifactQuotaBytesDaily: cl.ArtifactQuotaBytesDaily,
			ImageEnabled:            cl.ImageEnabled,
			ImageSiteKey:            cl.ImageSiteKey,
			ImageCDNBase:            cl.ImageCDNBase,
			ImageAllowedPresets:     imagePresets,
			ImageMaxFileSize:        cl.ImageMaxFileSize,
			ImageQuotaDaily:         cl.ImageQuotaDaily,
			ImageQuotaBytesDaily:    cl.ImageQuotaBytesDaily,
		},
	}
}
