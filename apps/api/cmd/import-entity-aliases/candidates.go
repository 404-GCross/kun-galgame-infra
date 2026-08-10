package main

import (
	"fmt"
	"io"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type candStats struct {
	Generated     int
	Bidirectional int
	Ambiguous     int
	AlreadyCand   int
	AlreadySame   int
}

type cnInfo struct {
	fold     string
	source   int16
	personID *int64
}

type aliasDecl struct {
	owner  int64
	source int16
	fold   string
}

func runCandidates(db *gorm.DB, w io.Writer, apply bool) (candStats, error) {
	var st candStats

	info, nameIndex, err := loadCreditNames(db)
	if err != nil {
		return st, err
	}
	decls, ownerFolds, err := loadAliasDecls(db)
	if err != nil {
		return st, err
	}
	existing, err := loadCandidatePairs(db)
	if err != nil {
		return st, err
	}

	seen := map[[2]int64]struct{}{}
	var toWrite []model.CatalogMatchCandidate
	for _, d := range decls {
		var target int64
		hits := 0
		for _, c := range nameIndex[d.fold] {
			if c.source == d.source || c.id == d.owner {
				continue
			}
			target = c.id
			hits++
		}
		if hits == 0 {
			continue
		}
		if hits > 1 {
			st.Ambiguous++
			continue
		}
		a, b := d.owner, target
		if a > b {
			a, b = b, a
		}
		pair := [2]int64{a, b}
		if _, ok := seen[pair]; ok {
			continue
		}
		if _, ok := existing[pair]; ok {
			st.AlreadyCand++
			continue
		}
		xi, yi := info[d.owner], info[target]
		if xi.personID != nil && yi.personID != nil && *xi.personID == *yi.personID {
			st.AlreadySame++
			continue
		}
		seen[pair] = struct{}{}
		st.Generated++
		if ownerFolds[target][xi.fold] {
			st.Bidirectional++
		}
		toWrite = append(toWrite, model.CatalogMatchCandidate{
			EntityType: model.EntityTypeCreditName, AID: a, BID: b,
			Reason: model.CandidateReasonAliasDeclared, Status: model.CandidateStatusPending,
		})
	}

	if apply && len(toWrite) > 0 {
		const batch = 1000
		for start := 0; start < len(toWrite); start += batch {
			end := min(start+batch, len(toWrite))
			if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(toWrite[start:end]).Error; err != nil {
				return st, err
			}
		}
	}

	mode := "DRY-RUN"
	if apply {
		mode = "APPLIED"
	}
	fmt.Fprintf(w, "leg B %s — alias_declared candidates=%d (bidirectional=%d) | ambiguous=%d already_candidate=%d already_same_person=%d\n",
		mode, st.Generated, st.Bidirectional, st.Ambiguous, st.AlreadyCand, st.AlreadySame)
	return st, nil
}

func loadCreditNames(db *gorm.DB) (map[int64]cnInfo, map[string][]struct {
	id     int64
	source int16
}, error) {
	var rows []struct {
		ID       int64  `gorm:"column:id"`
		Name     string `gorm:"column:name"`
		Source   int16  `gorm:"column:source_id"`
		PersonID *int64 `gorm:"column:person_id"`
	}
	if err := db.Raw(`SELECT cn.id, normalize(cn.name, NFKC) AS name, cn.person_id, r.source_id
		FROM catalog_credit_name cn
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = cn.id
		     AND r.link_kind = ? AND r.matched_by LIKE 'rule:%-import'`,
		model.EntityTypeCreditName, model.LinkKindExact).Scan(&rows).Error; err != nil {
		return nil, nil, fmt.Errorf("load credit names: %w", err)
	}
	info := make(map[int64]cnInfo, len(rows))
	index := make(map[string][]struct {
		id     int64
		source int16
	})
	for _, r := range rows {
		f := foldName(r.Name)
		if f == "" {
			continue
		}
		info[r.ID] = cnInfo{fold: f, source: r.Source, personID: r.PersonID}
		index[f] = append(index[f], struct {
			id     int64
			source int16
		}{r.ID, r.Source})
	}
	return info, index, nil
}

func loadAliasDecls(db *gorm.DB) ([]aliasDecl, map[int64]map[string]bool, error) {
	var decls []aliasDecl
	ownerFolds := map[int64]map[string]bool{}
	add := func(owner int64, source int16, fold string) {
		if fold == "" {
			return
		}
		decls = append(decls, aliasDecl{owner: owner, source: source, fold: fold})
		if ownerFolds[owner] == nil {
			ownerFolds[owner] = map[string]bool{}
		}
		ownerFolds[owner][fold] = true
	}

	var brows []struct {
		Owner int64  `gorm:"column:owner"`
		Alias string `gorm:"column:alias"`
		Raw   string `gorm:"column:raw"`
	}
	if err := db.Raw(`SELECT r.entity_id AS owner, normalize(it->>'Value', NFKC) AS alias, it->>'Value' AS raw
		FROM catalog_external_ref r
		JOIN src_bangumi.person p ON p.id = CASE WHEN r.external_id ~ '^[0-9]+$' THEN r.external_id::bigint END
		CROSS JOIN LATERAL jsonb_array_elements(p.infobox_parsed->'Fields') f
		CROSS JOIN LATERAL jsonb_array_elements(f->'Items') it
		WHERE r.entity_type = ? AND r.source_id = ? AND r.matched_by = 'rule:bangumi-person-import'
		  AND p.parse_error = '' AND jsonb_typeof(p.infobox_parsed->'Fields') = 'array'
		  AND (f->>'Key') = '别名' AND jsonb_typeof(f->'Items') = 'array'
		  AND coalesce(trim(it->>'Value'), '') <> ''`,
		model.EntityTypeCreditName, sourceBangumi).Scan(&brows).Error; err != nil {
		return nil, nil, fmt.Errorf("load bangumi aliases: %w", err)
	}
	for _, r := range brows {
		if !isRoleTag(r.Raw) {
			add(r.Owner, sourceBangumi, foldName(r.Alias))
		}
	}

	var erows []struct {
		Owner int64  `gorm:"column:owner"`
		Name  string `gorm:"column:name"`
	}
	if err := db.Raw(`SELECT cn.id AS owner, normalize(cn.name, NFKC) AS name
		FROM catalog_credit_name cn
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = cn.id
		     AND r.source_id = ? AND r.matched_by = 'rule:eg-creater-import'
		WHERE cn.name ~ '[（(]'`, model.EntityTypeCreditName, sourceEG).Scan(&erows).Error; err != nil {
		return nil, nil, fmt.Errorf("load eg aliases: %w", err)
	}
	for _, r := range erows {
		for _, raw := range parenItemsRaw(r.Name) {
			if !isRoleTag(raw) {
				add(r.Owner, sourceEG, foldName(raw))
			}
		}
	}
	return decls, ownerFolds, nil
}

func loadCandidatePairs(db *gorm.DB) (map[[2]int64]struct{}, error) {
	var rows []struct {
		AID int64 `gorm:"column:a_id"`
		BID int64 `gorm:"column:b_id"`
	}
	if err := db.Raw(`SELECT a_id, b_id FROM catalog_match_candidate WHERE entity_type = ?`,
		model.EntityTypeCreditName).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load existing candidates: %w", err)
	}
	set := make(map[[2]int64]struct{}, len(rows))
	for _, r := range rows {
		set[[2]int64{r.AID, r.BID}] = struct{}{}
	}
	return set, nil
}
