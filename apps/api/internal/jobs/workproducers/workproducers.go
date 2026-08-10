package workproducers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
)

type Opts struct {
	Apply bool
	DSN   string
}

type Stats struct {
	DevPlanned int
	PubPlanned int
	Written    int
	SkippedDup int
	Unresolved int
	Errors     int
}

func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn)")
	}
	db, err := database.OpenJob(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	defer func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	}()

	var vndbID int16
	if err := db.WithContext(ctx).Raw(
		`SELECT id FROM catalog_source WHERE key = 'vndb'`).Scan(&vndbID).Error; err != nil || vndbID == 0 {
		return nil, fmt.Errorf("resolve vndb source id: %v", err)
	}

	st := &Stats{}

	var cands []struct {
		WorkID    int64 `gorm:"column:work_id"`
		LabelID   int64 `gorm:"column:label_id"`
		Developer bool  `gorm:"column:developer"`
		Publisher bool  `gorm:"column:publisher"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT rel.work_id, lr.entity_id AS label_id,
		       bool_or(rp.developer) AS developer, bool_or(rp.publisher) AS publisher
		FROM catalog_external_ref r
		JOIN catalog_release rel ON rel.id = r.entity_id AND rel.deleted_at IS NULL
		JOIN catalog_work w ON w.id = rel.work_id AND w.deleted_at IS NULL
		JOIN src_vndb.releases_producers rp ON rp.id = r.external_id
		JOIN catalog_external_ref lr ON lr.entity_type = ? AND lr.source_id = ?
		     AND lr.link_kind = 0 AND lr.external_id = rp.pid
		WHERE r.entity_type = ? AND r.source_id = ? AND r.link_kind = 0
		  AND EXISTS (SELECT 1 FROM src_vndb.releases_titles rt
		              WHERE rt.id = r.external_id AND rt.lang = w.olang AND NOT rt.mtl)
		GROUP BY 1, 2
		ORDER BY 1, 2`,
		model.EntityTypeLabel, vndbID, model.EntityTypeRelease, vndbID).Scan(&cands).Error; err != nil {
		return nil, fmt.Errorf("load candidates: %w", err)
	}

	if err := db.WithContext(ctx).Raw(`
		SELECT count(DISTINCT (rel.work_id, rp.pid))
		FROM catalog_external_ref r
		JOIN catalog_release rel ON rel.id = r.entity_id AND rel.deleted_at IS NULL
		JOIN catalog_work w ON w.id = rel.work_id AND w.deleted_at IS NULL
		JOIN src_vndb.releases_producers rp ON rp.id = r.external_id
		WHERE r.entity_type = ? AND r.source_id = ? AND r.link_kind = 0
		  AND EXISTS (SELECT 1 FROM src_vndb.releases_titles rt
		              WHERE rt.id = r.external_id AND rt.lang = w.olang AND NOT rt.mtl)
		  AND NOT EXISTS (SELECT 1 FROM catalog_external_ref lr WHERE lr.entity_type = ?
		                  AND lr.source_id = ? AND lr.link_kind = 0 AND lr.external_id = rp.pid)`,
		model.EntityTypeRelease, vndbID, model.EntityTypeLabel, vndbID).Scan(&st.Unresolved).Error; err != nil {
		return nil, fmt.Errorf("count unresolved: %w", err)
	}

	src := vndbID
	var rows []model.CatalogWorkLabel
	for _, c := range cands {
		if c.Developer {
			st.DevPlanned++
			rows = append(rows, model.CatalogWorkLabel{
				WorkID: c.WorkID, LabelID: c.LabelID, Kind: model.WorkLabelKindDeveloper, SourceID: &src,
			})
		}
		if c.Publisher {
			st.PubPlanned++
			rows = append(rows, model.CatalogWorkLabel{
				WorkID: c.WorkID, LabelID: c.LabelID, Kind: model.WorkLabelKindPublisher, SourceID: &src,
			})
		}
	}

	if opts.Apply {
		var touched []int64
		for start := 0; start < len(rows); start += 2000 {
			end := min(start+2000, len(rows))
			written, err := insertEdges(ctx, db, rows[start:end])
			if err != nil {
				st.Errors++
				slog.Warn("edge batch insert", "start", start, "err", err)
				continue
			}
			st.Written += len(written)
			touched = append(touched, written...)
		}
		st.SkippedDup = st.DevPlanned + st.PubPlanned - st.Written
		if err := repository.TouchWorks(ctx, db, touched); err != nil {
			return nil, fmt.Errorf("touch works: %w", err)
		}
	}

	slog.Info("workproducers done", "apply", opts.Apply,
		"dev_planned", st.DevPlanned, "pub_planned", st.PubPlanned,
		"written", st.Written, "skipped_dup", st.SkippedDup,
		"unresolved_pairs", st.Unresolved, "errors", st.Errors)
	return st, nil
}

func insertEdges(ctx context.Context, db *gorm.DB, batch []model.CatalogWorkLabel) ([]int64, error) {
	var sb strings.Builder
	sb.WriteString(`INSERT INTO catalog_work_label (work_id, label_id, kind, source_id) VALUES `)
	args := make([]any, 0, len(batch)*4)
	for i, r := range batch {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString("(?,?,?,?)")
		args = append(args, r.WorkID, r.LabelID, r.Kind, r.SourceID)
	}
	sb.WriteString(` ON CONFLICT DO NOTHING RETURNING work_id`)
	var written []int64
	if err := db.WithContext(ctx).Raw(sb.String(), args...).Scan(&written).Error; err != nil {
		return nil, err
	}
	return written, nil
}
