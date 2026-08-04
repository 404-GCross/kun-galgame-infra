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
	// defaultAPIBase is the official VNDB HTTPS API root. /vn is a public
	// endpoint — no token, no auth header.
	defaultAPIBase = "https://api.vndb.org/kana"

	// vnFields asks for exactly what a cover row needs: the vn id to key the
	// answer back onto its anchor, the image URL to fetch, its pixel dims to
	// decide the portrait pin, and its two content-rating axes.
	vnFields = "id,image.url,image.dims,image.sexual,image.violence"

	// apiBatchSize keeps each POST small and its `or` filter readable. The
	// endpoint allows up to 100 results per page; 25 is polite and means a
	// single failed batch costs almost nothing to retry.
	apiBatchSize = 25

	// apiThrottle is the minimum spacing between API calls (~1 req/s), well
	// under VNDB's published budget. The whole 72-id run is three requests.
	apiThrottle = time.Second

	// apiRetries bounds the retries of a 429 / 5xx / transport failure.
	apiRetries = 5

	// apiTimeout bounds one API round-trip.
	apiTimeout = 30 * time.Second
)

// vnImage mirrors the `image` object of a /vn result. Sexual and Violence are
// VNDB's averaged 0-2 votes and are therefore FRACTIONAL (0.8, 1.34); they are
// rounded onto the catalog's integer scale by ratingLevel.
type vnImage struct {
	URL      string  `json:"url"`
	Dims     []int   `json:"dims"` // [width, height]
	Sexual   float64 `json:"sexual"`
	Violence float64 `json:"violence"`
}

// vnResult is one /vn row. Image is a pointer because VNDB returns null for a
// vn with no cover.
type vnResult struct {
	ID    string   `json:"id"`
	Image *vnImage `json:"image"`
}

type vnResponse struct {
	Results []vnResult `json:"results"`
	More    bool       `json:"more"`
}

// vnQuery is the POST body /kana/vn expects.
type vnQuery struct {
	Filters any    `json:"filters"`
	Fields  string `json:"fields"`
	Results int    `json:"results"`
}

// vndbAPI is a throttled, retrying client for the public VNDB API.
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

// fetchImages resolves each vn id to its cover metadata. Ids VNDB does not
// answer for (a deleted vn) are simply absent from the map, and a vn with no
// cover maps to nil — the caller distinguishes both from a found image.
func (a *vndbAPI) fetchImages(ctx context.Context, ids []string) (map[string]*vnImage, error) {
	out := make(map[string]*vnImage, len(ids))
	for i := 0; i < len(ids); i += apiBatchSize {
		batch := ids[i:min(i+apiBatchSize, len(ids))]
		resp, err := a.queryVN(ctx, batch)
		if err != nil {
			return nil, err
		}
		if resp.More {
			// One result per id is the invariant; a `more` page would mean the
			// filter matched something unexpected. Loud, because a silent
			// truncation would look like "VNDB has no cover" for real works.
			slog.Warn("vndb api returned a truncated page", "batch_size", len(batch))
		}
		for _, r := range resp.Results {
			out[r.ID] = r.Image
		}
	}
	return out, nil
}

// queryVN POSTs one batch. Ids are matched with an `or` of exact id filters —
// the documented way to ask for a specific set.
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
		if wait <= 0 { // terminal (4xx that is not 429, or an unparseable body)
			return nil, err
		}
		slog.Warn("vndb api retry", "attempt", attempt+1, "wait", wait, "err", err)
		if !sleepCtx(ctx, wait) {
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("vndb api gave up after %d attempts: %w", apiRetries, lastErr)
}

// post performs one round-trip. The returned wait is > 0 only when the failure
// is worth retrying (429 / 5xx / transport), and carries the server's own
// Retry-After when it sent one.
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

// throttle spaces calls at least gap apart.
func (a *vndbAPI) throttle(ctx context.Context) {
	if a.gap <= 0 {
		return
	}
	if wait := a.gap - time.Since(a.last); wait > 0 && !a.last.IsZero() {
		sleepCtx(ctx, wait)
	}
	a.last = time.Now()
}

// retryAfter reads a Retry-After header (delta-seconds form, which is what VNDB
// sends), falling back to def when it is absent or unparseable.
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
