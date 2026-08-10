package releaselabels

import (
	"context"
	"fmt"
	"log/slog"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/model"

	"gorm.io/gorm/clause"
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
		ReleaseID int64 `gorm:"column:release_id"`
		LabelID   int64 `gorm:"column:label_id"`
		Developer bool  `gorm:"column:developer"`
		Publisher bool  `gorm:"column:publisher"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT rel.id AS release_id, lr.entity_id AS label_id,
		       bool_or(rp.developer) AS developer, bool_or(rp.publisher) AS publisher
		FROM catalog_external_ref r
		JOIN catalog_release rel ON rel.id = r.entity_id AND rel.deleted_at IS NULL
		JOIN src_vndb.releases_producers rp ON rp.id = r.external_id
		JOIN catalog_external_ref lr ON lr.entity_type = ? AND lr.source_id = ?
		     AND lr.link_kind = 0 AND lr.external_id = rp.pid
		WHERE r.entity_type = ? AND r.source_id = ? AND r.link_kind = 0
		GROUP BY 1, 2
		ORDER BY 1, 2`,
		model.EntityTypeLabel, vndbID, model.EntityTypeRelease, vndbID).Scan(&cands).Error; err != nil {
		return nil, fmt.Errorf("load candidates: %w", err)
	}

	if err := db.WithContext(ctx).Raw(`
		SELECT count(DISTINCT (rel.id, rp.pid))
		FROM catalog_external_ref r
		JOIN catalog_release rel ON rel.id = r.entity_id AND rel.deleted_at IS NULL
		JOIN src_vndb.releases_producers rp ON rp.id = r.external_id
		WHERE r.entity_type = ? AND r.source_id = ? AND r.link_kind = 0
		  AND NOT EXISTS (SELECT 1 FROM catalog_external_ref lr WHERE lr.entity_type = ?
		                  AND lr.source_id = ? AND lr.link_kind = 0 AND lr.external_id = rp.pid)`,
		model.EntityTypeRelease, vndbID, model.EntityTypeLabel, vndbID).Scan(&st.Unresolved).Error; err != nil {
		return nil, fmt.Errorf("count unresolved: %w", err)
	}

	src := vndbID
	var rows []model.CatalogReleaseLabel
	for _, c := range cands {
		if c.Developer {
			st.DevPlanned++
			rows = append(rows, model.CatalogReleaseLabel{
				ReleaseID: c.ReleaseID, LabelID: c.LabelID, Kind: model.WorkLabelKindDeveloper, SourceID: &src,
			})
		}
		if c.Publisher {
			st.PubPlanned++
			rows = append(rows, model.CatalogReleaseLabel{
				ReleaseID: c.ReleaseID, LabelID: c.LabelID, Kind: model.WorkLabelKindPublisher, SourceID: &src,
			})
		}
	}

	if opts.Apply {
		for start := 0; start < len(rows); start += 2000 {
			end := min(start+2000, len(rows))
			res := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
				CreateInBatches(rows[start:end], 2000)
			if res.Error != nil {
				st.Errors++
				slog.Warn("edge batch insert", "start", start, "err", res.Error)
				continue
			}
			st.Written += int(res.RowsAffected)
		}
		st.SkippedDup = st.DevPlanned + st.PubPlanned - st.Written - st.Errors
	}
	return st, nil
}
