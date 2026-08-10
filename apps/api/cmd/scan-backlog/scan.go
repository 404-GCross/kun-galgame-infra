package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"api/internal/platform/ai/upstream"
)

const (
	scoreMaxTokens    = 256
	retryBudget       = 1
	histogramBuckets  = 10
	worklistTextRunes = 200
	scanBufMax        = 16 << 20
)

type scanConfig struct {
	baseURL string
	token   string
	model   string
	workers int
	limit   int
	outPath string
	topN    int
}

type scorer interface {
	ChatJSON(ctx context.Context, system, user string, maxTokens int) (upstream.ChatResult, error)
}

type inputRecord struct {
	ID   string `json:"id"`
	Site string `json:"site"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type verdict struct {
	Flagged    bool     `json:"flagged"`
	Categories []string `json:"categories"`
	Score      *float32 `json:"score"`
}

type scoredRow struct {
	ID         string   `json:"id"`
	Site       string   `json:"site"`
	Kind       string   `json:"kind"`
	Flagged    bool     `json:"flagged"`
	Score      *float32 `json:"score"`
	Categories []string `json:"categories"`
}

type errorRow struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

type worklistItem struct {
	ID         string   `json:"id"`
	Site       string   `json:"site"`
	Score      *float32 `json:"score"`
	Categories []string `json:"categories"`
	Text       string   `json:"text"`
}

type inputStats struct {
	valid       int
	badLines    int
	invalidUTF8 int
}

type runResult struct {
	input         inputStats
	enqueued      int
	succeeded     int
	failed        int
	skippedResume int
	dupInput      int
	allScored     []scoredRow
}

func run(ctx context.Context, cfg scanConfig, client scorer, input io.Reader, stdout io.Writer) (runResult, error) {
	records, inStats, err := readInput(input, cfg.limit)
	if err != nil {
		return runResult{}, err
	}
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

	res.allScored = append(resume.prevScored, thisRun...)
	writeSummary(stdout, res)

	worklistPath := worklistPathFor(cfg.outPath)
	if err := writeWorklist(worklistPath, res.allScored, textByID, cfg.topN); err != nil {
		return res, err
	}
	fmt.Fprintf(stdout, "\nworklist (top %d) -> %s\n", cfg.topN, worklistPath)
	return res, nil
}
