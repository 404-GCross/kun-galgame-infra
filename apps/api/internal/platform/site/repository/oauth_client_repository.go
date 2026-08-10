package repository

import (
	"context"

	"api/internal/platform/site/model"

	"gorm.io/gorm"
)

type OAuthClientRepository struct {
	db *gorm.DB
}

func NewOAuthClientRepository(db *gorm.DB) *OAuthClientRepository {
	return &OAuthClientRepository{db: db}
}

func (r *OAuthClientRepository) Create(ctx context.Context, client *model.OAuthClient) error {
	return r.db.WithContext(ctx).Create(client).Error
}

func (r *OAuthClientRepository) FindByClientID(ctx context.Context, clientID string) (*model.OAuthClient, error) {
	var client model.OAuthClient
	if err := r.db.WithContext(ctx).Where("id = ?", clientID).First(&client).Error; err != nil {
		return nil, err
	}
	return &client, nil
}

func (r *OAuthClientRepository) FindByClientIDWithSite(ctx context.Context, clientID string) (*model.OAuthClient, error) {
	var client model.OAuthClient
	if err := r.db.WithContext(ctx).Preload("Site").Where("id = ?", clientID).First(&client).Error; err != nil {
		return nil, err
	}
	return &client, nil
}

func (r *OAuthClientRepository) FindBySiteID(ctx context.Context, siteID uint) ([]model.OAuthClient, error) {
	var clients []model.OAuthClient
	if err := r.db.WithContext(ctx).Where("site_id = ?", siteID).Find(&clients).Error; err != nil {
		return nil, err
	}
	return clients, nil
}

func (r *OAuthClientRepository) FindAll(ctx context.Context) ([]model.OAuthClient, error) {
	var clients []model.OAuthClient
	if err := r.db.WithContext(ctx).Find(&clients).Error; err != nil {
		return nil, err
	}
	return clients, nil
}

func (r *OAuthClientRepository) FindAllByCreator(ctx context.Context, userID uint) ([]model.OAuthClient, error) {
	var clients []model.OAuthClient
	if err := r.db.WithContext(ctx).Where("created_by_user_id = ?", userID).Find(&clients).Error; err != nil {
		return nil, err
	}
	return clients, nil
}

func (r *OAuthClientRepository) FindBySiteIDAndCreator(ctx context.Context, siteID, userID uint) ([]model.OAuthClient, error) {
	var clients []model.OAuthClient
	if err := r.db.WithContext(ctx).
		Where("site_id = ? AND created_by_user_id = ?", siteID, userID).
		Find(&clients).Error; err != nil {
		return nil, err
	}
	return clients, nil
}

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

func (r *OAuthClientRepository) Update(ctx context.Context, client *model.OAuthClient) error {
	return r.db.WithContext(ctx).Save(client).Error
}

func (r *OAuthClientRepository) Delete(ctx context.Context, clientID string) error {
	return r.db.WithContext(ctx).Where("id = ?", clientID).Delete(&model.OAuthClient{}).Error
}
