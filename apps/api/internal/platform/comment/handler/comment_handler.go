package handler

import (
	"strconv"

	"api/internal/platform/comment/service"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// CommentHandler handles comment requests
type CommentHandler struct {
	commentService *service.CommentService
}

// NewCommentHandler creates a new CommentHandler
func NewCommentHandler(commentService *service.CommentService) *CommentHandler {
	return &CommentHandler{commentService: commentService}
}

// List lists comments
func (h *CommentHandler) List(c fiber.Ctx) error {
	// TODO: implement
	return response.Success(c, nil)
}

// Create creates a new comment
func (h *CommentHandler) Create(c fiber.Ctx) error {
	// TODO: implement
	return response.Success(c, nil)
}

// Update updates a comment
func (h *CommentHandler) Update(c fiber.Ctx) error {
	// TODO: implement
	return response.Success(c, nil)
}

// Delete deletes a comment
func (h *CommentHandler) Delete(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	if err := h.commentService.Delete(c.Context(), uint(id)); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, nil)
}
