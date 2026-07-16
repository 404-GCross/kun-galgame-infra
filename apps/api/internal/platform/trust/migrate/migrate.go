// Package migrate owns the kun_trust schema migration: the AutoMigrate table
// list, the idempotent raw-SQL section for the indexes AutoMigrate cannot
// express (partial-unique + DESC-ordered composites), and the global report-
// reason seed. It is called by cmd/migrate-trust and by the trust integration
// tests (which provision their database with the exact production schema).
package migrate

import (
	"fmt"

	"api/internal/platform/trust/model"

	"gorm.io/gorm"
)

// Run applies the full trust schema (tables + raw SQL) and seeds the six global
// report reasons. Idempotent: safe to run on every deploy and repeatedly
// against the same database.
func Run(db *gorm.DB) error {
	if err := db.AutoMigrate(
		// Reason before report (report.reason_id FK); review_item before both
		// report (review_item_id, no FK) and disposition (review_item_id FK).
		&model.TrustReportReason{},
		&model.TrustSubjectKind{},
		&model.TrustReviewItem{},
		&model.TrustReport{},
		&model.TrustDisposition{},
		&model.TrustAuditLog{},
		// AI shadow-scoring pipeline (step 03); no FK — subject is referenced by
		// (site, subject_kind, subject_id) like every other trust table.
		&model.TrustScanResult{},
	); err != nil {
		return fmt.Errorf("trust automigrate: %w", err)
	}
	if err := rawSQL(db); err != nil {
		return err
	}
	return seedReasons(db)
}

// rawSQL is the post-AutoMigrate section: the indexes AutoMigrate cannot
// express (partial predicate / DESC sort). Every statement is idempotent
// (CREATE INDEX IF NOT EXISTS), so this section reruns freely.
func rawSQL(db *gorm.DB) error {
	for _, ix := range []struct{ name, stmt string }{
		// Reason uniqueness split on NULL site (Postgres treats NULLs as
		// distinct, so a plain UNIQUE(key, site) would let duplicate global
		// rows through): one global row per key, and one per (key, site).
		{"uq_trust_report_reason_key_global", `
			CREATE UNIQUE INDEX IF NOT EXISTS uq_trust_report_reason_key_global
			    ON trust_report_reason(key) WHERE site IS NULL`},
		{"uq_trust_report_reason_key_site", `
			CREATE UNIQUE INDEX IF NOT EXISTS uq_trust_report_reason_key_site
			    ON trust_report_reason(key, site) WHERE site IS NOT NULL`},
		// Invariant 5: one report per (subject, reporter). Partial (P0
		// reporter_id is always present; the predicate is forward-compatible
		// with the P1 anonymous channel where reporter_id may be NULL).
		{"uq_trust_report_subject_reporter", `
			CREATE UNIQUE INDEX IF NOT EXISTS uq_trust_report_subject_reporter
			    ON trust_report(site, subject_kind, subject_id, reporter_id)
			    WHERE reporter_id IS NOT NULL`},
		// Rate-limit window count (章程 ruling 6): reports by a reporter in a
		// time window.
		{"idx_trust_report_reporter_created", `
			CREATE INDEX IF NOT EXISTS idx_trust_report_reporter_created
			    ON trust_report(reporter_id, created_at)`},
		// Invariant 4: at most ONE open item per subject. Partial — terminal
		// items (actioned/dismissed) drop out so a fresh item can open later.
		{"uq_trust_review_item_open", `
			CREATE UNIQUE INDEX IF NOT EXISTS uq_trust_review_item_open
			    ON trust_review_item(site, subject_kind, subject_id)
			    WHERE status IN (0, 1)`},
		// Invariant 11 (tenant-first): the per-site queue, highest priority
		// first. DESC sort, so it lives here rather than a struct tag.
		{"idx_trust_review_item_site_status_priority", `
			CREATE INDEX IF NOT EXISTS idx_trust_review_item_site_status_priority
			    ON trust_review_item(site, status, priority DESC)`},
		// Dispatch worker claim: pending dispositions whose next_attempt_at is
		// due (章程 ruling 9).
		{"idx_trust_disposition_callback", `
			CREATE INDEX IF NOT EXISTS idx_trust_disposition_callback
			    ON trust_disposition(callback_status, next_attempt_at)`},
		// Scan scoring worker claim (step 03): pending rows, oldest first. The
		// (status, id) shape backs the FOR UPDATE SKIP LOCKED batch pick.
		{"idx_trust_scan_result_status_id", `
			CREATE INDEX IF NOT EXISTS idx_trust_scan_result_status_id
			    ON trust_scan_result(status, id)`},
		// Scan observation / calibration queries (step 03): per-site, per-kind,
		// newest first.
		{"idx_trust_scan_result_site_kind_created", `
			CREATE INDEX IF NOT EXISTS idx_trust_scan_result_site_kind_created
			    ON trust_scan_result(site, subject_kind, created_at)`},
	} {
		if err := db.Exec(ix.stmt).Error; err != nil {
			return fmt.Errorf("create index %s: %w", ix.name, err)
		}
	}
	return nil
}

// globalReason is one seed row of the global reason taxonomy (章程 ruling 12).
type globalReason struct {
	Key      string
	NameCN   string
	Severity int16
}

// SeedReasons is the global base taxonomy (site NULL). Exported so tests can
// assert the exact seed set.
var SeedReasons = []globalReason{
	{"abuse", "辱骂/骚扰", 2},
	{"spam", "垃圾信息", 1},
	{"illegal", "违法内容", 3},
	{"rating_mislabel", "分级标注错误", 1},
	{"copyright", "版权侵权", 2},
	{"other", "其他", 1},
}

// seedReasons upserts the six global reasons, keyed on the partial unique
// (key) WHERE site IS NULL. Idempotent: a re-run keeps name/severity in sync
// without duplicating rows. Severity is written explicitly here (no GORM
// default tag on the column), sidestepping the zero-value default trap.
func seedReasons(db *gorm.DB) error {
	for _, r := range SeedReasons {
		if err := db.Exec(`
			INSERT INTO trust_report_reason (key, name_cn, site, severity, is_deprecated)
			VALUES (?, ?, NULL, ?, false)
			ON CONFLICT (key) WHERE site IS NULL
			DO UPDATE SET name_cn = EXCLUDED.name_cn, severity = EXCLUDED.severity
		`, r.Key, r.NameCN, r.Severity).Error; err != nil {
			return fmt.Errorf("seed reason %s: %w", r.Key, err)
		}
	}
	return nil
}
