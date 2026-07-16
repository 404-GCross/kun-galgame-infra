package service

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

// ScanGateway is the scoring worker's SOLE contact with the AI gateway (cmd/ai):
// the moderate-text semantic route (doc 18 §6 Tier2-via-gateway; doc 20). The
// production implementation posts S2S Basic to cmd/ai; tests fake it to exercise
// the worker's three terminal states.
//
// Configured reports whether the KUN_AI_CLIENT_* envs are wired. When false the
// worker drains rows to degraded WITHOUT a network call (fail-closed — the queue
// never backs up while the channel layer is unprovisioned).
type ScanGateway interface {
	Configured() bool
	// Moderate scores one text. A non-nil error OR a degraded verdict makes the
	// worker mark the row degraded (fail-open shadow: scoring never blocks the
	// queue, mirroring the gateway's own fail-open contract).
	Moderate(ctx context.Context, text string, authorID *int64) (GatewayVerdict, error)
}

// GatewayVerdict is the moderate-text route verdict, projected from the gateway's
// {code,message,data} envelope. Degraded=true means the upstream was
// unavailable / over budget / env-empty (the gateway's own fail-open path).
type GatewayVerdict struct {
	Flagged    bool
	Score      *float32
	Categories []string
	Channel    string
	Degraded   bool
}

// AIGatewayClient is the production ScanGateway: a thin S2S Basic client to
// cmd/ai's POST /api/v1/ai/moderate-text. Its entire config is a base URL + a
// client id/secret (the trust→ai OAuth client credentials); when any is empty
// the client is NOT configured and the worker degrades without dialing. Zero new
// Go dependencies — hand-written on net/http, mirroring the callback worker + the
// ai upstream client.
type AIGatewayClient struct {
	baseURL  string
	clientID string
	secret   string
	http     *http.Client
}

// NewAIGatewayClient builds the client. Empty baseURL / clientID / secret →
// Configured() is false (degraded mode; see the interface doc).
func NewAIGatewayClient(baseURL, clientID, secret string) *AIGatewayClient {
	return &AIGatewayClient{
		baseURL:  strings.TrimRight(baseURL, "/"),
		clientID: clientID,
		secret:   secret,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

// Configured reports whether all three S2S credentials are present. Any empty →
// degraded (the worker drains to degraded without a call).
func (c *AIGatewayClient) Configured() bool {
	return c.baseURL != "" && c.clientID != "" && c.secret != ""
}

// moderateReq mirrors the ai ModerateTextRequest wire contract (docs/ai/openapi.yaml);
// site is NOT on the wire — cmd/ai derives it from the S2S client binding.
type moderateReq struct {
	Text     string `json:"text"`
	AuthorID *int64 `json:"author_id,omitempty"`
}

// moderateEnvelope mirrors the gateway's house {code,message,data} envelope with
// the ModerateTextResponse payload (docs/ai/openapi.yaml). Defined locally rather
// than importing cmd/ai's dto so trust stays decoupled from the ai Go package —
// the contract of record is the OpenAPI spec.
type moderateEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Route      string   `json:"route"`
		Flagged    bool     `json:"flagged"`
		Categories []string `json:"categories"`
		Score      *float32 `json:"score"`
		Channel    string   `json:"channel"`
		Degraded   bool     `json:"degraded"`
	} `json:"data"`
}

// Moderate posts one scan to the gateway and projects the verdict. A transport
// error, non-200, decode failure, or non-zero envelope code is returned as an
// error (the worker turns that into a degraded drain). A 200 with degraded:true
// is a successful call reporting the gateway's own fail-open path — returned as a
// verdict with Degraded=true (no error), which the worker also drains to degraded.
func (c *AIGatewayClient) Moderate(ctx context.Context, text string, authorID *int64) (GatewayVerdict, error) {
	raw, err := json.Marshal(moderateReq{Text: text, AuthorID: authorID})
	if err != nil {
		return GatewayVerdict{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/ai/moderate-text", bytes.NewReader(raw))
	if err != nil {
		return GatewayVerdict{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+basicCred(c.clientID, c.secret))

	resp, err := c.http.Do(req)
	if err != nil {
		return GatewayVerdict{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return GatewayVerdict{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return GatewayVerdict{}, fmt.Errorf("ai gateway http %d", resp.StatusCode)
	}
	var env moderateEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return GatewayVerdict{}, fmt.Errorf("decode moderate-text response: %w", err)
	}
	if env.Code != 0 {
		return GatewayVerdict{}, fmt.Errorf("ai gateway code %d: %s", env.Code, env.Message)
	}
	return GatewayVerdict{
		Flagged:    env.Data.Flagged,
		Score:      env.Data.Score,
		Categories: env.Data.Categories,
		Channel:    env.Data.Channel,
		Degraded:   env.Data.Degraded,
	}, nil
}

// basicCred builds the base64(client_id:secret) value for the Basic header.
func basicCred(id, secret string) string {
	return base64.StdEncoding.EncodeToString([]byte(id + ":" + secret))
}
