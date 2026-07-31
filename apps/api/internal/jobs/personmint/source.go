package personmint

import (
	"context"
	"fmt"
	"sort"

	"api/internal/platform/catalog/model"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// preloadChunk keeps every IN-list under the wire protocol's 65,535 parameter
// cap (the member set is ~16k credit names, the anchor sets smaller).
const preloadChunk = 10000

// Source keys recorded in field_provenance. They double as the survivorship
// vocabulary: a field whose latest writer is NOT one of these (a human edit) is
// out of this pipeline's reach anyway, since the pipeline only ever fills an
// EMPTY column.
const (
	sourceVNDB    = "vndb"
	sourceBangumi = "bangumi"
)

// matchedBy stamps every anchor this wave writes, so the whole wave is
// revocable by one predicate (the R7 traceability rule).
const matchedBy = "import:person-mint"

// anchorKey is an et=0 identity assertion: one external id in one source's id
// space, pointing at a person.
type anchorKey struct {
	SourceID   int16
	ExternalID string
}

// memberState is one cluster member as the database holds it.
type memberState struct {
	ID       int64
	Name     string
	NameNorm string
	PersonID *int64
}

// personState is a host candidate's current field state — what survivorship is
// allowed to fill (only the NULLs) and what it must leave alone.
type personState struct {
	ID                  int64
	DisplayName         string
	PrimaryCreditNameID *int64
	Gender              *int16
	BirthY              *int16
	BirthM              *int16
	BirthD              *int16
	FieldProvenance     datatypes.JSON
}

// environment is everything the decision needs, preloaded once.
type environment struct {
	db *gorm.DB
	// Source ids resolved BY KEY, never hardcoded (the charattrs /
	// entityintros discipline).
	srcVNDB, srcBangumi, srcDLsite, srcEG int16

	members        map[int64]*memberState
	personOfMember map[int64]int64
	anchors        map[int64][]anchorKey
	creditCount    map[int64]int

	// labelNorms is every live label's (and label alias's) normalized name —
	// the organization discriminator catalog_credit_name.kind does not carry.
	labelNorms map[string]bool

	// vndb staging: aid -> staff id, staff id -> main alias aid / gender.
	staffOfAid  map[int64]string
	staffMain   map[string]int64
	staffGender map[string]string

	// bangumi staging: person id -> the infobox facts this wave reads.
	bgmFacts map[int64]bgmFacts

	// et0Owner maps an already-written person anchor to its owner, so a
	// re-run recognizes its own work instead of colliding with it.
	et0Owner map[anchorKey]int64
	persons  map[int64]*personState
}

// loadEnvironment preloads every fact the mint decides on, in dependency
// order: members → their anchors → the staging rows those anchors reach.
func loadEnvironment(ctx context.Context, db *gorm.DB, memberIDs []int64) (*environment, error) {
	env := &environment{
		db:             db,
		members:        map[int64]*memberState{},
		personOfMember: map[int64]int64{},
		anchors:        map[int64][]anchorKey{},
		creditCount:    map[int64]int{},
		labelNorms:     map[string]bool{},
		staffOfAid:     map[int64]string{},
		staffMain:      map[string]int64{},
		staffGender:    map[string]string{},
		bgmFacts:       map[int64]bgmFacts{},
		et0Owner:       map[anchorKey]int64{},
		persons:        map[int64]*personState{},
	}
	var err error
	if env.srcVNDB, err = resolveSource(ctx, db, "vndb"); err != nil {
		return nil, err
	}
	if env.srcBangumi, err = resolveSource(ctx, db, "bangumi"); err != nil {
		return nil, err
	}
	if env.srcDLsite, err = resolveSource(ctx, db, "dlsite"); err != nil {
		return nil, err
	}
	if env.srcEG, err = resolveSource(ctx, db, "erogamespace"); err != nil {
		return nil, err
	}
	if err := env.loadMembers(ctx, memberIDs); err != nil {
		return nil, err
	}
	if err := env.loadAnchors(ctx, memberIDs); err != nil {
		return nil, err
	}
	if err := env.loadCreditCounts(ctx, memberIDs); err != nil {
		return nil, err
	}
	if err := env.loadLabelNorms(ctx); err != nil {
		return nil, err
	}
	if err := env.loadVNDBStaff(ctx); err != nil {
		return nil, err
	}
	if err := env.loadBangumiPersons(ctx); err != nil {
		return nil, err
	}
	if err := env.loadExistingPersons(ctx); err != nil {
		return nil, err
	}
	return env, nil
}

func resolveSource(ctx context.Context, db *gorm.DB, key string) (int16, error) {
	var id int16
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = ?`, key).Scan(&id).Error; err != nil {
		return 0, fmt.Errorf("resolve %s source: %w", key, err)
	}
	if id == 0 {
		return 0, fmt.Errorf("registry not seeded (%s source missing)", key)
	}
	return id, nil
}

func (e *environment) loadMembers(ctx context.Context, ids []int64) error {
	for _, chunk := range chunks(ids) {
		var rows []memberState
		if err := e.db.WithContext(ctx).Raw(
			`SELECT id, name, name_norm, person_id FROM catalog_credit_name WHERE id IN ?`, chunk).
			Scan(&rows).Error; err != nil {
			return fmt.Errorf("preload credit names: %w", err)
		}
		for i := range rows {
			r := rows[i]
			e.members[r.ID] = &r
			if r.PersonID != nil {
				e.personOfMember[r.ID] = *r.PersonID
			}
		}
	}
	for _, id := range ids {
		if e.members[id] == nil {
			return fmt.Errorf("credit_name %d from the cluster file does not exist in this database — wrong snapshot?", id)
		}
	}
	return nil
}

// loadAnchors reads the et=1 (credit-name-grained) anchors. They are the raw
// material of BOTH the person anchor set and the staging lookups.
func (e *environment) loadAnchors(ctx context.Context, ids []int64) error {
	for _, chunk := range chunks(ids) {
		var rows []struct {
			EntityID   int64  `gorm:"column:entity_id"`
			SourceID   int16  `gorm:"column:source_id"`
			ExternalID string `gorm:"column:external_id"`
		}
		if err := e.db.WithContext(ctx).Raw(
			`SELECT entity_id, source_id, external_id FROM catalog_external_ref
			 WHERE entity_type = ? AND link_kind = ? AND entity_id IN ?
			 ORDER BY entity_id, source_id, external_id`,
			model.EntityTypeCreditName, model.LinkKindExact, chunk).Scan(&rows).Error; err != nil {
			return fmt.Errorf("preload credit-name anchors: %w", err)
		}
		for _, r := range rows {
			e.anchors[r.EntityID] = append(e.anchors[r.EntityID], anchorKey{SourceID: r.SourceID, ExternalID: r.ExternalID})
		}
	}
	return nil
}

// loadCreditCounts is the primary-name tiebreak of last resort: the member
// with the most credit edges wins when vndb does not declare a main alias.
func (e *environment) loadCreditCounts(ctx context.Context, ids []int64) error {
	for _, chunk := range chunks(ids) {
		var rows []struct {
			CreditNameID int64 `gorm:"column:credit_name_id"`
			N            int   `gorm:"column:n"`
		}
		if err := e.db.WithContext(ctx).Raw(
			`SELECT credit_name_id, count(*) AS n FROM catalog_credit WHERE credit_name_id IN ? GROUP BY 1`, chunk).
			Scan(&rows).Error; err != nil {
			return fmt.Errorf("preload credit counts: %w", err)
		}
		for _, r := range rows {
			e.creditCount[r.CreditNameID] = r.N
		}
	}
	return nil
}

// loadLabelNorms loads every live label name and label alias in normalized
// form. The whole vocabulary is loaded rather than probed per member: it is
// ~40k short strings, and an in-memory set makes the guard exact and free.
func (e *environment) loadLabelNorms(ctx context.Context) error {
	var norms []string
	if err := e.db.WithContext(ctx).Raw(
		`SELECT display_name_norm FROM catalog_label WHERE deleted_at IS NULL
		 UNION SELECT name_norm FROM catalog_label_alias`).Scan(&norms).Error; err != nil {
		return fmt.Errorf("preload label names: %w", err)
	}
	for _, n := range norms {
		if n != "" {
			e.labelNorms[n] = true
		}
	}
	return nil
}

// loadVNDBStaff resolves the vndb aids carried by the members into STAFF ids —
// the et=0 anchor — plus the staff's main alias and gender.
func (e *environment) loadVNDBStaff(ctx context.Context) error {
	aids := make([]string, 0, len(e.anchors))
	for _, list := range e.anchors {
		for _, a := range list {
			if a.SourceID == e.srcVNDB {
				aids = append(aids, a.ExternalID)
			}
		}
	}
	sort.Strings(aids)
	for _, chunk := range chunks(aids) {
		var rows []struct {
			Aid    int64  `gorm:"column:aid"`
			ID     string `gorm:"column:id"`
			Main   int64  `gorm:"column:main"`
			Gender string `gorm:"column:gender"`
		}
		// The join is on aid::text so a non-numeric anchor drops out instead of
		// aborting the run (three of the 12,726 member anchors do not resolve
		// to a staff alias at all — wave 152 §2.1).
		if err := e.db.WithContext(ctx).Raw(
			`SELECT sa.aid, sa.id, s.main, s.gender
			 FROM src_vndb.staff_alias sa JOIN src_vndb.staff s ON s.id = sa.id
			 WHERE sa.aid::text IN ?`, chunk).Scan(&rows).Error; err != nil {
			return fmt.Errorf("preload vndb staff: %w", err)
		}
		for _, r := range rows {
			e.staffOfAid[r.Aid] = r.ID
			e.staffMain[r.ID] = r.Main
			e.staffGender[r.ID] = r.Gender
		}
	}
	return nil
}

// loadBangumiPersons reads the infobox facts (性别 / 生日) of the bangumi
// persons the members anchor to. The jsonb scalar guard lives in parse.go: the
// column carries a non-array Fields on a couple of rows (wave 152 §2.5).
func (e *environment) loadBangumiPersons(ctx context.Context) error {
	ids := make([]string, 0, len(e.anchors))
	for _, list := range e.anchors {
		for _, a := range list {
			if a.SourceID == e.srcBangumi {
				ids = append(ids, a.ExternalID)
			}
		}
	}
	sort.Strings(ids)
	for _, chunk := range chunks(ids) {
		var rows []struct {
			ID      int64          `gorm:"column:id"`
			Infobox datatypes.JSON `gorm:"column:infobox_parsed"`
		}
		if err := e.db.WithContext(ctx).Raw(
			`SELECT id, infobox_parsed FROM src_bangumi.person WHERE id::text IN ?`, chunk).Scan(&rows).Error; err != nil {
			return fmt.Errorf("preload bangumi persons: %w", err)
		}
		for _, r := range rows {
			e.bgmFacts[r.ID] = parseBangumiFacts(r.Infobox)
		}
	}
	return nil
}

// loadExistingPersons loads the host candidates: every person a member already
// points at, plus every person that already owns an et=0 anchor (a previous
// run of this very wave). Both are needed BEFORE the decision, since two
// different owners on one cluster is the merge case this wave defers.
func (e *environment) loadExistingPersons(ctx context.Context) error {
	var rows []struct {
		EntityID   int64  `gorm:"column:entity_id"`
		SourceID   int16  `gorm:"column:source_id"`
		ExternalID string `gorm:"column:external_id"`
	}
	if err := e.db.WithContext(ctx).Raw(
		`SELECT entity_id, source_id, external_id FROM catalog_external_ref
		 WHERE entity_type = ? AND link_kind = ?`,
		model.EntityTypePerson, model.LinkKindExact).Scan(&rows).Error; err != nil {
		return fmt.Errorf("preload person anchors: %w", err)
	}
	ids := map[int64]bool{}
	for _, r := range rows {
		e.et0Owner[anchorKey{SourceID: r.SourceID, ExternalID: r.ExternalID}] = r.EntityID
		ids[r.EntityID] = true
	}
	for _, pid := range e.personOfMember {
		ids[pid] = true
	}
	list := make([]int64, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
	for _, chunk := range chunks(list) {
		var persons []personState
		if err := e.db.WithContext(ctx).Raw(
			`SELECT id, display_name, primary_credit_name_id, gender, birth_y, birth_m, birth_d, field_provenance
			 FROM catalog_person WHERE deleted_at IS NULL AND id IN ?`, chunk).Scan(&persons).Error; err != nil {
			return fmt.Errorf("preload persons: %w", err)
		}
		for i := range persons {
			p := persons[i]
			e.persons[p.ID] = &p
		}
	}
	return nil
}

// chunks slices an id list into preload-sized batches.
func chunks[T any](ids []T) [][]T {
	var out [][]T
	for start := 0; start < len(ids); start += preloadChunk {
		end := min(start+preloadChunk, len(ids))
		out = append(out, ids[start:end])
	}
	return out
}
