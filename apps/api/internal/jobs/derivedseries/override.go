package derivedseries

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

// MemberHash is the membership snapshot an override was reviewed against:
// sha256 (hex) over the sorted member work ids joined by commas. Exported for
// the review tool (cmd/apply-series-name-overrides), which must compute the
// same value the builder will verify.
func MemberHash(works []int64) string {
	ids := append([]int64(nil), works...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, ",")))
	return hex.EncodeToString(sum[:])
}

type overrideRow struct {
	ExternalID  string `gorm:"column:external_id"`
	MemberHash  string `gorm:"column:member_hash"`
	DisplayName string `gorm:"column:display_name"`
}

// applyOverrides swaps candidates' mechanical names for reviewed ones where the
// membership snapshot still matches, and reaps the rows it can no longer trust:
// an override whose hash mismatches (the component changed) or whose series the
// graph no longer produces. Both are counted; the reap only writes on apply.
func applyOverrides(ctx context.Context, db *gorm.DB, src int16,
	want map[string]*candidate, opts Opts, st *Stats) error {
	var rows []overrideRow
	if err := db.WithContext(ctx).Raw(`
		SELECT external_id, member_hash, display_name
		FROM catalog_series_name_override WHERE source_id = ?`, src).Scan(&rows).Error; err != nil {
		return fmt.Errorf("load name overrides: %w", err)
	}
	var stale []string
	for _, r := range rows {
		cand, ok := want[r.ExternalID]
		if !ok || MemberHash(cand.works) != r.MemberHash {
			stale = append(stale, r.ExternalID)
			continue
		}
		// The mechanical counters already ticked for this candidate; move it
		// into the override bucket so the three counts stay a partition.
		switch cand.namedBy {
		case "prefix":
			st.NamedByPrefix--
		case "fallback":
			st.NamedByFallback--
		}
		cand.name = r.DisplayName
		cand.namedBy = "override"
		st.NamedByOverride++
	}
	st.OverridesStale = len(stale)
	if opts.Apply && len(stale) > 0 {
		if err := db.WithContext(ctx).Exec(`
			DELETE FROM catalog_series_name_override
			WHERE source_id = ? AND external_id IN ?`, src, stale).Error; err != nil {
			return fmt.Errorf("reap stale name overrides: %w", err)
		}
	}
	return nil
}
