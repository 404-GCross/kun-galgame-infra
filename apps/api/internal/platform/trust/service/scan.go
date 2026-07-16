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
type ScanService struct{ db *gorm.DB }

// NewScanService builds the intake service over the trust DB.
func NewScanService(db *gorm.DB) *ScanService { return &ScanService{db: db} }

// ScanParams is a normalized scan-intake request. Site is derived from the
// authenticated client's binding (never the wire body).
type ScanParams struct {
	Site        string
	SubjectKind string
	SubjectID   string
	Text        string
	AuthorID    *int64
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
	var kindCount int64
	if err := s.db.WithContext(ctx).Model(&model.TrustSubjectKind{}).
		Where("site = ? AND key = ? AND is_deprecated = false", p.Site, p.SubjectKind).
		Count(&kindCount).Error; err != nil {
		return ScanResult{}, err
	}
	if kindCount == 0 {
		return ScanResult{}, ErrSubjectKindNotRegistered
	}

	text, truncated := capRunes(p.Text, maxScanTextRunes)

	row := model.TrustScanResult{
		Site: p.Site, SubjectKind: p.SubjectKind, SubjectID: p.SubjectID,
		AuthorID: p.AuthorID, ContentText: text,
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
