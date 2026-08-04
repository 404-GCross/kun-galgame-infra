// Package wikizh adjudicates the retired wiki's hand-written Chinese intros
// against what the catalog holds today, and restores the ones that are better
// (refs/proj/168).
//
// BACKGROUND. Wave 146 discarded the claimed Chinese intros wholesale on the
// premise that they were translations the machine lane would redo. Wave 164
// walked part of that back after finding signed original writing among them.
// The user's 2026-08-03 ruling replaces the provenance criterion with a QUALITY
// one: keep whichever text is the better Chinese intro, whoever wrote it.
//
// The source texts survive a DROP: `src_wiki.intro_snapshot` (wave 168 rescue)
// captured all 16,690 of them, together with what the catalog held at capture
// time, precisely so this job never has to join the retired galgame family.
//
// TWO BUCKETS, TWO QUESTIONS:
//
//   - BucketUsable (3,807) — the catalog has no Chinese at all, so there is
//     nothing to compare against. The question is whether the wiki text is a
//     publishable intro or a fragment.
//   - BucketCompare (3,829) — a machine translation occupies the slot. The
//     question is which of the two is better.
//
// WRITES ARE PURELY ADDITIVE. A restored text lands as a NEW provenance=0 row;
// the machine row is never deleted or edited. The read face already prefers
// provenance=0 over provenance=1, so the human text takes over on display while
// the machine row stays as a fallback, and a rollback is a DELETE of exactly
// the rows this job wrote (recorded in its receipts).
package wikizh

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Bucket selects which question the judge is asked.
type Bucket string

const (
	// BucketUsable — is this lone wiki text publishable at all?
	BucketUsable Bucket = "usable"
	// BucketCompare — wiki text vs the machine translation holding the slot.
	BucketCompare Bucket = "compare"
)

// Verdict vocabularies, per bucket. A value outside them is treated as unsure:
// a judge that invents a verdict must not be able to cause a write.
const (
	VerdictUsable   = "usable"
	VerdictUnusable = "unusable"

	VerdictABetter    = "a_better"
	VerdictBBetter    = "b_better"
	VerdictEquivalent = "equivalent"

	VerdictUnsure = "unsure"
)

// restores reports whether a verdict means "write the wiki text".
func restores(b Bucket, v string) bool {
	if b == BucketCompare {
		return v == VerdictABetter
	}
	return v == VerdictUsable
}

// leaning maps a verdict onto the axis the decision actually turns on:
// +1 "write the wiki text", -1 "do not", 0 "no opinion".
//
// This is the distinction the first fold missed. `equivalent` is not a vote
// against restoring, it is a statement that the choice does not matter, and
// `unsure` is an abstention. Requiring three IDENTICAL labels treated both as
// dissent and put 306 works into the review pile that no round had actually
// contradicted — 197 in the compare bucket, 109 in usable.
func leaning(v string) int {
	switch v {
	case VerdictABetter, VerdictUsable:
		return 1
	case VerdictBBetter, VerdictUnusable:
		return -1
	default: // equivalent, unsure, anything invented
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

// Candidate is one work awaiting adjudication, assembled from the snapshot.
type Candidate struct {
	WorkID int64  `json:"work_id"`
	Bucket Bucket `json:"bucket"`
	// Lang is the wiki column the text came from: zh-Hans or zh-Hant.
	Lang string `json:"lang"`
	// Source is the original blurb the judge compares against — the catalog's
	// ja if it has one, else the wiki's ja, else either side's en.
	Source     string `json:"source"`
	SourceLang string `json:"source_lang"`
	WikiZh     string `json:"wiki_zh"`
	MachineZh  string `json:"machine_zh,omitempty"`
}

// Key identifies a candidate in a verdict file.
func (c Candidate) Key() string { return fmt.Sprintf("w%d", c.WorkID) }

// Verdict is one judged candidate, as written to the JSONL verdict file.
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

// Judge produces a verdict per candidate.
type Judge interface {
	JudgeBatch(ctx context.Context, bucket Bucket, cs []Candidate) ([]Verdict, error)
}

// LoadVerdicts reads a JSONL verdict file whole.
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

// LoadVerdictKeys reads just the keys already judged, so a killed pass resumes
// instead of re-paying for work it already did. A missing file is not an error.
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
