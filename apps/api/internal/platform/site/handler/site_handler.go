package handler

import (
	"encoding/json"
	"slices"
	"strconv"

	"api/internal/platform/site/dto"
	siteModel "api/internal/platform/site/model"
	"api/internal/platform/site/perm"
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
// CreateClient / UpdateClient (oauth.clients.privileged_config / storage_config).
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

// callerRoles returns the authenticated caller's OAuth roles from the Fiber
// locals populated by middleware.Auth — the input to the client-config
// permission checks below.
func callerRoles(c fiber.Ctx) []string {
	roles, _ := c.Locals("user_roles").([]string)
	return roles
}

// callerUserID returns the authenticated caller's numeric user id (0 when the
// JWT middleware did not populate one, which never happens on these routes).
func callerUserID(c fiber.Ctx) uint {
	id, _ := c.Locals("user_id").(uint)
	return id
}

// managesAll reports whether the caller sees the whole console (ren) rather
// than just the sites/clients they created.
func (h *SiteHandler) managesAll(c fiber.Ctx) bool {
	return perm.Resolver.Can(callerRoles(c), perm.SitesManageAll)
}

// mayManage is the ownership rule behind every per-row gate below: a manage_all
// caller reaches everything; anyone else reaches only rows they created. A NULL
// creator (pre-ownership rows, developer-portal apps) belongs to nobody and is
// therefore manage_all-only. Kept a pure function so it is unit-testable.
func mayManage(managesAll bool, callerID uint, createdBy *uint) bool {
	if managesAll {
		return true
	}
	return callerID != 0 && createdBy != nil && *createdBy == callerID
}

// notOwnerMsg is what a non-ren admin sees when they address someone else's row
// (or a pre-ownership one). Deliberately a 403 rather than a 404: the console's
// admins are trusted, and "you don't own this" is far more actionable than a
// phantom "not found".
const notOwnerMsg = "只能查看和管理自己创建的站点 / 客户端"

// List lists the sites the caller may manage: all of them for ren, only their
// own for an ordinary admin.
func (h *SiteHandler) List(c fiber.Ctx) error {
	var sites []siteModel.Site
	var err error
	if h.managesAll(c) {
		sites, err = h.siteService.List(c.Context())
	} else {
		sites, err = h.siteService.ListByCreator(c.Context(), callerUserID(c))
	}
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
	if !mayManage(h.managesAll(c), callerUserID(c), site.CreatedByUserID) {
		return response.ForbiddenMsg(c, errors.ErrForbidden, notOwnerMsg)
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

	// Stamp the creator: this is what scopes the console for non-ren admins.
	site := &siteModel.Site{
		Name:        req.Name,
		Domain:      req.Domain,
		Description: req.Description,
	}
	if uid := callerUserID(c); uid != 0 {
		site.CreatedByUserID = &uid
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
	if !mayManage(h.managesAll(c), callerUserID(c), site.CreatedByUserID) {
		return response.ForbiddenMsg(c, errors.ErrForbidden, notOwnerMsg)
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

	site, err := h.siteService.GetByID(c.Context(), uint(id))
	if err != nil {
		return response.NotFound(c, errors.ErrSiteNotFound)
	}
	if !mayManage(h.managesAll(c), callerUserID(c), site.CreatedByUserID) {
		return response.ForbiddenMsg(c, errors.ErrForbidden, notOwnerMsg)
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

// ListClients lists the OAuth clients the caller may manage: all of them for
// ren, only their own for an ordinary admin.
func (h *SiteHandler) ListClients(c fiber.Ctx) error {
	var clients []siteModel.OAuthClient
	var err error
	if h.managesAll(c) {
		clients, err = h.siteService.ListOAuthClients(c.Context())
	} else {
		clients, err = h.siteService.ListOAuthClientsByCreator(c.Context(), callerUserID(c))
	}
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

	// The site itself must be visible before its clients are, and the client
	// list is then narrowed by the same ownership rule.
	site, err := h.siteService.GetByID(c.Context(), uint(id))
	if err != nil {
		return response.NotFound(c, errors.ErrSiteNotFound)
	}
	managesAll := h.managesAll(c)
	if !mayManage(managesAll, callerUserID(c), site.CreatedByUserID) {
		return response.ForbiddenMsg(c, errors.ErrForbidden, notOwnerMsg)
	}

	var clients []siteModel.OAuthClient
	if managesAll {
		clients, err = h.siteService.GetOAuthClientsBySiteID(c.Context(), uint(id))
	} else {
		clients, err = h.siteService.GetOAuthClientsBySiteIDAndCreator(c.Context(), uint(id), callerUserID(c))
	}
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

	// privileged-config gate (ren): only ren（莲）may grant a ren-only scope
	// (image:upload / artifact:upload) or enable auto_consent. All are
	// default-off, so an ordinary admin simply creates a normal login client; a
	// caller without the permission who explicitly asks for any is refused.
	// (Backstops the frontend, which hides these controls for non-ren.)
	roles := callerRoles(c)
	if !perm.Resolver.Can(roles, perm.ClientsPrivilegedConfig) &&
		(addsRenOnlyScope(req.AllowedScopes) || req.AutoConsent) {
		return response.ForbiddenMsg(c, errors.ErrForbidden, renSensitiveFieldMsg)
	}

	// display_order is ren-only: it controls the cross-site ordering of the
	// public app directory (a central decision), unlike the per-client display
	// fields (listed/logo/tagline) any admin may set. Silently pin the value to
	// the default for a caller without the permission so their save still
	// succeeds (the frontend hides it).
	if !perm.Resolver.Can(roles, perm.ClientsPrivilegedConfig) {
		req.DisplayOrder = 0
	}

	// Verify site exists — and that the caller may manage it. Without the
	// ownership check an admin could attach a client to someone else's site by
	// guessing its id, which the filtered site list would never show them.
	site, err := h.siteService.GetByID(c.Context(), req.SiteID)
	if err != nil {
		return response.NotFound(c, errors.ErrSiteNotFound)
	}
	if !mayManage(perm.Resolver.Can(roles, perm.SitesManageAll), callerUserID(c), site.CreatedByUserID) {
		return response.ForbiddenMsg(c, errors.ErrForbidden, notOwnerMsg)
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

	var createdBy *uint
	if uid := callerUserID(c); uid != 0 {
		createdBy = &uid
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
		createdBy,
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

	cur, err := h.siteService.GetOAuthClient(c.Context(), clientID)
	if err != nil {
		return response.NotFound(c, errors.ErrOperationFailed)
	}
	if !mayManage(h.managesAll(c), callerUserID(c), cur.CreatedByUserID) {
		return response.ForbiddenMsg(c, errors.ErrForbidden, notOwnerMsg)
	}

	// privileged-config gate (no-escalation): a caller without
	// oauth.clients.privileged_config may edit a client, but may not ADD a
	// ren-only scope (image:upload / artifact:upload) or turn ON auto_consent.
	// Leaving a ren-provisioned client's existing sensitive fields untouched
	// (the form re-submits them) and de-escalating are both fine — only
	// escalation is blocked, compared against the current row.
	if !perm.Resolver.Can(callerRoles(c), perm.ClientsPrivilegedConfig) {
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

	cur, err := h.siteService.GetOAuthClient(c.Context(), clientID)
	if err != nil {
		return response.NotFound(c, errors.ErrOperationFailed)
	}
	if !mayManage(h.managesAll(c), callerUserID(c), cur.CreatedByUserID) {
		return response.ForbiddenMsg(c, errors.ErrForbidden, notOwnerMsg)
	}
	if !perm.Resolver.Can(callerRoles(c), perm.ClientsStorageConfig) {
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

	cur, err := h.siteService.GetOAuthClient(c.Context(), clientID)
	if err != nil {
		return response.NotFound(c, errors.ErrOperationFailed)
	}
	if !mayManage(h.managesAll(c), callerUserID(c), cur.CreatedByUserID) {
		return response.ForbiddenMsg(c, errors.ErrForbidden, notOwnerMsg)
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
