package newsmoderate

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

const (
	tier0Timeout = 15 * time.Second
	// The AI gateway may escalate an omni verdict to an LLM, and its own client
	// allows 120s for that. Anything shorter here turns a slow-but-correct
	// verdict into a degraded one, which on this track means the item waits for a
	// human instead — a real cost, so the budget is generous.
	scoreTimeout = 120 * time.Second
)

type envelope[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

// Tier0Verdict mirrors trust's CheckResponse.
type Tier0Verdict struct {
	Decision string   `json:"decision"`
	Matched  []string `json:"matched"`
}

// ScoreVerdict mirrors the AI gateway's moderate-text data block. Degraded is
// the field this whole track turns on: the gateway answers 200 with
// flagged=false when it could not actually judge, and only Degraded tells the
// two apart.
type ScoreVerdict struct {
	Flagged    bool     `json:"flagged"`
	Categories []string `json:"categories"`
	Score      *float32 `json:"score"`
	Channel    string   `json:"channel"`
	Degraded   bool     `json:"degraded"`
}

type Client struct {
	trustBase string
	aiBase    string
	basic     string
	tier0HTTP *http.Client
	scoreHTTP *http.Client
}

func NewClient(trustBase, aiBase, clientID, secret string) *Client {
	return &Client{
		trustBase: strings.TrimRight(trustBase, "/"),
		aiBase:    strings.TrimRight(aiBase, "/"),
		basic:     base64.StdEncoding.EncodeToString([]byte(clientID + ":" + secret)),
		tier0HTTP: &http.Client{Timeout: tier0Timeout},
		scoreHTTP: &http.Client{Timeout: scoreTimeout},
	}
}

func (c *Client) Tier0(ctx context.Context, text string) (Tier0Verdict, error) {
	var out Tier0Verdict
	// site is deliberately omitted: sending it takes the relay path, which is
	// gated on the trust forwarder allowlist. Left out, trust derives the site
	// from this client's catalog_site — a narrower privilege for the same result.
	err := c.post(ctx, c.tier0HTTP, c.trustBase+"/api/v1/trust/check",
		map[string]any{"text": text}, &out)
	return out, err
}

func (c *Client) Score(ctx context.Context, text string) (ScoreVerdict, error) {
	var out ScoreVerdict
	err := c.post(ctx, c.scoreHTTP, c.aiBase+"/api/v1/ai/moderate-text",
		map[string]any{"text": text}, &out)
	return out, err
}

func (c *Client) post(ctx context.Context, hc *http.Client, url string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+c.basic)

	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		// 403 here almost always means the client has no catalog_site: both
		// services derive the caller's site from it and refuse without one.
		return fmt.Errorf("%s: http %d: %s", url, resp.StatusCode, snippet(data))
	}
	var env envelope[json.RawMessage]
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("%s: decode envelope: %w", url, err)
	}
	if env.Code != 0 {
		return fmt.Errorf("%s: code %d: %s", url, env.Code, env.Message)
	}
	if len(env.Data) == 0 {
		return fmt.Errorf("%s: empty data block", url)
	}
	return json.Unmarshal(env.Data, out)
}

func snippet(b []byte) string {
	const max = 200
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max]
	}
	return s
}
