// audit-bangumi-ids audits every non-null galgame.bid against the Bangumi
// Silver layer (src_bangumi.subject) — the per-source generalization of the
// audit-vndb-ids squatting patrol (doc 10 invariant 5's inspection half):
//
//   - MISSING: the bid does not exist in the dump (deleted/private subject);
//   - WRONG-TYPE: the bid exists but points at a non-game subject (anime
//     page, OST, original novel...) — review-only, never auto-cleared
//     (the link may be a human mistake worth fixing to the right game id,
//     not blanking);
//   - DUPLICATE: structurally impossible — galgame.bid carries a plain
//     unique index, so the bucket is reported as N/A by construction.
//
// Default: report only.
//
//	go run ./cmd/audit-bangumi-ids
//	go run ./cmd/audit-bangumi-ids --dump /tmp/bangumi-audit.tsv
//	go run ./cmd/audit-bangumi-ids --clear   # blank MISSING (dead) bids
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"api/internal/infrastructure/database"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

type row struct {
	ID       int
	BID      int `gorm:"column:bid"`
	Status   int
	UserID   int    `gorm:"column:user_id"`
	NameJaJP string `gorm:"column:name_ja_jp"`
	NameZhCN string `gorm:"column:name_zh_cn"`
	// Filled from Silver for wrong-type rows.
	SubjectType int
	SubjectName string
}

func main() {
	doClear := flag.Bool("clear", false, "blank bid of rows whose id is MISSING in the dump (wrong-type rows are never auto-cleared)")
	dumpPath := flag.String("dump", "", "write the findings as TSV here")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	wikiDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("wiki db connect", "error", err)
		os.Exit(1)
	}
	defer wikiDB.Close()
	catalogDB, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		slog.Error("catalog db connect", "error", err)
		os.Exit(1)
	}
	defer catalogDB.Close()

	var anchors []row
	if err := wikiDB.DB().Raw(`
		SELECT id, bid, status, user_id, name_ja_jp, name_zh_cn
		FROM galgame WHERE bid IS NOT NULL ORDER BY id
	`).Scan(&anchors).Error; err != nil {
		slog.Error("load bids", "error", err)
		os.Exit(1)
	}

	// Silver lookup: id → type/name.
	type subj struct {
		ID   int64
		Type int
		Name string
	}
	subjects := map[int]subj{}
	bids := make([]int, 0, len(anchors))
	for _, a := range anchors {
		bids = append(bids, a.BID)
	}
	for start := 0; start < len(bids); start += 1000 {
		end := min(start+1000, len(bids))
		var batch []subj
		if err := catalogDB.DB().Raw(
			`SELECT id, type, name FROM src_bangumi.subject WHERE id IN ?`, bids[start:end],
		).Scan(&batch).Error; err != nil {
			slog.Error("load subjects", "error", err)
			os.Exit(1)
		}
		for _, s := range batch {
			subjects[int(s.ID)] = s
		}
	}

	var missing, wrongType []row
	okCount := 0
	for _, a := range anchors {
		s, ok := subjects[a.BID]
		switch {
		case !ok:
			missing = append(missing, a)
		case s.Type != 4:
			a.SubjectType = s.Type
			a.SubjectName = s.Name
			wrongType = append(wrongType, a)
		default:
			okCount++
		}
	}

	report("MISSING in dump (deleted/private subject; --clear blanks the bid)", missing)
	report("WRONG-TYPE (bid points at a non-game subject; review-only)", wrongType)
	fmt.Printf("\nDUPLICATE bid: N/A by construction (galgame.bid has a unique index)\n")
	slog.Info("audit complete",
		"checked", len(anchors),
		"ok", okCount,
		"missing", len(missing),
		"wrong_type", len(wrongType),
	)

	if *dumpPath != "" {
		if err := writeTSV(*dumpPath, missing, wrongType); err != nil {
			slog.Error("write dump", "error", err)
			os.Exit(1)
		}
		slog.Info("dump written", "path", *dumpPath)
	}

	if *doClear {
		if len(missing) == 0 {
			fmt.Println("\n[clear] nothing to clear — no missing bids.")
			return
		}
		ids := make([]int, 0, len(missing))
		for _, r := range missing {
			ids = append(ids, r.ID)
		}
		res := wikiDB.DB().Exec(`UPDATE galgame SET bid = NULL WHERE id IN ?`, ids)
		if res.Error != nil {
			slog.Error("clear failed", "error", res.Error)
			os.Exit(1)
		}
		fmt.Printf("\n[clear] blanked bid on %d rows.\n", res.RowsAffected)
	} else if len(missing) > 0 {
		fmt.Println("\n[dry-run] --clear blanks MISSING bids. Wrong-type rows are review-only.")
	}
}

func report(title string, rows []row) {
	fmt.Printf("\n%s: %d\n", title, len(rows))
	for _, r := range rows {
		extra := ""
		if r.SubjectType != 0 {
			extra = fmt.Sprintf(" subject_type=%d subject=%s", r.SubjectType, r.SubjectName)
		}
		fmt.Printf("  id=%-7d bid=%-8d status=%d user=%-7d %s%s\n",
			r.ID, r.BID, r.Status, r.UserID, gameName(r), extra)
	}
}

func gameName(r row) string {
	if r.NameZhCN != "" {
		return r.NameZhCN
	}
	return r.NameJaJP
}

func writeTSV(path string, missing, wrongType []row) error {
	var b strings.Builder
	b.WriteString("bucket\tgalgame_id\tbid\tstatus\tuser_id\tname\tsubject_type\tsubject_name\n")
	for _, r := range missing {
		fmt.Fprintf(&b, "missing\t%d\t%d\t%d\t%d\t%s\t\t\n", r.ID, r.BID, r.Status, r.UserID, gameName(r))
	}
	for _, r := range wrongType {
		fmt.Fprintf(&b, "wrong_type\t%d\t%d\t%d\t%d\t%s\t%d\t%s\n",
			r.ID, r.BID, r.Status, r.UserID, gameName(r), r.SubjectType, r.SubjectName)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
