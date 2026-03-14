package handler

import (
	"strconv"

	"api/internal/platform/site/service"
	"api/pkg/errors"
	"api/pkg/response"

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
	return response.Success(c, sites)
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

	return response.Success(c, site)
}

// Create creates a new site
func (h *SiteHandler) Create(c fiber.Ctx) error {
	// TODO: implement
	return response.Success(c, nil)
}

// Update updates a site
func (h *SiteHandler) Update(c fiber.Ctx) error {
	// TODO: implement
	return response.Success(c, nil)
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

// ListClients lists OAuth clients
func (h *SiteHandler) ListClients(c fiber.Ctx) error {
	clients, err := h.siteService.ListOAuthClients(c.Context())
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, clients)
}

// CreateClient creates an OAuth client
func (h *SiteHandler) CreateClient(c fiber.Ctx) error {
	// TODO: implement
	return response.Success(c, nil)
}
