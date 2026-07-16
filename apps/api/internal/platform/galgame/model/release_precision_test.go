package model

import "testing"

func TestDeriveInputPrecision(t *testing.T) {
	cases := []struct {
		date string
		tba  bool
		want ReleasePrecision
	}{
		{"", false, PrecisionUnknown},
		{"2026", false, PrecisionYear},
		{"2026-06", false, PrecisionMonth},
		{"2026-06-15", false, PrecisionDay},
		{"2026-06-15", true, PrecisionTBA}, // tba wins over a set date
		{"", true, PrecisionTBA},
	}
	for _, c := range cases {
		if got := DeriveInputPrecision(c.date, c.tba); got != c.want {
			t.Errorf("DeriveInputPrecision(%q, %v) = %q, want %q", c.date, c.tba, got, c.want)
		}
	}
}

func TestNormalizeReleaseDateInput(t *testing.T) {
	cases := []struct {
		in   string
		want string // "" = nil
	}{
		{"", ""},
		{"unknown", ""},
		{"tba", ""},
		{"2026", "2026-01-01"},       // year → Jan 1
		{"2026-06", "2026-06-01"},    // month → 1st
		{"2026-06-15", "2026-06-15"}, // day → as-is
	}
	for _, c := range cases {
		got := NormalizeReleaseDateInput(c.in)
		if c.want == "" {
			if got != nil {
				t.Errorf("NormalizeReleaseDateInput(%q) = %v, want nil", c.in, *got)
			}
			continue
		}
		if got == nil || *got != c.want {
			t.Errorf("NormalizeReleaseDateInput(%q) = %v, want %s", c.in, got, c.want)
		}
	}
}

func TestResolveReleasePrecision(t *testing.T) {
	d := "2026-06-15"
	cases := []struct {
		name string
		snap Snapshot
		want ReleasePrecision
	}{
		{"stored wins", Snapshot{ReleasePrecision: "month", ReleaseDate: &d}, PrecisionMonth},
		{"empty + tba", Snapshot{ReleaseDateTBA: true}, PrecisionTBA},
		{"empty + date", Snapshot{ReleaseDate: &d}, PrecisionDay},
		{"empty + nothing", Snapshot{}, PrecisionUnknown},
	}
	for _, c := range cases {
		if got := c.snap.ResolveReleasePrecision(); got != c.want {
			t.Errorf("%s: ResolveReleasePrecision() = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSnapshotReleasePrecisionDiffAndApply(t *testing.T) {
	old := &Snapshot{ReleasePrecision: "day"}
	neu := &Snapshot{ReleasePrecision: "month"}
	keys := ChangedKeys(old, neu)
	if !keys["release_precision"] {
		t.Fatal("ChangedKeys did not flag release_precision")
	}
	target := &Snapshot{ReleasePrecision: "day"}
	ApplyChanges(target, neu, keys)
	if target.ReleasePrecision != "month" {
		t.Errorf("ApplyChanges: release_precision = %q, want month", target.ReleasePrecision)
	}

	// No spurious diff when precision is unchanged.
	if ChangedKeys(&Snapshot{ReleasePrecision: "day"}, &Snapshot{ReleasePrecision: "day"})["release_precision"] {
		t.Error("ChangedKeys flagged release_precision when unchanged")
	}
}

func TestParseLegacyReleased(t *testing.T) {
	cases := []struct {
		in       string
		wantDate string // "" = nil
		wantPrec ReleasePrecision
	}{
		{"", "", PrecisionUnknown},
		{"unknown", "", PrecisionUnknown},
		{"tba", "", PrecisionTBA},
		{"2026", "2026-01-01", PrecisionYear},
		{"2026-06", "2026-06-01", PrecisionMonth},
		{"2026-06-15", "2026-06-15", PrecisionDay},
		{"  2026-06-15  ", "2026-06-15", PrecisionDay}, // whitespace trimmed
		{"2026-13", "2026-01-01", PrecisionYear},       // bad month degrades to year
		{"2026-06-45", "2026-06-01", PrecisionMonth},   // bad day degrades to month
		{"1899", "", PrecisionUnknown},                 // below [1900,2100]
		{"2101", "", PrecisionUnknown},                 // above [1900,2100]
		{"garbage", "", PrecisionUnknown},
	}
	for _, c := range cases {
		gotDate, gotPrec := ParseLegacyReleased(c.in)
		if gotPrec != c.wantPrec {
			t.Errorf("ParseLegacyReleased(%q) precision = %q, want %q", c.in, gotPrec, c.wantPrec)
		}
		if c.wantDate == "" {
			if gotDate != nil {
				t.Errorf("ParseLegacyReleased(%q) date = %v, want nil", c.in, gotDate)
			}
			continue
		}
		if gotDate == nil {
			t.Errorf("ParseLegacyReleased(%q) date = nil, want %s", c.in, c.wantDate)
			continue
		}
		if got := gotDate.UTC().Format("2006-01-02"); got != c.wantDate {
			t.Errorf("ParseLegacyReleased(%q) date = %s, want %s", c.in, got, c.wantDate)
		}
	}
}
