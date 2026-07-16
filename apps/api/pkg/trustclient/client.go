// Package trustclient is a thin server-side client for the trust service's S2S
// forward face. The community service uses it to forward its local review
// signals (over-threshold flags, held first posts) into the unified trust inbox
// and to resolve them back after a site moderator decides locally (step 03).
// The Basic S2S credentials stay server-side.
package trustclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Config is the caller-side configuration.
type Config struct {
	BaseURL      string // e.g. http://trust:9283  (no trailing slash, no /api/v1)
	ClientID     string
	ClientSecret string
	HTTPClient   *http.Client
	Timeout      time.Duration // default 15s
}

// Client talks to the trust S2S forward face. Safe for concurrent use.
type Client struct {
	cfg  Config
	http *http.Client
}

// New builds a Client. Returns nil when unconfigured (no base URL / credentials)
// so the caller can treat "trust forwarding not wired" as forwarding disabled
// (fail-closed — the outbox ticker idles).
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

// ForwardRequest mirrors the trust ForwardRequest DTO.
type ForwardRequest struct {
	Site         string   `json:"site"`
	SubjectKind  string   `json:"subject_kind"`
	SubjectID    string   `json:"subject_id"`
	Severity     *int16   `json:"severity,omitempty"`
	WeightSum    *float32 `json:"weight_sum,omitempty"`
	ContextNote  *string  `json:"context_note,omitempty"`
	ForwarderRef *string  `json:"forwarder_ref,omitempty"`
}

type envelope[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

type forwardData struct {
	ReviewItemID int64 `json:"review_item_id"`
	Created      bool  `json:"created"`
}

// Forward opens/updates a forwarded review item. Returns the trust review item
// id and whether it was newly created.
func (c *Client) Forward(ctx context.Context, req ForwardRequest) (trustItemID int64, created bool, err error) {
	var env envelope[forwardData]
	if err := c.post(ctx, "/api/v1/trust/forward", req, &env); err != nil {
		return 0, false, err
	}
	return env.Data.ReviewItemID, env.Data.Created, nil
}

// ScanRequest mirrors the trust ScanRequest DTO (step 04). Site is the relay
// path: a community forwarder relays for many sites through one S2S identity, so
// it carries the tenant site on the wire (allowlist-gated trust-side). Text is
// the author's RAW body (capped/truncated at intake, never client-side).
type ScanRequest struct {
	Site        string `json:"site,omitempty"`
	SubjectKind string `json:"subject_kind"`
	SubjectID   string `json:"subject_id"`
	Text        string `json:"text"`
	AuthorID    *int64 `json:"author_id,omitempty"`
}

type scanData struct {
	ScanID    int64 `json:"scan_id"`
	Truncated bool  `json:"truncated"`
}

// Scan submits a content-scan event for async AI shadow-scoring. Returns the
// scan-row id and whether the text was truncated at intake.
func (c *Client) Scan(ctx context.Context, req ScanRequest) (scanID int64, truncated bool, err error) {
	var env envelope[scanData]
	if err := c.post(ctx, "/api/v1/trust/scan", req, &env); err != nil {
		return 0, false, err
	}
	return env.Data.ScanID, env.Data.Truncated, nil
}

type resolveRequest struct {
	ReviewItemID int64   `json:"review_item_id"`
	Outcome      string  `json:"outcome"`
	ActorRef     *string `json:"actor_ref,omitempty"`
}

type resolveData struct {
	Closed bool `json:"closed"`
}

// Resolve closes a forwarded item after a local decision (outcome =
// approved|rejected). Returns whether an open item was actually closed
// (false = the trust item was already terminal — a tolerated race).
func (c *Client) Resolve(ctx context.Context, trustItemID int64, outcome, actorRef string) (closed bool, err error) {
	req := resolveRequest{ReviewItemID: trustItemID, Outcome: outcome}
	if actorRef != "" {
		req.ActorRef = &actorRef
	}
	var env envelope[resolveData]
	if err := c.post(ctx, "/api/v1/trust/forward/resolve", req, &env); err != nil {
		return false, err
	}
	return env.Data.Closed, nil
}

// post sends a Basic-authed JSON POST and decodes the house envelope. A non-2xx
// status or a non-zero envelope code is an error.
func (c *Client) post(ctx context.Context, path string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c.cfg.ClientID+":"+c.cfg.ClientSecret)))
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read trust response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("trust %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	// Peek the envelope code before decoding into the typed target.
	var head struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return fmt.Errorf("decode trust response: %w", err)
	}
	if head.Code != 0 {
		return fmt.Errorf("trust %s: code %d: %s", path, head.Code, head.Message)
	}
	return json.Unmarshal(raw, out)
}
