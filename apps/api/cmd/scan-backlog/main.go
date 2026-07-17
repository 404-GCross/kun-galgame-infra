// cmd/scan-backlog is a purely LOCAL offline batch scorer for the existing UGC
// backlog (spec 10; owner ruling 2026-07-16 — "local LLM pre-filter + prod omni
// re-check"). It is the pre-filter half: a JSONL file goes IN, an OpenAI-
// compatible chat endpoint (a local vLLM, kungal-llm-infra) scores each row with
// the SAME instruction production moderate-text uses, and files go OUT. It makes
// ZERO database access and ZERO production contact — it changes no schema and
// runs no migration.
//
// Usage:
//
//	go run ./cmd/scan-backlog -base-url URL -model ID -out FILE [flags] <input.jsonl>
//
// Flags:
//
//	-base-url  OpenAI-compatible base URL (required, e.g. http://127.0.0.1:8000/v1).
//	-token     bearer token (optional — a local vLLM usually needs none).
//	-model     model id to score with (required).
//	-workers   concurrent scoring workers (default 4).
//	-limit     score only the first N valid records (default 0 = all) — smoke sampling.
//	-out       scored JSONL output path (append-mode, resumable) (required).
//	-top       worklist size: the N highest-scoring items (default 100).
//
// Input is one JSON object per line: {"id","site","kind","text"}. Bad lines and
// non-UTF-8 lines are counted and skipped. Output is append-mode and resumable:
// re-running with the same -out preloads the already-succeeded ids and skips
// them (error rows are retried), so a crashed run resumes idempotently. Three
// artifacts are produced: the -out full scored JSONL, an stdout summary
// (totals/histogram/flagged/top categories), and a top-N worklist file next to
// -out.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"api/internal/platform/ai/upstream"
)

func main() {
	baseURL := flag.String("base-url", "", "OpenAI-compatible base URL (required, e.g. http://127.0.0.1:8000/v1)")
	token := flag.String("token", "", "bearer token (optional; a local vLLM usually needs none)")
	modelID := flag.String("model", "", "model id to score with (required)")
	workers := flag.Int("workers", 4, "concurrent scoring workers")
	limit := flag.Int("limit", 0, "score only the first N valid records (0 = all)")
	out := flag.String("out", "", "scored JSONL output path (append-mode, resumable) (required)")
	topN := flag.Int("top", 100, "worklist size: the N highest-scoring items")
	flag.Parse()

	args := flag.Args()
	if len(args) != 1 || *baseURL == "" || *modelID == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: scan-backlog -base-url URL -model ID -out FILE [flags] <input.jsonl>")
		flag.PrintDefaults()
		os.Exit(2)
	}
	if *workers < 1 {
		fmt.Fprintf(os.Stderr, "invalid -workers %d: must be >= 1\n", *workers)
		os.Exit(2)
	}
	if *topN < 0 {
		fmt.Fprintf(os.Stderr, "invalid -top %d: must be >= 0\n", *topN)
		os.Exit(2)
	}

	inFile, err := os.Open(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "open input %s: %v\n", args[0], err)
		os.Exit(1)
	}
	defer inFile.Close()

	cfg := scanConfig{
		baseURL: *baseURL,
		token:   *token,
		model:   *modelID,
		workers: *workers,
		limit:   *limit,
		outPath: *out,
		topN:    *topN,
	}
	// The production upstream client is reused verbatim, so the request shape is
	// identical to moderate-text. An empty token is fine here: ChatJSON dials
	// regardless (only the higher-level service gates on Configured()).
	client := upstream.NewClient(cfg.baseURL, cfg.token, cfg.model)

	if _, err := run(context.Background(), cfg, client, inFile, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "scan failed: %v\n", err)
		os.Exit(1)
	}
}
