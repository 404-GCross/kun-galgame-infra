package bgmzhnames

import (
	"context"
	"fmt"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Lane names the entity family a run projects into. The three lanes share the
// whole parse layer (which infobox keys are trusted, the Chinese test) and the
// whole write discipline (fill-missing per (owner, name), one primary claim,
// ON CONFLICT DO NOTHING, dry-run default); they differ only in which anchor
// they follow, which table they write, and who owns the alias row.
type Lane string

const (
	// LaneCharacter is the original wave (refs/proj/151): catalog_character
	// anchors → catalog_character_alias.
	LaneCharacter Lane = "character"
	// LanePerson follows PERSON-level anchors (entity_type=0) and writes to
	// catalog_name_alias, hung off the person's PRIMARY credit name — aliases
	// belong to a name, not to a person (the doc 10 invariant).
	LanePerson Lane = "person"
	// LaneLabel follows label anchors (entity_type=3) into catalog_label_alias.
	// Its Bangumi rows are `person` rows of type 2/3 (company / group): Bangumi
	// files a 会社 in the same table as an individual, so the staging join and
	// the infobox shape are identical to the person lane's.
	LaneLabel Lane = "label"
)

// anchoredEntity is one live entity joined to its Bangumi staging row.
//
// EntityID is what the anchor points at (and what the samples report);
// OwnerID is who the alias row hangs off — the same id for character and
// label, but the person's PRIMARY CREDIT NAME for the person lane. It is
// nullable because a person may legally have no primary credit name yet, and
// counting those is more useful than filtering them away in SQL.
type anchoredEntity struct {
	EntityID   int64          `gorm:"column:entity_id"`
	OwnerID    *int64         `gorm:"column:owner_id"`
	OwnerName  string         `gorm:"column:owner_name"`
	ExternalID string         `gorm:"column:external_id"`
	Infobox    datatypes.JSON `gorm:"column:infobox_parsed"`
}

// laneSpec is everything that differs between the three lanes.
type laneSpec struct {
	// load resolves the lane's live, EXACT-anchored entities with their parsed
	// infobox. The dirty-infobox guard deliberately stays OUT of the query, so
	// a run reports how many rows it refused instead of narrowing its own
	// universe (the character lane's rule).
	load func(ctx context.Context, db *gorm.DB, sourceID int16, limit, offset int) ([]anchoredEntity, error)
	// preload reports what each OWNER already carries in zh-Hans. This is the
	// primary skip; the ON CONFLICT clause is only the backstop.
	preload func(ctx context.Context, db *gorm.DB, ownerIDs []int64) (map[int64]*zhAliasState, error)
	// hostWorks maps an owner to the live works whose read face renders its
	// names — the set a real write bumps through the public changes feed.
	// nil means the lane has NO touch discipline (see the package doc).
	hostWorks func(ctx context.Context, db *gorm.DB, ownerIDs []int64) (map[int64][]int64, error)
	// insert writes one alias row, reporting false when ON CONFLICT absorbed it.
	insert func(ctx context.Context, db *gorm.DB, ownerID int64, name string, primary bool) (bool, error)
	// dropOwnerName drops a projected name that is already the owner's own
	// display name. Only the lanes whose owner can ALREADY be spelled in
	// Chinese set it — see Stats.SkippedSameAsOwner.
	dropOwnerName bool
}

// laneFor resolves the lane, defaulting to the original character wave so an
// existing invocation keeps its meaning.
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

// zhAliasState is what an owner already carries in zh-Hans: the exact names
// (the uniqueness key's third component is the language, so only zh-Hans rows
// can collide) and whether one of them is already the locale primary.
type zhAliasState struct {
	names      map[string]bool
	hasPrimary bool
}

// preloadZhAliasesBy loads that state from any of the three alias tables. The
// table and owner column are lane constants, never user input.
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
