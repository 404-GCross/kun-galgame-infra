// audit-vndb-ids audits galgame.vndb_id correctness against the live VNDB API.
// It surfaces two kinds of problem (vndb_id is uniquely indexed, so a wrong id
// "squats" that id — the correct game can never be saved with it and the sync
// silently skips it):
//
//  1. DEAD ids — the id does not exist on VNDB (fabricated/typo'd by hand, or a
//     VN later deleted/merged on VNDB). Split by status:
//     - status!=2 (user/admin entries) -> --clear blanks the id (game KEPT as
//     an unindexed entry; the id is freed). Loses no VNDB data: enrich could
//     never have pulled any for a non-existent id.
//     - status=2 (VNDB sync drafts for a now-deleted VN) -> --delete-drafts
//     removes the orphan stub (+ its relations); no one can claim a gone VN.
//
//  2. TITLE MISMATCH — the id exists, but NONE of the wiki game's own strings
//     (name_ja_jp / name_en_us / name_zh_cn / name_zh_tw + every galgame_alias)
//     EXACTLY matches ANY of the VN's VNDB titles (main + titles{} + latin +
//     aliases). That's the real "blocks the correct game" case: a typo onto a
//     DIFFERENT real VN. Reported for MANUAL review only (cross-language exact
//     matching is noisy — a hand-translated name can mismatch a correct link);
//     never auto-changed. Use --dump to write the full candidate list as TSV.
//
// Default: report only. Unlike cleanup-bogus-vndb-id (ids numerically above
// VNDB's max), this checks each in-range id against VNDB.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"strings"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/vndb"
	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/gorm"
)

// galgame child tables (rows keyed by galgame_id) purged before a draft row, for
// FK integrity. galgame_migrations is deliberately excluded (schema tracker).
var relatedTables = []string{
	"galgame_tag_relation", "galgame_official_relation", "galgame_engine_relation",
	"galgame_alias", "galgame_contributor", "galgame_link", "galgame_history",
	"galgame_revision", "galgame_pr", "galgame_cover", "galgame_screenshot",
	"galgame_message",
}

type row struct {
	ID       int    `gorm:"column:id"`
	VNDBID   string `gorm:"column:vndb_id"`
	NameJaJP string `gorm:"column:name_ja_jp"`
	NameEnUS string `gorm:"column:name_en_us"`
	NameZhCN string `gorm:"column:name_zh_cn"`
	NameZhTW string `gorm:"column:name_zh_tw"`
	Status   int    `gorm:"column:status"`
	UserID   int    `gorm:"column:user_id"`
}

func main() {
	doClear := flag.Bool("clear", false, "blank vndb_id of non-draft rows whose id is dead on VNDB")
	delDrafts := flag.Bool("delete-drafts", false, "delete status=2 sync drafts whose id is dead on VNDB (+ relations)")
	dumpPath := flag.String("dump", "", "write the title-mismatch candidates as TSV here")
	batchSize := flag.Int("batch", 100, "VNDB ids per API call (max 100)")
	gap := flag.Duration("gap", 2*time.Second, "min delay between VNDB API calls")
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
		SELECT id, vndb_id, name_ja_jp, name_en_us, name_zh_cn, name_zh_tw, status, user_id
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

	aliases, err := loadAliases(db)
	if err != nil {
		slog.Error("load aliases", "error", err)
		os.Exit(1)
	}

	if *batchSize < 1 || *batchSize > 100 {
		*batchSize = 100
	}
	vc := vndb.New(*gap)
	ctx := context.Background()

	titleSets := make(map[string][]string, len(rows)) // existing vndb_id -> VNDB title strings
	for start := 0; start < len(rows); start += *batchSize {
		end := min(start+*batchSize, len(rows))
		ids := make([]string, end-start)
		for i, r := range rows[start:end] {
			ids[i] = r.VNDBID
		}
		sets, err := vc.FetchVNTitleSets(ctx, ids)
		if err != nil {
			// Abort without changing anything — never act on a partial picture.
			slog.Error("vndb fetch failed; aborting (no rows changed)", "error", err, "at", start)
			os.Exit(1)
		}
		maps.Copy(titleSets, sets)
		if end%2000 == 0 || end == len(rows) {
			slog.Info("progress", "checked", end, "of", len(rows))
		}
	}

	var deadNonDraft, deadDraft, mismatch []row
	for _, r := range rows {
		vset, ok := titleSets[r.VNDBID]
		if !ok {
			if r.Status == 2 {
				deadDraft = append(deadDraft, r)
			} else {
				deadNonDraft = append(deadNonDraft, r)
			}
			continue
		}
		// Title-match only the user-originated entries; status=2 sync drafts take
		// their names straight from VNDB, so they match by construction.
		if r.Status == 2 {
			continue
		}
		wiki := append([]string{r.NameJaJP, r.NameEnUS, r.NameZhCN, r.NameZhTW}, aliases[r.ID]...)
		if !hasExactMatch(wiki, vset) {
			mismatch = append(mismatch, r)
		}
	}

	report("DEAD id, non-draft (--clear blanks the id; game kept)", deadNonDraft)
	report("DEAD id, status=2 sync draft of a deleted VN (--delete-drafts removes it)", deadDraft)
	fmt.Printf("\nTITLE MISMATCH candidates (id exists but no name matches its VNDB titles): %d\n", len(mismatch))
	for i, r := range mismatch {
		if i >= 40 {
			fmt.Printf("  ... and %d more (see --dump)\n", len(mismatch)-40)
			break
		}
		fmt.Printf("  id=%-7d vndb_id=%-9s status=%d  wiki=%q  vndb=%q\n",
			r.ID, r.VNDBID, r.Status, gameName(r), strings.Join(titleSets[r.VNDBID], " | "))
	}

	if *dumpPath != "" {
		if err := writeMismatchDump(*dumpPath, mismatch, titleSets, aliases); err != nil {
			slog.Error("write dump", "error", err)
			os.Exit(1)
		}
		fmt.Printf("wrote %d mismatch candidates to %s\n", len(mismatch), *dumpPath)
	}

	acted := false
	if *doClear && len(deadNonDraft) > 0 {
		ids := idsOf(deadNonDraft)
		res := db.Exec("UPDATE galgame SET vndb_id = '' WHERE id IN ?", ids)
		if res.Error != nil {
			slog.Error("clear vndb_id", "error", res.Error)
			os.Exit(1)
		}
		fmt.Printf("\n[clear] blanked vndb_id on %d rows.\n", res.RowsAffected)
		acted = true
	}
	if *delDrafts && len(deadDraft) > 0 {
		if err := deleteGames(db, idsOf(deadDraft)); err != nil {
			slog.Error("delete drafts", "error", err)
			os.Exit(1)
		}
		fmt.Printf("[delete-drafts] removed %d sync-draft rows + their relations.\n", len(deadDraft))
		acted = true
	}
	if !acted {
		fmt.Println("\n[dry-run] --clear blanks dead non-draft ids; --delete-drafts removes dead status=2 drafts. Mismatch candidates are review-only.")
	}
}

