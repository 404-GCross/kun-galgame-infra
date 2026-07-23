package devapi

import (
	"time"

	"gorm.io/datatypes"
)

// DeveloperAPIKey is one API key issued to a developer application (an
// oauth_clients row). Only the sha256 hash of the key is stored; the plaintext
// is shown once at mint and never persisted. The table lives in the main DB
// (kun_galgame_infra). See docs/developer-platform/04-platform-internals.md §5.2.
//
// Intent columns (Name/KeyHash/KeyPrefix/Last4/Scopes/NSFWAllowed/
// CreatedByUserID/ClientID) are NOT NULL with NO GORM `default:` tag — the
// service assigns every value explicitly, so the GORM zero-value INSERT trap
// (a meaningful false/0 silently replaced by a DB default) cannot bite.
type DeveloperAPIKey struct {
	ID              uint           `gorm:"primaryKey" json:"id"`
	ClientID        string         `gorm:"size:50;not null;index" json:"client_id"`  // FK oauth_clients.id
	Name            string         `gorm:"size:100;not null" json:"name"`            // developer-supplied label
	KeyHash         string         `gorm:"size:80;not null;uniqueIndex" json:"-"`    // "sha256:<hex>"
	KeyPrefix       string         `gorm:"size:24;not null;index" json:"key_prefix"` // nm_live_a1b2
	Last4           string         `gorm:"size:4;not null" json:"last4"`
	Scopes          datatypes.JSON `gorm:"type:jsonb;not null" json:"scopes"` // ⊆ app scopes; never nil ("[]")
	NSFWAllowed     bool           `gorm:"not null" json:"nsfw_allowed"`
	ExpiresAt       *time.Time     `json:"expires_at,omitempty"` // rotation grace / expiry; NULL = never
	RevokedAt       *time.Time     `json:"revoked_at,omitempty"` // revoked → rejected next request
	LastUsedAt      *time.Time     `json:"last_used_at,omitempty"`
	CreatedByUserID uint           `gorm:"not null" json:"created_by_user_id"`
	CreatedAt       time.Time      `json:"created_at"`
}

// TableName returns the table name for DeveloperAPIKey.
func (DeveloperAPIKey) TableName() string { return "developer_api_keys" }

// Active reports whether the key itself is currently usable: not revoked and
// not past its expiry. The owning app's dev_enabled flag is checked separately
// at resolve time (a key on a disabled app is inert regardless of this).
func (k *DeveloperAPIKey) Active(now time.Time) bool {
	if k.RevokedAt != nil {
		return false
	}
	if k.ExpiresAt != nil && !k.ExpiresAt.After(now) {
		return false
	}
	return true
}

// DeveloperAPIUsage is the per-(client, key, face, day) usage rollup, upserted
// periodically from the in-process aggregator (see UsageRecorder). The composite
// unique index spans all four dimensions with every column's priority pinned
// (the GORM shared-index priority trap: mark all or none). key_id 0 is reserved
// as the application-level aggregate sentinel — a plain NOT NULL uint avoids the
// NULLs-distinct semantics a nullable key_id would bring to the unique index.
type DeveloperAPIUsage struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	ClientID string `gorm:"size:50;not null;uniqueIndex:idx_usage_day,priority:1" json:"client_id"`
	KeyID    uint   `gorm:"not null;uniqueIndex:idx_usage_day,priority:2" json:"key_id"` // 0 = app aggregate
	// Face names grew past the original varchar(20) — galgame_internal_write (22)
	// and galgame_internal_propose (24) overflowed it, and because UpsertUsage is
	// one batched INSERT and Flush re-merges on error, a single long face poisoned
	// EVERY subsequent flush on that process (silent metering stall, 06a→06b W1).
	// 40 gives every current face headroom; prod was widened by hand in the 06b W1
	// deploy, cmd/migrate realigns any other environment.
	Face  string `gorm:"size:40;not null;uniqueIndex:idx_usage_day,priority:3" json:"face"` // catalog / galgame / galgame_internal[_write|_propose]
	Day   string `gorm:"size:10;not null;uniqueIndex:idx_usage_day,priority:4" json:"day"`  // YYYY-MM-DD (UTC)
	Count int64  `gorm:"not null" json:"count"`
	// Explicit column tags: GORM's naming strategy renders Status4xx as
	// "status4xx" (no underscore before a digit — the acronym/digit trap), but
	// the upsert SQL and readers use status_4xx / status_5xx.
	Status4xx int64     `gorm:"column:status_4xx;not null" json:"status_4xx"`
	Status5xx int64     `gorm:"column:status_5xx;not null" json:"status_5xx"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName returns the table name for DeveloperAPIUsage.
func (DeveloperAPIUsage) TableName() string { return "developer_api_usage" }

// DeveloperUsageRetentionDays is how many days of per-(client,key,face,day)
// usage rollup are kept. Rows older than this are pruned daily by the
// prune-developer-usage job — developer_api_usage grows unbounded otherwise.
// 400 (~13 months) preserves a full year-over-year window plus slack; it is a
// pinned product/ops decision, not derived from anything.
const DeveloperUsageRetentionDays = 400

// RetentionCutoffDay returns the oldest day (YYYY-MM-DD, UTC) still KEPT: rows
// whose day is strictly less than this are prunable. now is a parameter so the
// boundary is unit-testable.
func RetentionCutoffDay(now time.Time) string {
	return now.UTC().AddDate(0, 0, -DeveloperUsageRetentionDays).Format("2006-01-02")
}
