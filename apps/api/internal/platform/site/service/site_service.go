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

// ListByCreator lists the sites created by one admin (console ownership
// scope — see model.Site.CreatedByUserID).
func (s *SiteService) ListByCreator(ctx context.Context, userID uint) ([]model.Site, error) {
	return s.siteRepo.ListByCreator(ctx, userID)
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

// ListOAuthClientsByCreator lists the clients created by one admin.
func (s *SiteService) ListOAuthClientsByCreator(ctx context.Context, userID uint) ([]model.OAuthClient, error) {
	return s.oauthClientRepo.FindAllByCreator(ctx, userID)
}

// GetOAuthClientsBySiteID gets OAuth clients for a specific site
func (s *SiteService) GetOAuthClientsBySiteID(ctx context.Context, siteID uint) ([]model.OAuthClient, error) {
	return s.oauthClientRepo.FindBySiteID(ctx, siteID)
}

// GetOAuthClientsBySiteIDAndCreator is GetOAuthClientsBySiteID narrowed to the
// caller's own clients.
func (s *SiteService) GetOAuthClientsBySiteIDAndCreator(ctx context.Context, siteID, userID uint) ([]model.OAuthClient, error) {
	return s.oauthClientRepo.FindBySiteIDAndCreator(ctx, siteID, userID)
}

// CreateOAuthClient creates a new OAuth client with generated ID and secret.
//
//   - allowedScopes: scope allow-list (nil → not stored, falls back to OIDC core at runtime).
//   - isPublic:      public client flag (SPA / PKCE-only, no client_secret on refresh).
//   - autoConsent:   first-party flag (skips /oauth/authorize consent UI). Defaults
//                    false — only enable for clients you fully own.
//   - refreshTokenTTLSeconds: per-client refresh_token lifetime in seconds; nil → DB default (90d).
//   - listed/logoURL/tagline/displayOrder: public app-directory display fields
//     (GET /oauth/ecosystem). Plain metadata, not ren-gated.
//   - createdBy: the admin creating it; stamped for the console ownership scope
//     (nil only when there is no authenticated caller, e.g. a seeder).
func (s *SiteService) CreateOAuthClient(ctx context.Context, siteID uint, name string, redirectURIs, grants, allowedScopes []string, isPublic, autoConsent bool, refreshTokenTTLSeconds *int, listed bool, logoURL, tagline string, displayOrder int, createdBy *uint) (*model.OAuthClient, string, error) {
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
		ID:     clientID,
		SiteID: &siteID,
		Name:   name,
		// Store only the hash; the plaintext `secret` is returned to the admin
		// once below and never persisted.
		Secret:          model.HashOAuthClientSecret(secret),
		RedirectURIs:    urisJSON,
		Grants:          grantsJSON,
		IsPublic:        isPublic,
		AutoConsent:     autoConsent,
		CreatedByUserID: createdBy,
		Listed:          listed,
		LogoURL:         logoURL,
		Tagline:         tagline,
		DisplayOrder:    displayOrder,
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
// grants, allowed_scopes, auto_consent, and/or refresh_token_ttl. Nil
// pointer/slice = no change. is_public is intentionally NOT updatable
// (changing it retroactively breaks the auth-model invariants for
// existing tokens). auto_consent IS updatable — its only effect is on
// the consent-screen rendering, no token semantics change.
//
// listed/logoURL/tagline/displayOrder are the public app-directory display
// fields — PATCH-style pointers (nil = leave alone).
func (s *SiteService) UpdateOAuthClient(ctx context.Context, clientID string, name *string, redirectURIs, grants, allowedScopes []string, autoConsent *bool, refreshTokenTTLSeconds *int, listed *bool, logoURL, tagline *string, displayOrder *int) (*model.OAuthClient, error) {
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
	if autoConsent != nil {
		client.AutoConsent = *autoConsent
	}
	if refreshTokenTTLSeconds != nil {
		client.RefreshTokenTTLSeconds = *refreshTokenTTLSeconds
	}
	if listed != nil {
		client.Listed = *listed
	}
	if logoURL != nil {
		client.LogoURL = *logoURL
	}
	if tagline != nil {
		client.Tagline = *tagline
	}
	if displayOrder != nil {
		client.DisplayOrder = *displayOrder
	}

	if err := s.oauthClientRepo.Update(ctx, client); err != nil {
		return nil, err
	}
	return client, nil
}

// GetOAuthClient returns a single OAuth client by its client_id. Used by the
// admin update path to compare the request against the current row (the ren
// no-escalation guard needs to know whether image:upload / auto_consent were
// already set before this edit).
func (s *SiteService) GetOAuthClient(ctx context.Context, clientID string) (*model.OAuthClient, error) {
	return s.oauthClientRepo.FindByClientID(ctx, clientID)
}

// StorageConfig carries the per-client object-storage capability knobs the admin
// storage-config endpoint manages (the artifact_*/image_* oauth_clients columns).
type StorageConfig struct {
	ArtifactEnabled         bool
	ArtifactSiteKey         string
	ArtifactCDNBase         string
	ArtifactAllowedMime     []string
	ArtifactMaxFileSize     int64
	ArtifactQuotaDaily      int
	ArtifactQuotaBytesDaily int64
	ImageEnabled            bool
	ImageSiteKey            string
	ImageCDNBase            string
	ImageAllowedPresets     []string
	ImageMaxFileSize        int64
	ImageQuotaDaily         int
	ImageQuotaBytesDaily    int64
}

// UpdateOAuthClientStorage sets the artifact_*/image_* capability columns on a
// client (admin storage-config endpoint). It writes the full state — the admin
// form always submits every field.
func (s *SiteService) UpdateOAuthClientStorage(ctx context.Context, clientID string, cfg StorageConfig) (*model.OAuthClient, error) {
	client, err := s.oauthClientRepo.FindByClientID(ctx, clientID)
	if err != nil {
		return nil, err
	}
	client.ArtifactEnabled = cfg.ArtifactEnabled
	client.ArtifactSiteKey = cfg.ArtifactSiteKey
	client.ArtifactCDNBase = cfg.ArtifactCDNBase
	client.ArtifactAllowedMime = marshalStringList(cfg.ArtifactAllowedMime)
	client.ArtifactMaxFileSize = cfg.ArtifactMaxFileSize
	client.ArtifactQuotaDaily = cfg.ArtifactQuotaDaily
	client.ArtifactQuotaBytesDaily = cfg.ArtifactQuotaBytesDaily
	client.ImageEnabled = cfg.ImageEnabled
	client.ImageSiteKey = cfg.ImageSiteKey
	client.ImageCDNBase = cfg.ImageCDNBase
	client.ImageAllowedPresets = marshalStringList(cfg.ImageAllowedPresets)
	client.ImageMaxFileSize = cfg.ImageMaxFileSize
	client.ImageQuotaDaily = cfg.ImageQuotaDaily
	client.ImageQuotaBytesDaily = cfg.ImageQuotaBytesDaily
	if err := s.oauthClientRepo.Update(ctx, client); err != nil {
		return nil, err
	}
	return client, nil
}

// marshalStringList JSON-encodes a list for a jsonb column, normalising nil → [].
func marshalStringList(list []string) []byte {
	if list == nil {
		list = []string{}
	}
	b, _ := json.Marshal(list)
	return b
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
