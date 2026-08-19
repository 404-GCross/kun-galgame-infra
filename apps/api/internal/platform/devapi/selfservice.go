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

const (
	MaxAppsPerOwner     = 5
	MaxActiveKeysPerApp = 5
	maxAppNameLen       = 100
	maxAppDescLen       = 100
)

// Scopes a key owner may tick unilaterally. galgame:read left this list when the
// /v1/galgame face retired to a 410 tombstone (wave 146): no live route consumes
// it, so offering it minted a permission over nothing.
var selfServiceScopes = []string{ScopeCatalogRead}

// Scopes a key owner may hold only after we approve an application. The news
// partners authorised NextMoe's index, not whoever asks us for it, so who
// carries news:read stays our decision — the application only mechanises the
// paperwork, it does not make the scope self-service.
var grantableScopes = []string{ScopeNewsRead}

var (
	ErrAppLimitReached = errors.New("devapi: application limit reached")
	ErrKeyLimitReached = errors.New("devapi: active key limit reached")
	ErrScopeNotAllowed = errors.New("devapi: scope not permitted (want catalog:read)")
	ErrScopeNeedsGrant = errors.New("devapi: scope requires an approved application")
	ErrNameRequired    = errors.New("devapi: name is required")
	ErrNameTooLong     = errors.New("devapi: name too long (max 100)")
	ErrDescTooLong     = errors.New("devapi: description too long (max 100)")
)

type SelfServiceService struct {
	repo  *Repository
	admin *AdminService
	store Store
}

func NewSelfServiceService(repo *Repository, admin *AdminService, store Store) *SelfServiceService {
	return &SelfServiceService{repo: repo, admin: admin, store: store}
}

func (s *SelfServiceService) CreateApp(ctx context.Context, ownerUserID uint, name, description string, login *UserLoginRequest) (*siteModel.OAuthClient, error) {
	if err := validateAppMeta(name, description, true); err != nil {
		return nil, err
	}
	if err := validateAppName(name); err != nil {
		return nil, err
	}
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
		grants = `["authorization_code","refresh_token"]`
		userScopes = strings.Join(scopes, " ")
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
		ID:             clientID,
		Name:           name,
		Secret:         siteModel.HashOAuthClientSecret(secret),
		RedirectURIs:   datatypes.JSON([]byte(redirectURIs)),
		Grants:         datatypes.JSON([]byte(grants)),
		IsPublic:       isPublic,
		AllowedScopes:  datatypes.JSON(appAllowedScopes(userScopes)),
		Tagline:        description,
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
		fields["tagline"] = *description
	}
	if err := s.repo.UpdateAppFields(ctx, app.ID, fields); err != nil {
		return nil, err
	}
	return s.repo.GetApp(ctx, app.ID)
}

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
		if err := s.admin.RevokeKey(ctx, keys[i].ID); err != nil {
			return err
		}
	}
	return s.repo.UpdateAppFields(ctx, app.ID, map[string]any{"dev_enabled": false})
}

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
	if err := s.checkMintScopes(ctx, ownerUserID, in.Scopes); err != nil {
		return nil, "", err
	}
	active, err := s.repo.CountActiveKeysByClient(ctx, clientID, time.Now())
	if err != nil {
		return nil, "", err
	}
	if active >= MaxActiveKeysPerApp {
		return nil, "", ErrKeyLimitReached
	}
	return s.admin.MintKey(ctx, clientID, in, ownerUserID)
}

func (s *SelfServiceService) ListKeys(ctx context.Context, ownerUserID uint, clientID string) ([]DeveloperAPIKey, error) {
	if _, err := s.repo.GetAppByOwner(ctx, clientID, ownerUserID); err != nil {
		return nil, err
	}
	return s.admin.ListKeys(ctx, clientID)
}

func (s *SelfServiceService) RotateKey(ctx context.Context, ownerUserID uint, clientID string, keyID uint) (*DeveloperAPIKey, string, error) {
	if _, err := s.repo.GetAppByOwner(ctx, clientID, ownerUserID); err != nil {
		return nil, "", err
	}
	key, err := s.admin.GetKeyForClient(ctx, clientID, keyID)
	if err != nil {
		return nil, "", err
	}
	if key == nil {
		return nil, "", nil
	}
	return s.admin.RotateKey(ctx, keyID, ownerUserID)
}

func (s *SelfServiceService) RevokeKey(ctx context.Context, ownerUserID uint, clientID string, keyID uint) (found bool, err error) {
	if _, err := s.repo.GetAppByOwner(ctx, clientID, ownerUserID); err != nil {
		return false, err
	}
	key, err := s.admin.GetKeyForClient(ctx, clientID, keyID)
	if err != nil {
		return false, err
	}
	if key == nil {
		return false, nil
	}
	return true, s.admin.RevokeKey(ctx, keyID)
}

func (s *SelfServiceService) Usage(ctx context.Context, ownerUserID uint, clientID string, days int) ([]UsageDayFace, error) {
	if _, err := s.repo.GetAppByOwner(ctx, clientID, ownerUserID); err != nil {
		return nil, err
	}
	since := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	return s.repo.AggregateUsageByClient(ctx, clientID, since)
}

type OwnerUsageAppTotal struct {
	ClientID  string `json:"client_id"`
	Name      string `json:"name"`
	Count     int64  `json:"count"`
	Status4xx int64  `json:"status_4xx"`
	Status5xx int64  `json:"status_5xx"`
}

type LiveKeyUsage struct {
	AppName        string `json:"app_name"`
	KeyID          uint   `json:"key_id"`
	RateLimit      int    `json:"rate_limit"`
	QuotaLimit     int    `json:"quota_limit"`
	QuotaUsed      int64  `json:"quota_used"`
	QuotaRemaining int64  `json:"quota_remaining"`
	QuotaReset     int64  `json:"quota_reset"`
}

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

type scopeGate int

const (
	scopeGateDenied scopeGate = iota
	scopeGateSelfService
	scopeGateGrant
)

func gateForScope(sc string) scopeGate {
	switch {
	case slices.Contains(selfServiceScopes, sc):
		return scopeGateSelfService
	case slices.Contains(grantableScopes, sc):
		return scopeGateGrant
	default:
		return scopeGateDenied
	}
}

func (s *SelfServiceService) checkMintScopes(ctx context.Context, ownerUserID uint, scopes []string) error {
	for _, sc := range scopes {
		switch gateForScope(sc) {
		case scopeGateSelfService:
		case scopeGateGrant:
			approved, err := s.repo.HasApprovedScopeApplication(ctx, ownerUserID, sc)
			if err != nil {
				return err
			}
			if !approved {
				return ErrScopeNeedsGrant
			}
		default:
			return ErrScopeNotAllowed
		}
	}
	return nil
}

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

func generateHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
