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

// OmniClient is a minimal OpenAI /v1/moderations client — the Tier1 coarse pass
// of the moderate-text cascade (spec 09). Unlike Client (the chat/completions
// Tier2 LLM), it speaks the moderations protocol: one POST returns per-category
// boolean flags + scores. Its config mirrors Client: base URL + token + model;
// empty base/token → NOT configured (Tier1 off, the caller runs the LLM path).
type OmniClient struct {
	baseURL string
	token   string
	model   string
	http    *http.Client
}

// NewOmniClient builds a Tier1 client. baseURL is the API root (e.g.
// https://api.openai.com — NOT the /v1 base the chat client takes); the
// /v1/moderations path is appended. Empty baseURL/token = Tier1 off (Configured).
func NewOmniClient(baseURL, token, model string) *OmniClient {
	return &OmniClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		model:   model,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Configured reports whether Tier1 is wired. Empty base URL OR token → off: the
// caller runs the Tier2 LLM path without dialing omni (spec 09 ruling 3).
func (c *OmniClient) Configured() bool {
	return c.baseURL != "" && c.token != ""
}

// Model returns the configured omni model id (the metered channel for omni rows).
func (c *OmniClient) Model() string { return c.model }

// OmniResult is one /v1/moderations verdict: the raw per-category boolean flags
// and scores, plus the channel/model that served it (response model, falling
// back to the requested model). Category policy (which of these are adopted) is
// applied by the cascade, not here.
type OmniResult struct {
	Flagged        bool
	Categories     map[string]bool
	CategoryScores map[string]float64
	Channel        string
}

type omniRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type omniResponse struct {
	Model   string `json:"model"`
	Results []struct {
		Flagged        bool               `json:"flagged"`
		Categories     map[string]bool    `json:"categories"`
		CategoryScores map[string]float64 `json:"category_scores"`
	} `json:"results"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Moderate runs one /v1/moderations request for the given input text. A non-200,
// transport error, error object, or empty-results response is returned as an
// error; the caller (the cascade) meters status=upstream_error and falls through
// to the LLM path.
func (c *OmniClient) Moderate(ctx context.Context, input string) (OmniResult, error) {
	raw, err := json.Marshal(omniRequest{Model: c.model, Input: input})
	if err != nil {
		return OmniResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/moderations", bytes.NewReader(raw))
	if err != nil {
		return OmniResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return OmniResult{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return OmniResult{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return OmniResult{}, fmt.Errorf("omni http %d: %s", resp.StatusCode, truncate(string(data), 300))
	}
	var or omniResponse
	if err := json.Unmarshal(data, &or); err != nil {
		return OmniResult{}, fmt.Errorf("decode omni response: %w (body: %s)", err, truncate(string(data), 300))
	}
	if or.Error != nil {
		return OmniResult{}, fmt.Errorf("omni error: %s", or.Error.Message)
	}
	if len(or.Results) == 0 {
		return OmniResult{}, fmt.Errorf("omni returned no results")
	}
	channel := or.Model
	if channel == "" {
		channel = c.model
	}
	return OmniResult{
		Flagged:        or.Results[0].Flagged,
		Categories:     or.Results[0].Categories,
		CategoryScores: or.Results[0].CategoryScores,
		Channel:        channel,
	}, nil
}
