package tagcanon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"api/internal/platform/catalog/model"
)

// Relation is the LLM's cross-source equivalence verdict for a candidate pair
// (doc 87 ruling 1). ONLY RelExact merges into a canonical group/map; every
// other relation is留档 (kept in the JSONL for a future hierarchy wave) and
// never建 schema — the pilot's hard lesson that 层级吞并 / 强度梯度 must not
// collapse into a synonym (催眠⊂精神控制, 微H/H).
type Relation string

const (
	RelExact     Relation = "exact"     // true cross-source synonym → merge
	RelNarrower  Relation = "narrower"  // A ⊂ B (A is a special case of B) → keep, never merge
	RelBroader   Relation = "broader"   // A ⊃ B → keep, never merge
	RelRelated   Relation = "related"   // adjacent but distinct → keep, never merge
	RelUnrelated Relation = "unrelated" // no relation → drop
)

// validRelation guards against a model hallucinating an out-of-vocabulary
// relation — an unknown value is treated as unrelated (the safe, non-merging
// default) by the caller.
func validRelation(r Relation) bool {
	switch r {
	case RelExact, RelNarrower, RelBroader, RelRelated, RelUnrelated:
		return true
	}
	return false
}

// PairInput is one candidate pair presented to the matcher. The ORIGINAL forms
// (AOrig/BOrig) are the primary judging signal — the pilot修正① found zh
// display-name drift (dlsite「教育」 mistranslating 躾/調教) misleads a zh-only
// judge, so the prompt leads with the original (vndb EN / dlsite ja / bgm
// verbatim) and gives the zh display name only as a secondary hint.
type PairInput struct {
	ASourceKey string
	AName      string // zh display name (the source_map key)
	AOrig      string // original form (EN for vndb, ja for dlsite, verbatim for bgm)
	AUsage     int
	BSourceKey string
	BName      string
	BOrig      string
	BUsage     int
}

// PairVerdict is the matcher's judgment for one pair.
type PairVerdict struct {
	Relation   Relation
	Confidence float64
	Reason     string // Chinese, one line — surfaced verbatim in the human review file
}

// NameInput is one single-source name that cleared the usage gate (doc 87
// ruling 4) and needs a tier/kind proposal (a single-source admission row is
// NOT automatically core — a vndb structural/interface tag like 主人公露过正脸
// is expected to land meta or longtail).
type NameInput struct {
	SourceKey string
	Name      string
	Orig      string
	Usage     int
}

// NameVerdict is the matcher's tier/kind proposal for one single-source name.
type NameVerdict struct {
	Tier       int16 // model.TagTier*
	Kind       int16 // model.TagKind*
	Confidence float64
	Reason     string // Chinese, one line
}

// Matcher is the ONLY seam onto the LLM (mirrors intromt.Translator, doc 75).
// The pairing/classification pipeline never speaks HTTP directly, so the whole
// write path is provable offline with MockMatcher and every test injects a
// deterministic fake — no live gateway required. Both methods return the
// effective model id (recorded for accountability) and record-then-continue on
// error (a single bad pair never aborts the batch).
type Matcher interface {
	MatchPair(ctx context.Context, in PairInput) (PairVerdict, string, error)
	ClassifyName(ctx context.Context, in NameInput) (NameVerdict, string, error)
}

// PairMatchSystemPrompt is the PINNED cross-source equivalence prompt (doc 87
// ruling 1, grounded in the pilot's failure modes). Frozen here so a re-run
// wave can diff prompt versions; reasoning output is Chinese (ruling: 判定用
// 中文理由输出). STRICT JSON out.
const PairMatchSystemPrompt = `你是 galgame 标签体系的资深规范化审校。给你两个来自不同数据源的标签,判断它们是否指同一个概念,以便跨源归一。判定要求:
1. 以「原文名」为准(vndb 为英文原名、dlsite 为日文原名、bangumi 为原样中文/日文);中文展示名仅作辅助,遇到译名分歧一律信原文。
2. 关系取值只能是以下之一:
   - exact:两者确为同一概念的同义词,可以安全合并为一个规范标签。
   - narrower:A 是 B 的一个更窄的特例(如「催眠」相对「精神控制」)。
   - broader:A 比 B 更宽泛。
   - related:概念相邻但并不相同(如「福瑞」与「兽人」,「母亲型配角」与「母亲」)。
   - unrelated:没有实质关系。
3. 绝不把层级包含、强度梯度(微H 与 H)、修饰词吞并、表面相似但文化错配的对判为 exact。只有真正等价才给 exact。
4. confidence 为 0 到 1 的小数,表示你对该关系判断的把握。
5. reason 用简体中文一句话说明理由。
只输出一个 JSON 对象,形如 {"relation":"exact","confidence":0.95,"reason":"……"},不要输出任何多余文本或代码块围栏。`

