package tagcanon

import (
	"bufio"
	"fmt"
	"os"
	"slices"
	"strings"

	"api/internal/platform/catalog/model"
)

// ReviewOpts tunes the confidence buckets (doc 87 ruling 5). Defaults: high ≥
// 0.90 (auto-pass, drafter spot-checks 30), [0.60,0.90) medium (full human
// review file), < 0.60 low (dropped, counted).
type ReviewOpts struct {
	HighFloor   float64
	MediumFloor float64
	SpotCheck   int // how many high-confidence rows to surface for the drafter's抽查
}

func (o ReviewOpts) withDefaults() ReviewOpts {
	if o.HighFloor == 0 {
		o.HighFloor = 0.90
	}
	if o.MediumFloor == 0 {
		o.MediumFloor = 0.60
	}
	if o.SpotCheck == 0 {
		o.SpotCheck = 30
	}
	return o
}

// ReviewStats reports the bucketing.
type ReviewStats struct {
	HighExact    int // auto-pass merges
	MediumExact  int // need human review
	LowExact     int // dropped
	SingleHigh   int
	SingleMedium int
	SingleLow    int
	NonExact     map[Relation]int // narrower/broader/related/unrelated (留档, never merged)
	Approved     int              // records marked Approve=true (high buckets)
	Total        int
}

// MakeReview consumes the propose JSONL, buckets every record by confidence /
// relation, writes the human-readable markdown review file (medium bucket in
// full + a high-confidence spot-check sample + hierarchy/low tallies) and the
// machine decisions JSONL that apply-reviewed consumes. ONLY exact relations are
// ever eligible to merge (ruling 1); narrower/broader/related/unrelated are
// preserved in the decisions file with Approve=false for a future hierarchy
// wave.
func MakeReview(inPath, mdPath, decisionsPath string, opts ReviewOpts) (*ReviewStats, error) {
	opts = opts.withDefaults()
	recs, err := readRecords(inPath)
	if err != nil {
		return nil, err
	}
	st := &ReviewStats{NonExact: map[Relation]int{}}

	decisions := make([]pairRec, 0, len(recs))
	for _, r := range recs {
		st.Total++
		switch r.Kind {
		case "pair":
			rel := Relation(r.Relation)
			if rel != RelExact {
				st.NonExact[rel]++
				r.Bucket = "hierarchy"
				r.Approve = false
				decisions = append(decisions, r)
				continue
			}
			switch {
			case r.Confidence >= opts.HighFloor:
				r.Bucket = "high"
				r.Approve = true
				st.HighExact++
				st.Approved++
			case r.Confidence >= opts.MediumFloor:
				r.Bucket = "medium"
				r.NeedsReview = true
				r.Approve = false
				st.MediumExact++
			default:
				r.Bucket = "low"
				r.Approve = false
				st.LowExact++
			}
			decisions = append(decisions, r)
		case "single":
			switch {
			case r.Confidence >= opts.HighFloor:
				r.Bucket = "high"
				r.Approve = true
				st.SingleHigh++
				st.Approved++
			case r.Confidence >= opts.MediumFloor:
				r.Bucket = "medium"
				r.NeedsReview = true
				r.Approve = false
				st.SingleMedium++
			default:
				r.Bucket = "low"
				r.Approve = false
				st.SingleLow++
			}
			decisions = append(decisions, r)
		}
	}

	if err := writeRecords(decisionsPath, decisions); err != nil {
		return nil, fmt.Errorf("write decisions: %w", err)
	}
	if err := writeReviewMarkdown(mdPath, decisions, st, opts); err != nil {
		return nil, fmt.Errorf("write review md: %w", err)
	}
	return st, nil
}

