package personadj

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

// paceLimiter is a minimal evenly-spaced request pacer. The gateway quota is
// "inference requests per minute" per account, so an even trickle is strictly
// better than a burst followed by a stall — and worker count alone cannot pace
// anything, since short prompts finish fast and N workers then issue far more
// than N requests a minute (wave 156 measured 24% 429s at 20 unpaced workers).
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

// wait blocks until this caller's slot is due.
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
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// BatchJudge is implemented by judges that can answer several packets in ONE
// request. Every packet in a chunk shares a bucket (and therefore a prompt).
type BatchJudge interface {
	JudgeBatch(ctx context.Context, ps []Packet) ([]Verdict, error)
}

// Judge is the ONLY seam onto the LLM (mirrors tagcanon.Matcher / intromt.
// Translator), so the whole batch pipeline — resume, checkpointing, tiering —
// is provable offline against MockJudge with no gateway credentials anywhere
// near a test.
type Judge interface {
	Judge(ctx context.Context, p Packet) (Verdict, error)
}

// HTTPJudge speaks the OpenAI-compatible chat-completions wire (the CF Workers
// AI gateway, doc 87). Config is base URL + token + model, taken from env or
// flags and NEVER hardcoded or logged.
type HTTPJudge struct {
	baseURL   string
	token     string
	model     string
	maxTokens int
	http      *http.Client
	limiter   *paceLimiter
}

// NewHTTPJudge builds the live judge. maxTokens must be generous: glm-5.2 is a
// thinking model and a short cap truncates the answer mid-reasoning, which
// shows up as finish_reason=length and is refused below.
func NewHTTPJudge(baseURL, token, model string, maxTokens, rpm int) *HTTPJudge {
	if maxTokens <= 0 {
		maxTokens = 24576
	}
	return &HTTPJudge{
		limiter:   newPaceLimiter(rpm),
		baseURL:   strings.TrimRight(baseURL, "/"),
		token:     token,
		model:     model,
		maxTokens: maxTokens,
		http:      &http.Client{Timeout: 900 * time.Second},
	}
}