// NameClassifySystemPrompt is the PINNED single-source tier/kind proposal prompt
// (doc 87 ruling 4). STRICT JSON out; Chinese reason.
const NameClassifySystemPrompt = `你是 galgame 标签体系的资深规范化审校。给你一个只在单一数据源出现、但用量较高的标签,请为它提议展示分层(tier)与类别(kind)。判定要求:
1. tier 取值:core(核心,高信噪、值得默认展示的内容标签)、longtail(长尾,较冷门但仍是有效内容标签)、hidden(噪声/不值展示但保留映射)。
2. kind 取值:content(描述作品内容的标签)、meta(平台/发行/体裁/评级等属性,如 PC、R18、同人、像素、ADV;这类不进内容标签云,只作过滤器)。
3. vndb 的结构/界面系标签(如「主人公露过正脸」「多分支结局」「主人公可命名」)通常应判为 meta 或 longtail,而非 core。
4. 以原文名为准判断语义;confidence 为 0 到 1 的把握度;reason 用简体中文一句话。
只输出一个 JSON 对象,形如 {"tier":"core","kind":"content","confidence":0.9,"reason":"……"},不要输出任何多余文本或代码块围栏。`

// ── HTTP matcher (OpenAI-compatible chat completions) ────────────────────────

// HTTPMatcher is an OpenAI-compatible chat-completions matcher (the same wire
// intromt / llmsuggest speak). Its entire config is base URL + bearer token +
// model, taken from env/flag — NEVER hardcoded. In production it points at the
// one-api channel layer (doc 87: CF Workers AI glm-5.2); locally it is
// unconfigured, which is why the pipeline proves itself with MockMatcher.
type HTTPMatcher struct {
	baseURL   string
	token     string
	model     string
	maxTokens int
	http      *http.Client
}

// NewHTTPMatcher builds an OpenAI-compatible matcher.
func NewHTTPMatcher(baseURL, token, model string, maxTokens int) *HTTPMatcher {
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	return &HTTPMatcher{
		baseURL:   strings.TrimRight(baseURL, "/"),
		token:     token,
		model:     model,
		maxTokens: maxTokens,
		http:      &http.Client{Timeout: 600 * time.Second},
	}
}

// Configured reports whether the gateway is wired (base URL AND token set).
func (m *HTTPMatcher) Configured() bool { return m.baseURL != "" && m.token != "" }

type mChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type mChatRequest struct {
	Model       string         `json:"model"`
	Messages    []mChatMessage `json:"messages"`
	MaxTokens   int            `json:"max_tokens"`
	Temperature float64        `json:"temperature"`
}

type mChatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message      mChatMessage `json:"message"`
		FinishReason string       `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// matcherRetrySchedule paces retries on 429/408/5xx/transport errors (a var so
// tests can shrink it) — the same safety valve intromt uses so a batch rides
// out Workers AI's per-minute rate limit instead of bleeding errors.
var matcherRetrySchedule = []time.Duration{2 * time.Second, 8 * time.Second, 30 * time.Second, 60 * time.Second}

// pairVerdictJSON / nameVerdictJSON are the wire shapes the model must emit.
type pairVerdictJSON struct {
	Relation   string  `json:"relation"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

