package vndbresolve

import "testing"

// Reuse-first is the property that keeps the existing mixed (Chinese/English) tag
// set from gaining duplicates: ResolveTag must return an existing match — Chinese
// (canonical) OR English — without creating. The cache-hit paths return before
// touching the tx, so a nil tx proves no write happens.
func TestResolveTag_ReuseFirst(t *testing.T) {
	// Chinese already exists (canonical) → reuse it for the English VNDB name.
	r := New(map[string]string{"Protagonist": "主人公"})
	r.tagCache["主人公"] = 42
	if id, err := r.ResolveTag(nil, Tag{Name: "Protagonist"}); err != nil || id != 42 {
		t.Fatalf("Chinese reuse: got (%d,%v), want (42,nil)", id, err)
	}
	// English row already exists, no tagMap entry → reuse the English row.
	r2 := New(map[string]string{})
	r2.tagCache["Comedy"] = 7
	if id, err := r2.ResolveTag(nil, Tag{Name: "Comedy"}); err != nil || id != 7 {
		t.Fatalf("English reuse: got (%d,%v), want (7,nil)", id, err)
	}
	// Mapped, but only the English row exists (legacy) → reuse English, don't dup.
	r3 := New(map[string]string{"Loli": "萝莉"})
	r3.tagCache["Loli"] = 9
	if id, err := r3.ResolveTag(nil, Tag{Name: "Loli"}); err != nil || id != 9 {
		t.Fatalf("mapped-but-only-English reuse: got (%d,%v), want (9,nil)", id, err)
	}
	if r.NewTags() != 0 || r2.NewTags() != 0 || r3.NewTags() != 0 {
		t.Errorf("reuse must not create tags")
	}
}

func TestLookupTag(t *testing.T) {
	r := New(map[string]string{"Protagonist": "主人公"})
	r.tagCache["主人公"] = 1
	r.tagCache["Comedy"] = 2
	if id, ok := r.LookupTag(Tag{Name: "Protagonist"}); !ok || id != 1 {
		t.Errorf("mapped lookup: got (%d,%v), want (1,true)", id, ok)
	}
	if id, ok := r.LookupTag(Tag{Name: "Comedy"}); !ok || id != 2 {
		t.Errorf("english lookup: got (%d,%v), want (2,true)", id, ok)
	}
	if _, ok := r.LookupTag(Tag{Name: "Unknown"}); ok {
		t.Errorf("missing lookup must report not-found")
	}
}

func TestResolveOfficial_ReuseFirst(t *testing.T) {
	r := New(nil)
	r.officialCache["Key"] = 5
	r.officialCache["オーガスト"] = 9
	if id, err := r.ResolveOfficial(nil, Developer{Name: "Key"}); err != nil || id != 5 {
		t.Fatalf("name reuse: got (%d,%v), want (5,nil)", id, err)
	}
	if id, err := r.ResolveOfficial(nil, Developer{Name: "August", Original: "オーガスト"}); err != nil || id != 9 {
		t.Fatalf("original reuse: got (%d,%v), want (9,nil)", id, err)
	}
	if r.NewOfficials() != 0 {
		t.Errorf("reuse must not create officials")
	}
}

func TestTagSkipped(t *testing.T) {
	if !TagSkipped(Tag{Name: "x", Lie: true, Rating: 2}) {
		t.Error("lie tag must be skipped")
	}
	if !TagSkipped(Tag{Name: "x", Rating: 0.5}) {
		t.Error("below-rating tag must be skipped")
	}
	if TagSkipped(Tag{Name: "x", Rating: 1.5}) {
		t.Error("rated, non-lie tag must be kept")
	}
}
