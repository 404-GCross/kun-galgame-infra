package entityintromt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Translator turns one entity intro into a zh-Hans translation. It is the ONLY
// seam onto the LLM: the runner never speaks HTTP directly, so tests inject a
// deterministic fake and the rehearsal full-apply runs a mock — no live gateway
// is needed to prove the write path. Translate returns the translated text plus
// the effective model id that produced it (recorded in mt_model for
// accountability); an error is recorded and the run continues (a single item's
// failure never aborts the batch).
//
// Unlike the work job, the source language is a PER-CALL argument: one entity
// run mixes ja and en candidates (the source is chosen per entity, see the
// package doc), so a translator instance cannot carry a lane-wide language.
//
// gloss is likewise per CALL: it is the candidate's own term list (glossary.go)
// — the authoritative Chinese renderings of the proper nouns its text is likely
// to contain — and differs for every entity.
type Translator interface {
	Translate(ctx context.Context, text string, src SourceLang, gloss Glossary) (zh string, model string, err error)
}

// TranslateSystemPrompt is the PINNED ja→zh-Hans system prompt for entity
// intros (refs/proj/172). Frozen here so a re-translation wave can diff prompt
// versions; the full text goes in the execution report.
//
// It differs from the work-intro prompt only in what it says the input IS:
// these are 角色 / 声优·创作者 / 品牌·会社 descriptions, not 作品简介. Telling the
// model "this is a work synopsis" when it is a character profile hands it a
// premise that contradicts its input.
const TranslateSystemPrompt = `你是资深的游戏本地化译者,专门把视觉小说(galgame)相关的日文条目简介忠实地翻译成简体中文。条目可能是角色(人物)简介、创作者/声优简介,或品牌/会社/厂牌简介。翻译要求:
1. 忠实、完整地翻译原文,不增删、不总结、不改写、不做任何评论。
2. 保留专有名词与品牌名的原文写法:作品标题、角色名、人名、品牌/社团/厂牌名、商标等一律保留原文(日文汉字、假名或罗马字均照抄),不音译也不意译。
3. 保持原文的段落与换行结构。
4. 遇到无法确定的内容,按字面直译,不要留空或添加译注。
5. 只输出译文正文本身,不要输出原文、解释、前言、后记、标注或任何引号包裹。`

// TranslateSystemPromptEn is the en→zh-Hans prompt. It is a separate constant
// rather than a tweak of the ja one: that prompt calls its input 日文, which
// would contradict an English source.
//
// Two rules differ from the Japanese lane, both because the English entity
// intro is itself usually a translation of a Japanese original:
//
//   - proper nouns are to be rendered as the ORIGINAL Japanese/Latin form where
//     the translator can recognise one, not transliterated back out of English;
//   - the English wording is not authoritative, so awkward literalism inherited
//     from the first hop should not be preserved as if it were style.
const TranslateSystemPromptEn = `你是资深的游戏本地化译者,负责把视觉小说(galgame)相关的英文条目简介忠实地翻译成简体中文。条目可能是角色(人物)简介、创作者/声优简介,或品牌/会社/厂牌简介。请注意:英文原文本身通常是从日文翻译而来的二次文本。翻译要求:
1. 忠实、完整地翻译原文,不增删、不总结、不改写、不做任何评论。
2. 专有名词(作品标题、角色名、人名、品牌/社团/厂牌名、商标)如果能辨认出其原本的日文或拉丁字母写法,请使用该原写法;辨认不出时保留英文原样,不要音译成中文。
3. 保持原文的段落与换行结构。
4. 英文原文可能带有转译造成的生硬表达;请按中文的自然表达翻译其含义,但不得改变信息内容。
5. 遇到无法确定的内容,按字面直译,不要留空或添加译注。
6. 只输出译文正文本身,不要输出原文、解释、前言、后记、标注或任何引号包裹。`

// GlossaryHeader / GlossaryRule are the PINNED glossary section appended to
// whichever system prompt the candidate uses when it has terms (wave 175).
// Rule 2 of both base prompts already says "keep proper nouns verbatim"; the
// glossary carves out the exception — for THESE names an authoritative Chinese
// rendering exists in the catalog — and then restates the verbatim rule as a
// hard prohibition, because the failure mode is not "did not translate" but
// "invented kanji for a kana name". Byte-identical to intromt's pair.
const (
	GlossaryHeader = `术语对照表(以下名称在本站已有确定的中文写法,原文 → 中文译名):`
	GlossaryRule   = `对照表中的名称必须使用给定的中文译名;不在对照表中的人名、角色名、品牌/会社名、作品名一律保留原文写法,禁止自创音译或臆造汉字写法。`
)

// PromptSection renders the glossary block appended to the system prompt.
// Empty glossary → empty string → the prompt is byte-identical to the pre-175
// one, which is the same backward-compatibility promise the hash makes.
func (g Glossary) PromptSection() string {
	if len(g) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(GlossaryHeader)
	for _, e := range g {
		sb.WriteString("\n")
		sb.WriteString(e.Src)
		sb.WriteString(" → ")
		sb.WriteString(e.Zh)
	}
	sb.WriteString("\n")
	sb.WriteString(GlossaryRule)
	return sb.String()
}

// withGlossary appends the glossary section to a base system prompt.
func withGlossary(base string, gloss Glossary) string {
	if len(gloss) == 0 {
		return base
	}
	return base + "\n\n" + gloss.PromptSection()
}