type nameVerdictJSON struct {
	Tier       string  `json:"tier"`
	Kind       string  `json:"kind"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

// MatchPair asks the model for the equivalence relation of one pair.
func (m *HTTPMatcher) MatchPair(ctx context.Context, in PairInput) (PairVerdict, string, error) {
	user := fmt.Sprintf(
		"标签 A(来源 %s):原文名「%s」;中文展示名「%s」;用量 %d\n标签 B(来源 %s):原文名「%s」;中文展示名「%s」;用量 %d",
		in.ASourceKey, in.AOrig, in.AName, in.AUsage,
		in.BSourceKey, in.BOrig, in.BName, in.BUsage)
	content, model, err := m.chat(ctx, PairMatchSystemPrompt, user)
	if err != nil {
		return PairVerdict{}, "", err
	}
	var pv pairVerdictJSON
	if err := json.Unmarshal([]byte(stripFence(content)), &pv); err != nil {
		return PairVerdict{}, "", fmt.Errorf("decode pair verdict: %w (body: %s)", err, mTruncate(content, 300))
	}
	rel := Relation(strings.ToLower(strings.TrimSpace(pv.Relation)))
	if !validRelation(rel) {
		return PairVerdict{}, "", fmt.Errorf("model returned invalid relation %q", pv.Relation)
	}
	return PairVerdict{Relation: rel, Confidence: pv.Confidence, Reason: strings.TrimSpace(pv.Reason)}, model, nil
}

// ClassifyName asks the model for a tier/kind proposal for one single-source name.
func (m *HTTPMatcher) ClassifyName(ctx context.Context, in NameInput) (NameVerdict, string, error) {
	user := fmt.Sprintf("标签(来源 %s):原文名「%s」;中文展示名「%s」;用量 %d",
		in.SourceKey, in.Orig, in.Name, in.Usage)
	content, model, err := m.chat(ctx, NameClassifySystemPrompt, user)
	if err != nil {
		return NameVerdict{}, "", err
	}
	var nv nameVerdictJSON
	if err := json.Unmarshal([]byte(stripFence(content)), &nv); err != nil {
		return NameVerdict{}, "", fmt.Errorf("decode name verdict: %w (body: %s)", err, mTruncate(content, 300))
	}
	tier, ok := parseTier(nv.Tier)
	if !ok {
		return NameVerdict{}, "", fmt.Errorf("model returned invalid tier %q", nv.Tier)
	}
	kind, ok := parseKind(nv.Kind)
	if !ok {
		return NameVerdict{}, "", fmt.Errorf("model returned invalid kind %q", nv.Kind)
	}
	return NameVerdict{Tier: tier, Kind: kind, Confidence: nv.Confidence, Reason: strings.TrimSpace(nv.Reason)}, model, nil
}

// chat runs one temperature-0 chat completion and returns the reply content +
// effective model id.
func (m *HTTPMatcher) chat(ctx context.Context, system, user string) (string, string, error) {
	body := mChatRequest{
		Model:       m.model,
		MaxTokens:   m.maxTokens,
		Temperature: 0,
		Messages: []mChatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", "", err
	}
	data, err := m.post(ctx, raw)
	if err != nil {
		return "", "", err
	}
	var cr mChatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return "", "", fmt.Errorf("decode chat response: %w (body: %s)", err, mTruncate(string(data), 300))
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
		model = m.model
	}
	return strings.TrimSpace(cr.Choices[0].Message.Content), model, nil
}

// post retries 429/408/5xx/transport failures per matcherRetrySchedule.
func (m *HTTPMatcher) post(ctx context.Context, raw []byte) ([]byte, error) {
	var lastErr error
	for attempt := 0; ; attempt++ {
		data, retryable, err := m.postOnce(ctx, raw)
		if err == nil {
			return data, nil
		}
		lastErr = err
		if !retryable || attempt >= len(matcherRetrySchedule) {
			return nil, lastErr
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(matcherRetrySchedule[attempt]):
		}
	}
}

func (m *HTTPMatcher) postOnce(ctx context.Context, raw []byte) (body []byte, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.token)

	resp, err := m.http.Do(req)
	if err != nil {
		return nil, ctx.Err() == nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, err
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("gateway http %d: %s", resp.StatusCode, mTruncate(string(data), 300))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("gateway http %d: %s", resp.StatusCode, mTruncate(string(data), 300))
	}
	return data, false, nil
}

// ── mock matcher (offline, deterministic) ────────────────────────────────────

// MockMatcher is a deterministic, offline stand-in used ONLY by the rehearsal
// full-apply and tests to prove the pipeline WITHOUT a live gateway. Its
// verdicts are a pure function of the input, so the chain is идемпотентен and
// every relation branch (exact / narrower / broader / related / unrelated) is
// exercised. It stamps a "mock:" model prefix so a mock verdict that ever
// leaked to a real batch is unmistakable. The rules mirror the deterministic
// signals the real prompt keys on:
//   - equal normalized originals (or equal norms) → exact;
//   - one original contains the other → the shorter is broader, longer narrower;
//   - small edit distance on norms → related;
//   - otherwise → unrelated.
type MockMatcher struct{ Model string }

func (m MockMatcher) model() string {
	if m.Model == "" {
		return "mock:stub"
	}
	return "mock:" + m.Model
}

// MatchPair applies the deterministic relation rules above.
func (m MockMatcher) MatchPair(_ context.Context, in PairInput) (PairVerdict, string, error) {
	oa, ob := normalize(in.AOrig), normalize(in.BOrig)
	na, nb := normalize(in.AName), normalize(in.BName)
	switch {
	case (oa != "" && oa == ob) || na == nb:
		return PairVerdict{Relation: RelExact, Confidence: 0.99, Reason: "mock:原文/展示名规范化后相等,判为同义"}, m.model(), nil
	case containsWord(na, nb) || containsWord(oa, ob):
		// A contains B → B is the narrower/more-specific, A the broader.
		return PairVerdict{Relation: RelBroader, Confidence: 0.82, Reason: "mock:A 字面包含 B,A 更宽泛"}, m.model(), nil
	case containsWord(nb, na) || containsWord(ob, oa):
		return PairVerdict{Relation: RelNarrower, Confidence: 0.82, Reason: "mock:B 字面包含 A,A 更狭窄"}, m.model(), nil
	case levenshtein(na, nb) <= 1:
		return PairVerdict{Relation: RelRelated, Confidence: 0.6, Reason: "mock:编辑距离很近但不等,判为相邻"}, m.model(), nil
	default:
		return PairVerdict{Relation: RelUnrelated, Confidence: 0.4, Reason: "mock:无实质关系"}, m.model(), nil
	}
}

// ClassifyName applies deterministic tier/kind rules: meta by the hand-pinned
// set; tier by usage bucket (≥1000 core, otherwise longtail).
func (m MockMatcher) ClassifyName(_ context.Context, in NameInput) (NameVerdict, string, error) {
	kind := model.TagKindContent
	if isMeta(normalize(in.Name)) || isMeta(normalize(in.Orig)) {
		kind = model.TagKindMeta
	}
	tier := model.TagTierLongtail
	reason := "mock:单源长尾,默认折叠"
	if kind == model.TagKindMeta {
		tier = model.TagTierHidden
		reason = "mock:平台/属性 meta,不进内容标签云"
	} else if in.Usage >= 1000 {
		tier = model.TagTierCore
		reason = "mock:单源高用量内容标签,提升为核心"
	}
	return NameVerdict{Tier: tier, Kind: kind, Confidence: 0.9, Reason: reason}, m.model(), nil
}

// ── small helpers ────────────────────────────────────────────────────────────

// parseTier / parseKind map the model's string labels onto the int16 constants.
func parseTier(s string) (int16, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "core", "0":
		return model.TagTierCore, true
	case "longtail", "long-tail", "1":
		return model.TagTierLongtail, true
	case "hidden", "2":
		return model.TagTierHidden, true
	}
	return 0, false
}

func parseKind(s string) (int16, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "content", "0":
		return model.TagKindContent, true
	case "meta", "1":
		return model.TagKindMeta, true
	}
	return 0, false
}

// stripFence tolerates a model that wraps its JSON in a ```json fence despite
// the instruction not to.
func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return strings.TrimSpace(s)
}

func mTruncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
