package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"api/internal/platform/trust/model"
	"api/internal/platform/trust/norm"

	"gorm.io/gorm"
)

// Tier0 check decisions (step 05; doc 18 §6). deny is a hard block (a banned
// term matched); hold routes to review (only a suspect term matched); allow is
// the clean path. deny wins over hold.
const (
	DecisionAllow = "allow"
	DecisionDeny  = "deny"
	DecisionHold  = "hold"
)

// TermService owns the Tier0 deterministic word list (step 05; doc 18 §6): the
// admin CRUD registry, an in-memory TTL-refreshed snapshot of active terms, and
// the normalized-substring matcher that backs BOTH the sync /trust/check face
// and the async scan worker's tier0_matched recording. It is entirely channel-
// independent (no AI gateway).
//
// The matcher is a substring-containment scan (CJK has no word boundaries, so a
// substring is the v0 reality). Both stored terms and the checked text pass
// through the single norm.Normalize choke point, so folding stays consistent.
// The active-term set is cached in-process (termCacheTTL) — the hot path never
// hits the DB except on a refresh — and invalidated immediately on an in-process
// admin mutation; multi-instance staleness is bounded by the TTL (accepted).
type TermService struct {
	db        *gorm.DB
	allowlist map[string]bool
	ttl       time.Duration
	now       func() time.Time

	mu       sync.Mutex
	cache    []activeTerm
	loaded   bool
	loadedAt time.Time
}

// activeTerm is one non-deprecated term projected for matching. site "" = a
// global term (applies to every site); a non-empty site scopes it.
type activeTerm struct {
	norm   string
	site   string
	banned bool
}

// NewTermService builds the service over the trust DB. allowlist is the shared
// forwarder client-id set (same counterweight as scan/forward): a wire-supplied
// `site` on /trust/check is allowed only for a listed forwarder.
func NewTermService(db *gorm.DB, allowlist map[string]bool) *TermService {
	return &TermService{db: db, allowlist: allowlist, ttl: termCacheTTL, now: time.Now}
}

func (s *TermService) allowed(clientID string) bool {
	return clientID != "" && s.allowlist[clientID]
}

// --- matching -------------------------------------------------------------

// CheckParams is a normalized /trust/check request (mirrors ScanParams' three-
// state site resolution). AuthorID is accepted for the future repeat-offender
// weighting but is NOT consulted in v0.
type CheckParams struct {
	CallerClientID string
	Site           string
	WireSite       string
	Text           string
	AuthorID       *int64
}

// CheckResult is the check verdict: a decision plus the matched normalized
// terms (S2S audience — safe to relay). Matched is never nil (a no-match is []).
type CheckResult struct {
	Decision string
	Matched  []string
}

// Check resolves the three-state site (empty = the bound site; a wire site is
// allowlist-gated and wins) and matches the text. It never touches the registry
// (check is text-vs-terms, not subject-vs-kind) and never persists a row — it is
// a fast, stateless gate (fail-open is the caller's posture; this face only
// answers quickly).
func (s *TermService) Check(ctx context.Context, p CheckParams) (CheckResult, error) {
	site := p.Site
	if p.WireSite != "" {
		if !s.allowed(p.CallerClientID) {
			return CheckResult{}, ErrForwarderNotAllowed
		}
		site = p.WireSite
	}
	terms, err := s.snapshot(ctx)
	if err != nil {
		return CheckResult{}, err
	}
	decision, matched := matchTerms(terms, site, norm.Normalize(p.Text))
	return CheckResult{Decision: decision, Matched: matched}, nil
}

// Tier0Matches returns just the matched normalized terms for a resolved site —
// the scan worker's landing input (site already comes from the stored row, so
// there is no three-state resolution here). Never nil: a no-match is [].
func (s *TermService) Tier0Matches(ctx context.Context, site, text string) ([]string, error) {
	terms, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	_, matched := matchTerms(terms, site, norm.Normalize(text))
	return matched, nil
}

// matchTerms scans the active set for the given site against the already-
// normalized text. A global term (site "") applies to every site; a per-site
// term applies only to its own site. deny wins over hold.
func matchTerms(terms []activeTerm, site, normText string) (string, []string) {
	matched := []string{}
	deny, hold := false, false
	for _, t := range terms {
		if t.norm == "" { // defensive: an empty norm would match everything
			continue
		}
		if t.site != "" && t.site != site {
			continue
		}
		if strings.Contains(normText, t.norm) {
			matched = append(matched, t.norm)
			if t.banned {
				deny = true
			} else {
				hold = true
			}
		}
	}
	switch {
	case deny:
		return DecisionDeny, matched
	case hold:
		return DecisionHold, matched
	default:
		return DecisionAllow, matched
	}
}