// hasExactMatch reports whether any wiki string equals any vndb string after a
// whitespace trim (exact, per the chosen rule — no case/width folding).
func hasExactMatch(wiki, vndb []string) bool {
	set := make(map[string]bool, len(vndb))
	for _, v := range vndb {
		if t := strings.TrimSpace(v); t != "" {
			set[t] = true
		}
	}
	for _, w := range wiki {
		if t := strings.TrimSpace(w); t != "" && set[t] {
			return true
		}
	}
	return false
}

func loadAliases(db *gorm.DB) (map[int][]string, error) {
	var rows []struct {
		GalgameID int    `gorm:"column:galgame_id"`
		Name      string `gorm:"column:name"`
	}
	if err := db.Raw("SELECT galgame_id, name FROM galgame_alias").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int][]string, len(rows))
	for _, r := range rows {
		if r.Name != "" {
			out[r.GalgameID] = append(out[r.GalgameID], r.Name)
		}
	}
	return out, nil
}

func deleteGames(db *gorm.DB, ids []int) error {
	return db.Transaction(func(tx *gorm.DB) error {
		for _, table := range relatedTables {
			if err := tx.Exec(fmt.Sprintf("DELETE FROM %s WHERE galgame_id IN ?", table), ids).Error; err != nil {
				return fmt.Errorf("delete from %s: %w", table, err)
			}
		}
		if err := tx.Exec("DELETE FROM galgame WHERE id IN ?", ids).Error; err != nil {
			return fmt.Errorf("delete galgame: %w", err)
		}
		return nil
	})
}

func report(title string, rows []row) {
	fmt.Printf("\n%s: %d\n", title, len(rows))
	for _, r := range rows {
		fmt.Printf("  id=%-7d vndb_id=%-9s status=%d user=%-7d %s\n", r.ID, r.VNDBID, r.Status, r.UserID, gameName(r))
	}
}

func writeMismatchDump(path string, rows []row, titleSets map[string][]string, aliases map[int][]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()
	fmt.Fprintln(w, "id\tvndb_id\tstatus\twiki_names\tvndb_titles")
	for _, r := range rows {
		wiki := append([]string{r.NameJaJP, r.NameEnUS, r.NameZhCN, r.NameZhTW}, aliases[r.ID]...)
		fmt.Fprintf(w, "%d\t%s\t%d\t%s\t%s\n", r.ID, r.VNDBID, r.Status,
			joinNonEmpty(wiki), strings.Join(titleSets[r.VNDBID], " | "))
	}
	return nil
}

func idsOf(rows []row) []int {
	ids := make([]int, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return ids
}

func joinNonEmpty(ss []string) string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, " | ")
}

func gameName(r row) string {
	for _, s := range []string{r.NameZhCN, r.NameJaJP, r.NameEnUS} {
		if s != "" {
			return s
		}
	}
	return "(no name)"
}
