package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

// pairMeta is one candidate pair with everything the emit phase needs. It is
// the durable per-pair artefact (pairs.jsonl) the verdicts join back onto.
type pairMeta struct {
	A        int64   `json:"a"`
	B        int64   `json:"b"`
	Tier     int     `json:"tier"` // 1 fold-equal, 2 VA bridge, 3 name-similar
	Works    []int64 `json:"works"`
	CV       string  `json:"cv,omitempty"` // bridging voice actor name (tier 2, or extra evidence)
	AName    string   `json:"a_name"`
	BName    string   `json:"b_name"`
	ASources []string `json:"a_sources"`
	BSources []string `json:"b_sources"`
	Instance bool     `json:"instance,omitempty"` // either side is a variant row — never auto
	// Survivor-ordering signals (the detector's pickCharSurvivor rule),
	// captured at packets time so emit needs no database.
	ARich richness `json:"a_rich"`
	BRich richness `json:"b_rich"`
}

// richness orders a group's survivor pick: portrait first (an id consumers may
// already render), then alias count, then the older id.
type richness struct {
	Img      bool `json:"img,omitempty"`
	NAliases int  `json:"na,omitempty"`
}

// charInfo is the per-character evidence loaded once for every id any pair
// touches.
type charInfo struct {
	ID       int64
	Name     string
	Lang     string
	Gender   *int16
	Instance bool
	HasImage bool
	Aliases  []string
	Anchors  []string // "vndb:c35682"
	Sources  []string // distinct source keys
	Intro    string
	NAnchors int
}

// buildPairs computes the wave's candidate pairs: live single-source
// characters co-resident on a work with a character of a DIFFERENT single
// source, tiered by evidence strength. Pairs matching no tier are dropped —
// most co-residents are simply two different characters of the same game.
func buildPairs(db *gorm.DB) ([]pairMeta, map[int64]*charInfo, error) {
	type rawPair struct {
		A     int64  `gorm:"column:a"`
		B     int64  `gorm:"column:b"`
		Works string `gorm:"column:works"`
	}
	var raws []rawPair
	// Sides pair on DISJOINT anchor source sets, not on being single-source:
	// the reported 硯川 pair is a bgm+eg-resolved row facing its vndb-only
	// twin. A shared source is the exclusion — that source itself keeps the
	// two rows apart, so they are its claim of distinctness.
	if err := db.Raw(`
		WITH anchored AS (
			SELECT er.entity_id AS id, array_agg(DISTINCT er.source_id) AS sids
			FROM catalog_external_ref er
			JOIN catalog_character c ON c.id = er.entity_id AND c.deleted_at IS NULL
			WHERE er.entity_type = 4
			GROUP BY er.entity_id
		)
		SELECT wa.character_id AS a, wb.character_id AS b,
		       string_agg(DISTINCT wa.work_id::text, ',') AS works
		FROM catalog_work_character wa
		JOIN catalog_work_character wb
		  ON wb.work_id = wa.work_id AND wb.character_id > wa.character_id
		JOIN anchored sa ON sa.id = wa.character_id
		JOIN anchored sb ON sb.id = wb.character_id AND NOT (sa.sids && sb.sids)
		GROUP BY 1, 2`).Scan(&raws).Error; err != nil {
		return nil, nil, fmt.Errorf("co-resident pairs: %w", err)
	}

	ids := map[int64]bool{}
	for _, r := range raws {
		ids[r.A], ids[r.B] = true, true
	}
	info, err := loadCharInfo(db, ids)
	if err != nil {
		return nil, nil, err
	}
	bridge, err := loadVABridges(db)
	if err != nil {
		return nil, nil, err
	}

	var pairs []pairMeta
	for _, r := range raws {
		a, b := info[r.A], info[r.B]
		if a == nil || b == nil {
			continue
		}
		aNames := append([]string{a.Name}, a.Aliases...)
		bNames := append([]string{b.Name}, b.Aliases...)
		cv := bridge[bridgeKey(r.A, r.B)]
		tier := 0
		switch {
		case foldedEqual(aNames, bNames):
			tier = 1
		case cv != "":
			tier = 2
		case namesSimilar(aNames, bNames):
			tier = 3
		default:
			continue
		}
		var works []int64
		for _, w := range strings.Split(r.Works, ",") {
			if id, err := strconv.ParseInt(w, 10, 64); err == nil {
				works = append(works, id)
			}
		}
		sort.Slice(works, func(i, j int) bool { return works[i] < works[j] })
		pairs = append(pairs, pairMeta{
			A: r.A, B: r.B, Tier: tier, Works: works, CV: cv,
			AName: a.Name, BName: b.Name, ASources: a.Sources, BSources: b.Sources,
			Instance: a.Instance || b.Instance,
			ARich:    richness{Img: a.HasImage, NAliases: len(a.Aliases)},
			BRich:    richness{Img: b.HasImage, NAliases: len(b.Aliases)},
		})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].A != pairs[j].A {
			return pairs[i].A < pairs[j].A
		}
		return pairs[i].B < pairs[j].B
	})
	return pairs, info, nil
}

