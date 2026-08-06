package service

import (
	"sync"
	"time"

	"api/internal/platform/trust/model"

	"gorm.io/gorm"
)

// PlatformDefaults is the posture a site gets when it has expressed no opinion:
// today's env-and-constant values, passed in from main so this package keeps no
// hidden dependency on configuration.
type PlatformDefaults struct {
	ScanMode           int16
	SampleRate         float64
	AggregateThreshold float32
	AutoHideEnabled    bool
}

// ResolvedPolicy is the posture actually in force for one site: the platform
// defaults with any per-site override applied. Every field is a concrete value
// except FlagThreshold, whose absence is itself the meaning ("defer to the AI
// gateway's own verdict") rather than a missing setting.
type ResolvedPolicy struct {
	ScanMode           int16
	SampleRate         float64
	FlagThreshold      *float32
	AggregateThreshold float32
	AutoHideEnabled    bool
}

// PolicyService resolves the per-site moderation posture (step 07 M0). It is
// read on the scan worker's hot path, so the whole table — a handful of rows,
// one per onboarded site — is cached in process behind a TTL, the same shape
// TermService uses, and an admin write invalidates it in-process immediately.
// Cross-instance staleness is bounded by the TTL.
//
// The resolution rule is the single thing to keep in mind: a NULL column is not
// a missing value, it is an explicit "no opinion". A site with no row at all is
// therefore governed entirely by the platform defaults — which is exactly how
// every site behaves today, and why introducing this table is a no-op until
// somebody writes to it.
type PolicyService struct {
	db       *gorm.DB
	defaults PlatformDefaults
	ttl      time.Duration
	now      func() time.Time

	mu       sync.Mutex
	cache    map[string]model.TrustSitePolicy
	loaded   bool
	loadedAt time.Time
}

// DefaultAggregateThreshold exposes the platform report-weight threshold to
// main, which assembles PlatformDefaults. It stays an unexported constant
// otherwise so this package remains the only place it can be changed.
func DefaultAggregateThreshold() float32 { return aggregateThreshold }

func NewPolicyService(db *gorm.DB, defaults PlatformDefaults) *PolicyService {
	return &PolicyService{
		db:       db,
		defaults: defaults,
		ttl:      policyCacheTTL,
		now:      time.Now,
	}
}

// Defaults exposes the platform baseline so an admin surface can show what a
// NULL override actually resolves to. Displaying "inherits the default" without
// saying WHICH value that is makes the console unusable for the decision it
// exists to support.
func (s *PolicyService) Defaults() PlatformDefaults { return s.defaults }

// Resolve returns the posture in force for a site. It never fails: a database
// error falls back to the platform defaults rather than propagating, because
// the caller is the scan worker and the alternative to a slightly stale posture
// is no moderation at all.
func (s *PolicyService) Resolve(site string) ResolvedPolicy {
	resolved := ResolvedPolicy{
		ScanMode:           s.defaults.ScanMode,
		SampleRate:         s.defaults.SampleRate,
		AggregateThreshold: s.defaults.AggregateThreshold,
		AutoHideEnabled:    s.defaults.AutoHideEnabled,
	}
	row, ok := s.lookup(site)
	if !ok {
		return resolved
	}
	if row.ScanMode != nil {
		resolved.ScanMode = *row.ScanMode
	}
	if row.SampleRate != nil {
		resolved.SampleRate = *row.SampleRate
	}
	if row.AggregateThreshold != nil {
		resolved.AggregateThreshold = *row.AggregateThreshold
	}
	if row.AutoHideEnabled != nil {
		resolved.AutoHideEnabled = *row.AutoHideEnabled
	}
	resolved.FlagThreshold = row.FlagThreshold
	return resolved
}

// lookup serves the cached row for a site, reloading the whole table when the
// snapshot has expired.
func (s *PolicyService) lookup(site string) (model.TrustSitePolicy, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded || s.now().Sub(s.loadedAt) >= s.ttl {
		var rows []model.TrustSitePolicy
		if err := s.db.Find(&rows).Error; err != nil {
			// Serve the previous snapshot if there is one; an empty map otherwise
			// means every site resolves to the platform defaults, which is the
			// safe direction to fail in.
			if s.loaded {
				row, ok := s.cache[site]
				return row, ok
			}
			return model.TrustSitePolicy{}, false
		}
		cache := make(map[string]model.TrustSitePolicy, len(rows))
		for _, r := range rows {
			cache[r.Site] = r
		}
		s.cache, s.loaded, s.loadedAt = cache, true, s.now()
	}
	row, ok := s.cache[site]
	return row, ok
}

// Invalidate drops the cached snapshot so the next Resolve reloads. Called by
// the admin write path in-process; other instances catch up within the TTL.
func (s *PolicyService) Invalidate() {
	s.mu.Lock()
	s.loaded = false
	s.mu.Unlock()
}

// List returns every stored policy row, site-ordered, for the admin console.
func (s *PolicyService) List() ([]model.TrustSitePolicy, error) {
	var rows []model.TrustSitePolicy
	if err := s.db.Order("site").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// Upsert writes a site's overrides wholesale: the caller sends the complete
// desired state and every column is written, NULL included. A partial update
// would make "clear this override" unexpressible — the one operation an
// operator needs most when backing a site out of a posture that isn't working.
func (s *PolicyService) Upsert(p *model.TrustSitePolicy) error {
	now := s.now()
	p.UpdatedAt = now
	if err := s.db.Exec(`
		INSERT INTO trust_site_policy
		    (site, scan_mode, sample_rate, flag_threshold, aggregate_threshold,
		     auto_hide_enabled, note, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (site) DO UPDATE SET
		    scan_mode           = EXCLUDED.scan_mode,
		    sample_rate         = EXCLUDED.sample_rate,
		    flag_threshold      = EXCLUDED.flag_threshold,
		    aggregate_threshold = EXCLUDED.aggregate_threshold,
		    auto_hide_enabled   = EXCLUDED.auto_hide_enabled,
		    note                = EXCLUDED.note,
		    updated_at          = EXCLUDED.updated_at`,
		p.Site, p.ScanMode, p.SampleRate, p.FlagThreshold, p.AggregateThreshold,
		p.AutoHideEnabled, p.Note, now, now).Error; err != nil {
		return err
	}
	s.Invalidate()
	return nil
}

// MaxScanSampleRate exposes the calibration-sample cap so the admin face can
// REJECT an out-of-range rate instead of silently clamping it. Silently clamping
// a number an operator typed is how someone ends up believing sampling is
// running at a rate it is not.
func MaxScanSampleRate() float64 { return maxScanSampleRate }
