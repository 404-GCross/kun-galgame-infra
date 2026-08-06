package main

import "testing"

// TestClassifySeparatesMeasuredFromUnevidenced pins the distinction the whole
// tool rests on. "Fired often and was almost always wrong" is proof a term does
// harm; "never fired" is only absence of value. Collapsing the two would let a
// single flag silently retire tens of thousands of terms nobody ever measured.
func TestClassifySeparatesMeasuredFromUnevidenced(t *testing.T) {
	stats := []termStat{
		{ID: 1, Term: "loud-and-wrong", Hits: 4000, Flagged: 50, Precision: 0.0125},
		{ID: 2, Term: "loud-and-right", Hits: 4000, Flagged: 3000, Precision: 0.75},
		{ID: 3, Term: "never-fired", Hits: 0, Flagged: 0},
		{ID: 4, Term: "barely-fired-badly", Hits: 3, Flagged: 0},
	}

	// Default posture: only measured harm is retired.
	got := classify(stats, 20, 0.10, false)
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("measured-only pass retired %+v, want just loud-and-wrong", ids(got))
	}
	if got[0].Reason == "" {
		t.Fatal("a retired term must carry the reason into the backup file")
	}

	// Opt-in: unevidenced terms join, and the precise-but-loud one still does not.
	got = classify(stats, 20, 0.10, true)
	if len(got) != 3 {
		t.Fatalf("with -drop-unevidenced retired %v, want 3", ids(got))
	}
	for _, s := range got {
		if s.ID == 2 {
			t.Fatal("a term with 75% precision was retired; the policy is inverted")
		}
	}
}

// TestClassifyRespectsMinHits: a term must not be convicted on a handful of
// observations. At 2 hits and 0 flags the precision reads 0% — statistically
// meaningless, and exactly the shape that would retire a rare-but-correct term.
func TestClassifyRespectsMinHits(t *testing.T) {
	stats := []termStat{{ID: 1, Term: "rare", Hits: 2, Flagged: 0, Precision: 0}}
	if got := classify(stats, 20, 0.10, false); len(got) != 0 {
		t.Fatalf("retired a term on %d observations: %v", stats[0].Hits, ids(got))
	}
	// Exactly at the threshold it becomes judgeable.
	stats[0].Hits = 20
	if got := classify(stats, 20, 0.10, false); len(got) != 1 {
		t.Fatalf("a term at exactly min-hits was not judged: %v", ids(got))
	}
}

func ids(ss []termStat) []int64 {
	out := make([]int64, len(ss))
	for i, s := range ss {
		out[i] = s.ID
	}
	return out
}
