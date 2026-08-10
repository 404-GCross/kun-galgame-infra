package bgmzhnames

import (
	"context"
	"fmt"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type Lane string

const (
	LaneCharacter Lane = "character"
	LanePerson    Lane = "person"
	LaneLabel     Lane = "label"
)

type anchoredEntity struct {
	EntityID   int64          `gorm:"column:entity_id"`
	OwnerID    *int64         `gorm:"column:owner_id"`
	OwnerName  string         `gorm:"column:owner_name"`
	ExternalID string         `gorm:"column:external_id"`
	Infobox    datatypes.JSON `gorm:"column:infobox_parsed"`
}

type laneSpec struct {
	load          func(ctx context.Context, db *gorm.DB, sourceID int16, limit, offset int) ([]anchoredEntity, error)
	preload       func(ctx context.Context, db *gorm.DB, ownerIDs []int64) (map[int64]*zhAliasState, error)
	hostWorks     func(ctx context.Context, db *gorm.DB, ownerIDs []int64) (map[int64][]int64, error)
	insert        func(ctx context.Context, db *gorm.DB, ownerID int64, name string, primary bool) (bool, error)
	dropOwnerName bool
}

func laneFor(l Lane) (laneSpec, error) {
	switch l {
	case "", LaneCharacter:
		return characterLane(), nil
	case LanePerson:
		return personLane(), nil
	case LaneLabel:
		return labelLane(), nil
	default:
		return laneSpec{}, fmt.Errorf("unknown lane %q (want character|person|label)", l)
	}
}

type zhAliasState struct {
	names      map[string]bool
	hasPrimary bool
}

func preloadZhAliasesBy(ctx context.Context, db *gorm.DB, table, ownerCol string, ids []int64) (map[int64]*zhAliasState, error) {
	out := make(map[int64]*zhAliasState, len(ids))
	query := fmt.Sprintf(`SELECT %s AS owner_id, name, is_primary_for_locale
		FROM %s WHERE lang = ? AND %s IN ?`, ownerCol, table, ownerCol)
	for start := 0; start < len(ids); start += preloadChunk {
		end := min(start+preloadChunk, len(ids))
		var rows []struct {
			OwnerID   int64  `gorm:"column:owner_id"`
			Name      string `gorm:"column:name"`
			IsPrimary bool   `gorm:"column:is_primary_for_locale"`
		}
		if err := db.WithContext(ctx).Raw(query, LangZhHans, ids[start:end]).Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("preload zh-Hans aliases from %s: %w", table, err)
		}
		for _, r := range rows {
			st := out[r.OwnerID]
			if st == nil {
				st = &zhAliasState{names: map[string]bool{}}
				out[r.OwnerID] = st
			}
			st.names[r.Name] = true
			st.hasPrimary = st.hasPrimary || r.IsPrimary
		}
	}
	return out, nil
}
