package main

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

// The moderations endpoint scores only a subset of its categories on image
// input: sexual, violence, violence/graphic and the self-harm family. Notably
// sexual/minors is TEXT ONLY and never appears for an image, so no image-side
// gate can be built on it.
type omniImageClient struct {
	baseURL string
	token   string
	model   string
	http    *http.Client
}

func newOmniImageClient(baseURL, token, model string) *omniImageClient {
	return &omniImageClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		model:   model,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *omniImageClient) configured() bool {
	return c.baseURL != "" && c.token != ""
}

type omniImagePart struct {
	Type     string            `json:"type"`
	ImageURL map[string]string `json:"image_url"`
}

type omniImageRequest struct {
	Model string          `json:"model"`
	Input []omniImagePart `json:"input"`
}

type omniImageResponse struct {
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

type omniVerdict struct {
	Flagged    bool               `json:"flagged"`
	Categories map[string]bool    `json:"categories"`
	Scores     map[string]float64 `json:"scores"`
	Channel    string             `json:"channel"`
}

func (c *omniImageClient) moderateImage(ctx context.Context, imageURL string) (*omniVerdict, error) {
	raw, err := json.Marshal(omniImageRequest{
		Model: c.model,
		Input: []omniImagePart{{Type: "image_url", ImageURL: map[string]string{"url": imageURL}}},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/moderations", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, retryable{fmt.Errorf("moderations http %d: %s", resp.StatusCode, truncate(string(data), 200))}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("moderations http %d: %s", resp.StatusCode, truncate(string(data), 200))
	}
	var or omniImageResponse
	if err := json.Unmarshal(data, &or); err != nil {
		return nil, fmt.Errorf("decode: %w (body: %s)", err, truncate(string(data), 200))
	}
	if or.Error != nil {
		return nil, fmt.Errorf("moderations error: %s", or.Error.Message)
	}
	if len(or.Results) == 0 {
		return nil, fmt.Errorf("moderations returned no results")
	}
	channel := or.Model
	if channel == "" {
		channel = c.model
	}
	return &omniVerdict{
		Flagged:    or.Results[0].Flagged,
		Categories: or.Results[0].Categories,
		Scores:     or.Results[0].CategoryScores,
		Channel:    channel,
	}, nil
}

type retryable struct{ error }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
