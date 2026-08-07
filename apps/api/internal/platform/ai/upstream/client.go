// Package upstream is the semantic layer's ONLY contact with the channel layer:
// a thin OpenAI-compatible chat client (doc 20 §9 red-line). Its entire config
// is a base URL + a bearer token; when either is empty the client is NOT
// configured and callers must degrade WITHOUT dialling (never block, never
// error). Zero new Go dependencies — hand-written on net/http, mirroring
// internal/platform/catalog/llmsuggest.
package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// llmTimeout bounds one Tier2 chat-completion. It is deliberately generous: the
// LLM tier is reached ONLY from the async scan worker (never a user request
// path — see the package doc on moderate-text), and the production channel is
// slow. Measured 2026-08-06 over 482 live calls: successful ones averaged 6.8s
// and peaked at 27.2s, while 48.8% of calls died at exactly 30,004ms — they were
// not upstream failures, they were this timeout cutting off answers that were
// still coming. 90s leaves ~3x headroom over the observed peak. The trust-side
// scan gateway timeout MUST stay above this (it is 120s) or it severs the call
// first and the extra budget here buys nothing.
const llmTimeout = 90 * time.Second

// Client is a minimal OpenAI-compatible chat-completions client.
type Client struct {
	baseURL string
	token   string
	model   string
	http    *http.Client
}

// NewClient builds a client. baseURL is the OpenAI-compatible base (e.g.
// http://one-api:3000/v1); token is the bearer; model is the id the v0 route
// maps to. Empty baseURL/token = degraded (see Configured).
func NewClient(baseURL, token, model string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		model:   model,
		http:    &http.Client{Timeout: llmTimeout},
	}
}

// Configured reports whether the channel layer is wired. Empty base URL OR
// token → degraded mode: the caller returns fail-open without a network call
// (doc 20 §9). This is the sole coupling to the channel layer's readiness.
func (c *Client) Configured() bool {
	return c.baseURL != "" && c.token != ""
}

// Model returns the configured route→model id (v0 single-route mapping).
func (c *Client) Model() string { return c.model }

// ChatResult carries the raw reply content, the channel/model that served it
// (from the response, falling back to the requested model), and token usage.
type ChatResult struct {
	Content string
	Channel string
	// FinishReason is the upstream's own account of why generation stopped
	// ("stop", "length", …; "" when the server omits it). It is the ONLY way to
	// tell a reply the model chose to end from one the token ceiling cut off —
	// both arrive as a 200 carrying content that merely fails to parse. Callers
	// that treat unparseable as "upstream is broken" will chase the wrong fault
	// without it.
	FinishReason     string
	PromptTokens     int
	CompletionTokens int
}

type chatRequest struct {
	Model          string         `json:"model"`
	Messages       []chatMessage  `json:"messages"`
	MaxTokens      int            `json:"max_tokens"`
	Temperature    float64        `json:"temperature"`
	ResponseFormat map[string]any `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// ChatJSON runs one chat completion asking for a JSON object reply
// (response_format json_object — the widely-supported portable form; parsing is
// tolerant so a server that ignores it still works). A non-200, transport
// error, or empty-choices response is returned as an error; the caller
// (fail-open moderation) turns that into a degraded allow.
func (c *Client) ChatJSON(ctx context.Context, system, user string, maxTokens int) (ChatResult, error) {
	body := chatRequest{
		Model:       c.model,
		MaxTokens:   maxTokens,
		Temperature: 0,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		ResponseFormat: map[string]any{"type": "json_object"},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return ChatResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return ChatResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return ChatResult{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return ChatResult{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return ChatResult{}, fmt.Errorf("upstream http %d: %s", resp.StatusCode, truncate(string(data), 300))
	}
	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return ChatResult{}, fmt.Errorf("decode chat response: %w (body: %s)", err, truncate(string(data), 300))
	}
	if cr.Error != nil {
		return ChatResult{}, fmt.Errorf("upstream error: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return ChatResult{}, fmt.Errorf("upstream returned no choices")
	}
	channel := cr.Model
	if channel == "" {
		channel = c.model
	}
	return ChatResult{
		Content:          strings.TrimSpace(cr.Choices[0].Message.Content),
		Channel:          channel,
		FinishReason:     cr.Choices[0].FinishReason,
		PromptTokens:     cr.Usage.PromptTokens,
		CompletionTokens: cr.Usage.CompletionTokens,
	}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
