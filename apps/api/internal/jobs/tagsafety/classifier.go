// Package tagsafety classifies the Bangumi / DLsite folksonomy vocabulary (and
// the canonical catalog_tag vocabulary) on two axes the ingest path never
// filled: the SEXUAL safety flag and a JUNK verdict for names with no retrieval
// value. Measured on the 2026-08 snapshot, catalog_work_tag carries 215,022
// bangumi rows over 12,259 distinct names with sexual = false on EVERY row —
// the axis was simply never classified, not classified-as-safe. DLsite is the
// same story (38,605 rows / 373 names). Bangumi-only canonicals in catalog_tag
// inherit that same false-by-construction.
//
// The package follows the tagcanon (doc 87/90) shape exactly: ONE seam onto the
// LLM (Classifier), a pinned Chinese system prompt const so a re-run wave can
// diff prompt versions, strict JSON out, and a deterministic offline
// MockClassifier so the whole write path is provable without a gateway.
package tagsafety

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

// Class is the safety/junk verdict for one tag name. Exactly three values — a
// name is either an adult-content marker, retrieval noise, or an ordinary
// content tag. An unknown value from the model is rejected (never coerced to a
// default) so a hallucinated label can never silently become a write.
type Class string

const (
	ClassSexual Class = "sexual" // adult/pornographic content marker → sexual=true
	ClassJunk   Class = "junk"   // no retrieval value → tier=hidden (mapped names only)
	ClassNormal Class = "normal" // ordinary content tag → no write
)

// validClass guards against an out-of-vocabulary label.
func validClass(c Class) bool {
	switch c {
	case ClassSexual, ClassJunk, ClassNormal:
		return true
	}
	return false
}

// NameInput is one tag name presented to the classifier. Uses (distinct works
// carrying the name) and Votes (summed folksonomy count) are handed to the model
// as popularity context — a name used on 8,000 works is far more likely to be a
// site-wide truism ("游戏", "Galgame") than a genuine discriminator.
type NameInput struct {
	Source string // registry key ("bangumi" / "dlsite") or VocabSource
	Name   string
	Uses   int
	Votes  int
}

// NameVerdict is the model's judgment for one name. Name echoes the input name
// so a batched reply can be verified position-by-position (see parseBatch).
type NameVerdict struct {
	Name       string
	Class      Class
	Confidence float64
	Reason     string // Chinese, one line — surfaced verbatim in the human review file
}

// Classifier is the ONLY seam onto the LLM (mirrors tagcanon.Matcher, doc 87).
// The classify pipeline never speaks HTTP directly, so every test injects a
// deterministic fake and no live gateway is required.
//
// BATCHING IS THE PINNED APPROACH (not one call per name): 12,259 bangumi names
// at one round trip each is ~12k calls against a rate-limited gateway, while a
// tag name is a few tokens of context — batching N names per prompt is the same
// judgment at ~1/N the calls. The cost is alignment risk, which the wire format
// removes by making the model echo BOTH a 1-based index and the name verbatim;
// parseBatch drops any item whose echo does not match its slot rather than
// guessing (a misaligned verdict would write the wrong tag's sexual flag).
//
// ClassifyBatch returns a slice the SAME LENGTH as in, index-aligned. An entry
// the model omitted or garbled comes back with an empty Class; the caller counts
// it as an error and does not persist it, so a resumed run re-asks for it.
type Classifier interface {
	ClassifyBatch(ctx context.Context, in []NameInput) ([]NameVerdict, string, error)
}

// ClassifySystemPrompt is the PINNED tag safety/junk prompt. Frozen here so a
// re-run wave can diff prompt versions; reasoning output is Chinese (the same
// ruling tagcanon follows). STRICT JSON out.
//
// The two negative rulings in points 3 and 4 are the ones that matter: genre
// words (RPG/SLG/乙女/BL/百合) and state/meta words (汉化/生肉/合集/短篇) are NOT
// junk — the latter belong to the catalog_tag kind=meta axis, which is a
// different wave's job, and folding them into junk here would hide a whole
// filter facet behind tier=hidden.
const ClassifySystemPrompt = `你是 galgame 标签体系的资深内容审校。给你一批标签名(来自 bangumi / dlsite 的用户标签或规范标签表),为每个标签判定一个类别。

类别只能取以下三者之一:
1. sexual —— 标签本身指向成人/色情内容(如 拔作、R18、18禁、NTR、调教、中出、凌辱、触手、后宫肉 等)。注意:仅暗示恋爱或年龄向的不算 sexual;「纯爱」「恋爱」判 normal,「全年龄」判 normal。
2. junk —— 对 galgame 条目检索毫无区分价值的标签:
   - 平台/载体词:PC、Windows、Android、iOS、Web、STEAM、DMM、主机;
   - 全站恒真词(几乎每个条目都成立):游戏、Galgame、GAL、GALGAME、AVG、ADV、黄油、EROGE、エロゲ、VN、视觉小说;
   - 具体人名、声优名、社团名、公司名、品牌名、具体作品原名(标签不该是一个专有名词实体);
   - 无意义碎语、单个数字、日期、乱码、纯标点。
3. normal —— 其余一切有效的内容标签。

必须遵守的反例(判错这几类是本次任务最严重的错误):
- 类型/题材词不是 junk:RPG、SLG、AVG 之外的玩法词、乙女、BL、百合、悬疑、科幻、治愈 一律判 normal(其中 AVG/ADV 因为全站恒真才算 junk,其他玩法词不是)。
- 状态/元信息词不是 junk:汉化、生肉、官中、合集、短篇、长篇、未完成、同人 一律判 normal —— 这些由规范标签表的 kind=meta 轴单独处理,不归本次判定。
- 拿不准时判 normal 并给低 confidence,不要为了「像是垃圾」就判 junk。

其他要求:
- confidence 为 0 到 1 的小数,表示你对该判定的把握;把握不足时如实给低分,人工会复审。
- reason 用简体中文一句话说明理由。
- 输入每行形如「序号. 标签名(用量 uses/votes)」;你必须为每一行都输出一条结果,index 与序号一一对应,name 必须与输入标签名逐字相同。

只输出一个 JSON 对象,形如
{"results":[{"index":1,"name":"拔作","class":"sexual","confidence":0.97,"reason":"……"}]}
不要输出任何多余文本或代码块围栏。`