// Configured reports whether the gateway is wired.
func (j *HTTPJudge) Configured() bool { return j.baseURL != "" && j.token != "" }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature float64       `json:"temperature"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type verdictJSON struct {
	Verdict       string   `json:"verdict"`
	Confidence    float64  `json:"confidence"`
	EntityKind    string   `json:"entity_kind"`
	DetachSources []string `json:"detach_sources"`
	Reason        string   `json:"reason"`
}

// retrySchedule paces 429/408/5xx/transport retries (a var so tests shrink it).
// The gateway's limit is "inference requests per minute" per account, so a
// burst that exhausts it needs to wait out a WHOLE minute, not milliseconds:
// the tail of the schedule is deliberately longer than 60s.
var retrySchedule = []time.Duration{5 * time.Second, 20 * time.Second, 45 * time.Second,
	70 * time.Second, 90 * time.Second, 120 * time.Second}

// Judge asks the model for one packet's verdict.
func (j *HTTPJudge) Judge(ctx context.Context, p Packet) (Verdict, error) {
	content, model, err := j.chat(ctx, SystemPrompt(p.Bucket), p.User)
	if err != nil {
		return Verdict{}, err
	}
	return parseVerdict(p, content, model)
}

// parseVerdict turns a completion into a Verdict, refusing anything outside the
// three-value vocabulary rather than defaulting it to unsure — an unparsed
// answer must show up in the batch's error count, not as a real judgement.
func parseVerdict(p Packet, content, model string) (Verdict, error) {
	var vj verdictJSON
	if err := json.Unmarshal([]byte(stripFence(content)), &vj); err != nil {
		return Verdict{}, fmt.Errorf("decode verdict: %w (body: %s)", err, truncate(content, 300))
	}
	v := strings.ToLower(strings.TrimSpace(vj.Verdict))
	if !validVerdict(v) {
		return Verdict{}, fmt.Errorf("model returned invalid verdict %q", vj.Verdict)
	}
	out := Verdict{
		Key:        p.Key,
		Bucket:     p.Bucket,
		Verdict:    v,
		Confidence: vj.Confidence,
		Reason:     strings.TrimSpace(vj.Reason),
		Model:      model,
	}
	if p.Bucket != BucketCharacterCV {
		out.EntityKind = normalizeKind(vj.EntityKind)
	}
	if p.Bucket == BucketE4Split {
		for _, s := range vj.DetachSources {
			if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
				out.DetachSources = append(out.DetachSources, s)
			}
		}
	}
	return out, nil
}

// JudgeBatch judges several packets of one bucket in a single request. It
// returns an error for the WHOLE chunk when the reply does not line up with the
// input — the caller re-judges those packets one at a time rather than risking
// verdicts landing on the wrong pairs.
func (j *HTTPJudge) JudgeBatch(ctx context.Context, ps []Packet) ([]Verdict, error) {
	if len(ps) == 0 {
		return nil, nil
	}
	if len(ps) == 1 {
		v, err := j.Judge(ctx, ps[0])
		if err != nil {
			return nil, err
		}
		return []Verdict{v}, nil
	}
	var sb strings.Builder
	for i, p := range ps {
		fmt.Fprintf(&sb, "### 案例 %d\n%s\n\n", i+1, p.User)
	}
	content, model, err := j.chat(ctx, BatchSystemPrompt(ps[0].Bucket), sb.String())
	if err != nil {
		return nil, err
	}
	return parseBatch(ps, content, model)
}

// parseBatch decodes the array reply and pins it back to the input by id.
func parseBatch(ps []Packet, content, model string) ([]Verdict, error) {
	var items []struct {
		ID int `json:"id"`
		verdictJSON
	}
	if err := json.Unmarshal([]byte(stripArrayFence(content)), &items); err != nil {
		return nil, fmt.Errorf("decode batch verdicts: %w (body: %s)", err, truncate(content, 300))
	}
	if len(items) != len(ps) {
		return nil, fmt.Errorf("batch returned %d verdicts for %d cases", len(items), len(ps))
	}
	out := make([]Verdict, 0, len(ps))
	for i, it := range items {
		if it.ID != i+1 {
			return nil, fmt.Errorf("batch item %d carries id %d — refusing a misaligned chunk", i+1, it.ID)
		}
		raw, err := json.Marshal(it.verdictJSON)
		if err != nil {
			return nil, err
		}
		v, err := parseVerdict(ps[i], string(raw), model)
		if err != nil {
			return nil, fmt.Errorf("batch item %d: %w", i+1, err)
		}
		out = append(out, v)
	}
	return out, nil
}

func (j *HTTPJudge) chat(ctx context.Context, system, user string) (string, string, error) {
	raw, err := json.Marshal(chatRequest{
		Model:       j.model,
		MaxTokens:   j.maxTokens,
		Temperature: 0,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
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
	// A thinking model that runs out of budget still returns well-formed-looking
	// prose; only finish_reason distinguishes it from a finished answer.
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

func (j *HTTPJudge) postOnce(ctx context.Context, raw []byte) (body []byte, retryable bool, err error) {
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
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("gateway http %d: %s", resp.StatusCode, truncate(string(data), 300))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("gateway http %d: %s", resp.StatusCode, truncate(string(data), 300))
	}
	return data, false, nil
}

// MockJudge is the deterministic offline stand-in. Its rule is a pure function
// of the packet so the batch pipeline is reproducible in tests; it stamps a
// "mock:" model prefix so a mock verdict that ever leaked into a real batch is
// unmistakable.
type MockJudge struct {
	// Rule, when set, decides; otherwise every packet is judged unsure, which
	// is the safe default (nothing automatic ever follows from unsure).
	Rule func(Packet) (string, float64)
}

// Judge applies Rule.
func (m MockJudge) Judge(_ context.Context, p Packet) (Verdict, error) {
	verdict, conf := VerdictUnsure, 0.5
	if m.Rule != nil {
		verdict, conf = m.Rule(p)
	}
	if !validVerdict(verdict) {
		return Verdict{}, fmt.Errorf("mock produced invalid verdict %q", verdict)
	}
	v := Verdict{
		Key: p.Key, Bucket: p.Bucket, Verdict: verdict, Confidence: conf,
		Reason: "mock:确定性判定", Model: "mock:" + promptVersion,
	}
	if p.Bucket != BucketCharacterCV {
		v.EntityKind = KindPerson
	}
	return v, nil
}

// stripFence tolerates a model that wraps its JSON in a ``` fence, and pulls
// the object out of any surrounding prose a thinking model may have leaked.
func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}
	if strings.HasPrefix(s, "{") {
		return s
	}
	if i, j := strings.Index(s, "{"), strings.LastIndex(s, "}"); i >= 0 && j > i {
		return s[i : j+1]
	}
	return s
}

// stripArrayFence is stripFence for an array reply.
func stripArrayFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}
	if strings.HasPrefix(s, "[") {
		return s
	}
	if i, j := strings.Index(s, "["), strings.LastIndex(s, "]"); i >= 0 && j > i {
		return s[i : j+1]
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
