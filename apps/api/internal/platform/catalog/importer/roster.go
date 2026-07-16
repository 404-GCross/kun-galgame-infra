package importer

import (
	"fmt"
	"strconv"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// The character-roster wave lands the work↔character花名册 edges the credit
// wave (step 13) deliberately left out: step 13 ruled "a character with no VA
// only gets an entity, not an edge" (no independent work↔character edge
// existed). The need is here now (kungal + letmoe + the /v1 API all show a
// roster), so this wave fills catalog_work_character.
//
// Identity discipline (step 13, unchanged): a missing character is created as
// an entity + a self-referential exact anchor (rule:<source>-character-import,
// the SAME rule step 13 used) with zero person rows and zero name→person
// links — the anchor layer is the only gate. The roster GATE is different from
// step 13's bid-audit门: an EXACT work anchor means identity is already
// settled, and the roster is a per-work fact, so the anchor alone gates it (no
// second audit门). This widens the set well past step 13's pass layer.
//
// The edge is INDEPENDENT of credit: credits (incl. VA credits carrying a
// character_id) are untouched, never deduped against — a voiced character
// legitimately appears in both catalog_credit and catalog_work_character with
// different semantics.

const (
	ruleRosterBangumi = "import:character-roster-bangumi"
	ruleRosterEG      = "import:character-roster-eg"
)

// RosterStats is the per-wave tally for the roster import.
type RosterStats struct {
	CharactersCreated   int // new character entities (missing exact anchor)
	EdgesWritten        int // roster edges actually inserted
	Already             int // edges the unique (work,character) already held
	SkippedNoWorkAnchor int // roster row whose work has no exact anchor (out of gate)
	SkippedNoName       int // staging character carries no usable name — cannot build the entity
	Errors              int // materialize drops (character unresolved) — expected 0
}

func (s *RosterStats) add(o RosterStats) {
	s.CharactersCreated += o.CharactersCreated
	s.EdgesWritten += o.EdgesWritten
	s.Already += o.Already
	s.SkippedNoWorkAnchor += o.SkippedNoWorkAnchor
	s.SkippedNoName += o.SkippedNoName
	s.Errors += o.Errors
}

// RunRoster dispatches the requested roster wave(s).
func (im *Importer) RunRoster(source string) (RosterStats, error) {
	var total RosterStats
	if source == "bangumi" || source == "all" {
		s, err := im.runRosterBangumi()
		if err != nil {
			return total, fmt.Errorf("bangumi roster wave: %w", err)
		}
		total.add(s)
	}
	if source == "eg" || source == "all" {
		if im.eg == nil {
			return total, fmt.Errorf("eg roster wave requested but no erogamespace connection")
		}
		s, err := im.runRosterEG()
		if err != nil {
			return total, fmt.Errorf("eg roster wave: %w", err)
		}
		total.add(s)
	}
	return total, nil
}

// rosterPlan is a would-be roster edge resolved by SOURCE external ids, so it
// can be counted in dry-run (before entities exist) and materialized on apply.
type rosterPlan struct {
	workID    int64
	charExtID string
	kind      int16
}

// loadExactWorkMap returns numeric external_id → work id for one source's EXACT
// work anchors (the roster gate). An exact anchor means identity is settled, so
// the roster (a per-work fact) needs no further audit门.
func (im *Importer) loadExactWorkMap(source int16) (map[int64]int64, error) {
	var rows []struct {
		ExternalID int64 `gorm:"column:external_id"`
		WorkID     int64 `gorm:"column:work_id"`
	}
	if err := im.catalog.Raw(`
		SELECT external_id::bigint AS external_id, entity_id AS work_id
		FROM catalog_external_ref
		WHERE entity_type = ? AND source_id = ? AND link_kind = ?`,
		model.EntityTypeWork, source, model.LinkKindExact).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[int64]int64, len(rows))
	for _, r := range rows {
		m[r.ExternalID] = r.WorkID
	}
	return m, nil
}

