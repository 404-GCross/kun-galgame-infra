package intromt

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

type Translator interface {
	Translate(ctx context.Context, jaText string, gloss Glossary) (zh string, model string, err error)
}

const TranslateSystemPrompt = `你是资深的游戏本地化译者,专门把日文视觉小说(galgame)的作品简介忠实地翻译成简体中文。翻译要求:
1. 忠实、完整地翻译原文,不增删、不总结、不改写、不做任何评论。
2. 保留专有名词与品牌名的原文写法:作品标题、角色名、人名、品牌/社团/厂牌名、商标等一律保留原文(日文汉字、假名或罗马字均照抄),不音译也不意译。
3. 保持原文的段落与换行结构。
4. 遇到无法确定的内容,按字面直译,不要留空或添加译注。
5. 只输出译文正文本身,不要输出原文、解释、前言、后记、标注或任何引号包裹。`

const TranslateSystemPromptEn = `你是资深的游戏本地化译者,负责把视觉小说(galgame)的英文作品简介忠实地翻译成简体中文。请注意:英文原文本身通常是从日文翻译而来的二次文本。翻译要求:
1. 忠实、完整地翻译原文,不增删、不总结、不改写、不做任何评论。
2. 专有名词(作品标题、角色名、人名、品牌/社团/厂牌名、商标)如果能辨认出其原本的日文或拉丁字母写法,请使用该原写法;辨认不出时保留英文原样,不要音译成中文。
3. 保持原文的段落与换行结构。
4. 英文原文可能带有转译造成的生硬表达;请按中文的自然表达翻译其含义,但不得改变信息内容。
5. 遇到无法确定的内容,按字面直译,不要留空或添加译注。
6. 只输出译文正文本身,不要输出原文、解释、前言、后记、标注或任何引号包裹。`

const (
	GlossaryHeader = `术语对照表(以下名称在本站已有确定的中文写法,原文 → 中文译名):`
	GlossaryRule   = `对照表中的名称必须使用给定的中文译名;不在对照表中的人名、角色名、品牌/会社名、作品名一律保留原文写法,禁止自创音译或臆造汉字写法。`
)

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

func withGlossary(base string, gloss Glossary) string {
	if len(gloss) == 0 {
		return base
	}
	return base + "\n\n" + gloss.PromptSection()
}

type HTTPTranslator struct {
	sourceLang SourceLang
	baseURL    string
	token      string
	model      string
	maxTokens  int
	http       *http.Client
}

func NewHTTPTranslator(baseURL, token, model string, maxTokens int) *HTTPTranslator {
	return &HTTPTranslator{
		baseURL:   strings.TrimRight(baseURL, "/"),
		token:     token,
		model:     model,
		maxTokens: maxTokens,
		http:      &http.Client{Timeout: 600 * time.Second},
	}
}

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

var retrySchedule = []time.Duration{2 * time.Second, 8 * time.Second, 30 * time.Second, 60 * time.Second}

func (t *HTTPTranslator) Translate(ctx context.Context, jaText string, gloss Glossary) (string, string, error) {
	body := chatRequest{
		Model:       t.model,
		MaxTokens:   t.maxTokens,
		Temperature: 0,
		Messages: []chatMessage{
			{Role: "system", Content: withGlossary(t.systemPrompt(), gloss)},
			{Role: "user", Content: jaText},
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
	if fr := cr.Choices[0].FinishReason; fr != "" && fr != "stop" {
		return "", "", fmt.Errorf("generation finished with finish_reason=%q — refusing partial output", fr)
	}
	model := cr.Model
	if model == "" {
		model = t.model
	}
	return strings.TrimSpace(cr.Choices[0].Message.Content), model, nil
}

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

type MockTranslator struct{ Model string }

func (m MockTranslator) Translate(_ context.Context, jaText string, gloss Glossary) (string, string, error) {
	model := m.Model
	if model == "" {
		model = "stub"
	}
	return "【MT・rehearsal mock】[gloss:" + strconv.Itoa(len(gloss)) + "] " + firstRunes(jaText, 60), "mock:" + model, nil
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

func (t *HTTPTranslator) SetSourceLang(src SourceLang) { t.sourceLang = src }

func (t *HTTPTranslator) systemPrompt() string {
	if t.sourceLang == SourceEn {
		return TranslateSystemPromptEn
	}
	return TranslateSystemPrompt
}
