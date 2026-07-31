// resite-claims moves the wiki's catalog claims onto the forum's tenant key
// and gives every one of them a birth event (N5, refs/proj/161 §1 P2).
//
// Two writes, one window:
//
//  1. catalog_work.site 'galgame_wiki' → 'kungal'. The wiki face is retiring;
//     the claims it holds are the forum's from the moment its edit face is the
//     one editing them. product_work_id is NOT touched — the gid stays the
//     product's local id, which is the whole point of that column (03 §9-3:
//     product-local id spaces are registry canon, not legacy).
//
//  2. one catalog_claim_event per re-sited claim: from_state=NULL (there was
//     no prior recorded state — the transition that created the claim
//     predates the event table), to_state = the claim's CURRENT state,
//     actor_uid = the submitter, looked up in the products' own records
//     (--forum-dsn then --moyu-dsn, in that order), reason='w1-resite
//     backfill'. This is the 157 §4-②
//     ruling landing: without it the per-user faces and the pending queue that
//     wave 157 shipped are empty for every pre-existing claim, because they
//     are aggregates over this table and the table only starts at wave 155.
//     The event id space is new, so no downstream idempotency key can collide
//     (03 §8-2).
//
// Both are idempotent: the update is keyed on the OLD site, and the backfill
// skips a work that already carries a backfill row. A second run writes zero.
//
// NOTE on the literal "galgame_wiki": it names TWO unrelated things — the claim
// SITE on catalog_work (what this tool flips) and the catalog_source KEY behind
// external_ref source 12 (which this tool never reads and P5 renames, id
// unchanged). They are deliberately not a shared constant anywhere in these
// tools; fromSite below is the site role only.
//
// It changes NO credentials and NO client bindings — it only reports on them
// (--infra-dsn), because deciding what happens to galgame-wiki-admin and the
// internal keys is the window operator's call, not a tool's.
//
//	go run ./cmd/resite-claims --dsn '...'                       # dry run
//	go run ./cmd/resite-claims --dsn '...' --apply               # re-site
//	go run ./cmd/resite-claims --dsn '...' --infra-dsn '...'     # + client report
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	catmodel "api/internal/platform/catalog/model"
	"api/pkg/logger"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const (
	// fromSite is the tenant key the wiki claimed under; toSite is the forum's,
	// already bound on its OAuth client (oauth_clients.catalog_site='kungal'),
	// which is why no client change is part of this migration.
	fromSite = "galgame_wiki"
	toSite   = "kungal"

	// backfillReason marks the rows this tool minted. It is also the
	// idempotency key: one backfill event per work, forever.
	backfillReason = "w1-resite backfill"

	// eventChunk bounds one INSERT.
	eventChunk = 2000
)