// snapshot returns the cached active-term set, reloading from the DB when the
// TTL has elapsed or the cache was invalidated. Holds a mutex across the reload
// (a once-per-TTL cost; v0 volume makes the brief serialization a non-issue).
func (s *TermService) snapshot(ctx context.Context) ([]activeTerm, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded && s.now().Sub(s.loadedAt) < s.ttl {
		return s.cache, nil
	}
	var rows []model.TrustTerm
	if err := s.db.WithContext(ctx).Where("is_deprecated = false").Find(&rows).Error; err != nil {
		return nil, err
	}
	snap := make([]activeTerm, 0, len(rows))
	for _, r := range rows {
		site := ""
		if r.Site != nil {
			site = *r.Site
		}
		snap = append(snap, activeTerm{norm: r.TermNorm, site: site, banned: r.Kind == model.TermKindBanned})
	}
	s.cache = snap
	s.loaded = true
	s.loadedAt = s.now()
	return snap, nil
}

// invalidate forces the next snapshot to reload (called after an in-process
// admin mutation, so the change is visible immediately in this instance).
func (s *TermService) invalidate() {
	s.mu.Lock()
	s.loaded = false
	s.mu.Unlock()
}

// --- admin CRUD -----------------------------------------------------------

// TermFilters filters an admin term listing. Site "" = all sites; Kind nil =
// both kinds.
type TermFilters struct {
	Site              string
	Kind              *int16
	IncludeDeprecated bool
}

// List returns terms for the admin surface, global first then per-site.
func (s *TermService) List(ctx context.Context, f TermFilters) ([]model.TrustTerm, error) {
	q := s.db.WithContext(ctx).Model(&model.TrustTerm{})
	if f.Site != "" {
		q = q.Where("site = ?", f.Site)
	}
	if f.Kind != nil {
		q = q.Where("kind = ?", *f.Kind)
	}
	if !f.IncludeDeprecated {
		q = q.Where("is_deprecated = false")
	}
	var terms []model.TrustTerm
	err := q.Order("site NULLS FIRST").Order("term_norm ASC").Find(&terms).Error
	return terms, err
}

// CreateTermParams registers one Tier0 term. Term is the RAW admin input; the
// service normalizes it before storage. Site nil (or blank) = a global term.
type CreateTermParams struct {
	ActorID int64
	Site    *string
	Term    string
	Kind    int16
	Note    *string
}

// Create normalizes and inserts a term. An empty norm or a bad kind is rejected
// fail-loud; a duplicate active (site-or-global, norm) → ErrTermExists (deprecate
// + re-create, never a duplicate — registry discipline). Invalidates the cache.
func (s *TermService) Create(ctx context.Context, p CreateTermParams) (*model.TrustTerm, error) {
	if p.Kind != model.TermKindSuspect && p.Kind != model.TermKindBanned {
		return nil, ErrTermInvalidKind
	}
	site := p.Site
	if site != nil && strings.TrimSpace(*site) == "" {
		site = nil // an empty per-site value is a global term
	}
	normTerm := norm.Normalize(p.Term)
	if normTerm == "" {
		return nil, ErrTermEmpty
	}
	term := model.TrustTerm{Site: site, TermNorm: normTerm, Kind: p.Kind, Note: p.Note, IsDeprecated: false}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&model.TrustTerm{}).
			Where("COALESCE(site, '') = COALESCE(?, '') AND term_norm = ? AND is_deprecated = false", site, normTerm).
			Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return ErrTermExists
		}
		if err := tx.Create(&term).Error; err != nil {
			if isTermDuplicate(err) { // the partial-unique index backstops a race
				return ErrTermExists
			}
			return err
		}
		return AppendAudit(tx, AuditEntry{ActorID: &p.ActorID, Action: "term_created", Site: site})
	})
	if err != nil {
		return nil, err
	}
	s.invalidate()
	return &term, nil
}

// Deprecate retires a term (never a hard delete — registry discipline). A
// re-deprecate is a tolerated no-op. Invalidates the cache.
func (s *TermService) Deprecate(ctx context.Context, actorID, id int64) (*model.TrustTerm, error) {
	var term model.TrustTerm
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		lerr := tx.Take(&term, id).Error
		if errors.Is(lerr, gorm.ErrRecordNotFound) {
			return ErrTermNotFound
		}
		if lerr != nil {
			return lerr
		}
		if !term.IsDeprecated {
			if err := tx.Model(&model.TrustTerm{}).Where("id = ?", id).Update("is_deprecated", true).Error; err != nil {
				return err
			}
		}
		if err := tx.Take(&term, id).Error; err != nil {
			return err
		}
		return AppendAudit(tx, AuditEntry{ActorID: &actorID, Action: "term_deprecated", Site: term.Site})
	})
	if err != nil {
		return nil, err
	}
	s.invalidate()
	return &term, nil
}

// isTermDuplicate reports whether err is the trust_term active-uniqueness
// violation (Postgres 23505 on uq_trust_term_active). Detected by message so no
// pg driver dependency is pulled into the service — the pre-check inside the tx
// is the primary guard; this only backstops a concurrent-insert race.
func isTermDuplicate(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "uq_trust_term_active") ||
		strings.Contains(msg, "duplicate key value")
}
