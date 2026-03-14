package handler

import (
	"strconv"

	"api/internal/platform/artifact/service"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// ArtifactHandler handles artifact requests
type ArtifactHandler struct {
	artifactService *service.ArtifactService
}

// NewArtifactHandler creates a new ArtifactHandler
func NewArtifactHandler(artifactService *service.ArtifactService) *ArtifactHandler {
	return &ArtifactHandler{artifactService: artifactService}
}

// List lists artifacts
func (h *ArtifactHandler) List(c fiber.Ctx) error {
	var p utils.Pagination
	if err := c.Bind().Query(&p); err != nil {
		p = utils.DefaultPagination()
	}

	result, err := h.artifactService.List(c.Context(), p)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, result)
}

// Get gets an artifact by ID
func (h *ArtifactHandler) Get(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	artifact, err := h.artifactService.GetByID(c.Context(), uint(id))
	if err != nil {
		return response.NotFound(c, errors.ErrArtifactNotFound)
	}

	return response.Success(c, artifact)
}

// Create creates a new artifact (initiates upload)
func (h *ArtifactHandler) Create(c fiber.Ctx) error {
	// TODO: implement - generate presigned URL for upload
	return response.Success(c, nil)
}

// Delete deletes an artifact
func (h *ArtifactHandler) Delete(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	if err := h.artifactService.Delete(c.Context(), uint(id)); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, nil)
}

// Download gets a download URL for an artifact
func (h *ArtifactHandler) Download(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	artifact, err := h.artifactService.GetByID(c.Context(), uint(id))
	if err != nil {
		return response.NotFound(c, errors.ErrArtifactNotFound)
	}

	// TODO: generate presigned download URL from FileKey
	return response.Success(c, fiber.Map{
		"file_key": artifact.FileKey,
	})
}
