// Package ymgalnews ingests 月幕 Galgame's news and column topics into kun_news.
//
// The whole package exists under one authorisation: 苍麟 granted the preview
// message and the banner image, explicitly not the article body, with
// click-through to 月幕 as the only path to the full text. Nothing here may grow
// a code path that stores an article body.
package ymgalnews

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// The upstream serves both lanes from one topic family with an identical
	// payload; only the path differs.
	pathNews   = "/open/topic/news"
	pathColumn = "/open/topic/column"

	// 月幕 fixes the page size at 10. It is not a parameter.
	pageSize = 10

	tokenPath  = "/oauth/token"
	tokenEarly = 5 * time.Minute
)

// shanghai is where publishTime lives. The upstream sends "2026-08-08 09:36:31"
// with no zone at all, so a parser that defaults to UTC shifts every article by
// eight hours — silently, because nothing errors and the feed simply sorts and
// caches wrong.
var shanghai = time.FixedZone("Asia/Shanghai", 8*60*60)

type Config struct {
	BaseURL      string
	ClientID     string
	ClientSecret string
}

type Client struct {
	cfg  Config
	http *http.Client

	mu     sync.Mutex
	token  string
	expiry time.Time

	rateLimited int
}

func NewClient(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://www.ymgal.games"
	}
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) RateLimitedCount() int { return c.rateLimited }

// Topic is the upstream payload. Fields 月幕 sends but we deliberately drop:
// views/replyNum/likesNum/favoritesNum (volatile counters — storing them would
// churn updated_at on every poll and drown the "content changed" signal) and
// author (per-item author id; our publisher identity is site-level, on
// news_source.publisher_uid).
type Topic struct {
	// TopicID is a snowflake the upstream serialises as a JSON STRING even
	// though its docs type it int64. Decoding it as a number fails outright.
	TopicID string `json:"topicId"`

	MainImg       string `json:"mainImg"`
	TopicURL      string `json:"topicUrl"`
	Title         string `json:"title"`
	Introduction  string `json:"introduction"`
	PublishTime   string `json:"publishTime"`
	TopicCategory string `json:"topicCategory"`

	// CreateAt is the AUTHOR'S NAME, not a timestamp — the upstream named a
	// display-name field after a creation date. Anything that maps it onto a
	// created_at column produces a time column full of people's names.
	CreateAt string `json:"createAt"`
}

// PublishedAt resolves publishTime against Asia/Shanghai.
func (t Topic) PublishedAt() (time.Time, error) {
	ts, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(t.PublishTime), shanghai)
	if err != nil {
		return time.Time{}, fmt.Errorf("publishTime %q: %w", t.PublishTime, err)
	}
	return ts.UTC(), nil
}

type envelope struct {
	Success bool            `json:"success"`
	Code    int             `json:"code"`
	Msg     string          `json:"msg"`
	Data    json.RawMessage `json:"data"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

// accessToken fetches and caches a client-credentials token.
//
// The token endpoint is a GET with query parameters, not the POST-form of the
// OAuth spec: a standards-shaped request 404s, and the 404 reads like a wrong
// path rather than a wrong method.
func (c *Client) accessToken(ctx context.Context, force bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !force && c.token != "" && time.Now().Before(c.expiry) {
		return c.token, nil
	}

	q := url.Values{}
	q.Set("grant_type", "client_credentials")
	q.Set("client_id", c.cfg.ClientID)
	q.Set("client_secret", c.cfg.ClientSecret)
	q.Set("scope", "public")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+tokenPath+"?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json;charset=utf-8")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("ymgal token: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		// The body can carry the client id; report the status only.
		return "", fmt.Errorf("ymgal token: http %d", resp.StatusCode)
	}
	var tr tokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return "", fmt.Errorf("ymgal token: parse: %w", err)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("ymgal token: empty access_token")
	}
	c.token = tr.AccessToken
	ttl := time.Duration(tr.ExpiresIn) * time.Second
	if ttl > tokenEarly {
		ttl -= tokenEarly
	}
	c.expiry = time.Now().Add(ttl)
	return c.token, nil
}

func lanePath(lane string) (string, error) {
	switch lane {
	case LaneNews:
		return pathNews, nil
	case LaneColumn:
		return pathColumn, nil
	default:
		return "", fmt.Errorf("ymgal: unknown lane %q", lane)
	}
}

// Topics fetches one page of a lane. An exhausted page is an empty array with
// code 0, which is the only stop signal the upstream offers: there is no total
// and no hasNext.
func (c *Client) Topics(ctx context.Context, lane string, page int) ([]Topic, error) {
	path, err := lanePath(lane)
	if err != nil {
		return nil, err
	}
	if page < 1 {
		page = 1
	}

	var lastErr error
	for attempt := range 4 {
		if attempt > 0 {
			backoff := time.Duration(1<<attempt) * time.Second
			slog.Warn("ymgal: backing off", "lane", lane, "page", page, "attempt", attempt, "sleep", backoff)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
		topics, retry, err := c.topicsOnce(ctx, path, lane, page)
		if err == nil {
			return topics, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
	}
	return nil, lastErr
}

func (c *Client) topicsOnce(ctx context.Context, path, lane string, page int) (topics []Topic, retry bool, err error) {
	token, err := c.accessToken(ctx, false)
	if err != nil {
		return nil, true, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s%s?page=%d", c.cfg.BaseURL, path, page), nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Accept", "application/json;charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)
	// The upstream requires an explicit interface version. Without it the call
	// does not fail loudly; it falls through to different default behaviour.
	req.Header.Set("version", "1")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, true, fmt.Errorf("ymgal %s page %d: %w", lane, page, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, err
	}

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		c.rateLimited++
		return nil, true, fmt.Errorf("ymgal %s page %d: rate limited", lane, page)
	case resp.StatusCode == http.StatusUnauthorized:
		if _, err := c.accessToken(ctx, true); err != nil {
			return nil, false, err
		}
		return nil, true, fmt.Errorf("ymgal %s page %d: unauthorized, token refreshed", lane, page)
	case resp.StatusCode >= 500:
		return nil, true, fmt.Errorf("ymgal %s page %d: http %d", lane, page, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, false, fmt.Errorf("ymgal %s page %d: http %d", lane, page, resp.StatusCode)
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, false, fmt.Errorf("ymgal %s page %d: parse envelope: %w", lane, page, err)
	}
	if env.Code == http.StatusTooManyRequests {
		c.rateLimited++
		return nil, true, fmt.Errorf("ymgal %s page %d: rate limited (code %d)", lane, page, env.Code)
	}
	if !env.Success || env.Code != 0 {
		return nil, false, fmt.Errorf("ymgal %s page %d: code %d msg %q", lane, page, env.Code, env.Msg)
	}
	if len(env.Data) == 0 {
		return nil, false, nil
	}
	if err := json.Unmarshal(env.Data, &topics); err != nil {
		return nil, false, fmt.Errorf("ymgal %s page %d: parse data: %w", lane, page, err)
	}
	return topics, false, nil
}
