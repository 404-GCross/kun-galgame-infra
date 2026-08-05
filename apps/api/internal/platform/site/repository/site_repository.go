package repository

import (
	"context"

	"api/internal/platform/site/model"

	"gorm.io/gorm"
)

// SiteRepository handles site data access
type SiteRepository struct {
	db *gorm.DB
}

// NewSiteRepository creates a new SiteRepository
func NewSiteRepository(db *gorm.DB) *SiteRepository {
	return &SiteRepository{db: db}
}

// FindByID finds a site by ID
func (r *SiteRepository) FindByID(ctx context.Context, id uint) (*model.Site, error) {
	var site model.Site
	if err := r.db.WithContext(ctx).First(&site, id).Error; err != nil {
		return nil, err
	}
	return &site, nil
}

// FindByDomain finds a site by domain
func (r *SiteRepository) FindByDomain(ctx context.Context, domain string) (*model.Site, error) {
	var site model.Site
	if err := r.db.WithContext(ctx).Where("domain = ?", domain).First(&site).Error; err != nil {
		return nil, err
	}
	return &site, nil
}

// Create creates a new site
func (r *SiteRepository) Create(ctx context.Context, site *model.Site) error {
	return r.db.WithContext(ctx).Create(site).Error
}

// Update updates a site
func (r *SiteRepository) Update(ctx context.Context, site *model.Site) error {
	return r.db.WithContext(ctx).Save(site).Error
}

// Delete deletes a site
func (r *SiteRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&model.Site{}, id).Error
}

// List lists all sites
func (r *SiteRepository) List(ctx context.Context) ([]model.Site, error) {
	var sites []model.Site
	if err := r.db.WithContext(ctx).Find(&sites).Error; err != nil {
		return nil, err
	}
	return sites, nil
}

// ListByCreator lists the sites a given admin created. Rows with a NULL
// creator (pre-ownership) never match — only a manage_all caller sees those,
// and that caller goes through List instead.
func (r *SiteRepository) ListByCreator(ctx context.Context, userID uint) ([]model.Site, error) {
	var sites []model.Site
	if err := r.db.WithContext(ctx).Where("created_by_user_id = ?", userID).Find(&sites).Error; err != nil {
		return nil, err
	}
	return sites, nil
}

// ListOAuthClients lists all OAuth clients
func (r *SiteRepository) ListOAuthClients(ctx context.Context) ([]model.OAuthClient, error) {
	var clients []model.OAuthClient
	if err := r.db.WithContext(ctx).Find(&clients).Error; err != nil {
		return nil, err
	}
	return clients, nil
}
