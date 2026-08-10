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

type Relation string

const (
	RelExact     Relation = "exact"
	RelNarrower  Relation = "narrower"
	RelBroader   Relation = "broader"
	RelRelated   Relation = "related"
	RelUnrelated Relation = "unrelated"
)

func validRelation(r Relation) bool {
	switch r {
	case RelExact, RelNarrower, RelBroader, RelRelated, RelUnrelated:
		return true
	}
	return false
}

type PairInput struct {
	ASourceKey string
	AName      string
	AOrig      string
	AUsage     int
	BSourceKey string
	BName      string
	BOrig      string
	BUsage     int
}

type PairVerdict struct {
	Relation   Relation
	Confidence float64
	Reason     string
}

type NameInput struct {
	SourceKey string
	Name      string
	Orig      string
	Usage     int
}

type NameVerdict struct {
	Tier       int16
	Kind       int16
	Confidence float64
	Reason     string
}

type Matcher interface {
	MatchPair(ctx context.Context, in PairInput) (PairVerdict, string, error)
	ClassifyName(ctx context.Context, in NameInput) (NameVerdict, string, error)
}

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

const NameClassifySystemPrompt = `你是 galgame 标签体系的资深规范化审校。给你一个只在单一数据源出现、但用量较高的标签,请为它提议展示分层(tier)与类别(kind)。判定要求:
1. tier 取值:core(核心,高信噪、值得默认展示的内容标签)、longtail(长尾,较冷门但仍是有效内容标签)、hidden(噪声/不值展示但保留映射)。
2. kind 取值:content(描述作品内容的标签)、meta(平台/发行/体裁/评级等属性,如 PC、R18、同人、像素、ADV;这类不进内容标签云,只作过滤器)。
3. vndb 的结构/界面系标签(如「主人公露过正脸」「多分支结局」「主人公可命名」)通常应判为 meta 或 longtail,而非 core。
4. 以原文名为准判断语义;confidence 为 0 到 1 的把握度;reason 用简体中文一句话。
只输出一个 JSON 对象,形如 {"tier":"core","kind":"content","confidence":0.9,"reason":"……"},不要输出任何多余文本或代码块围栏。`

type HTTPMatcher struct {
	baseURL   string
	token     string
	model     string
	maxTokens int
	http      *http.Client
}

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

var matcherRetrySchedule = []time.Duration{2 * time.Second, 8 * time.Second, 30 * time.Second, 60 * time.Second}

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

type MockMatcher struct{ Model string }

func (m MockMatcher) model() string {
	if m.Model == "" {
		return "mock:stub"
	}
	return "mock:" + m.Model
}

func (m MockMatcher) MatchPair(_ context.Context, in PairInput) (PairVerdict, string, error) {
	oa, ob := normalize(in.AOrig), normalize(in.BOrig)
	na, nb := normalize(in.AName), normalize(in.BName)
	switch {
	case (oa != "" && oa == ob) || na == nb:
		return PairVerdict{Relation: RelExact, Confidence: 0.99, Reason: "mock:原文/展示名规范化后相等,判为同义"}, m.model(), nil
	case containsWord(na, nb) || containsWord(oa, ob):
		return PairVerdict{Relation: RelBroader, Confidence: 0.82, Reason: "mock:A 字面包含 B,A 更宽泛"}, m.model(), nil
	case containsWord(nb, na) || containsWord(ob, oa):
		return PairVerdict{Relation: RelNarrower, Confidence: 0.82, Reason: "mock:B 字面包含 A,A 更狭窄"}, m.model(), nil
	case levenshtein(na, nb) <= 1:
		return PairVerdict{Relation: RelRelated, Confidence: 0.6, Reason: "mock:编辑距离很近但不等,判为相邻"}, m.model(), nil
	default:
		return PairVerdict{Relation: RelUnrelated, Confidence: 0.4, Reason: "mock:无实质关系"}, m.model(), nil
	}
}

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
