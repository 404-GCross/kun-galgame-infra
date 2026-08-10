package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"api/internal/infrastructure/database"

	"gorm.io/gorm"
)

type backfillOptions struct {
	DSN         string
	BaseURL     string
	Variant     string
	Limit       int
	Batch       int
	Concurrency int
	QPS         float64
	Apply       bool
	Client      *omniImageClient
}

type autoLabels struct {
	Provider string             `json:"provider"`
	Channel  string             `json:"channel"`
	Flagged  bool               `json:"flagged"`
	Scores   map[string]float64 `json:"scores"`
	At       time.Time          `json:"at"`
}

func runBackfill(ctx context.Context, o backfillOptions, w io.Writer) error {
	if o.DSN == "" {
		return fmt.Errorf("--dsn is required")
	}
	if o.BaseURL == "" {
		return fmt.Errorf("--base-url is required")
	}
	if !o.Client.configured() {
		return fmt.Errorf("moderations client not configured (need --omni-base and --omni-token)")
	}
	if o.Batch <= 0 {
		o.Batch = 2000
	}

	db, err := database.OpenJob(o.DSN)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err == nil {
		defer sqlDB.Close()
	}

	var remaining int64
	if err := db.WithContext(ctx).
		Raw(`SELECT count(*) FROM images WHERE review_labels -> 'auto' IS NULL`).
		Scan(&remaining).Error; err != nil {
		return fmt.Errorf("count pending: %w", err)
	}
	fmt.Fprintf(w, "pending=%d apply=%v concurrency=%d\n", remaining, o.Apply, o.Concurrency)

	interval := time.Duration(float64(time.Second) / o.QPS)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

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
				err := backfillOne(ctx, db, o, r)
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
					fmt.Fprintf(w, "progress done=%d errors=%d rate=%.2f/s\n", ok, bad, rate)
				}
				mu.Unlock()
			}
		}()
	}

	cursor := ""
	processed := 0
feed:
	for {
		var rows []imageRow
		if err := db.WithContext(ctx).
			Raw(`SELECT hash, ext, width, height, size_bytes, variants::text AS variants
			       FROM images
			      WHERE review_labels -> 'auto' IS NULL AND hash > ?
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
			select {
			case <-ctx.Done():
				break feed
			case <-ticker.C:
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

	fmt.Fprintf(w, "backfill complete done=%d errors=%d elapsed=%s\n", ok, bad, time.Since(started).Truncate(time.Second))
	return nil
}

func backfillOne(ctx context.Context, db *gorm.DB, o backfillOptions, r imageRow) error {
	url := publicURL(o.BaseURL, r, o.Variant)
	v, err := moderateWithRetry(ctx, o.Client, url)
	if err != nil {
		return err
	}

	scores := make(map[string]float64, len(imageScoredCategories))
	for cat, s := range v.Scores {
		if imageScoredCategories[cat] {
			scores[cat] = s
		}
	}
	raw, err := json.Marshal(autoLabels{
		Provider: "openai-moderations",
		Channel:  v.Channel,
		Flagged:  v.Flagged,
		Scores:   scores,
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
		    SET review_labels = jsonb_set(coalesce(review_labels, '{}'::jsonb), '{auto}', ?::jsonb)
		  WHERE hash = ?`, string(raw), r.Hash).Error
}