// ── HTTP classifier (OpenAI-compatible chat completions) ─────────────────────

// HTTPClassifier is an OpenAI-compatible chat-completions classifier (the same
// wire tagcanon / intromt speak). Its entire config is base URL + bearer token +
// model, taken from env/flag — NEVER hardcoded. In production it points at the
// one-api channel layer (glm-5.2); locally it is unconfigured, which is why the
// pipeline proves itself with MockClassifier.
type HTTPClassifier struct {
	baseURL   string
	token     string
	model     string
	maxTokens int
	http      *http.Client
}

// NewHTTPClassifier builds an OpenAI-compatible classifier.
func NewHTTPClassifier(baseURL, token, model string, maxTokens int) *HTTPClassifier {
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	return &HTTPClassifier{
		baseURL:   strings.TrimRight(baseURL, "/"),
		token:     token,
		model:     model,
		maxTokens: maxTokens,
		http:      &http.Client{Timeout: 600 * time.Second},
	}
}

// Configured reports whether the gateway is wired (base URL AND token set).
func (c *HTTPClassifier) Configured() bool { return c.baseURL != "" && c.token != "" }

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

// batchReplyJSON is the wire shape the model must emit.
type batchReplyJSON struct {
	Results []struct {
		Index      int     `json:"index"`
		Name       string  `json:"name"`
		Class      string  `json:"class"`
		Confidence float64 `json:"confidence"`
		Reason     string  `json:"reason"`
	} `json:"results"`
}

// retrySchedule paces retries on 429/408/5xx/transport errors (a var so tests
// can shrink it) — the same safety valve tagcanon uses so a batch rides out the
// gateway's per-minute rate limit instead of bleeding errors.
var retrySchedule = []time.Duration{2 * time.Second, 8 * time.Second, 30 * time.Second, 60 * time.Second}

// ClassifyBatch asks the model to judge a whole batch in one call, retrying the
// PARSE once (a fresh completion) when the reply is malformed — the tagcanon
// convention: transport failures retry inside post, semantic failures retry
// exactly once here, and anything still broken is an error the caller records
// and moves past.
func (c *HTTPClassifier) ClassifyBatch(ctx context.Context, in []NameInput) ([]NameVerdict, string, error) {
	if len(in) == 0 {
		return nil, "", nil
	}
	user := renderBatchPrompt(in)
	var lastErr error
	for attempt := range 2 {
		content, model, err := c.chat(ctx, ClassifySystemPrompt, user)
		if err != nil {
			return nil, "", err
		}
		out, err := parseBatch(content, in)
		if err == nil {
			return out, model, nil
		}
		lastErr = err
		_ = attempt // second pass is the single pinned retry
	}
	return nil, "", lastErr
}

// renderBatchPrompt formats the batch as a 1-based numbered list. Popularity is
// given as uses/votes so the model can spot site-wide truisms.
func renderBatchPrompt(in []NameInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "数据源:%s\n以下是待判定的标签(共 %d 条):\n", in[0].Source, len(in))
	for i, n := range in {
		fmt.Fprintf(&b, "%d. %s(用量 %d/%d)\n", i+1, n.Name, n.Uses, n.Votes)
	}
	return b.String()
}