// writeReviewMarkdown renders the human review file: a spot-check sample of the
// auto-passed high bucket, the FULL medium bucket (one line per decision, the
// user is the final arbiter), and hierarchy/low tallies.
func writeReviewMarkdown(path string, decisions []pairRec, st *ReviewStats, opts ReviewOpts) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)

	fmt.Fprintf(w, "# 70b 标签配对人审文件\n\n")
	fmt.Fprintf(w, "- 高置信自动放行(exact≥%.2f):**%d** 对 + 单源 %d(起草者抽查 %d)\n",
		opts.HighFloor, st.HighExact, st.SingleHigh, opts.SpotCheck)
	fmt.Fprintf(w, "- 中置信全审(%.2f≤conf<%.2f):**%d** 对 + 单源 %d(逐行人工决定)\n",
		opts.MediumFloor, opts.HighFloor, st.MediumExact, st.SingleMedium)
	fmt.Fprintf(w, "- 低置信丢弃(conf<%.2f):%d 对 + 单源 %d\n", opts.MediumFloor, st.LowExact, st.SingleLow)
	fmt.Fprintf(w, "- 层级/相邻留档(非 exact,不合并):%s\n\n", nonExactSummary(st.NonExact))

	// medium pairs — full, one line per decision.
	fmt.Fprintf(w, "## 中置信 · 配对(全审;把要合并的行 approve 改为 true)\n\n")
	fmt.Fprintf(w, "| A 原文 / 中文 (来源, 用量) | B 原文 / 中文 (来源, 用量) | 关系 | 置信 | 建议 | 理由 |\n")
	fmt.Fprintf(w, "|---|---|---|---|---|---|\n")
	for _, r := range filterBucket(decisions, "pair", "medium") {
		fmt.Fprintf(w, "| %s | %s | %s | %.2f | 合并? | %s |\n",
			nameCell(r.AOrig, r.AName, r.ASource, r.AUsage),
			nameCell(r.BOrig, r.BName, r.BSource, r.BUsage),
			r.Relation, r.Confidence, mdEscape(r.Reason))
	}

	// medium singles — full.
	fmt.Fprintf(w, "\n## 中置信 · 单源准入 tier/kind(全审)\n\n")
	fmt.Fprintf(w, "| 原文 / 中文 (来源, 用量) | tier | kind | 置信 | 理由 |\n")
	fmt.Fprintf(w, "|---|---|---|---|---|\n")
	for _, r := range filterBucket(decisions, "single", "medium") {
		fmt.Fprintf(w, "| %s | %s | %s | %.2f | %s |\n",
			nameCell(r.Orig, r.Name, r.Source, r.Usage),
			tierLabel(r.Tier), kindLabel(r.Kind_), r.Confidence, mdEscape(r.Reason))
	}

	// high spot-check.
	fmt.Fprintf(w, "\n## 高置信抽查(前 %d;自动放行,仅供核对)\n\n", opts.SpotCheck)
	fmt.Fprintf(w, "| A 原文 / 中文 (来源) | B 原文 / 中文 (来源) | 置信 | 理由 |\n")
	fmt.Fprintf(w, "|---|---|---|---|\n")
	shown := 0
	for _, r := range filterBucket(decisions, "pair", "high") {
		if shown >= opts.SpotCheck {
			break
		}
		fmt.Fprintf(w, "| %s | %s | %.2f | %s |\n",
			nameCellNoUsage(r.AOrig, r.AName, r.ASource),
			nameCellNoUsage(r.BOrig, r.BName, r.BSource), r.Confidence, mdEscape(r.Reason))
		shown++
	}
	// Flush explicitly and surface the error — a short write here silently drops
	// medium-bucket rows a human must adjudicate while the command reports success.
	return w.Flush()
}

func filterBucket(recs []pairRec, kind, bucket string) []pairRec {
	var out []pairRec
	for _, r := range recs {
		if r.Kind == kind && r.Bucket == bucket {
			out = append(out, r)
		}
	}
	return out
}

func nameCell(orig, name, source string, usage int) string {
	if orig == "" || orig == name {
		return fmt.Sprintf("%s (%s, %d)", mdEscape(name), source, usage)
	}
	return fmt.Sprintf("%s / %s (%s, %d)", mdEscape(orig), mdEscape(name), source, usage)
}

func nameCellNoUsage(orig, name, source string) string {
	if orig == "" || orig == name {
		return fmt.Sprintf("%s (%s)", mdEscape(name), source)
	}
	return fmt.Sprintf("%s / %s (%s)", mdEscape(orig), mdEscape(name), source)
}

func tierLabel(t *int16) string {
	if t == nil {
		return "?"
	}
	switch *t {
	case model.TagTierCore:
		return "core"
	case model.TagTierLongtail:
		return "longtail"
	case model.TagTierHidden:
		return "hidden"
	}
	return "?"
}

func kindLabel(k *int16) string {
	if k == nil {
		return "?"
	}
	switch *k {
	case model.TagKindContent:
		return "content"
	case model.TagKindMeta:
		return "meta"
	}
	return "?"
}

func nonExactSummary(m map[Relation]int) string {
	if len(m) == 0 {
		return "(无)"
	}
	keys := make([]Relation, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	return strings.Join(parts, " / ")
}

// mdEscape neutralizes pipe/newline so a reason never breaks the markdown table.
func mdEscape(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.TrimSpace(s)
}
