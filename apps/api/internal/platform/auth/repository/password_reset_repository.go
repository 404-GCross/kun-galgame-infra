package repository

import (
	"context"
	"time"

	"api/internal/platform/auth/model"

	"gorm.io/gorm"
)

// PasswordResetRepository handles password reset data access
type PasswordResetRepository struct {
	db *gorm.DB
}

// NewPasswordResetRepository creates a new PasswordResetRepository
func NewPasswordResetRepository(db *gorm.DB) *PasswordResetRepository {
	return &PasswordResetRepository{db: db}
}

// Create creates a new password reset token
func (r *PasswordResetRepository) Create(ctx context.Context, reset *model.PasswordReset) error {
	return r.db.WithContext(ctx).Create(reset).Error
}

// FindByToken finds a password reset by token
func (r *PasswordResetRepository) FindByToken(ctx context.Context, token string) (*model.PasswordReset, error) {
	var reset model.PasswordReset
	if err := r.db.WithContext(ctx).Where("token = ?", token).First(&reset).Error; err != nil {
		return nil, err
	}
	return &reset, nil
}

// FindValidByToken finds a valid (not expired, not used) password reset by token
func (r *PasswordResetRepository) FindValidByToken(ctx context.Context, token string) (*model.PasswordReset, error) {
	var reset model.PasswordReset
	if err := r.db.WithContext(ctx).
		Where("token = ?", token).
		Where("expires_at > ?", time.Now()).
		Where("used_at IS NULL").
		First(&reset).Error; err != nil {
		return nil, err
	}
	return &reset, nil
}

// MarkAsUsed marks a password reset as used
func (r *PasswordResetRepository) MarkAsUsed(ctx context.Context, id uint) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&model.PasswordReset{}).
		Where("id = ?", id).
		Update("used_at", now).Error
}

// DeleteExpired deletes expired password reset tokens
func (r *PasswordResetRepository) DeleteExpired(ctx context.Context) error {
	return r.db.WithContext(ctx).
		Where("expires_at < ?", time.Now()).
		Delete(&model.PasswordReset{}).Error
}

// DeleteByUserID deletes all password reset tokens for a user
func (r *PasswordResetRepository) DeleteByUserID(ctx context.Context, userID uint) error {
	return r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Delete(&model.PasswordReset{}).Error
}
