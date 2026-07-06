// Package llmsuggest is the local-LLM SUGGESTION pipeline (doc 17 §4): the
// model is a proposer, never an asserter. Every output lands in the src_llm
// staging schema as a graded suggestion carrying its model + prompt_version +
// input_hash; nothing here writes Gold, touches Silver's infobox_parsed,
// mutates wiki data, or re-grades an external_ref. Confidence values are
// archived for sorting only — there is no threshold that triggers an
// automatic action (self-reported confidence is systematically overconfident).
//
// The client talks to a local vLLM OpenAI-compatible server. Zero new Go
// dependencies: the HTTP client is hand-written on net/http.
package llmsuggest

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

// Client is a minimal OpenAI-compatible chat client for a local vLLM server.
type Client struct {
	baseURL string
	model   string
	http    *http.Client
}

// NewClient builds a client. baseURL is the OpenAI base (…/v1); model is the
// served model id (e.g. "qwen3-14b").
func NewClient(baseURL, model string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		http:    &http.Client{Timeout: 180 * time.Second},
	}
}

// chatRequest is the subset of the chat-completions body we send. guided JSON
// via response_format=json_schema; Qwen3 "thinking" is disabled through
// chat_template_kwargs so the whole budget goes to the schema-valid answer.
type chatRequest struct {
	Model             string         `json:"model"`
	Messages          []chatMessage  `json:"messages"`
	MaxTokens         int            `json:"max_tokens"`
	Temperature       float64        `json:"temperature"`
	ChatTemplateKwarg map[string]any `json:"chat_template_kwargs"`
	ResponseFormat    map[string]any `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// ChatResult carries the raw content plus token usage (for throughput stats).
type ChatResult struct {
	Content          string
	CompletionTokens int
	FinishReason     string
}

// ChatJSON runs one guided-JSON chat completion. schema is a JSON Schema
// object; the server constrains the reply to it. The returned Content is the
// raw JSON string (caller unmarshals into the task struct).
func (c *Client) ChatJSON(ctx context.Context, system, user, schemaName string, schema map[string]any, maxTokens int) (ChatResult, error) {
	body := chatRequest{
		Model:             c.model,
		MaxTokens:         maxTokens,
		Temperature:       0,
		ChatTemplateKwarg: map[string]any{"enable_thinking": false},
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		ResponseFormat: map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   schemaName,
				"schema": schema,
			},
		},
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
		return ChatResult{}, fmt.Errorf("vllm http %d: %s", resp.StatusCode, truncate(string(data), 300))
	}
	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return ChatResult{}, fmt.Errorf("decode chat response: %w (body: %s)", err, truncate(string(data), 300))
	}
	if cr.Error != nil {
		return ChatResult{}, fmt.Errorf("vllm error: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return ChatResult{}, fmt.Errorf("vllm returned no choices")
	}
	return ChatResult{
		Content:          strings.TrimSpace(cr.Choices[0].Message.Content),
		CompletionTokens: cr.Usage.CompletionTokens,
		FinishReason:     cr.Choices[0].FinishReason,
	}, nil
}

// Ping checks the server is reachable and returns the served model ids.
func (c *Client) Ping(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	ids := make([]string, len(out.Data))
	for i, d := range out.Data {
		ids[i] = d.ID
	}
	return ids, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
