package service

import (
	"context"

	"api/internal/platform/site/model"
	"api/internal/platform/site/repository"
)

// SiteService handles site business logic
type SiteService struct {
	siteRepo *repository.SiteRepository
}

// NewSiteService creates a new SiteService
func NewSiteService(siteRepo *repository.SiteRepository) *SiteService {
	return &SiteService{siteRepo: siteRepo}
}

// GetByID gets a site by ID
func (s *SiteService) GetByID(ctx context.Context, id uint) (*model.Site, error) {
	return s.siteRepo.FindByID(ctx, id)
}

// GetByDomain gets a site by domain
func (s *SiteService) GetByDomain(ctx context.Context, domain string) (*model.Site, error) {
	return s.siteRepo.FindByDomain(ctx, domain)
}

// Create creates a new site
func (s *SiteService) Create(ctx context.Context, site *model.Site) error {
	return s.siteRepo.Create(ctx, site)
}

// Update updates a site
func (s *SiteService) Update(ctx context.Context, site *model.Site) error {
	return s.siteRepo.Update(ctx, site)
}

// Delete deletes a site
func (s *SiteService) Delete(ctx context.Context, id uint) error {
	return s.siteRepo.Delete(ctx, id)
}

// List lists all sites
func (s *SiteService) List(ctx context.Context) ([]model.Site, error) {
	return s.siteRepo.List(ctx)
}

// ListOAuthClients lists all OAuth clients
func (s *SiteService) ListOAuthClients(ctx context.Context) ([]model.OAuthClient, error) {
	return s.siteRepo.ListOAuthClients(ctx)
}
