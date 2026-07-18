// catalog-fix-bid-enrichment repairs the enrichment a wrong-game bid contaminated,
// scoped to the games whose bid the QA track corrected (refs/qa/04). For each game
// it undoes what enrich-bangumi wrote from the WRONG subject and writes the correct
// subject's values instead — WITHOUT triggering a wholesale enrich pass:
//
//   - aliases: removes galgame_alias rows whose name equals the WRONG subject's
//     name / name_cn (and is not a display name nor the correct subject's name_cn),
//     and drops those names from the latest revision snapshot's `aliases` array.
//   - intro_zh_cn: if the current value equals the WRONG subject's summary (i.e. it
//     was enrich-filled from the wrong bid), replaces it with the CORRECT subject's
//     summary (or clears it) and patches the snapshot (Approach B: live == snapshot).
//   - galgame_bangumi_meta: upserts score / rank / total / nsfw from the CORRECT bid.
//
// User-/vndb-sourced values are never touched (every write is guarded on equality
// with the wrong subject's value). Dry-run by default; DSNs always explicit. On prod
// the galgame family lives inside kun_catalog, so both DSNs are the same database.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type fix struct{ gid, workID, curBid, correctBid int64 }

type subject struct {
	Name, NameCN, Summary string
	Score                 float64
	Rank                  int
	NSFW                  bool
	ScoreDetails          []byte
}

func main() {
	catDSN := flag.String("catalog-dsn", "", "REQUIRED explicit DSN with the src_bangumi schema (kun_catalog)")
	wikiDSN := flag.String("wiki-dsn", "", "REQUIRED explicit DSN with the galgame family")
	file := flag.String("file", "", "REQUIRED corrections TSV (gid, work_id, cur_bid, correct_bid, ...)")
	apply := flag.Bool("apply", false, "actually write (default: dry-run)")
	logPath := flag.String("log", "bid-enrichment-fix.log.tsv", "TSV change log path")
	flag.Parse()
	if *catDSN == "" || *wikiDSN == "" || *file == "" {
		slog.Error("--catalog-dsn, --wiki-dsn and --file are all required")
		os.Exit(2)
	}
	fixes := readFixes(*file)
	cat := open(*catDSN, "catalog")
	wiki := open(*wikiDSN, "wiki")
	logf, _ := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	defer logf.Close()
	fmt.Fprintln(logf, "gid\tkind\taction\tdetail")

	var aliasDel, introFix, metaUp int
	for _, f := range fixes {
		wrong := loadSubject(cat, f.curBid)
		correct := loadSubject(cat, f.correctBid)
		if wrong == nil || correct == nil {
			slog.Warn("subject missing; skipping", "gid", f.gid)
			continue
		}
		var g struct {
			NameJaJP, NameZhCN, NameZhTW, NameEnUS, IntroZhCN string
		}
		wiki.Raw(`SELECT name_ja_jp,name_zh_cn,name_zh_tw,name_en_us,intro_zh_cn FROM galgame WHERE id=?`, f.gid).Scan(&g)
		display := map[string]bool{g.NameJaJP: true, g.NameZhCN: true, g.NameZhTW: true, g.NameEnUS: true, correct.NameCN: true, correct.Name: true}

		// wrong-alias names to purge: the wrong subject's name / name_cn, unless they
		// are legitimately a display name or the correct subject's name.
		var badAliases []string
		for _, n := range []string{wrong.Name, wrong.NameCN} {
			n = strings.TrimSpace(n)
			if n != "" && !display[n] {
				badAliases = append(badAliases, n)
			}
		}
		// intro contamination: current intro == the wrong subject's summary. enrich
		// stored the summary verbatim, so compare raw AND trimmed to catch either.
		introContaminated := g.IntroZhCN != "" &&
			(g.IntroZhCN == wrong.Summary || strings.TrimSpace(g.IntroZhCN) == strings.TrimSpace(wrong.Summary))
		newIntro := strings.TrimSpace(correct.Summary)

		if len(badAliases) == 0 && !introContaminated {
			// still refresh meta (score/rank always from the wrong bid before)
			if *apply {
				upsertMeta(wiki, f, correct)
			}
			metaUp++
			fmt.Fprintf(logf, "%d\tmeta\t%s\tbid=%d score=%.2f\n", f.gid, applyWord(*apply), f.correctBid, correct.Score)
			continue
		}
		if *apply {
			if err := wiki.Transaction(func(tx *gorm.DB) error {
				for _, n := range badAliases {
					if err := tx.Exec(`DELETE FROM galgame_alias WHERE galgame_id=? AND name=?`, f.gid, n).Error; err != nil {
						return err
					}
				}
				if len(badAliases) > 0 {
					if err := purgeSnapshotAliases(tx, f.gid, badAliases); err != nil {
						return err
					}
				}
				if introContaminated {
					if err := tx.Exec(`UPDATE galgame SET intro_zh_cn=? WHERE id=? AND intro_zh_cn=?`, newIntro, f.gid, g.IntroZhCN).Error; err != nil {
						return err
					}
					if err := tx.Exec(`UPDATE galgame_revision SET snapshot=jsonb_set(snapshot,'{intro_zh_cn}',to_jsonb(?::text),true)
						WHERE galgame_id=? AND revision=(SELECT max(revision) FROM galgame_revision WHERE galgame_id=?)`, newIntro, f.gid, f.gid).Error; err != nil {
						return err
					}
				}
				return upsertMetaTx(tx, f, correct)
			}); err != nil {
				slog.Error("apply", "gid", f.gid, "error", err)
				os.Exit(1)
			}
		}
		if len(badAliases) > 0 {
			aliasDel += len(badAliases)
			fmt.Fprintf(logf, "%d\talias\t%s\tremove %v\n", f.gid, applyWord(*apply), badAliases)
		}
		if introContaminated {
			introFix++
			fmt.Fprintf(logf, "%d\tintro\t%s\twrong->correct (len %d->%d)\n", f.gid, applyWord(*apply), len(g.IntroZhCN), len(newIntro))
		}
		metaUp++
		fmt.Fprintf(logf, "%d\tmeta\t%s\tbid=%d score=%.2f\n", f.gid, applyWord(*apply), f.correctBid, correct.Score)
	}
	mode := "DRY-RUN"
	if *apply {
		mode = "APPLIED"
	}
	slog.Info("done", "mode", mode, "games", len(fixes), "aliases_removed", aliasDel, "intros_fixed", introFix, "meta_upserted", metaUp, "log", *logPath)
}

