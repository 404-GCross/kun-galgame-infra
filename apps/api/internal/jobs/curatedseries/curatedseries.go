package curatedseries

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	curatedSourceKey = "curated"
	dlsiteSourceKey  = "dlsite"
	externalIDPrefix = "wiki:"
	introLang        = "zh-Hans"
)

type Opts struct {
	Apply        bool
	DSN          string
	ArtifactsDir string
	Receipts     string
}

type Stats struct {
	SeriesInFile         int
	SeriesWithMembers    int
	SeriesSkippedCovered int
	SeriesSeeded         int
	SeriesExisting       int
	MembersInserted      int
	MembersExisting      int
	IntrosInserted       int
	IntrosExisting       int
	MembersSkippedNoWork int
	TouchedWorks         int
}

type receipt struct {
	WikiSeriesID    int64   `json:"wiki_series_id"`
	Name            string  `json:"name"`
	ExternalID      string  `json:"external_id"`
	Decision        string  `json:"decision"`
	CoveredBySeries int64   `json:"covered_by_series_id,omitempty"`
	SeriesID        int64   `json:"series_id,omitempty"`
	MembersInserted int     `json:"members_inserted"`
	MembersExisting int     `json:"members_existing"`
	WorkIDs         []int64 `json:"work_ids"`
	IntroInserted   bool    `json:"intro_inserted"`
}

type plan struct {
	row       seriesRow
	workIDs   []int64
	coveredBy int64
}

func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess")
	}
	if opts.ArtifactsDir == "" {
		return nil, fmt.Errorf("artifacts directory is required (--artifacts): the frozen refs/proj/180-artifacts TSVs")
	}
	db, err := openGorm(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}
	return RunWithDB(ctx, db, opts)
}

func RunWithDB(ctx context.Context, db *gorm.DB, opts Opts) (*Stats, error) {
	series, err := parseSeries(opts.ArtifactsDir)
	if err != nil {
		return nil, err
	}
	members, err := parseMembers(opts.ArtifactsDir)
	if err != nil {
		return nil, err
	}
	curatedSrc, err := resolveSource(ctx, db, curatedSourceKey)
	if err != nil {
		return nil, err
	}
	dlsiteSrc, err := resolveSource(ctx, db, dlsiteSourceKey)
	if err != nil {
		return nil, err
	}

	st := &Stats{SeriesInFile: len(series)}
	plans, err := buildPlans(ctx, db, series, members, dlsiteSrc, st)
	if err != nil {
		return nil, err
	}

	var log *os.File
	if opts.Receipts != "" {
		if log, err = os.Create(opts.Receipts); err != nil {
			return nil, fmt.Errorf("receipts: %w", err)
		}
		defer log.Close()
	}
	enc := json.NewEncoder(log)

	var touched []int64
	for _, p := range plans {
		rec := receipt{
			WikiSeriesID: p.row.ID, Name: p.row.Name,
			ExternalID: externalIDPrefix + fmt.Sprint(p.row.ID),
			WorkIDs:    p.workIDs,
		}
		if p.coveredBy != 0 {
			st.SeriesSkippedCovered++
			rec.Decision, rec.CoveredBySeries = "skipped_covered", p.coveredBy
		} else if err := seedOne(ctx, db, curatedSrc, p, opts.Apply, st, &rec); err != nil {
			return nil, err
		}
		if rec.MembersInserted > 0 {
			touched = append(touched, p.workIDs...)
		}
		if log != nil {
			if err := enc.Encode(rec); err != nil {
				return nil, fmt.Errorf("receipts: %w", err)
			}
		}
	}

	if opts.Apply && len(touched) > 0 {
		if err := repository.TouchWorks(ctx, db, touched); err != nil {
			return nil, fmt.Errorf("touch works: %w", err)
		}
	}
	st.TouchedWorks = len(dedupe(touched))
	if !opts.Apply {
		st.TouchedWorks = 0
	}
	return st, nil
}

func buildPlans(ctx context.Context, db *gorm.DB, series []seriesRow, members []memberRow,
	dlsiteSrc int16, st *Stats) ([]plan, error) {
	byID := make(map[int64][]int64, len(series))
	for _, m := range members {
		byID[m.SeriesID] = append(byID[m.SeriesID], m.WorkID)
	}
	var allWorks []int64
	for id := range byID {
		byID[id] = dedupe(byID[id])
		allWorks = append(allWorks, byID[id]...)
	}
	live, err := liveWorks(ctx, db, dedupe(allWorks))
	if err != nil {
		return nil, err
	}
	dlsiteMembers, err := dlsiteCoverage(ctx, db, dedupe(allWorks), dlsiteSrc)
	if err != nil {
		return nil, err
	}

	plans := make([]plan, 0, len(series))
	for _, row := range series {
		works := byID[row.ID]
		if len(works) == 0 {
			continue
		}
		st.SeriesWithMembers++
		kept := make([]int64, 0, len(works))
		for _, w := range works {
			if _, ok := live[w]; ok {
				kept = append(kept, w)
			} else {
				st.MembersSkippedNoWork++
			}
		}
		if len(kept) == 0 {
			continue
		}
		plans = append(plans, plan{row: row, workIDs: kept, coveredBy: coveringSeries(dlsiteMembers, kept)})
	}
	return plans, nil
}

