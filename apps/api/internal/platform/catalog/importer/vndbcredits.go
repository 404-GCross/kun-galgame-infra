package importer

import (
	"fmt"
	"log/slog"
	"sort"
	"strconv"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// The VNDB credits wave (step 73) lands catalog_credit edges from VNDB's staff
// tables for works holding an EXACT VNDB work anchor (source 2):
//
//   - vn_staff → non-VA credits. The credited identity is the ALIAS (aid), whose
//     name is staff_alias.name — VNDB's ONE id space without a letter prefix, and
//     the credited-name granularity (one staff member credited under two pen
//     names is two credit names). vn_staff.role → catalog_role via the seeded
//     source-2 role map; a role with no map row skips the credit (all ten VNDB
//     staff roles map today — translator/editor/qa via the reserved-band roles
//     3/4/5, refs/proj/80).
//   - vn_seiyuu → VA credits, resolved to the character voiced (cid → the
//     source-2 character anchor the roster wave, step 47, already minted). A VA
//     whose character was never imported (no name / out of the roster gate) is
//     skipped, not created — this wave writes ONLY credits.
//
// Discipline reused verbatim from the Bangumi/EG credit wave (doc 73: reuse, do
// not invent): every credited alias becomes ONE orphan (person_id NULL) credit
// name with a self-referential exact anchor (rule:vndb-staff-import) + an
// imported revision; zero persons; source_id=2 makes the whole wave rollbackable;
// insertCredits' ON CONFLICT DO NOTHING on the doc-10 expression unique index
// (work, credit_name, role, COALESCE(character,0)) makes re-runs write nothing.
// eid (edition) is ignored — ReleaseID stays NULL and same-alias/same-role rows
// that differ only by edition collapse onto the one work-level credit.
//
// ⚠️ Identity discipline (the same ruling the VNDB roster wave carries): an
// external id a LIVING entity already answers to is not a vacancy at ANY grade.
// Exactness governs how strongly a link is asserted, never whether the upstream
// thing already has a body here. So a claimed id is skipped outright — not
// minted, not attached, and not re-graded (re-grading is an adjudication, not
// an import) — and every skip class gets its own counter so an unattended run
// reports what it declined to do instead of hiding it in a generic bucket.

const ruleVNDBStaff = "rule:vndb-staff-import" // credit_name self-anchor (external_id = alias aid)

// vnStaffRow / vnSeiyuuRow are the in-gate staging rows (already joined to the
// exact source-2 work anchor, so the work id is resolved at the query).
type vnStaffRow struct {
	WorkID int64  `gorm:"column:work_id"`
	AID    int    `gorm:"column:aid"`
	Role   string `gorm:"column:role"`
	Note   string `gorm:"column:note"`
}

type vnSeiyuuRow struct {
	WorkID int64  `gorm:"column:work_id"`
	AID    int    `gorm:"column:aid"`
	CID    string `gorm:"column:cid"`
	Note   string `gorm:"column:note"`
}

// aliasName is a staff alias's credited name + its language (from the owning
// staff row; ” folds to "ja").
type aliasName struct {
	name string
	lang string
}

func (im *Importer) runVNDBCredits() (Stats, error) {
	var st Stats

	// 1. Load the in-gate staff/seiyuu rows by JOINing the exact source-2 work
	// anchors. VNDB external_ids carry the "v" prefix ("v38"), so the join is on
	// text equality (loadExactWorkMap's ::bigint cast would fail here).
	var staffRows []vnStaffRow
	if err := im.catalog.Raw(`
		SELECT r.entity_id AS work_id, vs.aid AS aid, vs.role AS role, vs.note AS note
		FROM src_vndb.vn_staff vs
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.source_id = ? AND r.link_kind = ? AND r.external_id = vs.id`,
		model.EntityTypeWork, vndbSource, model.LinkKindExact).Scan(&staffRows).Error; err != nil {
		return st, err
	}
	var seiyuuRows []vnSeiyuuRow
	if err := im.catalog.Raw(`
		SELECT r.entity_id AS work_id, vse.aid AS aid, vse.cid AS cid, vse.note AS note
		FROM src_vndb.vn_seiyuu vse
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.source_id = ? AND r.link_kind = ? AND r.external_id = vse.id`,
		model.EntityTypeWork, vndbSource, model.LinkKindExact).Scan(&seiyuuRows).Error; err != nil {
		return st, err
	}
	if im.limit > 0 {
		keep := firstNWorks(staffRows, seiyuuRows, im.limit)
		staffRows = filterStaff(staffRows, keep)
		seiyuuRows = filterSeiyuu(seiyuuRows, keep)
	}

	// 2. Resume/resolve indexes: the seeded role map, the source-2 credit-name
	// anchors (aid already imported), the source-2 character anchors (the roster
	// wave's cid → catalog character), and every alias's credited name.
	roleMap, err := im.roleMap(vndbSource)
	if err != nil {
		return st, err
	}
	cnAnchor, err := im.loadVNDBAnchors(model.EntityTypeCreditName)
	if err != nil {
		return st, err
	}
	charAnchor, err := im.loadVNDBAnchors(model.EntityTypeCharacter)
	if err != nil {
		return st, err
	}
	aliasNames, err := im.loadVNDBAliasNames()
	if err != nil {
		return st, err
	}
	// 2b. Ids an ALIVE entity already answers to at a NON-exact grade, and ids
	// whose only exact holder is retired. An external id with a body is not a
	// vacancy at any grade: for a credit_name alias minting a second one would
	// re-split a merge the platform deliberately made (and re-claim the id with a
	// fresh exact anchor); for a character resolving onto nothing would drop the
	// VA credit. Neither is re-graded here — promoting a probable ref is an
	// adjudication for the confirmation queue, not something an import may do
	// silently. An id that ALSO has an alive exact anchor is not claimed at all:
	// exactness decides which link resolves, never whether an id is free.
	claimedNames, err := im.loadVNDBClaimedRefs(model.EntityTypeCreditName)
	if err != nil {
		return st, err
	}
	claimedChars, err := im.loadVNDBClaimedRefs(model.EntityTypeCharacter)
	if err != nil {
		return st, err
	}
	for ext := range claimedNames {
		if cnAnchor[ext] != 0 {
			delete(claimedNames, ext)
		}
	}
	for ext := range claimedChars {
		if charAnchor[ext] != 0 {
			delete(claimedChars, ext)
		}
	}
	// Disjoint from charAnchor by the exact unique index — one exact holder per
	// id, alive or not. Empty for credit_name (no soft delete), so not loaded.
	retiredChars, err := im.loadVNDBRetiredExactRefs(model.EntityTypeCharacter)
	if err != nil {
		return st, err
	}

	// 3. Build the credit plans (resolvable by source ext id), counting the skip
	// classes. roleCounts is the per-role dry-run breakdown. claimedNameSeen
	// keeps the alias skip counter per-entity (like the roster wave) rather than
	// per-credit-row; the row totals go to the plan log below.
	roleCounts := map[string]int{}
	claimedNameSeen := map[string]bool{}
	claimedCharSeen := map[string]bool{}
	retiredCharSeen := map[string]bool{}
	rowsClaimedName, rowsClaimedChar, rowsRetiredChar := 0, 0, 0
	// skipClaimedName reports whether this alias id already has a body at
	// probable grade; the whole credit row is dropped when it does.
	skipClaimedName := func(aid string) bool {
		if claimedNames[aid] == 0 {
			return false
		}
		rowsClaimedName++
		if !claimedNameSeen[aid] {
			claimedNameSeen[aid] = true
			st.SkippedClaimedProbableName++
		}
		return true
	}
	var plans []creditPlan
	for _, r := range staffRows {
		if skipClaimedName(strconv.Itoa(r.AID)) {
			continue
		}
		roleID, ok := roleMap[r.Role]
		if !ok {
			st.SkippedUnmappedRole++
			roleCounts[r.Role+" (unmapped)"]++
			continue
		}
		// The staff→其他 bucket refines by note (see staffnotes.go) — a
		// composite note plans one credit per position. Planning the refined
		// roles here — not just backfilling moved rows — is what keeps a
		// re-import from re-inserting the 其他 edge a backfill moved: the
		// unique index includes role_id, so only an identical plan conflicts.
		for _, refined := range RefineVNDBStaffRoles(roleID, r.Note) {
			roleCounts[r.Role]++
			plans = append(plans, creditPlan{
				workID: r.WorkID, cnExtID: strconv.Itoa(r.AID), roleID: refined, note: r.Note,
			})
		}
	}
	skippedVANoChar := 0
	for _, r := range seiyuuRows {
		if skipClaimedName(strconv.Itoa(r.AID)) {
			continue
		}
		if charAnchor[r.CID] == 0 {
			// The character has no ALIVE exact anchor. Three distinct reasons, kept
			// in three counters because they need different follow-ups: a probable
			// claim is a merge survivor waiting on a human confirmation, a retired
			// squat needs the dead ref out of the identity index, and a plain miss
			// means the roster wave never imported the character. None of them mint
			// or attach anything — this wave writes ONLY credits.
			switch {
			case claimedChars[r.CID] != 0:
				rowsClaimedChar++
				if !claimedCharSeen[r.CID] {
					claimedCharSeen[r.CID] = true
					st.SkippedClaimedProbableChar++
				}
			case retiredChars[r.CID] != 0:
				rowsRetiredChar++
				if !retiredCharSeen[r.CID] {
					retiredCharSeen[r.CID] = true
					st.SkippedRetiredExactChar++
				}
			default:
				skippedVANoChar++
			}
			continue
		}
		roleCounts["seiyuu"]++
		plans = append(plans, creditPlan{
			workID: r.WorkID, cnExtID: strconv.Itoa(r.AID), roleID: roleVoiceActor,
			charExtID: r.CID, note: r.Note,
		})
	}

	// 4. Credit names to create: every plan alias that has no source-2 anchor yet.
	var newNames []nameItem
	seen := map[string]bool{}
	for _, p := range plans {
		if seen[p.cnExtID] || cnAnchor[p.cnExtID] != 0 {
			continue
		}
		seen[p.cnExtID] = true
		an := aliasNames[p.cnExtID]
		newNames = append(newNames, nameItem{extID: p.cnExtID, name: an.name, lang: an.lang})
	}
	st.NamesCreated = len(newNames)

	slog.Info("vndb credits plan",
		"in_gate_staff_rows", len(staffRows), "in_gate_seiyuu_rows", len(seiyuuRows),
		"planned_credits", len(plans), "names_to_create", len(newNames),
		"skipped_unmapped_role", st.SkippedUnmappedRole, "skipped_va_no_char", skippedVANoChar,
		"skipped_claimed_probable_name", st.SkippedClaimedProbableName,
		"skipped_claimed_probable_char", st.SkippedClaimedProbableChar,
		"skipped_retired_exact_char", st.SkippedRetiredExactChar,
		"skipped_rows_claimed_name", rowsClaimedName,
		"skipped_rows_claimed_char", rowsClaimedChar,
		"skipped_rows_retired_char", rowsRetiredChar,
		"per_role", roleCounts)

	if im.dryRun {
		st.CreditsWritten = len(plans) // would-be (clean-state == apply, minus edition-collapse)
		return st, nil
	}

	// 5. Apply: create the orphan names, resolve every ext id, insert credits.
	err = im.catalog.Transaction(func(tx *gorm.DB) error {
		nameIDs, err := im.createCreditNames(tx, vndbSource, ruleVNDBStaff, newNames)
		if err != nil {
			return err
		}
		cnResolve := func(ext string) (int64, bool) {
			if id, ok := nameIDs[ext]; ok {
				return id, true
			}
			if id, ok := cnAnchor[ext]; ok && id != 0 {
				return id, true
			}
			return 0, false
		}
		charResolve := func(ext string) (int64, bool) {
			if id, ok := charAnchor[ext]; ok && id != 0 {
				return id, true
			}
			return 0, false
		}
		noLabel := func(string) (int64, bool) { return 0, false } // VNDB vn_staff has no company signer

		credits, dropped := materialize(plans, cnResolve, noLabel, charResolve, vndbSource)
		st.Errors += dropped
		written, err := im.insertCredits(tx, credits)
		if err != nil {
			return err
		}
		st.CreditsWritten = written
		st.Already = len(credits) - written
		return nil
	})
	return st, err
}

// vndbAnchorEntities maps the entity types the VNDB ref loaders serve to their
// entity table and whether that table soft-deletes. catalog_credit_name has NO
// soft delete by design (a merge hard-deletes the losing row and its refs move
// or drop, see service.retireSource), so a credit_name ref can never have a
// retired holder — only a live one or none at all.
var vndbAnchorEntities = map[int16]struct {
	table      string
	softDelete bool
}{
	model.EntityTypeCreditName: {"catalog_credit_name", false},
	model.EntityTypeCharacter:  {"catalog_character", true},
	model.EntityTypeWork:       {"catalog_work", true},
}

// loadVNDBAnchors returns external_id → entity_id for source-2 EXACT anchors of
// one entity type held by an ALIVE entity (ext-only keys — this wave is
// single-source). Empty for credit_name on the first run.
//
// The liveness restriction is the point: a merged-away holder has left the
// identity indexes, so an anchor pointing at it must not resolve — for
// credit_name a hard-deleted holder would make insertCredits fail its FK, and
// for character/work it would silently hang new rows off an entity the merge
// removed. The callers pair this with the claimed/retired loaders below so an
// id that drops out here is skipped and counted rather than treated as vacant.
func (im *Importer) loadVNDBAnchors(entityType int16) (map[string]int64, error) {
	return im.loadVNDBRefs(entityType, model.LinkKindExact, false)
}

// loadVNDBClaimedRefs returns external_id → entity_id for source-2 PROBABLE
// refs held by an ALIVE entity. Such an id is NOT vacant: the merge engine
// demotes every competing same-source exact on a survivor to probable
// (service.mergeExternalRefs, doc 10 §6.2-4), so a probable ref is the trace of
// a body the catalog already has for that id. `related` (link_kind 2) is
// excluded on purpose — doc 10 forbids a non-identity link from participating
// in identity resolution.
func (im *Importer) loadVNDBClaimedRefs(entityType int16) (map[string]int64, error) {
	return im.loadVNDBRefs(entityType, model.LinkKindProbable, false)
}

// loadVNDBRetiredExactRefs returns external_id → entity_id for source-2 EXACT
// refs whose holder is soft-deleted. A retired entity SHOULD free its id, but
// uq_catalog_external_ref_exact is (source_id, external_id, entity_type) WHERE
// link_kind = 0 and is not deleted_at-aware, so the retired row still squats
// the exact slot. Always empty for an entity type that does not soft-delete.
func (im *Importer) loadVNDBRetiredExactRefs(entityType int16) (map[string]int64, error) {
	return im.loadVNDBRefs(entityType, model.LinkKindExact, true)
}

// loadVNDBRefs is the shared loader: source-2 refs of one entity type at one
// link grade, restricted to holders of the requested liveness. The same
// external id can legitimately sit on several entities (the ref PK carries
// entity_id), so the lowest matching holder is picked for determinism.
func (im *Importer) loadVNDBRefs(entityType, linkKind int16, retired bool) (map[string]int64, error) {
	e, ok := vndbAnchorEntities[entityType]
	if !ok {
		return nil, fmt.Errorf("vndb ref load: unsupported entity type %d", entityType)
	}
	liveness := "e.deleted_at IS NULL"
	switch {
	case !e.softDelete && retired:
		return map[string]int64{}, nil // no deleted_at column → no retired holder can exist
	case !e.softDelete:
		liveness = "TRUE" // the row existing IS its liveness
	case retired:
		liveness = "e.deleted_at IS NOT NULL"
	}

	var rows []struct {
		ExternalID string `gorm:"column:external_id"`
		EntityID   int64  `gorm:"column:entity_id"`
	}
	// The table name comes from the package-level map above, never from input.
	q := fmt.Sprintf(`
		SELECT r.external_id, min(r.entity_id) AS entity_id
		FROM catalog_external_ref r
		JOIN %s e ON e.id = r.entity_id AND %s
		WHERE r.entity_type = ? AND r.source_id = ? AND r.link_kind = ?
		GROUP BY r.external_id`, e.table, liveness)
	if err := im.catalog.Raw(q, entityType, vndbSource, linkKind).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[string]int64, len(rows))
	for _, r := range rows {
		m[r.ExternalID] = r.EntityID
	}
	return m, nil
}