func main() {
	dsn := flag.String("dsn", "", "catalog DSN (REQUIRED — this tool never guesses a database)")
	forumDSN := flag.String("forum-dsn", "", "forum DSN, read-only; first attribution source (REQUIRED unless --no-attribution)")
	moyuDSN := flag.String("moyu-dsn", "", "moyu DSN, read-only; second attribution source (REQUIRED unless --no-attribution)")
	noAttribution := flag.Bool("no-attribution", false, "deliberately mint every event with actor_uid=0")
	infraDSN := flag.String("infra-dsn", "", "infra DSN; when set, prints the OAuth client / API key binding report")
	apply := flag.Bool("apply", false, "write changes (default: dry run, ledger only)")
	flag.Parse()

	logger.Init("development")
	if *dsn == "" {
		slog.Error("--dsn is required")
		os.Exit(2)
	}
	// Attribution is opt-OUT, not opt-in. A forgotten flag would mint 64k
	// unattributed events, and the backfill is idempotent — the second run
	// would not fix them, it would skip them. The footgun is worth a flag.
	if (*forumDSN == "" || *moyuDSN == "") && !*noAttribution {
		slog.Error("--forum-dsn and --moyu-dsn are both required (or pass --no-attribution to mint every event as system)")
		os.Exit(2)
	}
	db, err := open(*dsn)
	if err != nil {
		slog.Error("catalog db connect", "error", err)
		os.Exit(1)
	}
	if s, err := db.DB(); err == nil {
		defer s.Close()
	}

	ctx := context.Background()
	var sources []attributionSource
	for _, src := range []struct {
		name, dsn, query string
	}{
		{sourceForum, *forumDSN, forumSubmitterQuery},
		{sourceMoyu, *moyuDSN, moyuSubmitterQuery},
	} {
		if src.dsn == "" {
			continue
		}
		conn, err := open(src.dsn)
		if err != nil {
			slog.Error("attribution db connect", "source", src.name, "error", err)
			os.Exit(1)
		}
		if sqlDB, err := conn.DB(); err == nil {
			defer sqlDB.Close()
		}
		byGID, err := loadSubmitters(ctx, conn, src.query)
		if err != nil {
			slog.Error("load submitters", "source", src.name, "error", err)
			os.Exit(1)
		}
		slog.Info("submitter snapshot loaded", "source", src.name, "rows_with_a_submitter", len(byGID))
		sources = append(sources, attributionSource{Name: src.name, ByGID: byGID})
	}
	r := &resiter{db: db, apply: *apply, sources: sources}
	if err := r.run(ctx); err != nil {
		slog.Error("resite failed", "error", err)
		os.Exit(1)
	}
	if *infraDSN != "" {
		infra, err := open(*infraDSN)
		if err != nil {
			slog.Error("infra db connect", "error", err)
			os.Exit(1)
		}
		if s, err := infra.DB(); err == nil {
			defer s.Close()
		}
		if err := reportBindings(ctx, infra); err != nil {
			slog.Error("binding report failed", "error", err)
			os.Exit(1)
		}
	}
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}

func open(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}

// workRow is one claim considered for re-siting.
type workRow struct {
	ID int64 `gorm:"column:id"`
	// ProductWorkID is the wiki gid — the join key to the forum's stub table.
	ProductWorkID *int64 `gorm:"column:product_work_id"`
	ClaimState    *int16 `gorm:"column:claim_state"`
	HasEvent      bool   `gorm:"column:has_event"`
}

// plannedEvent is one backfill row, built before any write so the dry run
// prints exactly what an apply would insert.
type plannedEvent struct {
	WorkID   int64
	ToState  int16
	ActorUID int64
	// Source names the attribution lane that supplied ActorUID ("" = system).
	Source string
}

// planEvents decides which claims get a birth event.
//
// A NULL claim_state does NOT mean "unknown" here and is not skipped: on a
// claimed row it means `live`, and it means that in exactly one place —
// model.ClaimStateKey, the single definition the read face and the search index
// both project through ("claimed, column NULL → live ... zero-regression
// semantics"). Recording 0 for such a row states what every consumer already
// sees; skipping it would leave a claim with no birth event and a per-user face
// with a hole in it. The count is reported separately all the same, because a
// backfill that had to fill in a state deserves to be visible.
//
// The only skip is idempotency: a work that already carries a backfill row.
//
// actor_uid is looked up in the attribution sources IN ORDER — forum first,
// then moyu — and falls back to 0 = system. First match wins: a work both
// products know is the forum's submission, because that is where its stub was
// created.
//
// 0 is not a guess and not a placeholder: it is the model's documented value
// for "unattributed", and neither product recorded a submitter for most of this
// corpus. Inventing an actor for those rows would put a name on an act nobody
// performed.
func planEvents(rows []workRow, sources []attributionSource) (events []plannedEvent, nullState, already int, bySource map[string]int) {
	bySource = map[string]int{}
	for _, row := range rows {
		if row.HasEvent {
			already++
			continue
		}
		state := catmodel.ClaimStateLive
		if row.ClaimState != nil {
			state = *row.ClaimState
		} else {
			nullState++
		}
		event := plannedEvent{WorkID: row.ID, ToState: state}
		if row.ProductWorkID != nil {
			for _, src := range sources {
				if uid, ok := src.ByGID[*row.ProductWorkID]; ok {
					event.ActorUID, event.Source = uid, src.Name
					bySource[src.Name]++
					break
				}
			}
		}
		events = append(events, event)
	}
	return events, nullState, already, bySource
}

