package getchuchars

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// SourceID reads the catalog_source row this whole Getchu family anchors on.
// Every Getchu lane needs it and none of them should hardcode the integer: the
// id is assigned by the seed, not by us.
func SourceID(ctx context.Context, db *gorm.DB) (int16, error) {
	var id int16
	if err := db.WithContext(ctx).
		Raw(`SELECT id FROM catalog_source WHERE key = 'getchu'`).Scan(&id).Error; err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, fmt.Errorf("catalog_source has no getchu row — seed it first (refs/proj/167)")
	}
	return id, nil
}

// Resolve produces the (catalog character → Getchu roster row) links: it reads
// both sides and runs the within-work name match.
//
// It is exported so the PORTRAIT lane reuses this exact matcher rather than
// keeping a second copy. That is not tidiness — the two lanes must agree on
// which catalog character a Getchu roster entry is, because one writes that
// character's prose and the other writes its face. A private copy that drifted
// by one normalization rule would put the wrong girl's portrait on a profile,
// and nothing downstream could tell.
//
// Callers get the ambiguity policy for free: a name matching two roster
// characters, or two rows of one item matching one character, is DROPPED.
func Resolve(ctx context.Context, db, gdb *gorm.DB, getchuSource int16) ([]Candidate, MatchStats, error) {
	roster, err := loadRoster(ctx, db, getchuSource)
	if err != nil {
		return nil, MatchStats{}, err
	}
	chars, err := loadGetchuChars(ctx, gdb)
	if err != nil {
		return nil, MatchStats{}, err
	}
	cands, ms := match(chars, buildIndex(roster))
	return cands, ms, nil
}
