package getchutitlerefs

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// confirmByRoster decides each candidate's tier.
//
// The rosters are the SECOND signal family: the match was made on title, brand
// and date, and looked at no character at all. If the two catalogues describe
// the same game they should name at least one character in common, and if the
// match is wrong they have no reason to.
//
// A non-overlap is NOT evidence of a bad match — the two sides transcribe names
// differently (a romaji gloss, a different separator, katakana against
// romaji), and NormPersonName only closes some of that. So an unconfirmed row
// is left at `probable` rather than dropped: the claim is "not yet
// corroborated", not "refuted".
//
// Catalog names are compared through BOTH display_name and latin, because a
// work whose roster is romanised will otherwise never meet a Japanese Getchu
// roster.
func confirmByRoster(ctx context.Context, db, gdb *gorm.DB, cands []candidate, st *Stats) (map[string]bool, error) {
	if len(cands) == 0 {
		return map[string]bool{}, nil
	}
	workIDs := make([]int64, 0, len(cands))
	getchuIDs := make([]string, 0, len(cands))
	for _, c := range cands {
		workIDs = append(workIDs, c.WorkID)
		getchuIDs = append(getchuIDs, c.GetchuID)
	}

	catalog := map[int64]map[string]bool{}
	for i := 0; i < len(workIDs); i += 5000 {
		batch := workIDs[i:min(i+5000, len(workIDs))]
		var rows []struct {
			WorkID int64  `gorm:"column:work_id"`
			Name   string `gorm:"column:name"`
			Latin  string `gorm:"column:latin"`
		}
		if err := db.WithContext(ctx).Raw(`
			SELECT wc.work_id, ch.display_name AS name, coalesce(ch.latin,'') AS latin
			FROM catalog_work_character wc
			JOIN catalog_character ch ON ch.id = wc.character_id AND ch.deleted_at IS NULL
			WHERE wc.work_id IN ?`, batch).Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("load catalog rosters: %w", err)
		}
		for _, r := range rows {
			set := catalog[r.WorkID]
			if set == nil {
				set = map[string]bool{}
				catalog[r.WorkID] = set
			}
			set[NormPersonName(r.Name)] = true
			if r.Latin != "" {
				set[NormPersonName(r.Latin)] = true
			}
		}
	}

	staging := map[string]map[string]bool{}
	for i := 0; i < len(getchuIDs); i += 5000 {
		batch := getchuIDs[i:min(i+5000, len(getchuIDs))]
		var rows []struct {
			GetchuID string `gorm:"column:getchu_id"`
			Name     string `gorm:"column:name"`
			Reading  string `gorm:"column:reading"`
		}
		if err := gdb.WithContext(ctx).Raw(`
			SELECT getchu_id, name, coalesce(reading,'') AS reading
			FROM item_characters WHERE getchu_id IN ? AND btrim(name) <> ''`, batch).
			Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("load staging rosters: %w", err)
		}
		for _, r := range rows {
			set := staging[r.GetchuID]
			if set == nil {
				set = map[string]bool{}
				staging[r.GetchuID] = set
			}
			set[NormPersonName(r.Name)] = true
			if r.Reading != "" {
				set[NormPersonName(r.Reading)] = true
			}
		}
	}

	confirmed := make(map[string]bool, len(cands))
	for _, c := range cands {
		g, cat := staging[c.GetchuID], catalog[c.WorkID]
		if len(g) == 0 || len(cat) == 0 {
			st.NoRoster++
			continue
		}
		st.Testable++
		for name := range g {
			if name != "" && cat[name] {
				confirmed[c.GetchuID] = true
				break
			}
		}
		if confirmed[c.GetchuID] {
			st.Confirmed++
		} else {
			st.Unconfirmed++
		}
	}
	return confirmed, nil
}
