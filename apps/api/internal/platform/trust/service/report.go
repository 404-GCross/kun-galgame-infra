package service

import (
	"context"
	"time"

	"api/internal/platform/trust/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ReportService owns the S2S report intake: tenant assertion, rate limiting,
// dedup, negative-knowledge folding, reputation weighting, and threshold
// aggregation into the review inbox.
type ReportService struct {
	db      *gorm.DB
	weigher Weigher
}

// NewReportService builds the intake service over the trust DB + a reporter
// weigher (main-DB backed in production, faked in tests).
func NewReportService(db *gorm.DB, weigher Weigher) *ReportService {
	return &ReportService{db: db, weigher: weigher}
}

// ReportParams is a normalized intake request. Site is derived from the
// authenticated client's binding (never the wire body).
type ReportParams struct {
	Site        string
	SubjectKind string
	SubjectID   string
	ReasonKey   string
	Note        *string
	Snapshot    *string
	ReporterID  int64
}

// ReportResult is the intake outcome: the report row and, when one exists, the
// review item it aggregated/folded into.
type ReportResult struct {
	ReportID     int64
	ReviewItemID *int64
}

// Submit runs the full intake flow (doc 18 §5.1). Errors map to structured HTTP
// codes at the handler; the happy path returns the report id and any linked
// review item.
func (s *ReportService) Submit(ctx context.Context, p ReportParams) (ReportResult, error) {
	// Tenant fail-loud: the subject_kind must be registered (non-deprecated)
	// for the caller's site (invariant 11).
	var kindCount int64
	if err := s.db.WithContext(ctx).Model(&model.TrustSubjectKind{}).
		Where("site = ? AND key = ? AND is_deprecated = false", p.Site, p.SubjectKind).
		Count(&kindCount).Error; err != nil {
		return ReportResult{}, err
	}
	if kindCount == 0 {
		return ReportResult{}, ErrSubjectKindNotRegistered
	}

	// Resolve the reason (a per-site extension overrides the global base).
	var reason struct {
		ID       int64
		Severity int16
	}
	if err := s.db.WithContext(ctx).Model(&model.TrustReportReason{}).
		Select("id, severity").
		Where("key = ? AND (site = ? OR site IS NULL) AND is_deprecated = false", p.ReasonKey, p.Site).
		Order("site NULLS LAST").Limit(1).Scan(&reason).Error; err != nil {
		return ReportResult{}, err
	}
	if reason.ID == 0 {
		return ReportResult{}, ErrReasonUnknown
	}

	// Rate limit: SQL window count over (reporter_id, created_at) — 章程 ruling 6.
	var recent int64
	if err := s.db.WithContext(ctx).Model(&model.TrustReport{}).
		Where("reporter_id = ? AND created_at > ?", p.ReporterID, time.Now().Add(-rateLimitWindow)).
		Count(&recent).Error; err != nil {
		return ReportResult{}, err
	}
	if recent >= rateLimitMax {
		return ReportResult{}, ErrRateLimited
	}

	weight, err := s.weigher.Weigh(ctx, p.ReporterID)
	if err != nil {
		return ReportResult{}, err
	}

	var result ReportResult
	txErr := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		report := model.TrustReport{
			Site: p.Site, SubjectKind: p.SubjectKind, SubjectID: p.SubjectID,
			ReporterID: p.ReporterID, ReasonID: reason.ID, Note: p.Note,
			SubjectSnapshot: p.Snapshot, Weight: weight.Weight,
			Status: model.ReportStatusReceived,
		}
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&report)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// Dedup (invariant 5): idempotent — return the existing report.
			var existing model.TrustReport
			if err := tx.Where(
				"site = ? AND subject_kind = ? AND subject_id = ? AND reporter_id = ?",
				p.Site, p.SubjectKind, p.SubjectID, p.ReporterID,
			).Take(&existing).Error; err != nil {
				return err
			}
			result = ReportResult{ReportID: existing.ID, ReviewItemID: existing.ReviewItemID}
			return nil
		}

		result.ReportID = report.ID
		linked, err := s.aggregate(tx, p, report.ID, weight, reason.Severity)
		if err != nil {
			return err
		}
		result.ReviewItemID = linked
		return nil
	})
	if txErr != nil {
		return ReportResult{}, txErr
	}
	return result, nil
}

