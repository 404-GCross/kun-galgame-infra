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

type ImageRepository struct {
	db *gorm.DB
}

func NewImageRepository(db *gorm.DB) *ImageRepository {
	return &ImageRepository{db: db}
}

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

func (r *ImageRepository) Create(ctx context.Context, img *model.Image) (bool, error) {
	res := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "hash"}}, DoNothing: true}).
		Create(img)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

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

func (r *ImageRepository) Resurrect(ctx context.Context, hash string) error {
	return r.db.WithContext(ctx).
		Model(&model.Image{}).
		Where("hash = ?", hash).
		Updates(map[string]any{"deleted_at": nil, "last_referenced_at": time.Now()}).
		Error
}

func (r *ImageRepository) UpdateVariants(ctx context.Context, hash string, variants []string) error {
	img := &model.Image{}
	img.SetVariants(variants)
	return r.db.WithContext(ctx).
		Model(&model.Image{}).
		Where("hash = ?", hash).
		Update("variants", img.Variants).
		Error
}

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

type ImageMeta struct {
	Hash      string `gorm:"column:hash" json:"-"`
	Width     int    `gorm:"column:width" json:"width"`
	Height    int    `gorm:"column:height" json:"height"`
	Thumbhash string `gorm:"column:thumbhash" json:"thumbhash,omitempty"`
}

func (r *ImageRepository) MetaByHashes(ctx context.Context, hashes []string) (map[string]ImageMeta, error) {
	out := make(map[string]ImageMeta, len(hashes))
	if len(hashes) == 0 {
		return out, nil
	}
	var rows []ImageMeta
	if err := r.db.WithContext(ctx).
		Model(&model.Image{}).
		Select("hash", "width", "height", "thumbhash").
		Where("hash IN ? AND deleted_at IS NULL", hashes).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, m := range rows {
		out[m.Hash] = m
	}
	return out, nil
}

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
