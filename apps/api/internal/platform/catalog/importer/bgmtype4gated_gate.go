package importer

// Stateless gate-decision, collision and sampling helpers for the Bangumi type-4
// gated expansion (refs/proj/78) — kept apart from the run/creation flow so the
// main file stays under the size guideline.

import (
	"math/bits"
	"math/rand"
	"unicode"
)

// tallySignals accumulates the per-signal + overlap matrix over the eligible pool.
func tallySignals(st *BgmGatedStats, p, t, x bool) {
	if p {
		st.SigP++
	}
	if t {
		st.SigT++
	}
	if x {
		st.SigX++
	}
	switch {
	case p && t && x:
		st.All3++
	}
	if p && t {
		st.PT++
	}
	if p && x {
		st.PX++
	}
	if t && x {
		st.TX++
	}
	if p && !t && !x {
		st.POnly++
	}
	if t && !p && !x {
		st.TOnly++
	}
	if x && !p && !t {
		st.XOnly++
	}
}

// collide reports whether either normalized title (≥4) already exists on a work.
func collide(r poolRow, wt map[string]wtNorm) (BgmGatedCollision, bool) {
	for _, n := range []string{r.NameNorm, r.NameCNNorm} {
		if runeLen(n) < bgmGatedMinLen {
			continue
		}
		if w, ok := wt[n]; ok {
			return BgmGatedCollision{
				SubjectID: r.ID, Name: r.Name, NameCN: r.NameCN,
				CollidedNorm: n, WorkID: w.workID, WorkTitle: w.title,
			}, true
		}
	}
	return BgmGatedCollision{}, false
}

// dropIntraCollisions removes every survivor that shares a normalized title with
// ANOTHER survivor (the bidirectional-uniqueness guard within the wave: two
// same-title subjects are ambiguous, so neither is minted — they stay reconcile
// candidates, 漏收可补).
func dropIntraCollisions(cands []candidate, st *BgmGatedStats) []candidate {
	subjectsPerNorm := make(map[string]map[int64]struct{})
	note := func(norm string, id int64) {
		if runeLen(norm) < bgmGatedMinLen {
			return
		}
		if subjectsPerNorm[norm] == nil {
			subjectsPerNorm[norm] = make(map[int64]struct{})
		}
		subjectsPerNorm[norm][id] = struct{}{}
	}
	for _, c := range cands {
		note(c.row.NameNorm, c.row.ID)
		note(c.row.NameCNNorm, c.row.ID)
	}
	dupNorm := func(norm string) bool {
		return runeLen(norm) >= bgmGatedMinLen && len(subjectsPerNorm[norm]) > 1
	}
	out := cands[:0]
	for _, c := range cands {
		if dupNorm(c.row.NameNorm) || dupNorm(c.row.NameCNNorm) {
			st.SkippedIntraCollision++
			continue
		}
		out = append(out, c)
	}
	return out
}

// pickRandomSample returns a deterministic (seeded) random sample of ≤100
// to-create subjects — the reviewer's approval artifact.
func pickRandomSample(cands []candidate) []BgmGatedSample {
	idx := make([]int, len(cands))
	for i := range idx {
		idx[i] = i
	}
	rng := rand.New(rand.NewSource(bgmGatedSampleSeed))
	rng.Shuffle(len(idx), func(i, j int) { idx[i], idx[j] = idx[j], idx[i] })
	n := min(bgmGatedSampleN, len(idx))
	out := make([]BgmGatedSample, 0, n)
	for _, i := range idx[:n] {
		c := cands[i]
		out = append(out, BgmGatedSample{SubjectID: c.row.ID, Name: c.row.Name, NameCN: c.row.NameCN, Signals: c.signals})
	}
	return out
}

func signalString(p, t, x bool) string {
	var parts []string
	if p {
		parts = append(parts, "P")
	}
	if t {
		parts = append(parts, "T")
	}
	if x {
		parts = append(parts, "X")
	}
	out := ""
	for i, s := range parts {
		if i > 0 {
			out += "+"
		}
		out += s
	}
	return out
}

// corpusCount is the number of distinct corpora the bitmask represents.
func corpusCount(mask uint8) int { return bits.OnesCount8(mask) }

// pureASCIIHits reports whether EVERY corpus-hitting normalized title lacks any
// CJK character (the reviewer's "pure ASCII" discriminator — a hit whose title
// carries kana/kanji/hanzi is treated as Japanese/Chinese and is NOT pure ASCII).
// Only the strings that actually produced a corpus hit are tested.
func pureASCIIHits(r poolRow, nMask, cnMask uint8) bool {
	hit := false
	if nMask != 0 {
		if hasCJK(r.NameNorm) {
			return false
		}
		hit = true
	}
	if cnMask != 0 {
		if hasCJK(r.NameCNNorm) {
			return false
		}
		hit = true
	}
	return hit
}

// hasCJK reports whether s contains any Han ideograph, Hiragana or Katakana rune
// (halfwidth katakana included) — i.e. any Japanese/Chinese script character.
func hasCJK(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
			return true
		}
	}
	return false
}

// bgmSampleOf builds a sample row for the evidence lists.
func bgmSampleOf(r poolRow, signals string) BgmGatedSample {
	return BgmGatedSample{SubjectID: r.ID, Name: r.Name, NameCN: r.NameCN, Signals: signals}
}

// runeLen is the character length of s (Postgres length() semantics), the
// isomorphic counterpart to the SQL-side `length(norm) >= 4` floors.
func runeLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
