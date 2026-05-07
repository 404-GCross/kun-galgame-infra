// Package userclient is the Go SDK for the OAuth `/api/v1/users/batch`
// endpoint. It's intended to be imported by calling services
// (kungal / moyu / galgame_wiki) that need to render user names / avatars
// alongside their own data.
//
// Usage pattern: keep a singleton in calling services. The SDK manages an
// internal TTL cache + singleflight so concurrent renders sharing the same
// user_ids only fan out one HTTP request.
//
//	var sharedUserClient = userclient.New(userclient.Config{
//	    BaseURL:      "https://oauth.example.com/api/v1",
//	    ClientID:     cfg.OAuthClientID,
//	    ClientSecret: cfg.OAuthClientSecret,
//	    CacheTTL:     10 * time.Minute,
//	})
//
//	users, err := sharedUserClient.Users(ctx, []uint{1, 2, 3, 4, 5})
//	// users[1].Name, users[1].Avatar, users[1].AvatarImageHash
//
// On image_hash → URL resolution, pair with imageclient.MainURL or the
// frontend's resolveAvatarUrl helper. This SDK only delivers the hash.
package userclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	urlpkg "net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// UserBrief mirrors the OAuth service's dto.UserBrief response shape.
// Fields here ARE the public contract — keep in sync with auth/dto/user_batch_dto.go.
type UserBrief struct {
	ID              uint     `json:"id"`
	UUID            string   `json:"uuid"`
	Name            string   `json:"name"`
	Avatar          string   `json:"avatar"`
	AvatarImageHash *string  `json:"avatar_image_hash,omitempty"`
	Bio             string   `json:"bio"`
	Status          int      `json:"status"`
	Roles           []string `json:"roles"`
}

// Config configures the SDK.
type Config struct {
	BaseURL      string // e.g. https://oauth.example.com/api/v1 (no trailing slash)
	ClientID     string // OAuth client_id (Basic Auth)
	ClientSecret string // OAuth client_secret

	HTTPClient *http.Client
	Timeout    time.Duration // default 5s

	CacheTTL         time.Duration // default 10m; how long to keep a UserBrief before refetching
	NotFoundCacheTTL time.Duration // default 1m; negative cache TTL — short to recover from typo / late-creates

	// MaxBatch caps how many ids are sent in a single batch request to
	// the server (also enforced server-side; 100 is the server max).
	MaxBatch int // default 100
}

// Client is a singleton-friendly client for the users batch endpoint.
type Client struct {
	cfg      Config
	http     *http.Client
	authHdr  string
	cache    sync.Map // uint → cacheEntry
	notFound sync.Map // uint → time.Time (until)
	sf       singleflight.Group
}

type cacheEntry struct {
	brief   *UserBrief
	expires time.Time
}

// New constructs a Client from Config.
func New(cfg Config) *Client {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.CacheTTL == 0 {
		cfg.CacheTTL = 10 * time.Minute
	}
	if cfg.NotFoundCacheTTL == 0 {
		cfg.NotFoundCacheTTL = 1 * time.Minute
	}
	if cfg.MaxBatch <= 0 || cfg.MaxBatch > 100 {
		cfg.MaxBatch = 100
	}

	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: cfg.Timeout}
	}

	creds := cfg.ClientID + ":" + cfg.ClientSecret
	hdr := "Basic " + base64.StdEncoding.EncodeToString([]byte(creds))

	return &Client{cfg: cfg, http: hc, authHdr: hdr}
}

