// ingest-trait-zh gives catalog_character_trait its Simplified-Chinese names
// (wave 176). The 3,327-row VNDB trait vocabulary is English-only, so every
// character page renders "Ahoge" and "Kuudere" raw; this tool fills name_zh
// from two sources, in strict priority order:
//
//   - CURATED (name_zh_provenance=0): the community dictionary below, ~2.6k of
//     the 3,094 distinct names, hand-written by galgame readers.
//   - MACHINE (name_zh_provenance=1): an LLM proposal for the residue, emitted
//     as a review CSV and written back only after a human has read it.
//
// Attribution — the curated dictionary is not ours:
//
//	VNDBTranslatorLib v5.0.0 · author aotmd · MIT licence
//	https://greasyfork.org/ (userscript; the trait blocks additionally credit
//	the contributor https://greasyfork.org/zh-CN/users/1210764-railguns)
//
// The script is a Tampermonkey userscript translating the VNDB web UI. Only its
// (标签与特征) rule's CHARACTER-TRAIT sections are read — the VN-tag sections of
// the same map are a different vocabulary (--include-tag-vocab, off by default)
// — and every key must additionally exist in catalog_character_trait.name, so a
// UI string can never be mistaken for a trait name.
//
//	# dry: parse + match + per-decision counts + samples (no writes)
//	go run ./cmd/ingest-trait-zh --dsn "$DSN" --script /path/vndbtranslatorlib.js
//
//	# write the curated renderings (provenance 0)
//	go run ./cmd/ingest-trait-zh --dsn "$DSN" --script /path/vndbtranslatorlib.js --apply
//
//	# residue lane: LLM-propose the names still empty → review sheet, NO writes
//	go run ./cmd/ingest-trait-zh --dsn "$DSN" --mt --out /tmp/trait-zh-review.csv
//
//	# write back the REVIEWED sheet (provenance 1)
//	go run ./cmd/ingest-trait-zh --dsn "$DSN" --apply-csv /tmp/trait-zh-review.csv
//
// --dsn is ALWAYS explicit (a bare run cannot touch a live database), and the
// gateway config comes from flags or env (KUN_INTRO_MT_LLM_BASE/_TOKEN/_MODEL,
// falling back to KUN_AI_UPSTREAM_BASE_URL/_TOKEN/_MODEL) — never hardcoded.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"api/pkg/logger"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func main() {
	dsn := flag.String("dsn", "", "catalog DSN (REQUIRED)")
	script := flag.String("script", "", "path to the VNDBTranslatorLib userscript (required unless --mt / --apply-csv)")
	apply := flag.Bool("apply", false, "write the curated renderings (default: dry — counts + samples, no writes)")
	includeTags := flag.Bool("include-tag-vocab", false, "ALSO read the script's VN-TAG sections (a different vocabulary that shares ~365 names with the trait table) — review the diff before using")
	mtMode := flag.Bool("mt", false, "residue lane: LLM-propose the names still empty and emit a review CSV (never writes to the DB)")
	out := flag.String("out", "", "--mt: review CSV output path (required)")
	applyCSV := flag.String("apply-csv", "", "write back a REVIEWED review CSV as machine provenance")
	limit := flag.Int("limit", 0, "--mt: propose at most N candidates (0 = all)")
	model := flag.String("model", envOr("KUN_INTRO_MT_LLM_MODEL", envOr("KUN_AI_UPSTREAM_MODEL", "glm-5.2")), "served model id")
	llmBase := flag.String("llm-base", envOr("KUN_INTRO_MT_LLM_BASE", os.Getenv("KUN_AI_UPSTREAM_BASE_URL")), "OpenAI-compatible gateway base URL (…/v1)")
	llmToken := flag.String("llm-token", envOr("KUN_INTRO_MT_LLM_TOKEN", os.Getenv("KUN_AI_UPSTREAM_TOKEN")), "gateway bearer token")
	maxTokens := flag.Int("max-tokens", 256, "--mt: max_tokens (a trait name is a handful of characters)")
	delayMS := flag.Int("delay-ms", 0, "--mt: delay between gateway calls (ms)")
	samples := flag.Int("samples", 10, "how many sample pairs to print")
	flag.Parse()

	logger.Init("development")

	if *dsn == "" {
		slog.Error("--dsn is required")
		os.Exit(2)
	}
	db, err := open(*dsn)
	if err != nil {
		slog.Error("open database", "error", err)
		os.Exit(1)
	}
	ctx := context.Background()

	switch {
	case *mtMode:
		if *out == "" {
			slog.Error("--mt requires --out (the review CSV path)")
			os.Exit(2)
		}
		tr := newHTTPTranslator(*llmBase, *llmToken, *model, *maxTokens)
		if !tr.Configured() {
			fmt.Println("BLOCKED: LLM gateway not configured (need --llm-base + --llm-token, or KUN_INTRO_MT_LLM_* / KUN_AI_UPSTREAM_*).\n" +
				"This is a designed precondition for --mt, not a failure.")
			os.Exit(3)
		}
		err = runMT(ctx, db, tr, *out, *limit, time.Duration(*delayMS)*time.Millisecond)
	case *applyCSV != "":
		err = runIngest(ctx, db, csvSource(*applyCSV), provenanceMachine, true, *samples)
	default:
		if *script == "" {
			slog.Error("--script is required")
			os.Exit(2)
		}
		err = runIngest(ctx, db, scriptSource(*script, parseOpts{IncludeTagVocab: *includeTags}), provenanceCurated, *apply, *samples)
	}
	if err != nil {
		slog.Error("run failed", "error", err)
		os.Exit(1)
	}
}