type resiter struct {
	db    *gorm.DB
	apply bool
	// sources are the attribution lanes IN PRIORITY ORDER: the first one that
	// knows a gid's submitter wins. Empty = every event is minted as system.
	sources []attributionSource
}

// attributionSource is one product's record of who submitted a work, keyed by
// the wiki gid.
type attributionSource struct {
	Name  string
	ByGID map[int64]int64
}

const (
	sourceForum = "forum"
	sourceMoyu  = "moyu"

	// forumSubmitterQuery reads the forum's creator snapshot. Its galgame stub
	// table's id IS the wiki gid, and creator_user_id is the very column the
	// forum's "my submissions" page renders off — attributing the backfill to
	// anything else would build a per-user face that disagrees with the one the
	// product already shows.
	forumSubmitterQuery = `SELECT id, creator_user_id AS uid FROM galgame
	                       WHERE creator_user_id IS NOT NULL AND creator_user_id > 0`

	// moyuSubmitterQuery is the second lane: a work submitted on moyu and never
	// touched on the forum has no forum stub, so without this its events would
	// be system-owned and moyu's own mine page would come back empty.
	//
	// patch.id IS the wiki gid — VERIFIED, not assumed, because the obvious
	// alternative is a trap. galgame_migrations(source_db='moyu') looks like the
	// id map and is not one: it records the PRE-migration moyu ids, and joining
	// through it agrees with the wiki's vndb_id on 4 rows out of 6,465. The
	// direct join agrees on 10,091 of 10,232, and 135 of the 141 remainder are
	// moyu placeholder ids of the literal form 'wiki-<gid>' against an empty
	// wiki vndb_id — i.e. the moyu row names the gid it is joined to. moyu
	// adopted the gid as its own primary key at migration time.
	//
	// user_id (not creator_id) per the P4 ruling. The two differ on 3,430 of
	// 10,233 rows, so this is a real choice and not a synonym.
	moyuSubmitterQuery = `SELECT id, user_id AS uid FROM patch WHERE user_id > 0`
)

// loadSubmitters reads one product's gid → submitter map. READ ONLY, and the
// single query this tool ever sends that database.
//
// Both products' user ids are the PLATFORM uid (their user tables have no
// sequence of their own and every id resolves in the infra users table —
// verified for all 38,199 moyu and 75,807 forum rows), so the two lanes write
// into the same actor space and a moyu-attributed event means the same thing to
// a consumer as a forum-attributed one.
func loadSubmitters(ctx context.Context, db *gorm.DB, query string) (map[int64]int64, error) {
	var rows []struct {
		ID  int64 `gorm:"column:id"`
		UID int64 `gorm:"column:uid"`
	}
	if err := db.WithContext(ctx).Raw(query).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]int64, len(rows))
	for _, row := range rows {
		out[row.ID] = row.UID
	}
	return out, nil
}

