package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

func writeSummary(w io.Writer, res runResult) {
	fmt.Fprintln(w, "scan-backlog summary")
	fmt.Fprintf(w, "  input read (valid)  %8d\n", res.input.valid)
	fmt.Fprintf(w, "  bad lines           %8d\n", res.input.badLines)
	fmt.Fprintf(w, "  invalid utf-8       %8d\n", res.input.invalidUTF8)
	fmt.Fprintf(w, "  dup ids in input    %8d\n", res.dupInput)
	fmt.Fprintf(w, "  enqueued            %8d\n", res.enqueued)
	fmt.Fprintf(w, "  succeeded           %8d\n", res.succeeded)
	fmt.Fprintf(w, "  failed (error row)  %8d\n", res.failed)
	fmt.Fprintf(w, "  skipped (resume)    %8d\n", res.skippedResume)

	hist, flagged := histogram(res.allScored)
	maxB := 0
	for _, c := range hist {
		if c > maxB {
			maxB = c
		}
	}
	fmt.Fprintf(w, "\nscore histogram (scored=%d, flagged=%d):\n", len(res.allScored), flagged)
	for b := 0; b < histogramBuckets; b++ {
		lo := float64(b) / 10.0
		hi := lo + 0.1
		label := fmt.Sprintf("[%.1f-%.1f)", lo, hi)
		if b == histogramBuckets-1 {
			label = fmt.Sprintf("[%.1f-%.1f]", lo, hi)
		}
		bar := ""
		if maxB > 0 {
			bar = strings.Repeat("#", hist[b]*40/maxB)
		}
		fmt.Fprintf(w, "  %s %7d %s\n", label, hist[b], bar)
	}

	if cats := topCategories(res.allScored, 10); len(cats) > 0 {
		fmt.Fprintln(w, "\ntop categories:")
		for _, c := range cats {
			fmt.Fprintf(w, "  %7d  %s\n", c.count, c.name)
		}
	}
}

func histogram(rows []scoredRow) ([histogramBuckets]int, int) {
	var h [histogramBuckets]int
	flagged := 0
	for _, r := range rows {
		if r.Flagged {
			flagged++
		}
		h[bucketOf(scoreVal(r))]++
	}
	return h, flagged
}

func bucketOf(s float64) int {
	if s <= 0 {
		return 0
	}
	if s >= 1 {
		return histogramBuckets - 1
	}
	b := int(s * 10)
	if b >= histogramBuckets {
		b = histogramBuckets - 1
	}
	return b
}

type catCount struct {
	name  string
	count int
}

func topCategories(rows []scoredRow, n int) []catCount {
	counts := map[string]int{}
	for _, r := range rows {
		for _, c := range r.Categories {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			counts[c]++
		}
	}
	out := make([]catCount, 0, len(counts))
	for k, v := range counts {
		out = append(out, catCount{name: k, count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		return out[i].name < out[j].name
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func writeWorklist(path string, rows []scoredRow, textByID map[string]string, topN int) error {
	sorted := make([]scoredRow, len(rows))
	copy(sorted, rows)
	sort.Slice(sorted, func(i, j int) bool {
		si, sj := scoreVal(sorted[i]), scoreVal(sorted[j])
		if si != sj {
			return si > sj
		}
		if sorted[i].Flagged != sorted[j].Flagged {
			return sorted[i].Flagged
		}
		return sorted[i].ID < sorted[j].ID
	})
	if topN > 0 && len(sorted) > topN {
		sorted = sorted[:topN]
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create worklist %s: %w", path, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for _, r := range sorted {
		item := worklistItem{
			ID:         r.ID,
			Site:       r.Site,
			Score:      r.Score,
			Categories: r.Categories,
			Text:       firstRunes(textByID[r.ID], worklistTextRunes),
		}
		if err := enc.Encode(&item); err != nil {
			return fmt.Errorf("write worklist item: %w", err)
		}
	}
	return nil
}

func scoreVal(r scoredRow) float64 {
	if r.Score == nil {
		return 0
	}
	return float64(*r.Score)
}

func firstRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

func worklistPathFor(outPath string) string {
	if strings.HasSuffix(outPath, ".jsonl") {
		return strings.TrimSuffix(outPath, ".jsonl") + ".top.jsonl"
	}
	return outPath + ".top.jsonl"
}