// source yields the proposed renderings plus a label for the report.
type source struct {
	label string
	load  func() ([]pair, error)
}

func scriptSource(path string, opts parseOpts) source {
	return source{label: "userscript " + path, load: func() ([]pair, error) { return parseScript(path, opts) }}
}

func csvSource(path string) source {
	return source{label: "reviewed CSV " + path, load: func() ([]pair, error) { return readReviewCSV(path) }}
}

// runIngest is the shared plan/report/write path of both write lanes: the only
// thing that differs is where the pairs came from and which provenance they
// carry. Dry is the default everywhere, and the guard lives in plan().
func runIngest(ctx context.Context, db *gorm.DB, src source, prov int16, apply bool, samples int) error {
	pairs, err := src.load()
	if err != nil {
		return err
	}
	rows, err := loadTraits(ctx, db)
	if err != nil {
		return err
	}
	writes := plan(pairs, rows, prov)
	c := summarise(pairs, writes)

	fmt.Printf("\n=== ingest-trait-zh %s (%s, provenance=%d) ===\n", mode(apply), src.label, prov)
	fmt.Printf("proposals=%d  vocabulary_rows=%d  matched_rows=%d  write=%d  already_same=%d  conflict=%d\n",
		c.Proposals, len(rows), c.Matched, c.Write, c.Same, c.Conflict)
	fmt.Printf("still_without_zh_after_run=%d\n", withoutZhAfter(rows, writes))
	printSamples(writes, samples)
	printConflicts(writes)

	if !apply {
		fmt.Println("\n[dry run] nothing written — re-run with --apply")
		return nil
	}
	n, err := applyWrites(ctx, db, writes, prov)
	if err != nil {
		return err
	}
	fmt.Printf("\nwritten=%d\n", n)
	return nil
}

// runMT proposes a Chinese name for every trait still without one and writes
// the review sheet. It deliberately has no --apply: machine output enters the
// database only through --apply-csv, after review.
func runMT(ctx context.Context, db *gorm.DB, tr translator, out string, limit int, delay time.Duration) error {
	cands, err := loadMTCandidates(ctx, db, limit)
	if err != nil {
		return err
	}
	fmt.Printf("\n=== ingest-trait-zh MT (candidates without name_zh: %d) ===\n", len(cands))
	rows := make([]csvRow, 0, len(cands))
	var errs int
	for i, c := range cands {
		if i > 0 && delay > 0 {
			time.Sleep(delay)
		}
		zh, _, err := tr.Translate(ctx, c)
		if err != nil {
			errs++
			slog.Warn("translate failed", "trait", c.Name, "error", err)
			zh = "" // an empty proposal is a reviewer prompt, not a silent skip
		}
		rows = append(rows, csvRow{
			TraitID: c.ID, VndbTID: c.VndbTID, Name: c.Name, Group: c.GroupName,
			Description: truncateRunes(plainDescription(c.Description), 120), ProposedZh: zh,
		})
		if (i+1)%50 == 0 {
			slog.Info("mt progress", "done", i+1, "of", len(cands), "errors", errs)
		}
	}
	if err := writeReviewCSV(out, rows); err != nil {
		return err
	}
	fmt.Printf("proposed=%d errors=%d → %s\nreview it, then: --apply-csv %s\n", len(rows)-errs, errs, out, out)
	return nil
}

// withoutZhAfter counts the vocabulary rows that would STILL have no Chinese
// name once this run's writes land — the number the residue lane has to close.
func withoutZhAfter(rows []traitRow, writes []plannedWrite) int {
	filled := map[int64]bool{}
	for _, w := range writes {
		if w.Decision == decWrite {
			filled[w.Trait.ID] = true
		}
	}
	n := 0
	for _, r := range rows {
		if r.NameZh == "" && !filled[r.ID] {
			n++
		}
	}
	return n
}

func printSamples(writes []plannedWrite, n int) {
	shown := 0
	for _, w := range writes {
		if w.Decision != decWrite || shown >= n {
			continue
		}
		fmt.Printf("  sample: %-40s → %s\n", w.Trait.Name, w.Zh)
		shown++
	}
}

func printConflicts(writes []plannedWrite) {
	for _, w := range writes {
		if w.Decision == decConflict {
			fmt.Printf("  CONFLICT trait %d (%s) %q: curated %q vs proposed %q — left untouched\n",
				w.Trait.ID, w.Trait.VndbTID, w.Trait.Name, w.Trait.NameZh, w.Zh)
		}
	}
}

func mode(apply bool) string {
	if apply {
		return "APPLY"
	}
	return "DRY"
}

func open(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
