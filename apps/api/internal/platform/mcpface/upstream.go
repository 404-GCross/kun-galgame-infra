// Package mcpface is the thin MCP (Model Context Protocol) protocol-adapter over
// the already-public NextMoe /v1 read faces (served by the catalog service).
//
// It owns nothing. Every tool call is a pass-through GET to the public /v1 face,
// forwarding the caller's API key verbatim, so all authentication, tier / NSFW
// visibility, rate-limiting, daily quota and usage metering happen UPSTREAM on
// the same key — indistinguishable from a direct /v1 call in the caller's
// /dev/usage. This package touches NO database and NO cache: that is the whole
// point of the design (docs/developer-platform/09-mcp-server.md §1).
package mcpface

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// upstreamTimeout is the conservative per-request budget for a single upstream
// GET (design §2). M1 is all read-only, so an idempotent GET is retried once.
const upstreamTimeout = 30 * time.Second

// Upstream is the pooled pass-through HTTP client to the public /v1 face.
type Upstream struct {
	base   string
	client *http.Client
}

// NewUpstream builds the client over base (e.g. http://catalog:9281 in prod,
// http://127.0.0.1:9281 in local dev). The base's own path prefix, if any, is
// preserved; tool paths already carry the /v1/... segment.
func NewUpstream(base string) *Upstream {
	return &Upstream{
		base: strings.TrimRight(base, "/"),
		client: &http.Client{
			Timeout: upstreamTimeout,
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   10 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 20,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// Get issues an idempotent GET to path (which already includes the /v1 prefix)
// with the given query, forwarding authorization verbatim as the Authorization
// header. A single retry is allowed on a transport-level error (never on an HTTP
// status — a 4xx/5xx body carries an error the caller must see). Returns the
// upstream status code and raw body; err is non-nil only on a transport failure.
func (u *Upstream) Get(ctx context.Context, path string, query url.Values, authorization string) (status int, body []byte, err error) {
	target := u.base + path
	if enc := query.Encode(); enc != "" {
		target += "?" + enc
	}

	const maxAttempts = 2
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		status, body, err = u.do(ctx, target, authorization)
		if err == nil {
			return status, body, nil
		}
		// Do not retry once the caller's context is done (timeout/cancel).
		if ctx.Err() != nil {
			break
		}
	}
	return 0, nil, err
}

func (u *Upstream) do(ctx context.Context, target, authorization string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Accept", "application/json")

	resp, err := u.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("read body: %w", err)
	}
	return resp.StatusCode, b, nil
}