// Error is returned when the server responds with non-2xx.
type Error struct {
	StatusCode int
	Code       int    `json:"code"`
	Message    string `json:"message"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("userclient: status=%d code=%d: %s", e.StatusCode, e.Code, e.Message)
}

// User fetches a single user. Convenience wrapper over Users.
func (c *Client) User(ctx context.Context, id uint) (*UserBrief, error) {
	users, err := c.Users(ctx, []uint{id})
	if err != nil {
		return nil, err
	}
	if u, ok := users[id]; ok {
		return u, nil
	}
	return nil, nil // not found
}

// Users fetches a batch of users. Returns a map keyed by id; not-found ids
// are simply absent from the map (caller can compute the diff if needed).
//
// Behavior:
//   - Hits the in-memory TTL cache first; missing/expired ids are deduped
//   - Concurrent calls with overlapping missing ids are coalesced via singleflight
//   - Server-side max batch (100) is enforced; SDK chunks larger requests
func (c *Client) Users(ctx context.Context, ids []uint) (map[uint]*UserBrief, error) {
	out := make(map[uint]*UserBrief, len(ids))
	now := time.Now()

	// 1) Serve from cache
	missing := make([]uint, 0, len(ids))
	for _, id := range dedupe(ids) {
		if v, ok := c.cache.Load(id); ok {
			e := v.(cacheEntry)
			if e.expires.After(now) {
				out[id] = e.brief
				continue
			}
		}
		// negative cache check
		if v, ok := c.notFound.Load(id); ok {
			until := v.(time.Time)
			if until.After(now) {
				continue // known not-found, don't refetch
			}
		}
		missing = append(missing, id)
	}
	if len(missing) == 0 {
		return out, nil
	}

	// 2) Fetch missing in chunks
	for _, chunk := range chunk(missing, c.cfg.MaxBatch) {
		resp, err := c.fetchBatch(ctx, chunk)
		if err != nil {
			return out, err
		}
		c.populateCacheFromResponse(chunk, resp, now)
		for i := range resp.Users {
			u := resp.Users[i]
			out[u.ID] = &u
		}
	}
	return out, nil
}

// batchResponse mirrors dto.BatchGetUsersResponse.
type batchResponse struct {
	Users    []UserBrief `json:"users"`
	NotFound []uint      `json:"not_found"`
}

// envelope mirrors pkg/response.Response.
type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// fetchBatch coalesces concurrent requests with the same id-set via
// singleflight, then issues one HTTP call.
func (c *Client) fetchBatch(ctx context.Context, ids []uint) (*batchResponse, error) {
	key := singleflightKey(ids)
	v, err, _ := c.sf.Do(key, func() (any, error) {
		return c.doFetch(ctx, ids)
	})
	if err != nil {
		return nil, err
	}
	return v.(*batchResponse), nil
}

func (c *Client) doFetch(ctx context.Context, ids []uint) (*batchResponse, error) {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatUint(uint64(id), 10)
	}
	url := fmt.Sprintf("%s/users/batch?ids=%s", c.cfg.BaseURL, strings.Join(parts, ","))
	var data batchResponse
	if err := c.getJSON(ctx, url, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// getJSON does a GET against the OAuth API, validates the standard envelope,
// and unmarshals `data` into dataOut. Shared by Users and Search.
func (c *Client) getJSON(ctx context.Context, url string, dataOut any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("userclient: build request: %w", err)
	}
	req.Header.Set("Authorization", c.authHdr)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("userclient: http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("userclient: read body: %w", err)
	}

	if resp.StatusCode >= 400 {
		var e Error
		_ = json.Unmarshal(body, &e)
		e.StatusCode = resp.StatusCode
		if e.Message == "" {
			e.Message = string(body)
		}
		return &e
	}

	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("userclient: parse envelope: %w", err)
	}
	if env.Code != 0 {
		return &Error{StatusCode: resp.StatusCode, Code: env.Code, Message: env.Message}
	}
	if err := json.Unmarshal(env.Data, dataOut); err != nil {
		return fmt.Errorf("userclient: parse data: %w", err)
	}
	return nil
}

func (c *Client) populateCacheFromResponse(requested []uint, resp *batchResponse, now time.Time) {
	expires := now.Add(c.cfg.CacheTTL)
	notFoundExpires := now.Add(c.cfg.NotFoundCacheTTL)

	found := make(map[uint]struct{}, len(resp.Users))
	for i := range resp.Users {
		u := &resp.Users[i]
		c.cache.Store(u.ID, cacheEntry{brief: u, expires: expires})
		found[u.ID] = struct{}{}
		// Clear any stale negative cache for this id
		c.notFound.Delete(u.ID)
	}
	// Negative cache for ids that were requested but not returned
	for _, id := range requested {
		if _, ok := found[id]; ok {
			continue
		}
		c.notFound.Store(id, notFoundExpires)
	}
}

// Search performs a name-substring search against the OAuth users table.
// Returns up to `limit` UserBriefs ranked locally by the server: exact match
// first, then prefix match, then substring match — alphabetical within ties.
//
// Unlike Users(), results are NOT cached: the query space is essentially
// unbounded and results shift as users register / rename. Callers that need
// per-keystroke autocomplete should debounce client-side.
//
// limit is capped server-side at 50; pass 0 to use the server default (20).
func (c *Client) Search(ctx context.Context, query string, limit int) ([]UserBrief, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, errors.New("userclient: search query required")
	}
	if limit < 0 {
		limit = 0
	}

	url := fmt.Sprintf("%s/users/search?q=%s",
		c.cfg.BaseURL, urlpkg.QueryEscape(q))
	if limit > 0 {
		url += "&limit=" + strconv.Itoa(limit)
	}

	var data struct {
		Users []UserBrief `json:"users"`
	}
	if err := c.getJSON(ctx, url, &data); err != nil {
		return nil, err
	}
	return data.Users, nil
}

// Invalidate drops a user from cache. Useful after a known mutation
// (e.g. user just changed their name in this same process) so the next
// fetch hits the server.
func (c *Client) Invalidate(ids ...uint) {
	for _, id := range ids {
		c.cache.Delete(id)
		c.notFound.Delete(id)
	}
}

// ---- helpers ----

// dedupe returns a slice with duplicates removed, preserving first-seen order.
func dedupe(ids []uint) []uint {
	seen := make(map[uint]struct{}, len(ids))
	out := make([]uint, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// chunk splits ids into chunks of up to size n.
func chunk(ids []uint, n int) [][]uint {
	if n <= 0 {
		return [][]uint{ids}
	}
	out := make([][]uint, 0, (len(ids)+n-1)/n)
	for i := 0; i < len(ids); i += n {
		end := min(i+n, len(ids))
		out = append(out, ids[i:end])
	}
	return out
}

// singleflightKey produces a stable key for a sorted set of ids so two
// concurrent goroutines requesting the same set share work.
func singleflightKey(ids []uint) string {
	// ids may not be sorted; sort to make the key deterministic
	cp := make([]uint, len(ids))
	copy(cp, ids)
	sortUints(cp)
	parts := make([]string, len(cp))
	for i, id := range cp {
		parts[i] = strconv.FormatUint(uint64(id), 10)
	}
	return strings.Join(parts, ",")
}

func sortUints(s []uint) {
	// Simple insertion sort — typical batch is small (≤100 items, often <20)
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// IsNotFoundError reports whether err represents a missing user. Helper
// for callers that want to handle "user 99 doesn't exist" specifically.
func IsNotFoundError(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.StatusCode == 404
}
