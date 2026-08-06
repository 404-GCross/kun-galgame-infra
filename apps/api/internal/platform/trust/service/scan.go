package service

import (
	"context"

	"api/internal/platform/trust/model"

	"gorm.io/gorm"
)

// ScanService owns the async content-scan intake (doc 18 §5.1 /trust/scan): a
// tenant fail-loud registry check (mirroring report intake), text capping, and a
// pending-row insert. It is an ACCEPT-type surface — it never scores; the
// ScanWorker drains pending rows later (shadow mode).
//
// Like the forward face (step 04) it accepts a body-supplied `site` for callers
// that relay for many community-backed sites through one S2S identity; the
// KUN_TRUST_FORWARDER_CLIENT_IDS allowlist is the counterweight. An empty
// allowlist closes the relay path (a wire site → 403) but never the default
// bind-derived path.
type ScanService struct {
	db        *gorm.DB
	allowlist map[string]bool
}

// NewScanService builds the intake service over the trust DB with the forwarder
// client-id allowlist (shared with the forward face). A nil/empty allowlist means
// only the bind-derived site path works — any wire-supplied site 403s.
func NewScanService(db *gorm.DB, allowlist map[string]bool) *ScanService {
	return &ScanService{db: db, allowlist: allowlist}
}

func (s *ScanService) allowed(clientID string) bool {
	return clientID != "" && s.allowlist[clientID]
}

// ScanParams is a normalized scan-intake request. Site is derived from the
// authenticated client's binding and used when WireSite is empty. WireSite is the
// optional body-supplied site (allowlist-gated via CallerClientID); when set it
// wins over Site.
type ScanParams struct {
	CallerClientID string
	Site           string
	WireSite       string
	SubjectKind    string
	SubjectID      string
	Text           string
	AuthorID       *int64
	// SubjectReach is the audience the content had reached at scan time (nil =
	// the product does not report it). Carried to the review item this scan may
	// open, where it ranks the queue.
	SubjectReach *int64
}

// ScanResult is the intake outcome: the row id and whether the text was capped.
type ScanResult struct {
	ScanID    int64
	Truncated bool
}

// Ingest validates and persists a scan event as status=pending / mode=shadow.
// The subject_kind must be registered (non-deprecated) for the caller's site —
// the same tenant fail-loud as report intake (invariant 11). Text over the cap
// is truncated (rune-safe) and the truncation is reported. De-dup is deliberately
// NOT done: scan events are naturally repeatable (each edit → a fresh scan), and
// the worker scores idempotently.
func (s *ScanService) Ingest(ctx context.Context, p ScanParams) (ScanResult, error) {
	// Site three-state (step 04, mirrors the forward face): an empty WireSite
	// derives from the client binding (the default); a non-empty WireSite is the
	// allowlist-gated relay path and wins over the bound site.
	site := p.Site
	if p.WireSite != "" {
		if !s.allowed(p.CallerClientID) {
			return ScanResult{}, ErrForwarderNotAllowed
		}
		site = p.WireSite
	}

	var kindCount int64
	if err := s.db.WithContext(ctx).Model(&model.TrustSubjectKind{}).
		Where("site = ? AND key = ? AND is_deprecated = false", site, p.SubjectKind).
		Count(&kindCount).Error; err != nil {
		return ScanResult{}, err
	}
	if kindCount == 0 {
		return ScanResult{}, ErrSubjectKindNotRegistered
	}

	text, truncated := capRunes(p.Text, maxScanTextRunes)

	row := model.TrustScanResult{
		Site: site, SubjectKind: p.SubjectKind, SubjectID: p.SubjectID,
		AuthorID: p.AuthorID, ContentText: text, SubjectReach: p.SubjectReach,
		// Mode starts at shadow because intake does not know the worker's posture;
		// the worker stamps its OWN mode on the terminal update, so a scored row
		// always records the posture that actually governed it.
		Status: model.ScanStatusPending, Mode: model.ScanModeShadow,
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return ScanResult{}, err
	}
	return ScanResult{ScanID: row.ID, Truncated: truncated}, nil
}

// capRunes truncates s to at most n runes, reporting whether it cut anything.
// Rune-based so multibyte CJK text is never split mid-glyph.
func capRunes(s string, n int) (string, bool) {
	r := []rune(s)
	if len(r) <= n {
		return s, false
	}
	return string(r[:n]), true
}