func seedOne(ctx context.Context, db *gorm.DB, curatedSrc int16, p plan, apply bool,
	st *Stats, rec *receipt) error {
	externalID := externalIDPrefix + fmt.Sprint(p.row.ID)

	seriesID, err := existingSeriesID(ctx, db, curatedSrc, externalID)
	if err != nil {
		return err
	}
	switch {
	case seriesID != 0:
		st.SeriesExisting++
		rec.Decision = "existing"
	case !apply:
		st.SeriesSeeded++
		rec.Decision = "seeded"
	default:
		row := model.CatalogSeries{DisplayName: p.row.Name, SourceID: curatedSrc, ExternalID: externalID}
		if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return fmt.Errorf("series %d: %w", p.row.ID, err)
		}
		if seriesID, err = existingSeriesID(ctx, db, curatedSrc, externalID); err != nil {
			return err
		}
		if seriesID == 0 {
			return fmt.Errorf("series %d: inserted but not resolvable by (%d, %s)", p.row.ID, curatedSrc, externalID)
		}
		if row.ID == 0 {
			st.SeriesExisting++
			rec.Decision = "existing"
		} else {
			st.SeriesSeeded++
			rec.Decision = "seeded"
		}
	}
	rec.SeriesID = seriesID

	if err := seedMembers(ctx, db, seriesID, p.workIDs, apply, st, rec); err != nil {
		return err
	}
	return seedIntro(ctx, db, seriesID, curatedSrc, p.row.Description, apply, st, rec)
}

func seedMembers(ctx context.Context, db *gorm.DB, seriesID int64, workIDs []int64, apply bool,
	st *Stats, rec *receipt) error {
	have := map[int64]struct{}{}
	if seriesID != 0 {
		var ids []int64
		if err := db.WithContext(ctx).Model(&model.CatalogSeriesMember{}).
			Where("series_id = ?", seriesID).Pluck("work_id", &ids).Error; err != nil {
			return err
		}
		for _, id := range ids {
			have[id] = struct{}{}
		}
	}
	rows := make([]model.CatalogSeriesMember, 0, len(workIDs))
	for _, w := range workIDs {
		if _, dup := have[w]; dup {
			st.MembersExisting++
			rec.MembersExisting++
			continue
		}
		rows = append(rows, model.CatalogSeriesMember{SeriesID: seriesID, WorkID: w})
	}
	if len(rows) == 0 {
		return nil
	}
	if !apply {
		st.MembersInserted += len(rows)
		rec.MembersInserted = len(rows)
		return nil
	}
	res := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&rows)
	if res.Error != nil {
		return fmt.Errorf("series %d members: %w", seriesID, res.Error)
	}
	inserted := int(res.RowsAffected)
	st.MembersInserted += inserted
	st.MembersExisting += len(rows) - inserted
	rec.MembersInserted = inserted
	rec.MembersExisting += len(rows) - inserted
	return nil
}

func seedIntro(ctx context.Context, db *gorm.DB, seriesID int64, curatedSrc int16, description string,
	apply bool, st *Stats, rec *receipt) error {
	intro := strings.TrimSpace(description)
	if intro == "" {
		return nil
	}
	if seriesID != 0 {
		var n int64
		if err := db.WithContext(ctx).Model(&model.CatalogSeriesIntro{}).
			Where("series_id = ? AND lang = ? AND source_id = ?", seriesID, introLang, curatedSrc).
			Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			st.IntrosExisting++
			return nil
		}
	}
	if !apply {
		st.IntrosInserted++
		rec.IntroInserted = true
		return nil
	}
	row := model.CatalogSeriesIntro{SeriesID: seriesID, Lang: introLang, Intro: intro, SourceID: curatedSrc}
	res := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if res.Error != nil {
		return fmt.Errorf("series %d intro: %w", seriesID, res.Error)
	}
	if res.RowsAffected == 0 {
		st.IntrosExisting++
		return nil
	}
	st.IntrosInserted++
	rec.IntroInserted = true
	return nil
}

func coveringSeries(dlsiteMembers map[int64]map[int64]struct{}, works []int64) int64 {
	for seriesID, have := range dlsiteMembers {
		covered := true
		for _, w := range works {
			if _, ok := have[w]; !ok {
				covered = false
				break
			}
		}
		if covered {
			return seriesID
		}
	}
	return 0
}

func dlsiteCoverage(ctx context.Context, db *gorm.DB, workIDs []int64, dlsiteSrc int16) (map[int64]map[int64]struct{}, error) {
	out := map[int64]map[int64]struct{}{}
	if len(workIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		SeriesID int64 `gorm:"column:series_id"`
		WorkID   int64 `gorm:"column:work_id"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT m.series_id, m.work_id
		FROM catalog_series_member m
		JOIN catalog_series s ON s.id = m.series_id
		WHERE s.source_id = ? AND m.work_id IN (?)`, dlsiteSrc, workIDs).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		if out[r.SeriesID] == nil {
			out[r.SeriesID] = map[int64]struct{}{}
		}
		out[r.SeriesID][r.WorkID] = struct{}{}
	}
	return out, nil
}

func liveWorks(ctx context.Context, db *gorm.DB, workIDs []int64) (map[int64]struct{}, error) {
	out := make(map[int64]struct{}, len(workIDs))
	if len(workIDs) == 0 {
		return out, nil
	}
	var ids []int64
	if err := db.WithContext(ctx).Model(&model.CatalogWork{}).
		Where("id IN (?)", workIDs).Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out, nil
}

func existingSeriesID(ctx context.Context, db *gorm.DB, sourceID int16, externalID string) (int64, error) {
	var id int64
	err := db.WithContext(ctx).Model(&model.CatalogSeries{}).
		Where("source_id = ? AND external_id = ?", sourceID, externalID).
		Limit(1).Pluck("id", &id).Error
	return id, err
}

func resolveSource(ctx context.Context, db *gorm.DB, key string) (int16, error) {
	var id int16
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = ?`, key).
		Scan(&id).Error; err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, fmt.Errorf("catalog_source has no %q row", key)
	}
	return id, nil
}

func dedupe(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func openGorm(dsn string) (*gorm.DB, error) {
	return database.OpenJob(dsn)
}
