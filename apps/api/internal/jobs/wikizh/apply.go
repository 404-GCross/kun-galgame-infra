package wikizh

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
)

const curatedSourceKey = "curated"

const MinConfidence = 0.90

type ApplyStats struct {
	Verdicts   int
	Restores   int
	BelowGate  int
	Invalid    int
	Written    int
	Promoted   int
	Conflict   int
	Skipped    int
	Errors     int
	ReceiptIDs []int64
}

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

const supersededTable = "src_wiki.mt_superseded"

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

func restore(ctx context.Context, db *gorm.DB, workID int64, curated int16, text string) (id int64, promoted bool, err error) {
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

func (s ApplyStats) String() string {
	return fmt.Sprintf("verdicts=%d restores=%d below_gate=%d invalid=%d written=%d promoted=%d conflict=%d skipped=%d errors=%d",
		s.Verdicts, s.Restores, s.BelowGate, s.Invalid, s.Written, s.Promoted, s.Conflict, s.Skipped, s.Errors)
}
