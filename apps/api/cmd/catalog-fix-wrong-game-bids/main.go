// catalog-fix-wrong-game-bids corrects verified "wrong-game bid" errors (QA data
// track, refs/qa/03-wrong-game-verified.md): a galgame whose Bangumi bid points at
// a DIFFERENT game. It repoints BOTH the catalog identity link and the wiki root:
//
//   - catalog kun_catalog: on catalog_external_ref (source=bangumi, entity_type=work,
//     exact), it records the wrong (work, bid) pairing as a catalog_match_rejection
//     (negative knowledge, so a re-import can never silently re-add it), then repoints
//     the ref from the wrong bid to the verified correct bid.
//   - wiki kun_galgame_wiki: UPDATE galgame.bid to the verified correct bid (the root
//     the catalog ref is derived from).
//
// Every write is GUARDED on the current value (idempotent + safe against concurrent
// edits) and logged old->new. DSNs are ALWAYS explicit (--catalog-dsn / --wiki-dsn,
// never config) so it can only touch the databases you name. Dry-run is the DEFAULT.
//
// Input --file: TSV `gid <TAB> work_id <TAB> cur_bid <TAB> correct_bid [<TAB> ...]`.
// Only "clean" corrections belong here — where the correct bid is held by no other
// catalog work. Swap/rotation collisions are handled separately.
//
//	go run ./cmd/catalog-fix-wrong-game-bids --catalog-dsn '<dsn>' --wiki-dsn '<dsn>' --file corrections.tsv
//	go run ./cmd/catalog-fix-wrong-game-bids --catalog-dsn '<dsn>' --wiki-dsn '<dsn>' --file corrections.tsv --apply
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type fix struct{ gid, workID, curBid, correctBid int64 }

func main() {
	catDSN := flag.String("catalog-dsn", "", "REQUIRED explicit kun_catalog DSN")
	wikiDSN := flag.String("wiki-dsn", "", "REQUIRED explicit kun_galgame_wiki DSN")
	file := flag.String("file", "", "REQUIRED corrections TSV (gid, work_id, cur_bid, correct_bid, ...)")
	apply := flag.Bool("apply", false, "actually write (default: dry-run preview)")
	logPath := flag.String("log", "wrong-game-fix.log.tsv", "TSV change log path")
	flag.Parse()
	if *catDSN == "" || *wikiDSN == "" || *file == "" {
		slog.Error("--catalog-dsn, --wiki-dsn and --file are all required")
		os.Exit(2)
	}
	fixes := readFixes(*file)
	cat := open(*catDSN, "catalog")
	wiki := open(*wikiDSN, "wiki")

	logf, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		slog.Error("open log", "error", err)
		os.Exit(1)
	}
	defer logf.Close()
	fmt.Fprintf(logf, "# run %s apply=%v fixes=%d\n", time.Now().Format(time.RFC3339), *apply, len(fixes))
	fmt.Fprintln(logf, "gid\twork_id\tside\taction\tfrom_bid\tto_bid")

	var catDone, wikiDone, skipped int
	for _, f := range fixes {
		// ---- catalog side ----
		var curExt string
		err := cat.Raw(`SELECT external_id FROM catalog_external_ref
			WHERE source_id=3 AND entity_type=5 AND link_kind=0 AND entity_id=?`, f.workID).Scan(&curExt).Error
		if err != nil {
			slog.Error("catalog read", "gid", f.gid, "error", err)
			os.Exit(1)
		}
		switch {
		case curExt == strconv.FormatInt(f.correctBid, 10):
			// already repointed — idempotent no-op
		case curExt != strconv.FormatInt(f.curBid, 10):
			slog.Warn("catalog ref not at expected cur_bid; skipping", "gid", f.gid, "have", curExt, "want", f.curBid)
			skipped++
			fmt.Fprintf(logf, "%d\t%d\tcatalog\tSKIP(have=%s)\t%d\t%d\n", f.gid, f.workID, curExt, f.curBid, f.correctBid)
		default:
			// guard: correct bid must not already be an exact ref on a different entity
			var holder int64
			cat.Raw(`SELECT entity_id FROM catalog_external_ref
				WHERE source_id=3 AND entity_type=5 AND link_kind=0 AND external_id=? LIMIT 1`,
				strconv.FormatInt(f.correctBid, 10)).Scan(&holder)
			if holder != 0 && holder != f.workID {
				slog.Warn("correct bid already held by another work; skipping (collision)", "gid", f.gid, "held_by", holder)
				skipped++
				fmt.Fprintf(logf, "%d\t%d\tcatalog\tSKIP(collision:%d)\t%d\t%d\n", f.gid, f.workID, holder, f.curBid, f.correctBid)
			} else if *apply {
				if err := repointCatalog(cat, f); err != nil {
					slog.Error("catalog repoint", "gid", f.gid, "error", err)
					os.Exit(1)
				}
				catDone++
				fmt.Fprintf(logf, "%d\t%d\tcatalog\tREPOINT\t%d\t%d\n", f.gid, f.workID, f.curBid, f.correctBid)
			} else {
				fmt.Fprintf(logf, "%d\t%d\tcatalog\twould-repoint\t%d\t%d\n", f.gid, f.workID, f.curBid, f.correctBid)
			}
		}
		// ---- wiki side ---- galgame.bid carries a UNIQUE index, so guard against
		// a collision (the correct bid already held by ANOTHER galgame) — skip +
		// log rather than crashing; those are swap/rotation cases handled separately.
		var wikiHolder int64
		wiki.Raw(`SELECT id FROM galgame WHERE bid=? AND id<>? LIMIT 1`, f.correctBid, f.gid).Scan(&wikiHolder)
		switch {
		case wikiHolder != 0:
			skipped++
			fmt.Fprintf(logf, "%d\t%d\twiki\tSKIP(bid-held-by:%d)\t%d\t%d\n", f.gid, f.workID, wikiHolder, f.curBid, f.correctBid)
		case *apply:
			res := wiki.Exec(`UPDATE galgame SET bid=? WHERE id=? AND bid=?`, f.correctBid, f.gid, f.curBid)
			if res.Error != nil {
				slog.Error("wiki update", "gid", f.gid, "error", res.Error)
				os.Exit(1)
			}
			if res.RowsAffected > 0 {
				wikiDone++
				fmt.Fprintf(logf, "%d\t%d\twiki\tUPDATE_BID\t%d\t%d\n", f.gid, f.workID, f.curBid, f.correctBid)
			} else {
				fmt.Fprintf(logf, "%d\t%d\twiki\tno-op(bid!=%d)\t%d\t%d\n", f.gid, f.workID, f.curBid, f.curBid, f.correctBid)
			}
		default:
			var wb int64
			wiki.Raw(`SELECT bid FROM galgame WHERE id=?`, f.gid).Scan(&wb)
			act := "would-update"
			if wb != f.curBid {
				act = fmt.Sprintf("guard-mismatch(bid=%d)", wb)
			}
			fmt.Fprintf(logf, "%d\t%d\twiki\t%s\t%d\t%d\n", f.gid, f.workID, act, f.curBid, f.correctBid)
		}
	}
	mode := "DRY-RUN (no writes)"
	if *apply {
		mode = "APPLIED"
	}
	slog.Info("done", "mode", mode, "fixes", len(fixes), "catalog_repointed", catDone, "wiki_updated", wikiDone, "skipped", skipped, "log", *logPath)
}

