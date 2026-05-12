package repository

import (
	"context"
	"errors"
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

// ErrCodeAlreadyUsed signals a concurrent / replayed code-exchange attempt:
// the row exists but used_at is already set, so this caller did NOT win
// the race. The service layer must surface this as ErrOAuthInvalidCode
// and not proceed to issue tokens.
var ErrCodeAlreadyUsed = errors.New("authorization code already used")

// MarkAsUsed atomically claims an authorization code by setting used_at
// only when it is still NULL. Returns ErrCodeAlreadyUsed if another
// caller (or process) already claimed the same code — this is the
// race-safety guarantee that ExchangeCode relies on.
//
// Why this matters: ExchangeCode is FindValid → validate → MarkAsUsed.
// Two concurrent token exchanges with the same code can both pass
// FindValid (both see used_at=NULL) and both pass validation. The atomic
// `UPDATE ... WHERE used_at IS NULL` plus RowsAffected check is what
// guarantees only one of them succeeds; the loser gets ErrCodeAlreadyUsed.
func (r *AuthorizationCodeRepository) MarkAsUsed(ctx context.Context, id uint) error {
	now := time.Now()
	res := r.db.WithContext(ctx).Model(&model.AuthorizationCode{}).
		Where("id = ? AND used_at IS NULL", id).
		Update("used_at", now)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrCodeAlreadyUsed
	}
	return nil
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
