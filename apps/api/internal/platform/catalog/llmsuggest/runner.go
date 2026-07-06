package llmsuggest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"gorm.io/gorm"
)

// verdictResult is the parsed same/different/unsure judgment shared by the
// name-pair and bid-identity tasks.
type verdictResult struct {
	Verdict    string  `json:"verdict"`
	Reason     string  `json:"reason"`
	Confidence float64 `json:"confidence"`
}

// verdictSchema constrains the model to a same/different/unsure verdict with a
// short reason and a self-reported confidence (archived for sorting only).
var verdictSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"verdict":    map[string]any{"type": "string", "enum": []string{VerdictSame, VerdictDifferent, VerdictUnsure}},
		"reason":     map[string]any{"type": "string"},
		"confidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
	},
	"required":             []string{"verdict", "reason", "confidence"},
	"additionalProperties": false,
}

// judge runs one verdict call with up to two retries (3 attempts total). A
// schema-invalid or unparseable reply is retried; the final error is returned
// so the caller can record an error row without aborting the batch.
func judge(ctx context.Context, c *Client, system, user string, maxTokens int) (verdictResult, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		res, err := c.ChatJSON(ctx, system, user, "verdict", verdictSchema, maxTokens)
		if err != nil {
			lastErr = err
			continue
		}
		var v verdictResult
		if err := json.Unmarshal([]byte(res.Content), &v); err != nil {
			lastErr = fmt.Errorf("unmarshal verdict: %w (raw: %s)", err, truncate(res.Content, 200))
			continue
		}
		switch v.Verdict {
		case VerdictSame, VerdictDifferent, VerdictUnsure:
			return v, nil
		default:
			lastErr = fmt.Errorf("verdict out of enum: %q", v.Verdict)
		}
	}
	return verdictResult{}, lastErr
}

// batchSchema constrains a batched name-pair reply to one result per pair,
// each tagged with its 1-based index.
var batchSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"results": map[string]any{"type": "array", "items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"index":      map[string]any{"type": "integer"},
				"verdict":    map[string]any{"type": "string", "enum": []string{VerdictSame, VerdictDifferent, VerdictUnsure}},
				"reason":     map[string]any{"type": "string"},
				"confidence": map[string]any{"type": "number"},
			},
			"required":             []string{"index", "verdict", "reason", "confidence"},
			"additionalProperties": false,
		}},
	},
	"required":             []string{"results"},
	"additionalProperties": false,
}

// judgeBatch judges several name pairs in one request and returns verdicts
// keyed by 1-based index. Missing / out-of-enum entries are simply absent from
// the map (the caller records those as errors).
func judgeBatch(ctx context.Context, c *Client, system, user string, n int) (map[int]verdictResult, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		res, err := c.ChatJSON(ctx, system, user, "batch", batchSchema, 120+n*90)
		if err != nil {
			lastErr = err
			continue
		}
		var parsed struct {
			Results []struct {
				Index int `json:"index"`
				verdictResult
			} `json:"results"`
		}
		if err := json.Unmarshal([]byte(res.Content), &parsed); err != nil {
			lastErr = fmt.Errorf("unmarshal batch: %w", err)
			continue
		}
		out := map[int]verdictResult{}
		for _, r := range parsed.Results {
			switch r.Verdict {
			case VerdictSame, VerdictDifferent, VerdictUnsure:
				out[r.Index] = r.verdictResult
			}
		}
		return out, nil
	}
	return nil, lastErr
}

// runPool executes fn over items with bounded concurrency. fn is responsible
// for its own persistence and error recording; a panic in fn is not caught, a
// returned error is ignored (tasks record errors as rows, not returns).
func runPool[T any](ctx context.Context, items []T, concurrency int, fn func(context.Context, T)) {
	if concurrency < 1 {
		concurrency = 1
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, it := range items {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(item T) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(ctx, item)
		}(it)
	}
	wg.Wait()
}

// loadDoneHashes preloads the resume set: input hashes already recorded for
// (model, prompt_version) in the given table, optionally scoped by task.
func loadDoneHashes(db *gorm.DB, table, model, promptVersion, taskCol, task string) (map[string]bool, error) {
	q := db.Table(table).Where("model = ? AND prompt_version = ?", model, promptVersion)
	if taskCol != "" {
		q = q.Where(taskCol+" = ?", task)
	}
	var hashes []string
	if err := q.Pluck("input_hash", &hashes).Error; err != nil {
		return nil, err
	}
	done := make(map[string]bool, len(hashes))
	for _, h := range hashes {
		done[h] = true
	}
	return done, nil
}

// recordRun writes the run-metadata row.
func recordRun(db *gorm.DB, task, model, promptVersion string, counts any, startedAt any, notes string) error {
	cj, _ := json.Marshal(counts)
	return db.Exec(
		`INSERT INTO src_llm.run (task, model, prompt_version, counts, notes, started_at) VALUES (?,?,?,?::jsonb,?,?)`,
		task, model, promptVersion, string(cj), notes, startedAt,
	).Error
}
