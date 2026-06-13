// Package vndb is a small read-only client for the VNDB Kana API, used to
// enrich wiki galgames with accurate data straight from the source.
//
// Today it fetches a game's external links (store / official pages). The links
// VNDB exposes are spread across two places and are noisy, so the fetch is
// curated rather than dumped verbatim:
//
//   - a VN's own extlinks are mostly encyclopedic (Wikipedia / Wikidata / IGDB);
//   - each RELEASE carries the real store/official links (Steam, DLsite, Getchu,
//     GOG, JAST, console stores, …) but with heavy per-release redundancy (the
//     same shop repeated for every SKU) and auto-fetched noise (SteamDB next to
//     Steam, stats sites, …).
//
// We aggregate VN + all release extlinks, drop info/stats sites (denylist),
// collapse to one entry per site (VNDB's stable `name`), and label them with
// VNDB's `label`. Producer/developer links are deliberately NOT pulled here —
// the company's official site belongs on the official ENTITY, not the game's
// related links (the legacy nitro-server conflated the two).
package vndb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"api/internal/platform/galgame/model"
)

const apiBase = "https://api.vndb.org/kana"

// denyExact / denySubstrings drop VNDB extlink sites that are pure info / stats
// / aggregator entries (never a store or official page). Stores, console shops
// and the official `website` are kept. VNDB suffixes its site names
// (igdb_game, mobygames_game, acdb_source, enwiki/jawiki/wikidata…), so the
// info sites are matched by substring; precise stats sites by exact name.
var denyExact = map[string]bool{
	"egs":           true, // ErogameScape (stats)
	"steamdb":       true, // redundant with steam
	"howlongtobeat": true,
	"renai":         true,
	"vndb":          true, // self — added explicitly below
}

var denySubstrings = []string{"wiki", "igdb", "mobygames", "acdb", "anidb", "vgmdb"}

func denied(name string) bool {
	if denyExact[name] {
		return true
	}
	for _, sub := range denySubstrings {
		if strings.Contains(name, sub) {
			return true
		}
	}
	return false
}

// infoHostSubstrings identify legacy links to info/stats/encyclopedic sites that
// earlier imports dumped into galgame_link unmarked. On re-sync we drop links on
// these hosts (they're not store/official) so the curated set wins.
var infoHostSubstrings = []string{
	"wikipedia.org", "wikidata.org", "igdb.com", "animecharactersdatabase.com",
	"anidb.net", "vgmdb.net", "mobygames.com", "gamefaqs", "erogamescape",
	"howlongtobeat", "steamdb",
}

// Host returns the lowercased registrable host of a URL without a leading
// "www.", or "" if it can't be parsed. Used to dedup links across path/param
// differences (e.g. a legacy Getchu URL with extra query vs the canonical one).
func Host(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Host), "www.")
}

// IsInfoHost reports whether a host is a known info/stats/encyclopedic site
// (not a store or official page).
func IsInfoHost(host string) bool {
	for _, sub := range infoHostSubstrings {
		if strings.Contains(host, sub) {
			return true
		}
	}
	return false
}

// Client is a rate-limited VNDB API client.
type Client struct {
	http    *http.Client
	minGap  time.Duration
	lastReq time.Time
}

// New returns a client that spaces requests at least gap apart (be polite to
// the public API).
func New(gap time.Duration) *Client {
	if gap <= 0 {
		gap = time.Second
	}
	return &Client{http: &http.Client{Timeout: 30 * time.Second}, minGap: gap}
}

type extLink struct {
	URL   string `json:"url"`
	Label string `json:"label"`
	Name  string `json:"name"`
}

func (c *Client) post(path string, body any, out any) error {
	if d := c.minGap - time.Since(c.lastReq); d > 0 {
		time.Sleep(d)
	}
	c.lastReq = time.Now()

	buf, _ := json.Marshal(body)
	resp, err := c.http.Post(apiBase+path, "application/json", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("vndb %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// FetchGameLinks returns the curated VNDB-sourced links for a VN, as
// SnapshotLinks tagged Source="vndb" and SourceKey=<site name>. The vndb.org
// self-link is always first. Returns deduped, deterministic-ordered results.
func (c *Client) FetchGameLinks(vndbID string) ([]model.SnapshotLink, error) {
	var vnResp struct {
		Results []struct {
			Extlinks []extLink `json:"extlinks"`
		} `json:"results"`
	}
	if err := c.post("/vn", map[string]any{
		"filters": []any{"id", "=", vndbID},
		"fields":  "extlinks{url,label,name}",
	}, &vnResp); err != nil {
		return nil, fmt.Errorf("fetch vn: %w", err)
	}

	var relResp struct {
		Results []struct {
			Extlinks []extLink `json:"extlinks"`
		} `json:"results"`
	}
	if err := c.post("/release", map[string]any{
		"filters": []any{"vn", "=", []any{"id", "=", vndbID}},
		"fields":  "extlinks{url,label,name}",
		"results": 100,
	}, &relResp); err != nil {
		return nil, fmt.Errorf("fetch releases: %w", err)
	}

	// Aggregate VN + all release extlinks, drop denied sites, keep one per site.
	seen := map[string]bool{}
	var curated []extLink
	add := func(links []extLink) {
		for _, l := range links {
			if l.URL == "" || l.Name == "" || denied(l.Name) || seen[l.Name] {
				continue
			}
			seen[l.Name] = true
			curated = append(curated, l)
		}
	}
	for _, vn := range vnResp.Results {
		add(vn.Extlinks)
	}
	for _, r := range relResp.Results {
		add(r.Extlinks)
	}
	sort.Slice(curated, func(i, j int) bool { return curated[i].Name < curated[j].Name })

	out := make([]model.SnapshotLink, 0, len(curated)+1)
	out = append(out, model.SnapshotLink{
		Name: "VNDB", Link: "https://vndb.org/" + vndbID, Source: "vndb", SourceKey: "vndb",
	})
	for _, l := range curated {
		out = append(out, model.SnapshotLink{
			Name: l.Label, Link: l.URL, Source: "vndb", SourceKey: l.Name,
		})
	}
	return out, nil
}
