package repository

import (
	"context"

	"api/internal/platform/galgame/dto"

	"gorm.io/gorm"
)

// UserReadonlyRepository provides read-only access to user data in kun_oauth_admin
type UserReadonlyRepository struct {
	db *gorm.DB
}

// NewUserReadonlyRepository creates a new UserReadonlyRepository
func NewUserReadonlyRepository(db *gorm.DB) *UserReadonlyRepository {
	return &UserReadonlyRepository{db: db}
}

// FindByID returns minimal user info for display
func (r *UserReadonlyRepository) FindByID(ctx context.Context, id int) (*dto.UserBrief, error) {
	var user dto.UserBrief
	err := r.db.WithContext(ctx).
		Table("users").
		Select("id, name, avatar").
		Where("id = ?", id).
		First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// FindByIDs returns minimal user info for multiple users
func (r *UserReadonlyRepository) FindByIDs(ctx context.Context, ids []int) (map[int]*dto.UserBrief, error) {
	if len(ids) == 0 {
		return make(map[int]*dto.UserBrief), nil
	}

	var users []dto.UserBrief
	err := r.db.WithContext(ctx).
		Table("users").
		Select("id, name, avatar").
		Where("id IN ?", ids).
		Find(&users).Error
	if err != nil {
		return nil, err
	}

	result := make(map[int]*dto.UserBrief, len(users))
	for i := range users {
		result[users[i].ID] = &users[i]
	}
	return result, nil
}
