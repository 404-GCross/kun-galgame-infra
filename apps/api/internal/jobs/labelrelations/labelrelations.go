// Package labelrelations builds the label↔label corporate-structure graph
// (wave 186) out of VNDB's producers_relations table.
//
// VNDB is the only upstream that publishes company structure at all — which
// brand is a subsidiary of which company, which imprint operates under which
// publisher, which studio a spin-off came out of, which name a defunct label
// was succeeded by. The catalog held none of it: a label page could list a
// label's works and nothing about the company behind it.
//
// Shape of the job, and why:
//
//   - It is a FULL REBUILD per source, in one transaction: DELETE every row of
//     this source, then insert the whole graph. The upstream is a dump the
//     ingest replaces wholesale, so an incremental writer would have to invent
//     a reaper for edges the dump dropped; a rebuild gets that for free and is
//     idempotent by construction (a second --apply over an unchanged dump
//     produces a byte-identical table).
//   - It writes MIRRORED rows because VNDB publishes them mirrored — the dump
//     already carries every edge twice, once per direction, under the inverse
//     code. The builder does not synthesize the mirror and does not dedupe it
//     away: it passes the upstream's own two rows through, so a reader selecting
//     `WHERE label_id = ?` sees the label's whole neighbourhood without ever
//     inverting a relation.
//   - BOTH endpoints must carry an EXACT vndb label anchor. A relation to a
//     producer the catalog never anchored is not a half-edge to be kept for
//     later — it has no second endpoint to point at — so it is skipped and
//     counted. Run reconcile-org-labels first; anchors grow, the graph follows.
//
// The direction reading of an upstream row is provisional and lives in exactly
// one place: relationmap.go. Read its banner before touching this package.
package labelrelations

import (
	"context"
	"fmt"
	"time"

	"api/internal/platform/catalog/model"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// vndbSourceKey is the upstream this builder mirrors. The numeric id is never
// hard-coded — it is resolved from the seeded registry at run time.
const vndbSourceKey = "vndb"

// matchedBy is the rule id every row of this lane carries (the external_ref
// 'rule:<rule-id>' provenance convention): one rule, one lane, one writer.
const matchedBy = "rule:vndb-producer-relation"

// insertBatch keeps the rebuild's INSERT statements to a sane parameter count.
const insertBatch = 1000

// Opts configures a run. Apply=false is the repo's dry-run default; DSN is
// REQUIRED and never guessed (it also hosts the src_vndb staging schema).
type Opts struct {
	Apply bool
	DSN   string
}

// Stats is the run's receipt.
type Stats struct {
	// EdgesTotal is every producers_relations row the dump holds.
	EdgesTotal int
	// BothAnchored is the rows whose two producers BOTH carry an exact vndb
	// label anchor — the graph this run is entitled to write.
	BothAnchored int
	// Written is the rows inserted — BothAnchored minus the self-edges and the
	// duplicate keys collapsed below. On a dry run it is what WOULD be written,
	// which is the number the run is there to report.
	Written int
	// SkippedUnanchored is the rows dropped for want of an anchor on one or
	// both endpoints — the number that shrinks as reconcile-org-labels runs.
	SkippedUnanchored int
	// SkippedUnknownRelation is the rows carrying a relation code this build
	// has no mapping for. It must be 0; a non-zero value means VNDB extended
	// its vocabulary and relationmap.go has to catch up.
	SkippedUnknownRelation int
	// SkippedSelf is the rows whose two producers anchor to the SAME catalog
	// label — a real outcome of a label merge, and never a fact worth storing
	// ("X is the parent of X").
	SkippedSelf int
	// Deleted is the pre-existing rows of this source the rebuild removed.
	Deleted int64
}

// Run builds the graph. On a dry run it reports what it would write and
// touches nothing.
func Run(ctx context.Context, opts Opts) (Stats, error) {
	var st Stats
	if opts.DSN == "" {
		return st, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess the target database")
	}
	db, err := gorm.Open(postgres.Open(opts.DSN), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return st, fmt.Errorf("open catalog pool: %w", err)
	}
	return build(ctx, db, opts.Apply)
}

// build is Run over an already-open pool — the seam the DB suite drives, so the
// tests exercise the same code path production does minus the dialing.
func build(ctx context.Context, db *gorm.DB, apply bool) (Stats, error) {
	var st Stats
	sourceID, err := resolveSourceID(ctx, db, vndbSourceKey)
	if err != nil {
		return st, err
	}
	anchors, err := loadLabelAnchors(ctx, db, sourceID)
	if err != nil {
		return st, err
	}
	edges, err := loadProducerRelations(ctx, db)
	if err != nil {
		return st, err
	}
	st.EdgesTotal = len(edges)

	rows := project(edges, anchors, sourceID, &st)
	if !apply {
		return st, nil
	}
	if err := rebuild(ctx, db, sourceID, rows, &st); err != nil {
		return st, err
	}
	return st, nil
}

// producerRelation is one raw upstream edge.
type producerRelation struct {
	ID       string `gorm:"column:id"`
	PID      string `gorm:"column:pid"`
	Relation string `gorm:"column:relation"`
}

// resolveSourceID looks up a source id by key, failing loudly if the seed is
// missing (never silently guess a vocabulary id).
func resolveSourceID(ctx context.Context, db *gorm.DB, key string) (int16, error) {
	var id int16
	if err := db.WithContext(ctx).Raw(
		`SELECT id FROM catalog_source WHERE key = ?`, key).Scan(&id).Error; err != nil {
		return 0, fmt.Errorf("resolve source %q: %w", key, err)
	}
	if id == 0 {
		return 0, fmt.Errorf("source %q not seeded — run migrate-catalog first", key)
	}
	return id, nil
}

// loadLabelAnchors returns vndb producer id ("p123") → catalog label id, over
// the EXACT tier only. probable/related refs are excluded on purpose: a
// structural edge inherits the identity confidence of its endpoints, and an
// unconfirmed anchor would publish a corporate fact about a label that may not
// be the one the upstream meant.
func loadLabelAnchors(ctx context.Context, db *gorm.DB, sourceID int16) (map[string]int64, error) {
	var rows []struct {
		ExternalID string `gorm:"column:external_id"`
		EntityID   int64  `gorm:"column:entity_id"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT r.external_id, r.entity_id
		FROM catalog_external_ref r
		JOIN catalog_label l ON l.id = r.entity_id AND l.deleted_at IS NULL
		WHERE r.entity_type = ? AND r.source_id = ? AND r.link_kind = ?`,
		model.EntityTypeLabel, sourceID, model.LinkKindExact).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load label anchors: %w", err)
	}
	m := make(map[string]int64, len(rows))
	for _, r := range rows {
		m[r.ExternalID] = r.EntityID
	}
	return m, nil
}

