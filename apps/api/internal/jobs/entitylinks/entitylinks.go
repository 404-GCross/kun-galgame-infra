// Package entitylinks mints the catalog's NON-IDENTITY web presence (wave 186):
// link_kind=related catalog_external_ref rows on works, labels and persons,
// read from VNDB's own curated external-link pool plus one Bangumi sub-lane.
//
// WHY THIS IS NOT AN IDENTITY IMPORT. src_vndb.extlinks is a shared pool of
// (site, site-native value) pairs that VNDB hangs off releases / vns /
// producers / staff. A row here says "this entity has a presence there", never
// "this entity IS that id" — so everything lands at link_kind=related, the tier
// that takes no part in identity resolution. The anchors themselves are
// untouched: the job only READS the exact vndb refs it rides on (the
// getchurefs chain shape — release "r123" / vn "v123" / producer "p123" /
// staff "s123").
//
// TWO STORAGE TIERS (sites.go holds the table):
//
//   - TYPED — website / twitter / cien / pixiv land on their existing
//     first-class catalog_source rows with the SAME normalized external_id the
//     E2 org wave and the editspec link field write (bare host+path, bare
//     lowercase handle, numeric id), so a link discovered twice from two waves
//     dedups on the primary key instead of doubling.
//   - WEB-RENDERED — everything else with a known URL shape lands on the
//     generic `web` source with the fully rendered absolute URL as its
//     external_id. No new catalog_source is invented for a site whose only
//     role is "a page a human may want to open".
//
// DELIBERATE SILENCES. Store/marketplace sites (dlsite / dmm / steam / getchu
// / booth / itch / …) are NOT ingested at work grain: release-grain identity
// anchors already carry them, and a store URL on the work would be a weaker
// duplicate of a stronger row. Identity spaces (vndb / bgmtv / egs_creator)
// are never mirrored as related links — an identity claim belongs to an
// importer lane, not here.
//
// Discipline: in-run dedup + ON CONFLICT DO NOTHING (a second --apply writes
// zero); the negative-knowledge set (catalog_match_rejection) is preloaded and
// blocks re-assertion; an entity already holding an exact/probable ref from the
// same source is skipped, so a related row can never shadow identity material;
// dry-run default; every DSN explicit.
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

// matched_by rule ids (rule:<src>-<what> convention).
const (
	ruleVNDBExtlink = "rule:vndb-extlink"
	ruleBGMWorkSite = "rule:bgm-work-website"
)

// Lane names accepted by --only.
const (
	LaneWork   = "work"
	LaneLabel  = "label"
	LanePerson = "person"
)

// Opts configures one run.
type Opts struct {
	Apply bool
	DSN   string // catalog DSN — REQUIRED (also hosts src_vndb / src_bangumi)
	Only  string // work | label | person; empty = all three
	Limit int    // cap PLANNED inserts per lane (0 = no cap); rehearsal aid
}

// LaneStats reports one lane. The skipped_* counters are identical in dry and
// apply mode — only Written depends on --apply.
type LaneStats struct {
	Planned          int
	Written          int
	SkippedDedup     int // the same (entity, source, external_id) planned twice in-run
	SkippedRejection int // blocked by catalog_match_rejection
	SkippedIdentity  int // entity already holds an exact/probable ref from that source
	SkippedStore     int // work-grain official site whose host is a storefront
	SkippedMalformed int // value failed its tier's sanity check — never guessed
}

// Stats reports a run, one entry per lane actually selected.
type Stats struct {
	Work   LaneStats
	Label  LaneStats
	Person LaneStats
}

// Run opens the catalog pool and executes the selected lanes.
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

// run is the pool-agnostic core (tests inject their own handle).
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

// selectedLanes resolves --only into the ordered lane list.
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

// registry holds the source ids this job reads and writes. Ids are resolved by
// KEY at startup — a vocabulary id is never hardcoded into a write.
type registry struct {
	vndb    int16
	bangumi int16
	web     int16
	typed   map[string]int16 // catalog_source key → id, for the typed tier
}

// targets lists every source id this job may write, for the rejection and
// identity preloads.
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
	sort.Strings(keys) // a missing seed always names the same key first
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

// runner accumulates one lane's plan.
type runner struct {
	db         *gorm.DB
	reg        registry
	entityType int16
	stats      *LaneStats
	limit      int
	seen       map[string]bool
	rejected   map[string]bool
	identity   map[string]bool // "entityID|sourceID" already exact/probable
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

// add queues one related ref, applying the four gates in order: the per-run cap,
// in-run dedup (a repeat is counted once, as a dedup), the same-source identity
// guard, and the negative-knowledge set.
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

// loadRejections preloads the negative-knowledge set for this entity type and
// the job's target sources (step-21 discipline: a reconciler must CONSUME
// match_rejection, not only write it).
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

// loadIdentityHolders preloads (entity, source) pairs that already carry an
// exact or probable ref from one of the target sources. A related row for such
// a pair is never minted: the identity material is the stronger assertion, and
// a second row from the same source under a different external_id would read as
// a competing claim.
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

// batchInsert writes the plan with ON CONFLICT DO NOTHING (no target — the
// four-column primary key catches it), returning the rows actually inserted.
// This is the backstop that makes a second --apply run write zero.
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
