package vndbcovers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

const (
	defaultAPIBase = "https://api.vndb.org/kana"

	vnFields = "id,image.url,image.dims,image.sexual,image.violence"

	apiBatchSize = 25

	apiThrottle = time.Second

	apiRetries = 5

	apiTimeout = 30 * time.Second
)

type vnImage struct {
	URL      string  `json:"url"`
	Dims     []int   `json:"dims"`
	Sexual   float64 `json:"sexual"`
	Violence float64 `json:"violence"`
}

type vnResult struct {
	ID    string   `json:"id"`
	Image *vnImage `json:"image"`
}

type vnResponse struct {
	Results []vnResult `json:"results"`
	More    bool       `json:"more"`
}

type vnQuery struct {
	Filters any    `json:"filters"`
	Fields  string `json:"fields"`
	Results int    `json:"results"`
}

type vndbAPI struct {
	base string
	http *http.Client
	gap  time.Duration
	last time.Time
}

func newVNDBAPI(base string) *vndbAPI {
	if base == "" {
		base = defaultAPIBase
	}
	return &vndbAPI{base: base, http: &http.Client{Timeout: apiTimeout}, gap: apiThrottle}
}

func (a *vndbAPI) fetchImages(ctx context.Context, ids []string) (map[string]*vnImage, error) {
	out := make(map[string]*vnImage, len(ids))
	for i := 0; i < len(ids); i += apiBatchSize {
		batch := ids[i:min(i+apiBatchSize, len(ids))]
		resp, err := a.queryVN(ctx, batch)
		if err != nil {
			return nil, err
		}
		if resp.More {
			slog.Warn("vndb api returned a truncated page", "batch_size", len(batch))
		}
		for _, r := range resp.Results {
			out[r.ID] = r.Image
		}
	}
	return out, nil
}

func (a *vndbAPI) queryVN(ctx context.Context, ids []string) (*vnResponse, error) {
	body, err := json.Marshal(vnQuery{Filters: idFilter(ids), Fields: vnFields, Results: len(ids)})
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; attempt < apiRetries; attempt++ {
		a.throttle(ctx)
		resp, wait, err := a.post(ctx, body)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if wait <= 0 {
			return nil, err
		}
		slog.Warn("vndb api retry", "attempt", attempt+1, "wait", wait, "err", err)
		if !sleepCtx(ctx, wait) {
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("vndb api gave up after %d attempts: %w", apiRetries, lastErr)
}

func (a *vndbAPI) post(ctx context.Context, body []byte) (*vnResponse, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base+"/vn", bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := a.http.Do(req)
	if err != nil {
		return nil, 5 * time.Second, err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, retryAfter(resp.Header.Get("Retry-After"), 30*time.Second), fmt.Errorf("vndb api 429")
	case resp.StatusCode >= 500:
		return nil, 5 * time.Second, fmt.Errorf("vndb api %d", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, 0, fmt.Errorf("vndb api %d", resp.StatusCode)
	}
	out, err := parseVNResponse(resp.Body)
	if err != nil {
		return nil, 0, err
	}
	return out, 0, nil
}

func (a *vndbAPI) throttle(ctx context.Context) {
	if a.gap <= 0 {
		return
	}
	if wait := a.gap - time.Since(a.last); wait > 0 && !a.last.IsZero() {
		sleepCtx(ctx, wait)
	}
	a.last = time.Now()
}

func retryAfter(h string, def time.Duration) time.Duration {
	if secs, err := strconv.Atoi(h); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return def
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
