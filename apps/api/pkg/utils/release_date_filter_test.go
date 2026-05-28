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

func TestParseMonthSet(t *testing.T) {
	cases := []struct {
		in   string
		want []int
	}{
		{"", nil},
		{"3", []int{3}},
		{"3,7,12", []int{3, 7, 12}},
		{"12,3,7", []int{3, 7, 12}},      // sorted
		{"3,3,7,7", []int{3, 7}},          // deduped
		{" 3 , 7 ,12 ", []int{3, 7, 12}}, // whitespace tolerated
		{"1,2,3,4,5,6,7,8,9,10,11,12", []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}},
		{"3,,7", []int{3, 7}}, // empty token skipped
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := ParseMonthSet(c.in)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v want %v", got, c.want)
				}
			}
		})
	}
}

func TestParseMonthSetErrors(t *testing.T) {
	cases := []string{"0", "13", "-1", "abc", "3,abc", "3,15"}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if _, err := ParseMonthSet(c); err == nil {
				t.Fatalf("expected error for %q, got nil", c)
			}
		})
	}
}
