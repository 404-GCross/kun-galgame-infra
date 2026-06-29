package model

import "testing"

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
