package repository

import (
	"context"

	"api/internal/platform/game/model"

	"gorm.io/gorm"
)

// GameRepository handles game data access
type GameRepository struct {
	db *gorm.DB
}

// NewGameRepository creates a new GameRepository
func NewGameRepository(db *gorm.DB) *GameRepository {
	return &GameRepository{db: db}
}

// FindByID finds a game by ID
func (r *GameRepository) FindByID(ctx context.Context, id uint) (*model.Game, error) {
	var game model.Game
	if err := r.db.WithContext(ctx).Preload("Tags").First(&game, id).Error; err != nil {
		return nil, err
	}
	return &game, nil
}

// FindByUUID finds a game by UUID
func (r *GameRepository) FindByUUID(ctx context.Context, uuid string) (*model.Game, error) {
	var game model.Game
	if err := r.db.WithContext(ctx).Preload("Tags").Where("uuid = ?", uuid).First(&game).Error; err != nil {
		return nil, err
	}
	return &game, nil
}

// Create creates a new game
func (r *GameRepository) Create(ctx context.Context, game *model.Game) error {
	return r.db.WithContext(ctx).Create(game).Error
}

// Update updates a game
func (r *GameRepository) Update(ctx context.Context, game *model.Game) error {
	return r.db.WithContext(ctx).Save(game).Error
}

// Delete soft deletes a game
func (r *GameRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&model.Game{}, id).Error
}

// List lists games with pagination
func (r *GameRepository) List(ctx context.Context, offset, limit int) ([]model.Game, int64, error) {
	var games []model.Game
	var total int64

	if err := r.db.WithContext(ctx).Model(&model.Game{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if err := r.db.WithContext(ctx).Preload("Tags").Offset(offset).Limit(limit).Find(&games).Error; err != nil {
		return nil, 0, err
	}

	return games, total, nil
}

// Search searches games by name
func (r *GameRepository) Search(ctx context.Context, query string, offset, limit int) ([]model.Game, int64, error) {
	var games []model.Game
	var total int64

	q := r.db.WithContext(ctx).Model(&model.Game{}).Where("name ILIKE ?", "%"+query+"%")

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if err := q.Preload("Tags").Offset(offset).Limit(limit).Find(&games).Error; err != nil {
		return nil, 0, err
	}

	return games, total, nil
}