// parseBatch decodes the strict-JSON reply and aligns it back onto the input by
// index, verifying the echoed name. Alignment is the whole risk of batching, so
// the rules are deliberately unforgiving:
//
//   - an out-of-range index, a name echo that does not match its slot, or an
//     invalid class → that ONE slot is left empty (the caller re-asks on resume);
//   - a duplicate index → the later one is ignored (first wins, deterministic);
//   - a reply with no parsable results at all → an error, which triggers the
//     single pinned retry.
//
// It never re-matches by name across slots: a model that returns the right names
// in the wrong order is a model whose output we do not trust to write with.
func parseBatch(content string, in []NameInput) ([]NameVerdict, error) {
	var reply batchReplyJSON
	if err := json.Unmarshal([]byte(stripFence(content)), &reply); err != nil {
		return nil, fmt.Errorf("decode batch reply: %w (body: %s)", err, truncate(content, 300))
	}
	out := make([]NameVerdict, len(in))
	filled := 0
	for _, r := range reply.Results {
		i := r.Index - 1
		if i < 0 || i >= len(in) {
			continue
		}
		if out[i].Class != "" {
			continue // duplicate index — first wins
		}
		if strings.TrimSpace(r.Name) != in[i].Name {
			continue // echo mismatch — refuse to guess the alignment
		}
		cls := Class(strings.ToLower(strings.TrimSpace(r.Class)))
		if !validClass(cls) {
			continue
		}
		out[i] = NameVerdict{Name: in[i].Name, Class: cls, Confidence: r.Confidence, Reason: strings.TrimSpace(r.Reason)}
		filled++
	}
	if filled == 0 {
		return nil, fmt.Errorf("batch reply aligned zero of %d names (body: %s)", len(in), truncate(content, 300))
	}
	return out, nil
}

// chat runs one temperature-0 chat completion and returns the reply content +
// effective model id.
func (c *HTTPClassifier) chat(ctx context.Context, system, user string) (string, string, error) {
	raw, err := json.Marshal(chatRequest{
		Model:       c.model,
		MaxTokens:   c.maxTokens,
		Temperature: 0,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	})
	if err != nil {
		return "", "", err
	}
	data, err := c.post(ctx, raw)
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
		// A truncated batch reply is the one failure that silently loses names,
		// so a non-stop finish is refused outright rather than half-parsed.
		return "", "", fmt.Errorf("generation finished with finish_reason=%q — refusing partial output", fr)
	}
	model := cr.Model
	if model == "" {
		model = c.model
	}
	return strings.TrimSpace(cr.Choices[0].Message.Content), model, nil
}

// post retries 429/408/5xx/transport failures per retrySchedule.
func (c *HTTPClassifier) post(ctx context.Context, raw []byte) ([]byte, error) {
	var lastErr error
	for attempt := 0; ; attempt++ {
		data, retryable, err := c.postOnce(ctx, raw)
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

func (c *HTTPClassifier) postOnce(ctx context.Context, raw []byte) (body []byte, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
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

// ── mock classifier (offline, deterministic) ─────────────────────────────────

// mockSexual / mockJunk are the deterministic keyword sets MockClassifier keys
// on — a small hand-pinned echo of the prompt's own examples, NOT a production
// lexicon. They exist so the offline rehearsal exercises all three branches.
var mockSexual = []string{"r18", "18禁", "拔作", "ntr", "调教", "中出", "凌辱", "触手", "エロ", "hentai"}

var mockJunk = map[string]struct{}{
	"pc": {}, "windows": {}, "android": {}, "ios": {}, "web": {}, "steam": {},
	"游戏": {}, "galgame": {}, "gal": {}, "avg": {}, "adv": {}, "黄油": {}, "eroge": {}, "vn": {}, "视觉小说": {},
}

// MockClassifier is a deterministic, offline stand-in used ONLY by the rehearsal
// run and tests to prove the pipeline WITHOUT a live gateway. Its verdicts are a
// pure function of the input, so a rehearsal is reproducible and every class
// branch is exercised. It stamps a "mock:" model prefix so a mock verdict that
// ever leaked into a real batch is unmistakable in the JSONL.
type MockClassifier struct{ Model string }

func (m MockClassifier) model() string {
	if m.Model == "" {
		return "mock:stub"
	}
	return "mock:" + m.Model
}

// ClassifyBatch applies the deterministic rules: exact-match junk vocabulary
// first, then a sexual substring hit, otherwise normal.
func (m MockClassifier) ClassifyBatch(_ context.Context, in []NameInput) ([]NameVerdict, string, error) {
	out := make([]NameVerdict, len(in))
	for i, n := range in {
		low := strings.ToLower(strings.TrimSpace(n.Name))
		switch {
		case isMockJunk(low):
			out[i] = NameVerdict{Name: n.Name, Class: ClassJunk, Confidence: 0.95, Reason: "mock:平台/恒真词,无检索区分价值"}
		case containsAny(low, mockSexual):
			out[i] = NameVerdict{Name: n.Name, Class: ClassSexual, Confidence: 0.97, Reason: "mock:命中成人内容关键词"}
		default:
			out[i] = NameVerdict{Name: n.Name, Class: ClassNormal, Confidence: 0.85, Reason: "mock:普通内容标签"}
		}
	}
	return out, m.model(), nil
}

func isMockJunk(low string) bool { _, ok := mockJunk[low]; return ok }

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// ── small helpers ────────────────────────────────────────────────────────────

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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
