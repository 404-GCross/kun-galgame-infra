package devapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"time"

	siteModel "api/internal/platform/site/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository is the developer-platform data access layer over the main DB
// (kun_galgame_infra): API-key resolution + lifecycle, app dev-config, and
// usage upserts.
type Repository struct {
	db *gorm.DB
}

// NewRepository builds a Repository over the given main-DB handle.
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// resolveRow is the flat join result for credential resolution.
type resolveRow struct {
	KeyID         uint
	ClientID      string
	AppName       string
	KeyHash       string
	KeyScopes     []byte
	KeyNSFW       bool
	RevokedAt     *time.Time
	ExpiresAt     *time.Time
	DevEnabled    bool
	DevTier       string
	DevNSFW       bool
	DevRatePerMin int
	DevQuotaDaily int
}

// ResolveByHash loads the active credential for a stored key hash ("sha256:<hex>"),
// joining the owning application. Returns (nil, nil) — resolve to 401 — when no
// row matches, the key is revoked/expired, or the app has dev_enabled=false. A
// real error (DB down) is returned so the caller can reject WITHOUT failing open
// (裁定 5: the DB credential check never fails open).
func (r *Repository) ResolveByHash(ctx context.Context, hash string, now time.Time) (*Credential, error) {
	var row resolveRow
	err := r.db.WithContext(ctx).
		Table("developer_api_keys AS k").
		Select(`k.id AS key_id, k.client_id AS client_id, c.name AS app_name,
			k.key_hash AS key_hash, k.scopes AS key_scopes, k.nsfw_allowed AS key_nsfw,
			k.revoked_at AS revoked_at, k.expires_at AS expires_at,
			c.dev_enabled AS dev_enabled, c.dev_tier AS dev_tier,
			c.dev_nsfw_allowed AS dev_nsfw, c.dev_rate_per_min AS dev_rate_per_min,
			c.dev_quota_daily AS dev_quota_daily`).
		Joins("JOIN oauth_clients AS c ON c.id = k.client_id").
		Where("k.key_hash = ?", hash).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Constant-time re-check of the hash (defence in depth over the indexed
	// equality lookup) + active gates.
	if subtle.ConstantTimeCompare([]byte(row.KeyHash), []byte(hash)) != 1 {
		return nil, nil
	}
	if !row.DevEnabled || row.RevokedAt != nil {
		return nil, nil
	}
	if row.ExpiresAt != nil && !row.ExpiresAt.After(now) {
		return nil, nil
	}

	var scopes []string
	if len(row.KeyScopes) > 0 {
		_ = json.Unmarshal(row.KeyScopes, &scopes)
	}
	return &Credential{
		KeyID:         row.KeyID,
		ClientID:      row.ClientID,
		AppName:       row.AppName,
		Tier:          row.DevTier,
		Scopes:        scopes,
		NSFWAllowed:   row.KeyNSFW && row.DevNSFW,
		RateOverride:  row.DevRatePerMin,
		QuotaOverride: row.DevQuotaDaily,
	}, nil
}

// CreateKey inserts a new API key row.
func (r *Repository) CreateKey(ctx context.Context, key *DeveloperAPIKey) error {
	return r.db.WithContext(ctx).Create(key).Error
}

// ListKeysByClient returns an app's keys, newest first.
func (r *Repository) ListKeysByClient(ctx context.Context, clientID string) ([]DeveloperAPIKey, error) {
	var keys []DeveloperAPIKey
	err := r.db.WithContext(ctx).
		Where("client_id = ?", clientID).
		Order("created_at DESC").
		Find(&keys).Error
	return keys, err
}

// CountKeysByClient returns the live (non-revoked) key count for an app.
func (r *Repository) CountKeysByClient(ctx context.Context, clientID string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).
		Model(&DeveloperAPIKey{}).
		Where("client_id = ? AND revoked_at IS NULL", clientID).
		Count(&n).Error
	return n, err
}

// GetKey loads a single key by id.
func (r *Repository) GetKey(ctx context.Context, id uint) (*DeveloperAPIKey, error) {
	var key DeveloperAPIKey
	if err := r.db.WithContext(ctx).First(&key, id).Error; err != nil {
		return nil, err
	}
	return &key, nil
}

// SetKeyExpiry sets a key's expiry (rotation grace window).
func (r *Repository) SetKeyExpiry(ctx context.Context, id uint, expiresAt time.Time) error {
	return r.db.WithContext(ctx).
		Model(&DeveloperAPIKey{}).
		Where("id = ?", id).
		Update("expires_at", expiresAt).Error
}

// RevokeKey marks a key revoked (rejected on the next request).
func (r *Repository) RevokeKey(ctx context.Context, id uint, now time.Time) error {
	return r.db.WithContext(ctx).
		Model(&DeveloperAPIKey{}).
		Where("id = ?", id).
		Update("revoked_at", now).Error
}

// TouchLastUsed writes last_used_at (throttled by the caller).
func (r *Repository) TouchLastUsed(ctx context.Context, id uint, now time.Time) error {
	return r.db.WithContext(ctx).
		Model(&DeveloperAPIKey{}).
		Where("id = ?", id).
		Update("last_used_at", now).Error
}

// GetApp loads an oauth_clients row by client_id.
func (r *Repository) GetApp(ctx context.Context, clientID string) (*siteModel.OAuthClient, error) {
	var app siteModel.OAuthClient
	if err := r.db.WithContext(ctx).Where("id = ?", clientID).First(&app).Error; err != nil {
		return nil, err
	}
	return &app, nil
}

// ListDevApps returns every dev_enabled application, name-ordered.
func (r *Repository) ListDevApps(ctx context.Context) ([]siteModel.OAuthClient, error) {
	var apps []siteModel.OAuthClient
	err := r.db.WithContext(ctx).
		Where("dev_enabled = ?", true).
		Order("name ASC").
		Find(&apps).Error
	return apps, err
}

// UpdateAppDevConfig sets the dev_* columns on an app. Takes a field map so
// zero values (disable, override 0) are written explicitly — a struct Updates
// would silently skip them.
func (r *Repository) UpdateAppDevConfig(ctx context.Context, clientID string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).
		Model(&siteModel.OAuthClient{}).
		Where("id = ?", clientID).
		Updates(fields).Error
}

// UpsertUsage accumulates a batch of usage rollups into developer_api_usage:
// ON CONFLICT (client_id, key_id, face, day) the counters ADD (not replace), so
// each instance's periodic flush contributes its delta and re-flushing an empty
// batch is a no-op.
func (r *Repository) UpsertUsage(ctx context.Context, rows []DeveloperAPIUsage) error {
	if len(rows) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "client_id"}, {Name: "key_id"}, {Name: "face"}, {Name: "day"}},
		DoUpdates: clause.Assignments(map[string]any{
			"count":      gorm.Expr("developer_api_usage.count + excluded.count"),
			"status_4xx": gorm.Expr("developer_api_usage.status_4xx + excluded.status_4xx"),
			"status_5xx": gorm.Expr("developer_api_usage.status_5xx + excluded.status_5xx"),
			"updated_at": gorm.Expr("excluded.updated_at"),
		}),
	}).Create(&rows).Error
}
