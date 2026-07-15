package devapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	siteModel "api/internal/platform/site/model"

	"gorm.io/datatypes"
)

// rotateGrace is how long a rotated-out key stays valid after rotation so a
// deploy can swap keys without downtime (design §4.1: 24–72h grace).
const rotateGrace = 72 * time.Hour

// ErrInvalidTier is returned when an app is set to an unknown tier.
var ErrInvalidTier = errors.New("devapi: invalid tier (want free|trusted|internal)")

// AdminService orchestrates the developer-platform management surface: app
// dev-config and API-key lifecycle. It buses revocations through the resolve
// cache so a revoke takes effect immediately, not after the 60s TTL.
type AdminService struct {
	repo  *Repository
	store Store
}

// NewAdminService builds the management service.
func NewAdminService(repo *Repository, store Store) *AdminService {
	return &AdminService{repo: repo, store: store}
}

// AppConfig is a PATCH over an app's dev_* fields — nil fields are left
// unchanged.
type AppConfig struct {
	OwnerUserID    *uint
	DevEnabled     *bool
	DevTier        *string
	DevNSFWAllowed *bool
	DevRatePerMin  *int
	DevQuotaDaily  *int
}

// AppView is an app row plus its live key count, for the management list.
type AppView struct {
	Client   *siteModel.OAuthClient
	KeyCount int64
}

// UpdateAppConfig applies a PATCH to an app's dev configuration. An unknown
// tier is rejected. Returns the refreshed app row.
func (s *AdminService) UpdateAppConfig(ctx context.Context, clientID string, cfg AppConfig) (*siteModel.OAuthClient, error) {
	fields := map[string]any{}
	if cfg.OwnerUserID != nil {
		fields["owner_user_id"] = *cfg.OwnerUserID
	}
	if cfg.DevEnabled != nil {
		fields["dev_enabled"] = *cfg.DevEnabled
	}
	if cfg.DevTier != nil {
		if !validTier(*cfg.DevTier) {
			return nil, ErrInvalidTier
		}
		fields["dev_tier"] = *cfg.DevTier
	}
	if cfg.DevNSFWAllowed != nil {
		fields["dev_nsfw_allowed"] = *cfg.DevNSFWAllowed
	}
	if cfg.DevRatePerMin != nil {
		fields["dev_rate_per_min"] = *cfg.DevRatePerMin
	}
	if cfg.DevQuotaDaily != nil {
		fields["dev_quota_daily"] = *cfg.DevQuotaDaily
	}
	if err := s.repo.UpdateAppDevConfig(ctx, clientID, fields); err != nil {
		return nil, err
	}
	return s.repo.GetApp(ctx, clientID)
}

