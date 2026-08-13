package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/jobs/tagcanon"
	"api/pkg/logger"
)

func main() {
	mode := flag.String("mode", "", "propose | review | apply")
	dsn := flag.String("dsn", "", "catalog DSN (REQUIRED for propose/apply; rehearsal locally, live only in the acceptance run)")
	dlsiteDSN := flag.String("dlsite-dsn", os.Getenv("KUN_DLSITE_STAGING_DSN"), "OPTIONAL dlsite staging DSN (genre_taxonomy) — enriches dlsite names with ja originals")

	out := flag.String("out", "", "propose: verdict JSONL output path")
	in := flag.String("in", "", "review: verdict JSONL input path (propose --out)")
	md := flag.String("md", "", "review: human markdown review file output path")
	decisions := flag.String("decisions", "", "review: machine decisions JSONL output; apply: decisions JSONL input")

	apply := flag.Bool("apply", false, "apply: write changes (default dry)")
	singleThreshold := flag.Int("single-threshold", 100, "propose: single-source usage admission gate (ruling 4)")
	workers := flag.Int("workers", 1, "propose: LLM call concurrency")
	maxPairs := flag.Int("max-pairs", 0, "propose: blocking budget cap (0 = pinned default)")
	maxEdit := flag.Int("max-edit", 0, "propose: max Levenshtein for edit-distance blocking (0 = pinned default 1)")
	prior := flag.String("prior", "", "propose: OPTIONAL previous wave's verdict/decisions JSONL — already-judged pairs are skipped (doc 90 ruling 5)")
	highFloor := flag.Float64("high-floor", 0.90, "review: auto-pass confidence floor")
	mediumFloor := flag.Float64("medium-floor", 0.60, "review: drop-below confidence floor")
	spotCheck := flag.Int("spot-check", 30, "review: high-confidence spot-check sample size")

	model := flag.String("model", envOr("KUN_TAG_PAIR_LLM_MODEL", envOr("KUN_AI_UPSTREAM_MODEL", "glm-5.2")), "served model id")
	llmBase := flag.String("llm-base", envOr("KUN_TAG_PAIR_LLM_BASE", os.Getenv("KUN_AI_UPSTREAM_BASE_URL")), "OpenAI-compatible gateway base URL (…/v1)")
	llmToken := flag.String("llm-token", envOr("KUN_TAG_PAIR_LLM_TOKEN", os.Getenv("KUN_AI_UPSTREAM_TOKEN")), "gateway bearer token")
	maxTokens := flag.Int("max-tokens", 1024, "LLM max_tokens per judgment")
	tagMapPath := flag.String("tagmap", os.Getenv("KUN_VNDB_TAGMAP_PATH"), "propose: docs/tagMap.ts override (vndb EN originals)")
	mock := flag.Bool("mock", false, "propose: REHEARSAL ONLY offline deterministic matcher (no network)")
	flag.Parse()

	logger.Init("development")
	ctx := context.Background()

	switch *mode {
	case "propose":
		mt := selectMatcher(*apply, *mock, *llmBase, *llmToken, *model, *maxTokens)
		st, err := tagcanon.Propose(ctx, mt, tagcanon.ProposeOpts{
			DSN: *dsn, DlsiteDSN: *dlsiteDSN, TagMapPath: *tagMapPath,
			SingleThreshold: *singleThreshold, Out: *out,
			Workers: *workers, MaxPairs: *maxPairs, MaxEdit: *maxEdit, Prior: *prior,
		})
		must(err)
		fmt.Printf("\n=== propose ===\npairs=%d singles=%d skipped_prior=%d errors=%d\n", st.Pairs, st.SingleProposed, st.SkippedPrior, st.Errors)
		fmt.Printf("blocking: pool_by_source=%v substring=%d edit=%d cooccur=%d capped=%v\n",
			st.Block.PoolBySource, st.Block.Substring, st.Block.Edit, st.Block.Cooccur, st.Block.Capped)
		fmt.Printf("relations: %v\n", st.RelationCounts)
		if st.Errors > 0 {
			os.Exit(1)
		}

	case "review":
		st, err := tagcanon.MakeReview(*in, *md, *decisions, tagcanon.ReviewOpts{
			HighFloor: *highFloor, MediumFloor: *mediumFloor, SpotCheck: *spotCheck,
		})
		must(err)
		fmt.Printf("\n=== review ===\nhigh(pairs=%d singles=%d) medium(pairs=%d singles=%d) low(pairs=%d singles=%d)\n",
			st.HighExact, st.SingleHigh, st.MediumExact, st.SingleMedium, st.LowExact, st.SingleLow)
		fmt.Printf("non-exact(留档): %v\napproved=%d md=%s decisions=%s\n", st.NonExact, st.Approved, *md, *decisions)

	case "apply":
		st, err := tagcanon.ApplyReviewed(ctx, tagcanon.ApplyReviewedOpts{
			DSN: *dsn, Decisions: *decisions, Apply: *apply,
		})
		must(err)
		fmt.Printf("\n=== apply-reviewed %s ===\napproved_pairs=%d groups=%d single_rows=%d\n",
			modeLabel(*apply), st.ApprovedPairs, st.Groups, st.SingleRows)
		if *apply {
			fmt.Printf("tags_created=%d tags_conflict=%d maps_created=%d maps_conflict=%d tier_updated=%d errors=%d\n",
				st.TagsCreated, st.TagsConflict, st.MapsCreated, st.MapsConflict, st.TierUpdated, st.Errors)
		} else {
			fmt.Printf("DRY RUN — nothing written; re-run with --apply\n")
		}
		if st.Errors > 0 {
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "usage: -mode propose|review|apply (see cmd/tag-canon-pair doc)\n")
		os.Exit(2)
	}
}

func selectMatcher(_ bool, mock bool, base, token, model string, maxTokens int) tagcanon.Matcher {
	if mock {
		slog.Warn("MOCK matcher active — rehearsal only; verdicts are NOT real judgments")
		return tagcanon.MockMatcher{Model: model}
	}
	ht := tagcanon.NewHTTPMatcher(base, token, model, maxTokens)
	if !ht.Configured() {
		fmt.Printf("BLOCKED: LLM gateway not configured (need --llm-base + --llm-token, or KUN_TAG_PAIR_LLM_* / KUN_AI_UPSTREAM_*).\n" +
			"This is a designed precondition for a real propose, not a failure. Use --mock for the offline rehearsal.\n")
		os.Exit(3)
	}
	slog.Info("live gateway matcher", "base", base, "model", model)
	return ht
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func modeLabel(apply bool) string {
	if apply {
		return "APPLY"
	}
	return "DRY"
}

func must(err error) {
	if err != nil {
		slog.Error("tag-canon-pair failed", "error", err)
		os.Exit(1)
	}
}
