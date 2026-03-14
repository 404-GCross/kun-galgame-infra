package handler

import (
	"strconv"

	"api/internal/platform/game/service"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// GameHandler handles game requests
type GameHandler struct {
	gameService *service.GameService
}

// NewGameHandler creates a new GameHandler
func NewGameHandler(gameService *service.GameService) *GameHandler {
	return &GameHandler{gameService: gameService}
}

// List lists games with pagination
func (h *GameHandler) List(c fiber.Ctx) error {
	var p utils.Pagination
	if err := c.Bind().Query(&p); err != nil {
		p = utils.DefaultPagination()
	}

	result, err := h.gameService.List(c.Context(), p)
	if err != nil {
		return response.InternalError(c, "failed to list games")
	}

	return response.Success(c, result)
}

// Get gets a game by ID
func (h *GameHandler) Get(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return response.BadRequest(c, -1, "invalid id")
	}

	game, err := h.gameService.GetByID(c.Context(), uint(id))
	if err != nil {
		return response.NotFound(c, -1, "game not found")
	}

	return response.Success(c, game)
}

// Create creates a new game
func (h *GameHandler) Create(c fiber.Ctx) error {
	// TODO: implement
	return response.Success(c, nil)
}

// Update updates a game
func (h *GameHandler) Update(c fiber.Ctx) error {
	// TODO: implement
	return response.Success(c, nil)
}

// ListRevisions lists game revisions
func (h *GameHandler) ListRevisions(c fiber.Ctx) error {
	// TODO: implement
	return response.Success(c, nil)
}

// CreateRevision creates a new revision
func (h *GameHandler) CreateRevision(c fiber.Ctx) error {
	// TODO: implement
	return response.Success(c, nil)
}

// Revert reverts to a revision
func (h *GameHandler) Revert(c fiber.Ctx) error {
	// TODO: implement
	return response.Success(c, nil)
}
