package importer

import (
	"fmt"
	"strconv"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// Bangumi person.type routing (doc 17 §6.1): 1=individual → credit_name,
// 2=company / 3=group → label (companies are overwhelmingly "producer" in the
// galgame subset → publisher; groups → group kind). Because a credit edge
// always needs a credit_name (invariant 1, NOT NULL), a company/group ALSO
// gets an orphan credit_name (the credited name) with the label linked via
// credit.label_id — the model's "LabelID coexists with CreditNameID".
const (
	bangumiTypeIndividual = 1
	bangumiTypeCompany    = 2
	bangumiTypeGroup      = 3
)

// creditPlan is a would-be credit resolved by SOURCE external ids, so it can be
// counted in dry-run (before entities exist) and materialized on apply.
type creditPlan struct {
	workID    int64
	cnExtID   string // person → credit_name
	labelExt  string // "" unless a company/group signer
	roleID    int64
	charExtID string // "" unless a VA credit
	note      string
}

func (im *Importer) runBangumi() (Stats, error) {
	var st Stats
	workMap, err := im.loadPassWorkMap()
	if err != nil {
		return st, err
	}
	if im.limit > 0 {
		workMap = capMap(workMap, im.limit)
	}
	bids := int64Keys(workMap)

	roleMap, err := im.roleMap(bangumiSource)
	if err != nil {
		return st, err
	}
	cnAnchor, err := im.loadAnchors(model.EntityTypeCreditName)
	if err != nil {
		return st, err
	}
	labelAnchor, err := im.loadAnchors(model.EntityTypeLabel)
	if err != nil {
		return st, err
	}
	charAnchor, err := im.loadAnchors(model.EntityTypeCharacter)
	if err != nil {
		return st, err
	}

	// --- load the gated links + entity attributes ---
	type spRow struct {
		PersonID  int64 `gorm:"column:person_id"`
		SubjectID int64 `gorm:"column:subject_id"`
		Position  int   `gorm:"column:position"`
	}
	var sps []spRow
	if err := im.catalog.Raw(`SELECT person_id, subject_id, position FROM src_bangumi.subject_person WHERE subject_id IN ?`, bids).Scan(&sps).Error; err != nil {
		return st, err
	}
	type personRow struct {
		ID   int64  `gorm:"column:id"`
		Type int    `gorm:"column:type"`
		Name string `gorm:"column:name"`
	}
	var persons []personRow
	if err := im.catalog.Raw(`SELECT id, type, name FROM src_bangumi.person WHERE id IN (
		SELECT person_id FROM src_bangumi.subject_person WHERE subject_id IN ?
		UNION SELECT person_id FROM src_bangumi.person_character WHERE subject_id IN ?)`, bids, bids).Scan(&persons).Error; err != nil {
		return st, err
	}
	personByID := map[int64]personRow{}
	for _, p := range persons {
		personByID[p.ID] = p
	}
	type scRow struct {
		CharacterID int64 `gorm:"column:character_id"`
		SubjectID   int64 `gorm:"column:subject_id"`
		Type        int   `gorm:"column:type"`
	}
	var scs []scRow
	if err := im.catalog.Raw(`SELECT character_id, subject_id, type FROM src_bangumi.subject_character WHERE subject_id IN ?`, bids).Scan(&scs).Error; err != nil {
		return st, err
	}
	charRoleNote := map[string]string{} // subject|character → 主角/配角/客串
	for _, sc := range scs {
		charRoleNote[fmt.Sprintf("%d|%d", sc.SubjectID, sc.CharacterID)] = charTypeNote(sc.Type)
	}
	type charRow struct {
		ID   int64  `gorm:"column:id"`
		Name string `gorm:"column:name"`
	}
	var chars []charRow
	if err := im.catalog.Raw(`SELECT id, name FROM src_bangumi.character WHERE id IN (
		SELECT character_id FROM src_bangumi.subject_character WHERE subject_id IN ?
		UNION SELECT character_id FROM src_bangumi.person_character WHERE subject_id IN ?)`, bids, bids).Scan(&chars).Error; err != nil {
		return st, err
	}
	type pcRow struct {
		PersonID    int64 `gorm:"column:person_id"`
		SubjectID   int64 `gorm:"column:subject_id"`
		CharacterID int64 `gorm:"column:character_id"`
	}
	var pcs []pcRow
	if err := im.catalog.Raw(`SELECT person_id, subject_id, character_id FROM src_bangumi.person_character WHERE subject_id IN ?`, bids).Scan(&pcs).Error; err != nil {
		return st, err
	}

	// --- Pass A: collect new entities (deduped against self anchors) ---
	var newNames []nameItem
	var newLabels []labelItem
	seenName, seenLabel := map[int64]bool{}, map[int64]bool{}
	for _, p := range persons {
		ext := strconv.FormatInt(p.ID, 10)
		if !seenName[p.ID] && cnAnchor[anchorKey(bangumiSource, ext)] == 0 {
			seenName[p.ID] = true
			newNames = append(newNames, nameItem{extID: ext, name: p.Name, lang: "ja"})
		}
		if p.Type == bangumiTypeCompany || p.Type == bangumiTypeGroup {
			if !seenLabel[p.ID] && labelAnchor[anchorKey(bangumiSource, ext)] == 0 {
				seenLabel[p.ID] = true
				newLabels = append(newLabels, labelItem{extID: ext, name: p.Name, lang: "ja", kind: labelKind(p.Type)})
			}
		}
	}
	var newChars []charItem
	seenChar := map[int64]bool{}
	for _, c := range chars {
		ext := strconv.FormatInt(c.ID, 10)
		if !seenChar[c.ID] && charAnchor[anchorKey(bangumiSource, ext)] == 0 {
			seenChar[c.ID] = true
			newChars = append(newChars, charItem{extID: ext, name: c.Name, lang: "ja"})
		}
	}

	// --- Pass B: build the credit plans (resolvable by ext id) ---
	var plans []creditPlan
	for _, sp := range sps {
		workID, ok := workMap[sp.SubjectID]
		if !ok {
			st.SkippedGate++
			continue
		}
		roleID, ok := roleMap[fmt.Sprintf("4:%d", sp.Position)]
		if !ok {
			st.SkippedUnmappedRole++
			continue
		}
		p := personByID[sp.PersonID]
		ext := strconv.FormatInt(sp.PersonID, 10)
		plan := creditPlan{workID: workID, cnExtID: ext, roleID: roleID}
		if p.Type == bangumiTypeCompany || p.Type == bangumiTypeGroup {
			plan.labelExt = ext
		}
		plans = append(plans, plan)
	}
	for _, pc := range pcs {
		workID, ok := workMap[pc.SubjectID]
		if !ok {
			st.SkippedGate++
			continue
		}
		plans = append(plans, creditPlan{
			workID: workID, cnExtID: strconv.FormatInt(pc.PersonID, 10), roleID: roleVoiceActor,
			charExtID: strconv.FormatInt(pc.CharacterID, 10),
			note:      charRoleNote[fmt.Sprintf("%d|%d", pc.SubjectID, pc.CharacterID)],
		})
	}

	st.NamesCreated = len(newNames)
	st.LabelsCreated = len(newLabels)
	st.CharactersCreated = len(newChars)
	if im.dryRun {
		st.CreditsWritten = len(plans) // would-be (clean-state == apply)
		return st, nil
	}

	// --- apply: create entities, resolve, insert credits, in one tx ---
	err = im.catalog.Transaction(func(tx *gorm.DB) error {
		nameIDs, err := im.createCreditNames(tx, bangumiSource, ruleBangumiPerson, newNames)
		if err != nil {
			return err
		}
		labelIDs, err := im.createLabels(tx, bangumiSource, ruleBangumiPerson, newLabels)
		if err != nil {
			return err
		}
		charIDs, err := im.createCharacters(tx, bangumiSource, ruleBangumiChar, newChars)
		if err != nil {
			return err
		}
		cnResolve := resolver(cnAnchor, bangumiSource, nameIDs)
		labelResolve := resolver(labelAnchor, bangumiSource, labelIDs)
		charResolve := resolver(charAnchor, bangumiSource, charIDs)

		credits, dropped := materialize(plans, cnResolve, labelResolve, charResolve, bangumiSource)
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

// charTypeNote maps a Bangumi subject_character.type to a human note (the
// appearance strength is a NOTE, never a spoiler level — different semantics).
func charTypeNote(t int) string {
	switch t {
	case 1:
		return "主角"
	case 2:
		return "配角"
	case 3:
		return "客串"
	default:
		return ""
	}
}

func labelKind(personType int) int16 {
	if personType == bangumiTypeGroup {
		return model.LabelKindGroup
	}
	return model.LabelKindPublisher
}
