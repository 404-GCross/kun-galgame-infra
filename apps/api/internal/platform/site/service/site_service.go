package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"

	"api/internal/platform/site/model"
	"api/internal/platform/site/repository"
)

// SiteService handles site business logic
type SiteService struct {
	siteRepo        *repository.SiteRepository
	oauthClientRepo *repository.OAuthClientRepository
}

// NewSiteService creates a new SiteService
func NewSiteService(siteRepo *repository.SiteRepository, oauthClientRepo *repository.OAuthClientRepository) *SiteService {
	return &SiteService{
		siteRepo:        siteRepo,
		oauthClientRepo: oauthClientRepo,
	}
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

// DomainExists checks if a domain already exists
func (s *SiteService) DomainExists(ctx context.Context, domain string) bool {
	site, err := s.siteRepo.FindByDomain(ctx, domain)
	return err == nil && site != nil
}

// ListOAuthClients lists all OAuth clients
func (s *SiteService) ListOAuthClients(ctx context.Context) ([]model.OAuthClient, error) {
	return s.oauthClientRepo.FindAll(ctx)
}

// GetOAuthClientsBySiteID gets OAuth clients for a specific site
func (s *SiteService) GetOAuthClientsBySiteID(ctx context.Context, siteID uint) ([]model.OAuthClient, error) {
	return s.oauthClientRepo.FindBySiteID(ctx, siteID)
}

// CreateOAuthClient creates a new OAuth client with generated ID and secret.
//
//   - allowedScopes: scope allow-list (nil → not stored, falls back to OIDC core at runtime).
//   - isPublic:      public client flag (SPA / PKCE-only, no client_secret on refresh).
//   - refreshTokenTTLSeconds: per-client refresh_token lifetime in seconds; nil → DB default (90d).
func (s *SiteService) CreateOAuthClient(ctx context.Context, siteID uint, name string, redirectURIs, grants, allowedScopes []string, isPublic bool, refreshTokenTTLSeconds *int) (*model.OAuthClient, string, error) {
	// Generate client ID (16 bytes = 32 hex chars)
	clientID, err := generateRandomHex(16)
	if err != nil {
		return nil, "", err
	}

	// Generate client secret (32 bytes = 64 hex chars)
	secret, err := generateRandomHex(32)
	if err != nil {
		return nil, "", err
	}

	urisJSON, _ := json.Marshal(redirectURIs)
	grantsJSON, _ := json.Marshal(grants)

	client := &model.OAuthClient{
		ID:           clientID,
		SiteID:       &siteID,
		Name:         name,
		Secret:       secret,
		RedirectURIs: urisJSON,
		Grants:       grantsJSON,
		IsPublic:     isPublic,
	}
	if allowedScopes != nil {
		scopesJSON, _ := json.Marshal(allowedScopes)
		client.AllowedScopes = scopesJSON
	}
	if refreshTokenTTLSeconds != nil {
		client.RefreshTokenTTLSeconds = *refreshTokenTTLSeconds
	}

	if err := s.oauthClientRepo.Create(ctx, client); err != nil {
		return nil, "", err
	}

	return client, secret, nil
}

// UpdateOAuthClient updates an OAuth client's name, redirect URIs,
// grants, allowed_scopes, and/or refresh_token_ttl. Nil pointer/slice
// = no change. is_public is intentionally NOT updatable (changing it
// retroactively breaks the auth-model invariants for existing tokens).
func (s *SiteService) UpdateOAuthClient(ctx context.Context, clientID string, name *string, redirectURIs, grants, allowedScopes []string, refreshTokenTTLSeconds *int) (*model.OAuthClient, error) {
	client, err := s.oauthClientRepo.FindByClientID(ctx, clientID)
	if err != nil {
		return nil, err
	}

	if name != nil {
		client.Name = *name
	}
	if redirectURIs != nil {
		urisJSON, _ := json.Marshal(redirectURIs)
		client.RedirectURIs = urisJSON
	}
	if grants != nil {
		grantsJSON, _ := json.Marshal(grants)
		client.Grants = grantsJSON
	}
	if allowedScopes != nil {
		scopesJSON, _ := json.Marshal(allowedScopes)
		client.AllowedScopes = scopesJSON
	}
	if refreshTokenTTLSeconds != nil {
		client.RefreshTokenTTLSeconds = *refreshTokenTTLSeconds
	}

	if err := s.oauthClientRepo.Update(ctx, client); err != nil {
		return nil, err
	}
	return client, nil
}

// DeleteOAuthClient deletes an OAuth client
func (s *SiteService) DeleteOAuthClient(ctx context.Context, clientID string) error {
	return s.oauthClientRepo.Delete(ctx, clientID)
}

// generateRandomHex generates a random hex string of the given byte length
func generateRandomHex(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