func (r *resiter) run(ctx context.Context) error {
	if err := r.guardCollisions(ctx); err != nil {
		return err
	}
	rows, err := r.claims(ctx)
	if err != nil {
		return err
	}
	events, nullState, already, bySource := planEvents(rows, r.sources)

	byState := map[int16]int{}
	actors := map[int64]struct{}{}
	attributed := 0
	for _, e := range events {
		byState[e.ToState]++
		if e.Source != "" {
			attributed++
			actors[e.ActorUID] = struct{}{}
		}
	}
	slog.Info("claims to re-site", "from", fromSite, "to", toSite, "works", len(rows))
	slog.Info("backfill events planned", "events", len(events),
		"null_claim_state_recorded_as_live", nullState, "skipped_already_backfilled", already)
	slog.Info("attribution", "attributed_to_a_submitter", attributed,
		"system_actor_0", len(events)-attributed, "distinct_submitters", len(actors))
	for _, src := range r.sources {
		slog.Info("  by source", "source", src.Name, "events", bySource[src.Name])
	}
	site, productWorkID := toSite, int64(1) // a claimed row, for the projection
	for state, n := range byState {
		slog.Info("  by state", "claim_state", state,
			"key", catmodel.ClaimStateKey(&site, &productWorkID, &state), "works", n)
	}
	if !r.apply {
		return nil
	}

	// One transaction for both writes: a half-applied re-site (works moved, no
	// events) would leave the per-user faces silently empty with nothing in the
	// data saying a backfill is still owed.
	//
	// updated_at IS bumped, deliberately. claimed_by.site is a PUBLIC field —
	// consumers route product links off it — so this is a real change to the
	// public face of every one of these works and the changes feed must carry
	// it. Two consequences the window has to expect and neither is a defect:
	// downstream cursors see one very large batch, and the works search index
	// (which bakes the claim projection) needs a reindex after this runs.
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(`UPDATE catalog_work SET site = ?, updated_at = now() WHERE site = ?`, toSite, fromSite)
		if res.Error != nil {
			return res.Error
		}
		slog.Info("works re-sited", "rows", res.RowsAffected)
		for start := 0; start < len(events); start += eventChunk {
			end := min(start+eventChunk, len(events))
			if err := insertEvents(ctx, tx, events[start:end]); err != nil {
				return err
			}
		}
		slog.Info("backfill events written", "rows", len(events))
		return nil
	})
}

// guardCollisions refuses to run into uq_catalog_work_claim
// (medium_id, site, product_work_id). A kungal claim minted by the forum wave
// on a product_work_id that a wiki gid also uses would make the UPDATE fail
// mid-transaction; catching it first turns an aborted window into a decision.
func (r *resiter) guardCollisions(ctx context.Context) error {
	var collisions int64
	if err := r.db.WithContext(ctx).Raw(
		`SELECT count(*) FROM catalog_work a
		 JOIN catalog_work b
		   ON b.medium_id = a.medium_id AND b.product_work_id = a.product_work_id AND b.site = ?
		 WHERE a.site = ?`, toSite, fromSite).Scan(&collisions).Error; err != nil {
		return err
	}
	if collisions > 0 {
		return fmt.Errorf("%d wiki claims share (medium_id, product_work_id) with an existing %s claim: "+
			"re-siting would violate uq_catalog_work_claim — resolve the overlap before the window", collisions, toSite)
	}
	return nil
}

func (r *resiter) claims(ctx context.Context) ([]workRow, error) {
	var rows []workRow
	err := r.db.WithContext(ctx).Raw(
		`SELECT w.id, w.product_work_id, w.claim_state,
		        EXISTS (SELECT 1 FROM catalog_claim_event e
		                WHERE e.work_id = w.id AND e.reason = ?) AS has_event
		 FROM catalog_work w WHERE w.site = ? ORDER BY w.id`, backfillReason, fromSite).
		Scan(&rows).Error
	return rows, err
}

// insertEvents writes one batch. The columns are spelled out rather than
// handed to GORM's struct writer so the identity primary key is never supplied
// (the model's own note) and so from_state stays a real SQL NULL.
func insertEvents(ctx context.Context, tx *gorm.DB, batch []plannedEvent) error {
	values := ""
	args := make([]any, 0, len(batch)*4)
	for i, e := range batch {
		if i > 0 {
			values += ","
		}
		values += "(?, NULL, ?, ?, ?, ?, now())"
		args = append(args, e.WorkID, e.ToState, e.ActorUID, backfillReason, toSite)
	}
	return tx.WithContext(ctx).Exec(
		`INSERT INTO catalog_claim_event
		   (work_id, from_state, to_state, actor_uid, reason, site, created_at)
		 VALUES `+values, args...).Error
}
