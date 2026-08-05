// entity-intro-mt is the ENTITY-intro machine-translation driver
// (refs/proj/172): →zh-Hans for characters, persons and labels, filling a
// MISSING language only, from ja when the entity has any ja source row and from
// en otherwise. Every written row carries provenance=1, the source hash and the
// producing model; the job never overwrites a source/human zh row.
//
//	# dry forecast over all three lanes (no LLM, no writes) — counts + samples
//	go run ./cmd/entity-intro-mt --dsn "$DSN"
//
//	# one lane, resumable slice (the character lane runs nightly in batches)
//	go run ./cmd/entity-intro-mt --dsn "$DSN" --lane character --limit 2000 --offset 4000
//
//	# quality gate: real-translate 30 labels, write them, print pairs
//	go run ./cmd/entity-intro-mt --dsn "$DSN" --lane label --limit 30 --apply \
//	    --llm-base http://one-api:3000/v1 --llm-token "$TOK" --model deepseek-chat
//
//	# rehearsal: full write-path proof with an OFFLINE mock translator
//	go run ./cmd/entity-intro-mt --dsn "$DSN" --apply --mock
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

	"api/internal/jobs/entityintromt"
	"api/pkg/logger"
)

func main() {
	dsn := flag.String("dsn", "", "catalog DSN (REQUIRED; rehearsal locally, live catalog only in the acceptance run)")
	apply := flag.Bool("apply", false, "translate + write (default: dry — counts + samples, no LLM, no writes)")
	lane := flag.String("lane", "", "restrict to one lane: character | person | label (empty = all three)")
	limit := flag.Int("limit", 0, "process only N candidates per lane in entity-id order (0 = all)")
	offset := flag.Int("offset", 0, "skip the first N candidates per lane — with --limit, the resumable batch window")
	model := flag.String("model", envOr("KUN_INTRO_MT_LLM_MODEL", envOr("KUN_AI_UPSTREAM_MODEL", "deepseek-chat")), "served model id (recorded in mt_model)")
	llmBase := flag.String("llm-base", envOr("KUN_INTRO_MT_LLM_BASE", os.Getenv("KUN_AI_UPSTREAM_BASE_URL")), "OpenAI-compatible gateway base URL (…/v1)")
	llmToken := flag.String("llm-token", envOr("KUN_INTRO_MT_LLM_TOKEN", os.Getenv("KUN_AI_UPSTREAM_TOKEN")), "gateway bearer token")
	maxTokens := flag.Int("max-tokens", 4096, "translation max_tokens")
	delayMS := flag.Int("delay-ms", 0, "rate-limit delay between real gateway calls (ms)")
	mock := flag.Bool("mock", false, "REHEARSAL ONLY: offline deterministic mock translator (no network; obvious marker output)")
	workers := flag.Int("workers", 1, "apply-mode concurrency (per-request latency dominates)")
	flag.Parse()

	logger.Init("development")

	if *dsn == "" {
		slog.Error("--dsn is required")
		os.Exit(2)
	}

	var tr entityintromt.Translator
	if *apply {
		if *mock {
			tr = entityintromt.MockTranslator{Model: *model}
			slog.Warn("MOCK translator active — rehearsal write-path proof only; rows are NOT real translations")
		} else {
			ht := entityintromt.NewHTTPTranslator(*llmBase, *llmToken, *model, *maxTokens)
			if !ht.Configured() {
				fmt.Printf("BLOCKED: LLM gateway not configured (need --llm-base + --llm-token, or KUN_INTRO_MT_LLM_* / KUN_AI_UPSTREAM_*).\n" +
					"This is a designed precondition for a real --apply, not a failure. Use --mock for the offline write-path rehearsal.\n")
				os.Exit(3)
			}
			tr = ht
			slog.Info("live gateway translator", "base", *llmBase, "model", *model)
		}
	}

	st, err := entityintromt.Run(context.Background(), tr, entityintromt.Opts{
		DSN: *dsn, Apply: *apply, Lane: *lane, Limit: *limit, Offset: *offset,
		Delay:   time.Duration(*delayMS) * time.Millisecond,
		Workers: *workers,
	})
	if err != nil {
		slog.Error("run failed", "error", err)
		os.Exit(1)
	}

	printReport(st, *apply)
}

func printReport(lanes []*entityintromt.LaneStats, apply bool) {
	fmt.Printf("\n=== entity-intro-mt %s ===\n", modeLabel(apply))
	for _, st := range lanes {
		fmt.Printf("\n[%s] candidates=%d from_ja=%d from_en=%d would_insert=%d would_retranslate=%d skip_unchanged=%d\n",
			st.Lane, st.Candidates, st.FromJa, st.FromEn,
			st.WouldInsert, st.WouldRetranslate, st.SkipUnchanged)
		if apply {
			fmt.Printf("[%s] inserted=%d retranslated=%d refused=%d errors=%d\n",
				st.Lane, st.Inserted, st.Retranslated, st.Refused, st.Errors)
		}
		for i, s := range st.Samples {
			fmt.Printf("\n--- %s sample %d (entity %d, %s%s) ---\n", s.Lane, i+1, s.EntityID, s.Decision, modelSuffix(s.MTModel))
			fmt.Printf("SRC(%s): %s\n", s.SrcLang, s.Src)
			if s.Zh != "" {
				fmt.Printf("ZH: %s\n", s.Zh)
			}
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
