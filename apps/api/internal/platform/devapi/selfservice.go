package devapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"slices"
	"time"

	siteModel "api/internal/platform/site/model"

	"gorm.io/datatypes"
)

// Self-service caps (docs/developer-platform/01-design.md §9 / 06a 裁定 2-3).
// Plain constants — the developer self-service face has no admin knob to raise
// them; a higher ceiling is an admin/tier decision, not a self-service one.
const (
	// MaxAppsPerOwner is the number of applications one ecosystem account may own.
	MaxAppsPerOwner = 5
	// MaxActiveKeysPerApp is the number of live (non-revoked, non-expired) keys an
	// application may hold at once.
	MaxActiveKeysPerApp = 5
	// maxAppNameLen / maxAppDescLen mirror the oauth_clients.name (varchar 100) and
	// the reused tagline column (varchar 100) sizes — over-length is rejected up
	// front rather than surfacing as a Postgres error.
	maxAppNameLen = 100
	maxAppDescLen = 100
)

// selfServiceScopes is the scope allow-list a developer may request when minting
// a key for their own app (裁定 3): the two public read faces only. galgame:nsfw
// is never self-service-grantable (裁定 6).
var selfServiceScopes = []string{ScopeCatalogRead, ScopeGalgameRead}

var (
	// ErrAppLimitReached — the owner already holds MaxAppsPerOwner apps (→ 400).
	ErrAppLimitReached = errors.New("devapi: application limit reached")
	// ErrKeyLimitReached — the app already holds MaxActiveKeysPerApp active keys (→ 400).
	ErrKeyLimitReached = errors.New("devapi: active key limit reached")
	// ErrScopeNotAllowed — a requested scope is outside the self-service allow-list (→ 400).
	ErrScopeNotAllowed = errors.New("devapi: scope not permitted (want catalog:read and/or galgame:read)")
	// ErrNameRequired — app / key name missing (→ 400).
	ErrNameRequired = errors.New("devapi: name is required")
	// ErrNameTooLong / ErrDescTooLong — over the column length (→ 400).
	ErrNameTooLong = errors.New("devapi: name too long (max 100)")
	ErrDescTooLong = errors.New("devapi: description too long (max 100)")
)

// SelfServiceService is the developer's own view of the open API: create and
// manage their own applications, mint/rotate/revoke their own keys, read their
// own usage. Every operation is owner-guarded. The API-key lifecycle is NOT
// reimplemented here — it is delegated to the same AdminService the admin
// console uses (single source for mint/rotate/revoke + cache-busting); this
// layer only adds the owner guard, the self-service caps, and the scope
// allow-list on top.
type SelfServiceService struct {
	repo  *Repository
	admin *AdminService
}

// NewSelfServiceService builds the self-service service over the shared repo and
// the shared AdminService (so key operations reuse one implementation).
func NewSelfServiceService(repo *Repository, admin *AdminService) *SelfServiceService {
	return &SelfServiceService{repo: repo, admin: admin}
}

// CreateApp registers a new developer application owned by ownerUserID. It is
// admitted to the open API immediately (dev_enabled) at the free tier; tier /
// nsfw / quota are admin-only and stay at their defaults. The per-user app cap
// is enforced first. A client_secret is generated + hashed (never returned —
// Phase 1 is pure API-key; the OAuth flow is Phase 2/3).
func (s *SelfServiceService) CreateApp(ctx context.Context, ownerUserID uint, name, description string) (*siteModel.OAuthClient, error) {
	if err := validateAppMeta(name, description, true); err != nil {
		return nil, err
	}
	n, err := s.repo.CountAppsByOwner(ctx, ownerUserID)
	if err != nil {
		return nil, err
	}
	if n >= MaxAppsPerOwner {
		return nil, ErrAppLimitReached
	}

	clientID, err := generateHex(16)
	if err != nil {
		return nil, err
	}
	secret, err := generateHex(32)
	if err != nil {
		return nil, err
	}

	owner := ownerUserID
	app := &siteModel.OAuthClient{
		ID:   clientID,
		Name: name,
		// Hash only; the plaintext secret is discarded (unused in Phase 1).
		Secret: siteModel.HashOAuthClientSecret(secret),
		// NOT NULL jsonb columns — a dev app runs no OAuth flow in Phase 1, so
		// both are empty (grants empty = fail-closed on /oauth/token, which is
		// exactly right: an API-key app cannot mint OAuth tokens).
		RedirectURIs: datatypes.JSON([]byte("[]")),
		Grants:       datatypes.JSON([]byte("[]")),
		// The app's own scope allow-list = the two public read scopes, so its
		// keys' scopes are ⊆ the app by construction.
		AllowedScopes: datatypes.JSON([]byte(`["catalog:read","galgame:read"]`)),
		// description is stored in the reused tagline column (no dedicated column
		// exists; tagline is display-only and invisible unless Listed, which
		// self-service apps never are).
		Tagline: description,
		// Every dev_* intent column set explicitly (no GORM default tag → the
		// zero-value INSERT trap can't bite; dev_tier '' would be invalid).
		OwnerUserID:    &owner,
		DevEnabled:     true,
		DevTier:        TierFree,
		DevNSFWAllowed: false,
		DevRatePerMin:  0,
		DevQuotaDaily:  0,
	}
	if err := s.repo.CreateApp(ctx, app); err != nil {
		return nil, err
	}
	return app, nil
}

