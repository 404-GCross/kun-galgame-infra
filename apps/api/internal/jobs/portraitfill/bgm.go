package portraitfill

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// bgmAPIBase is the public Bangumi API. Only the read-only subject endpoint is
// used, politely rate-limited (this is the secondary/fallback source).
const bgmAPIBase = "https://api.bgm.tv"

// bgmImage is one subject's cover image + its nsfw flag, as returned by
// GET /v0/subjects/{id}. `large` is the highest-resolution cover Bangumi serves.
// Bangumi does not expose per-image content ratings, and the subject `nsfw`
// boolean is stored verbatim without inference (galgame_bangumi_meta doc):
// true does NOT imply an r18 cover rating, so the cover row's sexual/violence
// stay 0.
type bgmImage struct {
	ID     int
	Large  string
	NSFW   bool
	Width  int // filled by the caller after downloading (Bangumi omits dims)
	Height int
}

// bgmClient is a tiny rate-limited Bangumi API reader. When token is non-empty
// it sends `Authorization: Bearer <token>` so R18 subjects (which the API hides
// with a 404 for anonymous callers — the 40-wave finding) become visible.
type bgmClient struct {
	http  *http.Client
	lim   *limiter
	token string
}

func newBGMClient(gap time.Duration, token string) *bgmClient {
	return &bgmClient{http: &http.Client{Timeout: 30 * time.Second}, lim: newLimiter(gap), token: token}
}

// FetchSubjectImage returns the subject's large cover URL + nsfw flag, or
// (nil, nil) when the subject has no cover / does not exist. Bangumi asks that
// clients send a descriptive User-Agent.
func (b *bgmClient) FetchSubjectImage(ctx context.Context, bid int) (*bgmImage, error) {
	b.lim.wait()
	url := fmt.Sprintf("%s/v0/subjects/%d", bgmAPIBase, bid)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "kun-galgame-wiki/portrait-fill (https://www.kungal.com)")
	req.Header.Set("Accept", "application/json")
	if b.token != "" {
		req.Header.Set("Authorization", "Bearer "+b.token)
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("bgm subject %d: HTTP %d: %s", bid, resp.StatusCode, string(msg))
	}
	var body struct {
		ID     int  `json:"id"`
		NSFW   bool `json:"nsfw"`
		Images struct {
			Large  string `json:"large"`
			Common string `json:"common"`
		} `json:"images"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("bgm subject %d decode: %w", bid, err)
	}
	if body.Images.Large == "" {
		return nil, nil
	}
	return &bgmImage{ID: body.ID, Large: body.Images.Large, NSFW: body.NSFW}, nil
}
