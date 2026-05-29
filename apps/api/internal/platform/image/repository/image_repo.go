package repository

import (
	"context"
	"errors"
	"time"

	"api/internal/platform/image/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// ImageRepository handles CRUD for the images table.
type ImageRepository struct {
	db *gorm.DB
}

func NewImageRepository(db *gorm.DB) *ImageRepository {
	return &ImageRepository{db: db}
}

// FindByHash returns (nil, nil) if not found. "not found" is the hot path
// on upload (dedup miss) so we silence the default GORM logger emission.
func (r *ImageRepository) FindByHash(ctx context.Context, hash string) (*model.Image, error) {
	var img model.Image
	err := r.db.WithContext(ctx).
		Session(&gorm.Session{Logger: r.db.Logger.LogMode(logger.Silent)}).
		Where("hash = ? AND deleted_at IS NULL", hash).
		First(&img).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &img, nil
}

// Create inserts a new Image, deduplicating on hash. Returns inserted=true
// when a row was actually written, false when an identical hash already
// existed (the INSERT became a no-op via ON CONFLICT DO NOTHING). The caller
// converges the loser of a concurrent first-upload race on the winning row
// instead of surfacing the unique-index violation as a 500.
func (r *ImageRepository) Create(ctx context.Context, img *model.Image) (bool, error) {
	res := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "hash"}}, DoNothing: true}).
		Create(img)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// FindByHashIncludingDeleted returns the row even if it is soft-deleted
// (deleted_at set). Used by Upload to revive a soft-deleted hash rather than
// INSERTing a duplicate that would collide with the unique index. Returns
// (nil, nil) when the hash has never existed.
func (r *ImageRepository) FindByHashIncludingDeleted(ctx context.Context, hash string) (*model.Image, error) {
	var img model.Image
	err := r.db.WithContext(ctx).
		Session(&gorm.Session{Logger: r.db.Logger.LogMode(logger.Silent)}).
		Where("hash = ?", hash).
		First(&img).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &img, nil
}

// Resurrect clears deleted_at and refreshes last_referenced_at for a hash,
// bringing a soft-deleted image back to life on re-upload.
func (r *ImageRepository) Resurrect(ctx context.Context, hash string) error {
	return r.db.WithContext(ctx).
		Model(&model.Image{}).
		Where("hash = ?", hash).
		Updates(map[string]any{"deleted_at": nil, "last_referenced_at": time.Now()}).
		Error
}

// UpdateVariants atomically replaces the variants JSONB column for the
// given hash.
func (r *ImageRepository) UpdateVariants(ctx context.Context, hash string, variants []string) error {
	img := &model.Image{}
	img.SetVariants(variants)
	return r.db.WithContext(ctx).
		Model(&model.Image{}).
		Where("hash = ?", hash).
		Update("variants", img.Variants).
		Error
}

// TouchReferenced updates last_referenced_at for a set of hashes. Returns
// the number of rows that actually matched.
func (r *ImageRepository) TouchReferenced(ctx context.Context, hashes []string) (int64, error) {
	if len(hashes) == 0 {
		return 0, nil
	}
	res := r.db.WithContext(ctx).
		Model(&model.Image{}).
		Where("hash IN ? AND deleted_at IS NULL", hashes).
		Update("last_referenced_at", time.Now())
	return res.RowsAffected, res.Error
}

// FindExistingHashes returns the subset of the given hashes that exist and
// are not soft-deleted. Used by reference-ping to report "not_found".
func (r *ImageRepository) FindExistingHashes(ctx context.Context, hashes []string) ([]string, error) {
	if len(hashes) == 0 {
		return nil, nil
	}
	var out []string
	err := r.db.WithContext(ctx).
		Model(&model.Image{}).
		Where("hash IN ? AND deleted_at IS NULL", hashes).
		Pluck("hash", &out).
		Error
	return out, err
}
