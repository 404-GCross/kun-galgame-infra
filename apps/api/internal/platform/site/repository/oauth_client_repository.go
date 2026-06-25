package repository

import (
	"context"

	"api/internal/platform/site/model"

	"gorm.io/gorm"
)

// OAuthClientRepository handles OAuth client data access
type OAuthClientRepository struct {
	db *gorm.DB
}

// NewOAuthClientRepository creates a new OAuthClientRepository
func NewOAuthClientRepository(db *gorm.DB) *OAuthClientRepository {
	return &OAuthClientRepository{db: db}
}

// Create creates a new OAuth client
func (r *OAuthClientRepository) Create(ctx context.Context, client *model.OAuthClient) error {
	return r.db.WithContext(ctx).Create(client).Error
}

// FindByClientID finds an OAuth client by client ID
func (r *OAuthClientRepository) FindByClientID(ctx context.Context, clientID string) (*model.OAuthClient, error) {
	var client model.OAuthClient
	if err := r.db.WithContext(ctx).Where("id = ?", clientID).First(&client).Error; err != nil {
		return nil, err
	}
	return &client, nil
}

// FindByClientIDWithSite is FindByClientID with the parent Site preloaded.
// Used by GetClientPublicInfo so the public metadata endpoint can return
// the site_domain in one query instead of two.
func (r *OAuthClientRepository) FindByClientIDWithSite(ctx context.Context, clientID string) (*model.OAuthClient, error) {
	var client model.OAuthClient
	if err := r.db.WithContext(ctx).Preload("Site").Where("id = ?", clientID).First(&client).Error; err != nil {
		return nil, err
	}
	return &client, nil
}

// FindBySiteID finds all OAuth clients for a site
func (r *OAuthClientRepository) FindBySiteID(ctx context.Context, siteID uint) ([]model.OAuthClient, error) {
	var clients []model.OAuthClient
	if err := r.db.WithContext(ctx).Where("site_id = ?", siteID).Find(&clients).Error; err != nil {
		return nil, err
	}
	return clients, nil
}

// FindAll returns all OAuth clients
func (r *OAuthClientRepository) FindAll(ctx context.Context) ([]model.OAuthClient, error) {
	var clients []model.OAuthClient
	if err := r.db.WithContext(ctx).Find(&clients).Error; err != nil {
		return nil, err
	}
	return clients, nil
}

// FindListed returns the opt-in (listed) clients for the public app directory,
// Site preloaded. Ordered official (auto_consent) first, then display_order,
// then name. See GET /oauth/ecosystem.
func (r *OAuthClientRepository) FindListed(ctx context.Context) ([]model.OAuthClient, error) {
	var clients []model.OAuthClient
	if err := r.db.WithContext(ctx).
		Preload("Site").
		Where("listed = ?", true).
		Order("auto_consent DESC, display_order ASC, name ASC").
		Find(&clients).Error; err != nil {
		return nil, err
	}
	return clients, nil
}

// Update updates an OAuth client
func (r *OAuthClientRepository) Update(ctx context.Context, client *model.OAuthClient) error {
	return r.db.WithContext(ctx).Save(client).Error
}

// Delete deletes an OAuth client
func (r *OAuthClientRepository) Delete(ctx context.Context, clientID string) error {
	return r.db.WithContext(ctx).Where("id = ?", clientID).Delete(&model.OAuthClient{}).Error
}
