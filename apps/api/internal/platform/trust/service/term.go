package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"api/internal/platform/trust/actrie"
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
// The matcher is a byte-level Aho-Corasick automaton so it stays flat under tens
// of thousands of terms. A hit is then boundary-checked per term: a term whose
// end is ASCII-alphanumeric requires a non-alphanumeric neighbour there, while a
// CJK term keeps pure substring semantics (CJK has no word boundaries to demand). Both stored terms and the
// checked text pass through the single norm.Normalize choke point, so folding
// stays consistent. The active-term set — and the automaton built from it — is
// cached in-process (termCacheTTL); the hot path never hits the DB except on a
// refresh, and is invalidated immediately on an in-process admin mutation.
// Multi-instance staleness is bounded by the TTL (accepted).
type TermService struct {
	db        *gorm.DB
	allowlist map[string]bool
	ttl       time.Duration
	now       func() time.Time

	mu       sync.Mutex
	cache    *termSnapshot
	loaded   bool
	loadedAt time.Time
}

// activeTerm is one non-deprecated term projected for matching. site "" = a
// global term (applies to every site); a non-empty site scopes it.
type activeTerm struct {
	norm   string
	site   string
	banned bool
	lead   bool
	trail  bool
}

func isASCIIAlnum(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// matchesWithBoundary re-checks an automaton hit for the boundary its ends
// demand. Terms with no ASCII-alnum end demand nothing and short-circuit.
//
// Substring-only matching measured at 1.58% aggregate precision on 2026-08-09:
// `ice` fired on "service", `master` on "mastercard", `info` on "information",
// and the numeric terms landed inside longer IDs. Those terms read as bad terms
// on the precision report; they were a bad matcher.
func (t *activeTerm) matchesWithBoundary(text string) bool {
	if !t.lead && !t.trail {
		return true
	}
	for i := 0; i <= len(text)-len(t.norm); {
		j := strings.Index(text[i:], t.norm)
		if j < 0 {
			return false
		}
		s, e := i+j, i+j+len(t.norm)
		leadOK := !t.lead || s == 0 || !isASCIIAlnum(text[s-1])
		trailOK := !t.trail || e == len(text) || !isASCIIAlnum(text[e])
		if leadOK && trailOK {
			return true
		}
		i = s + 1
	}
	return false
}

// termSnapshot is the compiled active-term set: the term table plus the Aho-
// Corasick automaton built over the term norms (payload = index into terms).
// Immutable once built and shared read-only across concurrent checks; rebuilt
// only on a snapshot reload. The old []activeTerm cache became this struct so the
// automaton is built once per reload rather than per check (spec step 07 §3); the
// change is entirely internal — Check/Tier0Matches are unaffected.
type termSnapshot struct {
	terms   []activeTerm
	matcher *actrie.Matcher
}

// buildSnapshot compiles the projected terms into an automaton. Empty norms are
// left in terms (index alignment with the payloads) but Build never inserts them,
// so they never match — the same defensive skip the linear scan applied.
func buildSnapshot(terms []activeTerm) *termSnapshot {
	patterns := make([][]byte, len(terms))
	for i := range terms {
		t := &terms[i]
		if t.norm != "" {
			t.lead = isASCIIAlnum(t.norm[0])
			t.trail = isASCIIAlnum(t.norm[len(t.norm)-1])
		}
		patterns[i] = []byte(t.norm)
	}
	return &termSnapshot{terms: terms, matcher: actrie.Build(patterns)}
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
	snap, err := s.snapshot(ctx)
	if err != nil {
		return CheckResult{}, err
	}
	decision, matched := snap.match(site, norm.Normalize(p.Text))
	return CheckResult{Decision: decision, Matched: matched}, nil
}

// Tier0Matches returns just the matched normalized terms for a resolved site —
// the scan worker's landing input (site already comes from the stored row, so
// there is no three-state resolution here). Never nil: a no-match is [].
func (s *TermService) Tier0Matches(ctx context.Context, site, text string) ([]string, error) {
	snap, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	_, matched := snap.match(site, norm.Normalize(text))
	return matched, nil
}

// match runs the already-normalized text through the automaton and applies the
// scoping + decision rules. The automaton reports the distinct hit term indexes
// in ascending (insertion) order; for each we apply site scoping — a global term
// (site "") fires for every site, a per-site term only for its own — then dedupe
// is inherent (one payload per term) and matched is built in that order. deny
// wins over hold.
func (snap *termSnapshot) match(site, normText string) (string, []string) {
	matched := []string{}
	deny, hold := false, false
	for _, i := range snap.matcher.Match([]byte(normText)) {
		t := &snap.terms[i]
		if t.site != "" && t.site != site {
			continue
		}
		if !t.matchesWithBoundary(normText) {
			continue
		}
		matched = append(matched, t.norm)
		if t.banned {
			deny = true
		} else {
			hold = true
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

// snapshot returns the cached compiled snapshot (terms + automaton), reloading
// from the DB and rebuilding the automaton when the TTL has elapsed or the cache
// was invalidated. Holds a mutex across the reload + build (a once-per-TTL cost;
// building over tens of thousands of terms is a sub-second one-off, so the brief
// serialization stays a non-issue).
func (s *TermService) snapshot(ctx context.Context) (*termSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded && s.now().Sub(s.loadedAt) < s.ttl {
		return s.cache, nil
	}
	var rows []model.TrustTerm
	if err := s.db.WithContext(ctx).Where("is_deprecated = false").Find(&rows).Error; err != nil {
		return nil, err
	}
	terms := make([]activeTerm, 0, len(rows))
	for _, r := range rows {
		site := ""
		if r.Site != nil {
			site = *r.Site
		}
		terms = append(terms, activeTerm{norm: r.TermNorm, site: site, banned: r.Kind == model.TermKindBanned})
	}
	snap := buildSnapshot(terms)
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
// both kinds; Query "" = no substring search. Page/Limit page the result the
// same way ReviewFilters does — the word list is not a small table (46,434 live
// terms in production on 2026-08-06), so an unpaged listing is not an option.
type TermFilters struct {
	Site              string
	Kind              *int16
	// Purpose nil = both. Filtering by it is how an operator audits one lexicon
	// at a time — the compliance list and the abuse list are maintained on
	// completely different evidence, so they are rarely reviewed together.
	Purpose           *int16
	IncludeDeprecated bool
	Query             string
	Page              int
	Limit             int
}

// List returns one page of terms for the admin surface, global first then
// per-site, plus the total matching the filters (for the pager).
func (s *TermService) List(ctx context.Context, f TermFilters) ([]model.TrustTerm, int64, error) {
	q := s.db.WithContext(ctx).Model(&model.TrustTerm{})
	if f.Site != "" {
		q = q.Where("site = ?", f.Site)
	}
	if f.Kind != nil {
		q = q.Where("kind = ?", *f.Kind)
	}
	if f.Purpose != nil {
		q = q.Where("purpose = ?", *f.Purpose)
	}
	if !f.IncludeDeprecated {
		q = q.Where("is_deprecated = false")
	}
	// Search matches the NORMALIZED form, because that is what the gate actually
	// compares against and what the table stores. The raw word an operator types
	// goes through the same normalization first, so searching "ＳＰＡＭ" finds
	// the term stored as "spam".
	if needle := norm.Normalize(f.Query); needle != "" {
		q = q.Where("term_norm LIKE ?", "%"+escapeLike(needle)+"%")
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	limit := clampLimit(f.Limit)
	offset := 0
	if f.Page > 1 {
		offset = (f.Page - 1) * limit
	}
	var terms []model.TrustTerm
	// id breaks ties: (site, term_norm) is unique, but paging over a table that
	// is being written to needs a total order or rows can repeat across pages.
	if err := q.Order("site NULLS FIRST").Order("term_norm ASC").Order("id ASC").
		Limit(limit).Offset(offset).Find(&terms).Error; err != nil {
		return nil, 0, err
	}
	return terms, total, nil
}

// escapeLike neutralizes the LIKE wildcards in operator input so a search for
// "100%" looks for that literal string instead of matching everything.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// CreateTermParams registers one Tier0 term. Term is the RAW admin input; the
// service normalizes it before storage. Site nil (or blank) = a global term.
type CreateTermParams struct {
	ActorID int64
	Site    *string
	Term    string
	Kind    int16
	// Purpose decides what evidence may later retire the term; see
	// model.TrustTerm. Defaults to abuse (the zero) when a caller omits it.
	Purpose int16
	Note    *string
}

// Create normalizes and inserts a term. An empty norm or a bad kind is rejected
// fail-loud; a duplicate active (site-or-global, norm) → ErrTermExists (deprecate
// + re-create, never a duplicate — registry discipline). Invalidates the cache.
func (s *TermService) Create(ctx context.Context, p CreateTermParams) (*model.TrustTerm, error) {
	if p.Kind != model.TermKindSuspect && p.Kind != model.TermKindBanned {
		return nil, ErrTermInvalidKind
	}
	if p.Purpose != model.TermPurposeAbuse && p.Purpose != model.TermPurposeCompliance {
		return nil, ErrTermInvalidPurpose
	}
	site := p.Site
	if site != nil && strings.TrimSpace(*site) == "" {
		site = nil // an empty per-site value is a global term
	}
	normTerm := norm.Normalize(p.Term)
	if normTerm == "" {
		return nil, ErrTermEmpty
	}
	term := model.TrustTerm{
		Site: site, TermNorm: normTerm, Kind: p.Kind, Purpose: p.Purpose,
		Note: p.Note, IsDeprecated: false,
	}
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
