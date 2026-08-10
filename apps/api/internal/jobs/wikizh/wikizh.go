package wikizh

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type Bucket string

const (
	BucketUsable  Bucket = "usable"
	BucketCompare Bucket = "compare"
)

const (
	VerdictUsable   = "usable"
	VerdictUnusable = "unusable"

	VerdictABetter    = "a_better"
	VerdictBBetter    = "b_better"
	VerdictEquivalent = "equivalent"

	VerdictUnsure = "unsure"
)

func restores(b Bucket, v string) bool {
	if b == BucketCompare {
		return v == VerdictABetter
	}
	return v == VerdictUsable
}

func leaning(v string) int {
	switch v {
	case VerdictABetter, VerdictUsable:
		return 1
	case VerdictBBetter, VerdictUnusable:
		return -1
	default:
		return 0
	}
}

func validVerdict(b Bucket, v string) bool {
	switch b {
	case BucketCompare:
		return v == VerdictABetter || v == VerdictBBetter || v == VerdictEquivalent || v == VerdictUnsure
	default:
		return v == VerdictUsable || v == VerdictUnusable || v == VerdictUnsure
	}
}

type Candidate struct {
	WorkID     int64  `json:"work_id"`
	Bucket     Bucket `json:"bucket"`
	Lang       string `json:"lang"`
	Source     string `json:"source"`
	SourceLang string `json:"source_lang"`
	WikiZh     string `json:"wiki_zh"`
	MachineZh  string `json:"machine_zh,omitempty"`
}

func (c Candidate) Key() string { return fmt.Sprintf("w%d", c.WorkID) }

type Verdict struct {
	Key           string  `json:"key"`
	WorkID        int64   `json:"work_id"`
	Bucket        Bucket  `json:"bucket"`
	Verdict       string  `json:"verdict"`
	Confidence    float64 `json:"confidence"`
	Reason        string  `json:"reason"`
	Model         string  `json:"model,omitempty"`
	PromptVersion string  `json:"prompt_version"`
}

type Judge interface {
	JudgeBatch(ctx context.Context, bucket Bucket, cs []Candidate) ([]Verdict, error)
}

func LoadVerdicts(path string) ([]Verdict, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []Verdict
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var v Verdict
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			return nil, fmt.Errorf("parse verdict %q: %w", line, err)
		}
		out = append(out, v)
	}
	return out, nil
}

func LoadVerdictKeys(path string) (map[string]bool, error) {
	vs, err := LoadVerdicts(path)
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	keys := make(map[string]bool, len(vs))
	for _, v := range vs {
		keys[v.Key] = true
	}
	return keys, nil
}