// ListApps returns the caller's applications, each with its live key count.
func (s *SelfServiceService) ListApps(ctx context.Context, ownerUserID uint) ([]AppView, error) {
	apps, err := s.repo.ListAppsByOwner(ctx, ownerUserID)
	if err != nil {
		return nil, err
	}
	out := make([]AppView, len(apps))
	for i := range apps {
		n, err := s.repo.CountKeysByClient(ctx, apps[i].ID)
		if err != nil {
			return nil, err
		}
		out[i] = AppView{Client: &apps[i], KeyCount: n}
	}
	return out, nil
}

// GetApp returns one of the caller's applications with its live key count.
// Returns gorm.ErrRecordNotFound (→ 404) when the app is not owned by the caller.
func (s *SelfServiceService) GetApp(ctx context.Context, ownerUserID uint, clientID string) (*AppView, error) {
	app, err := s.repo.GetAppByOwner(ctx, clientID, ownerUserID)
	if err != nil {
		return nil, err
	}
	n, err := s.repo.CountKeysByClient(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	return &AppView{Client: app, KeyCount: n}, nil
}

// UpdateApp patches an owned app's name and/or description (the only
// self-service-writable fields — tier / nsfw / quota are admin-only). Returns
// gorm.ErrRecordNotFound (→ 404) for a non-owned app.
func (s *SelfServiceService) UpdateApp(ctx context.Context, ownerUserID uint, clientID string, name, description *string) (*siteModel.OAuthClient, error) {
	app, err := s.repo.GetAppByOwner(ctx, clientID, ownerUserID)
	if err != nil {
		return nil, err
	}
	if err := validateAppMetaPtr(name, description); err != nil {
		return nil, err
	}
	fields := map[string]any{}
	if name != nil {
		fields["name"] = *name
	}
	if description != nil {
		fields["tagline"] = *description // description ↔ tagline (reused column)
	}
	if err := s.repo.UpdateAppFields(ctx, app.ID, fields); err != nil {
		return nil, err
	}
	return s.repo.GetApp(ctx, app.ID)
}

// DeactivateApp disables an owned app for the open API and immediately revokes
// every one of its live keys (busting each key's resolve cache so they stop
// working now, not after the 60s TTL). The oauth_clients row itself is kept —
// only dev_enabled flips off. Returns gorm.ErrRecordNotFound (→ 404) for a
// non-owned app.
func (s *SelfServiceService) DeactivateApp(ctx context.Context, ownerUserID uint, clientID string) error {
	app, err := s.repo.GetAppByOwner(ctx, clientID, ownerUserID)
	if err != nil {
		return err
	}
	keys, err := s.repo.ListKeysByClient(ctx, app.ID)
	if err != nil {
		return err
	}
	for i := range keys {
		if keys[i].RevokedAt != nil {
			continue
		}
		// Reuse the admin revoke path — it sets revoked_at AND busts the resolve
		// cache for this key's hash.
		if err := s.admin.RevokeKey(ctx, keys[i].ID); err != nil {
			return err
		}
	}
	return s.repo.UpdateAppFields(ctx, app.ID, map[string]any{"dev_enabled": false})
}

// MintKey mints a key for an owned app. It enforces the owner guard, the
// per-app active-key cap, and the self-service scope allow-list, then delegates
// the actual key generation to the shared AdminService (which forces nsfw off).
func (s *SelfServiceService) MintKey(ctx context.Context, ownerUserID uint, clientID string, in MintKeyInput) (*DeveloperAPIKey, string, error) {
	if _, err := s.repo.GetAppByOwner(ctx, clientID, ownerUserID); err != nil {
		return nil, "", err
	}
	if in.Name == "" {
		return nil, "", ErrNameRequired
	}
	if len(in.Name) > maxAppNameLen {
		return nil, "", ErrNameTooLong
	}
	if err := checkSelfServiceScopes(in.Scopes); err != nil {
		return nil, "", err
	}
	active, err := s.repo.CountActiveKeysByClient(ctx, clientID, time.Now())
	if err != nil {
		return nil, "", err
	}
	if active >= MaxActiveKeysPerApp {
		return nil, "", ErrKeyLimitReached
	}
	// createdBy = the owner (self-service).
	return s.admin.MintKey(ctx, clientID, in, ownerUserID)
}

// ListKeys returns an owned app's keys (no secret material). 404 for non-owned.
func (s *SelfServiceService) ListKeys(ctx context.Context, ownerUserID uint, clientID string) ([]DeveloperAPIKey, error) {
	if _, err := s.repo.GetAppByOwner(ctx, clientID, ownerUserID); err != nil {
		return nil, err
	}
	return s.admin.ListKeys(ctx, clientID)
}

// RotateKey rotates a key of an owned app (old key enters a 72h grace window).
// Rotation is a replacement, so it is NOT subject to the active-key cap. Returns
// (nil, "", nil) — a 404 signal — when the app is owned but the key is not one
// of its keys. Returns gorm.ErrRecordNotFound for a non-owned app.
func (s *SelfServiceService) RotateKey(ctx context.Context, ownerUserID uint, clientID string, keyID uint) (*DeveloperAPIKey, string, error) {
	if _, err := s.repo.GetAppByOwner(ctx, clientID, ownerUserID); err != nil {
		return nil, "", err
	}
	key, err := s.admin.GetKeyForClient(ctx, clientID, keyID)
	if err != nil {
		return nil, "", err
	}
	if key == nil {
		return nil, "", nil // key not on this app → 404
	}
	return s.admin.RotateKey(ctx, keyID, ownerUserID)
}

// RevokeKey revokes a key of an owned app (immediate + cache bust). Returns
// (false, nil) — a 404 signal — when the app is owned but the key is not one of
// its keys. Returns gorm.ErrRecordNotFound for a non-owned app.
func (s *SelfServiceService) RevokeKey(ctx context.Context, ownerUserID uint, clientID string, keyID uint) (found bool, err error) {
	if _, err := s.repo.GetAppByOwner(ctx, clientID, ownerUserID); err != nil {
		return false, err
	}
	key, err := s.admin.GetKeyForClient(ctx, clientID, keyID)
	if err != nil {
		return false, err
	}
	if key == nil {
		return false, nil // key not on this app → 404
	}
	return true, s.admin.RevokeKey(ctx, keyID)
}

// Usage returns the caller's app usage aggregated by (day, face) for the last
// `days` days (inclusive of today, UTC). 404 for a non-owned app.
func (s *SelfServiceService) Usage(ctx context.Context, ownerUserID uint, clientID string, days int) ([]UsageDayFace, error) {
	if _, err := s.repo.GetAppByOwner(ctx, clientID, ownerUserID); err != nil {
		return nil, err
	}
	// days-1 so a window of 1 returns just today; UTC to match the stored Day.
	since := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	return s.repo.AggregateUsageByClient(ctx, clientID, since)
}

// checkSelfServiceScopes rejects any scope outside the self-service allow-list.
// An empty list is fine — MintKey defaults it to the two public reads.
func checkSelfServiceScopes(scopes []string) error {
	for _, sc := range scopes {
		if !slices.Contains(selfServiceScopes, sc) {
			return ErrScopeNotAllowed
		}
	}
	return nil
}

// validateAppMeta validates a create/patch app name + description. requireName
// makes an empty name an error (create); a patch validates only what's present.
func validateAppMeta(name, description string, requireName bool) error {
	if requireName && name == "" {
		return ErrNameRequired
	}
	if len(name) > maxAppNameLen {
		return ErrNameTooLong
	}
	if len(description) > maxAppDescLen {
		return ErrDescTooLong
	}
	return nil
}

// validateAppMetaPtr validates the optional-pointer PATCH form: a present name
// must be non-empty, and both are length-checked.
func validateAppMetaPtr(name, description *string) error {
	if name != nil {
		if *name == "" {
			return ErrNameRequired
		}
		if len(*name) > maxAppNameLen {
			return ErrNameTooLong
		}
	}
	if description != nil && len(*description) > maxAppDescLen {
		return ErrDescTooLong
	}
	return nil
}

// generateHex returns n cryptographically random bytes as a hex string (client
// id / secret material — mirrors the site service's generateRandomHex).
func generateHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
