package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"api/internal/infrastructure/database"
)

type scanOptions struct {
	DSN         string
	Out         string
	Limit       int
	Salt        string
	BaseURL     string
	Variant     string
	Concurrency int
	QPS         float64
	Client      *omniImageClient
}

type imageRow struct {
	Hash      string `gorm:"column:hash"`
	Ext       string `gorm:"column:ext"`
	Width     int    `gorm:"column:width"`
	Height    int    `gorm:"column:height"`
	SizeBytes int64  `gorm:"column:size_bytes"`
	Variants  string `gorm:"column:variants"`
}

type scanRecord struct {
	Hash      string       `json:"hash"`
	URL       string       `json:"url"`
	Width     int          `json:"width"`
	Height    int          `json:"height"`
	SizeBytes int64        `json:"size_bytes"`
	Verdict   *omniVerdict `json:"verdict,omitempty"`
	Error     string       `json:"error,omitempty"`
	At        time.Time    `json:"at"`
}

func runScan(ctx context.Context, o scanOptions, w io.Writer) error {
	if o.DSN == "" {
		return fmt.Errorf("--dsn is required")
	}
	if o.Out == "" {
		return fmt.Errorf("--out is required")
	}
	if o.BaseURL == "" {
		return fmt.Errorf("--base-url is required")
	}
	if !o.Client.configured() {
		return fmt.Errorf("moderations client not configured (need --omni-base and --omni-token)")
	}

	done, err := loadDone(o.Out)
	if err != nil {
		return err
	}

	db, err := database.OpenJob(o.DSN)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err == nil {
		defer sqlDB.Close()
	}

	var rows []imageRow
	if err := db.WithContext(ctx).
		Raw(`SELECT hash, ext, width, height, size_bytes, variants::text AS variants
		       FROM images
		      ORDER BY md5(hash || ?)
		      LIMIT ?`, o.Salt, o.Limit).
		Scan(&rows).Error; err != nil {
		return fmt.Errorf("sample images: %w", err)
	}

	pending := rows[:0:0]
	for _, r := range rows {
		if !done[r.Hash] {
			pending = append(pending, r)
		}
	}
	fmt.Fprintf(w, "sampled=%d already_scored=%d to_score=%d\n", len(rows), len(rows)-len(pending), len(pending))
	if len(pending) == 0 {
		return nil
	}

	f, err := os.OpenFile(o.Out, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	var (
		mu      sync.Mutex
		enc     = json.NewEncoder(f)
		ok, bad int
	)
	writeRec := func(rec scanRecord) {
		mu.Lock()
		defer mu.Unlock()
		if rec.Error == "" {
			ok++
		} else {
			bad++
		}
		if err := enc.Encode(rec); err != nil {
			fmt.Fprintf(w, "write failed: %v\n", err)
		}
		if n := ok + bad; n%100 == 0 {
			fmt.Fprintf(w, "progress scored=%d errors=%d\n", ok, bad)
		}
	}

	interval := time.Duration(float64(time.Second) / o.QPS)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	work := make(chan imageRow)
	var wg sync.WaitGroup
	for i := 0; i < o.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range work {
				url := publicURL(o.BaseURL, r, o.Variant)
				rec := scanRecord{Hash: r.Hash, URL: url, Width: r.Width, Height: r.Height, SizeBytes: r.SizeBytes, At: time.Now().UTC()}
				v, err := moderateWithRetry(ctx, o.Client, url)
				if err != nil {
					rec.Error = err.Error()
				} else {
					rec.Verdict = v
				}
				writeRec(rec)
			}
		}()
	}

feed:
	for _, r := range pending {
		select {
		case <-ctx.Done():
			break feed
		case <-ticker.C:
		}
		select {
		case work <- r:
		case <-ctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()

	fmt.Fprintf(w, "scan complete scored=%d errors=%d out=%s\n", ok, bad, o.Out)
	return nil
}

func moderateWithRetry(ctx context.Context, c *omniImageClient, url string) (*omniVerdict, error) {
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		v, err := c.moderateImage(ctx, url)
		if err == nil {
			return v, nil
		}
		lastErr = err
		if _, retry := err.(retryable); !retry {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(1<<attempt) * time.Second):
		}
	}
	return nil, lastErr
}

func publicURL(base string, r imageRow, variant string) string {
	key := fmt.Sprintf("%s/%s/%s.%s", r.Hash[:2], r.Hash[2:4], r.Hash, r.Ext)
	if variant != "" && strings.Contains(r.Variants, `"`+variant+`"`) {
		key = fmt.Sprintf("%s/%s/%s_%s.%s", r.Hash[:2], r.Hash[2:4], r.Hash, variant, r.Ext)
	}
	return strings.TrimRight(base, "/") + "/" + key
}

func loadDone(path string) (map[string]bool, error) {
	done := map[string]bool{}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return done, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec scanRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.Error == "" {
			done[rec.Hash] = true
		}
	}
	return done, sc.Err()
}
