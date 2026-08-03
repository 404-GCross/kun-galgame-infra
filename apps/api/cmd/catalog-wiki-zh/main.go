// catalog-wiki-zh adjudicates the retired wiki's hand-written Chinese intros
// against what the catalog holds today, and restores the ones that are better
// (refs/proj/168). Quality is the criterion, not who wrote it.
//
// Two phases, deliberately separate files on disk between them so a judgement
// can be inspected — and a human can overrule it — before anything is written:
//
//	# 1. judge: read src_wiki.intro_snapshot, write JSONL verdicts. Resumable.
//	catalog-wiki-zh judge --bucket usable  --out verdicts-usable.jsonl  --limit 30
//	catalog-wiki-zh judge --bucket compare --out verdicts-compare.jsonl --limit 30
//
//	# 2. apply: fold N independent rounds and consume the consensus. Dry by
//	#    default. Auto-apply needs EVERY round to agree and clear the gate.
//	catalog-wiki-zh apply --in r1.jsonl,r2.jsonl,r3.jsonl
//	catalog-wiki-zh apply --in r1.jsonl,r2.jsonl,r3.jsonl --apply
//
// A restore INSERTs a provenance=0 row; the machine row is never touched, so a
// rollback is a DELETE of the ids the apply pass reports.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/jobs/wikizh"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	var (
		bucket    = fs.String("bucket", string(wikizh.BucketUsable), "judge: usable (no zh at all) | compare (a machine row holds the slot)")
		out       = fs.String("out", "", "judge: JSONL verdict file (appended; already-judged keys are skipped)")
		in        = fs.String("in", "", "apply: comma-separated JSONL verdict files — one per INDEPENDENT judging round. Auto-apply requires every round to agree.")
		apply     = fs.Bool("apply", false, "apply: write (default is a dry forecast)")
		limit     = fs.Int("limit", 0, "max candidates (0 = all)")
		chunk     = fs.Int("chunk", 5, "judge: candidates per gateway request")
		rpm       = fs.Int("rpm", 60, "judge: gateway requests per minute (even pacing, shared across workers)")
		workers   = fs.Int("workers", 6, "judge: concurrent chunks; the reasoning model's per-request latency dominates, so this is the throughput lever, while --rpm stays the politeness ceiling")
		maxTokens = fs.Int("max-tokens", 24576, "judge: output budget; a reasoning model needs headroom or finish_reason trips")
		mock      = fs.Bool("mock", false, "judge: offline deterministic stand-in")
		llmBase   = fs.String("llm-base", os.Getenv("KUN_AI_UPSTREAM_BASE_URL"), "OpenAI-compatible base URL")
		llmToken  = fs.String("llm-token", os.Getenv("KUN_AI_UPSTREAM_TOKEN"), "bearer token")
		model     = fs.String("model", envOr("KUN_AI_UPSTREAM_MODEL", "@cf/zai-org/glm-5.2"), "model id")
	)
	_ = fs.Parse(os.Args[2:])

	_ = godotenv.Load("apps/api/.env")
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	db, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		slog.Error("catalog db connect", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	ctx := context.Background()

	switch cmd {
	case "judge":
		if *out == "" {
			slog.Error("--out is required")
			os.Exit(2)
		}
		b := wikizh.Bucket(*bucket)
		cands, err := wikizh.LoadCandidates(ctx, db.DB(), b, *limit)
		if err != nil {
			slog.Error("load candidates", "error", err)
			os.Exit(1)
		}
		done, err := wikizh.LoadVerdictKeys(*out)
		if err != nil {
			slog.Error("read existing verdicts", "error", err)
			os.Exit(1)
		}
		pending := cands[:0]
		for _, c := range cands {
			if !done[c.Key()] {
				pending = append(pending, c)
			}
		}
		slog.Info("wiki-zh judge", "bucket", b, "candidates", len(cands),
			"already_judged", len(done), "pending", len(pending))

		var judge wikizh.Judge
		if *mock {
			judge = wikizh.MockJudge{}
			slog.Warn("MOCK judge — verdicts are NOT real judgements")
		} else {
			hj := wikizh.NewHTTPJudge(*llmBase, *llmToken, *model, *maxTokens, *rpm)
			if !hj.Configured() {
				fmt.Println("BLOCKED: LLM gateway not configured (need --llm-base + --llm-token or KUN_AI_UPSTREAM_*).")
				os.Exit(3)
			}
			judge = hj
			slog.Info("live gateway judge", "base", *llmBase, "model", *model)
		}

		f, err := os.OpenFile(*out, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			slog.Error("open verdict file", "error", err)
			os.Exit(1)
		}
		defer f.Close()
		enc := json.NewEncoder(f)

		// Chunks run concurrently: a reasoning model spends ~15s on a chunk of
		// five, so a serial loop leaves 89% of the rpm budget unused and turns
		// a three-round pass into half a day. The shared pace limiter inside
		// the judge remains the politeness ceiling regardless of worker count.
		type chunkJob struct{ from, to int }
		jobs := make(chan chunkJob)
		var wg sync.WaitGroup
		var mu sync.Mutex // guards the encoder and the counters
		var judged, failed int

		for w := 0; w < max(*workers, 1); w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range jobs {
					vs, err := judge.JudgeBatch(ctx, b, pending[j.from:j.to])
					mu.Lock()
					if err != nil {
						// One failed chunk is not a failed pass — the file is
						// the resume point, so a re-run picks these up.
						failed += j.to - j.from
						slog.Warn("chunk failed", "from", j.from, "to", j.to, "err", err)
					} else {
						for _, v := range vs {
							if e := enc.Encode(v); e != nil {
								slog.Error("write verdict", "error", e)
								mu.Unlock()
								os.Exit(1)
							}
							judged++
						}
						if judged%100 < *chunk {
							slog.Info("progress", "judged", judged, "of", len(pending), "failed", failed)
						}
					}
					mu.Unlock()
				}
			}()
		}
		for i := 0; i < len(pending); i += *chunk {
			jobs <- chunkJob{i, min(i+*chunk, len(pending))}
		}
		close(jobs)
		wg.Wait()
		slog.Info("wiki-zh judge done", "judged", judged, "failed", failed, "file", *out)

	case "apply":
		if *in == "" {
			slog.Error("--in is required")
			os.Exit(2)
		}
		files := strings.Split(*in, ",")
		rounds := make([][]wikizh.Verdict, 0, len(files))
		for _, f := range files {
			f = strings.TrimSpace(f)
			if f == "" {
				continue
			}
			r, err := wikizh.LoadVerdicts(f)
			if err != nil {
				slog.Error("read verdicts", "file", f, "error", err)
				os.Exit(1)
			}
			slog.Info("round loaded", "file", f, "verdicts", len(r))
			rounds = append(rounds, r)
		}
		if len(rounds) < 2 {
			slog.Warn("SINGLE ROUND — a lone judging round cannot tell a borderline case from a confident one; " +
				"the wave-168 calibration saw 6 of 15 verdicts move between identical runs")
		}
		vs, cs := wikizh.Consensus(rounds)
		slog.Info("consensus", "result", cs.String())

		st, err := wikizh.Apply(ctx, db.DB(), vs, *apply)
		if st != nil {
			slog.Info("wiki-zh apply done", "apply", *apply, "result", st.String())
			if *apply && len(st.ReceiptIDs) > 0 {
				name := fmt.Sprintf("%s.receipts.%d.json", *in, time.Now().Unix())
				b, _ := json.Marshal(st.ReceiptIDs)
				if err := os.WriteFile(name, b, 0o644); err != nil {
					slog.Warn("write receipts", "error", err)
				} else {
					slog.Info("receipts written", "file", name, "rows", len(st.ReceiptIDs))
				}
			}
		}
		if err != nil {
			slog.Error("apply", "error", err)
			os.Exit(1)
		}

	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `catalog-wiki-zh <judge|apply> [flags]

  judge --bucket usable|compare --out FILE [--limit N] [--chunk 5] [--mock]
        run it N times to N different files — independent rounds are what
        distinguishes a borderline case from a confident one
  apply --in r1.jsonl,r2.jsonl,r3.jsonl [--apply]
        auto-applies only where EVERY round agreed and cleared the gate
`)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
