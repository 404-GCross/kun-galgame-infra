package handler

import (
	"encoding/json"
	"strconv"

	"api/internal/platform/site/dto"
	"api/internal/platform/site/service"
	siteModel "api/internal/platform/site/model"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

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
			CreatedAt:   s.CreatedAt.Format("2006-01-02T15:04:05Z"),
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
		CreatedAt:   site.CreatedAt.Format("2006-01-02T15:04:05Z"),
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
		CreatedAt:   site.CreatedAt.Format("2006-01-02T15:04:05Z"),
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
		CreatedAt:   site.CreatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

// Delete deletes a site
func (h *SiteHandler) Delete(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
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

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
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
	)
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
		CreatedAt:              cl.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
}
