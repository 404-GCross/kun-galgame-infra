package utils

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ParseReleaseLowerBound parses the start of a release-date range filter.
//
// Accepted formats:
//   - "YYYY"     → Jan 1, 00:00:00 UTC of that year
//   - "YYYY-MM"  → 1st of that month, 00:00:00 UTC
//   - ""         → no filter (returns zero time + nil)
//
// Used by both the wiki search endpoint (converted to Unix ts for the
// Meilisearch `released_ts` filter) and the main /galgame list endpoint
// (formatted as YYYY-MM-DD for the SQL `release_date >= ?` clause).
//
// Year-only accepts the legacy integer-year API shape: a caller passing
// `released_from=2024` (the old int contract) and a caller passing
// `released_from=2024-03` (the new month-precision API) both work; the
// caller never sees the int/string ambiguity.
func ParseReleaseLowerBound(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if len(s) == 4 {
		t, err := time.ParseInLocation("2006", s, time.UTC)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid release year %q: %w", s, err)
		}
		return t, nil
	}
	if len(s) == 7 {
		t, err := time.ParseInLocation("2006-01", s, time.UTC)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid release month %q (want YYYY-MM): %w", s, err)
		}
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid release lower bound %q (want YYYY or YYYY-MM)", s)
}

// ParseReleaseUpperBound parses the end of a release-date range filter.
// Same input formats as ParseReleaseLowerBound, but the returned time
// represents the LAST second of the period (inclusive upper bound):
//   - "YYYY"     → Dec 31, 23:59:59 UTC
//   - "YYYY-MM"  → last day of month, 23:59:59 UTC (handles 28/29/30/31
//                  day months correctly via time.Date(_, month+1, 0, ...)
//                  which normalizes to the last day of the previous month)
//   - ""         → no filter (zero time + nil)
//
// "Inclusive" is the caller's API contract: `released_to=2024-03` means
// "include everything released in March 2024 up to the final second".
// The SQL/Meilisearch side uses `<=` against this upper bound.
func ParseReleaseUpperBound(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if len(s) == 4 {
		t, err := time.ParseInLocation("2006", s, time.UTC)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid release year %q: %w", s, err)
		}
		return time.Date(t.Year(), 12, 31, 23, 59, 59, 0, time.UTC), nil
	}
	if len(s) == 7 {
		t, err := time.ParseInLocation("2006-01", s, time.UTC)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid release month %q (want YYYY-MM): %w", s, err)
		}
		// time.Date with day=0 normalizes to the LAST DAY of the previous
		// month — so (year, month+1, 0) gives us the last day of `month`
		// without us having to special-case Feb / leap years / 30 vs 31.
		lastDay := time.Date(t.Year(), t.Month()+1, 0, 23, 59, 59, 0, time.UTC)
		return lastDay, nil
	}
	return time.Time{}, fmt.Errorf("invalid release upper bound %q (want YYYY or YYYY-MM)", s)
}

// ParseMonthSet parses a comma-separated list of calendar months into a
// deduplicated, ascending []int. It powers "discontinuous months within a
// continuous year range" filtering — e.g. released_months=3,7,12 combined
// with released_from=2020&released_to=2024 means "March / July / December
// releases across 2020–2024". The month set is an orthogonal AND filter
// layered on top of the year-range bounds; absent (empty) = no month filter.
//
//   - ""            → nil (no filter)
//   - "3,7,12"      → []int{3, 7, 12}
//   - "12, 3, 3"    → []int{3, 12}   (sorted + deduped, whitespace tolerated)
//
// Out-of-range or non-integer entries return an error so the caller can
// surface a 400 rather than silently dropping the filter.
func ParseMonthSet(s string) ([]int, error) {
	if s == "" {
		return nil, nil
	}
	seen := make(map[int]bool, 12)
	var months []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		m, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("invalid month %q (want integer 1-12): %w", part, err)
		}
		if m < 1 || m > 12 {
			return nil, fmt.Errorf("invalid month %d (want 1-12)", m)
		}
		if !seen[m] {
			seen[m] = true
			months = append(months, m)
		}
	}
	sort.Ints(months)
	return months, nil
}
