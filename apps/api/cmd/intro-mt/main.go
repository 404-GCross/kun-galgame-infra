// intro-mt is the bodyless work-intro machine-translation PILOT driver (doc
// 75): ja→zh-Hans over the popularity-ranked top N, filling a MISSING language
// only, with a machine-provenance flag + source hash + model recorded on every
// row. The job never overwrites a source/human zh row.
//
//	# dry forecast (no LLM, no writes) — counts + samples
//	go run ./cmd/intro-mt --dsn "$DSN"
//
//	# quality gate: real-translate the 30 most popular, write them, print pairs
//	go run ./cmd/intro-mt --dsn "$DSN" --limit 30 --apply \
//	    --llm-base http://one-api:3000/v1 --llm-token "$TOK" --model deepseek-chat
//
//	# rehearsal: full write-path proof with an OFFLINE mock translator
//	go run ./cmd/intro-mt --dsn "$DSN" --apply --mock
//
// --dsn is ALWAYS explicit (a bare run cannot touch a live DB). The LLM gateway
// base/token/model come from flags or env (KUN_INTRO_MT_LLM_BASE/_TOKEN/_MODEL,
// falling back to the AI-gateway upstream KUN_AI_UPSTREAM_BASE_URL/_TOKEN/
// _MODEL) — NEVER hardcoded. An unconfigured gateway is a BLOCKED precondition
// for a real apply, not a crash.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"api/internal/jobs/intromt"
	"api/pkg/logger"
)

func main() {
	dsn := flag.String("dsn", "", "catalog DSN (REQUIRED; rehearsal locally, live catalog only in the acceptance run)")
	apply := flag.Bool("apply", false, "translate + write (default: dry — counts + samples, no LLM, no writes)")
	top := flag.Int("top", 5000, "popularity-ranked candidate ceiling (the pilot population)")
	limit := flag.Int("limit", 0, "process only the most-popular N candidates (0 = all within --top)")
	model := flag.String("model", envOr("KUN_INTRO_MT_LLM_MODEL", envOr("KUN_AI_UPSTREAM_MODEL", "deepseek-chat")), "served model id (recorded in mt_model)")
	llmBase := flag.String("llm-base", envOr("KUN_INTRO_MT_LLM_BASE", os.Getenv("KUN_AI_UPSTREAM_BASE_URL")), "OpenAI-compatible gateway base URL (…/v1)")
	llmToken := flag.String("llm-token", envOr("KUN_INTRO_MT_LLM_TOKEN", os.Getenv("KUN_AI_UPSTREAM_TOKEN")), "gateway bearer token")
	maxTokens := flag.Int("max-tokens", 4096, "translation max_tokens (ja intros run up to ~4.5k chars)")
	delayMS := flag.Int("delay-ms", 0, "rate-limit delay between real gateway calls (ms)")
	mock := flag.Bool("mock", false, "REHEARSAL ONLY: offline deterministic mock translator (no network; obvious marker output)")
	workers := flag.Int("workers", 1, "apply-mode concurrency (per-request latency dominates; 8 ≈ 10 req/min, far under gateway rate limits)")
	flag.Parse()

	logger.Init("development")

	if *dsn == "" {
		slog.Error("--dsn is required")
		os.Exit(2)
	}

	var tr intromt.Translator
	if *apply {
		if *mock {
			tr = intromt.MockTranslator{Model: *model}
			slog.Warn("MOCK translator active — rehearsal write-path proof only; rows are NOT real translations")
		} else {
			ht := intromt.NewHTTPTranslator(*llmBase, *llmToken, *model, *maxTokens)
			if !ht.Configured() {
				fmt.Printf("BLOCKED: LLM gateway not configured (need --llm-base + --llm-token, or KUN_INTRO_MT_LLM_* / KUN_AI_UPSTREAM_*).\n" +
					"This is a designed precondition for a real --apply, not a failure. Use --mock for the offline write-path rehearsal.\n")
				os.Exit(3)
			}
			tr = ht
			slog.Info("live gateway translator", "base", *llmBase, "model", *model)
		}
	}

	st, err := intromt.Run(context.Background(), tr, intromt.Opts{
		DSN: *dsn, Apply: *apply, Top: *top, Limit: *limit,
		Delay:   time.Duration(*delayMS) * time.Millisecond,
		Workers: *workers,
	})
	if err != nil {
		slog.Error("run failed", "error", err)
		os.Exit(1)
	}

	printReport(st, *apply)
}

func printReport(st *intromt.Stats, apply bool) {
	fmt.Printf("\n=== intro-mt %s ===\n", modeLabel(apply))
	fmt.Printf("candidates=%d would_insert=%d would_retranslate=%d skip_unchanged=%d\n",
		st.Candidates, st.WouldInsert, st.WouldRetranslate, st.SkipUnchanged)
	if apply {
		fmt.Printf("inserted=%d retranslated=%d refused=%d errors=%d\n",
			st.Inserted, st.Retranslated, st.Refused, st.Errors)
	}
	for i, s := range st.Samples {
		fmt.Printf("\n--- sample %d (work %d, %s%s) ---\n", i+1, s.WorkID, s.Decision, modelSuffix(s.MTModel))
		fmt.Printf("JA: %s\n", s.Ja)
		if s.Zh != "" {
			fmt.Printf("ZH: %s\n", s.Zh)
		}
	}
	if !apply {
		fmt.Println("\n[dry run] no LLM called, nothing written — re-run with --apply")
	}
}

func modeLabel(apply bool) string {
	if apply {
		return "APPLY"
	}
	return "DRY"
}

func modelSuffix(m string) string {
	if m == "" {
		return ""
	}
	return " · " + m
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
