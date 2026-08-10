package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

var bucketEdges = []float64{0.01, 0.05, 0.1, 0.2, 0.4, 0.6, 0.8, 0.9, 1.01}

func runReport(path string, w io.Writer) error {
	if path == "" {
		return fmt.Errorf("--in is required")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var (
		total, failed, flagged int
		scores                 = map[string][]float64{}
		trueCounts             = map[string]int{}
		records                []scanRecord
	)

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec scanRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		total++
		if rec.Error != "" || rec.Verdict == nil {
			failed++
			continue
		}
		if rec.Verdict.Flagged {
			flagged++
		}
		for cat, s := range rec.Verdict.Scores {
			scores[cat] = append(scores[cat], s)
		}
		for cat, v := range rec.Verdict.Categories {
			if v {
				trueCounts[cat]++
			}
		}
		records = append(records, rec)
	}
	if err := sc.Err(); err != nil {
		return err
	}

	scored := total - failed
	fmt.Fprintf(w, "records=%d scored=%d errors=%d flagged=%d (%.2f%%)\n\n",
		total, scored, failed, flagged, pct(flagged, scored))

	cats := make([]string, 0, len(scores))
	for c := range scores {
		cats = append(cats, c)
	}
	sort.Strings(cats)

	fmt.Fprintf(w, "%-26s %8s %8s %8s %8s %8s\n", "category", "true", "p50", "p90", "p99", "max")
	for _, c := range cats {
		v := scores[c]
		sort.Float64s(v)
		fmt.Fprintf(w, "%-26s %8d %8.4f %8.4f %8.4f %8.4f\n",
			c, trueCounts[c], quantile(v, 0.50), quantile(v, 0.90), quantile(v, 0.99), quantile(v, 1.0))
	}

	for _, c := range cats {
		v := scores[c]
		if quantile(v, 1.0) < 0.01 {
			continue
		}
		fmt.Fprintf(w, "\n%s\n", c)
		lo := 0.0
		for _, hi := range bucketEdges {
			n := countIn(v, lo, hi)
			fmt.Fprintf(w, "  [%.2f,%.2f) %6d %5.1f%% %s\n", lo, hi, n, pct(n, len(v)), bar(pct(n, len(v))))
			lo = hi
		}
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].Verdict.Scores["sexual"] > records[j].Verdict.Scores["sexual"]
	})
	fmt.Fprintf(w, "\ntop 20 by sexual score\n")
	for i, rec := range records {
		if i >= 20 {
			break
		}
		fmt.Fprintf(w, "  %.4f  v=%.4f  %s\n",
			rec.Verdict.Scores["sexual"], rec.Verdict.Scores["violence"], rec.URL)
	}
	return nil
}

func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(q * float64(len(sorted)-1))
	return sorted[i]
}

func countIn(sorted []float64, lo, hi float64) int {
	n := 0
	for _, v := range sorted {
		if v >= lo && v < hi {
			n++
		}
	}
	return n
}

func pct(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) * 100 / float64(d)
}

func bar(p float64) string {
	return strings.Repeat("█", int(p/2))
}
