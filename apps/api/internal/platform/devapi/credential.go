package devapi

import "slices"

// Tier names for the developer platform. D2: developer identity/role stays on
// the IdP five global roles; tier is platform-internal data granted by the
// admin surface — not a new global role. See docs/developer-platform §7.
const (
	TierFree     = "free"
	TierTrusted  = "trusted"
	TierInternal = "internal"
)

// Public-read scopes. galgame:nsfw is defined for the content_limit gate but is
// not granted to any Phase 1 key (裁定 6 — NSFW scope not issued yet).
//
// ScopeGalgameWrite (galgame:write) gates the /internal user-write face
// (09-open-api-phase2 06a doc 23 D3). It is deliberately NOT in the
// selfServiceScopes allow-list: a write scope is minted only by the admin
// console / SQL, never self-service, until the proposal face + trust tiers
// (06b) are ready to gate third-party writers.
const (
	ScopeCatalogRead  = "catalog:read"
	ScopeGalgameRead  = "galgame:read"
	ScopeGalgameNSFW  = "galgame:nsfw"
	ScopeGalgameWrite = "galgame:write"
)

// TierLimits returns a tier's default per-minute rate and daily quota.
// unlimited (internal) means neither limit is enforced. An unknown tier falls
// through to the tightest (free) limits — fail-safe.
func TierLimits(tier string) (ratePerMin, quotaDaily int, unlimited bool) {
	switch tier {
	case TierInternal:
		return 0, 0, true
	case TierTrusted:
		return 600, 1_000_000, false
	default: // free
		return 60, 50_000, false
	}
}

// Credential is the resolved, cacheable identity of an authenticated open-API
// request: a developer_api_keys row joined to its owning oauth_clients app. It
// is JSON-marshalled into the 60s resolve cache, so every field is exported.
type Credential struct {
	KeyID         uint     `json:"key_id"`
	ClientID      string   `json:"client_id"`
	AppName       string   `json:"app_name"`
	Tier          string   `json:"tier"`
	Scopes        []string `json:"scopes"`
	NSFWAllowed   bool     `json:"nsfw_allowed"`   // effective: key AND app both allow
	RateOverride  int      `json:"rate_override"`  // app dev_rate_per_min; 0 = tier default
	QuotaOverride int      `json:"quota_override"` // app dev_quota_daily; 0 = tier default
}

// EffectiveRate resolves the per-minute request limit: a positive app override
// wins over the tier default; internal tier is unlimited (0 = "use default"
// mirrors the OAuthClient RefreshTokenTTL 0-sentinel convention).
func (c *Credential) EffectiveRate() (limit int, unlimited bool) {
	def, _, unl := TierLimits(c.Tier)
	if unl {
		return 0, true
	}
	if c.RateOverride > 0 {
		return c.RateOverride, false
	}
	return def, false
}

// EffectiveQuota resolves the daily request quota (same override semantics as
// EffectiveRate).
func (c *Credential) EffectiveQuota() (limit int, unlimited bool) {
	_, def, unl := TierLimits(c.Tier)
	if unl {
		return 0, true
	}
	if c.QuotaOverride > 0 {
		return c.QuotaOverride, false
	}
	return def, false
}

// HasScope reports whether the credential carries scope s.
func (c *Credential) HasScope(s string) bool {
	return slices.Contains(c.Scopes, s)
}
