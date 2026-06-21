// audit-vndb-ids finds galgame rows whose vndb_id does NOT exist on VNDB —
// deleted ids, or fabricated/typo'd ids a user filled in "by hand" without
// actually finding the VN. Because vndb_id is uniquely indexed
// (uq_galgame_vndb_id_nonempty), such a wrong id "squats" that id: the correct
// game can never be created/saved with it (it hits ErrGalgameVNDBExists), and
// the VNDB sync silently skips it (id already held) so the real VN never gets a
// claimable draft.
//
// Unlike cleanup-bogus-vndb-id (which only catches ids numerically ABOVE VNDB's
// max), this checks each in-range id against the live VNDB API.
//
// Default: report only. Pass --clear to blank the offending rows' vndb_id — the
// game itself is KEPT (as an unindexed entry); only the id is freed so the
// correct game / the sync can use it. (Clearing a wrong, non-existent id never
// loses real VNDB data: enrich could not have pulled anything for it.)
//
// --dump <path> writes a TSV of every checked row (id, vndb_id, status,
// vndb_title, wiki_name) for manual review of the harder "valid id but WRONG VN"
// case (a typo that lands on a different real VN), which existence alone cannot
// detect — eyeball rows where wiki_name and vndb_title clearly disagree.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/vndb"
	"api/pkg/config"
	"api/pkg/logger"
)

type row struct {
	ID       int    `gorm:"column:id"`
	VNDBID   string `gorm:"column:vndb_id"`
	NameJaJP string `gorm:"column:name_ja_jp"`
	NameEnUS string `gorm:"column:name_en_us"`
	NameZhCN string `gorm:"column:name_zh_cn"`
	Status   int    `gorm:"column:status"`
	UserID   int    `gorm:"column:user_id"`
}

func main() {
	doClear := flag.Bool("clear", false, "Blank vndb_id of rows that don't exist on VNDB (default: report only)")
	batchSize := flag.Int("batch", 100, "VNDB ids per API call (max 100)")
	gap := flag.Duration("gap", 2*time.Second, "min delay between VNDB API calls")
	dumpPath := flag.String("dump", "", "write a TSV of every checked row (id,vndb_id,status,vndb_title,wiki_name) here")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	wikiDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("connect galgame wiki db", "error", err)
		os.Exit(1)
	}
	db := wikiDB.DB()

	var rows []row
	if err := db.Raw(`
		SELECT id, vndb_id, name_ja_jp, name_en_us, name_zh_cn, status, user_id
		FROM galgame
		WHERE vndb_id ~ '^v[0-9]+$'
		ORDER BY id
	`).Scan(&rows).Error; err != nil {
		slog.Error("query galgames", "error", err)
		os.Exit(1)
	}
	fmt.Printf("galgames with a vndb_id: %d\n", len(rows))
	if len(rows) == 0 {
		return
	}

	if *batchSize < 1 || *batchSize > 100 {
		*batchSize = 100
	}
	vc := vndb.New(*gap)
	ctx := context.Background()

	// titles holds the VNDB title for every id that exists (for --dump); rows
	// whose id is missing from a batch result are the wrong/squatted ones.
	titles := make(map[string]string, len(rows))
	var missing []row
	for start := 0; start < len(rows); start += *batchSize {
		end := min(start+*batchSize, len(rows))
		chunk := rows[start:end]
		ids := make([]string, len(chunk))
		for i, r := range chunk {
			ids[i] = r.VNDBID
		}
		exists, err := vc.FetchExistingVNIDs(ctx, ids)
		if err != nil {
			// Abort without clearing — never act on a partial existence picture.
			slog.Error("vndb existence check failed; aborting (no rows changed)", "error", err, "at", start)
			os.Exit(1)
		}
		for _, r := range chunk {
			if t, ok := exists[r.VNDBID]; ok {
				titles[r.VNDBID] = t
			} else {
				missing = append(missing, r)
			}
		}
		if end%2000 == 0 || end == len(rows) {
			slog.Info("progress", "checked", end, "of", len(rows), "missing", len(missing))
		}
	}

	if *dumpPath != "" {
		if err := writeDump(*dumpPath, rows, titles); err != nil {
			slog.Error("write dump", "error", err)
			os.Exit(1)
		}
		fmt.Printf("wrote review TSV (%d rows) to %s\n", len(rows), *dumpPath)
	}

	fmt.Printf("\nvndb_ids NOT found on VNDB (wrong/squatted): %d\n", len(missing))
	for _, r := range missing {
		fmt.Printf("  id=%-7d vndb_id=%-10s status=%d user=%-7d %s\n",
			r.ID, r.VNDBID, r.Status, r.UserID, gameName(r))
	}
	if len(missing) == 0 {
		fmt.Println("All vndb_ids resolve on VNDB. Nothing to clear.")
		return
	}

	if !*doClear {
		fmt.Println("\n[dry-run] Pass --clear to blank these vndb_ids (the games are kept; the ids are freed).")
		return
	}

	ids := make([]int, len(missing))
	for i, r := range missing {
		ids[i] = r.ID
	}
	res := db.Exec("UPDATE galgame SET vndb_id = '' WHERE id IN ?", ids)
	if res.Error != nil {
		slog.Error("clear vndb_id", "error", res.Error)
		os.Exit(1)
	}
	fmt.Printf("\n[clear] blanked vndb_id on %d rows.\n", res.RowsAffected)
}

func writeDump(path string, rows []row, titles map[string]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()
	fmt.Fprintln(w, "id\tvndb_id\tstatus\tvndb_title\twiki_name")
	for _, r := range rows {
		title, ok := titles[r.VNDBID]
		if !ok {
			title = "(NOT ON VNDB)"
		}
		fmt.Fprintf(w, "%d\t%s\t%d\t%s\t%s\n", r.ID, r.VNDBID, r.Status, title, gameName(r))
	}
	return nil
}

func gameName(r row) string {
	for _, s := range []string{r.NameZhCN, r.NameJaJP, r.NameEnUS} {
		if s != "" {
			return s
		}
	}
	return "(no name)"
}