func (im *Importer) runRosterBangumi() (RosterStats, error) {
	var st RosterStats
	workMap, err := im.loadExactWorkMap(bangumiSource)
	if err != nil {
		return st, err
	}
	if im.limit > 0 {
		workMap = capMap(workMap, im.limit)
	}
	charAnchor, err := im.loadAnchors(model.EntityTypeCharacter)
	if err != nil {
		return st, err
	}

	// Pre-gate at the query by JOINing to the EXACT work anchors: subject_character
	// spans all media (anime/manga/music), so this keeps only the reconciled
	// galgame rows (no cross-media noise, no 20k-id IN clause). The character
	// name is LEFT-JOINed so a missing name is counted, not silently dropped.
	type scRow struct {
		CharacterID int64   `gorm:"column:character_id"`
		SubjectID   int64   `gorm:"column:subject_id"`
		Type        int     `gorm:"column:type"`
		Name        *string `gorm:"column:name"`
	}
	var scs []scRow
	if err := im.catalog.Raw(`
		SELECT sc.character_id, sc.subject_id, sc.type, c.name
		FROM src_bangumi.subject_character sc
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.source_id = ? AND r.link_kind = ? AND r.external_id = sc.subject_id::text
		LEFT JOIN src_bangumi.character c ON c.id = sc.character_id`,
		model.EntityTypeWork, bangumiSource, model.LinkKindExact,
	).Scan(&scs).Error; err != nil {
		return st, err
	}

	// Pass A: new characters (missing exact anchor). Pass B: edge plans.
	var newChars []charItem
	seenChar := map[int64]bool{}
	var plans []rosterPlan
	for _, sc := range scs {
		// workMap is uncapped-equal to the JOIN gate unless --limit trimmed it.
		workID, ok := workMap[sc.SubjectID]
		if !ok {
			st.SkippedNoWorkAnchor++
			continue
		}
		if sc.Name == nil || strings.TrimSpace(*sc.Name) == "" {
			st.SkippedNoName++
			continue
		}
		name := *sc.Name
		ext := strconv.FormatInt(sc.CharacterID, 10)
		if !seenChar[sc.CharacterID] {
			seenChar[sc.CharacterID] = true
			if charAnchor[anchorKey(bangumiSource, ext)] == 0 {
				newChars = append(newChars, charItem{extID: ext, name: name, lang: "ja"})
			}
		}
		plans = append(plans, rosterPlan{workID: workID, charExtID: ext, kind: bangumiRosterKind(sc.Type)})
	}

	st.CharactersCreated = len(newChars)
	if im.dryRun {
		st.EdgesWritten = len(plans) // would-be (clean-state == apply)
		return st, nil
	}

	err = im.catalog.Transaction(func(tx *gorm.DB) error {
		charIDs, err := im.createCharacters(tx, bangumiSource, ruleBangumiChar, newChars)
		if err != nil {
			return err
		}
		charResolve := resolver(charAnchor, bangumiSource, charIDs)
		edges, dropped := materializeRoster(plans, charResolve, ruleRosterBangumi)
		st.Errors += dropped
		written, err := insertRosterEdges(tx, edges)
		if err != nil {
			return err
		}
		st.EdgesWritten = written
		st.Already = len(edges) - written
		return nil
	})
	return st, err
}

