package importer

import (
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

	// 3. Build the credit plans (resolvable by source ext id), counting the two
	// skip classes. roleCounts is the per-role dry-run breakdown.
	roleCounts := map[string]int{}
	var plans []creditPlan
	for _, r := range staffRows {
		roleID, ok := roleMap[r.Role]
		if !ok {
			st.SkippedUnmappedRole++
			roleCounts[r.Role+" (unmapped)"]++
			continue
		}
		// The staff→其他 bucket refines by note (see staffnotes.go). Planning
		// the refined role here — not just backfilling moved rows — is what
		// keeps a re-import from re-inserting the 其他 edge a backfill moved:
		// the unique index includes role_id, so only an identical plan conflicts.
		roleID = RefineVNDBStaffRole(roleID, r.Note)
		roleCounts[r.Role]++
		plans = append(plans, creditPlan{
			workID: r.WorkID, cnExtID: strconv.Itoa(r.AID), roleID: roleID, note: r.Note,
		})
	}
	skippedVANoChar := 0
	for _, r := range seiyuuRows {
		if charAnchor[r.CID] == 0 { // character never imported by the roster wave → skip (this wave writes only credits)
			skippedVANoChar++
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

// loadVNDBAnchors returns external_id → entity_id for source-2 EXACT anchors of
// one entity type (ext-only keys — this wave is single-source). Empty for
// credit_name on the first run.
func (im *Importer) loadVNDBAnchors(entityType int16) (map[string]int64, error) {
	var rows []struct {
		ExternalID string `gorm:"column:external_id"`
		EntityID   int64  `gorm:"column:entity_id"`
	}
	if err := im.catalog.Raw(`
		SELECT external_id, entity_id FROM catalog_external_ref
		WHERE entity_type = ? AND source_id = ? AND link_kind = ?`,
		entityType, vndbSource, model.LinkKindExact).Scan(&rows).Error; err != nil {
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
