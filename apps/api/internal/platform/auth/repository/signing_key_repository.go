package repository

import (
	"context"

	"api/internal/platform/auth/model"

	"gorm.io/gorm"
)

type SigningKeyRepository struct {
	db *gorm.DB
}

func NewSigningKeyRepository(db *gorm.DB) *SigningKeyRepository {
	return &SigningKeyRepository{db: db}
}

func (r *SigningKeyRepository) Create(ctx context.Context, k *model.SigningKey) error {
	return r.db.WithContext(ctx).Create(k).Error
}

func (r *SigningKeyRepository) FindActive(ctx context.Context, alg string) (*model.SigningKey, error) {
	var k model.SigningKey
	if err := r.db.WithContext(ctx).
		Where("alg = ? AND state = ?", alg, "active").
		First(&k).Error; err != nil {
		return nil, err
	}
	return &k, nil
}

func (r *SigningKeyRepository) FindPublished(ctx context.Context) ([]model.SigningKey, error) {
	var keys []model.SigningKey
	err := r.db.WithContext(ctx).
		Where("state IN ?", []string{"pending", "active", "retired"}).
		Order("created_at ASC").
		Find(&keys).Error
	return keys, err
}

func (r *SigningKeyRepository) CountActive(ctx context.Context, alg string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.SigningKey{}).
		Where("alg = ? AND state = ?", alg, "active").
		Count(&n).Error
	return n, err
}