func (im *Importer) runRosterEG() (RosterStats, error) {
	var st RosterStats
	workMap, err := im.loadExactWorkMap(egSource)
	if err != nil {
		return st, err
	}
	if im.limit > 0 {
		workMap = capMap(workMap, im.limit)
	}
	charAnchor, err := im.loadAnchors(model.EntityTypeCharacter)
	if err != nil {
		return st, err
	}

	// Load appearances whole and gate in Go (avoids a 20k-id IN clause; EG has
	// no main/supporting distinction, so every roster edge is kind=unknown).
	var apps []struct {
		Game int64 `gorm:"column:game"`
		Char int64 `gorm:"column:character_id"`
	}
	if err := im.eg.Raw(
		`SELECT game, character_id FROM appearances WHERE game IS NOT NULL AND character_id IS NOT NULL`,
	).Scan(&apps).Error; err != nil {
		return st, err
	}
	charName, err := im.egNameMap(`SELECT id, raw->>'name' AS name FROM characters WHERE btrim(coalesce(raw->>'name',''))<>''`)
	if err != nil {
		return st, err
	}

	var newChars []charItem
	seenChar := map[int64]bool{}
	var plans []rosterPlan
	for _, a := range apps {
		workID, ok := workMap[a.Game]
		if !ok {
			st.SkippedNoWorkAnchor++
			continue
		}
		name, ok := charName[a.Char]
		if !ok {
			st.SkippedNoName++
			continue
		}
		ext := strconv.FormatInt(a.Char, 10)
		if !seenChar[a.Char] {
			seenChar[a.Char] = true
			if charAnchor[anchorKey(egSource, ext)] == 0 {
				newChars = append(newChars, charItem{extID: ext, name: name, lang: "ja"})
			}
		}
		plans = append(plans, rosterPlan{workID: workID, charExtID: ext, kind: model.WorkCharacterKindUnknown})
	}

	st.CharactersCreated = len(newChars)
	if im.dryRun {
		st.EdgesWritten = len(plans)
		return st, nil
	}

	err = im.catalog.Transaction(func(tx *gorm.DB) error {
		charIDs, err := im.createCharacters(tx, egSource, ruleEGChar, newChars)
		if err != nil {
			return err
		}
		charResolve := resolver(charAnchor, egSource, charIDs)
		edges, dropped := materializeRoster(plans, charResolve, ruleRosterEG)
		st.Errors += dropped
		written, err := insertRosterEdges(tx, edges)
		if err != nil {
			return err
		}
		st.EdgesWritten = written
		st.Already = len(edges) - written
		return nil
	})
	return st, err
}

// bangumiRosterKind maps a Bangumi subject_character.type to the roster edge
// kind. 1=主角 / 2=配角 / 3=客串 are the documented buckets; the low-volume
// undocumented tails (4/5/6 = mob/narration/background in the galgame subset)
// map to unknown rather than being guessed.
func bangumiRosterKind(t int) int16 {
	switch t {
	case 1:
		return model.WorkCharacterKindMain
	case 2:
		return model.WorkCharacterKindSecondary
	case 3:
		return model.WorkCharacterKindAppears
	default:
		return model.WorkCharacterKindUnknown
	}
}

// materializeRoster resolves each plan's source character ext id to a catalog
// character id. A plan whose character does not resolve is dropped and counted
// (should be 0 — every character is created or already anchored).
func materializeRoster(plans []rosterPlan, char func(string) (int64, bool), matchedBy string) ([]model.CatalogWorkCharacter, int) {
	out := make([]model.CatalogWorkCharacter, 0, len(plans))
	dropped := 0
	for _, p := range plans {
		chID, ok := char(p.charExtID)
		if !ok {
			dropped++
			continue
		}
		out = append(out, model.CatalogWorkCharacter{
			WorkID: p.workID, CharacterID: chID, Kind: p.kind, MatchedBy: matchedBy,
		})
	}
	return out, dropped
}

// insertRosterEdges batch-inserts roster edges with ON CONFLICT (work_id,
// character_id) DO NOTHING — insert-if-absent, so the first source to assert a
// pair wins and re-runs write nothing. Returns the number actually written.
func insertRosterEdges(tx *gorm.DB, edges []model.CatalogWorkCharacter) (int, error) {
	written := 0
	const batch = 1000
	for start := 0; start < len(edges); start += batch {
		end := min(start+batch, len(edges))
		var sb strings.Builder
		sb.WriteString(`INSERT INTO catalog_work_character (work_id, character_id, kind, matched_by, created_at, updated_at) VALUES `)
		args := make([]any, 0, (end-start)*4)
		for i := start; i < end; i++ {
			e := edges[i]
			if i > start {
				sb.WriteString(",")
			}
			sb.WriteString("(?,?,?,?,now(),now())")
			args = append(args, e.WorkID, e.CharacterID, e.Kind, e.MatchedBy)
		}
		sb.WriteString(` ON CONFLICT (work_id, character_id) DO NOTHING`)
		res := tx.Exec(sb.String(), args...)
		if res.Error != nil {
			return written, res.Error
		}
		written += int(res.RowsAffected)
	}
	return written, nil
}
