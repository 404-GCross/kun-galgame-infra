package entitylinks

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ruleVNDBExtlink = "rule:vndb-extlink"
	ruleBGMWorkSite = "rule:bgm-work-website"
)

const (
	LaneWork   = "work"
	LaneLabel  = "label"
	LanePerson = "person"
)

type Opts struct {
	Apply bool
	DSN   string
	Only  string
	Limit int
}

type LaneStats struct {
	Planned          int
	Written          int
	SkippedDedup     int
	SkippedRejection int
	SkippedIdentity  int
	SkippedStore     int
	SkippedMalformed int
}

type Stats struct {
	Work   LaneStats
	Label  LaneStats
	Person LaneStats
}

func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess the target database")
	}
	db, err := openGorm(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	defer closeGorm(db)
	return run(ctx, db, opts)
}

func run(ctx context.Context, db *gorm.DB, opts Opts) (*Stats, error) {
	lanes, err := selectedLanes(opts.Only)
	if err != nil {
		return nil, err
	}
	reg, err := loadRegistry(ctx, db)
	if err != nil {
		return nil, err
	}
	st := &Stats{}
	for _, lane := range lanes {
		var laneStats *LaneStats
		var entityType int16
		var plan func(*runner, context.Context) error
		switch lane {
		case LaneWork:
			laneStats, entityType, plan = &st.Work, model.EntityTypeWork, (*runner).planWork
		case LaneLabel:
			laneStats, entityType, plan = &st.Label, model.EntityTypeLabel, (*runner).planLabel
		case LanePerson:
			laneStats, entityType, plan = &st.Person, model.EntityTypePerson, (*runner).planPerson
		}
		r, err := newRunner(ctx, db, reg, entityType, laneStats, opts.Limit)
		if err != nil {
			return nil, fmt.Errorf("prepare %s lane: %w", lane, err)
		}
		if err := plan(r, ctx); err != nil {
			return nil, fmt.Errorf("plan %s lane: %w", lane, err)
		}
		if opts.Apply {
			n, err := batchInsert(ctx, db, r.refs)
			if err != nil {
				return nil, fmt.Errorf("write %s lane: %w", lane, err)
			}
			laneStats.Written = n
		}
		slog.Info("entity-links lane done", "lane", lane, "apply", opts.Apply,
			"planned", laneStats.Planned, "written", laneStats.Written,
			"skipped_dedup", laneStats.SkippedDedup,
			"skipped_rejection", laneStats.SkippedRejection,
			"skipped_identity", laneStats.SkippedIdentity,
			"skipped_store", laneStats.SkippedStore,
			"skipped_malformed", laneStats.SkippedMalformed)
	}
	return st, nil
}

func selectedLanes(only string) ([]string, error) {
	switch only {
	case "":
		return []string{LaneWork, LaneLabel, LanePerson}, nil
	case LaneWork, LaneLabel, LanePerson:
		return []string{only}, nil
	default:
		return nil, fmt.Errorf("unknown lane %q (want work|label|person, or empty for all)", only)
	}
}

type registry struct {
	vndb    int16
	bangumi int16
	web     int16
	typed   map[string]int16
}

