package getchuchars

import "testing"

func rr(getchu string, work, char int64, name, alias string) rosterRow {
	return rosterRow{GetchuID: getchu, WorkID: work, CharacterID: char,
		KeyName: normKey(name), KeyAlias: normKey(alias)}
}

// TestMatchLevels pins the three-level ladder and what each level is worth.
// Measured on 24,067 crawled characters: display name 17,299, +alias 50,
// +furigana 1,012. The furigana level earns its code; alias is nearly free to
// keep and nearly worthless to have.
func TestMatchLevels(t *testing.T) {
	idx := buildIndex([]rosterRow{
		rr("g1", 1, 100, "九條都", ""),
		rr("g1", 1, 101, "新海天", "そら"),
		rr("g1", 1, 102, "香坂春風", "こうさかはるかぜ"),
	})
	got, st := match([]getchuChar{
		// L1: Getchu writes an ideographic space the catalog does not have.
		{GetchuID: "g1", Ordinal: 0, Name: "九條　都", Profile: "p"},
		// L2: the display name misses, an alias carries it.
		{GetchuID: "g1", Ordinal: 1, Name: "そら", Profile: "p"},
		// L3: neither name form matches, but Getchu's furigana does. This is
		// the level worth 1,012 of 24,067 in the measurement.
		{GetchuID: "g1", Ordinal: 2, Name: "香坂 春風（はるかぜ）", Reading: "こうさかはるかぜ", Profile: "p"},
	}, idx)

	if st.ByName != 1 || st.ByAlias != 1 || st.ByReading != 1 {
		t.Errorf("levels = name %d alias %d reading %d; want 1/1/1", st.ByName, st.ByAlias, st.ByReading)
	}
	if st.Matched != 3 || len(got) != 3 {
		t.Errorf("matched = %d, candidates = %d; want 3/3 — each level resolves a different character",
			st.Matched, len(got))
	}
	seen := map[string]bool{}
	for _, c := range got {
		seen[c.MatchedBy] = true
	}
	for _, want := range []string{MatchDisplayName, MatchAlias, MatchReading} {
		if !seen[want] {
			t.Errorf("no candidate carries matched_by=%q — the audit trail is incomplete", want)
		}
	}
}

// TestCollisionDropsBothSides pins the other half of the ambiguity rule: two
// crawled rows resolving to ONE catalog character is the same problem seen from
// the other direction, and both are dropped.
func TestCollisionDropsBothSides(t *testing.T) {
	idx := buildIndex([]rosterRow{rr("g1", 1, 101, "新海天", "そら")})
	got, st := match([]getchuChar{
		{GetchuID: "g1", Ordinal: 0, Name: "新海天"},
		{GetchuID: "g1", Ordinal: 1, Name: "そら"}, // the alias of the same character
	}, idx)
	if len(got) != 0 || st.Collided != 2 || st.Matched != 0 {
		t.Errorf("both sides of a collision must drop: got %d candidates, stats %+v", len(got), st)
	}
}

// TestAmbiguityIsDropped pins the safety rule: within one work, a name that
// resolves to two characters is where a wrong link is MOST plausible (twins, a
// shared surname), so it is skipped rather than picked.
func TestAmbiguityIsDropped(t *testing.T) {
	idx := buildIndex([]rosterRow{
		rr("g1", 1, 200, "神楽坂", ""),
		rr("g1", 1, 201, "神楽坂", ""), // two characters, same display name
	})
	got, st := match([]getchuChar{{GetchuID: "g1", Name: "神楽坂", Profile: "p"}}, idx)
	if len(got) != 0 || st.Ambiguous != 1 || st.Matched != 0 {
		t.Errorf("ambiguous name must be dropped: got %d candidates, stats %+v", len(got), st)
	}
}

// TestUnanchoredAndUnknownAreCounted pins that every input lands in exactly one
// bucket — a run can never silently shrink its population.
func TestUnanchoredAndUnknownAreCounted(t *testing.T) {
	idx := buildIndex([]rosterRow{rr("g1", 1, 300, "有栖川", "")})
	_, st := match([]getchuChar{
		{GetchuID: "g1", Name: "有栖川"},   // matched
		{GetchuID: "g1", Name: "知らない子"}, // roster exists, no form matches
		{GetchuID: "g9", Name: "誰か"},    // the work is not anchored at all
	}, idx)
	if st.Matched+st.NoNameInWork+st.NoWork+st.Ambiguous+st.Collided != st.Input {
		t.Errorf("buckets do not add up to the input: %+v", st)
	}
	if st.NoWork != 1 || st.NoNameInWork != 1 || st.Matched != 1 {
		t.Errorf("stats = %+v", st)
	}
}

func TestNormKey(t *testing.T) {
	cases := map[string]string{
		"九條　都":  "九條都", // ideographic space
		"九條 都":  "九條都",
		"ＡＢＣ":   "abc", // fullwidth → NFKC → lowercase
		" そら\t": "そら",
	}
	for in, want := range cases {
		if got := normKey(in); got != want {
			t.Errorf("normKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestEditionsAreDedupedNotDropped pins the distinction the first real run
// exposed: 3,108 works carry more than one getchu id (限定版 / 通常版 / DL版),
// so the same roster is crawled once per edition. Treating that as ambiguity
// threw away a third of the match set.
func TestEditionsAreDedupedNotDropped(t *testing.T) {
	// One catalog work, two getchu items — the same product's two editions.
	idx := buildIndex([]rosterRow{
		rr("g1", 1, 400, "九條都", ""),
		rr("g2", 1, 400, "九條都", ""),
	})
	got, st := match([]getchuChar{
		{GetchuID: "g1", Ordinal: 0, Name: "九條都", Profile: "短い"},
		{GetchuID: "g2", Ordinal: 0, Name: "九條都", Profile: "こちらのほうが長い紹介文です"},
	}, idx)

	if st.Matched != 1 || len(got) != 1 {
		t.Fatalf("editions must fold to one candidate: matched=%d candidates=%d", st.Matched, len(got))
	}
	if st.Deduped != 1 {
		t.Errorf("deduped = %d, want 1", st.Deduped)
	}
	if st.Collided != 0 {
		t.Errorf("collided = %d — editions are not ambiguity", st.Collided)
	}
	// The richer text wins, so folding never costs content.
	if got[0].Profile != "こちらのほうが長い紹介文です" {
		t.Errorf("kept the shorter profile: %q", got[0].Profile)
	}
}

// TestRichestIsDeterministic pins that the fold does not depend on map order.
func TestRichestIsDeterministic(t *testing.T) {
	g := []Candidate{
		{GetchuID: "g9", Ordinal: 1, Profile: "同じ長さ"},
		{GetchuID: "g2", Ordinal: 0, Profile: "同じ長さ"},
		{GetchuID: "g5", Ordinal: 3, Profile: "同じ長さ"},
	}
	for i := 0; i < 20; i++ {
		if got := richest(g); got.GetchuID != "g2" {
			t.Fatalf("tie-break drifted: got %s", got.GetchuID)
		}
	}
}