func loadSubject(cat *gorm.DB, bid int64) *subject {
	var s subject
	err := cat.Raw(`SELECT name,name_cn,summary,score,rank,nsfw,score_details FROM src_bangumi.subject WHERE id=?`, bid).Row().Scan(
		&s.Name, &s.NameCN, &s.Summary, &s.Score, &s.Rank, &s.NSFW, &s.ScoreDetails)
	if err != nil {
		return nil
	}
	return &s
}

func ratingTotal(details []byte) int {
	if len(details) == 0 {
		return 0
	}
	var buckets map[string]int
	if json.Unmarshal(details, &buckets) != nil {
		return 0
	}
	total := 0
	for _, v := range buckets {
		total += v
	}
	return total
}

func upsertMeta(db *gorm.DB, f fix, s *subject) { _ = upsertMetaTx(db, f, s) }
func upsertMetaTx(tx *gorm.DB, f fix, s *subject) error {
	return tx.Exec(`INSERT INTO galgame_bangumi_meta (galgame_id,bid,score,rank,total,nsfw,synced_at)
		VALUES (?,?,?,?,?,?,now())
		ON CONFLICT (galgame_id) DO UPDATE SET bid=EXCLUDED.bid,score=EXCLUDED.score,rank=EXCLUDED.rank,total=EXCLUDED.total,nsfw=EXCLUDED.nsfw,synced_at=now()`,
		f.gid, f.correctBid, s.Score, s.Rank, ratingTotal(s.ScoreDetails), s.NSFW).Error
}

// purgeSnapshotAliases removes the given names from the latest revision snapshot's
// `aliases` string array, keeping live galgame_alias == snapshot (Approach B).
func purgeSnapshotAliases(tx *gorm.DB, gid int64, names []string) error {
	drop, _ := json.Marshal(names)
	return tx.Exec(`
		UPDATE galgame_revision r SET snapshot = jsonb_set(snapshot, '{aliases}',
			COALESCE((SELECT jsonb_agg(a) FROM jsonb_array_elements(r.snapshot->'aliases') a
			          WHERE NOT (a IN (SELECT jsonb_array_elements(?::jsonb)))), '[]'::jsonb), true)
		WHERE r.galgame_id=? AND r.revision=(SELECT max(revision) FROM galgame_revision WHERE galgame_id=?)
		  AND jsonb_exists(r.snapshot, 'aliases')`, string(drop), gid, gid).Error
}

func applyWord(apply bool) string {
	if apply {
		return "APPLIED"
	}
	return "would"
}

func readFixes(path string) []fix {
	b, err := os.ReadFile(path)
	if err != nil {
		slog.Error("read file", "error", err)
		os.Exit(1)
	}
	var out []fix
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
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
