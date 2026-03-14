package handler

import (
	"strconv"

	"api/internal/platform/content/service"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// ContentHandler handles content requests
type ContentHandler struct {
	contentService *service.ContentService
}

// NewContentHandler creates a new ContentHandler
func NewContentHandler(contentService *service.ContentService) *ContentHandler {
	return &ContentHandler{contentService: contentService}
}

// List lists content
func (h *ContentHandler) List(c fiber.Ctx) error {
	var p utils.Pagination
	if err := c.Bind().Query(&p); err != nil {
		p = utils.DefaultPagination()
	}

	siteIDStr := c.Query("site_id", "0")
	siteID, _ := strconv.ParseUint(siteIDStr, 10, 32)

	result, err := h.contentService.List(c.Context(), uint(siteID), p)
	if err != nil {
		return response.InternalError(c, "failed to list content")
	}

	return response.Success(c, result)
}

// Get gets content by ID
func (h *ContentHandler) Get(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return response.BadRequest(c, -1, "invalid id")
	}

	content, err := h.contentService.GetByID(c.Context(), uint(id))
	if err != nil {
		return response.NotFound(c, -1, "content not found")
	}

	return response.Success(c, content)
}

// Create creates new content
func (h *ContentHandler) Create(c fiber.Ctx) error {
	// TODO: implement
	return response.Success(c, nil)
}

// Update updates content
func (h *ContentHandler) Update(c fiber.Ctx) error {
	// TODO: implement
	return response.Success(c, nil)
}

// Delete deletes content
func (h *ContentHandler) Delete(c fiber.Ctx) error {
	// TODO: implement
	return response.Success(c, nil)
}
