package devapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	siteModel "api/internal/platform/site/model"

	"gorm.io/datatypes"
)

// Self-service caps (docs/developer-platform/05-developer-portal.md §9 / 06a 裁定 2-3).
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
	// store is the same Redis enforcement backend the middleware increments;
	// the account-level usage view reads its live quota counters (same source as
	// enforcement, not an estimate off the rollup table).
	store Store
}

// NewSelfServiceService builds the self-service service over the shared repo,
// the shared AdminService (so key operations reuse one implementation), and the
// shared counter store (the live-remaining read source).
func NewSelfServiceService(repo *Repository, admin *AdminService, store Store) *SelfServiceService {
	return &SelfServiceService{repo: repo, admin: admin, store: store}
}

// CreateApp registers a new developer application owned by ownerUserID. It is
// admitted to the open API immediately (dev_enabled) at the free tier; tier /
// nsfw / quota are admin-only and stay at their defaults. The per-user app cap
// is enforced first. A client_secret is generated + hashed (never returned —
// Phase 1 is pure API-key; the OAuth flow is Phase 2/3).
func (s *SelfServiceService) CreateApp(ctx context.Context, ownerUserID uint, name, description string, login *UserLoginRequest) (*siteModel.OAuthClient, error) {
	if err := validateAppMeta(name, description, true); err != nil {
		return nil, err
	}
	// The consent screen shows this name beside the user's account; a
	// self-service registration does not get to claim it is us.
	if err := validateAppName(name); err != nil {
		return nil, err
	}
	// User login is opt-in at creation. Without it the app keeps the original
	// fail-closed shape — no grants, no redirect URIs, an API key and nothing
	// else — so every app registered before this existed behaves identically.
	redirectURIs, grants, userScopes := "[]", "[]", ""
	isPublic := false
	if login != nil {
		scopes, err := validateUserLogin(*login)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(login.RedirectURIs)
		if err != nil {
			return nil, err
		}
		redirectURIs = string(encoded)
		// authorization_code to get a token, refresh_token so a launcher that
		// runs for months does not send its user back through consent weekly.
		grants = `["authorization_code","refresh_token"]`
		userScopes = strings.Join(scopes, " ")
		// A desktop launcher ships its binary to the user; anything in it is
		// public. Marking it so is what makes the OAuth service REFUSE its
		// authorization code unless PKCE was used.
		isPublic = true
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
		// Both stay empty for a key-only app (grants empty = fail-closed on
		// /oauth/token: an API-key app cannot mint OAuth tokens). An app that
		// declared user login carries its callbacks and the two code-flow
		// grants instead — see the block above.
		RedirectURIs: datatypes.JSON([]byte(redirectURIs)),
		Grants:       datatypes.JSON([]byte(grants)),
		IsPublic:     isPublic,
		// The app's own scope allow-list holds BOTH credentials' vocabularies:
		// the two public read scopes its API keys draw from (keys' scopes are
		// ⊆ the app by construction) plus whatever consent scopes it declared.
		// The two never collide — one set is asked of a machine, the other of
		// a human — and the OAuth service filters every authorize request
		// against this column, so an app cannot request what it did not
		// register.
		AllowedScopes: datatypes.JSON(appAllowedScopes(userScopes)),
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
func (s *SelfServiceService) UpdateApp(ctx context.Context, ownerUserID uint, clientID string, name, description *string, login *UserLoginRequest) (*siteModel.OAuthClient, error) {
	app, err := s.repo.GetAppByOwner(ctx, clientID, ownerUserID)
	if err != nil {
		return nil, err
	}
	if err := validateAppMetaPtr(name, description); err != nil {
		return nil, err
	}
	if name != nil {
		if err := validateAppName(*name); err != nil {
			return nil, err
		}
	}
	fields := map[string]any{}
	// Declaring (or re-declaring) user login is a full replacement of the
	// callback set and the consent scopes — a patch that merged them would
	// make removing a redirect URI impossible, and a stale callback is exactly
	// the kind of thing an app wants gone the moment it stops using it.
	if login != nil {
		scopes, err := validateUserLogin(*login)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(login.RedirectURIs)
		if err != nil {
			return nil, err
		}
		fields["redirect_uris"] = datatypes.JSON(encoded)
		fields["grants"] = datatypes.JSON([]byte(`["authorization_code","refresh_token"]`))
		fields["allowed_scopes"] = datatypes.JSON(appAllowedScopes(strings.Join(scopes, " ")))
		fields["is_public"] = true
	}
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

// OwnerUsageAppTotal is one app's total usage over the window (named).
type OwnerUsageAppTotal struct {
	ClientID  string `json:"client_id"`
	Name      string `json:"name"`
	Count     int64  `json:"count"`
	Status4xx int64  `json:"status_4xx"`
	Status5xx int64  `json:"status_5xx"`
}

// LiveKeyUsage is one active key's real-time budget, read from the Redis
// enforcement counters (same source the middleware writes, NOT estimated off the
// rollup table). rate_limit / quota_limit are the key's EFFECTIVE limits (tier
// default with any app override; 0 = unlimited/internal). quota_used is the
// requests counted so far today (UTC); quota_reset is the epoch second at the
// next UTC midnight when the daily counter rolls over.
type LiveKeyUsage struct {
	AppName        string `json:"app_name"`
	KeyID          uint   `json:"key_id"`
	RateLimit      int    `json:"rate_limit"`
	QuotaLimit     int    `json:"quota_limit"`
	QuotaUsed      int64  `json:"quota_used"`
	QuotaRemaining int64  `json:"quota_remaining"`
	QuotaReset     int64  `json:"quota_reset"`
}

// OwnerUsageSummary is the account-level usage view: a dense daily series (every
// day in the window, gaps 0-filled, oldest→newest) for the volume chart, the
// per-app and per-face breakdowns, window totals, and the per-key live-remaining
// rows. `since` is the window start (UTC). live_unavailable is set (and live is
// empty) when the Redis enforcement backend is unreachable — a read-face
// degradation, never a 5xx.
type OwnerUsageSummary struct {
	Days            int                  `json:"days"`
	Since           string               `json:"since"`
	TotalCount      int64                `json:"total_count"`
	Total4xx        int64                `json:"total_4xx"`
	Total5xx        int64                `json:"total_5xx"`
	Daily           []UsageDayTotal      `json:"daily"`
	ByApp           []OwnerUsageAppTotal `json:"by_app"`
	ByFace          []UsageFaceTotal     `json:"by_face"`
	Live            []LiveKeyUsage       `json:"live"`
	LiveUnavailable bool                 `json:"live_unavailable,omitempty"`
}

// OwnerUsage aggregates the caller's usage across ALL their apps for the last
// `days` days (inclusive of today, UTC): a dense daily series for the chart plus
// a per-app breakdown sorted by volume. No apps → zero-filled series, empty
// breakdown (never an error).
func (s *SelfServiceService) OwnerUsage(ctx context.Context, ownerUserID uint, days int) (*OwnerUsageSummary, error) {
	apps, err := s.repo.ListAppsByOwner(ctx, ownerUserID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	summary := &OwnerUsageSummary{
		Days:   days,
		Since:  now.AddDate(0, 0, -(days - 1)).Format("2006-01-02"),
		ByApp:  []OwnerUsageAppTotal{},
		ByFace: []UsageFaceTotal{},
		Live:   []LiveKeyUsage{},
	}
	// Live per-key remaining is independent of the rollup window (real-time
	// counters), so it is built even for an owner with zero recorded usage.
	s.fillLive(ctx, summary, ownerUserID, now)

	if len(apps) == 0 {
		summary.Daily = denseDays(now, days, nil)
		return summary, nil
	}

	clientIDs := make([]string, len(apps))
	for i := range apps {
		clientIDs[i] = apps[i].ID
	}
	dayRows, err := s.repo.SumUsageByDay(ctx, clientIDs, summary.Since)
	if err != nil {
		return nil, err
	}
	clientRows, err := s.repo.SumUsageByClient(ctx, clientIDs, summary.Since)
	if err != nil {
		return nil, err
	}
	faceRows, err := s.repo.SumUsageByFace(ctx, clientIDs, summary.Since)
	if err != nil {
		return nil, err
	}
	if faceRows != nil {
		summary.ByFace = faceRows
	}

	summary.Daily = denseDays(now, days, dayRows)
	for i := range summary.Daily {
		summary.TotalCount += summary.Daily[i].Count
		summary.Total4xx += summary.Daily[i].Status4xx
		summary.Total5xx += summary.Daily[i].Status5xx
	}

	totalsByID := make(map[string]UsageClientTotal, len(clientRows))
	for _, r := range clientRows {
		totalsByID[r.ClientID] = r
	}
	for i := range apps {
		t := totalsByID[apps[i].ID]
		summary.ByApp = append(summary.ByApp, OwnerUsageAppTotal{
			ClientID:  apps[i].ID,
			Name:      apps[i].Name,
			Count:     t.Count,
			Status4xx: t.Status4xx,
			Status5xx: t.Status5xx,
		})
	}
	slices.SortFunc(summary.ByApp, func(a, b OwnerUsageAppTotal) int {
		switch {
		case a.Count > b.Count:
			return -1
		case a.Count < b.Count:
			return 1
		default:
			return 0
		}
	})
	return summary, nil
}

// fillLive populates summary.Live with each active key's real-time budget read
// from the Redis quota counters. When the counter backend is unreachable it
// leaves Live empty and sets LiveUnavailable (read-face degradation — the rest
// of the summary, sourced from the rollup table, is unaffected). A per-key DB
// error is non-fatal: live is a best-effort adjunct, not the primary payload.
func (s *SelfServiceService) fillLive(ctx context.Context, summary *OwnerUsageSummary, ownerUserID uint, now time.Time) {
	if s.store == nil || !s.store.Available(ctx) {
		summary.LiveUnavailable = true
		return
	}
	keys, err := s.repo.ListOwnerActiveKeys(ctx, ownerUserID, now)
	if err != nil {
		summary.LiveUnavailable = true
		return
	}
	day := now.UTC().Format("2006-01-02")
	reset := nextDayStartUnix(now)
	for _, k := range keys {
		cred := &Credential{Tier: k.DevTier, RateOverride: k.DevRatePerMin, QuotaOverride: k.DevQuotaDaily}
		rate, _ := cred.EffectiveRate()
		quota, unlimited := cred.EffectiveQuota()
		row := LiveKeyUsage{
			AppName:    k.AppName,
			KeyID:      k.KeyID,
			RateLimit:  rate,
			QuotaLimit: quota,
			QuotaReset: reset,
		}
		if !unlimited {
			row.QuotaUsed = s.readCounter(ctx, quotaCounterKey(k.KeyID, day))
			if rem := int64(quota) - row.QuotaUsed; rem > 0 {
				row.QuotaRemaining = rem
			}
		}
		summary.Live = append(summary.Live, row)
	}
}

// readCounter reads an integer enforcement counter through the store (a miss,
// an outage, or an unparseable value all read as 0 — the live view never errors
// on a single stale counter).
func (s *SelfServiceService) readCounter(ctx context.Context, key string) int64 {
	b, err := s.store.Get(ctx, key)
	if err != nil || len(b) == 0 {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// denseDays turns sparse (day → totals) rows into a gap-free series over the
// last `days` days ending today (UTC), oldest→newest, so the chart has a stable
// x-axis. Missing days become zero rows.
func denseDays(now time.Time, days int, rows []UsageDayTotal) []UsageDayTotal {
	byDay := make(map[string]UsageDayTotal, len(rows))
	for _, r := range rows {
		byDay[r.Day] = r
	}
	out := make([]UsageDayTotal, 0, days)
	for i := days - 1; i >= 0; i-- {
		day := now.AddDate(0, 0, -i).Format("2006-01-02")
		if r, ok := byDay[day]; ok {
			r.Day = day
			out = append(out, r)
		} else {
			out = append(out, UsageDayTotal{Day: day})
		}
	}
	return out
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
