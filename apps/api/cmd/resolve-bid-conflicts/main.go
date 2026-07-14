// resolve-bid-conflicts applies the adjudicated bid-vs-anchor conflict
// worklist produced by enrich-bangumi --conflicts-out (step 43): every row is
// a game whose curated galgame.bid disagrees with the catalog Bangumi exact
// anchor. Adjudication (step 44) found the wiki bid pointing at a NON-game
// subject (OST/book/anime) or a dead id in almost every case, so the fix is
// "adopt the anchor": swap galgame.bid to the anchor bid and record negative
// knowledge for the old id. Enrichment is deliberately NOT done here — after
// the swap the game stops being a conflict, so a normal enrich-bangumi run
// picks it up through the curated-bid lane.
//
// Guards per row (all violations are skipped and tallied, never forced):
//   - compare-and-swap: the game's CURRENT bid must still equal the TSV's
//     wiki_bid (drift = a human touched it since export → leave it alone);
//   - the anchor bid must be a type-4 (game) subject in src_bangumi;
//   - the anchor bid must not already be held by another game (galgame.bid
//     is globally unique — a duplicate holder is a curation question, not a
//     race to win).
//
// Idempotent: a game already carrying the anchor bid tallies as "already".
// Dry-run is the DEFAULT; pass --apply to write.
//
//	go run ./cmd/resolve-bid-conflicts --conflicts bangumi-bid-conflicts.tsv
//	go run ./cmd/resolve-bid-conflicts --conflicts ... --skip 840,4565 --apply
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"api/internal/infrastructure/database"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const (
	sourceBangumi int16 = 3
	entityWork    int16 = 5
)

type conflictRow struct {
	GalgameID int64
	WikiBID   int64
	AnchorBID int64
	Name      string
}

type stats struct {
	Applied, Already, Drift, DupHolder, AnchorNotGame, Suspicious, NoWork, Errors, Rejections int
}

func main() {
	conflicts := flag.String("conflicts", "", "conflict worklist TSV from enrich-bangumi --conflicts-out (required)")
	skip := flag.String("skip", "", "comma-separated galgame ids adjudicated suspicious — left untouched")
	apply := flag.Bool("apply", false, "write changes (default: dry run)")
	catalogDSN := flag.String("catalog-dsn", "", "catalog DSN override (src_bangumi + rejections); empty = KUN_CATALOG_* config. Locally point at kun_catalog_rehearsal.")
	flag.Parse()

	if *conflicts == "" {
		fmt.Fprintln(os.Stderr, "--conflicts <tsv> is required")
		os.Exit(2)
	}

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	rows, err := parseConflicts(*conflicts)
	if err != nil {
		slog.Error("parse conflicts", "error", err)
		os.Exit(1)
	}
	skipSet, err := parseSkip(*skip)
	if err != nil {
		slog.Error("parse skip list", "error", err)
		os.Exit(1)
	}

	wikiDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("wiki db connect", "error", err)
		os.Exit(1)
	}
	defer wikiDB.Close()

	catalogDB, closeCatalog, err := openCatalog(cfg, *catalogDSN)
	if err != nil {
		slog.Error("catalog db connect", "error", err)
		os.Exit(1)
	}
	defer closeCatalog()

	st := run(wikiDB.DB(), catalogDB, rows, skipSet, *apply)
	mode := "DRY"
	if *apply {
		mode = "APPLIED"
	}
	fmt.Printf("[%s] rows=%d applied=%d already=%d drift=%d dup_holder=%d anchor_not_game=%d suspicious_skipped=%d no_work_id=%d rejections=%d errors=%d\n",
		mode, len(rows), st.Applied, st.Already, st.Drift, st.DupHolder, st.AnchorNotGame, st.Suspicious, st.NoWork, st.Rejections, st.Errors)
	if st.Errors > 0 {
		os.Exit(1)
	}
}

func run(wiki, catalog *gorm.DB, rows []conflictRow, skipSet map[int64]bool, apply bool) stats {
	var st stats
	for _, r := range rows {
		if skipSet[r.GalgameID] {
			st.Suspicious++
			continue
		}
		if err := applyOne(wiki, catalog, r, apply, &st); err != nil {
			fmt.Printf("  galgame %d (%s): ERROR %v\n", r.GalgameID, r.Name, err)
			st.Errors++
		}
	}
	return st
}

