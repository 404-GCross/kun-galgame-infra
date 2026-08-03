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

// NOTE ON DUPLICATION. The transport below — even pacing, the retry schedule,
// the finish_reason guard — mirrors internal/jobs/personadj/judge.go, whose
// verdict vocabulary (merge/distinct/unsure) this wave cannot reuse. Extracting
// the shared transport into one package is the right end state and is recorded
// as a follow-up in refs/proj/168; it was not done inside this wave because it
// would mean refactoring a live wave-156 production lane. Both copies carry the
// same measured lessons, listed where they apply.

// paceLimiter spaces requests evenly. The gateway's quota is inference requests
// per minute per ACCOUNT, so worker count alone paces nothing: short prompts
// finish fast and N workers then issue far more than N requests a minute. Wave
// 156 measured 24% 429s at 20 unpaced workers.
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

// HTTPJudge talks an OpenAI-compatible chat-completions endpoint.
type HTTPJudge struct {
	baseURL   string
	token     string
	model     string
	maxTokens int
	limiter   *paceLimiter
	http      *http.Client
}

func NewHTTPJudge(baseURL, token, model string, maxTokens, rpm int) *HTTPJudge {
	return &HTTPJudge{
		baseURL: strings.TrimRight(baseURL, "/"), token: token, model: model,
		maxTokens: maxTokens, limiter: newPaceLimiter(rpm),
		// A reasoning model can spend minutes before the first byte on a chunk
		// of long intros; this is a wall ceiling, not a liveness knob.
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

// JudgeBatch judges a chunk in one request and parses one JSON object per line.
//
// A packet whose verdict does not come back is NOT silently dropped: it is
// returned as unsure, so the caller's counts always add up and a partially
// answered chunk cannot quietly shrink the population.
func (j *HTTPJudge) JudgeBatch(ctx context.Context, bucket Bucket, cs []Candidate) ([]Verdict, error) {
	if len(cs) == 0 {
		return nil, nil
	}
	var sb strings.Builder
	for _, c := range cs {
		sb.WriteString(UserPacket(c))
		sb.WriteString("\n")
	}
	content, model, err := j.chat(ctx, BatchSystemPrompt(bucket), sb.String())
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
			continue // a malformed line becomes a missing key, handled below
		}
		byKey[v.Key] = v
	}

	out := make([]Verdict, 0, len(cs))
	for _, c := range cs {
		v, ok := byKey[c.Key()]
		if !ok || !validVerdict(bucket, v.Verdict) {
			v = Verdict{Key: c.Key(), Verdict: VerdictUnsure, Confidence: 0,
				Reason: "模型未返回该条或裁决不在词表内"}
		}
		v.WorkID, v.Bucket, v.Model, v.PromptVersion = c.WorkID, bucket, model, PromptVersion()
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
	// A reasoning model that exhausts its budget still returns well-formed
	// prose; only finish_reason tells a finished answer from a truncated one.
	// Wave 75 lost translations to exactly this before the guard existed.
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

// MockJudge is the deterministic offline stand-in; it stamps a "mock:" model
// prefix so a mock verdict that ever leaked into a real batch is unmistakable.
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