// loadVNDBAliasNames returns alias aid (as text) → its credited name + language.
// The name is staff_alias.name (native form); the language is the owning staff's
// lang (” → "ja"). Loaded whole (~65k rows) to avoid a large IN clause.
func (im *Importer) loadVNDBAliasNames() (map[string]aliasName, error) {
	var rows []struct {
		AID  int    `gorm:"column:aid"`
		Name string `gorm:"column:name"`
		Lang string `gorm:"column:lang"`
	}
	if err := im.catalog.Raw(`
		SELECT sa.aid AS aid, sa.name AS name, coalesce(nullif(s.lang, ''), 'ja') AS lang
		FROM src_vndb.staff_alias sa
		JOIN src_vndb.staff s ON s.id = sa.id`).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[string]aliasName, len(rows))
	for _, r := range rows {
		m[strconv.Itoa(r.AID)] = aliasName{name: r.Name, lang: r.Lang}
	}
	return m, nil
}

// firstNWorks returns the set of the first n distinct work ids (ascending)
// across both row sets — the deterministic --limit debugging aid.
func firstNWorks(staff []vnStaffRow, seiyuu []vnSeiyuuRow, n int) map[int64]bool {
	works := map[int64]bool{}
	for _, r := range staff {
		works[r.WorkID] = true
	}
	for _, r := range seiyuu {
		works[r.WorkID] = true
	}
	keys := make([]int64, 0, len(works))
	for k := range works {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	if n < len(keys) {
		keys = keys[:n]
	}
	keep := make(map[int64]bool, len(keys))
	for _, k := range keys {
		keep[k] = true
	}
	return keep
}

func filterStaff(rows []vnStaffRow, keep map[int64]bool) []vnStaffRow {
	out := rows[:0:0]
	for _, r := range rows {
		if keep[r.WorkID] {
			out = append(out, r)
		}
	}
	return out
}

func filterSeiyuu(rows []vnSeiyuuRow, keep map[int64]bool) []vnSeiyuuRow {
	out := rows[:0:0]
	for _, r := range rows {
		if keep[r.WorkID] {
			out = append(out, r)
		}
	}
	return out
}
