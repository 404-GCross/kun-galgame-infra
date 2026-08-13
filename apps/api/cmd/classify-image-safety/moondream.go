package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

type moondreamClient struct {
	accountID string
	token     string
	model     string
	http      *http.Client
	// neurons are fractional; keep them as micro-units so the counter can be atomic.
	microNeurons atomic.Int64
}

func newMoondreamClient(accountID, token, model string) *moondreamClient {
	return &moondreamClient{
		accountID: accountID,
		token:     token,
		model:     model,
		http:      &http.Client{Timeout: 120 * time.Second},
	}
}

func (c *moondreamClient) configured() bool {
	return c.accountID != "" && c.token != ""
}

func (c *moondreamClient) neurons() float64 {
	return float64(c.microNeurons.Load()) / 1e6
}

type moondreamEvent struct {
	Error  string `json:"error"`
	Output []struct {
		Answer       string `json:"answer"`
		FinishReason string `json:"finish_reason"`
	} `json:"output"`
	Usage struct {
		Neurons float64 `json:"neurons"`
	} `json:"usage"`
}

// A non-streaming call returns HTTP 200 with "success":true and an empty result
// when the model runner could not serve the request — the real error only ever
// reaches the SSE channel, so this client always sets stream:true.
func (c *moondreamClient) ask(ctx context.Context, dataURI, question string) (string, error) {
	raw, err := json.Marshal(map[string]any{
		"task":       "query",
		"image":      dataURI,
		"question":   question,
		"max_tokens": 8,
		"stream":     true,
		"reasoning":  false,
	})
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/ai/run/%s", c.accountID, c.model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", retryable{err}
	}
	defer resp.Body.Close()

	// 409 carries "Cog streaming prediction failed" — a transient model-runner
	// fault, not a rejection of the request, and it reappears under concurrency.
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusConflict || resp.StatusCode >= 500 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", retryable{fmt.Errorf("workers-ai http %d: %s", resp.StatusCode, truncate(string(body), 200))}
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("workers-ai http %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var (
		answer   string
		streamed string
		total    float64
	)
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimSpace(line[6:])
		if payload == "[DONE]" {
			break
		}
		var ev moondreamEvent
		if json.Unmarshal([]byte(payload), &ev) != nil {
			continue
		}
		if ev.Error != "" {
			streamed = ev.Error
		}
		if ev.Usage.Neurons > total {
			total = ev.Usage.Neurons
		}
		for _, out := range ev.Output {
			if out.Answer != "" {
				answer = out.Answer
			}
		}
	}
	c.microNeurons.Add(int64(total * 1e6))
	if err := sc.Err(); err != nil {
		return "", retryable{fmt.Errorf("read stream: %w", err)}
	}
	if streamed != "" {
		return "", retryable{fmt.Errorf("workers-ai: %s", streamed)}
	}
	if answer == "" {
		return "", retryable{fmt.Errorf("workers-ai returned no answer")}
	}
	return answer, nil
}

func (c *moondreamClient) askYesNo(ctx context.Context, dataURI, question string) (bool, error) {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		answer, err := c.ask(ctx, dataURI, question)
		if err == nil {
			switch normalizeYesNo(answer) {
			case "yes":
				return true, nil
			case "no":
				return false, nil
			}
			return false, fmt.Errorf("unparsable answer %q", truncate(answer, 60))
		}
		lastErr = err
		if _, retry := err.(retryable); !retry {
			return false, err
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(time.Duration(1<<attempt) * time.Second):
		}
	}
	return false, lastErr
}

func normalizeYesNo(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Trim(s, ".,!\"' \t")
	switch {
	case strings.HasPrefix(s, "yes"):
		return "yes"
	case strings.HasPrefix(s, "no"):
		return "no"
	}
	return ""
}

func dataURI(mime string, body []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(body)
}
