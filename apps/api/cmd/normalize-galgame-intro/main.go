// normalize-galgame-intro cleans legacy VNDB BBCode out of English galgame
// intros (intro_en_us only — other languages are user-authored Markdown).
//
// Rules: see internal/platform/galgame/intronorm. Per the agreed plan:
//   - delete the trailing VNDB attribution line ([From ...], [Translated from
//     ...], etc.);
//   - convert inline [url=X]Y[/url] / [url]X[/url] to Markdown links;
//   - strip every other BBCode tag, keeping inner text;
//   - remove images from the text (manifest exported, NOT migrated).
//
// Write strategy = APPROACH B: for each changed galgame, in one transaction,
// UPDATE galgame.intro_en_us AND patch the LATEST revision's snapshot jsonb so
// revision.snapshot == DB stays true (revision design §1.5 #4). No new
// revisions are created. The transform is idempotent, so re-running is safe.
//
//	go run ./cmd/normalize-galgame-intro                         # dry run (report only)
//	go run ./cmd/normalize-galgame-intro --apply --limit 3       # apply a tiny test batch
//	go run ./cmd/normalize-galgame-intro --apply --images /tmp/intro_images.csv
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/intronorm"
	"api/internal/platform/galgame/model"
	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/gorm"
)

type change struct {
	ID  int
	New string
}

func main() {
	samples := flag.Int("samples", 8, "number of before/after diffs to print")
	imagesPath := flag.String("images", "", "write removed-image manifest CSV (galgame_id,image_url) here")
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

	var scanned, withImages, totalImages, shown int
	var catTrailer, catURL, catOtherBB int
	var changes []change
	var imgRows [][]string

	var batch []struct {
		ID        int
		IntroEnUS string
	}
	res := gdb.Model(&model.Galgame{}).
		Select("id", "intro_en_us").
		Where("intro_en_us <> ''").
		FindInBatches(&batch, 1000, func(_ *gorm.DB, _ int) error {
			for _, r := range batch {
				scanned++
				out, imgs, ch := intronorm.NormalizeEnglishIntro(r.IntroEnUS)
				if len(imgs) > 0 {
					withImages++
					totalImages += len(imgs)
					for _, u := range imgs {
						imgRows = append(imgRows, []string{strconv.Itoa(r.ID), u})
					}
				}
				if !ch {
					continue
				}
				changes = append(changes, change{ID: r.ID, New: out})
				lc := strings.ToLower(r.IntroEnUS)
				if containsAny(lc, "[from ", "[edited ", `\[from`, `\[edited`, "[translated", "[taken", "[source", "[adapted") {
					catTrailer++
				}
				if strings.Contains(lc, "[url") {
					catURL++
				}
				if containsAny(lc, "[spoiler", "[quote", "[code", "[b]", "[/b]", "[i]", "[/i]", "[u]", "[color", "[size", "[center") {
					catOtherBB++
				}
				if shown < *samples {
					shown++
					b, a := diffWindow(r.IntroEnUS, out)
					fmt.Printf("\n──────── galgame #%d (first change) ────────\n[BEFORE] …%s…\n[AFTER ] …%s…\n", r.ID, b, a)
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
	fmt.Printf("scanned (non-empty intro_en_us): %d\n", scanned)
	fmt.Printf("to change:                       %d\n", len(changes))
	fmt.Printf("  · with VNDB attribution trailer:          %d\n", catTrailer)
	fmt.Printf("  · with inline [url=...] links → markdown:  %d\n", catURL)
	fmt.Printf("  · with other BBCode tags → plain text:     %d\n", catOtherBB)
	fmt.Printf("  · with images (stripped from text):        %d  (image URLs: %d)\n", withImages, totalImages)
	fmt.Printf("(category buckets overlap — a row can hit several.)\n")

	if !*apply {
		fmt.Printf("\nDRY RUN — nothing written. Re-run with --apply to perform.\n")
		return
	}

	toApply := changes
	if *limit > 0 && *limit < len(toApply) {
		toApply = toApply[:*limit]
		fmt.Printf("\n--limit %d: applying only the first %d changes\n", *limit, len(toApply))
	}
	fmt.Printf("\nAPPLYING %d changes (approach B: UPDATE intro_en_us + patch latest revision snapshot)…\n", len(toApply))

	applied := 0
	for start := 0; start < len(toApply); start += *batchSize {
		chunk := toApply[start:min(start+*batchSize, len(toApply))]
		if err := gdb.Transaction(func(tx *gorm.DB) error {
			for _, c := range chunk {
				if err := tx.Exec(`UPDATE galgame SET intro_en_us = ? WHERE id = ?`, c.New, c.ID).Error; err != nil {
					return err
				}
				// Keep the latest revision's snapshot == live DB (§1.5 #4).
				if err := tx.Exec(`
					UPDATE galgame_revision
					SET snapshot = jsonb_set(snapshot, '{intro_en_us}', to_jsonb(?::text), true)
					WHERE galgame_id = ?
					  AND revision = (SELECT max(revision) FROM galgame_revision WHERE galgame_id = ?)
				`, c.New, c.ID, c.ID).Error; err != nil {
					return err
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
	fmt.Printf("DONE. %d English intros normalized (live column + latest revision snapshot).\n", applied)
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// diffWindow returns a short before/after snippet centered on the first rune
// where the two strings diverge, so the actual edit is visible even when it is
// buried mid- or end-of-text (e.g. a removed trailing attribution line).
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
	if err := w.Write([]string{"galgame_id", "image_url"}); err != nil {
		return err
	}
	return w.WriteAll(rows)
}
