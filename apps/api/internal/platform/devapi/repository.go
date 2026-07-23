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
	return r.UpdateAppFields(ctx, clientID, fields)
}

// UpdateAppFields is the generic column updater for an oauth_clients row (id +
// arbitrary field map). Both the admin dev-config PATCH and the self-service
// name/description PATCH route through it; an empty map is a no-op.
func (r *Repository) UpdateAppFields(ctx context.Context, clientID string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).
		Model(&siteModel.OAuthClient{}).
		Where("id = ?", clientID).
		Updates(fields).Error
}

// --- Self-service (owner-scoped) data access ---

// CreateApp inserts a new developer application (oauth_clients row). The caller
// (SelfServiceService) sets the id/secret/owner/dev_* fields explicitly.
func (r *Repository) CreateApp(ctx context.Context, app *siteModel.OAuthClient) error {
	return r.db.WithContext(ctx).Create(app).Error
}

// ListAppsByOwner returns every application owned by ownerUserID, newest first.
// First-party site clients (owner_user_id NULL) are structurally excluded, so a
// developer only ever sees their own apps.
func (r *Repository) ListAppsByOwner(ctx context.Context, ownerUserID uint) ([]siteModel.OAuthClient, error) {
	var apps []siteModel.OAuthClient
	err := r.db.WithContext(ctx).
		Where("owner_user_id = ?", ownerUserID).
		Order("created_at DESC").
		Find(&apps).Error
	return apps, err
}

// GetAppByOwner loads an application only when it is owned by ownerUserID. A
// row that doesn't exist OR isn't owned by the caller returns
// gorm.ErrRecordNotFound identically — the owner guard: a non-owner cannot tell
// a foreign app apart from a nonexistent one (404, no existence leak).
func (r *Repository) GetAppByOwner(ctx context.Context, clientID string, ownerUserID uint) (*siteModel.OAuthClient, error) {
	var app siteModel.OAuthClient
	if err := r.db.WithContext(ctx).
		Where("id = ? AND owner_user_id = ?", clientID, ownerUserID).
		First(&app).Error; err != nil {
		return nil, err
	}
	return &app, nil
}

// CountAppsByOwner counts a user's applications (the per-user app cap).
func (r *Repository) CountAppsByOwner(ctx context.Context, ownerUserID uint) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).
		Model(&siteModel.OAuthClient{}).
		Where("owner_user_id = ?", ownerUserID).
		Count(&n).Error
	return n, err
}

// CountActiveKeysByClient counts an app's currently-usable keys (not revoked and
// not past expiry) — the per-app active-key cap. A key in a rotation grace
// window (expires_at in the future) still counts; a fully-expired key frees its
// slot.
func (r *Repository) CountActiveKeysByClient(ctx context.Context, clientID string, now time.Time) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).
		Model(&DeveloperAPIKey{}).
		Where("client_id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", clientID, now).
		Count(&n).Error
	return n, err
}

// OwnerActiveKey is one currently-usable key of an owner (across ALL their
// dev-enabled apps), carrying the owning app's name + effective-limit inputs.
// It feeds the account-level live-remaining view (per-key rate/quota read from
// the Redis enforcement counters).
type OwnerActiveKey struct {
	KeyID         uint   `gorm:"column:key_id"`
	AppName       string `gorm:"column:app_name"`
	DevTier       string `gorm:"column:dev_tier"`
	DevRatePerMin int    `gorm:"column:dev_rate_per_min"`
	DevQuotaDaily int    `gorm:"column:dev_quota_daily"`
}

// ListOwnerActiveKeys returns every currently-usable key across an owner's
// dev-enabled apps (not revoked, not past expiry) joined to its app's name and
// dev-limit columns — the same activeness predicate ResolveByHash enforces.
// Ordered by app name then key id for a stable render.
func (r *Repository) ListOwnerActiveKeys(ctx context.Context, ownerUserID uint, now time.Time) ([]OwnerActiveKey, error) {
	var rows []OwnerActiveKey
	err := r.db.WithContext(ctx).
		Table("developer_api_keys AS k").
		Select(`k.id AS key_id, c.name AS app_name, c.dev_tier AS dev_tier,
			c.dev_rate_per_min AS dev_rate_per_min, c.dev_quota_daily AS dev_quota_daily`).
		Joins("JOIN oauth_clients AS c ON c.id = k.client_id").
		Where(`c.owner_user_id = ? AND c.dev_enabled = ? AND k.revoked_at IS NULL
			AND (k.expires_at IS NULL OR k.expires_at > ?)`, ownerUserID, true, now).
		Order("c.name ASC, k.id ASC").
		Scan(&rows).Error
	return rows, err
}

// UsageFaceTotal is one face's total usage over the window (all days/clients
// folded), for the account-level per-face breakdown. Same status_4xx/5xx column
// aliases as the sibling aggregates (dodge GORM's acronym/digit naming).
type UsageFaceTotal struct {
	Face      string `gorm:"column:face" json:"face"`
	Count     int64  `gorm:"column:count" json:"count"`
	Status4xx int64  `gorm:"column:status_4xx" json:"status_4xx"`
	Status5xx int64  `gorm:"column:status_5xx" json:"status_5xx"`
}