// aggregate decides the freshly-inserted report's linkage and returns the review
// item it landed on (nil when it stays unlinked below threshold): link to an
// open item, fold onto a recently-dismissed one (invariant 10), or open a new
// item when the accumulated weight crosses the threshold or the reporter is
// staff (章程 ruling 7).
func (s *ReportService) aggregate(tx *gorm.DB, p ReportParams, reportID int64, weight ReporterWeight, severity int16) (*int64, error) {
	// 1. An open item already exists → link + accumulate weight (invariant 4).
	var open model.TrustReviewItem
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("site = ? AND subject_kind = ? AND subject_id = ? AND status IN ?",
			p.Site, p.SubjectKind, p.SubjectID, []int16{model.ReviewStatusPending, model.ReviewStatusClaimed}).
		Limit(1).Take(&open).Error
	if err == nil {
		if err := s.linkReport(tx, reportID, open.ID); err != nil {
			return nil, err
		}
		if err := tx.Model(&model.TrustReviewItem{}).Where("id = ?", open.ID).
			Update("report_weight_sum", gorm.Expr("COALESCE(report_weight_sum, 0) + ?", weight.Weight)).Error; err != nil {
			return nil, err
		}
		return &open.ID, nil
	} else if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	// 2. A recently-dismissed item within the fold window → fold (invariant 10).
	var dismissed model.TrustReviewItem
	err = tx.Where("site = ? AND subject_kind = ? AND subject_id = ? AND status = ? AND decided_at > ?",
		p.Site, p.SubjectKind, p.SubjectID, model.ReviewStatusDismissed, time.Now().Add(-foldWindow)).
		Order("decided_at DESC").Limit(1).Take(&dismissed).Error
	if err == nil {
		if err := tx.Model(&model.TrustReport{}).Where("id = ?", reportID).
			Updates(map[string]any{"review_item_id": dismissed.ID, "status": model.ReportStatusFolded}).Error; err != nil {
			return nil, err
		}
		return &dismissed.ID, nil
	} else if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	// 3. No open/dismissed item → aggregate the un-linked, non-folded reports.
	var sum float32
	if err := tx.Model(&model.TrustReport{}).
		Where("site = ? AND subject_kind = ? AND subject_id = ? AND status <> ? AND review_item_id IS NULL",
			p.Site, p.SubjectKind, p.SubjectID, model.ReportStatusFolded).
		Select("COALESCE(SUM(weight), 0)").Scan(&sum).Error; err != nil {
		return nil, err
	}
	if !weight.Staff && sum < aggregateThreshold {
		return nil, nil // below threshold → the report stays received, unlinked
	}

	item := model.TrustReviewItem{
		Site: p.Site, SubjectKind: p.SubjectKind, SubjectID: p.SubjectID,
		Source: model.ReviewSourceReports, Severity: &severity,
		ReportWeightSum: &sum, Priority: float32(severity),
		Status: model.ReviewStatusPending,
	}
	res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&item)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		// A concurrent report opened the item first (invariant 4 backstop) →
		// adopt it instead of forking a second.
		if err := tx.Where("site = ? AND subject_kind = ? AND subject_id = ? AND status IN ?",
			p.Site, p.SubjectKind, p.SubjectID, []int16{model.ReviewStatusPending, model.ReviewStatusClaimed}).
			Limit(1).Take(&item).Error; err != nil {
			return nil, err
		}
	}
	// Backfill-link every un-linked, non-folded report for this subject.
	if err := tx.Model(&model.TrustReport{}).
		Where("site = ? AND subject_kind = ? AND subject_id = ? AND status <> ? AND review_item_id IS NULL",
			p.Site, p.SubjectKind, p.SubjectID, model.ReportStatusFolded).
		Updates(map[string]any{"review_item_id": item.ID, "status": model.ReportStatusLinked}).Error; err != nil {
		return nil, err
	}
	return &item.ID, nil
}

// linkReport attaches a single report to a review item (status → linked).
func (s *ReportService) linkReport(tx *gorm.DB, reportID, itemID int64) error {
	return tx.Model(&model.TrustReport{}).Where("id = ?", reportID).
		Updates(map[string]any{"review_item_id": itemID, "status": model.ReportStatusLinked}).Error
}