func applyOne(wiki, catalog *gorm.DB, r conflictRow, apply bool, st *stats) error {
	// Current wiki state: bid + the claimed catalog work (rejection target).
	var cur struct {
		BID           *int64 `gorm:"column:bid"`
		CatalogWorkID *int64 `gorm:"column:catalog_work_id"`
	}
	if err := wiki.Raw(`SELECT bid, catalog_work_id FROM galgame WHERE id = ?`, r.GalgameID).Scan(&cur).Error; err != nil {
		return err
	}
	if cur.BID != nil && *cur.BID == r.AnchorBID {
		st.Already++
		return nil
	}
	if cur.BID == nil || *cur.BID != r.WikiBID {
		fmt.Printf("  galgame %d (%s): bid drifted since export (now %v, worklist says %d) — SKIPPED\n", r.GalgameID, r.Name, deref(cur.BID), r.WikiBID)
		st.Drift++
		return nil
	}

	// The anchor must be a game page; the anchor came from type-gated catalog
	// matching, so a violation here means the worklist itself is stale. A
	// missing subject (nil scan) is distinguished from type 0 — it means the
	// catalog DSN has no (or a stale) src_bangumi Silver schema.
	var anchorType *int
	if err := catalog.Raw(`SELECT type FROM src_bangumi.subject WHERE id = ?`, r.AnchorBID).Scan(&anchorType).Error; err != nil {
		return err
	}
	if anchorType == nil {
		return fmt.Errorf("anchor bid %d not in src_bangumi.subject — wrong --catalog-dsn?", r.AnchorBID)
	}
	if *anchorType != 4 {
		fmt.Printf("  galgame %d (%s): anchor bid %d is type %d, not a game — SKIPPED\n", r.GalgameID, r.Name, r.AnchorBID, *anchorType)
		st.AnchorNotGame++
		return nil
	}

	// galgame.bid is globally unique — never steal another game's anchor.
	var holder int64
	if err := wiki.Raw(`SELECT coalesce(min(id), 0) FROM galgame WHERE bid = ? AND id <> ?`, r.AnchorBID, r.GalgameID).Scan(&holder).Error; err != nil {
		return err
	}
	if holder != 0 {
		fmt.Printf("  galgame %d (%s): anchor bid %d already held by galgame %d — SKIPPED\n", r.GalgameID, r.Name, r.AnchorBID, holder)
		st.DupHolder++
		return nil
	}

	if !apply {
		st.Applied++
		if workID(cur.CatalogWorkID) == 0 {
			st.NoWork++
		} else {
			st.Rejections++
		}
		return nil
	}

	if err := wiki.Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(`UPDATE galgame SET bid = ? WHERE id = ? AND bid = ?`, r.AnchorBID, r.GalgameID, r.WikiBID)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("compare-and-swap lost (bid changed concurrently)")
		}
		// Keep Approach-B's invariant (live == latest snapshot).
		if err := tx.Exec(`UPDATE galgame_revision
			SET snapshot = jsonb_set(snapshot, '{bid}', to_jsonb(?::bigint), true)
			WHERE galgame_id = ? AND revision = (SELECT max(revision) FROM galgame_revision WHERE galgame_id = ?)`,
			r.AnchorBID, r.GalgameID, r.GalgameID).Error; err != nil {
			return err
		}
		// Any meta row belongs to the wrong bid — drop it; the next
		// enrich-bangumi run rebuilds it from the corrected one.
		return tx.Exec(`DELETE FROM galgame_bangumi_meta WHERE galgame_id = ?`, r.GalgameID).Error
	}); err != nil {
		return err
	}
	st.Applied++

	// Negative knowledge: this work is NOT the old subject. Mirrors the
	// bid-worklist tool's contract; tolerated as best-effort when the game
	// has no claimed work yet.
	wid := workID(cur.CatalogWorkID)
	if wid == 0 {
		st.NoWork++
		return nil
	}
	if err := catalog.Exec(`INSERT INTO catalog_match_rejection (entity_type, entity_id, source_id, external_id, reason, rejected_at)
		VALUES (?, ?, ?, ?, ?, now()) ON CONFLICT DO NOTHING`,
		entityWork, wid, sourceBangumi, strconv.FormatInt(r.WikiBID, 10),
		"step-44 conflict resolution: wiki bid pointed at a non-game subject").Error; err != nil {
		return err
	}
	st.Rejections++
	return nil
}

// workID unwraps the claimed catalog work id (0 = none).
func workID(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}

func parseConflicts(path string) ([]conflictRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var out []conflictRow
	first := true
	for sc.Scan() {
		cols := strings.Split(sc.Text(), "\t")
		if first { // header: galgame_id  wiki_bid  anchor_bid  name
			first = false
			continue
		}
		if len(cols) < 4 {
			continue
		}
		gid, err1 := strconv.ParseInt(cols[0], 10, 64)
		wb, err2 := strconv.ParseInt(cols[1], 10, 64)
		ab, err3 := strconv.ParseInt(cols[2], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			return nil, fmt.Errorf("bad conflict row: %q", sc.Text())
		}
		out = append(out, conflictRow{GalgameID: gid, WikiBID: wb, AnchorBID: ab, Name: cols[3]})
	}
	return out, sc.Err()
}

func parseSkip(s string) (map[int64]bool, error) {
	out := map[int64]bool{}
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("bad skip id %q", part)
		}
		out[id] = true
	}
	return out, nil
}

func deref(p *int64) any {
	if p == nil {
		return "NULL"
	}
	return *p
}

// openCatalog mirrors enrich-bangumi's DSN-override convention.
func openCatalog(cfg *config.Config, dsn string) (*gorm.DB, func(), error) {
	if dsn == "" {
		db, err := database.NewPostgresDB(cfg.CatalogDatabase)
		if err != nil {
			return nil, nil, err
		}
		return db.DB(), func() { db.Close() }, nil
	}
	g, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Warn)})
	if err != nil {
		return nil, nil, err
	}
	sqlDB, err := g.DB()
	if err != nil {
		return nil, nil, err
	}
	return g, func() { _ = sqlDB.Close() }, nil
}
