package actrie

import (
	"math/rand"
	"slices"
	"strings"
	"testing"
	"time"
)

// bs turns string patterns into the [][]byte Build expects; pattern k gets
// payload k, matching how the term matcher aligns payloads with term indexes.
func bs(ss ...string) [][]byte {
	out := make([][]byte, len(ss))
	for i, s := range ss {
		out[i] = []byte(s)
	}
	return out
}

// brute is the reference oracle: the linear per-pattern strings.Contains scan
// this package replaces. It returns ascending distinct hit indexes, skipping
// empty patterns exactly as Build does.
func brute(patterns [][]byte, text []byte) []int {
	s := string(text)
	var res []int
	for i, p := range patterns {
		if len(p) == 0 {
			continue
		}
		if strings.Contains(s, string(p)) {
			res = append(res, i)
		}
	}
	return res
}

// TestClassicOverlap pins the textbook he/she/his/hers overlap: "ushers" holds
// he, she and hers (via fail/dictionary links) but not his.
func TestClassicOverlap(t *testing.T) {
	m := Build(bs("he", "she", "his", "hers"))
	if got := m.Match([]byte("ushers")); !slices.Equal(got, []int{0, 1, 3}) {
		t.Fatalf("ushers → %v, want [0 1 3] (he she hers)", got)
	}
	if got := m.Match([]byte("his")); !slices.Equal(got, []int{2}) {
		t.Fatalf("his → %v, want [2]", got)
	}
	if got := m.Match([]byte("xyz")); got != nil {
		t.Fatalf("no-match → %v, want nil", got)
	}
}

// TestMutualPrefixSuffix pins nested patterns where each is a prefix/suffix of
// another: over "abcd" every one fires.
func TestMutualPrefixSuffix(t *testing.T) {
	m := Build(bs("ab", "abc", "bc", "c", "abcd", "bcd", "cd", "d"))
	got := m.Match([]byte("abcd"))
	want := []int{0, 1, 2, 3, 4, 5, 6, 7}
	if !slices.Equal(got, want) {
		t.Fatalf("abcd → %v, want %v", got, want)
	}
	// A single 'a' only fires nothing here (no bare "a" pattern).
	if got := m.Match([]byte("a")); got != nil {
		t.Fatalf("a → %v, want nil", got)
	}
}

// TestCJKMultiByte pins that byte-level matching handles multi-byte UTF-8 CJK,
// including a pattern that spans two adjacent characters' bytes.
func TestCJKMultiByte(t *testing.T) {
	// 坏词 / 词的 overlap in 含坏词的文本; 好 is absent.
	m := Build(bs("坏词", "词的", "好"))
	if got := m.Match([]byte("含坏词的文本")); !slices.Equal(got, []int{0, 1}) {
		t.Fatalf("含坏词的文本 → %v, want [0 1]", got)
	}
	if got := m.Match([]byte("普通文本")); got != nil {
		t.Fatalf("clean CJK → %v, want nil", got)
	}
}

// TestMultiHitDedupe pins that a pattern occurring many times yields its payload
// exactly once, and distinct patterns are each reported once.
func TestMultiHitDedupe(t *testing.T) {
	m := Build(bs("ab", "ba"))
	if got := m.Match([]byte("abababab")); !slices.Equal(got, []int{0, 1}) {
		t.Fatalf("abababab → %v, want [0 1] (each once)", got)
	}
	m2 := Build(bs("x"))
	if got := m2.Match([]byte("xxxxx")); !slices.Equal(got, []int{0}) {
		t.Fatalf("xxxxx → %v, want [0]", got)
	}
}

// TestDuplicatePatterns pins that two patterns sharing the same string both emit
// their payloads — the property the term matcher relies on when two terms (e.g. a
// global and a per-site one) normalize to the same norm.
func TestDuplicatePatterns(t *testing.T) {
	m := Build(bs("dup", "dup", "other"))
	if got := m.Match([]byte("a dup b")); !slices.Equal(got, []int{0, 1}) {
		t.Fatalf("shared pattern → %v, want [0 1] (both payloads)", got)
	}
}

// TestEmptyAutomaton pins the degenerate builds: no patterns, and all-empty
// patterns — both match nothing. An empty pattern interleaved with a real one is
// skipped (strings.Contains(x,"") would be true, which is never the intent).
func TestEmptyAutomaton(t *testing.T) {
	if got := Build(nil).Match([]byte("anything")); got != nil {
		t.Fatalf("nil patterns → %v, want nil", got)
	}
	if got := Build(bs("", "")).Match([]byte("anything")); got != nil {
		t.Fatalf("all-empty patterns → %v, want nil", got)
	}
	m := Build(bs("", "abc", ""))
	if got := m.Match([]byte("zzabczz")); !slices.Equal(got, []int{1}) {
		t.Fatalf("empty-interleaved → %v, want [1] (empties never match)", got)
	}
	// An empty text never matches a non-empty pattern.
	if got := Build(bs("abc")).Match(nil); got != nil {
		t.Fatalf("empty text → %v, want nil", got)
	}
}

