package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"api/internal/platform/ai/service"
)

// process runs the worker pool over the queued records. Workers score in
// parallel; a single collector goroutine (this one) appends each result to out
// as it lands and accumulates the successes for the summary basis. It returns
// the success rows and the failure count.
func process(ctx context.Context, cfg scanConfig, client scorer, jobs []inputRecord, out io.Writer) ([]scoredRow, int, error) {
	if len(jobs) == 0 {
		return nil, 0, nil
	}
	workers := cfg.workers
	if workers < 1 {
		workers = 1
	}

	type outcome struct {
		row  *scoredRow
		erow *errorRow
	}
	jobCh := make(chan inputRecord)
	resCh := make(chan outcome, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rec := range jobCh {
				v, err := scoreWithRetry(ctx, client, rec.Text)
				if err != nil {
					resCh <- outcome{erow: &errorRow{ID: rec.ID, Error: err.Error()}}
					continue
				}
				resCh <- outcome{row: &scoredRow{
					ID: rec.ID, Site: rec.Site, Kind: rec.Kind,
					Flagged: v.Flagged, Score: v.Score, Categories: v.Categories,
				}}
			}
		}()
	}

	go func() {
		for _, rec := range jobs {
			jobCh <- rec
		}
		close(jobCh)
	}()
	go func() {
		wg.Wait()
		close(resCh)
	}()

	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	var successes []scoredRow
	failed := 0
	for o := range resCh {
		if o.row != nil {
			if err := enc.Encode(o.row); err != nil {
				return successes, failed, fmt.Errorf("write scored row: %w", err)
			}
			successes = append(successes, *o.row)
			continue
		}
		if err := enc.Encode(o.erow); err != nil {
			return successes, failed, fmt.Errorf("write error row: %w", err)
		}
		failed++
	}
	return successes, failed, nil
}

// scoreWithRetry scores one text, retrying once on any failure (transport OR
// parse). After the retry budget it returns the last error → an error row.
func scoreWithRetry(ctx context.Context, client scorer, text string) (verdict, error) {
	var lastErr error
	for attempt := 0; attempt <= retryBudget; attempt++ {
		v, err := scoreOnce(ctx, client, text)
		if err == nil {
			return v, nil
		}
		lastErr = err
	}
	return verdict{}, lastErr
}

func scoreOnce(ctx context.Context, client scorer, text string) (verdict, error) {
	res, err := client.ChatJSON(ctx, service.ModerateSystemPrompt, text, scoreMaxTokens)
	if err != nil {
		return verdict{}, err
	}
	return parseVerdict(res.Content)
}

// parseVerdict tolerantly parses the upstream JSON verdict, mirroring
// internal/platform/ai/service.parseModeration (copied — the spec sanctions
// exporting only the prompt constant, not the parser). It extracts the first
// {...} span (servers sometimes wrap JSON in prose) then unmarshals the known
// fields, so the output is shape-comparable to production moderate-text.
func parseVerdict(content string) (verdict, error) {
	raw := extractJSON(content)
	var v verdict
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return verdict{}, err
	}
	return v, nil
}

// extractJSON returns the substring from the first '{' to the last '}', or the
// input unchanged when no braces are present. Copied from
// internal/platform/ai/service.extractJSON.
func extractJSON(s string) string {
	i := strings.IndexByte(s, '{')
	j := strings.LastIndexByte(s, '}')
	if i >= 0 && j > i {
		return s[i : j+1]
	}
	return s
}
