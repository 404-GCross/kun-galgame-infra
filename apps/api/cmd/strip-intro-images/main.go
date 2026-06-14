// strip-intro-images removes embedded images (Markdown ![](url) + BBCode
// [img]...[/img]) from galgame intros in ALL languages, enforcing the wiki's
// "no images in intros — use the gallery" rule on legacy rows that predate the
// single write-path guard (intronorm.StripImages inside repository.ApplySnapshot).
//
// Why this exists: the earlier normalize-galgame-intro cleanup ran on
// intro_en_us ONLY (VNDB BBCode legacy). The bulk moyu/kungal imports
// (migrate-moyu-galgame / migrate-galgame-data) wrote intro_zh_cn/ja_jp/zh_tw
// directly, bypassing the guard, which left ~900 rows with Markdown images.
// Every ONGOING write (create/edit/submit/PR-merge/revert) now strips via
// ApplySnapshot, and VNDB sync writes no intros — so this is a one-off,
// terminal backfill: nothing re-introduces images afterwards.
//
// Write strategy = APPROACH B (same as normalize-galgame-intro): per changed
// galgame, in one transaction, UPDATE the changed intro column(s) AND patch the
// LATEST revision's snapshot jsonb for those keys, so revision.snapshot == DB
// stays true (revision design §1.5 #4). No new revisions are minted. The
// transform is intronorm.StripImages (idempotent), so re-running is safe.
//
//	go run ./cmd/strip-intro-images                          # dry run (report only)
//	go run ./cmd/strip-intro-images --apply --limit 3        # apply a tiny test batch
//	go run ./cmd/strip-intro-images --apply --images /tmp/intro_images.csv
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"

	// Embed the IANA tz database so KUN_PG_TIMEZONE (e.g. Asia/Shanghai)
	// resolves even in a minimal container without system tzdata — this cmd is
	// run as a one-off static binary, not only via the tzdata-equipped image.
	_ "time/tzdata"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/intronorm"
	"api/internal/platform/galgame/model"
	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/gorm"
)

// introCols is the fixed, hard-coded set of intro columns. The names are
// interpolated into UPDATE / jsonb_set SQL below, so they must NEVER come from
// data — this whitelist is the only source.
var introCols = []string{"intro_en_us", "intro_ja_jp", "intro_zh_cn", "intro_zh_tw"}

// candidateFilter pulls only rows carrying an image marker in some intro, so we
// scan ~hundreds of rows instead of all 60k. StripImages then confirms + cleans.
// Postgres LIKE treats only % and _ as special (not [), so '%![%' / '%[img%'
// match the literal markers.
const candidateFilter = `intro_en_us LIKE '%![%' OR intro_ja_jp LIKE '%![%' OR intro_zh_cn LIKE '%![%' OR intro_zh_tw LIKE '%![%' OR ` +
	`intro_en_us ILIKE '%[img%' OR intro_ja_jp ILIKE '%[img%' OR intro_zh_cn ILIKE '%[img%' OR intro_zh_tw ILIKE '%[img%'`

// Image-URL extractors for the OPTIONAL manifest only — the authoritative
// cleaning is intronorm.StripImages. Lenient: just capture the URL.
var (
	// Alt body skips escaped brackets so "图片\[1\]" / "\[4H\]" alts don't cut
	// the match short (mirrors intronorm.altText).
	reImgMarkdown = regexp.MustCompile(`!\[(?:[^\]\\]|\\.)*\]\(\s*<?([^)>\s]+)`)
	reImgBBCode   = regexp.MustCompile(`(?is)\[img(?:=([^\]]*))?\]\s*<?([^\[<>\s]*)`)
)

type change struct {
	ID  int
	New map[string]string // changed column -> stripped value (only changed cols)
}

type introRow struct {
	ID        int    `gorm:"column:id"`
	IntroEnUS string `gorm:"column:intro_en_us"`
	IntroJaJP string `gorm:"column:intro_ja_jp"`
	IntroZhCN string `gorm:"column:intro_zh_cn"`
	IntroZhTW string `gorm:"column:intro_zh_tw"`
}