// SumUsageByFace sums usage across the given clients grouped by face (all
// days/keys folded), for days on/after sinceDay. Highest-volume face first.
// Empty clientIDs → no rows (never emits `IN ()`).
func (r *Repository) SumUsageByFace(ctx context.Context, clientIDs []string, sinceDay string) ([]UsageFaceTotal, error) {
	var rows []UsageFaceTotal
	if len(clientIDs) == 0 {
		return rows, nil
	}
	err := r.db.WithContext(ctx).
		Model(&DeveloperAPIUsage{}).
		Select("face, SUM(count) AS count, SUM(status_4xx) AS status_4xx, SUM(status_5xx) AS status_5xx").
		Where("client_id IN ? AND day >= ?", clientIDs, sinceDay).
		Group("face").
		Order("count DESC, face ASC").
		Scan(&rows).Error
	return rows, err
}

// CountUsageBefore counts developer_api_usage rows strictly older than beforeDay
// (YYYY-MM-DD). Used by the retention job's dry-run.
func (r *Repository) CountUsageBefore(ctx context.Context, beforeDay string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).
		Model(&DeveloperAPIUsage{}).
		Where("day < ?", beforeDay).
		Count(&n).Error
	return n, err
}

// PruneUsageBefore deletes developer_api_usage rows strictly older than
// beforeDay (YYYY-MM-DD, UTC) and returns the number removed. day is a
// zero-padded fixed-width string, so the lexicographic `<` is chronological.
func (r *Repository) PruneUsageBefore(ctx context.Context, beforeDay string) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("day < ?", beforeDay).
		Delete(&DeveloperAPIUsage{})
	return res.RowsAffected, res.Error
}

// UsageDayFace is one aggregated usage row for the self-service usage panel:
// a client's counters summed across all its keys, grouped by (day, face).
type UsageDayFace struct {
	Day       string `gorm:"column:day" json:"day"`
	Face      string `gorm:"column:face" json:"face"`
	Count     int64  `gorm:"column:count" json:"count"`
	Status4xx int64  `gorm:"column:status_4xx" json:"status_4xx"`
	Status5xx int64  `gorm:"column:status_5xx" json:"status_5xx"`
}

// AggregateUsageByClient sums a client's usage across all its keys, grouped by
// (day, face), for days on/after sinceDay (YYYY-MM-DD, UTC). Newest day first.
// The explicit status_4xx/status_5xx column aliases dodge GORM's acronym/digit
// naming (Status4xx → status4xx) so the SUM columns bind to the struct fields.
func (r *Repository) AggregateUsageByClient(ctx context.Context, clientID, sinceDay string) ([]UsageDayFace, error) {
	var rows []UsageDayFace
	err := r.db.WithContext(ctx).
		Model(&DeveloperAPIUsage{}).
		Select("day, face, SUM(count) AS count, SUM(status_4xx) AS status_4xx, SUM(status_5xx) AS status_5xx").
		Where("client_id = ? AND day >= ?", clientID, sinceDay).
		Group("day, face").
		Order("day DESC, face ASC").
		Scan(&rows).Error
	return rows, err
}

// UsageDayTotal is one day's usage summed across a set of clients (all faces
// folded together), for the account-level usage panel. Same status_4xx/5xx
// column aliases as UsageDayFace (dodge GORM's acronym/digit naming).
type UsageDayTotal struct {
	Day       string `gorm:"column:day" json:"day"`
	Count     int64  `gorm:"column:count" json:"count"`
	Status4xx int64  `gorm:"column:status_4xx" json:"status_4xx"`
	Status5xx int64  `gorm:"column:status_5xx" json:"status_5xx"`
}

// UsageClientTotal is one client's total usage over the window (all days/faces
// folded), for the per-app breakdown.
type UsageClientTotal struct {
	ClientID  string `gorm:"column:client_id" json:"client_id"`
	Count     int64  `gorm:"column:count" json:"count"`
	Status4xx int64  `gorm:"column:status_4xx" json:"status_4xx"`
	Status5xx int64  `gorm:"column:status_5xx" json:"status_5xx"`
}

// SumUsageByDay sums usage across the given clients grouped by day (all faces
// folded), for days on/after sinceDay (YYYY-MM-DD, UTC). Oldest day first (chart
// order). Empty clientIDs → no rows (never emits `IN ()`).
func (r *Repository) SumUsageByDay(ctx context.Context, clientIDs []string, sinceDay string) ([]UsageDayTotal, error) {
	var rows []UsageDayTotal
	if len(clientIDs) == 0 {
		return rows, nil
	}
	err := r.db.WithContext(ctx).
		Model(&DeveloperAPIUsage{}).
		Select("day, SUM(count) AS count, SUM(status_4xx) AS status_4xx, SUM(status_5xx) AS status_5xx").
		Where("client_id IN ? AND day >= ?", clientIDs, sinceDay).
		Group("day").
		Order("day ASC").
		Scan(&rows).Error
	return rows, err
}

// SumUsageByClient sums usage across the given clients grouped by client_id (all
// days/faces folded), for days on/after sinceDay. Empty clientIDs → no rows.
func (r *Repository) SumUsageByClient(ctx context.Context, clientIDs []string, sinceDay string) ([]UsageClientTotal, error) {
	var rows []UsageClientTotal
	if len(clientIDs) == 0 {
		return rows, nil
	}
	err := r.db.WithContext(ctx).
		Model(&DeveloperAPIUsage{}).
		Select("client_id, SUM(count) AS count, SUM(status_4xx) AS status_4xx, SUM(status_5xx) AS status_5xx").
		Where("client_id IN ? AND day >= ?", clientIDs, sinceDay).
		Group("client_id").
		Scan(&rows).Error
	return rows, err
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
