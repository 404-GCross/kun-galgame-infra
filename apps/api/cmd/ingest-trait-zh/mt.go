package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

const TranslateSystemPrompt = `你是 galgame(视觉小说)领域的术语翻译者,负责把 VNDB 的角色特征标签名翻译成简体中文。
要求:
1. 输出这个 galgame 角色特征标签的简体中文译名,2-8 个字,只输出译名本身,不要输出英文原文、解释、标点或引号。
2. 这是术语,不是句子:用名词或名词短语,不要写成完整的句子或描述。
3. 已有通行中文说法的(如 Ahoge → 呆毛,Tsundere → 傲娇,Ojousama → 大小姐),一律优先使用通行说法。
4. 参考给出的所属分类和英文释义来消歧:同一个词在「服装」和「性格」分类下含义不同。
5. 无法确定时按字面直译,不要留空,也不要添加注释。`

type mtCandidate struct {
	ID          int64  `gorm:"column:id"`
	VndbTID     string `gorm:"column:vndb_tid"`
	Name        string `gorm:"column:name"`
	GroupName   string `gorm:"column:group_name"`
	Description string `gorm:"column:description"`
}

func loadMTCandidates(ctx context.Context, db *gorm.DB, limit int) ([]mtCandidate, error) {
	q := `SELECT t.id, t.vndb_tid, t.name, COALESCE(g.name, '') AS group_name, t.description
	        FROM catalog_character_trait t
	        LEFT JOIN catalog_character_trait g ON g.vndb_tid = t.group_tid
	       WHERE t.name_zh = ''
	       ORDER BY t.id`
	if limit > 0 {
		q += " LIMIT " + strconv.Itoa(limit)
	}
	var rows []mtCandidate
	err := db.WithContext(ctx).Raw(q).Scan(&rows).Error
	return rows, err
}

var bbcode = regexp.MustCompile(`\[/?[a-zA-Z]+(?:=[^\]]*)?\]`)

func plainDescription(s string) string {
	s = bbcode.ReplaceAllString(s, "")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func userMessage(c mtCandidate) string {
	var sb strings.Builder
	sb.WriteString("标签名(英文): ")
	sb.WriteString(c.Name)
	if c.GroupName != "" {
		sb.WriteString("\n所属分类: ")
		sb.WriteString(c.GroupName)
	}
	if d := plainDescription(c.Description); d != "" {
		sb.WriteString("\n英文释义: ")
		sb.WriteString(truncateRunes(d, 400))
	}
	return sb.String()
}

type translator interface {
	Translate(ctx context.Context, c mtCandidate) (zh string, model string, err error)
}

type httpTranslator struct {
	baseURL   string
	token     string
	model     string
	maxTokens int
	http      *http.Client
}

func newHTTPTranslator(baseURL, token, model string, maxTokens int) *httpTranslator {
	return &httpTranslator{
		baseURL:   strings.TrimRight(baseURL, "/"),
		token:     token,
		model:     model,
		maxTokens: maxTokens,
		http:      &http.Client{Timeout: 300 * time.Second},
	}
}

func (t *httpTranslator) Configured() bool { return t.baseURL != "" && t.token != "" }

func (t *httpTranslator) Translate(ctx context.Context, c mtCandidate) (string, string, error) {
	body := map[string]any{
		"model":       t.model,
		"max_tokens":  t.maxTokens,
		"temperature": 0,
		"messages": []map[string]string{
			{"role": "system", "content": TranslateSystemPrompt},
			{"role": "user", "content": userMessage(c)},
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
		return "", "", fmt.Errorf("gateway http %d: %s", resp.StatusCode, truncateRunes(string(data), 300))
	}
	var cr struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &cr); err != nil {
		return "", "", fmt.Errorf("decode chat response: %w", err)
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
	return cleanProposal(cr.Choices[0].Message.Content), model, nil
}

func cleanProposal(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n\r"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(strings.TrimPrefix(s, "译名:"))
	s = strings.TrimSpace(strings.TrimPrefix(s, "译名："))
	return strings.Trim(s, " \t\"'“”「」。.")
}

var csvHeader = []string{"trait_id", "vndb_tid", "name", "group", "description", "proposed_zh"}

func writeReviewCSV(path string, rows []csvRow) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if err := w.Write(csvHeader); err != nil {
		return err
	}
	for _, r := range rows {
		if err := w.Write([]string{
			strconv.FormatInt(r.TraitID, 10), r.VndbTID, r.Name, r.Group, r.Description, r.ProposedZh,
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

type csvRow struct {
	TraitID     int64
	VndbTID     string
	Name        string
	Group       string
	Description string
	ProposedZh  string
}

func readReviewCSV(path string) ([]pair, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	recs, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		return nil, fmt.Errorf("%s: empty CSV", path)
	}
	nameCol, zhCol := indexOf(recs[0], "name"), indexOf(recs[0], "proposed_zh")
	if nameCol < 0 || zhCol < 0 {
		return nil, fmt.Errorf("%s: header must contain name and proposed_zh columns", path)
	}
	var out []pair
	for i, rec := range recs[1:] {
		if nameCol >= len(rec) || zhCol >= len(rec) {
			continue
		}
		name, zh := strings.TrimSpace(rec[nameCol]), stripQualityMarkers(rec[zhCol])
		if name == "" || zh == "" {
			continue
		}
		out = append(out, pair{En: name, Zh: zh, Line: i + 2})
	}
	return out, nil
}

func indexOf(row []string, col string) int {
	for i, c := range row {
		if strings.TrimSpace(c) == col {
			return i
		}
	}
	return -1
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