// HTTPTranslator is an OpenAI-compatible chat-completions translator (the same
// wire the AI gateway's upstream client speaks). Its entire config is base URL
// + bearer token + model, taken from env/flag — NEVER hardcoded. In production
// it points at the one-api channel layer; locally it is unconfigured, which is
// why the write path proves itself with the mock.
type HTTPTranslator struct {
	baseURL   string
	token     string
	model     string
	maxTokens int
	http      *http.Client
}

// NewHTTPTranslator builds an OpenAI-compatible translator. baseURL is the
// OpenAI base (…/v1); token is the bearer; model is the served model id.
func NewHTTPTranslator(baseURL, token, model string, maxTokens int) *HTTPTranslator {
	return &HTTPTranslator{
		baseURL:   strings.TrimRight(baseURL, "/"),
		token:     token,
		model:     model,
		maxTokens: maxTokens,
		// Reasoning upstreams can spend minutes before the first byte arrives —
		// a per-item wall ceiling, not a liveness knob.
		http: &http.Client{Timeout: 600 * time.Second},
	}
}

// Configured reports whether the gateway is wired (base URL AND token set). An
// unconfigured translator must never be used for a real run — the CLI blocks.
func (t *HTTPTranslator) Configured() bool { return t.baseURL != "" && t.token != "" }

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

// retrySchedule paces retries on 429/5xx/transport errors — the safety valve
// that lets a worker pool ride out rate-limit bursts instead of bleeding
// errors. A var so the test can shrink it.
// The 60s tail matters: a per-MINUTE upstream model rate means a pool that hits
// it must sit out the rest of the window, not just blink.
var retrySchedule = []time.Duration{2 * time.Second, 8 * time.Second, 30 * time.Second, 60 * time.Second}

// Translate runs one plain-text chat completion (temperature 0 for a faithful,
// deterministic rendering). The reply content IS the translation. The prompt is
// picked per call from src — one run mixes both source languages.
func (t *HTTPTranslator) Translate(ctx context.Context, text string, src SourceLang, gloss Glossary) (string, string, error) {
	body := chatRequest{
		Model:       t.model,
		MaxTokens:   t.maxTokens,
		Temperature: 0,
		Messages: []chatMessage{
			{Role: "system", Content: withGlossary(systemPrompt(src), gloss)},
			{Role: "user", Content: text},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", "", err
	}
	data, err := t.post(ctx, raw)
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
	// A non-"stop" finish means the model never completed the translation — a
	// max_tokens squeeze on a reasoning model yields a mid-sentence PARTIAL that
	// would pass the empty-output guard and land in prod. Empty finish_reason is
	// tolerated: some gateways omit it, and the empty-content guard backstops.
	if fr := cr.Choices[0].FinishReason; fr != "" && fr != "stop" {
		return "", "", fmt.Errorf("generation finished with finish_reason=%q — refusing partial output", fr)
	}
	model := cr.Model
	if model == "" {
		model = t.model
	}
	return strings.TrimSpace(cr.Choices[0].Message.Content), model, nil
}

func systemPrompt(src SourceLang) string {
	if src == SourceEn {
		return TranslateSystemPromptEn
	}
	return TranslateSystemPrompt
}

// post sends the request, retrying 429/5xx/transport failures per
// retrySchedule (other 4xx fail immediately — they never heal on retry).
// Returns the 200 body.
func (t *HTTPTranslator) post(ctx context.Context, raw []byte) ([]byte, error) {
	var lastErr error
	for attempt := 0; ; attempt++ {
		data, retryable, err := t.postOnce(ctx, raw)
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

func (t *HTTPTranslator) postOnce(ctx context.Context, raw []byte) (body []byte, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.token)

	resp, err := t.http.Do(req)
	if err != nil {
		return nil, ctx.Err() == nil, err // transport error: retryable unless we were cancelled
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, err
	}
	// 408 ("Request timeout" from the edge upstream) is as transient as a 5xx.
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("gateway http %d: %s", resp.StatusCode, truncate(string(data), 300))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("gateway http %d: %s", resp.StatusCode, truncate(string(data), 300))
	}
	return data, false, nil
}

// MockTranslator is a deterministic, offline stand-in used ONLY by the
// rehearsal full-apply and tests to prove the write path (idempotence,
// hash-change re-translate, never-overwrite) WITHOUT a live gateway. It never
// produces believable translations: it emits an obvious marker + the source
// echo so a mock row that ever leaked to prod is unmistakable, and stamps
// mt_model with a "mock:" prefix. Its output is a pure function of the source
// text, so re-running with the same source is idempotent and changing the
// source yields a different translation (the re-translate proof).
type MockTranslator struct{ Model string }

// Translate returns a deterministic marker translation and a mock model id. The
// source language does not change the output — the marker is the point. The
// glossary is echoed as an entry COUNT so the write-path rehearsal (and the
// tests) can see that the term list actually reached the call.
func (m MockTranslator) Translate(_ context.Context, text string, _ SourceLang, gloss Glossary) (string, string, error) {
	model := m.Model
	if model == "" {
		model = "stub"
	}
	return "【MT・rehearsal mock】[gloss:" + strconv.Itoa(len(gloss)) + "] " + firstRunes(text, 60), "mock:" + model, nil
}

func firstRunes(s string, n int) string {
	s = strings.NewReplacer("\n", " ", "\r", " ").Replace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
