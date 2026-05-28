package utils

import (
	"testing"
	"time"
)

func TestParseReleaseLowerBound(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Time
		wantErr bool
	}{
		{"", time.Time{}, false},
		{"2024", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), false},
		{"2024-03", time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC), false},
		{"2024-02", time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC), false},
		{"2024-12", time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC), false},
		// Invalid
		{"24", time.Time{}, true},
		{"2024-3", time.Time{}, true},
		{"2024-13", time.Time{}, true},
		{"garbage", time.Time{}, true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := ParseReleaseLowerBound(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, c.wantErr)
			}
			if !got.Equal(c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}

func TestParseReleaseUpperBound(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
	}{
		{"", time.Time{}},
		{"2024", time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC)},
		{"2024-03", time.Date(2024, 3, 31, 23, 59, 59, 0, time.UTC)}, // 31-day month
		// 30-day months
		{"2024-04", time.Date(2024, 4, 30, 23, 59, 59, 0, time.UTC)},
		{"2024-11", time.Date(2024, 11, 30, 23, 59, 59, 0, time.UTC)},
		// Feb leap / non-leap — time.Date(_, month+1, 0) handles both
		{"2024-02", time.Date(2024, 2, 29, 23, 59, 59, 0, time.UTC)}, // leap
		{"2023-02", time.Date(2023, 2, 28, 23, 59, 59, 0, time.UTC)}, // non-leap
		// Year boundary: Dec → Jan of next year
		{"2024-12", time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC)},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := ParseReleaseUpperBound(c.in)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if !got.Equal(c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}

func TestParseReleaseUpperBoundErrors(t *testing.T) {
	cases := []string{"24", "2024-3", "2024-13", "garbage"}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			_, err := ParseReleaseUpperBound(c)
			if err == nil {
				t.Fatalf("expected error for %q, got nil", c)
			}
		})
	}
}
