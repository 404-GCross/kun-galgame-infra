package getchuchars

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

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