// ListApps returns every dev_enabled app with its live key count.
func (s *AdminService) ListApps(ctx context.Context) ([]AppView, error) {
	apps, err := s.repo.ListDevApps(ctx)
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

// MintKeyInput is the payload for minting a key.
type MintKeyInput struct {
	Name   string
	Test   bool     // nm_test_ instead of nm_live_
	Scopes []string // defaults to the public read scopes when empty
}

// MintKey generates a new key for an app, persists only its hash, and returns
// the plaintext ONCE (never stored, never logged). Phase 1 forces the key's
// nsfw_allowed off (裁定 6). The owning app is validated to exist.
func (s *AdminService) MintKey(ctx context.Context, clientID string, in MintKeyInput, createdBy uint) (*DeveloperAPIKey, string, error) {
	if _, err := s.repo.GetApp(ctx, clientID); err != nil {
		return nil, "", err
	}
	scopes := in.Scopes
	if len(scopes) == 0 {
		scopes = []string{ScopeCatalogRead, ScopeGalgameRead}
	}
	key, plaintext, err := s.buildKey(clientID, in.Name, in.Test, scopes, createdBy)
	if err != nil {
		return nil, "", err
	}
	if err := s.repo.CreateKey(ctx, key); err != nil {
		return nil, "", err
	}
	slog.Info("devapi key minted", "client_id", clientID, "key_id", key.ID, "prefix", key.KeyPrefix, "last4", key.Last4, "by", createdBy)
	return key, plaintext, nil
}

// ListKeys returns an app's keys (no plaintext, no hash — KeyHash is json:"-").
func (s *AdminService) ListKeys(ctx context.Context, clientID string) ([]DeveloperAPIKey, error) {
	return s.repo.ListKeysByClient(ctx, clientID)
}

// RotateKey mints a replacement for an existing key (same app/scopes/nsfw) and
// puts the old key into a grace window (expires_at = now + rotateGrace) so both
// work during the swap. Returns the NEW key's plaintext once.
func (s *AdminService) RotateKey(ctx context.Context, keyID, createdBy uint) (*DeveloperAPIKey, string, error) {
	old, err := s.repo.GetKey(ctx, keyID)
	if err != nil {
		return nil, "", err
	}
	var scopes []string
	if len(old.Scopes) > 0 {
		_ = json.Unmarshal(old.Scopes, &scopes)
	}
	newKey, plaintext, err := s.buildKeyWithNSFW(old.ClientID, old.Name, strings.HasPrefix(old.KeyPrefix, TestPrefix), scopes, old.NSFWAllowed, createdBy)
	if err != nil {
		return nil, "", err
	}
	if err := s.repo.CreateKey(ctx, newKey); err != nil {
		return nil, "", err
	}
	if err := s.repo.SetKeyExpiry(ctx, old.ID, time.Now().Add(rotateGrace)); err != nil {
		return nil, "", err
	}
	slog.Info("devapi key rotated", "client_id", old.ClientID, "old_key_id", old.ID, "new_key_id", newKey.ID, "by", createdBy)
	return newKey, plaintext, nil
}

// RevokeKey revokes a key immediately and actively busts its resolve cache so
// it stops working now, not after the 60s TTL.
func (s *AdminService) RevokeKey(ctx context.Context, keyID uint) error {
	key, err := s.repo.GetKey(ctx, keyID)
	if err != nil {
		return err
	}
	if err := s.repo.RevokeKey(ctx, key.ID, time.Now()); err != nil {
		return err
	}
	// The resolve cache is keyed by the bare hash hex; KeyHash is "sha256:<hex>".
	if hexPart, ok := strings.CutPrefix(key.KeyHash, keyHashPrefix); ok {
		_ = s.store.Del(ctx, "devkey:"+hexPart)
	}
	slog.Info("devapi key revoked", "client_id", key.ClientID, "key_id", key.ID)
	return nil
}

// GetKeyForClient loads a key and verifies it belongs to clientID (route
// nesting guard). Returns nil when it doesn't match.
func (s *AdminService) GetKeyForClient(ctx context.Context, clientID string, keyID uint) (*DeveloperAPIKey, error) {
	key, err := s.repo.GetKey(ctx, keyID)
	if err != nil {
		return nil, err
	}
	if key.ClientID != clientID {
		return nil, nil
	}
	return key, nil
}

// buildKey is buildKeyWithNSFW with nsfw forced off (Phase 1 mint path).
func (s *AdminService) buildKey(clientID, name string, test bool, scopes []string, createdBy uint) (*DeveloperAPIKey, string, error) {
	return s.buildKeyWithNSFW(clientID, name, test, scopes, false, createdBy)
}

// buildKeyWithNSFW assembles a key row + plaintext.
func (s *AdminService) buildKeyWithNSFW(clientID, name string, test bool, scopes []string, nsfw bool, createdBy uint) (*DeveloperAPIKey, string, error) {
	prefix := LivePrefix
	if test {
		prefix = TestPrefix
	}
	plaintext, err := GenerateKey(prefix)
	if err != nil {
		return nil, "", err
	}
	kp, last4 := KeyMetadata(plaintext)
	scopesJSON, err := json.Marshal(scopes)
	if err != nil {
		return nil, "", err
	}
	return &DeveloperAPIKey{
		ClientID:        clientID,
		Name:            name,
		KeyHash:         HashKey(plaintext),
		KeyPrefix:       kp,
		Last4:           last4,
		Scopes:          datatypes.JSON(scopesJSON),
		NSFWAllowed:     nsfw,
		CreatedByUserID: createdBy,
	}, plaintext, nil
}

func validTier(t string) bool {
	return t == TierFree || t == TierTrusted || t == TierInternal
}
