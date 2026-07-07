// Package catalogclient is a thin server-side client for the catalog service's
// S2S read face. The galgame backend uses it to proxy the internal data
// browser's read-only requests, keeping the Basic S2S credentials server-side
// (the wiki frontend never sees them). It is a generic GET forwarder — the
// browser's payloads are pass-through JSON, so there is no value in re-typing
// every catalog DTO here.
package catalogclient

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Config is the caller-side configuration.
type Config struct {
	BaseURL      string // e.g. http://catalog:9281  (no trailing slash, no /api/v1)
	ClientID     string
	ClientSecret string
	HTTPClient   *http.Client
	Timeout      time.Duration // default 15s
}

// Client talks to the catalog S2S read face. Safe for concurrent use.
type Client struct {
	cfg  Config
	http *http.Client
}

// New builds a Client. Returns nil if unconfigured (no base URL / credentials)
// so the caller can treat "catalog browsing not wired" as a soft 503.
func New(cfg Config) *Client {
	if cfg.BaseURL == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	hc := cfg.HTTPClient
	if hc == nil {
		timeout := cfg.Timeout
		if timeout == 0 {
			timeout = 15 * time.Second
		}
		hc = &http.Client{Timeout: timeout}
	}
	return &Client{cfg: cfg, http: hc}
}

// GetJSON forwards a GET to /api/v1/catalog/<path>?<rawQuery> with Basic auth
// and returns the catalog service's status code and raw response body
// verbatim. path must not start with a slash.
func (c *Client) GetJSON(ctx context.Context, path, rawQuery string) (status int, body []byte, err error) {
	u := c.cfg.BaseURL + "/api/v1/catalog/" + path
	if rawQuery != "" {
		u += "?" + rawQuery
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c.cfg.ClientID+":"+c.cfg.ClientSecret)))
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8 MiB cap
	if err != nil {
		return 0, nil, fmt.Errorf("read catalog response: %w", err)
	}
	return resp.StatusCode, b, nil
}
