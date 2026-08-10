package repository

import (
	"context"

	"api/internal/platform/site/model"

	"gorm.io/gorm"
)

type SiteRepository struct {
	db *gorm.DB
}

func NewSiteRepository(db *gorm.DB) *SiteRepository {
	return &SiteRepository{db: db}
}

func (r *SiteRepository) FindByID(ctx context.Context, id uint) (*model.Site, error) {
	var site model.Site
	if err := r.db.WithContext(ctx).First(&site, id).Error; err != nil {
		return nil, err
	}
	return &site, nil
}

func (r *SiteRepository) FindByDomain(ctx context.Context, domain string) (*model.Site, error) {
	var site model.Site
	if err := r.db.WithContext(ctx).Where("domain = ?", domain).First(&site).Error; err != nil {
		return nil, err
	}
	return &site, nil
}

func (r *SiteRepository) Create(ctx context.Context, site *model.Site) error {
	return r.db.WithContext(ctx).Create(site).Error
}

func (r *SiteRepository) Update(ctx context.Context, site *model.Site) error {
	return r.db.WithContext(ctx).Save(site).Error
}

func (r *SiteRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&model.Site{}, id).Error
}

func (r *SiteRepository) List(ctx context.Context) ([]model.Site, error) {
	var sites []model.Site
	if err := r.db.WithContext(ctx).Find(&sites).Error; err != nil {
		return nil, err
	}
	return sites, nil
}

func (r *SiteRepository) ListByCreator(ctx context.Context, userID uint) ([]model.Site, error) {
	var sites []model.Site
	if err := r.db.WithContext(ctx).Where("created_by_user_id = ?", userID).Find(&sites).Error; err != nil {
		return nil, err
	}
	return sites, nil
}

func (r *SiteRepository) ListOAuthClients(ctx context.Context) ([]model.OAuthClient, error) {
	var clients []model.OAuthClient
	if err := r.db.WithContext(ctx).Find(&clients).Error; err != nil {
		return nil, err
	}
	return clients, nil
}
