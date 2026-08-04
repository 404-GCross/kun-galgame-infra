package tagsafety

import (
	"fmt"
	"sort"
	"strings"
)

// ReportStats is the DB-free census of a verdict JSONL: how many names landed in
// each class, and how the confidences are distributed inside each class. It
// answers "is this batch worth applying?" before any DSN is involved — the dry
// run answers the different question "what would it write?", which needs the
// catalog state.
type ReportStats struct {
	Total   int
	ByClass map[Class]int
	// ByClassBucket is class → confidence bucket → count.
	ByClassBucket map[Class]map[string]int
	// Confident is the count that would auto-apply at MinConfidence.
	Confident map[Class]int
	// Sources is source key → verdict count.
	Sources map[string]int
	// Unknown counts lines whose class is not one of the three (a corrupted or
	// hand-edited file) — never silently ignored.
	Unknown int
}

// Report reads the verdict JSONL and censuses it. No DB, no LLM.
func Report(in string, minConfidence float64) (*ReportStats, error) {
	if in == "" {
		return nil, fmt.Errorf("verdict JSONL is required (--in)")
	}
	if minConfidence <= 0 {
		minConfidence = 0.90
	}
	verdicts, err := readVerdicts(in)
	if err != nil {
		return nil, err
	}
	st := &ReportStats{
		ByClass:       map[Class]int{},
		ByClassBucket: map[Class]map[string]int{},
		Confident:     map[Class]int{},
		Sources:       map[string]int{},
	}
	for _, v := range verdicts {
		st.Total++
		st.Sources[v.Source]++
		cls := Class(v.Class)
		if !validClass(cls) {
			st.Unknown++
			continue
		}
		st.ByClass[cls]++
		if st.ByClassBucket[cls] == nil {
			st.ByClassBucket[cls] = map[string]int{}
		}
		st.ByClassBucket[cls][confidenceBucket(v.Confidence)]++
		if v.Confidence >= minConfidence {
			st.Confident[cls]++
		}
	}
	return st, nil
}

// String renders the census as the command's stdout block.
func (s *ReportStats) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "total=%d unknown_class=%d\n", s.Total, s.Unknown)
	for _, src := range sortedKeys(s.Sources) {
		fmt.Fprintf(&b, "  source %-12s %d\n", src, s.Sources[src])
	}
	for _, cls := range []Class{ClassSexual, ClassJunk, ClassNormal} {
		fmt.Fprintf(&b, "  %-7s total=%-6d confident=%-6d", cls, s.ByClass[cls], s.Confident[cls])
		buckets := s.ByClassBucket[cls]
		for _, k := range []string{"0.90-1.00", "0.70-0.90", "0.50-0.70", "0.00-0.50"} {
			fmt.Fprintf(&b, " %s=%d", k, buckets[k])
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