func (reg registry) targets() []int16 {
	out := make([]int16, 0, len(reg.typed)+1)
	out = append(out, reg.web)
	for _, id := range reg.typed {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func loadRegistry(ctx context.Context, db *gorm.DB) (registry, error) {
	reg := registry{typed: map[string]int16{}}
	keys := []string{"vndb", "bangumi", "web"}
	for _, ts := range typedSites {
		if _, dup := reg.typed[ts.sourceKey]; !dup {
			reg.typed[ts.sourceKey] = 0
			keys = append(keys, ts.sourceKey)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		var id int16
		if err := db.WithContext(ctx).
			Raw(`SELECT id FROM catalog_source WHERE key = ?`, key).Scan(&id).Error; err != nil {
			return reg, fmt.Errorf("resolve source %q: %w", key, err)
		}
		if id == 0 {
			return reg, fmt.Errorf("source %q not seeded — run migrate-catalog first", key)
		}
		switch key {
		case "vndb":
			reg.vndb = id
		case "bangumi":
			reg.bangumi = id
		case "web":
			reg.web = id
		}
		if _, typed := reg.typed[key]; typed {
			reg.typed[key] = id
		}
	}
	return reg, nil
}

type runner struct {
	db         *gorm.DB
	reg        registry
	entityType int16
	stats      *LaneStats
	limit      int
	seen       map[string]bool
	rejected   map[string]bool
	identity   map[string]bool
	refs       []model.CatalogExternalRef
}

func newRunner(ctx context.Context, db *gorm.DB, reg registry, entityType int16, stats *LaneStats, limit int) (*runner, error) {
	rejected, err := loadRejections(ctx, db, entityType, reg.targets())
	if err != nil {
		return nil, err
	}
	identity, err := loadIdentityHolders(ctx, db, entityType, reg.targets())
	if err != nil {
		return nil, err
	}
	return &runner{
		db: db, reg: reg, entityType: entityType, stats: stats, limit: limit,
		seen: map[string]bool{}, rejected: rejected, identity: identity,
	}, nil
}

func (r *runner) add(entityID int64, source int16, externalID, rule string) {
	if r.limit > 0 && r.stats.Planned >= r.limit {
		return
	}
	key := refKey(entityID, source, externalID)
	if r.seen[key] {
		r.stats.SkippedDedup++
		return
	}
	r.seen[key] = true
	if r.identity[identityKey(entityID, source)] {
		r.stats.SkippedIdentity++
		return
	}
	if r.rejected[key] {
		r.stats.SkippedRejection++
		return
	}
	r.stats.Planned++
	r.refs = append(r.refs, model.CatalogExternalRef{
		EntityType: r.entityType,
		EntityID:   entityID,
		SourceID:   source,
		ExternalID: externalID,
		LinkKind:   model.LinkKindRelated,
		MatchedBy:  rule,
	})
}

func refKey(entityID int64, source int16, externalID string) string {
	return strconv.FormatInt(entityID, 10) + "\x00" + strconv.Itoa(int(source)) + "\x00" + externalID
}

func identityKey(entityID int64, source int16) string {
	return strconv.FormatInt(entityID, 10) + "\x00" + strconv.Itoa(int(source))
}

func loadRejections(ctx context.Context, db *gorm.DB, entityType int16, sources []int16) (map[string]bool, error) {
	var rows []struct {
		EntityID   int64  `gorm:"column:entity_id"`
		SourceID   int16  `gorm:"column:source_id"`
		ExternalID string `gorm:"column:external_id"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT entity_id, source_id, external_id FROM catalog_match_rejection
		WHERE entity_type = ? AND source_id IN ?`, entityType, sources).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load rejections: %w", err)
	}
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		out[refKey(r.EntityID, r.SourceID, r.ExternalID)] = true
	}
	return out, nil
}

func loadIdentityHolders(ctx context.Context, db *gorm.DB, entityType int16, sources []int16) (map[string]bool, error) {
	var rows []struct {
		EntityID int64 `gorm:"column:entity_id"`
		SourceID int16 `gorm:"column:source_id"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT DISTINCT entity_id, source_id FROM catalog_external_ref
		WHERE entity_type = ? AND source_id IN ? AND link_kind IN (0, 1)`,
		entityType, sources).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load identity holders: %w", err)
	}
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		out[identityKey(r.EntityID, r.SourceID)] = true
	}
	return out, nil
}

func batchInsert(ctx context.Context, db *gorm.DB, refs []model.CatalogExternalRef) (int, error) {
	if len(refs) == 0 {
		return 0, nil
	}
	res := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(refs, 1000)
	return int(res.RowsAffected), res.Error
}

func openGorm(dsn string) (*gorm.DB, error) {
	return database.OpenJob(dsn)
}

func closeGorm(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.Close()
	}
}
