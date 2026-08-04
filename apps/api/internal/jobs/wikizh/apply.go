package wikizh

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
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
//
// 0.90, not 0.85, and the number is calibrated rather than chosen: the 30-case
// v1 calibration put an English-relay machine translation — Latin names left
// untranslated, one character spelled two ways, a Japanese line turned into
// nonsense — at EXACTLY 0.85. The prompt now names that failure mode (v2), and
// the gate sits above where it landed, so the two corrections are independent.
const MinConfidence = 0.90

// ApplyStats reports one apply pass.
type ApplyStats struct {
	Verdicts   int
	Restores   int // verdicts that say "write the wiki text"
	BelowGate  int // restoring verdicts held back by confidence
	Invalid    int // verdict outside the bucket's vocabulary → treated as unsure
	Written    int // new provenance=0 rows (the `usable` shape)
	Promoted   int // machine rows turned into the human text (the `compare` shape)
	Conflict   int // a zh row appeared between judging and applying
	Skipped    int // no longer eligible (a human row landed meanwhile)
	Errors     int
	ReceiptIDs []int64 // rows written, so a rollback is exact
}

// Apply writes the restorations a verdict file calls for.
//
// TWO SHAPES, because the two buckets meet the unique key differently.
// catalog_work_intro is unique on (work_id, lang, source_id), and a machine
// translation is NOT a separate row — intromt reuses the ja row's source_id and
// marks itself provenance=1. So:
//
//   - `usable` (no zh at all): a plain INSERT. Nothing sits at the key.
//   - `compare` (a machine row holds the slot): the key is TAKEN, by definition
//     of the bucket. An insert can only conflict — the first production pass
//     wrote 0 of 259 for exactly this reason. The restoration is a promotion of
//     that row in place: the wiki text replaces the machine text and provenance
//     flips 1 → 0.
//
// A promotion is durable rather than a thing the nightly lane undoes: intromt's
// own upsert is guarded `WHERE provenance = 1`, so once the row is provenance=0
// the machine lane refuses it forever.
//
// Nothing is destroyed. The superseded machine text is copied into
// src_wiki.mt_superseded first, so a rollback is a DELETE of the inserted
// ReceiptIDs plus a restore of the promoted rows from that table.
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
	if apply {
		if err := ensureSuperseded(ctx, db); err != nil {
			return nil, fmt.Errorf("prepare %s: %w", supersededTable, err)
		}
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

		id, promoted, err := restore(ctx, db, v.WorkID, curated, row.WikiZh)
		switch {
		case err != nil:
			st.Errors++
			slog.Warn("write restored intro", "work", v.WorkID, "err", err)
		case id == 0:
			// The guard fired: a provenance=0 row sits at the key. Never
			// overwrite one — a human text is not this pass's to replace.
			st.Conflict++
		default:
			if promoted {
				st.Promoted++
			} else {
				st.Written++
			}
			st.ReceiptIDs = append(st.ReceiptIDs, id)
			touched = append(touched, v.WorkID)
		}
	}

	if apply && len(touched) > 0 {
		if err := repository.TouchWorks(ctx, db, touched); err != nil {
			return st, fmt.Errorf("touch works: %w", err)
		}
	}
	return st, nil
}

// supersededTable keeps every machine text a promotion displaced, so the
// destructive half of this pass is undoable without a database backup.
const supersededTable = "src_wiki.mt_superseded"

// ensureSuperseded creates the landing table for displaced machine texts. It
// lives beside the wave-168 snapshot in src_wiki rather than in the catalog
// schema: it is rescue scaffolding for one wave, not part of the model.
func ensureSuperseded(ctx context.Context, db *gorm.DB) error {
	if err := db.WithContext(ctx).Exec(`CREATE SCHEMA IF NOT EXISTS src_wiki`).Error; err != nil {
		return err
	}
	return db.WithContext(ctx).Exec(`
		CREATE TABLE IF NOT EXISTS ` + supersededTable + ` (
			intro_id   bigint PRIMARY KEY,
			work_id    bigint NOT NULL,
			lang       text   NOT NULL,
			source_id  smallint NOT NULL,
			intro      text   NOT NULL,
			src_hash   text,
			mt_model   text,
			superseded_at timestamptz NOT NULL DEFAULT now()
		)`).Error
}

// restore puts the wiki text at (work_id, zh-Hans, curated), whether or not a
// machine row already holds that key, and reports which of the two happened.
//
// The ON CONFLICT guard is the mirror image of intromt's: that lane may only
// touch provenance=1, and so may this one. A provenance=0 row at the key means
// a human text landed between judging and applying, and the pass yields to it
// (RowsAffected 0 → the caller counts a conflict).
//
// Returns the row id (0 = guard fired) and whether it was a promotion.
func restore(ctx context.Context, db *gorm.DB, workID int64, curated int16, text string) (id int64, promoted bool, err error) {
	// Copy the machine text aside BEFORE it is overwritten. A no-op when the
	// key is free, which is the whole `usable` bucket.
	if err = db.WithContext(ctx).Exec(`
		INSERT INTO `+supersededTable+` (intro_id, work_id, lang, source_id, intro, src_hash, mt_model)
		SELECT id, work_id, lang, source_id, intro, src_hash, mt_model
		FROM catalog_work_intro
		WHERE work_id = ? AND lang = 'zh-Hans' AND source_id = ? AND provenance = 1
		ON CONFLICT (intro_id) DO NOTHING`, workID, curated).Error; err != nil {
		return 0, false, fmt.Errorf("snapshot superseded machine text: %w", err)
	}

	var row struct {
		ID       int64 `gorm:"column:id"`
		Inserted bool  `gorm:"column:inserted"`
	}
	// xmax = 0 distinguishes a fresh INSERT from a DO UPDATE on the same
	// statement — the standard Postgres idiom, and cheaper than a second query.
	// src_hash and mt_model are NOT NULL, so the "no machine translation behind
	// this text" value is the empty string, not NULL. Clearing them is part of
	// the promotion: leaving intromt's hash on a human text would make the
	// nightly lane's re-translate trigger read as if it had produced this row.
	err = db.WithContext(ctx).Raw(`
		INSERT INTO catalog_work_intro
			(work_id, lang, intro, source_id, provenance, src_hash, mt_model, created_at, updated_at)
		VALUES (?, 'zh-Hans', ?, ?, 0, '', '', now(), now())
		ON CONFLICT (work_id, lang, source_id) DO UPDATE
			SET intro = EXCLUDED.intro,
				provenance = 0,
				src_hash = '',
				mt_model = '',
				updated_at = now()
			WHERE catalog_work_intro.provenance = 1
		RETURNING id, (xmax = 0) AS inserted`, workID, text, curated).Scan(&row).Error
	if err != nil {
		return 0, false, err
	}
	return row.ID, row.ID != 0 && !row.Inserted, nil
}

// String renders the one-line summary every pass logs.
func (s ApplyStats) String() string {
	return fmt.Sprintf("verdicts=%d restores=%d below_gate=%d invalid=%d written=%d promoted=%d conflict=%d skipped=%d errors=%d",
		s.Verdicts, s.Restores, s.BelowGate, s.Invalid, s.Written, s.Promoted, s.Conflict, s.Skipped, s.Errors)
}
