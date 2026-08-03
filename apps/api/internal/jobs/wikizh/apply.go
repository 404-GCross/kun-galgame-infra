package wikizh

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// curatedSourceKey is the first-party lane a restored text is attributed to.
// It is the same source wave 164's restores used: the text was written by a
// site user in the wiki's own editor, so it is first-party curation, not an
// upstream import.
const curatedSourceKey = "curated"

// MinConfidence is the auto-apply gate. Anything below it is left for human
// review rather than written — the 87/156/164 pattern, and the reason the gate
// exists is that an unusable text published as a work's only Chinese intro is
// visible to every reader of that page.
const MinConfidence = 0.85

// ApplyStats reports one apply pass.
type ApplyStats struct {
	Verdicts   int
	Restores   int // verdicts that say "write the wiki text"
	BelowGate  int // restoring verdicts held back by confidence
	Invalid    int // verdict outside the bucket's vocabulary → treated as unsure
	Written    int
	Conflict   int // a zh row appeared between judging and applying
	Skipped    int // no longer eligible (a human row landed meanwhile)
	Errors     int
	ReceiptIDs []int64 // rows written, so a rollback is exact
}

// Apply writes the restorations a verdict file calls for.
//
// Every write is an INSERT of a provenance=0 row; nothing is updated or
// deleted. The read face prefers provenance=0, so a restored text takes over
// display while the machine row remains as a fallback — and a rollback is a
// DELETE of ReceiptIDs, nothing more.
func Apply(ctx context.Context, db *gorm.DB, vs []Verdict, apply bool) (*ApplyStats, error) {
	st := &ApplyStats{Verdicts: len(vs)}
	var curated int16
	if err := db.WithContext(ctx).
		Raw(`SELECT id FROM catalog_source WHERE key = ?`, curatedSourceKey).Scan(&curated).Error; err != nil {
		return nil, fmt.Errorf("resolve curated source: %w", err)
	}
	if curated == 0 {
		return nil, fmt.Errorf("catalog_source has no %q row", curatedSourceKey)
	}

	var touched []int64
	for _, v := range vs {
		if !validVerdict(v.Bucket, v.Verdict) {
			// A verdict the model invented cannot cause a write.
			st.Invalid++
			continue
		}
		if !restores(v.Bucket, v.Verdict) {
			continue
		}
		st.Restores++
		if v.Confidence < MinConfidence {
			st.BelowGate++
			continue
		}
		if !apply {
			continue
		}

		// Re-read the text from the snapshot at write time rather than trusting
		// the verdict file to carry it: the verdict is a JUDGEMENT, and the text
		// of record lives in one place.
		var row struct {
			WikiZh string `gorm:"column:wiki_zh"`
		}
		if err := db.WithContext(ctx).Raw(
			`SELECT btrim(wiki_zh_cn) AS wiki_zh FROM `+snapshotTable+` WHERE work_id = ?`,
			v.WorkID).Scan(&row).Error; err != nil {
			st.Errors++
			slog.Warn("read snapshot", "work", v.WorkID, "err", err)
			continue
		}
		if strings.TrimSpace(row.WikiZh) == "" {
			st.Skipped++
			continue
		}

		// Still eligible? A human zh row may have landed since the judgement.
		var humanZh int64
		if err := db.WithContext(ctx).Raw(
			`SELECT count(*) FROM catalog_work_intro
			 WHERE work_id = ? AND lang LIKE 'zh%' AND provenance = 0`, v.WorkID).Scan(&humanZh).Error; err != nil {
			st.Errors++
			continue
		}
		if humanZh > 0 {
			st.Skipped++
			continue
		}

		res := db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "work_id"}, {Name: "lang"}, {Name: "source_id"}},
			DoNothing: true,
		}).Create(&model.CatalogWorkIntro{
			WorkID: v.WorkID, Lang: "zh-Hans", Intro: row.WikiZh,
			SourceID: curated, Provenance: 0,
		})
		switch {
		case res.Error != nil:
			st.Errors++
			slog.Warn("write restored intro", "work", v.WorkID, "err", res.Error)
		case res.RowsAffected == 0:
			st.Conflict++
		default:
			st.Written++
			touched = append(touched, v.WorkID)
		}
	}

	if apply && len(touched) > 0 {
		if err := repository.TouchWorks(ctx, db, touched); err != nil {
			return st, fmt.Errorf("touch works: %w", err)
		}
		// Receipts: the ids of exactly the rows this pass created.
		if err := db.WithContext(ctx).Raw(
			`SELECT id FROM catalog_work_intro
			 WHERE work_id IN ? AND lang = 'zh-Hans' AND provenance = 0 AND source_id = ?`,
			touched, curated).Scan(&st.ReceiptIDs).Error; err != nil {
			return st, fmt.Errorf("collect receipts: %w", err)
		}
	}
	return st, nil
}

// String renders the one-line summary every pass logs.
func (s ApplyStats) String() string {
	return fmt.Sprintf("verdicts=%d restores=%d below_gate=%d invalid=%d written=%d conflict=%d skipped=%d errors=%d",
		s.Verdicts, s.Restores, s.BelowGate, s.Invalid, s.Written, s.Conflict, s.Skipped, s.Errors)
}
