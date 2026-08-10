package wikizh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type paceLimiter struct {
	mu   sync.Mutex
	next time.Time
	gap  time.Duration
}

func newPaceLimiter(rpm int) *paceLimiter {
	if rpm <= 0 {
		return nil
	}
	return &paceLimiter{gap: time.Minute / time.Duration(rpm)}
}

func (p *paceLimiter) wait(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	now := time.Now()
	slot := p.next
	if slot.Before(now) {
		slot = now
	}
	p.next = slot.Add(p.gap)
	p.mu.Unlock()
	d := time.Until(slot)
	if d <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

var retrySchedule = []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second, 60 * time.Second}

type HTTPJudge struct {
	baseURL     string
	token       string
	model       string
	maxTokens   int
	limiter     *paceLimiter
	http        *http.Client
	adversarial bool
}

func (j *HTTPJudge) Adversarial() *HTTPJudge { j.adversarial = true; return j }

func NewHTTPJudge(baseURL, token, model string, maxTokens, rpm int) *HTTPJudge {
	return &HTTPJudge{
		baseURL: strings.TrimRight(baseURL, "/"), token: token, model: model,
		maxTokens: maxTokens, limiter: newPaceLimiter(rpm),
		http: &http.Client{Timeout: 10 * time.Minute},
	}
}

func (j *HTTPJudge) Configured() bool { return j.baseURL != "" && j.token != "" }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature"`
	Messages    []chatMessage `json:"messages"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string      `json:"finish_reason"`
		Message      chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (j *HTTPJudge) JudgeBatch(ctx context.Context, bucket Bucket, cs []Candidate) ([]Verdict, error) {
	if len(cs) == 0 {
		return nil, nil
	}
	packet, system, version := UserPacket, BatchSystemPrompt(bucket), PromptVersion()
	if j.adversarial {
		packet, system, version = AdversarialPacket, BatchAdversarialSystemPrompt(bucket), AdversarialPromptVersion()
	}
	var sb strings.Builder
	for _, c := range cs {
		sb.WriteString(packet(c))
		sb.WriteString("\n")
	}
	content, model, err := j.chat(ctx, system, sb.String())
	if err != nil {
		return nil, err
	}

	byKey := map[string]Verdict{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "`"))
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var v Verdict
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			continue
		}
		byKey[v.Key] = v
	}

	out := make([]Verdict, 0, len(cs))
	for _, c := range cs {
		v, ok := byKey[c.Key()]
		if !ok || !validVerdict(bucket, v.Verdict) {
			v = Verdict{Key: c.Key(), Verdict: VerdictUnsure, Confidence: 0,
				Reason: "模型未返回该条或裁决不在词表内"}
		} else if j.adversarial && bucket == BucketCompare {
			v.Verdict = SwapVerdict(v.Verdict)
			v.Reason = swapNote + v.Reason
		}
		v.WorkID, v.Bucket, v.Model, v.PromptVersion = c.WorkID, bucket, model, version
		out = append(out, v)
	}
	return out, nil
}

func (j *HTTPJudge) chat(ctx context.Context, system, user string) (string, string, error) {
	raw, err := json.Marshal(chatRequest{
		Model: j.model, MaxTokens: j.maxTokens, Temperature: 0,
		Messages: []chatMessage{{Role: "system", Content: system}, {Role: "user", Content: user}},
	})
	if err != nil {
		return "", "", err
	}
	data, err := j.post(ctx, raw)
	if err != nil {
		return "", "", err
	}
	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return "", "", fmt.Errorf("decode chat response: %w (body: %s)", err, truncate(string(data), 300))
	}
	if cr.Error != nil {
		return "", "", fmt.Errorf("gateway error: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return "", "", fmt.Errorf("gateway returned no choices")
	}
	if fr := cr.Choices[0].FinishReason; fr != "" && fr != "stop" {
		return "", "", fmt.Errorf("generation finished with finish_reason=%q — refusing partial output", fr)
	}
	model := cr.Model
	if model == "" {
		model = j.model
	}
	return strings.TrimSpace(cr.Choices[0].Message.Content), model, nil
}

func (j *HTTPJudge) post(ctx context.Context, raw []byte) ([]byte, error) {
	var lastErr error
	for attempt := 0; ; attempt++ {
		if err := j.limiter.wait(ctx); err != nil {
			return nil, err
		}
		data, retryable, err := j.postOnce(ctx, raw)
		if err == nil {
			return data, nil
		}
		lastErr = err
		if !retryable || attempt >= len(retrySchedule) {
			return nil, lastErr
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(retrySchedule[attempt]):
		}
	}
}

func (j *HTTPJudge) postOnce(ctx context.Context, raw []byte) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, j.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+j.token)
	resp, err := j.http.Do(req)
	if err != nil {
		return nil, ctx.Err() == nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, err
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode == http.StatusRequestTimeout,
		resp.StatusCode >= 500:
		return nil, true, fmt.Errorf("gateway http %d: %s", resp.StatusCode, truncate(string(data), 300))
	case resp.StatusCode != http.StatusOK:
		return nil, false, fmt.Errorf("gateway http %d: %s", resp.StatusCode, truncate(string(data), 300))
	}
	return data, false, nil
}

type MockJudge struct {
	Rule func(Candidate) (string, float64)
}

func (m MockJudge) JudgeBatch(_ context.Context, bucket Bucket, cs []Candidate) ([]Verdict, error) {
	out := make([]Verdict, 0, len(cs))
	for _, c := range cs {
		verdict, conf := VerdictUnsure, 0.5
		if m.Rule != nil {
			verdict, conf = m.Rule(c)
		}
		out = append(out, Verdict{Key: c.Key(), WorkID: c.WorkID, Bucket: bucket,
			Verdict: verdict, Confidence: conf, Reason: "mock",
			Model: "mock:" + string(bucket), PromptVersion: PromptVersion()})
	}
	return out, nil
}
