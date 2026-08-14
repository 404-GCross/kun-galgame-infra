package workratings

import (
	"encoding/json"
	"strconv"
)

// A histogram is stored sparsely: the source's own score value as the key, the
// vote count at that value as the value, and absent keys meaning zero. The
// scale itself is never stored — it follows from source_id, the same rule the
// score already obeys.
func marshalBuckets(b map[string]int) []byte {
	kept := make(map[string]int, len(b))
	for k, n := range b {
		if n > 0 {
			kept[k] = n
		}
	}
	if len(kept) == 0 {
		return nil
	}
	out, err := json.Marshal(kept)
	if err != nil {
		return nil
	}
	return out
}

func bgmBuckets(details []byte) map[string]int {
	if len(details) == 0 {
		return nil
	}
	var b map[string]int
	if err := json.Unmarshal(details, &b); err != nil {
		return nil
	}
	return b
}

func bucketsTotal(b map[string]int) int {
	total := 0
	for _, n := range b {
		total += n
	}
	return total
}

func dlsiteBuckets(detail []byte) map[string]int {
	if len(detail) == 0 {
		return nil
	}
	var rows []struct {
		ReviewPoint int `json:"review_point"`
		Count       int `json:"count"`
	}
	if err := json.Unmarshal(detail, &rows); err != nil {
		return nil
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		if r.ReviewPoint > 0 {
			out[strconv.Itoa(r.ReviewPoint)] += r.Count
		}
	}
	return out
}

type ratingStats struct {
	Average *float64 `json:"average,omitempty"`
	Stdev   *float64 `json:"stdev,omitempty"`
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
}

func marshalStats(s ratingStats) []byte {
	if s.Average == nil && s.Stdev == nil && s.Min == nil && s.Max == nil {
		return nil
	}
	out, err := json.Marshal(s)
	if err != nil {
		return nil
	}
	return out
}