// TestDifferentialVsBrute is the acceptance oracle for correctness: over many
// random pattern-set × text trials the automaton's hit set must equal the linear
// strings.Contains scan's, byte-for-byte. The alphabet mixes ASCII with shared
// high bytes (fragments of multi-byte UTF-8) so fail/dictionary links and byte-
// level overlaps are heavily exercised, including sequences that are not valid
// UTF-8 (strings.Contains is a byte oracle, so this stays a fair comparison).
func TestDifferentialVsBrute(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alphabet := []byte{'a', 'b', 'c', 0xE7, 0x95, 0x8c}
	tok := func(maxLen int) []byte {
		n := rng.Intn(maxLen + 1)
		b := make([]byte, n)
		for i := range b {
			b[i] = alphabet[rng.Intn(len(alphabet))]
		}
		return b
	}
	const trials = 2000
	for trial := 0; trial < trials; trial++ {
		nPat := rng.Intn(12)
		patterns := make([][]byte, nPat)
		for i := range patterns {
			patterns[i] = tok(5) // length 0..5 (empties exercised too)
		}
		text := tok(48)
		got := Build(patterns).Match(text)
		want := brute(patterns, text)
		if !slices.Equal(got, want) {
			t.Fatalf("trial %d: patterns=%v text=%v\n got=%v\nwant=%v", trial, patterns, text, got, want)
		}
	}
}

// syntheticCorpus builds a large, realistic corpus: nTerms distinct terms over a
// mixed ASCII+CJK alphabet, and an 8000-rune text with real terms scattered in so
// matches actually occur. Deterministic (fixed seed) for repeatable benchmarks.
func syntheticCorpus(nTerms int) (patterns [][]byte, text []byte) {
	rng := rand.New(rand.NewSource(42))
	alphabet := []rune("abcdefghijklmnopqrstuvwxyz0123456789的一是不了在人有我他这中大来")
	patterns = make([][]byte, 0, nTerms)
	seen := make(map[string]struct{}, nTerms)
	for len(patterns) < nTerms {
		l := 3 + rng.Intn(8) // 3..10 runes
		rs := make([]rune, l)
		for j := range rs {
			rs[j] = alphabet[rng.Intn(len(alphabet))]
		}
		s := string(rs)
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		patterns = append(patterns, []byte(s))
	}
	trs := make([]rune, 0, 8000)
	for len(trs) < 8000 {
		if rng.Intn(25) == 0 { // ~4% of the time splice in a real term → guaranteed hits
			trs = append(trs, []rune(string(patterns[rng.Intn(nTerms)]))...)
		} else {
			trs = append(trs, alphabet[rng.Intn(len(alphabet))])
		}
	}
	return patterns, []byte(string(trs[:8000]))
}

// TestPerformanceRegression is a loose-bound guard against an algorithmic
// regression (e.g. accidental per-check rebuild or a quadratic scan): building
// 30k terms stays well under 2s and a single 8000-rune match well under 50ms.
// The bounds are deliberately slack so the test guards the algorithm, not jitter.
func TestPerformanceRegression(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping perf regression in -short")
	}
	patterns, text := syntheticCorpus(30000)

	t0 := time.Now()
	m := Build(patterns)
	buildDur := time.Since(t0)
	if buildDur > 2*time.Second {
		t.Fatalf("build of %d terms took %v, want < 2s", len(patterns), buildDur)
	}

	t1 := time.Now()
	hits := m.Match(text)
	matchDur := time.Since(t1)
	if matchDur > 50*time.Millisecond {
		t.Fatalf("single match over 8000-rune text took %v, want < 50ms", matchDur)
	}
	if len(hits) == 0 {
		t.Fatalf("expected hits from spliced-in terms, got none")
	}
	t.Logf("build(%d terms)=%v match(8000 runes)=%v hits=%d", len(patterns), buildDur, matchDur, len(hits))
}

func BenchmarkBuild30k(b *testing.B) {
	patterns, _ := syntheticCorpus(30000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Build(patterns)
	}
}

func BenchmarkMatch30kx8k(b *testing.B) {
	patterns, text := syntheticCorpus(30000)
	m := Build(patterns)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = m.Match(text)
	}
}