func foldedEqual(aNames, bNames []string) bool {
	for _, an := range aNames {
		fa := foldName(an)
		if fa == "" {
			continue
		}
		for _, bn := range bNames {
			if fa == foldName(bn) {
				return true
			}
		}
	}
	return false
}

func bridgeKey(a, b int64) string { return strconv.FormatInt(a, 10) + "|" + strconv.FormatInt(b, 10) }

// loadVABridges finds every character pair voiced by the SAME credit name on
// the SAME work (a < b). The bridge is evidence, not proof: one actor voicing
// two roles in one game is routine, which is exactly what the character_cv
// prompt adjudicates.
func loadVABridges(db *gorm.DB) (map[string]string, error) {
	type row struct {
		A  int64  `gorm:"column:a"`
		B  int64  `gorm:"column:b"`
		CV string `gorm:"column:cv"`
	}
	var rows []row
	if err := db.Raw(`
		SELECT ka.character_id AS a, kb.character_id AS b, min(cn.name) AS cv
		FROM catalog_credit ka
		JOIN catalog_credit kb
		  ON kb.work_id = ka.work_id AND kb.credit_name_id = ka.credit_name_id
		 AND kb.role_id = 1 AND kb.character_id > ka.character_id
		JOIN catalog_credit_name cn ON cn.id = ka.credit_name_id
		WHERE ka.role_id = 1 AND ka.character_id IS NOT NULL AND kb.character_id IS NOT NULL
		GROUP BY 1, 2`).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("va bridges: %w", err)
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[bridgeKey(r.A, r.B)] = r.CV
	}
	return out, nil
}

// loadCharInfo loads names, aliases, anchors, the best intro and the ordering
// signals for every involved character, in id chunks (the 65,535-parameter
// lesson).
func loadCharInfo(db *gorm.DB, ids map[int64]bool) (map[int64]*charInfo, error) {
	all := make([]int64, 0, len(ids))
	for id := range ids {
		all = append(all, id)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	out := make(map[int64]*charInfo, len(all))

	for _, chunk := range chunks(all, 10000) {
		type crow struct {
			ID       int64
			Name     string `gorm:"column:display_name"`
			Lang     string
			Gender   *int16
			Instance *int64  `gorm:"column:instance_of"`
			Image    *string `gorm:"column:image_hash"`
		}
		var crows []crow
		if err := db.Raw(`SELECT id, display_name, lang, gender, instance_of, image_hash
			FROM catalog_character WHERE deleted_at IS NULL AND id IN ?`, chunk).Scan(&crows).Error; err != nil {
			return nil, fmt.Errorf("characters: %w", err)
		}
		for _, c := range crows {
			out[c.ID] = &charInfo{
				ID: c.ID, Name: c.Name, Lang: c.Lang, Gender: c.Gender,
				Instance: c.Instance != nil,
				HasImage: c.Image != nil && *c.Image != "",
			}
		}

		type arow struct {
			CharacterID int64
			Name        string
		}
		var arows []arow
		if err := db.Raw(`SELECT character_id, name FROM catalog_character_alias
			WHERE character_id IN ? ORDER BY character_id, id`, chunk).Scan(&arows).Error; err != nil {
			return nil, fmt.Errorf("aliases: %w", err)
		}
		for _, a := range arows {
			if c := out[a.CharacterID]; c != nil {
				c.Aliases = append(c.Aliases, a.Name)
			}
		}

		type erow struct {
			EntityID   int64
			Key        string
			ExternalID string
		}
		var erows []erow
		if err := db.Raw(`SELECT er.entity_id, s.key, er.external_id
			FROM catalog_external_ref er JOIN catalog_source s ON s.id = er.source_id
			WHERE er.entity_type = 4 AND er.entity_id IN ?
			ORDER BY er.entity_id, er.source_id, er.external_id`, chunk).Scan(&erows).Error; err != nil {
			return nil, fmt.Errorf("anchors: %w", err)
		}
		for _, e := range erows {
			if c := out[e.EntityID]; c != nil {
				c.Anchors = append(c.Anchors, e.Key+":"+e.ExternalID)
				if len(c.Sources) == 0 || c.Sources[len(c.Sources)-1] != e.Key {
					c.Sources = append(c.Sources, e.Key)
				}
				c.NAnchors++
			}
		}

		type irow struct {
			CharacterID int64
			Intro       string
		}
		var irows []irow
		if err := db.Raw(`SELECT DISTINCT ON (character_id) character_id, intro
			FROM catalog_character_intro WHERE character_id IN ?
			ORDER BY character_id, provenance,
			         CASE lang WHEN 'ja' THEN 0 WHEN 'zh-Hans' THEN 1 WHEN 'en' THEN 2 ELSE 3 END`,
			chunk).Scan(&irows).Error; err != nil {
			return nil, fmt.Errorf("intros: %w", err)
		}
		for _, i := range irows {
			if c := out[i.CharacterID]; c != nil {
				c.Intro = truncateRunes(i.Intro, 500)
			}
		}
	}
	return out, nil
}

func chunks(ids []int64, n int) [][]int64 {
	var out [][]int64
	for len(ids) > n {
		out = append(out, ids[:n])
		ids = ids[n:]
	}
	if len(ids) > 0 {
		out = append(out, ids)
	}
	return out
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
