package intromt

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

// Translator turns a ja source intro into a zh-Hans translation. It is the ONLY
// seam onto the LLM: the runner never speaks HTTP directly, so tests inject a
// deterministic fake (httptest / in-memory) and the rehearsal full-apply runs a
// mock — no live gateway required to prove the write path (doc 75). Translate
// returns the translated text plus the effective model id that produced it
// (recorded in mt_model for accountability); an error is recorded and the run
// continues (single-item failures never abort the batch).
type Translator interface {
	Translate(ctx context.Context, jaText string) (zh string, model string, err error)
}

// TranslateSystemPrompt is the PINNED ja→zh-Hans system prompt for the galgame
// intro MT pilot (doc 75). Faithful, complete, verbatim proper nouns / brand
// names, plain translation output only. Frozen here so a re-translation wave
// can diff prompt versions; the full text goes in the execution report.
const TranslateSystemPrompt = `你是资深的游戏本地化译者,专门把日文视觉小说(galgame)的作品简介忠实地翻译成简体中文。翻译要求:
1. 忠实、完整地翻译原文,不增删、不总结、不改写、不做任何评论。
2. 保留专有名词与品牌名的原文写法:作品标题、角色名、人名、品牌/社团/厂牌名、商标等一律保留原文(日文汉字、假名或罗马字均照抄),不音译也不意译。
3. 保持原文的段落与换行结构。
4. 遇到无法确定的内容,按字面直译,不要留空或添加译注。
5. 只输出译文正文本身,不要输出原文、解释、前言、后记、标注或任何引号包裹。`

// HTTPTranslator is an OpenAI-compatible chat-completions translator (the same
// wire the AI gateway's upstream client and llmsuggest speak). Its entire
// config is base URL + bearer token + model, taken from env/flag — NEVER
// hardcoded. In production it points at the one-api channel layer (or any
// OpenAI-compatible gateway); locally it is unconfigured/unreachable, which is
// why the pilot proves itself with the mock.
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
		http:      &http.Client{Timeout: 120 * time.Second},
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
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Translate runs one plain-text chat completion (temperature 0 for a faithful,
// deterministic rendering). The reply content IS the translation.
func (t *HTTPTranslator) Translate(ctx context.Context, jaText string) (string, string, error) {
	body := chatRequest{
		Model:       t.model,
		MaxTokens:   t.maxTokens,
		Temperature: 0,
		Messages: []chatMessage{
			{Role: "system", Content: TranslateSystemPrompt},
			{Role: "user", Content: jaText},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.token)

	resp, err := t.http.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("gateway http %d: %s", resp.StatusCode, truncate(string(data), 300))
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
	model := cr.Model
	if model == "" {
		model = t.model
	}
	return strings.TrimSpace(cr.Choices[0].Message.Content), model, nil
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

// Translate returns a deterministic marker translation and a mock model id.
func (m MockTranslator) Translate(_ context.Context, jaText string) (string, string, error) {
	model := m.Model
	if model == "" {
		model = "stub"
	}
	return "【MT・rehearsal mock】" + firstRunes(jaText, 60), "mock:" + model, nil
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
