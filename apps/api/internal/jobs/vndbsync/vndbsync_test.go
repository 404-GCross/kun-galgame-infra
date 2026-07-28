package vndbsync

import "testing"

// TestCancelledVaporware pins the relaxed devstatus=2 gate (2026-07): a
// cancelled VN is skipped ONLY when it has no real release date. Dated
// cancelled entries (trial or full release shipped before development
// stopped, e.g. v1912 EDEN) are admitted as claimable drafts — the old
// skip-all filter left ~1.5k released games invisible to the publish
// selector and broke moyu's "every VNDB entry is a claimable draft" premise.
func TestCancelledVaporware(t *testing.T) {
	str := func(s string) *string { return &s }

	cases := []struct {
		name     string
		released *string
		want     bool
	}{
		{"nil released", nil, true},
		{"empty string", str(""), true},
		{"tba lowercase", str("tba"), true},
		{"TBA uppercase (non-digit → nil parse)", str("TBA"), true},
		{"unknown", str("unknown"), true},
		{"garbage", str("soon™"), true},
		{"year out of range", str("1600"), true},
		{"full date (v1912 EDEN)", str("2008-04-04"), false},
		{"month precision", str("2008-04"), false},
		{"year precision", str("2008"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vn := &vndbVN{ID: "v1912", Devstatus: 2, Released: c.released}
			if got := cancelledVaporware(vn); got != c.want {
				t.Errorf("cancelledVaporware(released=%v) = %v, want %v", c.released, got, c.want)
			}
		})
	}
}
