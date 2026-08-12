package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"api/internal/infrastructure/database"

	"gorm.io/gorm"
)

type gradeOptions struct {
	DSN         string
	BaseURL     string
	Variant     string
	Limit       int
	Batch       int
	Concurrency int
	Apply       bool
	MaxNeurons  float64
	GuardDSN    string
	GuardShare  float64
	Client      *moondreamClient
}

// The rungs are ordered most severe first and the level is the first "yes" —
// a grade therefore never depends on a tuned threshold, only on this ordering.
// Deliberately absent: any question about the subject's age. moondream answers
// one, but a control question ("what age does the main character look?") came
// back "child" for 20 of 30 images omni scored clean — it reads art style, not
// age, so a stored minors verdict would be a confident wrong number.
var gradeLadder = []struct {
	Key      string
	Level    int
	Question string
}{
	{"act", 3, "Does this image depict a sexual act? Answer yes or no."},
	{"nude", 2, "Does this image show a nude or partially nude person (exposed breasts, buttocks or genitals)? Answer yes or no."},
	{"underwear", 1, "Is anyone in this image wearing only underwear, lingerie or a swimsuit, or less? Answer yes or no."},
}

type gradeLabels struct {
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Level    int             `json:"level"`
	Answers  map[string]bool `json:"answers"`
	At       time.Time       `json:"at"`
}

func runGrade(ctx context.Context, o gradeOptions, w io.Writer) error {
	if o.DSN == "" {
		return fmt.Errorf("--dsn is required")
	}
	if o.BaseURL == "" {
		return fmt.Errorf("--base-url is required")
	}
	if !o.Client.configured() {
		return fmt.Errorf("moondream client not configured (need --cf-account and --cf-token)")
	}
	if o.Batch <= 0 {
		o.Batch = 2000
	}

	db, err := database.OpenJob(o.DSN)
	if err != nil {
		return err
	}
	if sqlDB, err := db.DB(); err == nil {
		defer sqlDB.Close()
	}

	var remaining int64
	if err := db.WithContext(ctx).
		Raw(`SELECT count(*) FROM images WHERE review_labels -> 'grade' IS NULL`).
		Scan(&remaining).Error; err != nil {
		return fmt.Errorf("count pending: %w", err)
	}
	estimate := float64(remaining) * float64(len(gradeLadder)) * 41.6 * 0.011 / 1000
	fmt.Fprintf(w, "pending=%d apply=%v concurrency=%d questions=%d estimated_usd=%.2f max_neurons=%.0f\n",
		remaining, o.Apply, o.Concurrency, len(gradeLadder), estimate, o.MaxNeurons)

	ctx, abort := context.WithCancelCause(ctx)
	stopGuard, err := startUsageGuard(ctx, o, abort, w)
	if err != nil {
		abort(nil)
		return err
	}
	// The guard only unblocks once ctx is cancelled, so cancelling has to happen
	// inside the same deferred call — two separate defers run LIFO and hang.
	defer func() {
		abort(nil)
		stopGuard()
	}()

	fetch := &http.Client{Timeout: 60 * time.Second}
	var (
		mu      sync.Mutex
		ok, bad int
		started = time.Now()
	)
	work := make(chan imageRow)
	var wg sync.WaitGroup
	for i := 0; i < o.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range work {
				err := gradeOne(ctx, db, o, fetch, r)
				mu.Lock()
				if err != nil {
					bad++
					if bad%20 == 1 {
						fmt.Fprintf(w, "error hash=%s: %v\n", r.Hash, err)
					}
				} else {
					ok++
				}
				if n := ok + bad; n%500 == 0 {
					rate := float64(n) / time.Since(started).Seconds()
					fmt.Fprintf(w, "progress done=%d errors=%d rate=%.2f img/s neurons=%.0f usd=%.2f\n",
						ok, bad, rate, o.Client.neurons(), o.Client.neurons()*0.011/1000)
				}
				mu.Unlock()
			}
		}()
	}

	cursor := ""
	processed := 0
