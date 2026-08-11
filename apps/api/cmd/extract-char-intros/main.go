// extract-char-intros — wave 178 P3, the fill bucket only.
//
// A work's zh-Hans intro often embeds per-character introductions. For the
// roster characters that have NO zh-Hans intro row at all, this job asks the
// model to EXTRACT (never write) those passages and files them as
// source=derived, provenance=machine rows (adjudication refs/proj/178 §3:
// a character without an intro adopts the extracted passage directly).
//
// The panel-compare bucket (a character that already has a machine intro) is
// deliberately NOT here — its read-face mechanics (source-order election vs
// "keep the better") still need adjudication; see the ledger.
//
// Anti-hallucination gate: an extracted passage must reappear verbatim
// (modulo whitespace and leading list markers) inside the work intro, or it
// is refused. The model is an index into the text, never an author.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"api/internal/infrastructure/database"
	"api/pkg/logger"

	"gorm.io/gorm"
)

func main() {
	dsn := flag.String("dsn", "", "catalog DSN (REQUIRED)")
	apply := flag.Bool("apply", false, "write the extracted rows (default: dry — extraction runs, nothing written)")
	limit := flag.Int("limit", 0, "max WORKS this run (0 = all) — ramp through a small limit first (the rehearsal rule)")
	offset := flag.Int("offset", 0, "skip the first N candidate works (stable id order)")
	model := flag.String("model", envOr("KUN_INTRO_MT_LLM_MODEL", envOr("KUN_AI_UPSTREAM_MODEL", "glm-5.2")), "served model id")
	llmBase := flag.String("llm-base", envOr("KUN_INTRO_MT_LLM_BASE", os.Getenv("KUN_AI_UPSTREAM_BASE_URL")), "OpenAI-compatible gateway base URL (…/v1)")
	llmToken := flag.String("llm-token", envOr("KUN_INTRO_MT_LLM_TOKEN", os.Getenv("KUN_AI_UPSTREAM_TOKEN")), "gateway bearer token")
	maxTokens := flag.Int("max-tokens", 16384, "max_tokens per extraction call")
	delayMS := flag.Int("delay-ms", 0, "delay between gateway calls (ms)")
	samples := flag.Int("samples", 5, "how many extracted samples to print")
	flag.Parse()

	logger.Init("development")

	if *dsn == "" {
		slog.Error("--dsn is required")
		os.Exit(2)
	}
	db, err := database.OpenJob(*dsn)
	if err != nil {
		slog.Error("open database", "error", err)
		os.Exit(1)
	}
	ex := newHTTPExtractor(*llmBase, *llmToken, *model, *maxTokens)
	if !ex.Configured() {
		fmt.Println("BLOCKED: LLM gateway not configured (need --llm-base + --llm-token, or KUN_INTRO_MT_LLM_* / KUN_AI_UPSTREAM_*).")
		os.Exit(3)
	}
	if err := run(context.Background(), db, ex, opts{
		Apply: *apply, Limit: *limit, Offset: *offset,
		Delay: time.Duration(*delayMS) * time.Millisecond, Samples: *samples,
	}); err != nil {
		slog.Error("run failed", "error", err)
		os.Exit(1)
	}
}

type opts struct {
	Apply   bool
	Limit   int
	Offset  int
	Delay   time.Duration
	Samples int
}

func run(ctx context.Context, db *gorm.DB, ex extractor, o opts) error {
	works, err := loadCandidateWorks(ctx, db, o.Limit, o.Offset)
	if err != nil {
		return err
	}
	fmt.Printf("\n=== extract-char-intros (%s) candidate_works=%d ===\n", mode(o.Apply), len(works))
	st := &stats{}
	w := &writer{db: db, apply: o.Apply, st: st, samples: o.Samples}
	for i, cand := range works {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if i > 0 && o.Delay > 0 {
			time.Sleep(o.Delay)
		}
		found, model, err := ex.Extract(ctx, cand)
		if err != nil {
			st.CallErrors++
			slog.Warn("extract failed", "work", cand.WorkID, "err", err)
			continue
		}
		w.write(ctx, cand, found, model)
		if (i+1)%25 == 0 {
			slog.Info("progress", "works", i+1, "of", len(works), "inserted", st.Inserted,
				"refused_not_verbatim", st.RefusedNotVerbatim, "call_errors", st.CallErrors)
		}
	}
	if o.Apply {
		if err := w.flushTouch(ctx); err != nil {
			return err
		}
	}
	fmt.Printf("\nworks=%d extracted=%d inserted=%d conflict=%d refused_not_verbatim=%d refused_short=%d unmatched_name=%d call_errors=%d touched_works=%d\n",
		len(works), st.Extracted, st.Inserted, st.Conflict, st.RefusedNotVerbatim, st.RefusedShort, st.UnmatchedName, st.CallErrors, st.Touched)
	if !o.Apply {
		fmt.Println("[dry run] nothing written — re-run with --apply")
	}
	return nil
}

func mode(apply bool) string {
	if apply {
		return "APPLY"
	}
	return "DRY"
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