// repointCatalog runs the 3-step guarded catalog correction in one transaction:
// record negative knowledge, delete the wrong ref, insert the correct ref.
func repointCatalog(cat *gorm.DB, f fix) error {
	return cat.Transaction(func(tx *gorm.DB) error {
		reason := fmt.Sprintf("qa wrong-game bid: work %d was linked to bangumi %d, corrected to %d (LLM+human verified)", f.workID, f.curBid, f.correctBid)
		if err := tx.Exec(`INSERT INTO catalog_match_rejection (entity_type,entity_id,source_id,external_id,reason,rejected_at)
			VALUES (5,?,3,?,?,now()) ON CONFLICT DO NOTHING`, f.workID, strconv.FormatInt(f.curBid, 10), reason).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM catalog_external_ref
			WHERE source_id=3 AND entity_type=5 AND link_kind=0 AND entity_id=? AND external_id=?`,
			f.workID, strconv.FormatInt(f.curBid, 10)).Error; err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO catalog_external_ref (entity_type,entity_id,source_id,external_id,link_kind,matched_by,created_at)
			VALUES (5,?,3,?,0,'rule:wiki-bid-typed',now())`, f.workID, strconv.FormatInt(f.correctBid, 10)).Error
	})
}

func readFixes(path string) []fix {
	fh, err := os.Open(path)
	if err != nil {
		slog.Error("open file", "error", err)
		os.Exit(1)
	}
	defer fh.Close()
	var out []fix
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "gid\t") || strings.HasPrefix(line, "#") {
			continue
		}
		p := strings.Split(line, "\t")
		if len(p) < 4 {
			continue
		}
		g, _ := strconv.ParseInt(p[0], 10, 64)
		w, _ := strconv.ParseInt(p[1], 10, 64)
		c, _ := strconv.ParseInt(p[2], 10, 64)
		cor, _ := strconv.ParseInt(p[3], 10, 64)
		if g == 0 || w == 0 || c == 0 || cor == 0 {
			continue
		}
		out = append(out, fix{g, w, c, cor})
	}
	return out
}

func open(dsn, name string) *gorm.DB {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		slog.Error("db connect", "which", name, "error", err)
		os.Exit(1)
	}
	return db
}
