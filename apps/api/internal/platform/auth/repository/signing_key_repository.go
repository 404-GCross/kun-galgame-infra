package repository

import (
	"context"

	"api/internal/platform/auth/model"

	"gorm.io/gorm"
)

// SigningKeyRepository persists OIDC signing keys (the `signing_keys` table).
type SigningKeyRepository struct {
	db *gorm.DB
}

// NewSigningKeyRepository creates a new SigningKeyRepository.
func NewSigningKeyRepository(db *gorm.DB) *SigningKeyRepository {
	return &SigningKeyRepository{db: db}
}

// Create inserts a new signing key.
func (r *SigningKeyRepository) Create(ctx context.Context, k *model.SigningKey) error {
	return r.db.WithContext(ctx).Create(k).Error
}

// FindActive returns the single active signing key for an alg, or
// gorm.ErrRecordNotFound when none is active.
func (r *SigningKeyRepository) FindActive(ctx context.Context, alg string) (*model.SigningKey, error) {
	var k model.SigningKey
	if err := r.db.WithContext(ctx).
		Where("alg = ? AND state = ?", alg, "active").
		First(&k).Error; err != nil {
		return nil, err
	}
	return &k, nil
}

// FindPublished returns all keys whose public material must appear in the JWKS
// (pending, active, retired) — everything except deleted keys — oldest first.
func (r *SigningKeyRepository) FindPublished(ctx context.Context) ([]model.SigningKey, error) {
	var keys []model.SigningKey
	err := r.db.WithContext(ctx).
		Where("state IN ?", []string{"pending", "active", "retired"}).
		Order("created_at ASC").
		Find(&keys).Error
	return keys, err
}

// CountActive counts active keys for an alg — the bootstrap idempotence guard.
func (r *SigningKeyRepository) CountActive(ctx context.Context, alg string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.SigningKey{}).
		Where("alg = ? AND state = ?", alg, "active").
		Count(&n).Error
	return n, err
}
