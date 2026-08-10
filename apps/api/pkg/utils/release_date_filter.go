package utils

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

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
		lastDay := time.Date(t.Year(), t.Month()+1, 0, 23, 59, 59, 0, time.UTC)
		return lastDay, nil
	}
	return time.Time{}, fmt.Errorf("invalid release upper bound %q (want YYYY or YYYY-MM)", s)
}

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
