// Package vndb is a small read-only client for the VNDB Kana API, used to
// enrich wiki galgames with accurate data straight from the source.
//
// Today it fetches a game's external links (store / official pages). The real
// store/official links all live on a VN's RELEASES (Steam, DLsite, Getchu, GOG,
// JAST, console stores, the official website, …), but VNDB returns them noisily:
// heavy per-release redundancy (the same shop repeated for every SKU) plus
// auto-fetched extras (SteamDB next to Steam, gogdb, lutris, gamefaqs, the
// wikis, …). A VN's own extlinks are purely encyclopedic (Wikidata / Wikipedia /
// IGDB / MobyGames / …) and carry no store or official link, so we don't fetch
// them at all.
//
// Curation keeps only sites on a small ALLOWLIST (see storeSites) — the
// canonical storefront set from VNDB's GET /schema — collapses to one entry per
// site (VNDB's stable `name`), and labels them with VNDB's `label`.
// Producer/developer links are deliberately NOT pulled here — the company's
// official site belongs on the official ENTITY, not the game's related links
// (the legacy nitro-server conflated the two).
package vndb

import (
	"bytes"
	"context"
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

// storeSites is the allowlist of VNDB release extlink names we keep as a
// galgame's related links: real stores, shops and official platforms. It is the
// canonical `extlinks["/release"]` list from VNDB's GET /schema, MINUS `egs`
// (ErogameScape — a stats/ratings database, not a store), PLUS `website` (the
// official-website extlink, which VNDB returns on releases but does not list in
// the schema's named-site set).
//
// Everything else VNDB returns is auto-fetched noise and dropped by omission:
// the encyclopedic/stats/aggregator sites (wikidata, the wikis, igdb, mobygames,
// gogdb, steamdb, lutris, gamefaqs, howlongtobeat, pcgamingwiki, winehq, …).
//
// Source of truth: https://api.vndb.org/kana/schema → extlinks["/release"].
// Re-derive and update this list if VNDB adds a storefront (rare).
var storeSites = map[string]bool{
	"website":        true, // official website (returned on releases; not in schema's named set)
	"steam":          true,
	"dlsite":         true,
	"dmm":            true,
	"getchu":         true,
	"getchudl":       true, // DL.Getchu
	"digiket":        true,
	"gyutto":         true,
	"melon":          true, // Melonbooks.com
	"melonjp":        true, // Melonbooks.co.jp
	"toranoana":      true,
	"booth":          true,
	"animateg":       true, // Animate Games
	"novelgam":       true, // NovelGame
	"freem":          true, // Freem!
	"freegame":       true, // Freegame Mugen
	"gog":            true,
	"mg":             true, // MangaGamer
	"jastusa":        true, // JAST USA
	"jlist":          true, // J-List
	"denpa":          true, // Denpasoft
	"kagura":         true, // Kagura Games
	"johren":         true,
	"nutaku":         true,
	"fakku":          true,
	"playasia":       true,
	"itch":           true, // Itch.io
	"gamejolt":       true, // Game Jolt
	"appstore":       true, // App Store
	"googplay":       true, // Google Play
	"nintendo":       true,
	"nintendo_jp":    true,
	"nintendo_hk":    true,
	"playstation_jp": true,
	"playstation_na": true,
	"playstation_eu": true,
	"playstation_hk": true,
	"patreon":        true,
	"patreonp":       true, // Patreon post
	"substar":        true, // SubscribeStar
}

func isStore(name string) bool { return storeSites[name] }

// infoHostSubstrings identify legacy links to info/stats/encyclopedic sites that
// earlier imports dumped into galgame_link unmarked. On re-sync we drop links on
// these hosts (they're not store/official) so the curated set wins.
var infoHostSubstrings = []string{
	"wikipedia.org", "wikidata.org", "igdb.com", "animecharactersdatabase.com",
	"anidb.net", "vgmdb.net", "mobygames.com", "gamefaqs", "erogamescape",
	"howlongtobeat", "steamdb", "gogdb.org", "lutris.net", "pcgamingwiki.com",
	"winehq.org",
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

// maxThrottleRetries bounds the 429 back-off loop so a sustained throttle/ban
// can't hang a scheduled job forever (it returns an error instead).
const maxThrottleRetries = 5

func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	for attempt := 0; ; attempt++ {
		throttled, err := c.attempt(ctx, path, body, out)
		if err != nil {
			return err
		}
		if !throttled {
			return nil
		}
		if attempt >= maxThrottleRetries {
			return fmt.Errorf("vndb %s: still throttled (429) after %d retries", path, maxThrottleRetries)
		}
		select { // back off, but stay interruptible (graceful shutdown / run timeout)
		case <-time.After(60 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// attempt makes one rate-limited request and closes the body. It reports
// throttled=true on a 429 (caller backs off + retries) without consuming an error.
func (c *Client) attempt(ctx context.Context, path string, body, out any) (bool, error) {
	if d := c.minGap - time.Since(c.lastReq); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	c.lastReq = time.Now()

	buf, err := json.Marshal(body)
	if err != nil {
		return false, fmt.Errorf("marshal vndb %s body: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+path, bytes.NewReader(buf))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return true, nil
	}
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return false, fmt.Errorf("vndb %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return false, json.NewDecoder(resp.Body).Decode(out)
}

// curate turns a VN's raw release extlinks into the clean, deterministic
// SnapshotLink set: keep only allowlisted store/official sites, one entry per
// VNDB site name, label from VNDB, vndb.org self-link first.
func curate(vndbID string, links []extLink) []model.SnapshotLink {
	seen := map[string]bool{}
	kept := make([]extLink, 0, len(links))
	for _, l := range links {
		if l.URL == "" || !isStore(l.Name) || seen[l.Name] {
			continue
		}
		// Drop links that wouldn't fit galgame_link's columns (rare: a few
		// percent-encoded forum URLs mislabeled "website" run past the cap).
		// Dropping here keeps the snapshot and the table in agreement.
		if len(l.URL) > model.LinkURLMaxLen || len(l.Label) > model.LinkNameMaxLen {
			continue
		}
		seen[l.Name] = true
		kept = append(kept, l)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].Name < kept[j].Name })

	out := make([]model.SnapshotLink, 0, len(kept)+1)
	out = append(out, model.SnapshotLink{
		Name: "VNDB", Link: "https://vndb.org/" + vndbID, Source: "vndb", SourceKey: "vndb",
	})
	for _, l := range kept {
		out = append(out, model.SnapshotLink{Name: l.Label, Link: l.URL, Source: "vndb", SourceKey: l.Name})
	}
	return out
}

// FetchGameLinksBatch fetches curated links for up to 100 VNs in one batched
// query group — paginated /release calls filtered to those VNs — keyed by vndb
// id. This is what makes a bulk backfill fast: ~(releases/100) calls per 100 VNs
// instead of one call per VN. (VN-level extlinks are purely encyclopedic — see
// the package doc — so they aren't fetched.)
func (c *Client) FetchGameLinksBatch(ctx context.Context, ids []string) (map[string][]model.SnapshotLink, error) {
	want := make(map[string]bool, len(ids))
	or := []any{"or"}
	for _, id := range ids {
		if id == "" || want[id] {
			continue
		}
		want[id] = true
		or = append(or, []any{"id", "=", id})
	}
	if len(want) == 0 {
		return map[string][]model.SnapshotLink{}, nil
	}

	agg := make(map[string][]extLink, len(want)) // vndb id → its releases' extlinks

	// Release extlinks — paginated; each release is attributed to its VN(s).
	relFilter := []any{"vn", "=", or}
	for page := 1; ; page++ {
		var relResp struct {
			More    bool `json:"more"`
			Results []struct {
				VNs []struct {
					ID string `json:"id"`
				} `json:"vns"`
				Extlinks []extLink `json:"extlinks"`
			} `json:"results"`
		}
		if err := c.post(ctx, "/release", map[string]any{
			"filters": relFilter, "fields": "vns{id}, extlinks{url,label,name}",
			"results": 100, "page": page,
		}, &relResp); err != nil {
			return nil, fmt.Errorf("release batch page %d: %w", page, err)
		}
		for _, r := range relResp.Results {
			for _, v := range r.VNs {
				if want[v.ID] {
					agg[v.ID] = append(agg[v.ID], r.Extlinks...)
				}
			}
		}
		if !relResp.More {
			break
		}
	}

	out := make(map[string][]model.SnapshotLink, len(want))
	for id := range want {
		out[id] = curate(id, agg[id])
	}
	return out, nil
}

// FetchGameLinks is the single-VN convenience wrapper (for the future
// create-time path). The vndb.org self-link is always present.
func (c *Client) FetchGameLinks(ctx context.Context, vndbID string) ([]model.SnapshotLink, error) {
	m, err := c.FetchGameLinksBatch(ctx, []string{vndbID})
	if err != nil {
		return nil, err
	}
	return m[vndbID], nil
}

// Tag / Developer are the VN-level metadata used for relation enrichment (tags
// live on /vn, developers are the producers with a developer role). Callers map
// these into wiki entities via the vndbresolve package.
type Tag struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Category string  `json:"category"`
	Rating   float64 `json:"rating"`
	Spoiler  int     `json:"spoiler"`
	Lie      bool    `json:"lie"`
}

type Developer struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Original string `json:"original"`
	Type     string `json:"type"`
	Lang     string `json:"lang"`
}

// VNMeta is the per-VN tag/developer payload from /vn.
type VNMeta struct {
	Tags       []Tag
	Developers []Developer
}

// FetchVNMetaBatch fetches tags + developers for up to 100 VNs in a single /vn
// call (≤100 ids → ≤100 results, one page), keyed by vndb id. Paired with
// FetchGameLinksBatch (which hits /release), this is the "one /vn + paginated
// /release per 100 VNs" combined enrichment fetch.
func (c *Client) FetchVNMetaBatch(ctx context.Context, ids []string) (map[string]VNMeta, error) {
	want := make(map[string]bool, len(ids))
	or := []any{"or"}
	for _, id := range ids {
		if id == "" || want[id] {
			continue
		}
		want[id] = true
		or = append(or, []any{"id", "=", id})
	}
	if len(want) == 0 {
		return map[string]VNMeta{}, nil
	}

	var resp struct {
		Results []struct {
			ID         string      `json:"id"`
			Tags       []Tag       `json:"tags"`
			Developers []Developer `json:"developers"`
		} `json:"results"`
	}
	if err := c.post(ctx, "/vn", map[string]any{
		"filters": or,
		"fields":  "id, tags{id,name,category,rating,spoiler,lie}, developers{id,name,original,type,lang}",
		"results": 100,
	}, &resp); err != nil {
		return nil, fmt.Errorf("vn meta batch: %w", err)
	}

	out := make(map[string]VNMeta, len(resp.Results))
	for _, r := range resp.Results {
		out[r.ID] = VNMeta{Tags: r.Tags, Developers: r.Developers}
	}
	return out, nil
}

// VNImage is the per-VN cover image from /vn. ID is the VNDB image id
// (e.g. "cv12345", stored as galgame_cover.source_key); URL is the t.vndb.org
// cover URL; Sexual/Violence are VNDB's 0-2 average flag votes (rounded to the
// 0-2 cover columns by the caller).
type VNImage struct {
	ID       string
	URL      string
	Sexual   float64
	Violence float64
}

// FetchVNImagesBatch fetches the cover image for up to 100 VNs in a single /vn
// call, keyed by vndb id. A VN with no cover (image{} == null) is simply absent
// from the result. Used by the cover-sync daily job (API mode); the bulk
// backfill reads the offline VNDB dump instead (see internal/jobs/vndbcovers).
func (c *Client) FetchVNImagesBatch(ctx context.Context, ids []string) (map[string]VNImage, error) {
	want := make(map[string]bool, len(ids))
	or := []any{"or"}
	for _, id := range ids {
		if id == "" || want[id] {
			continue
		}
		want[id] = true
		or = append(or, []any{"id", "=", id})
	}
	if len(want) == 0 {
		return map[string]VNImage{}, nil
	}

	var resp struct {
		Results []struct {
			ID    string `json:"id"`
			Image *struct {
				ID       string  `json:"id"`
				URL      string  `json:"url"`
				Sexual   float64 `json:"sexual"`
				Violence float64 `json:"violence"`
			} `json:"image"`
		} `json:"results"`
	}
	if err := c.post(ctx, "/vn", map[string]any{
		"filters": or,
		"fields":  "id, image{id,url,sexual,violence}",
		"results": 100,
	}, &resp); err != nil {
		return nil, fmt.Errorf("vn image batch: %w", err)
	}

	out := make(map[string]VNImage, len(resp.Results))
	for _, r := range resp.Results {
		if r.Image == nil || r.Image.URL == "" {
			continue
		}
		out[r.ID] = VNImage{ID: r.Image.ID, URL: r.Image.URL, Sexual: r.Image.Sexual, Violence: r.Image.Violence}
	}
	return out, nil
}

// VNScreenshot is one screenshot from /vn screenshots{} — VNDB caps these at 10
// per VN. ID is the VNDB image id ("sf12345", stored as
// galgame_screenshot.source_key); URL is the t.vndb.org URL; Sexual/Violence are
// 0-2 average flag votes (rounded to the 0-2 columns by the caller).
type VNScreenshot struct {
	ID       string
	URL      string
	Sexual   float64
	Violence float64
}

// FetchVNScreenshotsBatch fetches the screenshot set for up to 100 VNs in a
// single /vn call, keyed by vndb id, preserving VNDB's order. A VN with no
// screenshots is absent. Used by the sync-vndb-screenshots job (the cover
// sibling); the one-time bulk was done offline (cmd/migrate-galgame-screenshots).
func (c *Client) FetchVNScreenshotsBatch(ctx context.Context, ids []string) (map[string][]VNScreenshot, error) {
	want := make(map[string]bool, len(ids))
	or := []any{"or"}
	for _, id := range ids {
		if id == "" || want[id] {
			continue
		}
		want[id] = true
		or = append(or, []any{"id", "=", id})
	}
	if len(want) == 0 {
		return map[string][]VNScreenshot{}, nil
	}

	var resp struct {
		Results []struct {
			ID          string `json:"id"`
			Screenshots []struct {
				ID       string  `json:"id"`
				URL      string  `json:"url"`
				Sexual   float64 `json:"sexual"`
				Violence float64 `json:"violence"`
			} `json:"screenshots"`
		} `json:"results"`
	}
	if err := c.post(ctx, "/vn", map[string]any{
		"filters": or,
		"fields":  "id, screenshots{id,url,sexual,violence}",
		"results": 100,
	}, &resp); err != nil {
		return nil, fmt.Errorf("vn screenshots batch: %w", err)
	}

	out := make(map[string][]VNScreenshot, len(resp.Results))
	for _, r := range resp.Results {
		for _, s := range r.Screenshots {
			if s.ID == "" || s.URL == "" {
				continue
			}
			out[r.ID] = append(out[r.ID], VNScreenshot{ID: s.ID, URL: s.URL, Sexual: s.Sexual, Violence: s.Violence})
		}
	}
	return out, nil
}