feed:
	for {
		if o.MaxNeurons > 0 && o.Client.neurons() >= o.MaxNeurons {
			fmt.Fprintf(w, "neuron budget reached (%.0f) — stopping feed\n", o.Client.neurons())
			break
		}
		var rows []imageRow
		if err := db.WithContext(ctx).
			Raw(`SELECT hash, ext, width, height, size_bytes, variants::text AS variants
			       FROM images
			      WHERE review_labels -> 'grade' IS NULL AND hash > ?
			      ORDER BY hash
			      LIMIT ?`, cursor, o.Batch).
			Scan(&rows).Error; err != nil {
			close(work)
			wg.Wait()
			return fmt.Errorf("page images: %w", err)
		}
		if len(rows) == 0 {
			break
		}
		cursor = rows[len(rows)-1].Hash

		for _, r := range rows {
			if o.Limit > 0 && processed >= o.Limit {
				break feed
			}
			if o.MaxNeurons > 0 && o.Client.neurons() >= o.MaxNeurons {
				break feed
			}
			select {
			case work <- r:
				processed++
			case <-ctx.Done():
				break feed
			}
		}
	}
	close(work)
	wg.Wait()

	fmt.Fprintf(w, "grade complete done=%d errors=%d neurons=%.0f usd=%.2f elapsed=%s\n",
		ok, bad, o.Client.neurons(), o.Client.neurons()*0.011/1000, time.Since(started).Truncate(time.Second))
	if cause := context.Cause(ctx); cause != nil && cause != context.Canceled {
		return cause
	}
	return nil
}

func gradeOne(ctx context.Context, db *gorm.DB, o gradeOptions, fetch *http.Client, r imageRow) error {
	body, err := fetchImage(ctx, fetch, publicURL(o.BaseURL, r, o.Variant))
	if err != nil {
		return err
	}
	uri := dataURI("image/"+r.Ext, body)

	answers := make(map[string]bool, len(gradeLadder))
	level := 0
	for _, rung := range gradeLadder {
		yes, err := o.Client.askYesNo(ctx, uri, rung.Question)
		if err != nil {
			return fmt.Errorf("%s: %w", rung.Key, err)
		}
		answers[rung.Key] = yes
		if yes && rung.Level > level {
			level = rung.Level
		}
	}

	raw, err := json.Marshal(gradeLabels{
		Provider: "cloudflare-workers-ai",
		Model:    o.Client.model,
		Level:    level,
		Answers:  answers,
		At:       time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	if !o.Apply {
		return nil
	}
	return db.WithContext(ctx).Exec(
		`UPDATE images
		    SET review_labels = jsonb_set(coalesce(review_labels, '{}'::jsonb), '{grade}', ?::jsonb)
		  WHERE hash = ?`, string(raw), r.Hash).Error
}

func fetchImage(ctx context.Context, c *http.Client, url string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.Do(req)
		if err != nil {
			lastErr = err
		} else {
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, 24<<20))
			resp.Body.Close()
			switch {
			case resp.StatusCode == http.StatusOK && readErr == nil:
				return body, nil
			case resp.StatusCode == http.StatusNotFound:
				return nil, fmt.Errorf("fetch %s: http 404", url)
			case readErr != nil:
				lastErr = readErr
			default:
				lastErr = fmt.Errorf("fetch %s: http %d", url, resp.StatusCode)
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(1<<attempt) * time.Second):
		}
	}
	return nil, lastErr
}

// The Workers AI token is the one prod cmd/ai runs on, and Workers AI limits are
// account-scoped: a batch large enough to starve the account shows up first as
// failures on the live route, not here. In 2026-08 a moderation batch on a shared
// token drove the live text gate to 58% fail-open before anyone noticed, so the
// trip condition is measured on the victim, not on this job's own error count.
func startUsageGuard(ctx context.Context, o gradeOptions, abort context.CancelCauseFunc, w io.Writer) (func(), error) {
	if o.GuardDSN == "" {
		fmt.Fprintln(w, "usage guard DISABLED (no --guard-dsn)")
		return func() {}, nil
	}
	guardDB, err := database.OpenJob(o.GuardDSN)
	if err != nil {
		return nil, fmt.Errorf("open guard db: %w", err)
	}
	closeDB := func() {
		if sqlDB, err := guardDB.DB(); err == nil {
			sqlDB.Close()
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			var row struct {
				Total  int64 `gorm:"column:total"`
				Failed int64 `gorm:"column:failed"`
			}
			if err := guardDB.WithContext(ctx).Raw(
				`SELECT count(*) AS total, count(*) FILTER (WHERE status = 1) AS failed
				   FROM ai_usage
				  WHERE created_at > now() - interval '5 minutes'`).Scan(&row).Error; err != nil {
				fmt.Fprintf(w, "usage guard query failed: %v\n", err)
				continue
			}
			if row.Total < 50 {
				continue
			}
			share := float64(row.Failed) / float64(row.Total)
			if share > o.GuardShare {
				fmt.Fprintf(w, "usage guard TRIPPED: ai_usage failure share %.1f%% over %d calls (limit %.1f%%)\n",
					share*100, row.Total, o.GuardShare*100)
				abort(fmt.Errorf("usage guard tripped: live ai_usage failure share %.1f%%", share*100))
				return
			}
		}
	}()
	return func() { <-done; closeDB() }, nil
}