func main() {
	samples := flag.Int("samples", 8, "number of before/after diffs to print")
	imagesPath := flag.String("images", "", "write removed-image manifest CSV (galgame_id,lang,image_url) here")
	apply := flag.Bool("apply", false, "perform the changes (default: dry run, nothing written)")
	limit := flag.Int("limit", 0, "cap the number of rows changed (0 = all); for a safe test batch")
	batchSize := flag.Int("batch", 500, "rows per write transaction")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	db, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("connect galgame db", "error", err, "dbname", cfg.GalgameDatabase.DBName)
		os.Exit(1)
	}
	defer db.Close()
	gdb := db.DB()

	var scanned, shown, totalImages int
	perLang := map[string]int{}
	var changes []change
	var imgRows [][]string

	var batch []introRow
	res := gdb.Model(&model.Galgame{}).
		Select("id", "intro_en_us", "intro_ja_jp", "intro_zh_cn", "intro_zh_tw").
		Where(candidateFilter).
		FindInBatches(&batch, 1000, func(_ *gorm.DB, _ int) error {
			for _, r := range batch {
				scanned++
				orig := map[string]string{
					"intro_en_us": r.IntroEnUS,
					"intro_ja_jp": r.IntroJaJP,
					"intro_zh_cn": r.IntroZhCN,
					"intro_zh_tw": r.IntroZhTW,
				}
				ch := change{ID: r.ID, New: map[string]string{}}
				for _, col := range introCols {
					cleaned := intronorm.StripImages(orig[col])
					if cleaned == orig[col] {
						continue
					}
					ch.New[col] = cleaned
					perLang[col]++
					urls := extractImageURLs(orig[col])
					totalImages += len(urls)
					for _, u := range urls {
						imgRows = append(imgRows, []string{strconv.Itoa(r.ID), col, u})
					}
				}
				if len(ch.New) == 0 {
					continue
				}
				changes = append(changes, ch)
				if shown < *samples {
					shown++
					col := firstChangedCol(ch.New)
					b, a := diffWindow(orig[col], ch.New[col])
					fmt.Printf("\n──────── galgame #%d  (%s) ────────\n[BEFORE] …%s…\n[AFTER ] …%s…\n", r.ID, col, b, a)
				}
			}
			return nil
		})
	if res.Error != nil {
		slog.Error("scan galgame intros", "error", res.Error)
		os.Exit(1)
	}

	if *imagesPath != "" && len(imgRows) > 0 {
		if err := writeCSV(*imagesPath, imgRows); err != nil {
			slog.Error("write image manifest", "error", err)
		} else {
			fmt.Printf("\nimage manifest written: %s (%d urls)\n", *imagesPath, len(imgRows))
		}
	}

	fmt.Printf("\n==================== SUMMARY ====================\n")
	fmt.Printf("candidate rows scanned (image marker present): %d\n", scanned)
	fmt.Printf("rows to change:                                %d\n", len(changes))
	for _, col := range introCols {
		fmt.Printf("  · %-12s changed: %d\n", col, perLang[col])
	}
	fmt.Printf("total image URLs removed:                      %d\n", totalImages)

	if !*apply {
		fmt.Printf("\nDRY RUN — nothing written. Re-run with --apply to perform.\n")
		return
	}

	toApply := changes
	if *limit > 0 && *limit < len(toApply) {
		toApply = toApply[:*limit]
		fmt.Printf("\n--limit %d: applying only the first %d changes\n", *limit, len(toApply))
	}
	fmt.Printf("\nAPPLYING %d changes (approach B: UPDATE intro col(s) + patch latest revision snapshot)…\n", len(toApply))

	applied := 0
	for start := 0; start < len(toApply); start += *batchSize {
		chunk := toApply[start:min(start+*batchSize, len(toApply))]
		if err := gdb.Transaction(func(tx *gorm.DB) error {
			for _, c := range chunk {
				for _, col := range introCols { // fixed order; skip unchanged cols
					nv, ok := c.New[col]
					if !ok {
						continue
					}
					// col is a hard-coded whitelist value (introCols), never data.
					if err := tx.Exec(`UPDATE galgame SET `+col+` = ? WHERE id = ?`, nv, c.ID).Error; err != nil {
						return err
					}
					if err := tx.Exec(`
						UPDATE galgame_revision
						SET snapshot = jsonb_set(snapshot, '{`+col+`}', to_jsonb(?::text), true)
						WHERE galgame_id = ?
						  AND revision = (SELECT max(revision) FROM galgame_revision WHERE galgame_id = ?)
					`, nv, c.ID, c.ID).Error; err != nil {
						return err
					}
				}
			}
			return nil
		}); err != nil {
			slog.Error("apply batch failed", "start", start, "error", err)
			os.Exit(1)
		}
		applied += len(chunk)
		fmt.Printf("  applied %d / %d\n", applied, len(toApply))
	}
	fmt.Printf("DONE. %d galgame intros stripped of images (live columns + latest revision snapshot).\n", applied)
}

// extractImageURLs collects image URLs for the manifest. Mirrors intronorm's
// markup recognition but only needs the URL (the cleaning itself is delegated to
// intronorm.StripImages).
func extractImageURLs(s string) []string {
	if !strings.Contains(s, "![") && !strings.Contains(strings.ToLower(s), "[img") {
		return nil
	}
	var urls []string
	for _, m := range reImgBBCode.FindAllStringSubmatch(s, -1) {
		if u := firstNonEmpty(m[1], m[2]); u != "" {
			urls = append(urls, u)
		}
	}
	for _, m := range reImgMarkdown.FindAllStringSubmatch(s, -1) {
		if m[1] != "" {
			urls = append(urls, m[1])
		}
	}
	return urls
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func firstChangedCol(m map[string]string) string {
	for _, col := range introCols {
		if _, ok := m[col]; ok {
			return col
		}
	}
	return ""
}

// diffWindow returns a short before/after snippet centered on the first rune
// where the two strings diverge, so the actual edit is visible even mid-text.
func diffWindow(before, after string) (string, string) {
	const ctx = 90
	rb, ra := []rune(before), []rune(after)
	i := 0
	for i < len(rb) && i < len(ra) && rb[i] == ra[i] {
		i++
	}
	lo := max(0, i-ctx)
	return string(rb[lo:min(i+ctx, len(rb))]), string(ra[lo:min(i+ctx, len(ra))])
}

func writeCSV(path string, rows [][]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write([]string{"galgame_id", "lang", "image_url"}); err != nil {
		return err
	}
	return w.WriteAll(rows)
}
