package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"api/internal/platform/ai/upstream"
)

const (
	// scoreMaxTokens mirrors ai/service moderateMaxTokens: the verdict JSON is
	// tiny, so a small cap is plenty.
	scoreMaxTokens = 256
	// retryBudget is the per-item retry budget: 1 retry = 2 attempts total
	// (spec ruling 3).
	retryBudget = 1
	// histogramBuckets is the fixed 10-bucket [0,0.1)…[0.9,1.0] score histogram.
	histogramBuckets = 10
	// worklistTextRunes caps the worklist text preview (spec ruling 5).
	worklistTextRunes = 200
	// scanBufMax is the max input line length (long forum topics need a big cap).
	scanBufMax = 16 << 20
)

// scanConfig is the resolved run configuration (the parsed CLI flags).
type scanConfig struct {
	baseURL string
	token   string
	model   string
	workers int
	limit   int
	outPath string
	topN    int
}

// scorer is the upstream seam (satisfied by *upstream.Client; the tests point a
// real client at an httptest stub). Reusing the production client keeps the
// request shape identical to moderate-text.
type scorer interface {
	ChatJSON(ctx context.Context, system, user string, maxTokens int) (upstream.ChatResult, error)
}

// inputRecord is one line of the input JSONL. The export is each site's ad-hoc
// SQL, not this tool's concern (spec ruling 1).
type inputRecord struct {
	ID   string `json:"id"`
	Site string `json:"site"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// verdict mirrors the parsed moderation reply of
// internal/platform/ai/service.parseModeration — the SAME shape {flagged,
// categories, score} so the calibration set stays comparable to production
// moderate-text output (spec ruling 2). Copied (not exported) because the spec
// sanctions exporting only the prompt constant from ai/service.
type verdict struct {
	Flagged    bool     `json:"flagged"`
	Categories []string `json:"categories"`
	Score      *float32 `json:"score"`
}

// scoredRow is a success line of the -out JSONL.
type scoredRow struct {
	ID         string   `json:"id"`
	Site       string   `json:"site"`
	Kind       string   `json:"kind"`
	Flagged    bool     `json:"flagged"`
	Score      *float32 `json:"score"`
	Categories []string `json:"categories"`
}

// errorRow is a failure line of the -out JSONL (written after the retry budget).
type errorRow struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

// worklistItem is one line of the top-N worklist file.
type worklistItem struct {
	ID         string   `json:"id"`
	Site       string   `json:"site"`
	Score      *float32 `json:"score"`
	Categories []string `json:"categories"`
	Text       string   `json:"text"`
}

// inputStats counts input-hygiene outcomes while reading the JSONL.
type inputStats struct {
	valid       int
	badLines    int
	invalidUTF8 int
}

// runResult is the summary of one scan invocation: this run's counters plus the
// merged basis (this run ∪ resumed) for the histogram/top.
type runResult struct {
	input         inputStats
	enqueued      int
	succeeded     int
	failed        int
	skippedResume int
	dupInput      int
	allScored     []scoredRow
}

// run is the whole scan: read input → preload resume state → score the not-yet-
// done records over a worker pool (appending each result durably) → merge the
// resumed and fresh scores → print the summary and write the top-N worklist.
func run(ctx context.Context, cfg scanConfig, client scorer, input io.Reader, stdout io.Writer) (runResult, error) {
	records, inStats, err := readInput(input, cfg.limit)
	if err != nil {
		return runResult{}, err
	}
	// The worklist needs the text of any top-N id (scored this run OR resumed), so
	// index every valid input row's text by id.
	textByID := make(map[string]string, len(records))
	for _, r := range records {
		if _, ok := textByID[r.ID]; !ok {
			textByID[r.ID] = r.Text
		}
	}

	resume, err := loadResume(cfg.outPath)
	if err != nil {
		return runResult{}, err
	}

	// Build the work queue: skip ids already succeeded (resume) and in-input dup
	// ids (score each id once per run).
	res := runResult{input: inStats}
	enqueued := make([]inputRecord, 0, len(records))
	seen := make(map[string]struct{}, len(records))
	for _, r := range records {
		if _, done := resume.done[r.ID]; done {
			res.skippedResume++
			continue
		}
		if _, dup := seen[r.ID]; dup {
			res.dupInput++
			continue
		}
		seen[r.ID] = struct{}{}
		enqueued = append(enqueued, r)
	}
	res.enqueued = len(enqueued)

	// Append mode: each result is durably written as it completes (crash-safe
	// resume). The file is created if absent.
	out, err := os.OpenFile(cfg.outPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return res, fmt.Errorf("open out %s: %w", cfg.outPath, err)
	}
	thisRun, failed, procErr := process(ctx, cfg, client, enqueued, out)
	closeErr := out.Close()
	if procErr != nil {
		return res, procErr
	}
	if closeErr != nil {
		return res, fmt.Errorf("close out: %w", closeErr)
	}
	res.succeeded = len(thisRun)
	res.failed = failed

	// Summary + worklist are over the WHOLE known backlog (resumed ∪ this run),
	// not just this run, so the top-N reflects everything scored so far.
	res.allScored = append(resume.prevScored, thisRun...)
	writeSummary(stdout, res)

	worklistPath := worklistPathFor(cfg.outPath)
	if err := writeWorklist(worklistPath, res.allScored, textByID, cfg.topN); err != nil {
		return res, err
	}
	fmt.Fprintf(stdout, "\nworklist (top %d) -> %s\n", cfg.topN, worklistPath)
	return res, nil
}
