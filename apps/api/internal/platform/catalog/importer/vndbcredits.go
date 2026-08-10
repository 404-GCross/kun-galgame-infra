package importer

import (
	"fmt"
	"log/slog"
	"sort"
	"strconv"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

const ruleVNDBStaff = "rule:vndb-staff-import"

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

type aliasName struct {
	name string
	lang string
}

func (im *Importer) runVNDBCredits() (Stats, error) {
	var st Stats

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
	retiredChars, err := im.loadVNDBRetiredExactRefs(model.EntityTypeCharacter)
	if err != nil {
		return st, err
	}

	roleCounts := map[string]int{}
	claimedNameSeen := map[string]bool{}
	claimedCharSeen := map[string]bool{}
	retiredCharSeen := map[string]bool{}
	rowsClaimedName, rowsClaimedChar, rowsRetiredChar := 0, 0, 0
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
		st.CreditsWritten = len(plans)
		return st, nil
	}

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
		noLabel := func(string) (int64, bool) { return 0, false }

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

var vndbAnchorEntities = map[int16]struct {
	table      string
	softDelete bool
}{
	model.EntityTypeCreditName: {"catalog_credit_name", false},
	model.EntityTypeCharacter:  {"catalog_character", true},
	model.EntityTypeWork:       {"catalog_work", true},
}

func (im *Importer) loadVNDBAnchors(entityType int16) (map[string]int64, error) {
	return im.loadVNDBRefs(entityType, model.LinkKindExact, false)
}

func (im *Importer) loadVNDBClaimedRefs(entityType int16) (map[string]int64, error) {
	return im.loadVNDBRefs(entityType, model.LinkKindProbable, false)
}

func (im *Importer) loadVNDBRetiredExactRefs(entityType int16) (map[string]int64, error) {
	return im.loadVNDBRefs(entityType, model.LinkKindExact, true)
}

func (im *Importer) loadVNDBRefs(entityType, linkKind int16, retired bool) (map[string]int64, error) {
	e, ok := vndbAnchorEntities[entityType]
	if !ok {
		return nil, fmt.Errorf("vndb ref load: unsupported entity type %d", entityType)
	}
	liveness := "e.deleted_at IS NULL"
	switch {
	case !e.softDelete && retired:
		return map[string]int64{}, nil
	case !e.softDelete:
		liveness = "TRUE"
	case retired:
		liveness = "e.deleted_at IS NOT NULL"
	}

	var rows []struct {
		ExternalID string `gorm:"column:external_id"`
		EntityID   int64  `gorm:"column:entity_id"`
	}
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
