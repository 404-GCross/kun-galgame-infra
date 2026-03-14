package repository

import (
	"context"
	"time"

	"api/internal/platform/auth/model"

	"gorm.io/gorm"
)

// AuthorizationCodeRepository handles authorization code data access
type AuthorizationCodeRepository struct {
	db *gorm.DB
}

// NewAuthorizationCodeRepository creates a new AuthorizationCodeRepository
func NewAuthorizationCodeRepository(db *gorm.DB) *AuthorizationCodeRepository {
	return &AuthorizationCodeRepository{db: db}
}

// Create creates a new authorization code
func (r *AuthorizationCodeRepository) Create(ctx context.Context, code *model.AuthorizationCode) error {
	return r.db.WithContext(ctx).Create(code).Error
}

// FindByCode finds an authorization code by code string
func (r *AuthorizationCodeRepository) FindByCode(ctx context.Context, code string) (*model.AuthorizationCode, error) {
	var authCode model.AuthorizationCode
	if err := r.db.WithContext(ctx).Where("code = ?", code).First(&authCode).Error; err != nil {
		return nil, err
	}
	return &authCode, nil
}

// FindValidByCode finds a valid (not expired, not used) authorization code
func (r *AuthorizationCodeRepository) FindValidByCode(ctx context.Context, code string) (*model.AuthorizationCode, error) {
	var authCode model.AuthorizationCode
	if err := r.db.WithContext(ctx).
		Where("code = ?", code).
		Where("expires_at > ?", time.Now()).
		Where("used_at IS NULL").
		First(&authCode).Error; err != nil {
		return nil, err
	}
	return &authCode, nil
}

// MarkAsUsed marks an authorization code as used
func (r *AuthorizationCodeRepository) MarkAsUsed(ctx context.Context, id uint) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&model.AuthorizationCode{}).
		Where("id = ?", id).
		Update("used_at", now).Error
}

// DeleteExpired deletes expired authorization codes
func (r *AuthorizationCodeRepository) DeleteExpired(ctx context.Context) error {
	return r.db.WithContext(ctx).
		Where("expires_at < ?", time.Now()).
		Delete(&model.AuthorizationCode{}).Error
}

// DeleteByUserID deletes all authorization codes for a user
func (r *AuthorizationCodeRepository) DeleteByUserID(ctx context.Context, userID uint) error {
	return r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Delete(&model.AuthorizationCode{}).Error
}
