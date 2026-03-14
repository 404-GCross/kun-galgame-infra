package service

import (
	"context"

	"api/internal/platform/game/model"
	"api/internal/platform/game/repository"
	"api/pkg/utils"
)

// GameService handles game business logic
type GameService struct {
	gameRepo *repository.GameRepository
}

// NewGameService creates a new GameService
func NewGameService(gameRepo *repository.GameRepository) *GameService {
	return &GameService{gameRepo: gameRepo}
}

// GetByID gets a game by ID
func (s *GameService) GetByID(ctx context.Context, id uint) (*model.Game, error) {
	return s.gameRepo.FindByID(ctx, id)
}

// GetByUUID gets a game by UUID
func (s *GameService) GetByUUID(ctx context.Context, uuid string) (*model.Game, error) {
	return s.gameRepo.FindByUUID(ctx, uuid)
}

// Create creates a new game
func (s *GameService) Create(ctx context.Context, game *model.Game) error {
	return s.gameRepo.Create(ctx, game)
}

// Update updates a game
func (s *GameService) Update(ctx context.Context, game *model.Game) error {
	return s.gameRepo.Update(ctx, game)
}

// Delete deletes a game
func (s *GameService) Delete(ctx context.Context, id uint) error {
	return s.gameRepo.Delete(ctx, id)
}

// List lists games with pagination
func (s *GameService) List(ctx context.Context, p utils.Pagination) (utils.PaginatedResult[model.Game], error) {
	p.Normalize()
	games, total, err := s.gameRepo.List(ctx, p.Offset(), p.Limit())
	if err != nil {
		return utils.PaginatedResult[model.Game]{}, err
	}
	return utils.NewPaginatedResult(games, total, p.Page, p.PageSize), nil
}

// Search searches games
func (s *GameService) Search(ctx context.Context, query string, p utils.Pagination) (utils.PaginatedResult[model.Game], error) {
	p.Normalize()
	games, total, err := s.gameRepo.Search(ctx, query, p.Offset(), p.Limit())
	if err != nil {
		return utils.PaginatedResult[model.Game]{}, err
	}
	return utils.NewPaginatedResult(games, total, p.Page, p.PageSize), nil
}