// loadProducerRelations reads the staged dump table whole. It is small (tens of
// thousands of rows at most) and the rebuild needs all of it at once anyway.
func loadProducerRelations(ctx context.Context, db *gorm.DB) ([]producerRelation, error) {
	var rows []producerRelation
	if err := db.WithContext(ctx).Raw(
		`SELECT id, pid, relation FROM src_vndb.producers_relations ORDER BY id, pid, relation`,
	).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load producers_relations: %w", err)
	}
	return rows, nil
}

// project turns raw upstream edges into the rows to insert, counting every
// reason a row did not make it. Duplicate primary keys are collapsed here
// rather than left to ON CONFLICT: the rebuild inserts into a table it just
// emptied, so a duplicate inside one batch would abort the whole transaction.
func project(edges []producerRelation, anchors map[string]int64, sourceID int16, st *Stats) []model.CatalogLabelRelation {
	now := nowUTC()
	out := make([]model.CatalogLabelRelation, 0, len(edges))
	seen := make(map[[3]int64]struct{}, len(edges))
	for _, e := range edges {
		relation, ok := vndbRelation[e.Relation]
		if !ok {
			st.SkippedUnknownRelation++
			continue
		}
		// The direction reading lives in relationmap.go: (id, pid, rel) means
		// "the producer at pid is <rel> of the producer at id".
		labelID, okA := anchors[e.ID]
		otherID, okB := anchors[e.PID]
		if !okA || !okB {
			st.SkippedUnanchored++
			continue
		}
		st.BothAnchored++
		if labelID == otherID {
			st.SkippedSelf++
			continue
		}
		key := [3]int64{labelID, otherID, int64(relation)}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, model.CatalogLabelRelation{
			LabelID: labelID, OtherLabelID: otherID, Relation: relation,
			SourceID: sourceID, MatchedBy: matchedBy, CreatedAt: now,
		})
	}
	st.Written = len(out)
	return out
}

// rebuild replaces this source's whole graph inside ONE transaction: a reader
// never observes the table half-empty, and a failed run leaves the previous
// graph exactly as it was.
func rebuild(ctx context.Context, db *gorm.DB, sourceID int16, rows []model.CatalogLabelRelation, st *Stats) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(`DELETE FROM catalog_label_relation WHERE source_id = ?`, sourceID)
		if res.Error != nil {
			return fmt.Errorf("delete source graph: %w", res.Error)
		}
		st.Deleted = res.RowsAffected
		if len(rows) == 0 {
			return nil
		}
		if err := tx.CreateInBatches(rows, insertBatch).Error; err != nil {
			return fmt.Errorf("insert graph: %w", err)
		}
		return nil
	})
}

// nowUTC is a package seam so provenance timestamps are stable in tests.
var nowUTC = func() time.Time { return time.Now().UTC() }
